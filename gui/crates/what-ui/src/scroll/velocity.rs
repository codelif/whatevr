//! release velocity: least squares over the last 100 ms before the fingers
//! lift, after the last direction change.

use super::since;

const WINDOW_MS: f64 = 100.0;

#[derive(Debug, Default)]
pub(crate) struct Samples {
    /// (event ms, position since the pan started)
    s: Vec<(u32, f64)>,
    pos: f64,
}

impl Samples {
    pub fn clear(&mut self) {
        self.s.clear();
        self.pos = 0.0;
    }

    pub fn push(&mut self, d: f64, t: u32) {
        self.pos += d;
        self.s.push((t, self.pos));
        // nothing older than the window can make it into a fit
        let keep = self
            .s
            .iter()
            .position(|&(st, _)| since(t, st) <= WINDOW_MS)
            .unwrap_or(self.s.len());
        self.s.drain(..keep.saturating_sub(1));
    }

    /// px/s at `stop`
    pub fn velocity(&self, stop: u32) -> f64 {
        let mut s: Vec<(f64, f64)> = self
            .s
            .iter()
            .filter(|(t, _)| since(stop, *t) <= WINDOW_MS)
            .map(|&(t, y)| (-since(stop, t), y))
            .collect();
        for i in (2..s.len()).rev() {
            let d1 = s[i].1 - s[i - 1].1;
            let d0 = s[i - 1].1 - s[i - 2].1;
            if d1 * d0 < 0.0 {
                s.drain(..i - 1);
                break;
            }
        }
        lsq(&s)
    }
}

/// slope of (ms, px) points, in px/s
fn lsq(s: &[(f64, f64)]) -> f64 {
    if s.len() < 2 {
        return 0.0;
    }
    let n = s.len() as f64;
    let mt = s.iter().map(|p| p.0).sum::<f64>() / n;
    let my = s.iter().map(|p| p.1).sum::<f64>() / n;
    let (mut num, mut den) = (0.0, 0.0);
    for p in s {
        num += (p.0 - mt) * (p.1 - my);
        den += (p.0 - mt).powi(2);
    }
    if den == 0.0 { 0.0 } else { num / den * 1000.0 }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn constant_speed() {
        let mut s = Samples::default();
        // 2 px every 4 ms = 500 px/s, for 300 ms
        for i in 1..=75u32 {
            s.push(2.0, 1000 + i * 4);
        }
        assert!((s.velocity(1300) - 500.0).abs() < 1e-6);
        assert!(s.s.len() <= 27);
    }

    #[test]
    fn direction_change_drops_what_came_before() {
        let mut s = Samples::default();
        for i in 1..=10u32 {
            s.push(5.0, i * 8);
        }
        for i in 11..=13u32 {
            s.push(-3.0, i * 8);
        }
        assert!((s.velocity(104) + 375.0).abs() < 1e-6);
    }
}
