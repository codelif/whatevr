//! vector icons (ticks, chevrons) drawn through the glyph rasterizer, so
//! they land in the glyph atlas and tint like text.

use std::f32::consts::TAU;

use crate::raster::Raster;
use crate::raster::accum::Accumulator;
use crate::{GlyphKey, GlyphKind, TextSystem};

#[derive(Clone, Debug, PartialEq)]
enum El {
    Move(f32, f32),
    Line(f32, f32),
    Quad(f32, f32, f32, f32),
    Cubic(f32, f32, f32, f32, f32, f32),
    /// polyline with round caps and joins
    Stroke(Vec<[f32; 2]>, f32),
}

/// in its own units, y down; [`TextSystem::rasterize_icon`] scales a unit
/// to some px. fills are nonzero, everything unions.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Icon {
    els: Vec<El>,
}

#[derive(Debug, Default)]
pub struct IconBuilder {
    icon: Icon,
}

impl Icon {
    pub fn builder() -> IconBuilder {
        IconBuilder::default()
    }
}

impl IconBuilder {
    pub fn move_to(&mut self, x: f32, y: f32) -> &mut Self {
        self.icon.els.push(El::Move(x, y));
        self
    }

    pub fn line_to(&mut self, x: f32, y: f32) -> &mut Self {
        self.icon.els.push(El::Line(x, y));
        self
    }

    pub fn quad_to(&mut self, cx: f32, cy: f32, x: f32, y: f32) -> &mut Self {
        self.icon.els.push(El::Quad(cx, cy, x, y));
        self
    }

    pub fn cubic_to(&mut self, c0: [f32; 2], c1: [f32; 2], x: f32, y: f32) -> &mut Self {
        self.icon
            .els
            .push(El::Cubic(c0[0], c0[1], c1[0], c1[1], x, y));
        self
    }

    /// fill contours close themselves; this only marks intent
    pub fn close(&mut self) -> &mut Self {
        self
    }

    pub fn stroke(&mut self, points: &[[f32; 2]], width: f32) -> &mut Self {
        self.icon.els.push(El::Stroke(points.to_vec(), width));
        self
    }

    pub fn build(&mut self) -> Icon {
        std::mem::take(&mut self.icon)
    }
}

impl GlyphKey {
    /// an icon in the glyph atlas: `id` is the caller's number for it,
    /// `size` its px per unit. lives under face id 0xffff, which text never uses.
    pub fn icon(id: u16, size: f32) -> GlyphKey {
        GlyphKey::new(0xffff, id, size, 0, None)
    }
}

impl TextSystem {
    /// coverage mask, origin at the icon's (0, 0); `top` counts up from there
    /// like a glyph's baseline. None if it draws nothing.
    pub fn rasterize_icon(&self, icon: &Icon, size: f32) -> Option<Raster> {
        let polys = flatten(icon, size);
        let (mut x0, mut y0, mut x1, mut y1) = (f32::MAX, f32::MAX, f32::MIN, f32::MIN);
        for p in polys.iter().flatten() {
            x0 = x0.min(p[0]);
            y0 = y0.min(p[1]);
            x1 = x1.max(p[0]);
            y1 = y1.max(p[1]);
        }
        if x1 <= x0 || y1 <= y0 {
            return None;
        }
        let (bx, by) = (x0.floor() as i32, y0.floor() as i32);
        let (w, h) = (
            (x1.ceil() as i32 - bx) as usize,
            (y1.ceil() as i32 - by) as usize,
        );
        let mut acc = Accumulator::merged(w, h);
        for poly in &polys {
            let Some((first, rest)) = poly.split_first() else {
                continue;
            };
            acc.move_to(first[0] - bx as f32, first[1] - by as f32);
            for p in rest {
                acc.line_to(p[0] - bx as f32, p[1] - by as f32);
            }
        }
        let mut data = vec![0; w * h];
        acc.finish(|i, c| data[i] = (c * 255.0).round() as u8);
        Some(Raster {
            left: bx,
            top: -by,
            width: w as u32,
            height: h as u32,
            kind: GlyphKind::Mask,
            data,
        })
    }
}

