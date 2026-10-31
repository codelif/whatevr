//! the jump checker. a row on screen in two frames in a row must move by
//! exactly what the list says it moved, measured from the list's gravity
//! edge so a resize that moves that edge doesn't count. when the width
//! changes rows rewrap, so only the anchor row (the bottom one) is held.

use std::collections::HashMap;

use what_core::PhysicalRect;
use what_ui::{FrameReport, Gravity};

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Jump {
    pub frame: u64,
    pub key: u32,
    /// physical px off from where it should have been
    pub off: i32,
}

struct Seen {
    rows: HashMap<u32, (i32, u32)>,
    anchor: Option<u32>,
    edge: i32,
    width: u32,
}

pub struct Checker {
    gravity: Gravity,
    prev: Option<Seen>,
    pub frames: u64,
    pub jumps: Vec<Jump>,
}

impl Checker {
    pub fn new(gravity: Gravity) -> Self {
        Self {
            gravity,
            prev: None,
            frames: 0,
            jumps: Vec::new(),
        }
    }

    /// `rows` from List::visible(); `exempt` when the content itself moved
    /// (a followed append)
    pub fn frame(
        &mut self,
        rows: &[(u32, i32, u32)],
        vp: PhysicalRect,
        report: &FrameReport,
        exempt: bool,
    ) -> &[Jump] {
        self.frames += 1;
        let vt = vp.origin.y;
        let vb = vt + vp.size.height as i32;
        let edge = match self.gravity {
            Gravity::Bottom => vb,
            Gravity::Top => vt,
        };
        let now = Seen {
            rows: rows.iter().map(|&(k, t, h)| (k, (t, h))).collect(),
            anchor: rows.iter().rev().find(|r| r.1 < vb).map(|r| r.0),
            edge,
            width: vp.size.width,
        };
        let before = self.jumps.len();
        if let Some(p) = self.prev.take()
            && !report.jumped
            && !exempt
        {
            let d = now.edge - p.edge - report.moved;
            if p.width == now.width {
                for (&k, &(t, _)) in &now.rows {
                    if let Some(&(t0, _)) = p.rows.get(&k)
                        && t != t0 + d
                    {
                        self.jumps.push(Jump {
                            frame: self.frames,
                            key: k,
                            off: t - (t0 + d),
                        });
                    }
                }
            } else if let Some(k) = p.anchor
                && let (Some(&(t0, h0)), Some(&(t, h))) = (p.rows.get(&k), now.rows.get(&k))
            {
                let off = (t + h as i32) - (t0 + h0 as i32 + d);
                if off != 0 {
                    self.jumps.push(Jump {
                        frame: self.frames,
                        key: k,
                        off,
                    });
                }
            }
        }
        self.prev = Some(now);
        &self.jumps[before..]
    }
}

#[cfg(test)]
mod tests {
    use what_core::{PhysicalPoint, PhysicalSize};

    use super::*;

    fn vp(h: u32) -> PhysicalRect {
        PhysicalRect::new(PhysicalPoint::new(0, 0), PhysicalSize::new(300, h))
    }

    fn moved(m: i32) -> FrameReport {
        FrameReport {
            moved: m,
            ..FrameReport::default()
        }
    }

    #[test]
    fn catches_a_row_that_moved_on_its_own() {
        let mut c = Checker::new(Gravity::Bottom);
        c.frame(&[(1, 0, 50), (2, 50, 50)], vp(100), &moved(0), false);
        assert!(
            c.frame(&[(1, -10, 50), (2, 40, 50)], vp(100), &moved(10), false)
                .is_empty()
        );
        let j = c.frame(&[(1, -20, 50), (2, 33, 50)], vp(100), &moved(10), false);
        assert_eq!(
            j,
            [Jump {
                frame: 3,
                key: 2,
                off: 3
            }]
        );
    }

    #[test]
    fn a_taller_window_moves_the_bottom_edge() {
        let mut c = Checker::new(Gravity::Bottom);
        c.frame(&[(1, 0, 50), (2, 50, 50)], vp(100), &moved(0), false);
        assert!(
            c.frame(&[(1, 20, 50), (2, 70, 50)], vp(120), &moved(0), false)
                .is_empty()
        );
    }
}
