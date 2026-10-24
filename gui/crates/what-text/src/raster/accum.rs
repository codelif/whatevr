//! dense float accumulation rasterizer (font-rs style). every line adds its
//! signed area into the cells it crosses, a running sum along each row turns
//! that into coverage. nonzero-ish: |sum| clamped to 1, like FreeType.
//! that sum lets opposite-wound contours cancel where they meet inside a
//! pixel, so `merged` unions the contours first.

use i_overlay::core::fill_rule::FillRule;
use i_overlay::float::simplify::SimplifyShape;

/// pixel space, y down, origin at the top left of the bitmap
pub(crate) struct Accumulator {
    width: usize,
    height: usize,
    /// one spare cell per row: lines ending on the right edge spill into it
    cells: Vec<f32>,
    start: (f32, f32),
    pen: (f32, f32),
    /// merged mode: flattened contours, accumulated in `finish`
    contours: Option<Vec<Vec<[f32; 2]>>>,
}

impl Accumulator {
    pub fn new(width: usize, height: usize) -> Self {
        Self {
            width,
            height,
            cells: vec![0.0; (width + 2) * height],
            start: (0.0, 0.0),
            pen: (0.0, 0.0),
            contours: None,
        }
    }

    /// overlaps and opposite windings resolved exactly with a nonzero union
    pub fn merged(width: usize, height: usize) -> Self {
        Self {
            contours: Some(Vec::new()),
            ..Self::new(width, height)
        }
    }

    pub fn move_to(&mut self, x: f32, y: f32) {
        self.close();
        self.start = (x, y);
        self.pen = (x, y);
        if let Some(c) = &mut self.contours {
            c.push(vec![[x, y]]);
        }
    }

    pub fn line_to(&mut self, x: f32, y: f32) {
        match self.contours.as_mut().and_then(|c| c.last_mut()) {
            Some(c) => c.push([x, y]),
            None => {
                let (x0, y0) = self.pen;
                self.line(x0, y0, x, y);
            }
        }
        self.pen = (x, y);
    }

    pub fn quad_to(&mut self, x1: f32, y1: f32, x2: f32, y2: f32) {
        let (x0, y0) = self.pen;
        let dd = (x0 - 2.0 * x1 + x2).hypot(y0 - 2.0 * y1 + y2);
        let n = 1 + (3.0 * dd * dd).sqrt().sqrt() as usize;
        for i in 1..=n {
            let t = i as f32 / n as f32;
            let mt = 1.0 - t;
            let x = mt * mt * x0 + 2.0 * mt * t * x1 + t * t * x2;
            let y = mt * mt * y0 + 2.0 * mt * t * y1 + t * t * y2;
            self.line_to(x, y);
        }
    }

    pub fn cubic_to(&mut self, x1: f32, y1: f32, x2: f32, y2: f32, x3: f32, y3: f32) {
        let (x0, y0) = self.pen;
        let dd = (x0 - 2.0 * x1 + x2)
            .hypot(y0 - 2.0 * y1 + y2)
            .max((x1 - 2.0 * x2 + x3).hypot(y1 - 2.0 * y2 + y3));
        let n = 1 + ((3.0 * dd * dd).sqrt().sqrt() * 1.5) as usize;
        for i in 1..=n {
            let t = i as f32 / n as f32;
            let mt = 1.0 - t;
            let x =
                mt * mt * mt * x0 + 3.0 * mt * mt * t * x1 + 3.0 * mt * t * t * x2 + t * t * t * x3;
            let y =
                mt * mt * mt * y0 + 3.0 * mt * mt * t * y1 + 3.0 * mt * t * t * y2 + t * t * t * y3;
            self.line_to(x, y);
        }
    }

    /// contours close themselves; calling this twice is harmless
    pub fn close(&mut self) {
        if self.pen != self.start {
            let (x, y) = self.start;
            self.line_to(x, y);
        }
    }

    /// coverage 0..1 per pixel, row major
    pub fn finish(mut self, mut out: impl FnMut(usize, f32)) {
        self.close();
        if let Some(contours) = self.contours.take() {
            // i_overlay panics on non-finite input; those go through unmerged
            let finite = contours
                .iter()
                .flatten()
                .all(|p| p[0].is_finite() && p[1].is_finite());
            let shapes = if finite {
                contours.simplify_shape(FillRule::NonZero)
            } else {
                vec![contours]
            };
            for c in shapes.iter().flatten() {
                let Some((first, rest)) = c.split_first() else {
                    continue;
                };
                self.move_to(first[0], first[1]);
                for p in rest {
                    self.line_to(p[0], p[1]);
                }
            }
            self.close();
        }
        let stride = self.width + 2;
        for y in 0..self.height {
            let row = &self.cells[y * stride..(y + 1) * stride];
            let mut sum = 0.0;
            for (x, cell) in row[..self.width].iter().enumerate() {
                sum += cell;
                out(y * self.width + x, sum.abs().min(1.0));
            }
        }
    }

