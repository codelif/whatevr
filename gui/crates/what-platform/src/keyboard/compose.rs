use std::ffi::OsString;

use log::debug;
use xkbcommon::xkb::{self, compose};

pub(crate) enum Composed {
    /// not part of a sequence, use the key as is
    Passthrough,
    /// swallowed: mid-sequence, or the sequence was cancelled
    Swallowed,
    Done {
        text: Option<String>,
        keysym: Option<xkb::Keysym>,
    },
}

pub(crate) struct Compose {
    state: Option<compose::State>,
}

impl Compose {
    pub(crate) fn from_env() -> Self {
        let ctx = xkb::Context::new(xkb::CONTEXT_NO_FLAGS);
        let locale = locale();
        let state = compose::Table::new_from_locale(&ctx, &locale, compose::COMPILE_NO_FLAGS)
            .ok()
            .map(|t| compose::State::new(&t, compose::STATE_NO_FLAGS));
        if state.is_none() {
            debug!("no compose table for locale {locale:?}");
        }
        Self { state }
    }

    pub(crate) fn feed(&mut self, sym: xkb::Keysym) -> Composed {
        let Some(st) = self.state.as_mut() else {
            return Composed::Passthrough;
        };
        if matches!(st.feed(sym), compose::FeedResult::Ignored) {
            return Composed::Passthrough;
        }
        match st.status() {
            compose::Status::Composing => Composed::Swallowed,
            compose::Status::Cancelled => {
                st.reset();
                Composed::Swallowed
            }
            compose::Status::Composed => {
                let done = Composed::Done {
                    text: st.utf8().and_then(super::xkb::printable),
                    keysym: st.keysym(),
                };
                st.reset();
                done
            }
            compose::Status::Nothing => Composed::Passthrough,
        }
    }

    pub(crate) fn reset(&mut self) {
        if let Some(st) = self.state.as_mut() {
            st.reset();
        }
    }
}

fn locale() -> OsString {
    ["LC_ALL", "LC_CTYPE", "LANG"]
        .into_iter()
        .filter_map(std::env::var_os)
        .find(|v| !v.is_empty())
        .unwrap_or_else(|| "C".into())
}
