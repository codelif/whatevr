use wayland_client::protocol::wl_pointer::{
    self, Axis, AxisRelativeDirection, AxisSource as WlSource,
};
use wayland_client::{Connection, Dispatch, Proxy, QueueHandle, WEnum};
use wayland_protocols::wp::cursor_shape::v1::client::wp_cursor_shape_device_v1::{
    self, WpCursorShapeDeviceV1,
};
use wayland_protocols::wp::pointer_gestures::zv1::client::zwp_pointer_gesture_hold_v1::{
    self, ZwpPointerGestureHoldV1,
};
use wayland_protocols::wp::pointer_gestures::zv1::client::zwp_pointer_gestures_v1::ZwpPointerGesturesV1;
use what_core::{AxisSource, Button, LogicalPoint, PointerEvent, ScrollFrame, WindowId};

use crate::keyboard::surface_window;
use crate::{Event, State};

pub(crate) struct Pointer {
    pub wl: wl_pointer::WlPointer,
    pub shape: Option<WpCursorShapeDeviceV1>,
    pub hold: Option<ZwpPointerGestureHoldV1>,
    focus: Option<WindowId>,
    pos: LogicalPoint,
    /// everything since the last wl_pointer.frame, in order; axis data
    /// collects into one scroll that goes last
    pending: Vec<(WindowId, PointerEvent)>,
    scroll: Option<ScrollFrame>,
}

impl Pointer {
    pub(crate) fn new(
        wl: wl_pointer::WlPointer,
        shape: Option<WpCursorShapeDeviceV1>,
        hold: Option<ZwpPointerGestureHoldV1>,
    ) -> Self {
        Self {
            wl,
            shape,
            hold,
            focus: None,
            pos: LogicalPoint::new(0.0, 0.0),
            pending: Vec::new(),
            scroll: None,
        }
    }

    pub(crate) fn release(self) {
        if let Some(s) = self.shape {
            s.destroy();
        }
        if let Some(h) = self.hold {
            h.destroy();
        }
        if self.wl.version() >= 3 {
            self.wl.release();
        }
    }

    pub(crate) fn unfocus(&mut self, id: WindowId) {
        if self.focus == Some(id) {
            self.focus = None;
        }
        self.pending.retain(|(w, _)| *w != id);
    }

    fn push(&mut self, ev: PointerEvent) {
        if let Some(id) = self.focus {
            self.pending.push((id, ev));
        }
    }

    fn scroll(&mut self) -> &mut ScrollFrame {
        self.scroll.get_or_insert_with(ScrollFrame::default)
    }

    fn flush(&mut self, out: &mut Vec<Event>) {
        out.extend(
            self.pending
                .drain(..)
                .map(|(id, ev)| Event::Pointer(id, ev)),
        );
        if let (Some(s), Some(id)) = (self.scroll.take(), self.focus) {
            out.push(Event::Pointer(id, PointerEvent::Scroll(s)));
        }
    }
}

fn axis(a: WEnum<Axis>) -> Option<usize> {
    match a {
        WEnum::Value(Axis::HorizontalScroll) => Some(0),
        WEnum::Value(Axis::VerticalScroll) => Some(1),
        _ => None,
    }
}

fn button(code: u32) -> Button {
    // linux/input-event-codes.h
    match code {
        0x110 => Button::Left,
        0x111 => Button::Right,
        0x112 => Button::Middle,
        c => Button::Other(c),
    }
}

