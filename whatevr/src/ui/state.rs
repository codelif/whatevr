use std::{
    collections::{HashMap, HashSet},
    time::{SystemTime, UNIX_EPOCH},
};

use crate::proto;
use crate::proto::DaemonState;

pub struct UiState {
    pub daemon_state: DaemonState,
    pub daemon_detail: String,
    pub can_reconnect: bool,
    pub retry_attempt: i32,
    pub next_retry_unix: i64,

    pub daemon_disconnected: bool,
    pub reconnect_in_flight: bool,

    pub chats: Vec<proto::Chat>,
    pub chats_loaded: bool,
    pub initial_chat_request_started: bool,

    pub selected_chat_id: Option<String>,
    pub pending_chat_id: Option<String>,
    pub loading_chat_id: Option<String>,

    pub message_request_generation: u64,

    pub current_messages_chat_id: Option<String>,
    pub current_messages: Vec<proto::Message>,

    pub composer_error: String,
    pub send_in_flight: bool,

    pub window_focused: bool,

    pub composing_peers: HashMap<String, bool>,
    pub last_sent_composing: bool,
    pub typing_generation: u64,

    pub pending_open_chat_id: Option<String>,
    pub frontend_session_id: String,

    pub chat_list_render_scheduled: bool,

    pub history_sync: Option<HistorySyncStatus>,
    pub older_fetch_in_flight: Option<OlderFetchInFlight>,
    // Chats whose local-store oldest message we've already paginated past
    // (server returned an empty page). Stops the scroll-to-top handler
    // from spamming the daemon with no-op fetches.
    pub chats_with_exhausted_history: HashSet<String>,
    pub pending_history_refresh_chats: HashSet<String>,
}

#[derive(Clone, Debug)]
pub struct HistorySyncStatus {
    pub sync_type: proto::HistorySyncType,
    pub progress_percent: u32,
    pub chunk_order: u32,
    pub conversations_in_chunk: u32,
    pub messages_in_chunk: u32,
}

#[derive(Clone, Debug)]
pub struct OlderFetchInFlight {
    pub chat_id: String,
    pub anchor_message_id: String,
    pub generation: u64,
}

pub struct HistorySyncUpdate {
    pub snapshot: Option<HistorySyncStatus>,
    pub completed_visible_sync: bool,
}

impl Default for UiState {
    fn default() -> Self {
        Self {
            daemon_state: DaemonState::Starting,
            daemon_detail: String::new(),
            can_reconnect: false,
            retry_attempt: 0,
            next_retry_unix: 0,

            daemon_disconnected: false,
            reconnect_in_flight: false,

            chats: Vec::new(),
            chats_loaded: false,
            initial_chat_request_started: false,

            selected_chat_id: None,
            pending_chat_id: None,
            loading_chat_id: None,

            message_request_generation: 0,

            current_messages_chat_id: None,
            current_messages: Vec::new(),

            composer_error: String::new(),
            send_in_flight: false,

            window_focused: false,

            composing_peers: HashMap::new(),
            last_sent_composing: false,
            typing_generation: 0,

            pending_open_chat_id: None,
            frontend_session_id: new_frontend_session_id(),

            chat_list_render_scheduled: false,

            history_sync: None,
            older_fetch_in_flight: None,
            chats_with_exhausted_history: HashSet::new(),
            pending_history_refresh_chats: HashSet::new(),
        }
    }
}

fn new_frontend_session_id() -> String {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_nanos())
        .unwrap_or_default();

    format!("whatevr-{}-{nanos}", std::process::id())
}

pub fn next_message_request_generation(state: &std::rc::Rc<std::cell::RefCell<UiState>>) -> u64 {
    let mut state = state.borrow_mut();
    state.message_request_generation = state.message_request_generation.wrapping_add(1);
    state.message_request_generation
}

