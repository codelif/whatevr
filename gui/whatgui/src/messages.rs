//! the open chat over the daemon's messages view, slid along in windows of
//! LIMIT messages. day rows, run edges and the two pads are made here from
//! the window's order; a day row or pad only shows where the window knows
//! it's there.

use std::cell::Cell;
use std::collections::HashMap;
use std::sync::Arc;

use what_chat::content::{Entry, Item};
use what_chat::shell::Conversation;
use what_proto::v2;
use what_proto::v2::oneof::messages_view;
use what_proto::v2::oneof::subscribe::View;
use what_ui::{Dir, ListSource};

use crate::clock::Clock;
use crate::daemon::{Anchor, OWN, Slide, anchor};
use crate::rows::{Kind, MsgItem};

/// the messages view's cap
pub const LIMIT: u32 = 256;
const MARGIN: usize = 96;

const TOP: u32 = u32::MAX - 1;
const BOTTOM: u32 = u32::MAX;

fn day_key(day: i32) -> u32 {
    OWN | (day as u32 & 0x3fff_ffff)
}

pub struct Messages {
    pub slide: Slide<MsgItem>,
    /// the chat the window shows, and its name
    pub chat: Option<Arc<str>>,
    pub name: String,
    /// rows top to bottom, pads and days included
    order: Vec<u32>,
    at: HashMap<u32, usize>,
    /// a message key's place among the window's messages
    rank: HashMap<u32, usize>,
    days: HashMap<u32, Arc<Entry>>,
    /// first and last of a run
    runs: HashMap<u32, (bool, bool)>,
    clock: Clock,
    want: Cell<Option<u64>>,
}

impl Messages {
    pub fn new(clock: Clock) -> Self {
        Self {
            slide: Slide::default(),
            chat: None,
            name: String::new(),
            order: Vec::new(),
            at: HashMap::new(),
            rank: HashMap::new(),
            days: HashMap::new(),
            runs: HashMap::new(),
            clock,
            want: Cell::new(None),
        }
    }

    /// after the window changed
    pub fn rebuild(&mut self) {
        let w = &self.slide.cur;
        self.order.clear();
        self.rank.clear();
        self.days.clear();
        self.runs.clear();
        if w.ready && w.at_start() {
            self.order.push(TOP);
        }
        let mut prev: Option<(i32, Option<&str>)> = None;
        for (i, k) in w.keys().enumerate() {
            let r = &w.get(k).expect("keys and rows agree").item;
            self.rank.insert(k, i);
            // the first message's day row only shows at the very start; a
            // window that starts mid-history doesn't know where its day began
            let new_day = prev.map_or(w.at_start(), |p| p.0 != r.day);
            if new_day {
                let dk = day_key(r.day);
                let (day, date) = self.clock.day_row(r.t_ms);
                self.days.insert(dk, Arc::new(Entry::Day { day, date }));
                self.order.push(dk);
            }
            let who = match r.kind {
                Kind::Msg(_) => Some(&*r.who),
                Kind::System(_) => None,
            };
            let joins = !new_day && who.is_some() && prev.is_some_and(|p| p.1 == who);
            if joins && let Some(&pk) = self.order.last() {
                self.runs.entry(pk).and_modify(|e| e.1 = false);
            }
            if who.is_some() {
                self.runs.insert(k, (!joins, true));
            }
            self.order.push(k);
            prev = Some((r.day, who));
        }
        if w.ready && w.at_end() {
            self.order.push(BOTTOM);
        }
        self.at.clear();
        self.at
            .extend(self.order.iter().enumerate().map(|(i, &k)| (k, i)));
    }

    /// drops a window asked for while another chat showed
    pub fn forget(&self) {
        self.want.set(None);
    }

    pub fn wanted(&self, chat: &str) -> Option<View> {
        let centre = self.want.take()?;
        let edge = self.slide.cur.count.saturating_sub(1);
        Some(Self::view(chat, anchor(centre, LIMIT, edge)))
    }

    pub fn view(chat: &str, a: Anchor) -> View {
        View::Messages(Box::new(v2::MessagesView {
            chat_id: chat.into(),
            anchor: Some(match a {
                Anchor::Edge => messages_view::Anchor::Latest(Box::default()),
                Anchor::Index(i) => messages_view::Anchor::Index(i),
            }),
        }))
    }

