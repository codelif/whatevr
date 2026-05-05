use std::{
    collections::HashMap,
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

pub fn upsert_chat(chats: &mut Vec<proto::Chat>, chat: proto::Chat) {
    if let Some(existing) = chats.iter_mut().find(|existing| existing.id == chat.id) {
        *existing = chat;
    } else {
        chats.push(chat);
    }
}

pub fn sort_chats(chats: &mut [proto::Chat]) {
    chats.sort_by_key(|chat| std::cmp::Reverse(chat.last_message_time_unix));
}
