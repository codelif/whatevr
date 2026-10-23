mod compose;
mod repeat;
mod xkb;

pub(crate) use repeat::Repeat;

use log::{debug, warn};
use wayland_client::protocol::{wl_keyboard, wl_surface};
use wayland_client::{Connection, Dispatch, Proxy, QueueHandle, WEnum};
use what_core::{KeyEvent, KeyState, Keysym, WindowId};

use crate::{Event, State};
use compose::{Compose, Composed};
use xkb::Xkb;

pub(crate) struct Keyboard {
    wl: wl_keyboard::WlKeyboard,
    xkb: Option<Xkb>,
    compose: Compose,
    focus: Option<WindowId>,
}

impl Keyboard {
    pub(crate) fn new(wl: wl_keyboard::WlKeyboard) -> Self {
        Self {
            wl,
            xkb: None,
            compose: Compose::from_env(),
            focus: None,
        }
    }

    pub(crate) fn release(self) {
        if self.wl.version() >= 3 {
            self.wl.release();
        }
    }

    pub(crate) fn unfocus(&mut self, id: WindowId) {
        if self.focus == Some(id) {
            self.focus = None;
        }
    }

    fn event(
        &mut self,
        code: u32,
        state: KeyState,
        time_ms: u32,
        compose: bool,
    ) -> Option<KeyEvent> {
        let xkb = self.xkb.as_ref()?;
        let mut keysym = xkb.keysym(code);
        let mut text = if state == KeyState::Released {
            None
        } else {
            xkb.text(code)
        };
        if compose {
            match self.compose.feed(xkb.raw_keysym(code)) {
                Composed::Passthrough => {}
                Composed::Swallowed => return None,
                Composed::Done { text: t, keysym: k } => {
                    text = t;
                    if let Some(k) = k {
                        keysym = Keysym(k.raw());
                    }
                }
            }
        }
        Some(KeyEvent {
            keysym,
            text,
            state,
            mods: xkb.mods(),
            consumed: xkb.consumed(code),
            keycode: code,
            time_ms,
        })
    }
}

fn surface_window(surface: &wl_surface::WlSurface) -> Option<WindowId> {
    surface.data::<WindowId>().copied()
}

impl State {
    /// the loop calls this when the repeat timerfd fires
    pub(crate) fn repeat_fire(&mut self) {
        let Some((id, code, time_ms)) = self.seat.repeat.fire() else {
            return;
        };
        let Some(kb) = self.seat.keyboard.as_mut() else {
            return;
        };
        if let Some(ev) = kb.event(code, KeyState::Repeated, time_ms, false) {
            self.events.push(Event::Key(id, ev));
        }
    }
}

impl Dispatch<wl_keyboard::WlKeyboard, ()> for State {
    fn event(
        state: &mut Self,
        _: &wl_keyboard::WlKeyboard,
        event: wl_keyboard::Event,
        _: &(),
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        let State { seat, events, .. } = state;
        let Some(kb) = seat.keyboard.as_mut() else {
            return;
        };
        match event {
            wl_keyboard::Event::Keymap { format, fd, size } => {
                if format != WEnum::Value(wl_keyboard::KeymapFormat::XkbV1) {
                    warn!("keymap format {format:?} not supported");
                    return;
                }
                kb.xkb = Xkb::from_fd(fd, size);
                if kb.xkb.is_none() {
                    warn!("keymap failed to compile");
                }
            }
            wl_keyboard::Event::Enter { surface, .. } => {
                if let Some(id) = surface_window(&surface) {
                    kb.focus = Some(id);
                    events.push(Event::Focus(id, true));
                }
            }
            wl_keyboard::Event::Leave { surface, .. } => {
                seat.repeat.stop();
                kb.compose.reset();
                if let Some(id) = surface_window(&surface) {
                    kb.unfocus(id);
                    events.push(Event::Focus(id, false));
                }
            }
            wl_keyboard::Event::Key {
                key,
                state: key_state,
                time,
                ..
            } => {
                let Some(id) = kb.focus else { return };
                // evdev to xkb keycodes
                let code = key + 8;
                match key_state {
                    WEnum::Value(wl_keyboard::KeyState::Pressed) => {
                        if let Some(ev) = kb.event(code, KeyState::Pressed, time, true) {
                            events.push(Event::Key(id, ev));
                            if kb.xkb.as_ref().is_some_and(|x| x.repeats(code)) {
                                seat.repeat.start(id, code, time);
                            }
                        }
                    }
                    WEnum::Value(wl_keyboard::KeyState::Released) => {
                        seat.repeat.stop_key(code);
                        if let Some(ev) = kb.event(code, KeyState::Released, time, false) {
                            events.push(Event::Key(id, ev));
                        }
                    }
                    WEnum::Value(wl_keyboard::KeyState::Repeated) => {
                        seat.repeat.compositor_repeats();
                        if let Some(ev) = kb.event(code, KeyState::Repeated, time, false) {
                            events.push(Event::Key(id, ev));
                        }
                    }
                    // unknown states are not releases; ignore them like mpv does
                    other => debug!("key state {other:?} ignored"),
                }
            }
            wl_keyboard::Event::Modifiers {
                mods_depressed,
                mods_latched,
                mods_locked,
                group,
                ..
            } => {
                let Some(xkb) = kb.xkb.as_mut() else { return };
                xkb.update_mask(mods_depressed, mods_latched, mods_locked, group);
                if let Some(id) = kb.focus {
                    events.push(Event::Modifiers(id, xkb.mods()));
                }
            }
            wl_keyboard::Event::RepeatInfo { rate, delay } => seat.repeat.set_info(rate, delay),
            _ => {}
        }
    }
}