    /// where the view is now, for a window after a reconnect
    pub fn here(&self) -> Anchor {
        let w = &self.slide.cur;
        if w.at_end() {
            Anchor::Edge
        } else {
            Anchor::Index(w.offset + w.len() as u64 / 2)
        }
    }

    /// the message id and sort under a list key
    pub fn message(&self, k: u32) -> Option<(&Arc<str>, &[u8])> {
        let r = self.slide.cur.get(k)?;
        matches!(r.item.kind, Kind::Msg(_)).then_some((&r.id, &r.sort[..]))
    }

    /// the nearest message at or after display place i, for places that
    /// are pads or days
    fn rank_near(&self, i: usize) -> usize {
        self.order[i..]
            .iter()
            .chain(self.order[..i].iter().rev())
            .find_map(|k| self.rank.get(k).copied())
            .unwrap_or(0)
    }
}

impl ListSource for Messages {
    type Key = u32;
    type Content = Item;

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
            Dir::Older => self.order.first() == Some(&k) && k != TOP && !w.at_start(),
            Dir::Newer => self.order.last() == Some(&k) && k != BOTTOM && !w.at_end(),
        }
    }

    fn revision(&self, k: u32) -> u64 {
        let rev = self.slide.cur.get(k).map_or(0, |r| r.rev);
        let (first, last) = self.runs.get(&k).copied().unwrap_or((true, true));
        rev << 2 | (first as u64) << 1 | last as u64
    }

    fn content(&self, k: u32) -> Item {
        match k {
            TOP => return Item::Top,
            BOTTOM => return Item::Bottom,
            _ => {}
        }
        if let Some(d) = self.days.get(&k) {
            return Item::Entry(d.clone());
        }
        let r = self
            .slide
            .cur
            .get(k)
            .expect("the list asks for rows it has");
        let e = match &r.item.kind {
            Kind::System(s) => Entry::System(s.clone()),
            Kind::Msg(m) => {
                let (first, last) = self.runs.get(&k).copied().unwrap_or((true, true));
                let mut m = m.clone();
                m.first = first;
                m.last = last;
                Entry::Msg(m)
            }
        };
        Item::Entry(Arc::new(e))
    }

    fn fraction(&self, k: u32) -> f32 {
        let w = &self.slide.cur;
        let r = match k {
            TOP => return 0.0,
            BOTTOM => return 1.0,
            k => self.at.get(&k).map_or(0, |&i| self.rank_near(i)),
        };
        ((w.offset + r as u64) as f32 / w.count.saturating_sub(1).max(1) as f32).clamp(0.0, 1.0)
    }

    fn at_fraction(&self, f: f32) -> Option<u32> {
        let w = &self.slide.cur;
        if w.count == 0 {
            return self.newest();
        }
        if f >= 1.0 && w.at_end() {
            return self.order.last().copied();
        }
        if f <= 0.0 && w.at_start() {
            return self.order.first().copied();
        }
        let t = (f.clamp(0.0, 1.0) * (w.count - 1) as f32).round() as u64;
        match t.checked_sub(w.offset) {
            Some(i) if (i as usize) < w.len() => w.keys().nth(i as usize),
            _ => {
                self.want.set(Some(t));
                None
            }
        }
    }

    fn total(&self) -> Option<usize> {
        let w = &self.slide.cur;
        (w.at_start() && w.at_end() && self.order.len() <= 200).then_some(self.order.len())
    }
}

impl Conversation for Messages {
    fn title(&self) -> (&str, &str) {
        (&self.name, "")
    }

    fn newest_message(&self) -> Option<u32> {
        self.slide.cur.keys().next_back()
    }

    fn showing(&mut self, first: u32, last: u32) -> bool {
        let (Some(&a), Some(&b)) = (self.at.get(&first), self.at.get(&last)) else {
            return false;
        };
        let w = &self.slide.cur;
        if (!w.at_start() && a < MARGIN) || (!w.at_end() && self.order.len() - 1 - b < MARGIN) {
            let mid = self.rank_near((a + b) / 2);
            self.want.set(Some(w.offset + mid as u64));
        }
        false
    }
}
