use std::ops::{BitAnd, BitOr, BitOrAssign};

/// an X11/xkb keysym value
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct Keysym(pub u32);

impl Keysym {
    /// latin-1 keysyms equal their code point; everything else is 0x0100_0000 + code point
    pub const fn from_char(c: char) -> Self {
        let cp = c as u32;
        if (cp >= 0x20 && cp <= 0x7e) || (cp >= 0xa0 && cp <= 0xff) {
            Self(cp)
        } else {
            Self(0x0100_0000 + cp)
        }
    }

    /// ascii and latin-1 only, enough for shortcut matching under caps lock
    pub const fn to_lower(self) -> Self {
        match self.0 {
            0x41..=0x5a | 0xc0..=0xd6 | 0xd8..=0xde => Self(self.0 + 0x20),
            _ => self,
        }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub struct Modifiers(u8);

impl Modifiers {
    pub const SHIFT: Self = Self(1);
    pub const CTRL: Self = Self(1 << 1);
    pub const ALT: Self = Self(1 << 2);
    pub const SUPER: Self = Self(1 << 3);
    pub const CAPS_LOCK: Self = Self(1 << 4);
    pub const NUM_LOCK: Self = Self(1 << 5);

    /// the ones a shortcut can ask for; lock state never decides a match
    pub const SHORTCUT: Self = Self(0b1111);

    pub const fn empty() -> Self {
        Self(0)
    }

    pub const fn contains(self, other: Self) -> bool {
        self.0 & other.0 == other.0
    }

    pub const fn is_empty(self) -> bool {
        self.0 == 0
    }

    pub const fn difference(self, other: Self) -> Self {
        Self(self.0 & !other.0)
    }
}

impl BitOr for Modifiers {
    type Output = Self;
    fn bitor(self, rhs: Self) -> Self {
        Self(self.0 | rhs.0)
    }
}

impl BitOrAssign for Modifiers {
    fn bitor_assign(&mut self, rhs: Self) {
        self.0 |= rhs.0;
    }
}

impl BitAnd for Modifiers {
    type Output = Self;
    fn bitand(self, rhs: Self) -> Self {
        Self(self.0 & rhs.0)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum KeyState {
    Pressed,
    Released,
    Repeated,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct KeyEvent {
    pub keysym: Keysym,
    /// what the key types, after compose; none for control characters and releases
    pub text: Option<String>,
    pub state: KeyState,
    pub mods: Modifiers,
    /// modifiers the keymap used to produce `keysym` (shift for 'Q', etc)
    pub consumed: Modifiers,
    /// evdev keycode + 8, as xkb counts
    pub keycode: u32,
    pub time_ms: u32,
}

impl KeyEvent {
    /// matches on keysym with consumed modifiers removed, so shortcuts follow the
    /// layout and not the physical key. shift that only changed letter case still
    /// counts, otherwise ctrl+shift+q would fire ctrl+q.
    pub fn matches(&self, keysym: Keysym, mods: Modifiers) -> bool {
        if self.state == KeyState::Released {
            return false;
        }
        let mut consumed = self.consumed;
        if self.keysym.to_lower() != self.keysym {
            consumed = consumed.difference(Modifiers::SHIFT);
        }
        let active = self.mods.difference(consumed) & Modifiers::SHORTCUT;
        active == mods && self.keysym.to_lower() == keysym.to_lower()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ev(sym: char, mods: Modifiers, consumed: Modifiers) -> KeyEvent {
        KeyEvent {
            keysym: Keysym::from_char(sym),
            text: None,
            state: KeyState::Pressed,
            mods,
            consumed,
            keycode: 0,
            time_ms: 0,
        }
    }

    #[test]
    fn ctrl_q() {
        let q = Keysym::from_char('q');
        assert!(ev('q', Modifiers::CTRL, Modifiers::empty()).matches(q, Modifiers::CTRL));
        // caps lock turns q into Q and consumes lock
        let caps = Modifiers::CTRL | Modifiers::CAPS_LOCK;
        assert!(ev('Q', caps, Modifiers::CAPS_LOCK).matches(q, Modifiers::CTRL));
        // ctrl+shift+q is its own shortcut, not ctrl+q
        let shifted = Modifiers::CTRL | Modifiers::SHIFT;
        assert!(!ev('Q', shifted, Modifiers::SHIFT).matches(q, Modifiers::CTRL));
        assert!(ev('Q', shifted, Modifiers::SHIFT).matches(q, shifted));
        // shift consumed for a non-letter (ctrl+shift+1 typing '!') stays consumed
        let bang = Keysym::from_char('!');
        assert!(ev('!', shifted, Modifiers::SHIFT).matches(bang, Modifiers::CTRL));
        assert!(!ev('q', Modifiers::empty(), Modifiers::empty()).matches(q, Modifiers::CTRL));
    }

    #[test]
    fn latin1_keysyms() {
        assert_eq!(Keysym::from_char('a').0, 0x61);
        assert_eq!(Keysym::from_char('é').0, 0xe9);
        assert_eq!(Keysym::from_char('क').0, 0x0100_0915);
    }
}
