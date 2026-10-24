/// 8-bit sRGB, straight alpha. what themes and hex codes are written in.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub struct Srgba8 {
    pub r: u8,
    pub g: u8,
    pub b: u8,
    pub a: u8,
}

impl Srgba8 {
    pub const fn new(r: u8, g: u8, b: u8, a: u8) -> Self {
        Self { r, g, b, a }
    }

    pub const fn rgb(r: u8, g: u8, b: u8) -> Self {
        Self::new(r, g, b, 255)
    }

    /// 0xRRGGBB, opaque
    pub const fn hex(rgb: u32) -> Self {
        Self::rgb((rgb >> 16) as u8, (rgb >> 8) as u8, rgb as u8)
    }

    pub fn premultiply(self) -> PremulRgba {
        let a = self.a as f32 / 255.0;
        PremulRgba {
            r: self.r as f32 / 255.0 * a,
            g: self.g as f32 / 255.0 * a,
            b: self.b as f32 / 255.0 * a,
            a,
        }
    }
}

/// sRGB encoded, premultiplied alpha. what the renderer blends in, like
/// browsers do.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct PremulRgba {
    pub r: f32,
    pub g: f32,
    pub b: f32,
    pub a: f32,
}

impl PremulRgba {
    pub const TRANSPARENT: Self = Self {
        r: 0.0,
        g: 0.0,
        b: 0.0,
        a: 0.0,
    };
}

impl From<Srgba8> for PremulRgba {
    fn from(c: Srgba8) -> Self {
        c.premultiply()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn endpoints_and_premultiply() {
        let white = Srgba8::rgb(255, 255, 255).premultiply();
        assert_eq!((white.r, white.a), (1.0, 1.0));
        let clear = Srgba8::new(255, 0, 0, 0).premultiply();
        assert_eq!(clear.r, 0.0);
        let half = Srgba8::new(255, 128, 0, 51).premultiply();
        assert_eq!((half.r, half.a), (0.2, 0.2));
        assert!((half.g - 128.0 / 255.0 * 0.2).abs() < 1e-6);
    }
}
