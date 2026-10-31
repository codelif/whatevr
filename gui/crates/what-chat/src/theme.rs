//! the colours and type everything is drawn in, and the small helpers the
//! variant sheet's css needed.

use what_core::Srgba8;
use what_text::{Family, Icon, Style};

#[derive(Clone, Copy, Debug)]
pub struct Theme {
    pub bg: Srgba8,
    pub chrome: Srgba8,
    pub inc: Srgba8,
    pub inc2: Srgba8,
    pub out: Srgba8,
    pub text: Srgba8,
    pub muted: Srgba8,
    pub faint: Srgba8,
    pub acc: Srgba8,
    pub acc_ink: Srgba8,
    /// link preview card over a bubble
    pub link_bg: Srgba8,
    /// the corner button's outline
    pub ring: Srgba8,
    pub bar_idle: Srgba8,
    pub bar_hot: Srgba8,
    /// sender name colours
    pub ids: [Srgba8; 5],
}

impl Theme {
    /// a sender's name colour; any index works
    pub fn id(&self, i: u8) -> Srgba8 {
        self.ids[i as usize % self.ids.len()]
    }
}

pub const DARK: Theme = Theme {
    bg: Srgba8::hex(0x1f1f1d),
    chrome: Srgba8::hex(0x161615),
    inc: Srgba8::hex(0x2d2d2a),
    inc2: Srgba8::hex(0x363633),
    out: Srgba8::hex(0x22384f),
    text: Srgba8::hex(0xecece8),
    muted: Srgba8::hex(0x9a9a94),
    faint: Srgba8::hex(0x4a4a44),
    acc: Srgba8::hex(0x7fa3c9),
    acc_ink: Srgba8::hex(0x161615),
    link_bg: Srgba8::new(0, 0, 0, 46),
    ring: Srgba8::hex(0x3a3a35),
    bar_idle: Srgba8::hex(0x6a6a63),
    bar_hot: Srgba8::hex(0x9a9a94),
    ids: [
        Srgba8::hex(0xd39a78),
        Srgba8::hex(0x7fb8bd),
        Srgba8::hex(0xaa9fd6),
        Srgba8::hex(0xc9b97c),
        Srgba8::hex(0x92bd98),
    ],
};

/// straight srgb lerp, like the sheet's hexMix
pub fn mix(a: Srgba8, b: Srgba8, t: f32) -> Srgba8 {
    let l = |x: u8, y: u8| (x as f32 + (y as f32 - x as f32) * t).round() as u8;
    Srgba8::new(l(a.r, b.r), l(a.g, b.g), l(a.b, b.b), l(a.a, b.a))
}

/// css hsl(), hue in degrees
pub fn hsl(h: f32, s: f32, l: f32) -> Srgba8 {
    let h = h.rem_euclid(360.0) / 30.0;
    let a = s * l.min(1.0 - l);
    let f = |n: f32| {
        let k = (n + h).rem_euclid(12.0);
        let v = l - a * (k - 3.0).min(9.0 - k).clamp(-1.0, 1.0);
        (v * 255.0).round() as u8
    };
    Srgba8::rgb(f(0.0), f(8.0), f(4.0))
}

/// a css linear-gradient angle as start and end in the box's own 0..1 space
pub fn css_linear(deg: f32, w: f32, h: f32) -> ([f32; 2], [f32; 2]) {
    let (s, c) = deg.to_radians().sin_cos();
    let len = (w * s).abs() + (h * c).abs();
    let (dx, dy) = (s * len / 2.0 / w, -c * len / 2.0 / h);
    ([0.5 - dx, 0.5 - dy], [0.5 + dx, 0.5 + dy])
}

pub fn font() -> Family {
    Family::Named(what_assets::fonts::FAMILY.into())
}

/// `line` is css px
pub fn style(size: f32, line: f32, weight: f32, color: Srgba8) -> Style {
    Style {
        family: font(),
        size,
        weight,
        italic: false,
        color: color.premultiply(),
        line_height: line / size,
    }
}

/// delivery ticks as the sheet drew them (state h): read is the heavier
/// stroke. returns the icon and its width, 10 px tall.
pub fn ticks(state: u8) -> (Icon, f32) {
    let w = if state == 3 { 1.9 } else { 1.25 };
    let n = state.clamp(1, 2);
    let mut b = Icon::builder();
    b.stroke(&[[1.0, 5.2], [3.6, 7.8], [8.6, 1.8]], w);
    if n == 2 {
        b.stroke(&[[4.4 + 4.2, 7.6], [4.4 + 9.6, 1.8]], w);
    }
    (b.build(), 11.0 + (n - 1) as f32 * 4.4)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn css_hsl_and_gradients() {
        assert_eq!(hsl(0.0, 1.0, 0.5), Srgba8::rgb(255, 0, 0));
        assert_eq!(hsl(120.0, 1.0, 0.25), Srgba8::rgb(0, 128, 0));
        // 180deg runs straight down
        let (a, b) = css_linear(180.0, 100.0, 50.0);
        assert!((a[0] - 0.5).abs() < 1e-6 && a[1].abs() < 1e-6);
        assert!((b[1] - 1.0).abs() < 1e-6);
    }
}
