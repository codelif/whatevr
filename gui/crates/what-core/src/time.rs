use std::time::Duration;

/// CLOCK_MONOTONIC nanoseconds. the same clock wayland timestamps use,
/// so frame callbacks, presentation feedback and our own reads line up.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub struct Timestamp(u64);

impl Timestamp {
    pub const fn from_nanos(ns: u64) -> Self {
        Self(ns)
    }

    pub const fn from_millis(ms: u64) -> Self {
        Self(ms * 1_000_000)
    }

    pub const fn as_nanos(self) -> u64 {
        self.0
    }

    pub fn saturating_since(self, earlier: Self) -> Duration {
        Duration::from_nanos(self.0.saturating_sub(earlier.0))
    }
}
