use std::ops::Range;
use std::sync::Arc;

use parley::{Alignment, AlignmentOptions, Layout, PositionedLayoutItem};
use what_core::PremulRgba;

use crate::key::{GlyphKey, SUBPIXEL_BINS};
use crate::system::FaceKey;
use crate::{Span, Style};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum GlyphKind {
    /// coverage, tinted with the glyph's colour
    Mask,
    /// emoji: drawn as is
    Color,
}

/// a glyph placed relative to its paragraph's top left, physical px
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct PlacedGlyph {
    pub key: GlyphKey,
    /// whole px; the subpixel part lives in the key
    pub x: i32,
    /// the baseline, whole px
    pub y: i32,
    pub color: PremulRgba,
    pub kind: GlyphKind,
}

/// one line of a paragraph, logical px from the paragraph's top left
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct LineInfo {
    pub top: f32,
    pub height: f32,
    pub baseline: f32,
    /// ink advance; an rtl line ends at the paragraph's right edge
    pub width: f32,
    pub rtl: bool,
}

/// shaped text. wrapping again is cheap, building it again is not.
pub struct Paragraph {
    pub(crate) layout: Layout<PremulRgba>,
    pub(crate) scale: f32,
    /// every face the shaper picked, with the system's number for it
    pub(crate) faces: Vec<(FaceKey, Face)>,
    pub(crate) placed: Vec<PlacedGlyph>,
    /// each placed glyph's x before snapping, layout units
    pub(crate) raw_x: Vec<f32>,
    /// which of `placed` each line owns
    pub(crate) line_glyphs: Vec<Range<usize>>,
    pub(crate) clamp: Option<Clamp>,
    /// what it was built from, for reshaping a cut line
    pub(crate) text: Arc<str>,
    pub(crate) base: Style,
    pub(crate) spans: Arc<[Span]>,
}

/// lines past `lines` are gone and the last one ends in an ellipsis
#[derive(Clone, Copy, Debug)]
pub(crate) struct Clamp {
    pub lines: usize,
    /// layout units: the last line's new advance and the paragraph's width
    pub last_width: f32,
    pub width: f32,
}

#[derive(Clone, Debug)]
pub(crate) struct Face {
    pub id: u16,
    /// per glyph; None for fonts without colour glyphs
    pub color: Option<Arc<[Ink]>>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Ink {
    Mask,
    Color,
    /// a colour glyph that also paints with the text's colour, so it's
    /// cached per colour
    Foreground,
}

impl Paragraph {
    /// break into lines no wider than `width` logical px; None keeps one
    /// line per hard break
    pub fn wrap(&mut self, width: Option<f32>) {
        self.layout.break_all_lines(width.map(|w| w * self.scale));
        self.layout
            .align(Alignment::Start, AlignmentOptions::default());
        self.clamp = None;
        self.place();
    }

    /// logical px
    pub fn width(&self) -> f32 {
        self.clamp.map_or(self.layout.width(), |c| c.width) / self.scale
    }

    /// logical px
    pub fn height(&self) -> f32 {
        match self.clamp {
            Some(c) => self
                .layout
                .get(c.lines - 1)
                .map_or(0.0, |l| l.metrics().block_max_coord / self.scale),
            None => self.layout.height() / self.scale,
        }
    }

    pub fn glyphs(&self) -> impl Iterator<Item = PlacedGlyph> + '_ {
        self.placed.iter().copied()
    }