impl Dispatch<wl_pointer::WlPointer, ()> for State {
    fn event(
        state: &mut Self,
        wl: &wl_pointer::WlPointer,
        event: wl_pointer::Event,
        _: &(),
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        use wl_pointer::Event as E;
        let Some(p) = state.seat.pointer.as_mut() else {
            return;
        };
        match event {
            E::Enter {
                serial,
                surface,
                surface_x,
                surface_y,
            } => {
                if let Some(shape) = &p.shape {
                    shape.set_shape(serial, wp_cursor_shape_device_v1::Shape::Default);
                }
                p.focus = surface_window(&surface);
                p.pos = LogicalPoint::new(surface_x as f32, surface_y as f32);
                p.push(PointerEvent::Enter { pos: p.pos });
            }
            E::Leave { .. } => {
                // axis data already queued still belongs to the old window
                p.flush(&mut state.events);
                p.push(PointerEvent::Leave);
                p.flush(&mut state.events);
                p.focus = None;
            }
            E::Motion {
                time,
                surface_x,
                surface_y,
            } => {
                p.pos = LogicalPoint::new(surface_x as f32, surface_y as f32);
                p.push(PointerEvent::Motion {
                    pos: p.pos,
                    time_ms: time,
                });
            }
            E::Warp {
                surface_x,
                surface_y,
            } => {
                p.pos = LogicalPoint::new(surface_x as f32, surface_y as f32);
                p.push(PointerEvent::Motion {
                    pos: p.pos,
                    time_ms: 0,
                });
            }
            E::Button {
                time,
                button: code,
                state: s,
                ..
            } => {
                let pressed = s == WEnum::Value(wl_pointer::ButtonState::Pressed);
                p.push(PointerEvent::Button {
                    button: button(code),
                    pressed,
                    pos: p.pos,
                    time_ms: time,
                });
            }
            E::Axis {
                time,
                axis: a,
                value,
            } => {
                if let Some(i) = axis(a) {
                    let s = p.scroll();
                    s.time_ms = time;
                    s.delta[i] += value as f32;
                }
            }
            E::AxisSource { axis_source } => {
                p.scroll().source = match axis_source {
                    WEnum::Value(WlSource::Wheel) => Some(AxisSource::Wheel),
                    WEnum::Value(WlSource::Finger) => Some(AxisSource::Finger),
                    WEnum::Value(WlSource::Continuous) => Some(AxisSource::Continuous),
                    WEnum::Value(WlSource::WheelTilt) => Some(AxisSource::WheelTilt),
                    _ => None,
                };
            }
            E::AxisStop { time, axis: a } => {
                if let Some(i) = axis(a) {
                    let s = p.scroll();
                    s.time_ms = time;
                    s.stop[i] = true;
                }
            }
            E::AxisValue120 { axis: a, value120 } => {
                if let Some(i) = axis(a) {
                    p.scroll().value120[i] += value120;
                }
            }
            E::AxisDiscrete { axis: a, discrete } => {
                // only sent below v8; value120 replaces it
                if let Some(i) = axis(a) {
                    p.scroll().value120[i] += discrete * 120;
                }
            }
            E::AxisRelativeDirection { axis: a, direction } => {
                if let Some(i) = axis(a) {
                    p.scroll().inverted[i] =
                        direction == WEnum::Value(AxisRelativeDirection::Inverted);
                }
            }
            E::Frame => p.flush(&mut state.events),
            _ => {}
        }
        // before v5 there are no frames: every event stands alone
        if wl.version() < 5 {
            p.flush(&mut state.events);
        }
    }
}

impl Dispatch<ZwpPointerGestureHoldV1, ()> for State {
    fn event(
        state: &mut Self,
        _: &ZwpPointerGestureHoldV1,
        event: zwp_pointer_gesture_hold_v1::Event,
        _: &(),
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        use zwp_pointer_gesture_hold_v1::Event as E;
        let Some(p) = state.seat.pointer.as_mut() else {
            return;
        };
        let (id, ev) = match event {
            E::Begin { time, surface, .. } => (
                surface_window(&surface),
                PointerEvent::Hold {
                    begin: true,
                    cancelled: false,
                    time_ms: time,
                },
            ),
            E::End {
                time, cancelled, ..
            } => (
                p.focus,
                PointerEvent::Hold {
                    begin: false,
                    cancelled: cancelled != 0,
                    time_ms: time,
                },
            ),
            _ => return,
        };
        if let Some(id) = id {
            // gestures aren't framed; keep them behind pointer events already in
            p.flush(&mut state.events);
            state.events.push(Event::Pointer(id, ev));
        }
    }
}

wayland_client::delegate_noop!(State: ignore WpCursorShapeDeviceV1);
wayland_client::delegate_noop!(State: ignore ZwpPointerGesturesV1);