/// every element as closed polygons in px, all wound the same way so the
/// nonzero union never cuts holes where they overlap
fn flatten(icon: &Icon, s: f32) -> Vec<Vec<[f32; 2]>> {
    let mut out: Vec<Vec<[f32; 2]>> = Vec::new();
    let mut cur: Vec<[f32; 2]> = Vec::new();
    let flush = |cur: &mut Vec<[f32; 2]>, out: &mut Vec<Vec<[f32; 2]>>| {
        if cur.len() > 2 {
            out.push(std::mem::take(cur));
        }
        cur.clear();
    };
    let tol = |a: [f32; 2], b: [f32; 2], c: [f32; 2]| {
        let dd = (a[0] - 2.0 * b[0] + c[0]).hypot(a[1] - 2.0 * b[1] + c[1]);
        1 + (3.0 * dd * dd).sqrt().sqrt() as usize
    };
    for el in &icon.els {
        match el {
            El::Move(x, y) => {
                flush(&mut cur, &mut out);
                cur.push([x * s, y * s]);
            }
            El::Line(x, y) => cur.push([x * s, y * s]),
            El::Quad(cx, cy, x, y) => {
                let p0 = *cur.last().unwrap_or(&[0.0, 0.0]);
                let (c, p) = ([cx * s, cy * s], [x * s, y * s]);
                let n = tol(p0, c, p);
                for i in 1..=n {
                    let t = i as f32 / n as f32;
                    let m = 1.0 - t;
                    cur.push([
                        m * m * p0[0] + 2.0 * m * t * c[0] + t * t * p[0],
                        m * m * p0[1] + 2.0 * m * t * c[1] + t * t * p[1],
                    ]);
                }
            }
            El::Cubic(ax, ay, bx, by, x, y) => {
                let p0 = *cur.last().unwrap_or(&[0.0, 0.0]);
                let (a, b, p) = ([ax * s, ay * s], [bx * s, by * s], [x * s, y * s]);
                let n = tol(p0, a, b).max(tol(a, b, p)) * 3 / 2;
                for i in 1..=n {
                    let t = i as f32 / n as f32;
                    let m = 1.0 - t;
                    let k = [m * m * m, 3.0 * m * m * t, 3.0 * m * t * t, t * t * t];
                    cur.push([
                        k[0] * p0[0] + k[1] * a[0] + k[2] * b[0] + k[3] * p[0],
                        k[0] * p0[1] + k[1] * a[1] + k[2] * b[1] + k[3] * p[1],
                    ]);
                }
            }
            El::Stroke(points, width) => {
                flush(&mut cur, &mut out);
                let r = width * s / 2.0;
                let pts: Vec<[f32; 2]> = points.iter().map(|p| [p[0] * s, p[1] * s]).collect();
                for w in pts.windows(2) {
                    let (a, b) = (w[0], w[1]);
                    let (dx, dy) = (b[0] - a[0], b[1] - a[1]);
                    let len = dx.hypot(dy);
                    if len > 0.0 {
                        let (nx, ny) = (-dy / len * r, dx / len * r);
                        out.push(vec![
                            [a[0] + nx, a[1] + ny],
                            [b[0] + nx, b[1] + ny],
                            [b[0] - nx, b[1] - ny],
                            [a[0] - nx, a[1] - ny],
                        ]);
                    }
                }
                // round caps and joins: a disc on every point
                let n = ((TAU * r / 0.5).ceil() as usize).max(8);
                for p in &pts {
                    out.push(
                        (0..n)
                            .map(|i| {
                                let a = i as f32 / n as f32 * TAU;
                                [p[0] + r * a.cos(), p[1] + r * a.sin()]
                            })
                            .collect(),
                    );
                }
            }
        }
    }
    flush(&mut cur, &mut out);
    for p in &mut out {
        let area: f32 = (0..p.len())
            .map(|i| {
                let (a, b) = (p[i], p[(i + 1) % p.len()]);
                a[0] * b[1] - b[0] * a[1]
            })
            .sum();
        if area < 0.0 {
            p.reverse();
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn stroke_covers_its_width() {
        let ts = TextSystem::with_fonts(&[]).unwrap();
        let icon = Icon::builder()
            .stroke(&[[1.0, 5.0], [9.0, 5.0]], 2.0)
            .build();
        let r = ts.rasterize_icon(&icon, 2.0).unwrap();
        // 2 units wide at 2 px each, plus round caps
        assert_eq!((r.left, r.top, r.height), (0, -8, 4));
        let mid = (r.height / 2) as usize * r.width as usize + r.width as usize / 2;
        assert_eq!(r.data[mid], 255);
        // the caps overlap the body without holes
        let row = &r.data[mid - r.width as usize / 2..][..r.width as usize];
        assert!(row[2..row.len() - 2].iter().all(|&c| c == 255), "{row:?}");
    }

    #[test]
    fn icon_keys_stay_off_text_faces() {
        let k = GlyphKey::icon(3, 2.0);
        assert_eq!((k.font(), k.glyph(), k.size()), (0xffff, 3, 2.0));
    }
}
