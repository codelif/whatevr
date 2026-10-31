//! the daemon side of a list: a window kept exactly as the daemon's view
//! updates say, list keys for its rows, and the slide that swaps in a fresh
//! window around the view before the loaded end comes on screen.

use std::collections::{BTreeMap, HashMap};
use std::sync::Arc;

use what_proto::v2;
use what_proto::v2::oneof::request::Method;
use what_proto::v2::view::oneof::change::Change;
use what_proto::v2::view::{UpsertView, ViewUpdateView};

/// list keys at or past this are the sources' own rows: pads and days
pub const OWN: u32 = 1 << 31;

/// list keys for rows by (id, sort). a row whose sort changed moved, so it
/// gets a new key: to the list that's a row leaving and another arriving,
/// and nothing on screen around it moves
#[derive(Default)]
pub struct Keys {
    by_id: HashMap<Arc<str>, (Box<[u8]>, u32)>,
    next: u32,
}

impl Keys {
    fn key(&mut self, id: &str, sort: &[u8]) -> (Arc<str>, u32) {
        if let Some((k, (s, key))) = self.by_id.get_key_value(id)
            && **s == *sort
        {
            return (k.clone(), *key);
        }
        let key = self.next.max(1);
        self.next = key + 1;
        debug_assert!(key < OWN);
        if let Some(e) = self.by_id.get_mut(id) {
            *e = (sort.into(), key);
        } else {
            self.by_id.insert(id.into(), (sort.into(), key));
        }
        let (k, _) = self.by_id.get_key_value(id).expect("just put there");
        (k.clone(), key)
    }

    fn retain(&mut self, live: &HashMap<Arc<str>, u32>) {
        self.by_id.retain(|id, _| live.contains_key(id));
    }
}

pub struct Row<T> {
    pub id: Arc<str>,
    pub sort: Box<[u8]>,
    /// bumps on every upsert under the same key
    pub rev: u64,
    pub item: T,
}

/// one subscription's copy of its window
pub struct Window<T> {
    pub sub: Option<u64>,
    order: BTreeMap<Box<[u8]>, u32>,
    rows: HashMap<u32, Row<T>>,
    ids: HashMap<Arc<str>, u32>,
    /// the whole view's size, and how many sort before the window
    pub count: u64,
    pub offset: u64,
    pub ready: bool,
    /// (gone, replaced_by) from removes with a replacement
    pub replaced: Vec<(String, String)>,
}

impl<T> Default for Window<T> {
    fn default() -> Self {
        Self {
            sub: None,
            order: BTreeMap::new(),
            rows: HashMap::new(),
            ids: HashMap::new(),
            count: 0,
            offset: 0,
            ready: false,
            replaced: Vec::new(),
        }
    }
}

impl<T> Window<T> {
    pub fn len(&self) -> usize {
        self.order.len()
    }

    /// keys in view order
    pub fn keys(&self) -> impl DoubleEndedIterator<Item = u32> + '_ {
        self.order.values().copied()
    }

    pub fn get(&self, k: u32) -> Option<&Row<T>> {
        self.rows.get(&k)
    }

    pub fn key_of(&self, id: &str) -> Option<u32> {
        self.ids.get(id).copied()
    }

    /// the window starts at the view's first row
    pub fn at_start(&self) -> bool {
        self.offset == 0
    }

    /// the window ends at the view's last row
    pub fn at_end(&self) -> bool {
        self.offset + self.len() as u64 >= self.count
    }

    fn remove(&mut self, id: &str) -> Option<u32> {
        let k = self.ids.remove(id)?;
        if let Some(r) = self.rows.remove(&k) {
            self.order.remove(&r.sort);
        }
        Some(k)
    }

    /// applies one update whole. conv makes an item from an upsert, None
    /// for rows of another kind
    fn apply(
        &mut self,
        u: &ViewUpdateView<'_>,
        keys: &mut Keys,
        mut conv: impl FnMut(&UpsertView<'_>) -> Option<T>,
    ) {
        if u.reset {
            self.order.clear();
            self.rows.clear();
            self.ids.clear();
        }
        for c in u.changes.iter() {
            match &c.change {
                Some(Change::Upsert(up)) => {
                    let Some(item) = conv(up) else {
                        continue;
                    };
                    let (id, key) = keys.key(up.id, up.sort);
                    let rev = match self.ids.get(up.id) {
                        Some(&old) if old == key => {
                            let r = self.rows.remove(&old).expect("ids and rows agree");
                            self.order.remove(&r.sort);
                            r.rev + 1
                        }
                        Some(_) => {
                            self.remove(up.id);
                            0
                        }
                        None => 0,
                    };
                    let sort: Box<[u8]> = up.sort.into();
                    self.order.insert(sort.clone(), key);
                    self.ids.insert(id.clone(), key);
                    self.rows.insert(
                        key,
                        Row {
                            id,
                            sort,
                            rev,
                            item,
                        },
                    );
                }
                Some(Change::Remove(r)) => {
                    self.remove(r.id);
                    if !r.replaced_by.is_empty() {
                        self.replaced
                            .push((r.id.to_string(), r.replaced_by.to_string()));
                    }
                }
                None => {}
            }
        }
        if let Some(p) = u.position.as_option() {
            self.count = p.count;
            self.offset = p.offset;
        }
        if u.ready.as_option().is_some() {
            self.ready = true;
        }
    }
}

