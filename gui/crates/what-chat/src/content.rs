//! what rows are drawn from: plain owned data, filled by whoever has the
//! chat, the daemon's rows in whatgui or the fakes in the lab.

use std::sync::Arc;

/// who sent a message, as a bubble names them
#[derive(Clone, Debug)]
pub struct Sender {
    pub name: Arc<str>,
    /// into the theme's name colours
    pub color: u8,
}

pub fn initials(name: &str) -> String {
    name.split_whitespace()
        .filter_map(|w| w.chars().next())
        .take(2)
        .collect()
}

#[derive(Clone)]
pub enum Body {
    Text,
    /// width / height
    Photo {
        ratio: f32,
        hue: f32,
    },
    Link {
        title: String,
        host: String,
        hue: f32,
        /// the site's image is wide: big preview, else a side thumb
        wide: bool,
    },
}

#[derive(Clone)]
pub struct Quote {
    /// None is you
    pub from: Option<Sender>,
    pub text: String,
}

#[derive(Clone)]
pub struct Msg {
    /// None is you
    pub from: Option<Sender>,
    /// the caption for a photo
    pub text: String,
    pub body: Body,
    pub quote: Option<Quote>,
    pub reactions: Vec<(String, u32)>,
    pub time: String,
    /// 1 sent, 2 delivered, 3 read
    pub state: u8,
    /// first and last of a run from one sender
    pub first: bool,
    pub last: bool,
}

pub enum Entry {
    Day { day: String, date: String },
    System(String),
    Msg(Msg),
}

/// what a chat row is built from
#[derive(Clone)]
pub enum Item {
    Entry(Arc<Entry>),
    /// the chat's top padding, above the very first message
    Top,
    /// the chat's bottom padding, under the newest message
    Bottom,
}

/// one chat in the sidebar
pub struct ChatInfo {
    pub name: String,
    pub group: bool,
    pub color: u8,
    /// None: they sent it (or nobody in a group, a 1:1); Some("") is you
    pub from: Option<String>,
    pub preview: String,
    /// your last message's ticks, when `from` is you
    pub state: u8,
    pub time: String,
    pub unread: u32,
}
