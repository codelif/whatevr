//! overlay scrollbar: a 3 px line that shows while scrolling and fades 0.8 s
//! after, 8 px and brighter while the pointer is on the edge strip or drags.
//! the thumb is 48 px, or proportional (never under 48) when the list knows
//! its whole height.

use std::cell::Cell;

use what_core::{
    Button, LogicalPoint, LogicalRect, PhysicalRect, PointerEvent, Scale, Srgba8, Timestamp,
};
use what_render::{Corners, Fill, Quad, Rect, Scene};

use crate::scroll::wheel::Bez;

const THIN: f32 = 3.0;
const WIDE: f32 = 8.0;
const INSET: f32 = 3.0;
const THUMB: f32 = 48.0;
const HOLD_MS: u64 = 800;
const FADE_MS: u64 = 200;
const GROW_MS: u64 = 120;
/// css `ease`, what the demo's transitions ran on
const EASE: Bez = Bez(0.25, 0.1, 0.25, 1.0);

#[derive(Clone, Copy, Debug, PartialEq)]
pub enum ScrollbarAction {
    /// 0 = oldest at the top .. 1 = newest at the bottom
    DragTo(f32),
}

#[derive(Clone, Copy)]
struct Tween {
    from: f32,
    to: f32,
    start: Timestamp,
    ms: u64,
}

impl Tween {
    fn at(&self, now: Timestamp) -> f32 {
        let t = now.saturating_since(self.start).as_millis() as f64 / self.ms as f64;
        if t >= 1.0 {
            return self.to;
        }
        self.from + (self.to - self.from) * EASE.y(t) as f32
    }

    fn done(&self, now: Timestamp) -> bool {
        now.saturating_since(self.start).as_millis() as u64 >= self.ms
    }
}

pub struct Scrollbar {
    /// last thing that keeps it shown: a scroll, a hover change, a drag end
    last: Timestamp,
    shown: Tween,
    wide: Tween,
    hover: bool,
    /// grab point from the thumb's top, logical px
    drag: Option<f32>,
    /// what draw() got last, for pointer hits: (pos, len)
    drawn: Cell<(f32, Option<f32>)>,
    drag_pos: Cell<Option<f32>>,
}

impl Default for Scrollbar {
    fn default() -> Self {
        Self::new()
    }
}

impl Scrollbar {
    pub fn new() -> Self {
        let zero = Timestamp::from_nanos(0);
        let still = |v| Tween {
            from: v,
            to: v,
            start: zero,
            ms: 1,
        };
        Self {
            last: zero,
            shown: still(0.0),
            wide: still(0.0),
            hover: false,
            drag: None,
            drawn: Cell::new((1.0, None)),
            drag_pos: Cell::new(None),
        }
    }

    /// any scroll movement: show and restart the fade
    pub fn scrolled(&mut self, now: Timestamp) {
        self.show(now);
    }

    /// pointer events over the list; Some when it takes the event (a press
    /// on the edge strip, or the drag after it)
    pub fn pointer(
        &mut self,
        ev: &PointerEvent,
        track: LogicalRect,
        now: Timestamp,
    ) -> Option<ScrollbarAction> {
        match *ev {
            PointerEvent::Enter { pos } | PointerEvent::Motion { pos, .. } => {
                if let Some(grab) = self.drag {
                    let f = self.fraction(track, pos.y - grab);
                    self.drag_pos.set(Some(f));
                    return Some(ScrollbarAction::DragTo(f));
                }
                self.set_hover(inside(track, pos), now);
                None
            }
            PointerEvent::Leave => {
                self.drag = None;
                self.drag_pos.set(None);
                self.set_hover(false, now);
                None
            }
            PointerEvent::Button {
                button: Button::Left,
                pressed: true,
                pos,
                ..
            } if inside(track, pos) => {
                let (top, len) = self.thumb(track.size.height);
                let y = pos.y - track.origin.y;
                // grab the thumb where it was hit, or by its middle when the
                // press lands on the bare track
                let grab = if (top..top + len).contains(&y) {
                    y - top
                } else {
                    len / 2.0
                };
                self.drag = Some(grab);
                self.set_wide(now);
                self.show(now);
                let f = self.fraction(track, pos.y - grab);
                self.drag_pos.set(Some(f));
                Some(ScrollbarAction::DragTo(f))
            }
            PointerEvent::Button {
                button: Button::Left,
                pressed: false,
                pos,
                ..
            } if self.drag.is_some() => {
                self.drag = None;
                self.drag_pos.set(None);
                self.hover = inside(track, pos);
                self.set_wide(now);
                self.show(now);
                None
            }
            _ => None,
        }
    }

