use std::ops::Range;
use std::sync::Arc;

use what_core::{PremulRgba, Srgba8};

#[derive(Clone, Debug, Default, PartialEq)]
pub enum Family {
    #[default]
    SansSerif,
    Serif,
    Monospace,
    Emoji,
    Named(Arc<str>),
}

/// what a paragraph starts with. sizes are logical px.
#[derive(Clone, Debug, PartialEq)]
pub struct Style {
    pub family: Family,
    pub size: f32,
    /// 100..900
    pub weight: f32,
    pub italic: bool,
    pub color: PremulRgba,
    /// multiple of the font size, like a unitless css line-height
    pub line_height: f32,
}

impl Default for Style {
    fn default() -> Self {
        Self {
            family: Family::SansSerif,
            size: 15.0,
            weight: 400.0,
            italic: false,
            color: Srgba8::rgb(0, 0, 0).premultiply(),
            line_height: 1.3,
        }
    }
}

/// overrides the base style on a byte range of the text; None keeps the
/// base value. later spans win where they overlap.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Span {
    pub range: Range<usize>,
    pub family: Option<Family>,
    pub size: Option<f32>,
    pub weight: Option<f32>,
    pub italic: Option<bool>,
    pub color: Option<PremulRgba>,
}

impl Span {
    pub fn new(range: Range<usize>) -> Self {
        Self {
            range,
            ..Self::default()
        }
    }
}
