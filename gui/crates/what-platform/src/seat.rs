use log::debug;
use wayland_client::protocol::wl_seat;
use wayland_client::{Connection, Dispatch, QueueHandle, WEnum};

use crate::State;
use crate::keyboard::{Keyboard, Repeat};
use crate::pointer::Pointer;

pub(crate) struct Seat {
    pub keyboard: Option<Keyboard>,
    pub pointer: Option<Pointer>,
    /// outlives keyboards so its timerfd stays registered with the loop
    pub repeat: Repeat,
}

impl Seat {
    pub(crate) fn new() -> std::io::Result<Self> {
        Ok(Self {
            keyboard: None,
            pointer: None,
            repeat: Repeat::new()?,
        })
    }
}

impl Dispatch<wl_seat::WlSeat, ()> for State {
    fn event(
        state: &mut Self,
        seat: &wl_seat::WlSeat,
        event: wl_seat::Event,
        _: &(),
        _: &Connection,
        qh: &QueueHandle<Self>,
    ) {
        match event {
            wl_seat::Event::Capabilities {
                capabilities: WEnum::Value(caps),
            } => {
                let has_kb = caps.contains(wl_seat::Capability::Keyboard);
                let has_ptr = caps.contains(wl_seat::Capability::Pointer);
                debug!("seat keyboard {has_kb} pointer {has_ptr}");

                if has_kb && state.seat.keyboard.is_none() {
                    state.seat.keyboard = Some(Keyboard::new(seat.get_keyboard(qh, ())));
                } else if !has_kb && let Some(kb) = state.seat.keyboard.take() {
                    state.seat.repeat.stop();
                    kb.release();
                }

                if has_ptr && state.seat.pointer.is_none() {
                    let wl = seat.get_pointer(qh, ());
                    let shape = state
                        .globals
                        .cursor_shape
                        .as_ref()
                        .map(|m| m.get_pointer(&wl, qh, ()));
                    let hold = state
                        .globals
                        .pointer_gestures
                        .as_ref()
                        .map(|g| g.get_hold_gesture(&wl, qh, ()));
                    state.seat.pointer = Some(Pointer::new(wl, shape, hold));
                } else if !has_ptr && let Some(p) = state.seat.pointer.take() {
                    p.release();
                }
            }
            wl_seat::Event::Name { name } => debug!("seat {name}"),
            _ => {}
        }
    }
}