    /// `track` is the edge strip; `pos` and `len` come from List::position.
    /// colours are the thin idle line and the hovered or dragged thumb.
    #[allow(clippy::too_many_arguments)]
    pub fn draw(
        &self,
        scene: &mut Scene,
        track: PhysicalRect,
        pos: f32,
        len: Option<f32>,
        scale: Scale,
        now: Timestamp,
        idle: Srgba8,
        hot: Srgba8,
    ) {
        self.drawn.set((pos, len));
        let alpha = self.alpha(now);
        if alpha <= 0.0 {
            return;
        }
        let s = scale.as_f32();
        let t = self.wide.at(now);
        // quads sit on whole pixels, so the widening steps a pixel at a time
        let w = ((THIN + (WIDE - THIN) * t) * s).round();
        let h = track.size.height as f32 / s;
        // a drag shows where the pointer put it, not where the list caught up to
        let pos = self.drag_pos.get().unwrap_or(pos);
        let (top, l) = self.thumb_at(h, pos, len);
        let right = (track.origin.x + track.size.width as i32) as f32 - (INSET * s).round();
        let y0 = (track.origin.y as f32 + top * s).round();
        let y1 = (track.origin.y as f32 + (top + l) * s).round();
        let mut c = mix(idle, hot, t);
        c.a = (c.a as f32 * alpha).round() as u8;
        scene.quad(Quad {
            rect: Rect::new(right - w, y0, w, y1 - y0),
            radii: Corners::all(w / 2.0),
            fill: Fill::Solid(c.premultiply()),
            ..Quad::default()
        });
    }

    /// still fading or growing: keep frames coming
    pub fn active(&self, now: Timestamp) -> bool {
        let fading = !self.held() && now < after(self.off_at(), FADE_MS) && self.shown.to > 0.0;
        fading || !self.shown.done(now) || !self.wide.done(now)
    }

    fn held(&self) -> bool {
        self.hover || self.drag.is_some()
    }

    fn off_at(&self) -> Timestamp {
        after(self.last, HOLD_MS)
    }

    fn alpha(&self, now: Timestamp) -> f32 {
        if !self.held() && now >= self.off_at() {
            let t = self.off_at();
            return Tween {
                from: self.shown.at(t),
                to: 0.0,
                start: t,
                ms: FADE_MS,
            }
            .at(now);
        }
        self.shown.at(now)
    }

    fn show(&mut self, now: Timestamp) {
        let from = self.alpha(now);
        self.last = now;
        if self.shown.to < 1.0 || from < 1.0 {
            self.shown = Tween {
                from,
                to: 1.0,
                start: now,
                ms: FADE_MS,
            };
        }
    }

    fn set_hover(&mut self, on: bool, now: Timestamp) {
        if on != self.hover {
            self.hover = on;
            self.set_wide(now);
            self.show(now);
        }
    }

    fn set_wide(&mut self, now: Timestamp) {
        let to = if self.held() { 1.0 } else { 0.0 };
        if to != self.wide.to {
            self.wide = Tween {
                from: self.wide.at(now),
                to,
                start: now,
                ms: GROW_MS,
            };
        }
    }

    /// thumb top and length in logical px within a track `h` tall
    fn thumb_at(&self, h: f32, pos: f32, len: Option<f32>) -> (f32, f32) {
        let l = len.map_or(THUMB, |l| (l * h).max(THUMB)).min(h);
        (pos.clamp(0.0, 1.0) * (h - l), l)
    }

