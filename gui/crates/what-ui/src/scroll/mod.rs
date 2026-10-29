//! scroll physics, knowing nothing about what scrolls. pointer frames go in,
//! [`Scroller::tick`] hands out how far to move this frame; the owner clamps
//! and calls [`Scroller::hit_edge`] when it can't.

mod boost;
mod fling;
mod velocity;
pub(crate) mod wheel;

use what_core::{AxisSource, ScrollFrame, Timestamp};

use boost::Boost;
use fling::Fling;
use velocity::Samples;
use wheel::{Anim, NOTCH};

/// touchpad px are tiny next to a wheel's; picked by feel in the fling lab
const FINGER_MULT: f64 = 4.0;
/// fastest fling in logical px/s, boosted or not (chromium's touchscreen
/// cap). past this rows stop surviving from one frame to the next
const MAX_FLING: f64 = 17_000.0;

/// ms between two wayland timestamps, 0 if `then` is later
fn since(now: u32, then: u32) -> f64 {
    let d = now.wrapping_sub(then);
    if d > 1 << 31 { 0.0 } else { d as f64 }
}

fn ms(t: Timestamp) -> u32 {
    (t.as_nanos() / 1_000_000) as u32
}

/// one axis. positive moves the content up, wayland's sign.
#[derive(Debug, Default)]
pub struct Scroller {
    /// where the input says the content is, logical px from wherever we started
    pos: f64,
    /// how much of `pos` tick already handed out
    reported: f64,
    fling: Option<(Fling, f64)>,
    anim: Option<Anim>,
    samples: Samples,
    boost: Boost,
    source: Option<AxisSource>,
    last_t: u32,
}

impl Scroller {
    pub fn new() -> Self {
        Self::default()
    }

    /// takes the frame's vertical axis; the movement shows up at the next tick
    pub fn scroll(&mut self, f: &ScrollFrame) {
        if f.source.is_some() {
            self.source = f.source;
        }
        let t = if f.time_ms != 0 {
            f.time_ms
        } else {
            self.last_t
        };
        self.last_t = t;
        let dy = f.delta[1] as f64;
        let v120 = f.value120[1];
        let wheel =
            matches!(self.source, Some(AxisSource::Wheel | AxisSource::WheelTilt)) || v120 != 0;
        if wheel {
            let n = if v120 != 0 {
                v120 as f64 / 120.0
            } else {
                dy / 15.0
            };
            if n != 0.0 {
                self.wheel(n, t);
            }
            return;
        }
        let mult = match self.source {
            Some(AxisSource::Finger) => FINGER_MULT,
            _ => 1.0,
        };
        if dy != 0.0 {
            self.pan(dy, mult, t);
        }
        if f.stop[1] {
            self.lift(mult, t);
        }
    }

    /// touchpad hold: fingers resting stop a fling
    pub fn hold(&mut self, begin: bool, time_ms: u32) {
        if begin {
            self.interrupt(time_ms);
            self.boost.touched(time_ms);
        }
    }

    /// logical px to move the content this frame
    pub fn tick(&mut self, now: Timestamp) -> f32 {
        let now = ms(now);
        if let Some(a) = &self.anim {
            let (y, done) = a.at(now);
            self.pos = y;
            if done {
                self.anim = None;
            }
        }
        if let Some((f, base)) = &self.fling {
            let (x, done) = f.at(since(now, f.start) / 1000.0);
            self.pos = base + x;
            if done {
                self.fling = None;
            }
        }
        let d = self.pos - self.reported;
        self.reported = self.pos;
        d as f32
    }

    /// keep frames coming
    pub fn active(&self) -> bool {
        self.fling.is_some() || self.anim.is_some() || self.pos != self.reported
    }

    /// logical px still to go if nothing interrupts: a fling's landing or the
    /// wheel's target
    pub fn remaining(&self) -> f32 {
        let end = match (&self.fling, &self.anim) {
            (Some((f, base)), _) => base + f.total(),
            (None, Some(a)) => a.to,
            (None, None) => self.pos,
        };
        (end - self.reported) as f32
    }

    /// the content can't go further this way
    pub fn hit_edge(&mut self) {
        self.fling = None;
        self.anim = None;
        self.boost.reset();
    }

    pub fn stop(&mut self) {
        self.hit_edge();
        self.samples.clear();
        self.pos = self.reported;
    }

