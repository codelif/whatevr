//! glyph bitmaps: outlines through our own rasterizer, colour emoji from
//! CBDT bitmaps or COLR paint graphs.

pub(crate) mod accum;
pub(crate) mod bitmap;
pub(crate) mod colr;
pub(crate) mod outline;

use std::sync::LazyLock;

use crate::GlyphKind;

/// one glyph's pixels, placed relative to its origin on the baseline
#[derive(Clone, Debug, PartialEq)]
pub struct Raster {
    /// px from the origin to the bitmap's left column
    pub left: i32,
    /// px from the baseline up to the bitmap's top row
    pub top: i32,
    pub width: u32,
    pub height: u32,
    pub kind: GlyphKind,
    /// Mask: one coverage byte per pixel. Color: rgba8, srgb, premultiplied
    /// (what what-render's atlas takes).
    pub data: Vec<u8>,
}

/// srgb byte to linear light
pub(crate) static TO_LINEAR: LazyLock<[f32; 256]> =
    LazyLock::new(|| std::array::from_fn(|i| to_linear(i as f32 / 255.0)));

pub(crate) fn to_linear(c: f32) -> f32 {
    if c <= 0.04045 {
        c / 12.92
    } else {
        ((c + 0.055) / 1.055).powf(2.4)
    }
}

pub(crate) fn to_srgb(l: f32) -> f32 {
    let l = l.clamp(0.0, 1.0);
    if l <= 0.0031308 {
        l * 12.92
    } else {
        1.055 * l.powf(1.0 / 2.4) - 0.055
    }
}

/// premultiplied srgb rgba to the atlas's bytes
pub(crate) fn encode(px: [f32; 4]) -> [u8; 4] {
    px.map(|v| (v.clamp(0.0, 1.0) * 255.0).round() as u8)
}

/// premultiplied linear light to premultiplied srgb
pub(crate) fn linear_to_srgb(c: [f32; 4]) -> [f32; 4] {
    let a = c[3].min(1.0);
    if a <= 0.0 {
        return [0.0; 4];
    }
    [
        to_srgb(c[0] / a) * a,
        to_srgb(c[1] / a) * a,
        to_srgb(c[2] / a) * a,
        a,
    ]
}