pub fn commit_pending_chat(state: &mut UiState, chat_id: String) -> bool {
    let was_selected = state.selected_chat_id.as_deref() == Some(chat_id.as_str());

    state.selected_chat_id = Some(chat_id.clone());
    state.pending_chat_id = None;
    state.composer_error.clear();

    if !was_selected {
        state.current_messages_chat_id = None;
        state.current_messages.clear();
        state.last_sent_composing = false;
        state.typing_generation = state.typing_generation.wrapping_add(1);
    }

    !was_selected
}

pub fn clear_local_ui_state(state: &mut UiState) {
    state.chats.clear();
    state.chats_loaded = true;

    state.selected_chat_id = None;
    state.pending_chat_id = None;
    state.loading_chat_id = None;

    state.current_messages_chat_id = None;
    state.current_messages.clear();

    state.composer_error.clear();
    state.send_in_flight = false;

    state.composing_peers.clear();
    state.last_sent_composing = false;
    state.typing_generation = state.typing_generation.wrapping_add(1);

    state.pending_open_chat_id = None;

    state.history_sync = None;
    state.older_fetch_in_flight = None;
    state.chats_with_exhausted_history.clear();
    state.pending_history_refresh_chats.clear();
}

pub fn apply_history_sync_progress(
    state: &mut UiState,
    sync_type: proto::HistorySyncType,
    progress_percent: u32,
    chunk_order: u32,
    conversations_in_chunk: u32,
    messages_in_chunk: u32,
    is_complete: bool,
) -> HistorySyncUpdate {
    if is_complete {
        let should_complete_visible = state
            .history_sync
            .as_ref()
            .map(|current| sync_type == current.sync_type || progress_percent >= 100)
            .unwrap_or(false);

        if should_complete_visible {
            state.history_sync = None;
            return HistorySyncUpdate {
                snapshot: None,
                completed_visible_sync: true,
            };
        }

        return HistorySyncUpdate {
            snapshot: state.history_sync.clone(),
            completed_visible_sync: false,
        };
    }

    if is_auxiliary_history_sync(sync_type)
        && state
            .history_sync
            .as_ref()
            .map(|current| !is_auxiliary_history_sync(current.sync_type))
            .unwrap_or(false)
    {
        return HistorySyncUpdate {
            snapshot: state.history_sync.clone(),
            completed_visible_sync: false,
        };
    }

    let progress_percent = state
        .history_sync
        .as_ref()
        .map(|current| current.progress_percent.max(progress_percent.min(100)))
        .unwrap_or(progress_percent.min(100));

    state.history_sync = Some(HistorySyncStatus {
        sync_type,
        progress_percent,
        chunk_order,
        conversations_in_chunk,
        messages_in_chunk,
    });

    HistorySyncUpdate {
        snapshot: state.history_sync.clone(),
        completed_visible_sync: false,
    }
}

fn is_auxiliary_history_sync(sync_type: proto::HistorySyncType) -> bool {
    matches!(
        sync_type,
        proto::HistorySyncType::PushName | proto::HistorySyncType::NonBlockingData
    )
}

pub fn sync_selected_chat_after_reload(state: &mut UiState) {
    if let Some(selected_chat_id) = state.selected_chat_id.clone() {
        if !state.chats.iter().any(|chat| chat.id == selected_chat_id) {
            state.selected_chat_id = None;
            state.pending_chat_id = None;
            state.loading_chat_id = None;
            state.current_messages_chat_id = None;
            state.current_messages.clear();
            state.composer_error.clear();
            state.send_in_flight = false;
        }
    }
}

pub fn composer_send_allowed(state: &UiState) -> bool {
    if state.daemon_disconnected || state.send_in_flight {
        return false;
    }

    matches!(
        state.daemon_state,
        DaemonState::Online
            | DaemonState::Connecting
            | DaemonState::Reconnecting
            | DaemonState::Offline
    )
}

pub struct NewMessageTransition {
    pub chat_id: String,
    pub is_for_selected_chat: bool,
    pub should_mark_read: bool,
}