    fn interrupt(&mut self, t: u32) {
        if let Some((f, _)) = self.fling.take() {
            self.boost
                .interrupted(f.velocity(since(t, f.start) / 1000.0), t);
        }
        self.anim = None;
    }

    fn pan(&mut self, raw: f64, mult: f64, t: u32) {
        self.interrupt(t);
        let d = raw * mult;
        self.boost.pan(d, t);
        self.samples.push(raw, t);
        self.pos += d;
    }

    fn lift(&mut self, mult: f64, t: u32) {
        let v = self
            .boost
            .apply(self.samples.velocity(t) * mult)
            .clamp(-MAX_FLING, MAX_FLING);
        self.fling = Fling::new(v, t).map(|f| (f, self.pos));
        self.boost.prev_start = if self.fling.is_some() { v } else { 0.0 };
        self.boost.end_pan();
        self.samples.clear();
    }

    fn wheel(&mut self, notches: f64, t: u32) {
        self.fling = None;
        self.boost.reset();
        self.samples.clear();
        let d = notches * NOTCH;
        let prev = self.anim.take();
        self.anim = Anim::notch(prev.as_ref(), self.pos, d, t);
        if self.anim.is_none()
            && let Some(p) = prev
        {
            self.pos = p.to + d;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn at(ms: u32) -> Timestamp {
        Timestamp::from_millis(ms as u64)
    }

    fn finger(dy: f32, t: u32, stop: bool) -> ScrollFrame {
        ScrollFrame {
            source: Some(AxisSource::Finger),
            time_ms: t,
            delta: [0.0, dy],
            stop: [false, stop],
            ..ScrollFrame::default()
        }
    }

    fn run(s: &mut Scroller, from: u32, to: u32) -> f64 {
        let mut sum = 0.0;
        for t in (from..=to).step_by(8) {
            sum += s.tick(at(t)) as f64;
        }
        sum
    }

    #[test]
    fn pan_then_fling_lands_on_remaining() {
        let mut s = Scroller::new();
        for i in 1..=20 {
            s.scroll(&finger(3.0, 1000 + i * 8, false));
        }
        s.scroll(&finger(0.0, 1168, true));
        let moved = s.tick(at(1168)) as f64;
        assert_eq!(moved, 20.0 * 3.0 * FINGER_MULT);
        let left = s.remaining() as f64;
        assert!(left > 0.0);
        let flung = run(&mut s, 1176, 5000);
        assert!((flung - left).abs() < 1e-3, "{flung} vs {left}");
        assert!(!s.active());
    }

    #[test]
    fn hold_and_edges_stop_motion() {
        let mut s = Scroller::new();
        for i in 1..=20 {
            s.scroll(&finger(3.0, i * 8, false));
        }
        s.scroll(&finger(0.0, 168, true));
        run(&mut s, 168, 200);
        assert!(s.active());
        s.hold(true, 204);
        run(&mut s, 208, 216);
        assert!(!s.active());

        s.scroll(&ScrollFrame {
            source: Some(AxisSource::Wheel),
            time_ms: 300,
            value120: [0, 120],
            ..ScrollFrame::default()
        });
        assert_eq!(s.remaining(), 120.0);
        run(&mut s, 300, 340);
        s.hit_edge();
        assert_eq!(run(&mut s, 348, 400), 0.0);
    }

    #[test]
    fn boosted_flings_stop_at_the_cap() {
        let mut s = Scroller::new();
        let mut t = 0;
        for _ in 0..12 {
            s.hold(true, t);
            for _ in 0..5 {
                t += 8;
                s.scroll(&finger(80.0, t, false));
            }
            s.scroll(&finger(0.0, t, true));
            let (f, _) = s.fling.as_ref().expect("a fling");
            assert!(f.velocity(0.0).abs() <= MAX_FLING + 1e-6);
            t += 100;
            s.tick(at(t));
        }
        assert_eq!(s.fling.as_ref().unwrap().0.velocity(0.0), MAX_FLING);
    }

    #[test]
    fn wheel_notches_add_up() {
        let mut s = Scroller::new();
        for (i, t) in [0u32, 40, 80].into_iter().enumerate() {
            s.scroll(&ScrollFrame {
                source: Some(AxisSource::Wheel),
                time_ms: t,
                value120: [0, if i == 1 { -120 } else { 120 }],
                ..ScrollFrame::default()
            });
            run(&mut s, t, t + 39);
        }
        run(&mut s, 120, 1000);
        assert!((s.reported - 120.0).abs() < 1e-9, "{}", s.reported);
    }
}
