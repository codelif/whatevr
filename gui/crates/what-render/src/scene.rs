//! what a frame is made of. everything is in physical pixels, y down, origin
//! at the top left of the target. rebuilt every frame; `clear` keeps the
//! allocations.

use what_core::LinearRgba;

/// physical pixels. edges of quads, images and clips must sit on whole
/// pixels (debug builds check); use [`crate::snap`] to get there.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Rect {
    pub x: f32,
    pub y: f32,
    pub w: f32,
    pub h: f32,
}

impl Rect {
    pub const fn new(x: f32, y: f32, w: f32, h: f32) -> Self {
        Self { x, y, w, h }
    }

    pub fn is_snapped(&self) -> bool {
        [self.x, self.y, self.x + self.w, self.y + self.h]
            .iter()
            .all(|v| v.fract() == 0.0)
    }

    pub(crate) fn to_array(self) -> [f32; 4] {
        [self.x, self.y, self.w, self.h]
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Corners {
    pub top_left: f32,
    pub top_right: f32,
    pub bottom_right: f32,
    pub bottom_left: f32,
}

impl Corners {
    pub const fn all(r: f32) -> Self {
        Self {
            top_left: r,
            top_right: r,
            bottom_right: r,
            bottom_left: r,
        }
    }

    /// css: radii that don't fit their sides all shrink by the same factor
    pub(crate) fn fit(self, w: f32, h: f32) -> [f32; 4] {
        let r = self.map(|r| r.max(0.0));
        let side = |len: f32, a: f32, b: f32| {
            if a + b > 0.0 {
                len / (a + b)
            } else {
                f32::INFINITY
            }
        };
        let f = side(w, r.top_left, r.top_right)
            .min(side(w, r.bottom_left, r.bottom_right))
            .min(side(h, r.top_left, r.bottom_left))
            .min(side(h, r.top_right, r.bottom_right))
            .min(1.0);
        r.map(|r| r * f.max(0.0)).to_array()
    }

    fn map(self, f: impl Fn(f32) -> f32) -> Self {
        Self {
            top_left: f(self.top_left),
            top_right: f(self.top_right),
            bottom_right: f(self.bottom_right),
            bottom_left: f(self.bottom_left),
        }
    }

    fn to_array(self) -> [f32; 4] {
        [
            self.top_left,
            self.top_right,
            self.bottom_right,
            self.bottom_left,
        ]
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Edges {
    pub top: f32,
    pub right: f32,
    pub bottom: f32,
    pub left: f32,
}

impl Edges {
    pub const fn all(w: f32) -> Self {
        Self {
            top: w,
            right: w,
            bottom: w,
            left: w,
        }
    }

    pub(crate) fn to_array(self) -> [f32; 4] {
        [self.top, self.right, self.bottom, self.left]
    }
}

/// pixels outside the rect (rounded by radii) are dropped
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Clip {
    pub rect: Rect,
    pub radii: Corners,
}

impl Clip {
    pub const NONE: Self = Self {
        rect: Rect::new(-1e7, -1e7, 2e7, 2e7),
        radii: Corners::all(0.0),
    };

    pub const fn rect(rect: Rect) -> Self {
        Self {
            rect,
            radii: Corners::all(0.0),
        }
    }

    fn is_none(&self) -> bool {
        *self == Self::NONE
    }
}

impl Default for Clip {
    fn default() -> Self {
        Self::NONE
    }
}

/// gradient points are in the quad's own 0..1 box, so they follow it around.
/// colours mix premultiplied in linear light.
#[derive(Clone, Copy, Debug, PartialEq)]
pub enum Fill {
    Solid(LinearRgba),
    Linear {
        from: [f32; 2],
        to: [f32; 2],
        start: LinearRgba,
        end: LinearRgba,
    },
    /// an ellipse; radius [0.5, 0.5] at center [0.5, 0.5] touches all four sides
    Radial {
        center: [f32; 2],
        radius: [f32; 2],
        start: LinearRgba,
        end: LinearRgba,
    },
}

impl Default for Fill {
    fn default() -> Self {
        Self::Solid(LinearRgba::TRANSPARENT)
    }
}

impl From<LinearRgba> for Fill {
    fn from(c: LinearRgba) -> Self {
        Self::Solid(c)
    }
}

/// rounded rect with an optional border drawn inside its bounds (css
/// border-box). the fill runs under the border, like css's default
/// background-clip.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Quad {
    pub rect: Rect,
    pub radii: Corners,
    pub fill: Fill,
    pub border: Edges,
    pub border_color: LinearRgba,
    pub clip: Clip,
}

/// css box-shadow (outer only): `rect` and `radii` are the casting box, the
/// shadow is never drawn under it. blur is the css blur radius, sigma = blur / 2.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct Shadow {
    pub rect: Rect,
    pub radii: Corners,
    pub offset: [f32; 2],
    pub spread: f32,
    pub blur: f32,
    pub color: LinearRgba,
    pub clip: Clip,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct ImageId(pub(crate) u32);

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Image {
    pub rect: Rect,
    pub image: ImageId,
    pub radii: Corners,
    pub opacity: f32,
    pub clip: Clip,
}

impl Image {
    pub fn new(rect: Rect, image: ImageId) -> Self {
        Self {
            rect,
            image,
            radii: Corners::default(),
            opacity: 1.0,
            clip: Clip::NONE,
        }
    }
}

/// layers draw bottom to top. inside one layer all shadows draw first, then
/// all quads, then all images, whatever order they were pushed in.
#[derive(Clone, Debug, Default)]
pub struct Scene {
    pub(crate) quads: Vec<Quad>,
    pub(crate) shadows: Vec<Shadow>,
    pub(crate) images: Vec<Image>,
    /// where each layer after the first starts, per kind
    pub(crate) layers: Vec<LayerStart>,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) struct LayerStart {
    pub quads: usize,
    pub shadows: usize,
    pub images: usize,
}

impl Scene {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn clear(&mut self) {
        self.quads.clear();
        self.shadows.clear();
        self.images.clear();
        self.layers.clear();
    }

    pub fn is_empty(&self) -> bool {
        self.quads.is_empty() && self.shadows.is_empty() && self.images.is_empty()
    }

    /// everything pushed after this goes on top of everything before
    pub fn layer(&mut self) {
        let start = self.end();
        if self.layers.last() != Some(&start) && start != LayerStart::default() {
            self.layers.push(start);
        }
    }

    pub fn quad(&mut self, q: Quad) {
        debug_assert!(q.rect.is_snapped(), "quad off the pixel grid: {:?}", q.rect);
        debug_assert_clip(&q.clip);
        self.quads.push(q);
    }

    pub fn shadow(&mut self, s: Shadow) {
        debug_assert_clip(&s.clip);
        self.shadows.push(s);
    }

    pub fn image(&mut self, i: Image) {
        debug_assert!(
            i.rect.is_snapped(),
            "image off the pixel grid: {:?}",
            i.rect
        );
        debug_assert_clip(&i.clip);
        self.images.push(i);
    }

    fn end(&self) -> LayerStart {
        LayerStart {
            quads: self.quads.len(),
            shadows: self.shadows.len(),
            images: self.images.len(),
        }
    }

    /// (start, end) per kind for every layer, bottom first
    pub(crate) fn layer_ranges(&self) -> impl Iterator<Item = (LayerStart, LayerStart)> + '_ {
        let starts = std::iter::once(LayerStart::default()).chain(self.layers.iter().copied());
        let ends = self
            .layers
            .iter()
            .copied()
            .chain(std::iter::once(self.end()));
        starts.zip(ends)
    }
}

fn debug_assert_clip(c: &Clip) {
    debug_assert!(
        c.is_none() || c.rect.is_snapped(),
        "clip off the pixel grid: {:?}",
        c.rect
    );
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn radii_shrink_together_like_css() {
        let r = Corners {
            top_left: 30.0,
            top_right: 30.0,
            bottom_right: 0.0,
            bottom_left: 10.0,
        };
        // top side 40 wide holds 60 of radius: everything scales by 2/3
        let got = r.fit(40.0, 100.0);
        let want = [20.0, 20.0, 0.0, 20.0 / 3.0];
        for (g, w) in got.iter().zip(want) {
            assert!((g - w).abs() < 1e-5, "{got:?}");
        }
        assert_eq!(Corners::all(4.0).fit(40.0, 40.0), [4.0; 4]);
    }

    #[test]
    fn layers_split_by_kind() {
        let mut s = Scene::new();
        s.layer(); // nothing yet, no empty layer
        s.quad(Quad::default());
        s.shadow(Shadow::default());
        s.layer();
        s.layer(); // twice in a row, still one layer
        s.quad(Quad::default());
        let ranges: Vec<_> = s
            .layer_ranges()
            .map(|(a, b)| (a.quads, b.quads, a.shadows, b.shadows))
            .collect();
        assert_eq!(ranges, [(0, 1, 0, 1), (1, 2, 1, 1)]);
    }
}
