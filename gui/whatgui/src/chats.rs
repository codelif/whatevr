//! the chat list over the daemon's chats view, slid along in windows of
//! LIMIT rows so a list of any length scrolls through.

use std::cell::Cell;
use std::collections::HashMap;
use std::sync::Arc;

use what_chat::content::ChatInfo;
use what_chat::shell::ChatList;
use what_proto::v2;
use what_proto::v2::oneof::chats_view;
use what_proto::v2::oneof::subscribe::View;
use what_ui::{Dir, ListSource};

use crate::clock::Clock;
use crate::daemon::{Anchor, Slide, anchor};
use crate::rows::ChatItem;

pub const LIMIT: u32 = 1024;
/// rows left before the loaded end when a fresh window is asked for
const MARGIN: usize = 256;

pub struct Chats {
    pub slide: Slide<ChatItem>,
    clock: Clock,
    today: i32,
    order: Vec<u32>,
    at: HashMap<u32, usize>,
    want: Cell<Option<u64>>,
}

impl Chats {
    pub fn new(clock: Clock) -> Self {
        Self {
            slide: Slide::default(),
            today: clock.today(),
            clock,
            order: Vec::new(),
            at: HashMap::new(),
            want: Cell::new(None),
        }
    }

    /// after the window changed
    pub fn rebuild(&mut self) {
        self.order.clear();
        self.order.extend(self.slide.cur.keys());
        self.at.clear();
        self.at
            .extend(self.order.iter().enumerate().map(|(i, &k)| (k, i)));
    }

    /// true when the day turned and every time label is stale
    pub fn tick(&mut self) -> bool {
        let today = self.clock.today();
        std::mem::replace(&mut self.today, today) != today
    }

    /// the subscribe for the window the view wants next, if any
    pub fn wanted(&self) -> Option<View> {
        let centre = self.want.take()?;
        Some(Self::view(anchor(centre, LIMIT, 0)))
    }

    pub fn view(a: Anchor) -> View {
        View::Chats(Box::new(v2::ChatsView {
            filter: v2::ChatFilter::CHAT_FILTER_ALL.into(),
            archived: false,
            anchor: Some(match a {
                Anchor::Edge => chats_view::Anchor::Latest(Box::default()),
                Anchor::Index(i) => chats_view::Anchor::Index(i),
            }),
        }))
    }

    /// where the view is now, for a window after a reconnect
    pub fn here(&self) -> Anchor {
        let w = &self.slide.cur;
        if w.at_start() {
            Anchor::Edge
        } else {
            Anchor::Index(w.offset + w.len() as u64 / 2)
        }
    }

    pub fn id(&self, k: u32) -> Option<&Arc<str>> {
        self.slide.cur.get(k).map(|r| &r.id)
    }

    pub fn key_of(&self, id: &str) -> Option<u32> {
        self.slide.cur.key_of(id)
    }

    pub fn row(&self, id: &str) -> Option<&ChatItem> {
        let k = self.key_of(id)?;
        self.slide.cur.get(k).map(|r| &r.item)
    }
}

impl ListSource for Chats {
    type Key = u32;
    type Content = Arc<ChatInfo>;

    fn newest(&self) -> Option<u32> {
        self.order.last().copied()
    }

    fn neighbour(&self, k: u32, dir: Dir) -> Option<u32> {
        let i = *self.at.get(&k)?;
        match dir {
            Dir::Older => i.checked_sub(1).map(|i| self.order[i]),
            Dir::Newer => self.order.get(i + 1).copied(),
        }
    }

    fn contains(&self, k: u32) -> bool {
        self.at.contains_key(&k)
    }

    fn more(&self, k: u32, dir: Dir) -> bool {
        let w = &self.slide.cur;
        match dir {
            Dir::Older => self.order.first() == Some(&k) && !w.at_start(),
            Dir::Newer => self.order.last() == Some(&k) && !w.at_end(),
        }
    }

    fn revision(&self, k: u32) -> u64 {
        let rev = self.slide.cur.get(k).map_or(0, |r| r.rev);
        rev | (self.today as u64) << 40
    }

    fn content(&self, k: u32) -> Arc<ChatInfo> {
        self.slide
            .cur
            .get(k)
            .expect("the list asks for rows it has")
            .item
            .info(&self.clock, self.today)
    }

    fn fraction(&self, k: u32) -> f32 {
        let w = &self.slide.cur;
        let i = self.at.get(&k).copied().unwrap_or(0) as u64;
        ((w.offset + i) as f32 / w.count.saturating_sub(1).max(1) as f32).clamp(0.0, 1.0)
    }

    fn at_fraction(&self, f: f32) -> Option<u32> {
        let w = &self.slide.cur;
        if w.count == 0 {
            return self.newest();
        }
        let t = (f.clamp(0.0, 1.0) * (w.count - 1) as f32).round() as u64;
        match t.checked_sub(w.offset) {
            Some(i) if (i as usize) < self.order.len() => Some(self.order[i as usize]),
            _ => {
                self.want.set(Some(t));
                None
            }
        }
    }

    fn total(&self) -> Option<usize> {
        let w = &self.slide.cur;
        (w.at_start() && w.at_end() && w.len() <= 200).then_some(w.len())
    }
}

impl ChatList for Chats {
    fn count(&self) -> usize {
        self.slide.cur.count as usize
    }

    fn showing(&mut self, first: u32, last: u32) -> bool {
        let (Some(&a), Some(&b)) = (self.at.get(&first), self.at.get(&last)) else {
            return false;
        };
        let w = &self.slide.cur;
        if (!w.at_start() && a < MARGIN) || (!w.at_end() && self.order.len() - 1 - b < MARGIN) {
            self.want.set(Some(w.offset + (a + b) as u64 / 2));
        }
        false
    }
}
