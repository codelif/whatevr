//! daemon rows to what-chat's content. step 5 draws text: a text message's
//! own text, the daemon's one line fallback for everything else.

use std::sync::Arc;

use what_chat::content::{Body, ChatInfo, Msg, Quote, Sender};
use what_proto::v2::view::oneof::message_row::Body as MsgBody;
use what_proto::v2::view::{ChatRowView, MessageRowView, PersonView};
use what_proto::v2::{ChatType, MessageStatus};

use crate::clock::Clock;

pub enum Kind {
    Msg(Msg),
    System(String),
}

/// a message as stored; runs and day rows come from its neighbours
pub struct MsgItem {
    /// who a run is joined on: the sender's id, empty for you
    pub who: Arc<str>,
    pub day: i32,
    pub t_ms: i64,
    pub kind: Kind,
}

/// a name colour per person, the same in every chat
pub fn color(id: &str) -> u8 {
    // fnv-1a
    let h = id.bytes().fold(0x811c_9dc5u32, |h, b| {
        (h ^ u32::from(b)).wrapping_mul(0x0100_0193)
    });
    (h % 251) as u8
}

fn sender(p: &PersonView<'_>) -> Option<Sender> {
    if p.self_ {
        return None;
    }
    let name = if !p.name.is_empty() {
        p.name
    } else if !p.phone.is_empty() {
        p.phone
    } else {
        "?"
    };
    Some(Sender {
        name: name.into(),
        color: color(p.id),
    })
}

/// the ticks: 1 sent, 2 delivered, 3 read. pending and failed have no icon
/// of their own yet and show one tick
fn ticks(s: Option<MessageStatus>) -> u8 {
    match s {
        Some(MessageStatus::MESSAGE_STATUS_DELIVERED) => 2,
        Some(MessageStatus::MESSAGE_STATUS_READ | MessageStatus::MESSAGE_STATUS_PLAYED) => 3,
        _ => 1,
    }
}

pub fn message(m: &MessageRowView<'_>, clock: &Clock) -> MsgItem {
    let day = clock.day(m.t_ms);
    let who: Arc<str> = if m.from_me {
        "".into()
    } else {
        m.sender.id.into()
    };
    if let Some(MsgBody::System(_)) = &m.body {
        return MsgItem {
            who,
            day,
            t_ms: m.t_ms,
            kind: Kind::System(m.fallback.to_string()),
        };
    }
    let text = match &m.body {
        Some(MsgBody::TextBody(_)) if !m.revoked && !m.text.is_empty() => m.text,
        _ => m.fallback,
    };
    let quote = m.reply_to.as_option().map(|q| Quote {
        from: sender(&q.sender),
        text: q.text.to_string(),
    });
    let msg = Msg {
        from: if m.from_me { None } else { sender(&m.sender) },
        text: text.to_string(),
        body: Body::Text,
        quote,
        reactions: m
            .reaction_counts
            .iter()
            .map(|r| (r.emoji.to_string(), r.count))
            .collect(),
        time: clock.hm(m.t_ms),
        state: ticks(m.status.as_known()),
        first: true,
        last: true,
    };
    MsgItem {
        who,
        day,
        t_ms: m.t_ms,
        kind: Kind::Msg(msg),
    }
}

/// a chat row as stored; its time label is made when it's drawn, so it
/// rolls over at midnight
pub struct ChatItem {
    pub name: String,
    pub group: bool,
    pub color: u8,
    pub from_me: bool,
    pub preview: String,
    pub state: u8,
    pub last_ms: i64,
    pub unread: u32,
    pub marked_unread: bool,
}

pub fn chat(c: &ChatRowView<'_>) -> ChatItem {
    let p = c.preview.as_option();
    ChatItem {
        name: c.name.to_string(),
        group: matches!(
            c.r#type.as_known(),
            Some(ChatType::CHAT_TYPE_GROUP | ChatType::CHAT_TYPE_COMMUNITY)
        ),
        color: color(c.id),
        from_me: p.is_some_and(|p| p.from_me),
        preview: p.map_or_else(String::new, |p| p.text.to_string()),
        state: ticks(p.and_then(|p| p.status.as_known())),
        last_ms: c.last_ms,
        unread: c.unread,
        marked_unread: c.marked_unread,
    }
}

impl ChatItem {
    pub fn info(&self, clock: &Clock, today: i32) -> Arc<ChatInfo> {
        Arc::new(ChatInfo {
            name: self.name.clone(),
            group: self.group,
            color: self.color,
            from: self.from_me.then(String::new),
            preview: self.preview.clone(),
            state: self.state,
            time: clock.list(self.last_ms, today),
            unread: self.unread,
        })
    }
}
