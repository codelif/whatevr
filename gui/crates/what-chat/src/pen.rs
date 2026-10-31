//! the row pen: rows are built in logical px and every edge snaps to the
//! device grid.

use std::collections::HashSet;

use what_core::{Scale, Srgba8};
use what_render::{Corners, Fill, Quad, Rect};
use what_text::{GlyphKey, Icon, Paragraph, Raster, Style, TextSystem};
use what_ui::{Part, Row};

use crate::theme::css_linear;

/// builds a row in logical px, snapping every edge to the device grid
pub struct Pen<'a> {
    pub s: f32,
    scale: Scale,
    pub text: &'a mut TextSystem,
    shipped: &'a mut HashSet<GlyphKey>,
    parts: Vec<Part>,
    rasters: Vec<(GlyphKey, Raster)>,
}

impl<'a> Pen<'a> {
    pub fn new(scale: Scale, text: &'a mut TextSystem, shipped: &'a mut HashSet<GlyphKey>) -> Self {
        Self {
            s: scale.as_f32(),
            scale,
            text,
            shipped,
            parts: Vec::new(),
            rasters: Vec::new(),
        }
    }

    pub fn px(&self, v: f32) -> f32 {
        (v * self.s).round()
    }

    fn rect(&self, x: f32, y: f32, w: f32, h: f32) -> Rect {
        let (x0, y0) = (self.px(x), self.px(y));
        Rect::new(x0, y0, self.px(x + w) - x0, self.px(y + h) - y0)
    }

    fn corners(&self, r: [f32; 4]) -> Corners {
        Corners {
            top_left: r[0] * self.s,
            top_right: r[1] * self.s,
            bottom_right: r[2] * self.s,
            bottom_left: r[3] * self.s,
        }
    }

    pub fn quad(&mut self, x: f32, y: f32, w: f32, h: f32, r: [f32; 4], c: Srgba8) {
        self.parts.push(Part::Quad(Quad {
            rect: self.rect(x, y, w, h),
            radii: self.corners(r),
            fill: Fill::Solid(c.premultiply()),
            ..Quad::default()
        }));
    }

    /// css linear-gradient(deg, a, b)
    #[allow(clippy::too_many_arguments)]
    pub fn gradient(
        &mut self,
        x: f32,
        y: f32,
        w: f32,
        h: f32,
        r: [f32; 4],
        deg: f32,
        a: Srgba8,
        b: Srgba8,
    ) {
        let (from, to) = css_linear(deg, w, h);
        self.parts.push(Part::Quad(Quad {
            rect: self.rect(x, y, w, h),
            radii: self.corners(r),
            fill: Fill::Linear {
                from,
                to,
                start: a.premultiply(),
                end: b.premultiply(),
            },
            ..Quad::default()
        }));
    }

    pub fn para(&mut self, s: &str, st: &Style, spans: &[what_text::Span]) -> Paragraph {
        self.text.paragraph(s, st, spans, self.scale)
    }

    /// the paragraph's top left at (x, y)
    pub fn put(&mut self, x: f32, y: f32, p: Paragraph) {
        self.parts.push(Part::Text {
            x: self.px(x) as i32,
            y: self.px(y) as i32,
            paragraph: p,
        });
    }

    /// the icon's (0, 0) at (x, y); its bitmap ships with the first row
    /// this builder makes that uses it
    pub fn icon(&mut self, id: u16, icon: &Icon, x: f32, y: f32, c: Srgba8) {
        let key = GlyphKey::icon(id, self.s);
        if self.shipped.insert(key)
            && let Some(r) = self.text.rasterize_icon(icon, self.s)
        {
            self.rasters.push((key, r));
        }
        self.parts.push(Part::Icon {
            x: self.px(x) as i32,
            y: self.px(y) as i32,
            key,
            color: c.premultiply(),
        });
    }

    pub fn finish(self, h: f32) -> Row {
        Row {
            height: (h * self.s).round() as u32,
            parts: self.parts,
            rasters: self.rasters,
        }
    }
}