pub fn apply_new_message(state: &mut UiState, message: proto::Message) -> NewMessageTransition {
    let chat_id = message.chat_id.clone();
    let is_for_selected_chat = state.selected_chat_id.as_deref() == Some(chat_id.as_str());
    let should_mark_read = is_for_selected_chat && state.window_focused;

    if is_for_selected_chat && state.current_messages_chat_id.as_deref() == Some(chat_id.as_str()) {
        if let Some(existing) = state
            .current_messages
            .iter_mut()
            .find(|existing| existing.id == message.id)
        {
            *existing = message;
        } else {
            insert_message_in_order(&mut state.current_messages, message);
        }
    }

    NewMessageTransition {
        chat_id,
        is_for_selected_chat,
        should_mark_read,
    }
}

pub fn replace_current_messages(
    state: &mut UiState,
    chat_id: String,
    messages: Vec<proto::Message>,
) {
    state.current_messages_chat_id = Some(chat_id.clone());
    state.current_messages = messages;
    state.chats_with_exhausted_history.remove(&chat_id);
}

// insert_message_in_order keeps `current_messages` sorted by
// (timestamp, id). New live messages almost always belong at the tail, so
// the common case stays O(1); out-of-order arrivals (history backfill
// stragglers, retries, clock skew) land in the right slot instead of
// breaking the conversation flow.
fn insert_message_in_order(messages: &mut Vec<proto::Message>, new_message: proto::Message) {
    if messages
        .last()
        .map(|tail| message_sort_key(tail) <= message_sort_key(&new_message))
        .unwrap_or(true)
    {
        messages.push(new_message);
        return;
    }

    let new_key = message_sort_key(&new_message);
    let position = messages.partition_point(|existing| message_sort_key(existing) <= new_key);
    messages.insert(position, new_message);
}

fn message_sort_key(message: &proto::Message) -> (i64, &str) {
    (message.timestamp_unix, message.id.as_str())
}

// prepend_older_messages merges a paginated batch of older messages into
// the front of the current conversation, keeping it sorted by timestamp
// and dropping any IDs that already exist (the backfill / live race can
// hand us overlapping rows).
pub fn prepend_older_messages(
    state: &mut UiState,
    chat_id: &str,
    older: Vec<proto::Message>,
) -> usize {
    if state.current_messages_chat_id.as_deref() != Some(chat_id) {
        return 0;
    }

    let mut existing_ids: std::collections::HashSet<String> = state
        .current_messages
        .iter()
        .map(|message| message.id.clone())
        .collect();

    let mut filtered: Vec<proto::Message> = older
        .into_iter()
        .filter(|message| existing_ids.insert(message.id.clone()))
        .collect();
    let added = filtered.len();

    filtered.sort_by(|a, b| message_sort_key(a).cmp(&message_sort_key(b)));
    filtered.append(&mut state.current_messages);
    state.current_messages = filtered;

    added
}

// note_history_exhausted marks a chat as having no more older messages in
// the local store, after a paginated fetch returned an empty page. This
// short-circuits future scroll-to-top fetches until the local set grows
// (e.g. via a future history-sync chunk, which clears the flag).
pub fn note_history_exhausted(state: &mut UiState, chat_id: String) {
    state.chats_with_exhausted_history.insert(chat_id);
}

// note_history_backfilled clears the exhausted-history flag for a chat,
// because the daemon just told us new older messages have landed locally.
// Without this, a user who scrolled to the top before the backfill
// arrived would never get a chance to see the new rows via pagination.
pub fn note_history_backfilled(state: &mut UiState, chat_id: &str) {
    state.chats_with_exhausted_history.remove(chat_id);
    state
        .pending_history_refresh_chats
        .insert(chat_id.to_string());
}

