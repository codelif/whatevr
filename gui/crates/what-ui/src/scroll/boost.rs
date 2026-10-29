//! firefox's fling accelerator: a quick flick in the same direction while a
//! fast fling is still going adds that fling's starting speed to the new one.

use super::since;

#[derive(Debug)]
pub(crate) struct Boost {
    /// starting speed of the last fling, 0 once it can't boost any more
    pub prev_start: f64,
    /// speed of the fling the fingers stopped
    cancel_v: f64,
    /// when the fingers came down on a running fling
    touch: Option<u32>,
    pan_start: Option<u32>,
    pan_pos: f64,
    last: Option<(u32, f64)>,
    /// slowest the pan went, px/s over >5 ms steps
    min_pan_v: f64,
}

impl Default for Boost {
    fn default() -> Self {
        Self {
            prev_start: 0.0,
            cancel_v: 0.0,
            touch: None,
            pan_start: None,
            pan_pos: 0.0,
            last: None,
            min_pan_v: f64::INFINITY,
        }
    }
}

impl Boost {
    /// fingers stopped a fling going at `v`
    pub fn interrupted(&mut self, v: f64, t: u32) {
        self.cancel_v = v;
        self.touch = self.touch.or(Some(t));
    }

    pub fn touched(&mut self, t: u32) {
        self.touch = Some(t);
    }

    pub fn pan(&mut self, d: f64, t: u32) {
        if self.pan_start.is_none() {
            self.pan_start = Some(t);
            self.last = Some((t, 0.0));
        }
        self.pan_pos += d;
        if let Some((lt, lp)) = self.last
            && since(t, lt) > 5.0
        {
            let v = (self.pan_pos - lp).abs() / since(t, lt) * 1000.0;
            self.min_pan_v = self.min_pan_v.min(v);
            self.last = Some((t, self.pan_pos));
        }
    }

    pub fn apply(&self, v: f64) -> f64 {
        let rest = match (self.touch, self.pan_start) {
            (Some(a), Some(b)) => since(b, a),
            _ => return v,
        };
        let same = (self.prev_start >= 0.0) == (v >= 0.0);
        if self.prev_start != 0.0
            && same
            && v.abs() >= 1500.0
            && self.min_pan_v >= 800.0
            && self.cancel_v.abs() >= 1500.0
            && rest < 50.0
        {
            v + self.prev_start
        } else {
            v
        }
    }

    /// a pan ended; keeps prev_start for the fling the caller starts next
    pub fn end_pan(&mut self) {
        *self = Self {
            prev_start: self.prev_start,
            ..Self::default()
        };
    }

    pub fn reset(&mut self) {
        *self = Self::default();
    }
}
