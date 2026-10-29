//! kinetic scroll after the fingers lift: v·e^(-t/τ), τ 325 ms, stopping
//! where the speed drops under 20 px/s.

const TAU: f64 = 0.325;
const MIN_SPEED: f64 = 20.0;

#[derive(Clone, Copy, Debug)]
pub(crate) struct Fling {
    /// px/s at the start
    pub v: f64,
    pub start: u32,
    /// seconds until it stops
    end: f64,
}

impl Fling {
    pub fn new(v: f64, start: u32) -> Option<Self> {
        let s = v.abs();
        (s > MIN_SPEED).then(|| Self {
            v,
            start,
            end: TAU * (s / MIN_SPEED).ln(),
        })
    }

    /// px travelled `t` seconds in, and whether it's over
    pub fn at(&self, t: f64) -> (f64, bool) {
        let tt = t.clamp(0.0, self.end);
        (self.v * TAU * (1.0 - (-tt / TAU).exp()), t >= self.end)
    }

    pub fn velocity(&self, t: f64) -> f64 {
        if t >= self.end {
            0.0
        } else {
            self.v * (-t.max(0.0) / TAU).exp()
        }
    }

    pub fn total(&self) -> f64 {
        self.at(self.end).0
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn lands_where_speed_hits_the_floor() {
        let f = Fling::new(2000.0, 0).unwrap();
        assert!((f.velocity(f.end - 1e-9) - MIN_SPEED).abs() < 1e-3);
        assert!((f.total() - 2000.0 * TAU * (1.0 - MIN_SPEED / 2000.0)).abs() < 1e-6);
        assert!(f.at(f.end + 1.0).1);
        assert!(Fling::new(-15.0, 0).is_none());
    }
}
