/// one rasterized glyph: font, glyph id, physical size, subpixel bin and,
/// for colour glyphs that paint with the text's colour, that colour, packed
/// into the u128 the renderer's atlas is keyed by. fonts are numbered by the
/// [`crate::TextSystem`] that made the key, so keys from two systems don't mix.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct GlyphKey(pub u128);

/// quarter pixels: the subpixel x positions a glyph can be drawn at
pub const SUBPIXEL_BINS: u32 = 4;

// spare 32 | rgba 32 | font 16 | glyph 16 | size 24 (1/64 px) | spare 6 | bin 2
const SIZE_UNITS: f32 = 64.0;
const SIZE_MAX: u32 = (1 << 24) - 1;

impl GlyphKey {
    /// `color` is straight srgb; only colour glyphs that use it pass one
    pub(crate) fn new(font: u16, glyph: u16, size: f32, bin: u32, color: Option<[u8; 4]>) -> Self {
        debug_assert!(bin < SUBPIXEL_BINS);
        let size = ((size * SIZE_UNITS).round() as u32).min(SIZE_MAX);
        let color = color.map_or(0, u32::from_be_bytes);
        Self(
            (color as u128) << 64
                | (font as u128) << 48
                | (glyph as u128) << 32
                | (size as u128) << 8
                | bin as u128,
        )
    }

    pub(crate) fn font(self) -> u16 {
        (self.0 >> 48) as u16
    }

    pub(crate) fn glyph(self) -> u16 {
        (self.0 >> 32) as u16
    }

    /// physical px
    pub(crate) fn size(self) -> f32 {
        ((self.0 >> 8) as u32 & SIZE_MAX) as f32 / SIZE_UNITS
    }

    /// how far right of the whole-pixel origin the glyph is drawn, in px
    pub(crate) fn offset(self) -> f32 {
        (self.0 & 3) as f32 / SUBPIXEL_BINS as f32
    }

    /// the text colour a colour glyph paints with, straight srgb
    pub(crate) fn color(self) -> Option<[u8; 4]> {
        let c = (self.0 >> 64) as u32;
        (c != 0).then(|| c.to_be_bytes())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fields_round_trip() {
        let k = GlyphKey::new(513, 65535, 18.75, 3, Some([0x11, 0x1b, 0x21, 0xff]));
        assert_eq!(
            (k.font(), k.glyph(), k.size(), k.offset()),
            (513, 65535, 18.75, 0.75)
        );
        assert_eq!(k.color(), Some([0x11, 0x1b, 0x21, 0xff]));
        assert_eq!(GlyphKey::new(1, 2, 15.0, 0, None).color(), None);
        assert_ne!(
            GlyphKey::new(1, 2, 15.0, 0, None),
            GlyphKey::new(1, 2, 15.0, 1, None)
        );
    }
}
