use std::os::fd::OwnedFd;

use what_core::{Keysym, Modifiers};
use xkbcommon::xkb;

const MODS: [(&str, Modifiers); 6] = [
    (xkb::MOD_NAME_SHIFT, Modifiers::SHIFT),
    (xkb::MOD_NAME_CTRL, Modifiers::CTRL),
    (xkb::MOD_NAME_ALT, Modifiers::ALT),
    (xkb::MOD_NAME_LOGO, Modifiers::SUPER),
    (xkb::MOD_NAME_CAPS, Modifiers::CAPS_LOCK),
    (xkb::MOD_NAME_NUM, Modifiers::NUM_LOCK),
];

pub(crate) struct Xkb {
    keymap: xkb::Keymap,
    state: xkb::State,
    index: [(xkb::ModIndex, Modifiers); 6],
}

impl Xkb {
    pub(crate) fn from_fd(fd: OwnedFd, size: u32) -> Option<Self> {
        let ctx = xkb::Context::new(xkb::CONTEXT_NO_FLAGS);
        // SAFETY: the compositor hands us a keymap fd of `size` bytes; xkbcommon maps it private
        let keymap = unsafe {
            xkb::Keymap::new_from_fd(
                &ctx,
                fd,
                size as usize,
                xkb::KEYMAP_FORMAT_TEXT_V1,
                xkb::KEYMAP_COMPILE_NO_FLAGS,
            )
        }
        .ok()
        .flatten()?;
        let state = xkb::State::new(&keymap);
        let index = MODS.map(|(name, m)| (keymap.mod_get_index(name), m));
        Some(Self {
            keymap,
            state,
            index,
        })
    }

    pub(crate) fn update_mask(&mut self, depressed: u32, latched: u32, locked: u32, group: u32) {
        self.state
            .update_mask(depressed, latched, locked, 0, 0, group);
    }

    pub(crate) fn mods(&self) -> Modifiers {
        let mut m = Modifiers::empty();
        for (idx, bit) in self.index {
            if idx != xkb::MOD_INVALID
                && self
                    .state
                    .mod_index_is_active(idx, xkb::STATE_MODS_EFFECTIVE)
            {
                m |= bit;
            }
        }
        m
    }

    pub(crate) fn consumed(&self, code: u32) -> Modifiers {
        let key = xkb::Keycode::new(code);
        let mut m = Modifiers::empty();
        for (idx, bit) in self.index {
            if idx != xkb::MOD_INVALID && self.state.mod_index_is_consumed(key, idx) {
                m |= bit;
            }
        }
        m
    }

    pub(crate) fn keysym(&self, code: u32) -> Keysym {
        Keysym(self.state.key_get_one_sym(xkb::Keycode::new(code)).raw())
    }

    pub(crate) fn raw_keysym(&self, code: u32) -> xkb::Keysym {
        self.state.key_get_one_sym(xkb::Keycode::new(code))
    }

    /// none for empty output and control characters (ctrl+q gives 0x11)
    pub(crate) fn text(&self, code: u32) -> Option<String> {
        printable(self.state.key_get_utf8(xkb::Keycode::new(code)))
    }

    pub(crate) fn repeats(&self, code: u32) -> bool {
        self.keymap.key_repeats(xkb::Keycode::new(code))
    }
}

pub(crate) fn printable(s: String) -> Option<String> {
    if s.is_empty() || s.chars().all(char::is_control) {
        None
    } else {
        Some(s)
    }
}