pub fn take_selected_history_refresh_chat(state: &mut UiState) -> Option<String> {
    let selected = state.selected_chat_id.clone()?;
    if state.pending_history_refresh_chats.remove(&selected) {
        Some(selected)
    } else {
        None
    }
}

pub fn apply_message_update(state: &mut UiState, message: proto::Message) -> bool {
    let chat_id = message.chat_id.clone();

    if state.selected_chat_id.as_deref() != Some(chat_id.as_str()) {
        return false;
    }

    if let Some(existing) = state
        .current_messages
        .iter_mut()
        .find(|existing| existing.id == message.id)
    {
        *existing = message;
        true
    } else {
        false
    }
}

pub fn apply_chat_update(
    state: &mut UiState,
    chat: proto::Chat,
    previous_chat_id: Option<&str>,
) -> bool {
    if let Some(prev_id) = previous_chat_id {
        if state.selected_chat_id.as_deref() == Some(prev_id) {
            state.selected_chat_id = Some(chat.id.clone());

            if state.current_messages_chat_id.as_deref() == Some(prev_id) {
                state.current_messages_chat_id = Some(chat.id.clone());
                for message in state.current_messages.iter_mut() {
                    message.chat_id = chat.id.clone();
                }
            }

            if let Some(value) = state.composing_peers.remove(prev_id) {
                state.composing_peers.insert(chat.id.clone(), value);
            }
        }

        state.chats.retain(|existing| existing.id != prev_id);
    }

    let header_changed = state
        .selected_chat_id
        .as_deref()
        .map(|selected| selected == chat.id)
        .unwrap_or(false)
        && {
            let prev = state.chats.iter().find(|existing| existing.id == chat.id);
            match prev {
                Some(existing) => {
                    existing.name != chat.name
                        || existing.is_group != chat.is_group
                        || existing.avatar_local_path != chat.avatar_local_path
                }
                None => true,
            }
        };

    upsert_chat(&mut state.chats, chat);

    header_changed
}

pub fn upsert_chat(chats: &mut Vec<proto::Chat>, chat: proto::Chat) {
    if let Some(existing) = chats.iter_mut().find(|existing| existing.id == chat.id) {
        *existing = chat;
    } else {
        chats.push(chat);
    }
}

pub fn sort_chats(chats: &mut [proto::Chat]) {
    chats.sort_by(|a, b| {
        b.last_message_time_unix
            .cmp(&a.last_message_time_unix)
            .then_with(|| a.name.cmp(&b.name))
            .then_with(|| a.id.cmp(&b.id))
    });
}

#[cfg(test)]
mod tests {
    use super::*;

    fn chat(id: &str, last_message_time_unix: i64) -> proto::Chat {
        proto::Chat {
            id: id.to_string(),
            last_message_time_unix,
            ..Default::default()
        }
    }

    fn named_chat(id: &str, name: &str, avatar_local_path: &str) -> proto::Chat {
        proto::Chat {
            id: id.to_string(),
            name: name.to_string(),
            avatar_local_path: avatar_local_path.to_string(),
            ..Default::default()
        }
    }

    fn message(id: &str, chat_id: &str, text: &str) -> proto::Message {
        proto::Message {
            id: id.to_string(),
            chat_id: chat_id.to_string(),
            text: text.to_string(),
            ..Default::default()
        }
    }

    fn timed_message(id: &str, chat_id: &str, ts: i64) -> proto::Message {
        proto::Message {
            id: id.to_string(),
            chat_id: chat_id.to_string(),
            timestamp_unix: ts,
            ..Default::default()
        }
    }

