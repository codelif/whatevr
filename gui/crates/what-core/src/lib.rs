//! Geometry, units, colors, input event types, ids, a tiny logger and the
//! global allocator.

pub mod alloc;
pub mod color;
pub mod geom;
pub mod ids;
pub mod input;
pub mod log;
pub mod time;

pub use color::{PremulRgba, Srgba8};
pub use geom::{
    LogicalPoint, LogicalRect, LogicalSize, PhysicalPoint, PhysicalRect, PhysicalSize, Scale,
};
pub use ids::{Token, WindowId};
pub use input::{
    AxisSource, Button, KeyEvent, KeyState, Keysym, Modifiers, PointerEvent, ScrollFrame,
};
pub use time::Timestamp;
