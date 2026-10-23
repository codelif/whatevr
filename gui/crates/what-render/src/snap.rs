//! logical layout to physical pixels. every edge rounds on its own, so
//! neighbours always touch; two equal logical sizes can end up 1 px apart.

use what_core::{LogicalRect, Scale};

use crate::Rect;

/// a logical coordinate to the nearest physical pixel edge
pub fn snap(v: f32, scale: Scale) -> f32 {
    px(v, scale).round()
}

/// a logical length in physical pixels, unrounded: radii, blur, offsets
pub fn px(v: f32, scale: Scale) -> f32 {
    (v as f64 * scale.as_120ths() as f64 / 120.0) as f32
}

pub fn snap_rect(r: LogicalRect, scale: Scale) -> Rect {
    let x0 = snap(r.origin.x, scale);
    let y0 = snap(r.origin.y, scale);
    let x1 = snap(r.origin.x + r.size.width, scale);
    let y1 = snap(r.origin.y + r.size.height, scale);
    Rect::new(x0, y0, x1 - x0, y1 - y0)
}

/// 1 logical px line width: round(scale), never less than 1
pub fn hairline(scale: Scale) -> f32 {
    scale.as_f32().round().max(1.0)
}

#[cfg(test)]
mod tests {
    use what_core::{LogicalPoint, LogicalSize};

    use super::*;

    fn rect(x: f32, y: f32, w: f32, h: f32) -> LogicalRect {
        LogicalRect::new(LogicalPoint::new(x, y), LogicalSize::new(w, h))
    }

    #[test]
    fn neighbours_touch_at_fractional_scale() {
        let s = Scale::from_120ths(150);
        let a = snap_rect(rect(0.0, 0.0, 10.0, 37.0), s);
        let b = snap_rect(rect(0.0, 37.0, 10.0, 37.0), s);
        // 46.25 and 92.5: rows of 46 and 47
        assert_eq!((a.y, a.h, b.y, b.h), (0.0, 46.0, 46.0, 47.0));
        assert!(a.is_snapped() && b.is_snapped());
    }

    #[test]
    fn hairlines() {
        assert_eq!(hairline(Scale::ONE), 1.0);
        assert_eq!(hairline(Scale::from_120ths(150)), 1.0);
        assert_eq!(hairline(Scale::from_120ths(180)), 2.0);
        assert_eq!(hairline(Scale::from_integer(2)), 2.0);
        assert_eq!(hairline(Scale::from_120ths(90)), 1.0);
    }
}
