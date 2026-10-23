//! logical units are what layout works in; physical units are buffer pixels.
//! the two never convert implicitly, only through [`Scale`].

#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct LogicalSize {
    pub width: f32,
    pub height: f32,
}

impl LogicalSize {
    pub const fn new(width: f32, height: f32) -> Self {
        Self { width, height }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct LogicalPoint {
    pub x: f32,
    pub y: f32,
}

impl LogicalPoint {
    pub const fn new(x: f32, y: f32) -> Self {
        Self { x, y }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq)]
pub struct LogicalRect {
    pub origin: LogicalPoint,
    pub size: LogicalSize,
}

impl LogicalRect {
    pub const fn new(origin: LogicalPoint, size: LogicalSize) -> Self {
        Self { origin, size }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub struct PhysicalSize {
    pub width: u32,
    pub height: u32,
}

impl PhysicalSize {
    pub const fn new(width: u32, height: u32) -> Self {
        Self { width, height }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub struct PhysicalPoint {
    pub x: i32,
    pub y: i32,
}

impl PhysicalPoint {
    pub const fn new(x: i32, y: i32) -> Self {
        Self { x, y }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub struct PhysicalRect {
    pub origin: PhysicalPoint,
    pub size: PhysicalSize,
}

impl PhysicalRect {
    pub const fn new(origin: PhysicalPoint, size: PhysicalSize) -> Self {
        Self { origin, size }
    }
}

/// output scale in 120ths, the unit fractional-scale-v1 speaks. 150 = 1.25x.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct Scale(u32);

impl Scale {
    pub const ONE: Self = Self(120);

    /// zero is not a scale; treat it as 1x instead of dividing by it later
    pub const fn from_120ths(v: u32) -> Self {
        if v == 0 { Self::ONE } else { Self(v) }
    }

    pub const fn from_integer(n: u32) -> Self {
        Self::from_120ths(n.saturating_mul(120))
    }

    pub const fn as_120ths(self) -> u32 {
        self.0
    }

    pub fn as_f32(self) -> f32 {
        self.0 as f32 / 120.0
    }

    pub const fn is_integer(self) -> bool {
        self.0.is_multiple_of(120)
    }

    /// rounds half away from zero, as fractional-scale-v1 requires for buffer sizes
    pub fn to_physical(self, size: LogicalSize) -> PhysicalSize {
        PhysicalSize::new(self.scale_len(size.width), self.scale_len(size.height))
    }

    pub fn to_logical(self, size: PhysicalSize) -> LogicalSize {
        LogicalSize::new(
            size.width as f32 / self.as_f32(),
            size.height as f32 / self.as_f32(),
        )
    }

    fn scale_len(self, len: f32) -> u32 {
        (len.max(0.0) as f64 * self.0 as f64 / 120.0).round() as u32
    }
}

impl Default for Scale {
    fn default() -> Self {
        Self::ONE
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fractional_buffer_sizes_round_half_away() {
        let s = Scale::from_120ths(150);
        assert_eq!(
            s.to_physical(LogicalSize::new(960.0, 641.0)),
            PhysicalSize::new(1200, 801)
        );
        // 3 * 1.5 = 4.5 rounds up
        assert_eq!(
            Scale::from_120ths(180).to_physical(LogicalSize::new(3.0, 1.0)),
            PhysicalSize::new(5, 2)
        );
    }

    #[test]
    fn zero_scale_is_one() {
        assert_eq!(Scale::from_120ths(0), Scale::ONE);
    }
}