    #[test]
    fn commit_pending_chat_selects_chat_and_clears_conversation_when_changed() {
        let mut state = UiState {
            selected_chat_id: Some("old".to_string()),
            pending_chat_id: Some("new".to_string()),
            current_messages_chat_id: Some("old".to_string()),
            current_messages: vec![proto::Message::default()],
            composer_error: "error".to_string(),
            last_sent_composing: true,
            ..Default::default()
        };

        assert!(commit_pending_chat(&mut state, "new".to_string()));
        assert_eq!(state.selected_chat_id.as_deref(), Some("new"));
        assert_eq!(state.pending_chat_id, None);
        assert_eq!(state.current_messages_chat_id, None);
        assert!(state.current_messages.is_empty());
        assert!(state.composer_error.is_empty());
        assert!(!state.last_sent_composing);
        assert_eq!(state.typing_generation, 1);
    }

    #[test]
    fn commit_pending_chat_preserves_conversation_when_already_selected() {
        let mut state = UiState {
            selected_chat_id: Some("same".to_string()),
            pending_chat_id: Some("same".to_string()),
            current_messages_chat_id: Some("same".to_string()),
            current_messages: vec![proto::Message::default()],
            last_sent_composing: true,
            ..Default::default()
        };

        assert!(!commit_pending_chat(&mut state, "same".to_string()));
        assert_eq!(state.current_messages_chat_id.as_deref(), Some("same"));
        assert_eq!(state.current_messages.len(), 1);
        assert!(state.last_sent_composing);
        assert_eq!(state.typing_generation, 0);
    }

    #[test]
    fn clear_local_ui_state_resets_chat_specific_state() {
        let mut state = UiState {
            chats: vec![chat("a", 1)],
            selected_chat_id: Some("a".to_string()),
            pending_chat_id: Some("b".to_string()),
            loading_chat_id: Some("a".to_string()),
            current_messages_chat_id: Some("a".to_string()),
            current_messages: vec![proto::Message::default()],
            composer_error: "error".to_string(),
            send_in_flight: true,
            last_sent_composing: true,
            pending_open_chat_id: Some("a".to_string()),
            ..Default::default()
        };

        clear_local_ui_state(&mut state);

        assert!(state.chats.is_empty());
        assert!(state.chats_loaded);
        assert_eq!(state.selected_chat_id, None);
        assert_eq!(state.pending_chat_id, None);
        assert_eq!(state.loading_chat_id, None);
        assert_eq!(state.current_messages_chat_id, None);
        assert!(state.current_messages.is_empty());
        assert!(state.composer_error.is_empty());
        assert!(!state.send_in_flight);
        assert!(!state.last_sent_composing);
        assert_eq!(state.pending_open_chat_id, None);
        assert_eq!(state.typing_generation, 1);
    }

    #[test]
    fn sync_selected_chat_after_reload_clears_missing_selection() {
        let mut state = UiState {
            chats: vec![chat("new", 2)],
            selected_chat_id: Some("old".to_string()),
            pending_chat_id: Some("old".to_string()),
            loading_chat_id: Some("old".to_string()),
            current_messages_chat_id: Some("old".to_string()),
            current_messages: vec![proto::Message::default()],
            composer_error: "error".to_string(),
            send_in_flight: true,
            ..Default::default()
        };

        sync_selected_chat_after_reload(&mut state);

        assert_eq!(state.selected_chat_id, None);
        assert_eq!(state.pending_chat_id, None);
        assert_eq!(state.loading_chat_id, None);
        assert_eq!(state.current_messages_chat_id, None);
        assert!(state.current_messages.is_empty());
        assert!(state.composer_error.is_empty());
        assert!(!state.send_in_flight);
    }

    #[test]
    fn composer_send_allowed_respects_connection_and_in_flight_state() {
        let mut state = UiState {
            daemon_state: DaemonState::Online,
            ..Default::default()
        };

        assert!(composer_send_allowed(&state));

        state.send_in_flight = true;
        assert!(!composer_send_allowed(&state));

        state.send_in_flight = false;
        state.daemon_disconnected = true;
        assert!(!composer_send_allowed(&state));

        state.daemon_disconnected = false;
        state.daemon_state = DaemonState::NeedLogin;
        assert!(!composer_send_allowed(&state));
    }

