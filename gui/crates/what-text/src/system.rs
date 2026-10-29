use std::collections::HashMap;
use std::path::Path;
use std::sync::{Arc, Mutex};

use parley::fontique::{Blob, Collection, CollectionOptions, SourceCache};
use parley::{
    FontContext, FontFamily, FontFamilyName, FontStyle, FontWeight, GenericFamily, LayoutContext,
    LineHeight, PositionedLayoutItem, Run, StyleProperty,
};
use skrifa::bitmap::{BitmapFormat, BitmapStrikes};
use skrifa::color::ColorGlyphCollection;
use skrifa::instance::{LocationRef, NormalizedCoord, Size};
use skrifa::outline::{Engine, HintingInstance, HintingOptions, SmoothMode};
use skrifa::raw::TableProvider;
use skrifa::{FontRef, GlyphId, MetadataProvider};
use what_core::{PremulRgba, Scale};

use crate::emoji;
use crate::paragraph::{Face, Ink};
use crate::raster::{self, Raster};
use crate::{Error, Family, GlyphKey, Paragraph, Span, Style};

/// fonts, shaping scratch and the hinting state. one per thread that lays
/// out text; glyph keys mean the same to a system and its siblings, and
/// nothing to anyone else.
pub struct TextSystem {
    pub(crate) fcx: FontContext,
    lcx: LayoutContext<PremulRgba>,
    registry: Arc<Mutex<Registry>>,
    /// this thread's copy of the registry, filled on first use of each face
    faces: Vec<Option<Arc<FaceData>>>,
    face_ids: HashMap<FaceKey, u16>,
    /// (face, size in 1/64 px)
    hinting: HashMap<(u16, u32), Option<HintingInstance>>,
    /// the last ellipsis shaped, by style and scale
    pub(crate) ellipsis: Option<(Style, Scale, Paragraph)>,
}

/// every face any sibling has seen, numbered in order of first sight
#[derive(Default)]
struct Registry {
    faces: Vec<Arc<FaceData>>,
    ids: HashMap<FaceKey, u16>,
}

/// face ids stop here; 0xffff is the icons' (see [`GlyphKey::icon`])
const MAX_FACES: usize = 0xffff;

/// what tells two shaped runs' fonts apart
#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub(crate) struct FaceKey {
    blob: u64,
    index: u32,
    coords: Vec<i16>,
    skew: Option<u32>,
    embolden: bool,
}

impl FaceKey {
    pub(crate) fn of(run: &Run<'_, PremulRgba>) -> Self {
        let font = run.font();
        let synthesis = run.synthesis();
        Self {
            blob: font.data.id(),
            index: font.index,
            coords: run
                .normalized_coords()
                .iter()
                .map(|c| c.to_bits())
                .collect(),
            skew: synthesis.skew().map(f32::to_bits),
            embolden: synthesis.embolden(),
        }
    }
}

struct FaceData {
    blob: Blob<u8>,
    index: u32,
    coords: Vec<NormalizedCoord>,
    skew: Option<f32>,
    color: Option<Arc<[Ink]>>,
}

/// past this many sizes the hinting cache starts over
const HINTING_CACHE: usize = 256;

impl TextSystem {
    /// system fonts and fallback through fontconfig
    pub fn new() -> Self {
        // shared so fonts added later reach siblings too
        Self::with_context(FontContext {
            collection: Collection::new(CollectionOptions {
                shared: true,
                system_fonts: true,
            }),
            source_cache: SourceCache::new_shared(),
        })
    }

