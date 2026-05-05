use crate::proto;
use crate::proto::DaemonState;

#[derive(Clone)]
pub enum UiMessage {
    StatusLoaded(proto::GetStatusResponse),
    StatusFailed(String),

    DaemonState {
        state: DaemonState,
        detail: String,
        can_reconnect: bool,
        retry_attempt: i32,
        next_retry_unix: i64,
    },

    QrCode {
        code: String,
        expires_at: i64,
    },

    ChatsLoaded(Vec<proto::Chat>),
    ChatsFailed(String),

    MessagesLoaded {
        chat_id: String,
        generation: u64,
        messages: Vec<proto::Message>,
    },

    MessagesFailed {
        chat_id: String,
        generation: u64,
        error: String,
    },

    ShowConversationLoading {
        chat_id: String,
        generation: u64,
    },

    SendSucceeded {
        chat_id: String,
    },

    SendFailed {
        chat_id: String,
        error: String,
    },

    MediaSendSucceeded,

    MediaSendFailed {
        chat_id: String,
        error: String,
    },

    ChatPresence {
        chat_id: String,
        is_composing: bool,
    },

    TypingIdle {
        chat_id: String,
        generation: u64,
    },

    NewMessage {
        message: proto::Message,
    },

    MessageUpdated {
        message: proto::Message,
    },

    ChatUpdated {
        chat: proto::Chat,
        previous_chat_id: Option<String>,
    },

    RenderChatList,

    LogoutSucceeded,
    LogoutFailed(String),

    Notice(String),

    DaemonConnectionLost,
    DaemonConnectionRestored,
    WhatsAppConnectionLost(String),

    ReconnectSucceeded,
    ReconnectFailed(String),

    OpenChat {
        chat_id: String,
    },

    FrontendSessionConnected,
    BootstrapAfterFirstFrame,
}