    fn thumb(&self, h: f32) -> (f32, f32) {
        let (pos, len) = self.drawn.get();
        self.thumb_at(h, pos, len)
    }

    fn fraction(&self, track: LogicalRect, top: f32) -> f32 {
        let (_, l) = self.thumb(track.size.height);
        let room = track.size.height - l;
        if room <= 0.0 {
            return 1.0;
        }
        ((top - track.origin.y) / room).clamp(0.0, 1.0)
    }
}

fn inside(r: LogicalRect, p: LogicalPoint) -> bool {
    p.x >= r.origin.x
        && p.x < r.origin.x + r.size.width
        && p.y >= r.origin.y
        && p.y < r.origin.y + r.size.height
}

fn after(t: Timestamp, ms: u64) -> Timestamp {
    Timestamp::from_nanos(t.as_nanos() + ms * 1_000_000)
}

/// straight srgb lerp, what a css colour transition does
fn mix(a: Srgba8, b: Srgba8, t: f32) -> Srgba8 {
    let l = |x: u8, y: u8| (x as f32 + (y as f32 - x as f32) * t).round() as u8;
    Srgba8::new(l(a.r, b.r), l(a.g, b.g), l(a.b, b.b), l(a.a, b.a))
}

#[cfg(test)]
mod tests {
    use what_core::LogicalSize;

    use super::*;

    fn at(ms: u64) -> Timestamp {
        Timestamp::from_millis(ms)
    }

    fn track() -> LogicalRect {
        LogicalRect::new(LogicalPoint::new(284.0, 0.0), LogicalSize::new(16.0, 400.0))
    }

    #[test]
    fn fades_after_scrolling_stops() {
        let mut b = Scrollbar::new();
        assert_eq!(b.alpha(at(0)), 0.0);
        b.scrolled(at(1000));
        assert_eq!(b.alpha(at(1200)), 1.0);
        assert_eq!(b.alpha(at(1800)), 1.0);
        assert!(b.alpha(at(1900)) < 1.0 && b.alpha(at(1900)) > 0.0);
        assert_eq!(b.alpha(at(2000)), 0.0);
        assert!(!b.active(at(2001)));
    }

    #[test]
    fn hover_holds_and_widens() {
        let mut b = Scrollbar::new();
        let pos = LogicalPoint::new(295.0, 100.0);
        b.pointer(&PointerEvent::Motion { pos, time_ms: 0 }, track(), at(0));
        assert_eq!((b.alpha(at(5000)), b.wide.at(at(5000))), (1.0, 1.0));
        let out = LogicalPoint::new(100.0, 100.0);
        b.pointer(
            &PointerEvent::Motion {
                pos: out,
                time_ms: 0,
            },
            track(),
            at(5000),
        );
        assert_eq!(b.wide.at(at(5200)), 0.0);
        assert_eq!(b.alpha(at(6000)), 0.0);
    }

    #[test]
    fn drag_keeps_the_grab_point() {
        let mut b = Scrollbar::new();
        // thumb at the bottom: 352..400
        b.drawn.set((1.0, None));
        let press = |y| PointerEvent::Button {
            button: Button::Left,
            pressed: true,
            pos: LogicalPoint::new(295.0, y),
            time_ms: 0,
        };
        assert_eq!(
            b.pointer(&press(360.0), track(), at(0)),
            Some(ScrollbarAction::DragTo(1.0))
        );
        let to = |y| PointerEvent::Motion {
            pos: LogicalPoint::new(10.0, y),
            time_ms: 0,
        };
        // grabbed 8 px below its top, so the top sits at 176: halfway
        assert_eq!(
            b.pointer(&to(184.0), track(), at(10)),
            Some(ScrollbarAction::DragTo(0.5))
        );
        // a press on the bare track centres the thumb there
        let mut b = Scrollbar::new();
        b.drawn.set((1.0, None));
        assert_eq!(
            b.pointer(&press(24.0), track(), at(0)),
            Some(ScrollbarAction::DragTo(0.0))
        );
    }
}