    /// only these font files, no system fonts: for tests and comparisons.
    /// every generic family falls back through them in this order.
    pub fn with_fonts(files: &[&Path]) -> Result<Self, Error> {
        let mut collection = Collection::new(CollectionOptions {
            shared: true,
            system_fonts: false,
        });
        let mut families = Vec::new();
        for path in files {
            let bytes = std::fs::read(path).map_err(|e| Error::Read(path.to_path_buf(), e))?;
            let got = collection.register_fonts(Blob::from(bytes), None);
            if got.is_empty() {
                return Err(Error::NotAFont(path.to_path_buf()));
            }
            for (family, _) in got {
                if !families.contains(&family) {
                    families.push(family);
                }
            }
        }
        for g in [
            GenericFamily::SansSerif,
            GenericFamily::Serif,
            GenericFamily::Monospace,
            GenericFamily::SystemUi,
            GenericFamily::Emoji,
        ] {
            collection.set_generic_families(g, families.iter().copied());
        }
        Ok(Self::with_context(FontContext {
            collection,
            source_cache: SourceCache::new_shared(),
        }))
    }

    fn with_context(fcx: FontContext) -> Self {
        Self {
            fcx,
            lcx: LayoutContext::new(),
            registry: Arc::default(),
            faces: Vec::new(),
            face_ids: HashMap::new(),
            hinting: HashMap::new(),
            ellipsis: None,
        }
    }

    /// another system for another thread: same fonts, same face numbering,
    /// its own shaping scratch and hinting cache
    pub fn sibling(&self) -> TextSystem {
        Self {
            fcx: self.fcx.clone(),
            lcx: LayoutContext::new(),
            registry: self.registry.clone(),
            faces: Vec::new(),
            face_ids: HashMap::new(),
            hinting: HashMap::new(),
            ellipsis: None,
        }
    }

    /// a font compiled into the binary; system fonts still back it up per
    /// character. returns the families it brought.
    pub fn add_font(&mut self, data: &'static [u8]) -> Vec<String> {
        let got = self
            .fcx
            .collection
            .register_fonts(Blob::new(Arc::new(data)), None);
        let mut names: Vec<String> = Vec::new();
        for (family, _) in got {
            if let Some(n) = self.fcx.collection.family_name(family)
                && !names.iter().any(|m| m == n)
            {
                names.push(n.to_owned());
            }
        }
        names
    }

    /// shape `text`, laid out on one line per hard break; call
    /// [`Paragraph::wrap`] to break it to a width
    pub fn paragraph(
        &mut self,
        text: &str,
        base: &Style,
        spans: &[Span],
        scale: Scale,
    ) -> Paragraph {
        let scale = scale.as_f32();
        // quantize: whole device px ascent, descent and line tops, floored
        // half leading; lands where chromium does
        let mut b = self.lcx.ranged_builder(&mut self.fcx, text, scale, true);
        b.push_default(StyleProperty::FontFamily(family(&base.family)));
        b.push_default(StyleProperty::FontSize(base.size));
        b.push_default(StyleProperty::FontWeight(FontWeight::new(base.weight)));
        b.push_default(StyleProperty::FontStyle(style(base.italic)));
        b.push_default(StyleProperty::Brush(base.color));
        b.push_default(StyleProperty::LineHeight(LineHeight::FontSizeRelative(
            base.line_height,
        )));
        for s in spans {
            let r = s.range.clone();
            debug_assert!(text.is_char_boundary(r.start) && text.is_char_boundary(r.end));
            if let Some(f) = &s.family {
                b.push(StyleProperty::FontFamily(family(f)), r.clone());
            }
            if let Some(v) = s.size {
                b.push(StyleProperty::FontSize(v), r.clone());
            }
            if let Some(v) = s.weight {
                b.push(StyleProperty::FontWeight(FontWeight::new(v)), r.clone());
            }
            if let Some(v) = s.italic {
                b.push(StyleProperty::FontStyle(style(v)), r.clone());
            }
            if let Some(v) = s.color {
                b.push(StyleProperty::Brush(v), r.clone());
            }
        }
        for r in emoji::ranges(text) {
            let own = spans
                .iter()
                .rev()
                .find(|s| s.family.is_some() && s.range.contains(&r.start))
                .and_then(|s| s.family.as_ref())
                .unwrap_or(&base.family);
            let mut list = vec![FontFamilyName::Generic(GenericFamily::Emoji)];
            if *own != Family::Emoji {
                list.push(family_name(own));
            }
            b.push(StyleProperty::FontFamily(FontFamily::List(list.into())), r);
        }
        let mut layout = b.build(text);

        // fonts are picked while shaping, so one unbroken pass sees them all
        layout.break_all_lines(None);
        let mut faces: Vec<(FaceKey, Face)> = Vec::new();
        for line in layout.lines() {
            for item in line.items() {
                let PositionedLayoutItem::GlyphRun(run) = item else {
                    continue;
                };
                let key = FaceKey::of(run.run());
                if faces.iter().any(|(k, _)| *k == key) {
                    continue;
                }
                let face = self.intern(key.clone(), run.run());
                faces.push((key, face));
            }
        }
        let mut p = Paragraph {
            layout,
            scale,
            faces,
            placed: Vec::new(),
            raw_x: Vec::new(),
            line_glyphs: Vec::new(),
            clamp: None,
            text: text.into(),
            base: base.clone(),
            spans: spans.into(),
        };
        p.wrap(None);
        p
    }