    fn line(&mut self, x0: f32, y0: f32, x1: f32, y1: f32) {
        if y0 == y1 {
            return;
        }
        let (dir, x0, y0, x1, y1) = if y0 < y1 {
            (1.0, x0, y0, x1, y1)
        } else {
            (-1.0, x1, y1, x0, y0)
        };
        let stride = self.width + 2;
        let max_x = self.width as f32;
        let dxdy = (x1 - x0) / (y1 - y0);
        let mut x = x0;
        let mut ys = y0.floor() as isize;
        if ys < 0 {
            x -= y0 * dxdy;
            ys = 0;
        }
        let ye = (y1.ceil() as isize).min(self.height as isize);
        for y in ys..ye {
            let row = &mut self.cells[y as usize * stride..(y as usize + 1) * stride];
            let dy = ((y + 1) as f32).min(y1) - (y as f32).max(y0);
            let xnext = x + dxdy * dy;
            let d = dy * dir;
            // anything left of the bitmap still counts for the row: clamp into column 0
            let (xa, xb) = (
                x.min(xnext).clamp(0.0, max_x),
                x.max(xnext).clamp(0.0, max_x),
            );
            let xaf = xa.floor();
            let xai = xaf as usize;
            let xbi = xb.ceil() as usize;
            if xbi <= xai + 1 {
                let xmf = 0.5 * (x.clamp(0.0, max_x) + xnext.clamp(0.0, max_x)) - xaf;
                row[xai] += d - d * xmf;
                row[xai + 1] += d * xmf;
            } else {
                let s = 1.0 / (xb - xa);
                let x0f = xa - xaf;
                let a0 = 0.5 * s * (1.0 - x0f) * (1.0 - x0f);
                let x1f = xb - xb.ceil() + 1.0;
                let am = 0.5 * s * x1f * x1f;
                row[xai] += d * a0;
                if xbi == xai + 2 {
                    row[xai + 1] += d * (1.0 - a0 - am);
                } else {
                    let a1 = s * (1.5 - x0f);
                    row[xai + 1] += d * (a1 - a0);
                    for cell in &mut row[xai + 2..xbi - 1] {
                        *cell += d * s;
                    }
                    let a2 = a1 + (xbi - xai - 3) as f32 * s;
                    row[xbi - 1] += d * (1.0 - a2 - am);
                }
                row[xbi] += d * am;
            }
            x = xnext;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn raster(w: usize, h: usize, f: impl FnOnce(&mut Accumulator)) -> Vec<f32> {
        let mut a = Accumulator::new(w, h);
        f(&mut a);
        let mut out = vec![0.0; w * h];
        a.finish(|i, c| out[i] = c);
        out
    }

    #[test]
    fn pixel_aligned_square_is_solid() {
        let out = raster(4, 4, |a| {
            a.move_to(1.0, 1.0);
            a.line_to(3.0, 1.0);
            a.line_to(3.0, 3.0);
            a.line_to(1.0, 3.0);
        });
        let want = [
            0., 0., 0., 0., 0., 1., 1., 0., 0., 1., 1., 0., 0., 0., 0., 0.,
        ];
        assert_eq!(out, want);
    }

    #[test]
    fn half_pixel_offset_gives_half_coverage() {
        let out = raster(3, 1, |a| {
            a.move_to(0.5, 0.0);
            a.line_to(2.5, 0.0);
            a.line_to(2.5, 1.0);
            a.line_to(0.5, 1.0);
        });
        for (g, w) in out.iter().zip([0.5, 1.0, 0.5]) {
            assert!((g - w).abs() < 1e-6, "{out:?}");
        }
    }

    #[test]
    fn diagonal_splits_a_pixel_in_half() {
        let out = raster(1, 1, |a| {
            a.move_to(0.0, 0.0);
            a.line_to(1.0, 1.0);
            a.line_to(0.0, 1.0);
        });
        assert!((out[0] - 0.5).abs() < 1e-6, "{out:?}");
    }

    #[test]
    fn winding_direction_does_not_matter() {
        let cw = raster(2, 2, |a| {
            a.move_to(0.0, 0.0);
            a.line_to(2.0, 0.0);
            a.line_to(2.0, 2.0);
        });
        let ccw = raster(2, 2, |a| {
            a.move_to(0.0, 0.0);
            a.line_to(2.0, 2.0);
            a.line_to(2.0, 0.0);
        });
        assert_eq!(cw, ccw);
    }

    /// two halves of a square wound opposite ways, meeting mid pixel
    fn halves(a: &mut Accumulator) {
        a.move_to(0.0, 0.0);
        a.line_to(1.5, 0.0);
        a.line_to(1.5, 1.0);
        a.line_to(0.0, 1.0);
        a.move_to(1.5, 0.0);
        a.line_to(1.5, 1.0);
        a.line_to(3.0, 1.0);
        a.line_to(3.0, 0.0);
    }

    #[test]
    fn merged_seam_stays_solid() {
        let plain = raster(3, 1, halves);
        assert_eq!(plain, [1.0, 0.0, 1.0]);
        let mut a = Accumulator::merged(3, 1);
        halves(&mut a);
        let mut out = vec![0.0; 3];
        a.finish(|i, c| out[i] = c);
        for c in out {
            assert!((c - 1.0).abs() < 1e-6, "{c}");
        }
    }
}