    /// the glyphs to rasterize before drawing, repeats included
    pub fn keys(&self) -> impl Iterator<Item = GlyphKey> + '_ {
        self.placed.iter().map(|g| g.key)
    }

    pub fn lines(&self) -> impl Iterator<Item = LineInfo> + '_ {
        let n = self.clamp.map_or(usize::MAX, |c| c.lines);
        let rtl = self.layout.is_rtl();
        self.layout.lines().take(n).enumerate().map(move |(i, l)| {
            let m = l.metrics();
            let width = match self.clamp {
                Some(c) if i + 1 == c.lines => c.last_width,
                _ => m.advance,
            };
            LineInfo {
                top: m.block_min_coord / self.scale,
                height: (m.block_max_coord - m.block_min_coord) / self.scale,
                baseline: m.baseline.round() / self.scale,
                width: width / self.scale,
                rtl,
            }
        })
    }

    pub(crate) fn place(&mut self) {
        self.placed.clear();
        self.raw_x.clear();
        self.line_glyphs.clear();
        for li in 0..self.layout.len() {
            let start = self.placed.len();
            let line = self.layout.get(li).expect("in range");
            let m = line.metrics();
            // parley quantizes lines like blink, so this is whole already
            let baseline = m.baseline.round();
            let mut out = std::mem::take(&mut self.placed);
            let mut raw = std::mem::take(&mut self.raw_x);
            for item in line.items() {
                let PositionedLayoutItem::GlyphRun(run) = item else {
                    continue;
                };
                let key = FaceKey::of(run.run());
                let Some((_, face)) = self.faces.iter().find(|(k, _)| *k == key) else {
                    debug_assert!(false, "face missing from the paragraph's table");
                    continue;
                };
                let size = run.run().font_size();
                let color = run.style().brush;
                for g in run.positioned_glyphs() {
                    if let Some(p) =
                        place_glyph(face, g.id, size, color, g.x, baseline, g.y - m.baseline)
                    {
                        out.push(p);
                        raw.push(g.x);
                    }
                }
            }
            self.placed = out;
            self.raw_x = raw;
            self.line_glyphs.push(start..self.placed.len());
        }
    }
}

/// a shaped glyph at layout-unit `x` on whole-px `baseline`, `dy` off it
pub(crate) fn place_glyph(
    face: &Face,
    glyph: u32,
    size: f32,
    color: PremulRgba,
    x: f32,
    baseline: f32,
    dy: f32,
) -> Option<PlacedGlyph> {
    let id = u16::try_from(glyph).ok()?;
    let ink = face
        .color
        .as_ref()
        .and_then(|c| c.get(id as usize).copied())
        .unwrap_or(Ink::Mask);
    let is_color = ink != Ink::Mask;
    let (x, bin) = snap(x, is_color);
    Some(PlacedGlyph {
        key: GlyphKey::new(
            face.id,
            id,
            size,
            bin,
            (ink == Ink::Foreground).then(|| opaque(color)),
        ),
        x,
        y: (baseline + dy.round()) as i32,
        color,
        kind: if is_color {
            GlyphKind::Color
        } else {
            GlyphKind::Mask
        },
    })
}

/// webrender: x snaps to a quarter pixel, y to a whole one. colour glyphs
/// are bitmaps and only get whole pixels.
fn snap(x: f32, is_color: bool) -> (i32, u32) {
    if is_color {
        (x.round() as i32, 0)
    } else {
        let q = (x * SUBPIXEL_BINS as f32).round() as i32;
        (
            q.div_euclid(SUBPIXEL_BINS as i32),
            q.rem_euclid(SUBPIXEL_BINS as i32) as u32,
        )
    }
}

/// the same glyph placed at a new unsnapped x
pub(crate) fn moved(g: PlacedGlyph, raw: f32) -> PlacedGlyph {
    let (x, bin) = snap(raw, g.kind == GlyphKind::Color);
    PlacedGlyph {
        x,
        key: GlyphKey((g.key.0 & !(SUBPIXEL_BINS as u128 - 1)) | bin as u128),
        ..g
    }
}

/// the text colour as straight srgb bytes, at full opacity: the shader
/// applies the text's alpha to the whole glyph
fn opaque(c: PremulRgba) -> [u8; 4] {
    if c.a <= 0.0 {
        return [0, 0, 0, 255];
    }
    let byte = |v: f32| ((v / c.a).clamp(0.0, 1.0) * 255.0).round() as u8;
    [byte(c.r), byte(c.g), byte(c.b), 255]
}
