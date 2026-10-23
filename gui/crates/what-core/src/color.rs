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

    pub fn to_linear(self) -> LinearRgba {
        let a = self.a as f32 / 255.0;
        LinearRgba {
            r: srgb_to_linear(self.r) * a,
            g: srgb_to_linear(self.g) * a,
            b: srgb_to_linear(self.b) * a,
            a,
        }
    }
}

/// linear light, premultiplied alpha. what the renderer blends in.
#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct LinearRgba {
    pub r: f32,
    pub g: f32,
    pub b: f32,
    pub a: f32,
}

impl LinearRgba {
    pub const TRANSPARENT: Self = Self {
        r: 0.0,
        g: 0.0,
        b: 0.0,
        a: 0.0,
    };
}

impl From<Srgba8> for LinearRgba {
    fn from(c: Srgba8) -> Self {
        c.to_linear()
    }
}

fn srgb_to_linear(c: u8) -> f32 {
    let c = c as f32 / 255.0;
    if c <= 0.04045 {
        c / 12.92
    } else {
        ((c + 0.055) / 1.055).powf(2.4)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn endpoints_and_premultiply() {
        let white = Srgba8::rgb(255, 255, 255).to_linear();
        assert_eq!((white.r, white.a), (1.0, 1.0));
        let half = Srgba8::new(255, 0, 0, 0).to_linear();
        assert_eq!(half.r, 0.0);
        let mid = Srgba8::rgb(128, 128, 128).to_linear();
        assert!((mid.r - 0.2158).abs() < 1e-3);
    }
}