    /// None for glyphs with no ink, like a space
    pub fn rasterize(&mut self, key: GlyphKey) -> Option<Raster> {
        let face_id = key.font();
        let face = self.face(face_id)?;
        let font = FontRef::from_index(face.blob.as_ref(), face.index).ok()?;
        let glyph = GlyphId::new(key.glyph() as u32);
        let size = key.size();
        let location = LocationRef::new(&face.coords);
        let is_color = face
            .color
            .as_ref()
            .is_some_and(|c| c.get(key.glyph() as usize).is_some_and(|i| *i != Ink::Mask));
        if is_color {
            if let Some(r) = raster::colr::render(&font, glyph, size, location, key.color()) {
                return Some(r);
            }
            if let Some(r) = raster::bitmap::render(&font, glyph, size) {
                return Some(r);
            }
            // a paint graph that draws nothing is empty, not its outline
            if raster::colr::has(&font, glyph) {
                return None;
            }
        }
        if self.hinting.len() >= HINTING_CACHE {
            self.hinting.clear();
        }
        let hinting = self
            .hinting
            .entry((face_id, (size * 64.0).round() as u32))
            .or_insert_with(|| {
                let options = HintingOptions {
                    engine: Engine::Auto(None),
                    target: SmoothMode::Light.into(),
                };
                HintingInstance::new(&font.outline_glyphs(), Size::new(size), location, options)
                    .ok()
            });
        raster::outline::render(
            &font,
            glyph,
            size,
            location,
            hinting.as_ref(),
            key.offset(),
            face.skew,
        )
    }

    /// the face behind an id, from the local copy or, once, the registry
    fn face(&mut self, id: u16) -> Option<Arc<FaceData>> {
        let i = id as usize;
        if let Some(Some(f)) = self.faces.get(i) {
            return Some(f.clone());
        }
        let f = self.registry.lock().ok()?.faces.get(i)?.clone();
        if self.faces.len() <= i {
            self.faces.resize(i + 1, None);
        }
        self.faces[i] = Some(f.clone());
        Some(f)
    }

    fn intern(&mut self, key: FaceKey, run: &Run<'_, PremulRgba>) -> Face {
        if let Some(&id) = self.face_ids.get(&key)
            && let Some(f) = self.face(id)
        {
            return Face {
                id,
                color: f.color.clone(),
            };
        }
        let mut reg = self.registry.lock().unwrap_or_else(|e| e.into_inner());
        let id = match reg.ids.get(&key) {
            Some(&id) => id,
            None => {
                let font = run.font();
                let color = FontRef::from_index(font.data.as_ref(), font.index)
                    .ok()
                    .and_then(|f| color_table(&f));
                let face = Arc::new(FaceData {
                    blob: font.data.clone(),
                    index: font.index,
                    coords: run
                        .normalized_coords()
                        .iter()
                        .map(|c| NormalizedCoord::from_bits(c.to_bits()))
                        .collect(),
                    skew: run.synthesis().skew(),
                    color,
                });
                // past MAX_FACES new ones share the last id; nothing real gets there
                if reg.faces.len() >= MAX_FACES {
                    (MAX_FACES - 1) as u16
                } else {
                    let id = reg.faces.len() as u16;
                    reg.faces.push(face);
                    reg.ids.insert(key.clone(), id);
                    id
                }
            }
        };
        let face = reg.faces[id as usize].clone();
        drop(reg);
        self.face_ids.insert(key, id);
        let color = face.color.clone();
        if self.faces.len() <= id as usize {
            self.faces.resize(id as usize + 1, None);
        }
        self.faces[id as usize] = Some(face);
        Face { id, color }
    }
}

