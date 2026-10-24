use skrifa::instance::{LocationRef, Size};
use skrifa::outline::{DrawSettings, HintingInstance, OutlinePen};
use skrifa::{FontRef, GlyphId, MetadataProvider};

use super::Raster;
use super::accum::Accumulator;
use crate::GlyphKind;

/// `offset` moves the glyph right by a fraction of a pixel after hinting,
/// `skew` (degrees) is synthetic italic. no synthetic bold yet.
pub(crate) fn render(
    font: &FontRef,
    glyph: GlyphId,
    size: f32,
    location: LocationRef,
    hinting: Option<&HintingInstance>,
    offset: f32,
    skew: Option<f32>,
) -> Option<Raster> {
    let outline = font.outline_glyphs().get(glyph)?;
    let mut path = Path {
        shear: skew.map_or(0.0, |deg| deg.to_radians().tan()),
        offset,
        ..Path::default()
    };
    let settings = match hinting {
        Some(h) => DrawSettings::hinted(h, false),
        None => DrawSettings::unhinted(Size::new(size), location),
    };
    outline.draw(settings, &mut path).ok()?;
    let (x0, y0, x1, y1) = path.bounds()?;
    let (w, h) = ((x1 - x0) as usize, (y1 - y0) as usize);
    let mut acc = match outline.has_overlaps() {
        Some(true) => Accumulator::merged(w, h),
        _ => Accumulator::new(w, h),
    };
    path.replay(&mut acc, -x0 as f32, -y0 as f32);
    let mut data = vec![0; w * h];
    let mut ink = false;
    acc.finish(|i, c| {
        data[i] = (c * 255.0).round() as u8;
        ink |= data[i] != 0;
    });
    ink.then_some(Raster {
        left: x0,
        top: -y0,
        width: w as u32,
        height: h as u32,
        kind: GlyphKind::Mask,
        data,
    })
}

enum El {
    Move(f32, f32),
    Line(f32, f32),
    Quad(f32, f32, f32, f32),
    Cubic(f32, f32, f32, f32, f32, f32),
}

/// the outline in pixels, y down, sheared and shifted
#[derive(Default)]
struct Path {
    els: Vec<El>,
    shear: f32,
    offset: f32,
    min: (f32, f32),
    max: (f32, f32),
}

impl Path {
    fn point(&mut self, x: f32, y: f32) -> (f32, f32) {
        let p = (x + y * self.shear + self.offset, -y);
        if self.els.is_empty() {
            self.min = p;
            self.max = p;
        }
        self.min = (self.min.0.min(p.0), self.min.1.min(p.1));
        self.max = (self.max.0.max(p.0), self.max.1.max(p.1));
        p
    }

    /// whole pixels around every point, control points included
    fn bounds(&self) -> Option<(i32, i32, i32, i32)> {
        if self.els.is_empty() {
            return None;
        }
        let b = (
            self.min.0.floor() as i32,
            self.min.1.floor() as i32,
            self.max.0.ceil() as i32,
            self.max.1.ceil() as i32,
        );
        (b.2 > b.0 && b.3 > b.1).then_some(b)
    }

    fn replay(&self, acc: &mut Accumulator, dx: f32, dy: f32) {
        for el in &self.els {
            match *el {
                El::Move(x, y) => acc.move_to(x + dx, y + dy),
                El::Line(x, y) => acc.line_to(x + dx, y + dy),
                El::Quad(x1, y1, x, y) => acc.quad_to(x1 + dx, y1 + dy, x + dx, y + dy),
                El::Cubic(x1, y1, x2, y2, x, y) => {
                    acc.cubic_to(x1 + dx, y1 + dy, x2 + dx, y2 + dy, x + dx, y + dy)
                }
            }
        }
    }
}

impl OutlinePen for Path {
    fn move_to(&mut self, x: f32, y: f32) {
        let p = self.point(x, y);
        self.els.push(El::Move(p.0, p.1));
    }

    fn line_to(&mut self, x: f32, y: f32) {
        let p = self.point(x, y);
        self.els.push(El::Line(p.0, p.1));
    }

    fn quad_to(&mut self, cx0: f32, cy0: f32, x: f32, y: f32) {
        let c = self.point(cx0, cy0);
        let p = self.point(x, y);
        self.els.push(El::Quad(c.0, c.1, p.0, p.1));
    }

    fn curve_to(&mut self, cx0: f32, cy0: f32, cx1: f32, cy1: f32, x: f32, y: f32) {
        let c0 = self.point(cx0, cy0);
        let c1 = self.point(cx1, cy1);
        let p = self.point(x, y);
        self.els.push(El::Cubic(c0.0, c0.1, c1.0, c1.1, p.0, p.1));
    }

    fn close(&mut self) {}
}
