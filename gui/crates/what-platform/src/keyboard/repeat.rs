use std::io;
use std::os::fd::{AsFd, BorrowedFd, OwnedFd};
use std::time::Duration;

use log::debug;
use what_core::{Timestamp, WindowId};

use crate::event_loop::{arm, drain, timerfd};
use crate::presentation::now;

/// client-side repeat from repeat_info, unless the compositor turns out to send
/// wl_keyboard `repeated` itself (seat v10), in which case we step aside for good
#[derive(Clone, Copy)]
struct Held {
    window: WindowId,
    code: u32,
    /// compositor ms of the press, and our clock at the same moment
    press_ms: u32,
    press_at: Timestamp,
}

pub(crate) struct Repeat {
    timer: OwnedFd,
    rate: i32,
    delay: i32,
    key: Option<Held>,
    compositor_repeats: bool,
}

impl Repeat {
    pub(crate) fn new() -> io::Result<Self> {
        // spec defaults until repeat_info arrives
        Ok(Self {
            timer: timerfd()?,
            rate: 25,
            delay: 600,
            key: None,
            compositor_repeats: false,
        })
    }

    pub(crate) fn fd(&self) -> BorrowedFd<'_> {
        self.timer.as_fd()
    }

    pub(crate) fn set_info(&mut self, rate: i32, delay: i32) {
        debug!("key repeat rate {rate}/s delay {delay}ms");
        self.rate = rate;
        self.delay = delay;
        if rate <= 0 {
            self.stop();
        }
    }

    pub(crate) fn compositor_repeats(&mut self) {
        if !self.compositor_repeats {
            debug!("compositor sends key repeats, own repeat off");
            self.compositor_repeats = true;
            self.stop();
        }
    }

    pub(crate) fn start(&mut self, window: WindowId, code: u32, press_ms: u32) {
        if self.compositor_repeats || self.rate <= 0 {
            return;
        }
        self.key = Some(Held {
            window,
            code,
            press_ms,
            press_at: now(),
        });
        let delay = Duration::from_millis(self.delay.max(1) as u64);
        let interval = Duration::from_micros(1_000_000 / self.rate as u64);
        if let Err(e) = arm(&self.timer, delay, interval) {
            log::warn!("key repeat timer: {e}");
        }
    }

    pub(crate) fn stop_key(&mut self, code: u32) {
        if self.key.is_some_and(|k| k.code == code) {
            self.stop();
        }
    }

    pub(crate) fn stop(&mut self) {
        self.key = None;
        let _ = arm(&self.timer, Duration::ZERO, Duration::ZERO);
    }

    /// one repeat per wakeup even if several intervals passed; a stalled loop
    /// shouldn't type a burst of characters
    /// window, keycode, and a time_ms on the compositor's clock
    pub(crate) fn fire(&mut self) -> Option<(WindowId, u32, u32)> {
        if drain(self.timer.as_fd()) == 0 {
            return None;
        }
        let k = self.key?;
        let elapsed = now().saturating_since(k.press_at).as_millis() as u32;
        Some((k.window, k.code, k.press_ms.wrapping_add(elapsed)))
    }
}