impl Default for TextSystem {
    fn default() -> Self {
        Self::new()
    }
}

/// which glyphs of a font are colour ones; None when none are
fn color_table(font: &FontRef) -> Option<Arc<[Ink]>> {
    let colr = font.colr().is_ok().then(|| ColorGlyphCollection::new(font));
    let strikes = BitmapStrikes::new(font);
    let bitmaps = matches!(
        strikes.format(),
        Some(BitmapFormat::Cbdt | BitmapFormat::Sbix)
    );
    if colr.is_none() && !bitmaps {
        return None;
    }
    let n = font.maxp().ok()?.num_glyphs();
    let table: Arc<[Ink]> = (0..n as u32)
        .map(|g| {
            let g = GlyphId::new(g);
            match colr.as_ref().and_then(|c| c.get(g)) {
                Some(c) if raster::colr::uses_foreground(&c) => Ink::Foreground,
                Some(_) => Ink::Color,
                None if bitmaps && strikes.iter().any(|s| s.get(g).is_some()) => Ink::Color,
                None => Ink::Mask,
            }
        })
        .collect();
    table.iter().any(|i| *i != Ink::Mask).then_some(table)
}

fn family(f: &Family) -> FontFamily<'_> {
    FontFamily::Single(family_name(f))
}

fn family_name(f: &Family) -> FontFamilyName<'_> {
    match f {
        Family::SansSerif => FontFamilyName::Generic(GenericFamily::SansSerif),
        Family::Serif => FontFamilyName::Generic(GenericFamily::Serif),
        Family::Monospace => FontFamilyName::Generic(GenericFamily::Monospace),
        Family::Emoji => FontFamilyName::Generic(GenericFamily::Emoji),
        Family::Named(name) => FontFamilyName::named(name),
    }
}

fn style(italic: bool) -> FontStyle {
    if italic {
        FontStyle::Italic
    } else {
        FontStyle::Normal
    }
}

#[cfg(test)]
mod tests {
    use std::path::Path;

    use what_core::Srgba8;

    use super::*;

    fn colr() -> TextSystem {
        let path =
            Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fonts/test_glyphs-glyf_colr_1.ttf");
        TextSystem::with_fonts(&[&path]).unwrap()
    }

    fn key(ts: &mut TextSystem, text: &str, rgb: u32) -> GlyphKey {
        let style = Style {
            size: 40.0,
            color: Srgba8::hex(rgb).premultiply(),
            ..Style::default()
        };
        let p = ts.paragraph(text, &style, &[], Scale::from_120ths(120));
        p.glyphs().next().unwrap().key
    }

    #[test]
    fn only_foreground_glyphs_key_on_the_text_colour() {
        let mut ts = colr();
        // foreground_color_linear_alpha_1
        let (dark, light) = (
            key(&mut ts, "\u{f0b00}", 0x111b21),
            key(&mut ts, "\u{f0b00}", 0xe9edef),
        );
        assert_eq!(dark.color(), Some([0x11, 0x1b, 0x21, 0xff]));
        assert_ne!(dark, light);
        assert_ne!(
            ts.rasterize(dark).unwrap().data,
            ts.rasterize(light).unwrap().data
        );
        // a plain linear gradient
        let (a, b) = (
            key(&mut ts, "\u{f0100}", 0x111b21),
            key(&mut ts, "\u{f0100}", 0xe9edef),
        );
        assert_eq!((a, a.color()), (b, None));
    }
}