    #[test]
    fn apply_new_message_appends_selected_loaded_conversation_and_marks_read_when_focused() {
        let mut state = UiState {
            selected_chat_id: Some("chat-1".to_string()),
            current_messages_chat_id: Some("chat-1".to_string()),
            window_focused: true,
            ..Default::default()
        };

        let transition = apply_new_message(&mut state, message("msg-1", "chat-1", "hello"));

        assert_eq!(transition.chat_id, "chat-1");
        assert!(transition.is_for_selected_chat);
        assert!(transition.should_mark_read);
        assert_eq!(state.current_messages.len(), 1);
        assert_eq!(state.current_messages[0].text, "hello");
    }

    #[test]
    fn apply_new_message_replaces_existing_selected_message() {
        let mut state = UiState {
            selected_chat_id: Some("chat-1".to_string()),
            current_messages_chat_id: Some("chat-1".to_string()),
            current_messages: vec![message("msg-1", "chat-1", "old")],
            ..Default::default()
        };

        let transition = apply_new_message(&mut state, message("msg-1", "chat-1", "new"));

        assert!(transition.is_for_selected_chat);
        assert!(!transition.should_mark_read);
        assert_eq!(state.current_messages.len(), 1);
        assert_eq!(state.current_messages[0].text, "new");
    }

    #[test]
    fn apply_new_message_ignores_non_selected_conversation_messages() {
        let mut state = UiState {
            selected_chat_id: Some("selected".to_string()),
            current_messages_chat_id: Some("selected".to_string()),
            ..Default::default()
        };

        let transition = apply_new_message(&mut state, message("msg-1", "other", "hello"));

        assert_eq!(transition.chat_id, "other");
        assert!(!transition.is_for_selected_chat);
        assert!(!transition.should_mark_read);
        assert!(state.current_messages.is_empty());
    }

    #[test]
    fn apply_message_update_replaces_existing_selected_message() {
        let mut state = UiState {
            selected_chat_id: Some("chat-1".to_string()),
            current_messages: vec![message("msg-1", "chat-1", "old")],
            ..Default::default()
        };

        assert!(apply_message_update(
            &mut state,
            message("msg-1", "chat-1", "new")
        ));
        assert_eq!(state.current_messages[0].text, "new");
    }

    #[test]
    fn apply_message_update_returns_false_for_untracked_messages() {
        let mut state = UiState {
            selected_chat_id: Some("chat-1".to_string()),
            current_messages: vec![message("msg-1", "chat-1", "old")],
            ..Default::default()
        };

        assert!(!apply_message_update(
            &mut state,
            message("msg-2", "chat-1", "new")
        ));
        assert!(!apply_message_update(
            &mut state,
            message("msg-1", "other", "new")
        ));
    }

    #[test]
    fn apply_chat_update_migrates_selected_chat_identity() {
        let mut state = UiState {
            chats: vec![named_chat("old", "Old", "old.png")],
            selected_chat_id: Some("old".to_string()),
            current_messages_chat_id: Some("old".to_string()),
            current_messages: vec![message("msg-1", "old", "hello")],
            composing_peers: [("old".to_string(), true)].into_iter().collect(),
            ..Default::default()
        };

        let header_changed =
            apply_chat_update(&mut state, named_chat("new", "New", "new.png"), Some("old"));

        assert!(header_changed);
        assert_eq!(state.selected_chat_id.as_deref(), Some("new"));
        assert_eq!(state.current_messages_chat_id.as_deref(), Some("new"));
        assert_eq!(state.current_messages[0].chat_id, "new");
        assert!(state.composing_peers.get("new").copied().unwrap_or(false));
        assert!(!state.chats.iter().any(|chat| chat.id == "old"));
        assert!(state.chats.iter().any(|chat| chat.id == "new"));
    }

