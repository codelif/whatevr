//! chromium's wheel animation: 120 px a notch on an ease-in-out curve, 200 ms
//! for one notch down to 100 ms for four or more. a notch mid-animation
//! retargets from where it is, starting at the speed it already has.

use super::since;

pub(crate) const NOTCH: f64 = 120.0;

/// css cubic-bezier(x1, y1, x2, y2)
#[derive(Clone, Copy, Debug)]
pub(crate) struct Bez(pub f64, pub f64, pub f64, pub f64);

impl Bez {
    fn c(s: f64, p1: f64, p2: f64) -> f64 {
        3.0 * (1.0 - s).powi(2) * s * p1 + 3.0 * (1.0 - s) * s * s * p2 + s * s * s
    }

    fn d(s: f64, p1: f64, p2: f64) -> f64 {
        3.0 * (1.0 - s).powi(2) * p1 + 6.0 * (1.0 - s) * s * (p2 - p1) + 3.0 * s * s * (1.0 - p2)
    }

    fn solve(&self, x: f64) -> f64 {
        let (mut lo, mut hi) = (0.0, 1.0);
        for _ in 0..40 {
            let m = (lo + hi) / 2.0;
            if Self::c(m, self.0, self.2) < x {
                lo = m
            } else {
                hi = m
            }
        }
        (lo + hi) / 2.0
    }

    pub(crate) fn y(&self, x: f64) -> f64 {
        Self::c(self.solve(x), self.1, self.3)
    }

    fn slope(&self, x: f64) -> f64 {
        let s = self.solve(x);
        let dx = Self::d(s, self.0, self.2);
        if dx.abs() < 1e-9 {
            0.0
        } else {
            Self::d(s, self.1, self.3) / dx
        }
    }
}

fn segment_ms(d: f64) -> f64 {
    let slope = (6.0 - 12.0) / (480.0 - 120.0);
    let offset = 12.0 - 120.0 * slope;
    (offset + d.abs() * slope).clamp(6.0, 12.0) / 60.0 * 1000.0
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct Anim {
    from: f64,
    pub to: f64,
    start: u32,
    dur: f64,
    bez: Bez,
}

impl Anim {
    /// position at `now`, and whether it's done
    pub fn at(&self, now: u32) -> (f64, bool) {
        let p = since(now, self.start) / self.dur;
        if p >= 1.0 {
            (self.to, true)
        } else {
            (self.from + (self.to - self.from) * self.bez.y(p), false)
        }
    }

    /// px/s
    fn velocity(&self, now: u32) -> f64 {
        let p = since(now, self.start) / self.dur;
        if p >= 1.0 {
            0.0
        } else {
            self.bez.slope(p) * (self.to - self.from) / (self.dur / 1000.0)
        }
    }

    /// `d` more px from a wheel at time `t`; `pos` is where the content is
    /// when nothing is animating
    pub fn notch(prev: Option<&Anim>, pos: f64, d: f64, t: u32) -> Option<Anim> {
        let Some(a) = prev else {
            return Some(Anim {
                from: pos,
                to: pos + d,
                start: t,
                dur: segment_ms(d),
                bez: Bez(0.42, 0.0, 0.58, 1.0),
            });
        };
        let target = a.to + d;
        let cur = a.at(t).0;
        let v = a.velocity(t);
        let nd = target - cur;
        if nd.abs() < 0.01 {
            return None;
        }
        // don't let the new segment outlast 2.5x what the current speed needs
        let bound = if v.abs() < 0.01 {
            f64::INFINITY
        } else {
            let b = nd / v * 2.5;
            if b < 0.0 { f64::INFINITY } else { b * 1000.0 }
        };
        let dur = segment_ms(nd).min(bound).max(1.0);
        let slope = (v * (dur / 1000.0) / nd).clamp(-1000.0, 1000.0);
        Some(Anim {
            from: cur,
            to: target,
            start: t,
            dur,
            bez: Bez(0.42, 0.42 * slope, 0.58, 1.0),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn durations_follow_distance() {
        assert!((segment_ms(120.0) - 200.0).abs() < 1e-9);
        assert!((segment_ms(480.0) - 100.0).abs() < 1e-9);
        assert!((segment_ms(4000.0) - 100.0).abs() < 1e-9);
    }

    #[test]
    fn retarget_keeps_moving_and_lands() {
        let a = Anim::notch(None, 0.0, NOTCH, 0).unwrap();
        let mid = a.at(60).0;
        let b = Anim::notch(Some(&a), 0.0, NOTCH, 60).unwrap();
        assert!((b.at(60).0 - mid).abs() < 1e-6);
        assert!(b.velocity(60) > 0.0);
        assert_eq!(b.at(10_000), (240.0, true));
        // no backwards dip right after retargeting
        assert!(b.at(64).0 > mid);
    }
}
