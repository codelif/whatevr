//! Text layout and glyph rasterization: Parley wrapper, ellipsis, max lines, raster cache, COLRv1 painter.

mod emoji;
mod icon;
mod key;
mod paragraph;
mod raster;
mod style;
mod system;
mod truncate;

use std::path::PathBuf;

pub use icon::{Icon, IconBuilder};
pub use key::{GlyphKey, SUBPIXEL_BINS};
pub use paragraph::{GlyphKind, LineInfo, Paragraph, PlacedGlyph};
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

#[cfg(test)]
mod tests {
    #[test]
    fn crosses_threads() {
        fn send<T: Send>() {}
        send::<crate::TextSystem>();
        send::<crate::Paragraph>();
    }
}
