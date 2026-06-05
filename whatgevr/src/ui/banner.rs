use crate::proto::DaemonState;
use crate::ui::{state::UiState, widgets::Widgets};

pub fn update_banner(widgets: &Widgets, state: &UiState) {
    if state.daemon_disconnected {
        widgets
            .banner
            .set_title("Unable to connect to whatevrd. Retrying...");
        widgets.banner.set_button_label(Some("Retry"));
        widgets.banner.set_revealed(true);
        return;
    }

    let daemon_state = state.daemon_state;
    let detail = &state.daemon_detail;

    match daemon_state {
        DaemonState::Online => {
            widgets.banner.set_button_label(Some(""));
            widgets.banner.set_revealed(false);
        }
        DaemonState::Connecting => {
            widgets.banner.set_title(if detail.is_empty() {
                "Connecting to WhatsApp..."
            } else {
                detail
            });
            widgets.banner.set_button_label(Some(""));
            widgets.banner.set_revealed(true);
        }
        DaemonState::Reconnecting => {
            widgets.banner.set_title(if detail.is_empty() {
                "Reconnecting to WhatsApp..."
            } else {
                detail
            });

            let btn = if state.can_reconnect && !state.reconnect_in_flight {
                "Reconnect now"
            } else {
                ""
            };

            widgets.banner.set_button_label(Some(btn));
            widgets.banner.set_revealed(true);
        }
        DaemonState::Offline => {
            widgets.banner.set_title(if detail.is_empty() {
                "Offline. Local chats remain available."
            } else {
                detail
            });

            let btn = if state.can_reconnect && !state.reconnect_in_flight {
                "Reconnect now"
            } else {
                ""
            };

            widgets.banner.set_button_label(Some(btn));
            widgets.banner.set_revealed(true);
        }
        _ => {
            widgets.banner.set_button_label(Some(""));
            widgets.banner.set_revealed(false);
        }
    }
}

pub fn show_banner_notice(widgets: &Widgets, text: &str) {
    widgets.banner.set_button_label(Some(""));
    widgets.banner.set_title(text);
    widgets.banner.set_revealed(true);
}

pub fn show_banner_notice_preserving_action(widgets: &Widgets, state: &UiState, text: &str) {
    if state.daemon_disconnected
        || matches!(
            state.daemon_state,
            DaemonState::Offline | DaemonState::Reconnecting
        )
    {
        update_banner(widgets, state);
    } else {
        show_banner_notice(widgets, text);
    }
}
