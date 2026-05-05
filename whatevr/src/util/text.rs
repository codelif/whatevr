use crate::proto;
use crate::proto::DaemonState;

pub fn display_chat_name(chat: &proto::Chat) -> &str {
    if chat.name.trim().is_empty() {
        &chat.id
    } else {
        &chat.name
    }
}

pub fn chat_preview(chat: &proto::Chat) -> String {
    if chat.last_message.trim().is_empty() {
        "No text messages yet".to_string()
    } else {
        chat.last_message
            .split_whitespace()
            .collect::<Vec<_>>()
            .join(" ")
    }
}

pub fn unread_count_label(count: i32) -> String {
    if count > 99 {
        "99+".to_string()
    } else {
        count.to_string()
    }
}

pub fn format_login_state(state: DaemonState, detail: &str) -> String {
    if detail.is_empty() {
        return format!("State: {}", state_name(state));
    }

    format!("State: {}\n{}", state_name(state), detail)
}

pub fn state_name(state: DaemonState) -> &'static str {
    match state {
        DaemonState::Unspecified => "UNSPECIFIED",
        DaemonState::Starting => "STARTING",
        DaemonState::NeedLogin => "NEED_LOGIN",
        DaemonState::Connecting => "CONNECTING",
        DaemonState::Online => "ONLINE",
        DaemonState::Reconnecting => "RECONNECTING",
        DaemonState::Offline => "OFFLINE",
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn display_chat_name_falls_back_to_id_for_blank_names() {
        let chat = proto::Chat {
            id: "chat-1".to_string(),
            name: "  ".to_string(),
            ..Default::default()
        };

        assert_eq!(display_chat_name(&chat), "chat-1");
    }

    #[test]
    fn chat_preview_collapses_whitespace() {
        let chat = proto::Chat {
            last_message: "hello\n\tthere   friend".to_string(),
            ..Default::default()
        };

        assert_eq!(chat_preview(&chat), "hello there friend");
    }

    #[test]
    fn unread_count_label_caps_large_counts() {
        assert_eq!(unread_count_label(5), "5");
        assert_eq!(unread_count_label(100), "99+");
    }

    #[test]
    fn format_login_state_includes_optional_detail() {
        assert_eq!(format_login_state(DaemonState::Online, ""), "State: ONLINE");
        assert_eq!(
            format_login_state(DaemonState::Offline, "Waiting"),
            "State: OFFLINE\nWaiting"
        );
    }
}
