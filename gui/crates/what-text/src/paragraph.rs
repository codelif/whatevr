use std::sync::Arc;

use parley::{Alignment, AlignmentOptions, Layout, PositionedLayoutItem};
use what_core::PremulRgba;

use crate::key::{GlyphKey, SUBPIXEL_BINS};
use crate::system::FaceKey;

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

/// shaped text. wrapping again is cheap, building it again is not.
pub struct Paragraph {
    pub(crate) layout: Layout<PremulRgba>,
    pub(crate) scale: f32,
    /// every face the shaper picked, with the system's number for it
    pub(crate) faces: Vec<(FaceKey, Face)>,
    pub(crate) placed: Vec<PlacedGlyph>,
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
        self.place();
    }

    /// logical px
    pub fn width(&self) -> f32 {
        self.layout.width() / self.scale
    }

    /// logical px
    pub fn height(&self) -> f32 {
        self.layout.height() / self.scale
    }

    pub fn glyphs(&self) -> impl Iterator<Item = PlacedGlyph> + '_ {
        self.placed.iter().copied()
    }

    fn place(&mut self) {
        self.placed.clear();
        for line in self.layout.lines() {
            let m = line.metrics();
            // parley quantizes lines like blink, so this is whole already
            let baseline = m.baseline.round();
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
                    let Ok(id) = u16::try_from(g.id) else {
                        continue;
                    };
                    let ink = face
                        .color
                        .as_ref()
                        .and_then(|c| c.get(id as usize).copied())
                        .unwrap_or(Ink::Mask);
                    let is_color = ink != Ink::Mask;
                    // webrender: x snaps to a quarter pixel, y to a whole one.
                    // colour glyphs are bitmaps and only get whole pixels.
                    let (x, bin) = if is_color {
                        (g.x.round() as i32, 0)
                    } else {
                        let q = (g.x * SUBPIXEL_BINS as f32).round() as i32;
                        (
                            q.div_euclid(SUBPIXEL_BINS as i32),
                            q.rem_euclid(SUBPIXEL_BINS as i32),
                        )
                    };
                    self.placed.push(PlacedGlyph {
                        key: GlyphKey::new(
                            face.id,
                            id,
                            size,
                            bin as u32,
                            (ink == Ink::Foreground).then(|| opaque(color)),
                        ),
                        x,
                        y: (baseline + (g.y - m.baseline).round()) as i32,
                        color,
                        kind: if is_color {
                            GlyphKind::Color
                        } else {
                            GlyphKind::Mask
                        },
                    });
                }
            }
        }
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
