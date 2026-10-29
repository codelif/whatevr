//! a laid-out list row: generic parts in physical px from the row's top
//! left, built for one width and scale.

use what_core::PremulRgba;
use what_render::{Clip, Glyph, Quad, Rect, Scene, Shadow};
use what_text::{GlyphKey, Paragraph, Raster};

// text parts dwarf the rest; boxing them would cost an alloc each for nothing
#[allow(clippy::large_enum_variant)]
pub enum Part {
    Quad(Quad),
    Shadow(Shadow),
    /// glyph positions are the paragraph's own, offset by (x, y)
    Text {
        x: i32,
        y: i32,
        paragraph: Paragraph,
    },
    /// an icon from the glyph atlas, its origin at (x, y)
    Icon {
        x: i32,
        y: i32,
        key: GlyphKey,
        color: PremulRgba,
    },
}

/// whole-px height so rows stack exactly
pub struct Row {
    pub height: u32,
    pub parts: Vec<Part>,
    /// glyph and icon bitmaps the builder made first, for the ui to upload
    pub rasters: Vec<(GlyphKey, Raster)>,
}

impl Row {
    /// rough heap size, for the layout cache's budget
    pub fn bytes(&self) -> usize {
        let mut n = std::mem::size_of::<Self>() + self.parts.len() * std::mem::size_of::<Part>();
        for p in &self.parts {
            if let Part::Text { paragraph, .. } = p {
                // shaped data runs a few times the placed glyphs
                n += 512 + paragraph.glyphs().count() * 96;
            }
        }
        n + self
            .rasters
            .iter()
            .map(|(_, r)| r.data.len() + 64)
            .sum::<usize>()
    }

    /// everything the row draws, its top left at (x, y), cut to `clip`
    pub fn draw(&self, scene: &mut Scene, x: i32, y: i32, clip: Clip) {
        let (fx, fy) = (x as f32, y as f32);
        let mv = |r: Rect| Rect::new(r.x + fx, r.y + fy, r.w, r.h);
        let clip_of = |own: Clip| {
            if own == Clip::NONE {
                clip
            } else {
                Clip {
                    rect: intersect(mv(own.rect), clip.rect),
                    radii: own.radii,
                }
            }
        };
        for p in &self.parts {
            match p {
                Part::Quad(q) => scene.quad(Quad {
                    rect: mv(q.rect),
                    clip: clip_of(q.clip),
                    ..*q
                }),
                Part::Shadow(s) => scene.shadow(Shadow {
                    rect: mv(s.rect),
                    clip: clip_of(s.clip),
                    ..*s
                }),
                Part::Text {
                    x: tx,
                    y: ty,
                    paragraph,
                } => {
                    for g in paragraph.glyphs() {
                        scene.glyph(Glyph {
                            x: x + tx + g.x,
                            y: y + ty + g.y,
                            key: g.key.0,
                            color: g.color,
                            clip,
                        });
                    }
                }
                Part::Icon {
                    x: ix,
                    y: iy,
                    key,
                    color,
                } => scene.glyph(Glyph {
                    x: x + ix,
                    y: y + iy,
                    key: key.0,
                    color: *color,
                    clip,
                }),
            }
        }
    }

    /// every glyph key the row draws
    pub fn keys(&self) -> impl Iterator<Item = GlyphKey> + '_ {
        self.parts
            .iter()
            .flat_map(|p| -> Box<dyn Iterator<Item = GlyphKey> + '_> {
                match p {
                    Part::Text { paragraph, .. } => Box::new(paragraph.keys()),
                    Part::Icon { key, .. } => Box::new(std::iter::once(*key)),
                    _ => Box::new(std::iter::empty()),
                }
            })
    }
}

fn intersect(a: Rect, b: Rect) -> Rect {
    let x0 = a.x.max(b.x);
    let y0 = a.y.max(b.y);
    let x1 = (a.x + a.w).min(b.x + b.w);
    let y1 = (a.y + a.h).min(b.y + b.h);
    Rect::new(x0, y0, (x1 - x0).max(0.0), (y1 - y0).max(0.0))
}