pub enum Applied {
    /// not this slide's
    No,
    /// the shown window changed in place
    Changed,
    /// a fresh window took over; the old one's sub is to unsubscribe
    Swapped(Option<u64>),
    /// the fresh window is still filling
    Filling,
}

/// the shown window and the one coming to replace it
pub struct Slide<T> {
    pub cur: Window<T>,
    next: Option<Window<T>>,
    /// next's subscribe, until answered
    asked: Option<u64>,
    keys: Keys,
}

impl<T> Default for Slide<T> {
    fn default() -> Self {
        Self {
            cur: Window::default(),
            next: None,
            asked: None,
            keys: Keys::default(),
        }
    }
}

impl<T> Slide<T> {
    /// a window is on its way
    pub fn busy(&self) -> bool {
        self.next.is_some()
    }

    /// subscribe request req opens the next window. a window still coming
    /// is dropped; its sub, if it had one yet, is to unsubscribe
    pub fn asked(&mut self, req: u64) -> Option<u64> {
        self.asked = Some(req);
        self.next.replace(Window::default()).and_then(|w| w.sub)
    }

    /// the subscribe numbered req was answered with sub; false if it wasn't
    /// this slide's
    pub fn answered(&mut self, req: u64, sub: u64) -> bool {
        if self.asked != Some(req) {
            return false;
        }
        self.asked = None;
        if let Some(n) = &mut self.next {
            n.sub = Some(sub);
        }
        true
    }

    /// the subscribe numbered req failed
    pub fn refused(&mut self, req: u64) -> bool {
        if self.asked != Some(req) {
            return false;
        }
        self.asked = None;
        self.next = None;
        true
    }

    /// the connection dropped with every sub on it; what's shown stays
    pub fn disconnected(&mut self) {
        self.next = None;
        self.asked = None;
        self.cur.sub = None;
    }

    pub fn update(
        &mut self,
        u: &ViewUpdateView<'_>,
        conv: impl FnMut(&UpsertView<'_>) -> Option<T>,
    ) -> Applied {
        if self.cur.sub == Some(u.sub) {
            self.cur.apply(u, &mut self.keys, conv);
            return Applied::Changed;
        }
        let Some(n) = self.next.as_mut().filter(|n| n.sub == Some(u.sub)) else {
            return Applied::No;
        };
        n.apply(u, &mut self.keys, conv);
        if !n.ready {
            return Applied::Filling;
        }
        let old = std::mem::replace(&mut self.cur, self.next.take().expect("checked above"));
        self.keys.retain(&self.cur.ids);
        Applied::Swapped(old.sub)
    }
}

pub fn subscribe(view: v2::oneof::subscribe::View, limit: u32) -> Method {
    Method::Subscribe(Box::new(v2::Subscribe {
        limit,
        view: Some(view),
    }))
}

pub fn unsubscribe(sub: u64) -> Method {
    Method::Unsubscribe(Box::new(v2::Unsubscribe { sub }))
}

pub fn session(focused: bool, chat: &str) -> Method {
    Method::SessionUpdate(Box::new(v2::SessionUpdate {
        focused,
        active_chat_id: chat.into(),
        // no notifications here yet, the daemon keeps showing its own
        shows_notifications: false,
    }))
}

pub fn mark_read(chat: &str, up_to: &str) -> Method {
    Method::ChatMarkRead(Box::new(v2::ChatMarkRead {
        chat_id: chat.into(),
        up_to_message_id: up_to.into(),
    }))
}

/// where a fresh window should sit so `centre` is in its middle: the live
/// edge when that reaches it, else around the index
pub enum Anchor {
    Edge,
    Index(u64),
}

/// edge_at is the live edge's index in the view
pub fn anchor(centre: u64, limit: u32, edge_at: u64) -> Anchor {
    let half = u64::from(limit / 2);
    if centre.abs_diff(edge_at) < half {
        Anchor::Edge
    } else {
        Anchor::Index(centre)
    }
}