    #[test]
    fn apply_chat_update_reports_header_change_for_selected_display_changes() {
        let mut state = UiState {
            chats: vec![named_chat("chat-1", "Old", "old.png")],
            selected_chat_id: Some("chat-1".to_string()),
            ..Default::default()
        };

        assert!(apply_chat_update(
            &mut state,
            named_chat("chat-1", "New", "old.png"),
            None,
        ));
        assert_eq!(state.chats[0].name, "New");
    }

    #[test]
    fn apply_chat_update_does_not_report_header_change_for_unselected_chat() {
        let mut state = UiState {
            chats: vec![named_chat("chat-1", "Old", "old.png")],
            selected_chat_id: Some("other".to_string()),
            ..Default::default()
        };

        assert!(!apply_chat_update(
            &mut state,
            named_chat("chat-1", "New", "new.png"),
            None,
        ));
    }

    #[test]
    fn upsert_chat_replaces_existing_and_appends_new() {
        let mut chats = vec![chat("a", 1)];

        upsert_chat(&mut chats, chat("a", 3));
        upsert_chat(&mut chats, chat("b", 2));

        assert_eq!(chats.len(), 2);
        assert_eq!(chats[0].last_message_time_unix, 3);
        assert_eq!(chats[1].id, "b");
    }

    #[test]
    fn apply_new_message_inserts_older_message_in_timestamp_order() {
        let mut state = UiState {
            selected_chat_id: Some("chat-1".to_string()),
            current_messages_chat_id: Some("chat-1".to_string()),
            current_messages: vec![
                timed_message("msg-old", "chat-1", 100),
                timed_message("msg-new", "chat-1", 300),
            ],
            ..Default::default()
        };

        // History straggler arrives between the two existing rows. It
        // must land in the correct slot, not at the tail.
        let _ = apply_new_message(&mut state, timed_message("msg-mid", "chat-1", 200));

        let ids: Vec<&str> = state
            .current_messages
            .iter()
            .map(|m| m.id.as_str())
            .collect();
        assert_eq!(ids, vec!["msg-old", "msg-mid", "msg-new"]);
    }

    #[test]
    fn apply_new_message_pushes_to_tail_when_already_at_or_after_latest() {
        let mut state = UiState {
            selected_chat_id: Some("chat-1".to_string()),
            current_messages_chat_id: Some("chat-1".to_string()),
            current_messages: vec![timed_message("msg-old", "chat-1", 100)],
            ..Default::default()
        };

        let _ = apply_new_message(&mut state, timed_message("msg-new", "chat-1", 200));

        let ids: Vec<&str> = state
            .current_messages
            .iter()
            .map(|m| m.id.as_str())
            .collect();
        assert_eq!(ids, vec!["msg-old", "msg-new"]);
    }

    #[test]
    fn replace_current_messages_clears_exhausted_history_for_chat() {
        let mut state = UiState {
            chats_with_exhausted_history: ["chat-1".to_string(), "chat-2".to_string()]
                .into_iter()
                .collect(),
            ..Default::default()
        };

        replace_current_messages(
            &mut state,
            "chat-1".to_string(),
            vec![timed_message("msg-1", "chat-1", 100)],
        );

        assert_eq!(state.current_messages_chat_id.as_deref(), Some("chat-1"));
        assert_eq!(state.current_messages.len(), 1);
        assert!(!state.chats_with_exhausted_history.contains("chat-1"));
        assert!(state.chats_with_exhausted_history.contains("chat-2"));
    }

    #[test]
    fn prepend_older_messages_skips_duplicates_and_orders_correctly() {
        let mut state = UiState {
            selected_chat_id: Some("chat-1".to_string()),
            current_messages_chat_id: Some("chat-1".to_string()),
            current_messages: vec![
                timed_message("msg-2", "chat-1", 200),
                timed_message("msg-3", "chat-1", 300),
            ],
            ..Default::default()
        };

        let added = prepend_older_messages(
            &mut state,
            "chat-1",
            vec![
                timed_message("msg-0", "chat-1", 50),
                timed_message("msg-1", "chat-1", 100),
                timed_message("msg-2", "chat-1", 200), // duplicate of existing tail
            ],
        );

        assert_eq!(added, 2);
        let ids: Vec<&str> = state
            .current_messages
            .iter()
            .map(|m| m.id.as_str())
            .collect();
        assert_eq!(ids, vec!["msg-0", "msg-1", "msg-2", "msg-3"]);
    }

