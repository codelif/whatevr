//! Text layout and glyph rasterization: Parley wrapper, ellipsis, max lines, raster cache, COLRv1 painter.

mod emoji;
mod key;
mod paragraph;
mod raster;
mod style;
mod system;

use std::path::PathBuf;

pub use key::{GlyphKey, SUBPIXEL_BINS};
pub use paragraph::{GlyphKind, Paragraph, PlacedGlyph};
pub use raster::Raster;
pub use style::{Family, Span, Style};
pub use system::TextSystem;

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("reading {0}: {1}")]
    Read(PathBuf, std::io::Error),
    #[error("no font in {0}")]
    NotAFont(PathBuf),
}