    #[test]
    fn prepend_older_messages_ignores_other_chats() {
        let mut state = UiState {
            selected_chat_id: Some("chat-1".to_string()),
            current_messages_chat_id: Some("chat-1".to_string()),
            current_messages: vec![timed_message("msg-1", "chat-1", 100)],
            ..Default::default()
        };

        let added = prepend_older_messages(
            &mut state,
            "chat-2",
            vec![timed_message("other", "chat-2", 50)],
        );

        assert_eq!(added, 0);
        assert_eq!(state.current_messages.len(), 1);
    }

    #[test]
    fn note_history_exhausted_records_chat_id() {
        let mut state = UiState::default();
        note_history_exhausted(&mut state, "chat-1".to_string());
        assert!(state.chats_with_exhausted_history.contains("chat-1"));
    }

    #[test]
    fn note_history_backfilled_clears_exhausted_flag() {
        let mut state = UiState {
            chats_with_exhausted_history: ["chat-1".to_string()].into_iter().collect(),
            ..Default::default()
        };

        note_history_backfilled(&mut state, "chat-1");

        assert!(!state.chats_with_exhausted_history.contains("chat-1"));
        assert!(state.pending_history_refresh_chats.contains("chat-1"));
    }

    #[test]
    fn take_selected_history_refresh_chat_consumes_selected_dirty_chat() {
        let mut state = UiState {
            selected_chat_id: Some("chat-1".to_string()),
            pending_history_refresh_chats: ["chat-1".to_string(), "chat-2".to_string()]
                .into_iter()
                .collect(),
            ..Default::default()
        };

        assert_eq!(
            take_selected_history_refresh_chat(&mut state).as_deref(),
            Some("chat-1")
        );
        assert!(!state.pending_history_refresh_chats.contains("chat-1"));
        assert!(state.pending_history_refresh_chats.contains("chat-2"));
    }

    #[test]
    fn history_sync_progress_is_monotonic() {
        let mut state = UiState::default();

        let first = apply_history_sync_progress(
            &mut state,
            proto::HistorySyncType::Full,
            20,
            1,
            5,
            100,
            false,
        );
        assert_eq!(first.snapshot.unwrap().progress_percent, 20);

        let second = apply_history_sync_progress(
            &mut state,
            proto::HistorySyncType::Full,
            5,
            2,
            5,
            50,
            false,
        );
        assert_eq!(second.snapshot.unwrap().progress_percent, 20);
    }

    #[test]
    fn auxiliary_history_sync_does_not_replace_main_progress() {
        let mut state = UiState::default();

        let _ = apply_history_sync_progress(
            &mut state,
            proto::HistorySyncType::Full,
            30,
            1,
            5,
            100,
            false,
        );
        let update = apply_history_sync_progress(
            &mut state,
            proto::HistorySyncType::PushName,
            0,
            0,
            0,
            0,
            false,
        );

        let status = update.snapshot.unwrap();
        assert_eq!(status.sync_type, proto::HistorySyncType::Full);
        assert_eq!(status.progress_percent, 30);
    }

    #[test]
    fn sort_chats_orders_newest_first() {
        let mut chats = vec![chat("old", 1), chat("new", 3), chat("middle", 2)];

        sort_chats(&mut chats);

        assert_eq!(
            chats
                .iter()
                .map(|chat| chat.id.as_str())
                .collect::<Vec<_>>(),
            vec!["new", "middle", "old"]
        );
    }
}
