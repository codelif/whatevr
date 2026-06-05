use std::{cell::RefCell, rc::Rc, time::Duration};

use crate::config::TYPING_IDLE_TIMEOUT;
use crate::daemon::{self, DynError};
use crate::proto::{DaemonState, HistorySyncType, daemon_event, login_event};
use crate::runtime;
use crate::ui::{
    context::{UiSender, send_ui},
    message::UiMessage,
    state::UiState,
};

pub fn stop_current_composing(state: &mut UiState, sender: &UiSender) {
    if !state.last_sent_composing {
        return;
    }

    if let Some(chat_id) = state.selected_chat_id.clone() {
        request_set_chat_presence(sender.clone(), chat_id, false);
    }

    state.last_sent_composing = false;
    state.typing_generation = state.typing_generation.wrapping_add(1);
}

pub fn request_status(sender: UiSender) {
    runtime::spawn(async move {
        match daemon::daemon_service::fetch_status().await {
            Ok(status) => {
                send_ui(&sender, UiMessage::StatusLoaded(status));
            }
            Err(error) => {
                send_ui(&sender, UiMessage::StatusFailed(error.to_string()));
            }
        }
    });
}

pub fn subscribe_login_events(sender: UiSender) {
    runtime::spawn(async move {
        let mut backoff = Duration::from_secs(2);

        loop {
            match stream_login_events(sender.clone()).await {
                Ok(()) => break,
                Err(_) => {
                    tokio::time::sleep(backoff).await;
                    backoff = (backoff * 2).min(Duration::from_secs(30));
                }
            }
        }
    });
}

pub fn hold_frontend_session(sender: UiSender, session_id: String) {
    runtime::spawn(async move {
        let mut backoff = Duration::from_secs(2);

        loop {
            match stream_frontend_session(sender.clone(), session_id.clone()).await {
                Ok(()) => break,
                Err(_) => {
                    tokio::time::sleep(backoff).await;
                    backoff = (backoff * 2).min(Duration::from_secs(30));
                }
            }
        }
    });
}

pub fn report_frontend_session_state(state: &Rc<RefCell<UiState>>, sender: UiSender) {
    let (session_id, focused, active_chat_id) = {
        let state = state.borrow();

        (
            state.frontend_session_id.clone(),
            state.window_focused,
            state.selected_chat_id.clone().unwrap_or_default(),
        )
    };

    request_update_frontend_session_state(sender, session_id, focused, active_chat_id);
}

pub fn subscribe_daemon_events(sender: UiSender) {
    runtime::spawn(async move {
        let mut backoff = Duration::from_secs(2);

        loop {
            let _ = stream_daemon_events(sender.clone()).await;
            send_ui(&sender, UiMessage::DaemonConnectionLost);

            tokio::time::sleep(backoff).await;
            backoff = (backoff * 2).min(Duration::from_secs(30));
        }
    });
}

pub fn request_chats(sender: UiSender) {
    runtime::spawn(async move {
        match daemon::chat_service::load_chats().await {
            Ok(chats) => {
                send_ui(&sender, UiMessage::ChatsLoaded(chats));
            }
            Err(error) => {
                send_ui(&sender, UiMessage::ChatsFailed(error.to_string()));
            }
        }
    });
}

pub fn schedule_conversation_loading(sender: UiSender, chat_id: String, generation: u64) {
    runtime::spawn(async move {
        tokio::time::sleep(Duration::from_secs(1)).await;

        send_ui(
            &sender,
            UiMessage::ShowConversationLoading {
                chat_id,
                generation,
            },
        );
    });
}

pub fn schedule_typing_idle(sender: UiSender, chat_id: String, generation: u64) {
    runtime::spawn(async move {
        tokio::time::sleep(TYPING_IDLE_TIMEOUT).await;

        send_ui(
            &sender,
            UiMessage::TypingIdle {
                chat_id,
                generation,
            },
        );
    });
}

pub fn request_messages(sender: UiSender, chat_id: String, generation: u64) {
    runtime::spawn(async move {
        match daemon::chat_service::load_messages(chat_id.clone()).await {
            Ok(messages) => {
                send_ui(
                    &sender,
                    UiMessage::MessagesLoaded {
                        chat_id,
                        generation,
                        messages,
                    },
                );
            }
            Err(error) => {
                send_ui(
                    &sender,
                    UiMessage::MessagesFailed {
                        chat_id,
                        generation,
                        error: error.to_string(),
                    },
                );
            }
        }
    });
}

pub fn request_older_messages(
    sender: UiSender,
    chat_id: String,
    anchor_message_id: String,
    generation: u64,
) {
    runtime::spawn(async move {
        match daemon::chat_service::load_messages_before(
            chat_id.clone(),
            Some(anchor_message_id.clone()),
        )
        .await
        {
            Ok(messages) => {
                send_ui(
                    &sender,
                    UiMessage::OlderMessagesLoaded {
                        chat_id,
                        anchor_message_id,
                        generation,
                        messages,
                    },
                );
            }
            Err(error) => {
                send_ui(
                    &sender,
                    UiMessage::OlderMessagesFailed {
                        chat_id,
                        anchor_message_id,
                        generation,
                        error: error.to_string(),
                    },
                );
            }
        }
    });
}

pub fn request_mark_chat_read(sender: UiSender, chat_id: String) {
    runtime::spawn(async move {
        if let Err(error) = daemon::chat_service::mark_chat_read(chat_id).await {
            if daemon::errors::is_daemon_transport_error(&*error) {
                send_ui(&sender, UiMessage::DaemonConnectionLost);
                return;
            }

            send_ui(
                &sender,
                UiMessage::Notice(format!("Unable to mark chat read: {error}")),
            );
        }
    });
}

pub fn request_download_media(sender: UiSender, message_id: String) {
    runtime::spawn(async move {
        match daemon::chat_service::download_message_media(message_id).await {
            Ok(message) => {
                send_ui(&sender, UiMessage::MessageUpdated { message });
            }
            Err(error) => {
                send_ui(
                    &sender,
                    UiMessage::Notice(format!("Unable to load media: {error}")),
                );
            }
        }
    });
}

pub fn request_update_frontend_session_state(
    sender: UiSender,
    session_id: String,
    focused: bool,
    active_chat_id: String,
) {
    runtime::spawn(async move {
        if let Err(error) =
            daemon::frontend_service::update_session_state(session_id, focused, active_chat_id)
                .await
        {
            if daemon::errors::is_daemon_transport_error(&*error) {
                send_ui(&sender, UiMessage::DaemonConnectionLost);
            }
        }
    });
}

pub fn request_send_text(sender: UiSender, chat_id: String, text: String) {
    runtime::spawn(async move {
        match daemon::send_service::send_text(chat_id.clone(), text).await {
            Ok(chat_id) => {
                send_ui(&sender, UiMessage::SendSucceeded { chat_id });
            }
            Err(error) => {
                send_ui(
                    &sender,
                    UiMessage::SendFailed {
                        chat_id,
                        error: error.to_string(),
                    },
                );
            }
        }
    });
}

pub fn request_send_media(sender: UiSender, chat_id: String, file_path: String) {
    runtime::spawn(async move {
        match daemon::send_service::send_media(chat_id.clone(), file_path).await {
            Ok(()) => {
                send_ui(&sender, UiMessage::MediaSendSucceeded);
            }
            Err(error) => {
                send_ui(
                    &sender,
                    UiMessage::MediaSendFailed {
                        chat_id,
                        error: error.to_string(),
                    },
                );
            }
        }
    });
}

pub fn request_set_chat_presence(sender: UiSender, chat_id: String, composing: bool) {
    runtime::spawn(async move {
        if let Err(error) = daemon::chat_service::set_chat_presence(chat_id, composing).await {
            let error = error.to_string();

            if daemon::errors::is_whatsapp_connection_error_text(&error) {
                send_ui(
                    &sender,
                    UiMessage::WhatsAppConnectionLost(
                        daemon::errors::whatsapp_connection_lost_detail(),
                    ),
                );
                return;
            }

            send_ui(
                &sender,
                UiMessage::Notice(format!("Unable to set chat presence: {error}")),
            );
        }
    });
}

pub fn request_logout(sender: UiSender) {
    runtime::spawn(async move {
        match daemon::login_service::logout().await {
            Ok(()) => {
                send_ui(&sender, UiMessage::LogoutSucceeded);
            }
            Err(error) => {
                send_ui(&sender, UiMessage::LogoutFailed(error.to_string()));
            }
        }
    });
}

pub fn request_reconnect(sender: UiSender) {
    runtime::spawn(async move {
        match daemon::daemon_service::reconnect().await {
            Ok(()) => {
                send_ui(&sender, UiMessage::ReconnectSucceeded);
            }
            Err(error) => {
                send_ui(
                    &sender,
                    UiMessage::ReconnectFailed(daemon::errors::friendly_daemon_error(&*error)),
                );
            }
        }
    });
}

async fn stream_login_events(sender: UiSender) -> Result<(), DynError> {
    let mut stream = daemon::login_service::subscribe_login_events().await?;

    while let Some(event) = stream.message().await? {
        match event.payload {
            Some(login_event::Payload::LoginStateChanged(_)) => {}
            Some(login_event::Payload::QrCode(qr)) => {
                send_ui(
                    &sender,
                    UiMessage::QrCode {
                        code: qr.code,
                        expires_at: qr.expires_at_unix,
                    },
                );
            }
            None => {}
        }
    }

    Ok(())
}

async fn stream_frontend_session(sender: UiSender, session_id: String) -> Result<(), DynError> {
    let connected_sender = sender.clone();

    daemon::frontend_service::hold_session(session_id, move || {
        send_ui(&connected_sender, UiMessage::FrontendSessionConnected);
    })
    .await
}

async fn stream_daemon_events(sender: UiSender) -> Result<(), DynError> {
    let mut stream = daemon::daemon_service::subscribe_events().await?;

    send_ui(&sender, UiMessage::DaemonConnectionRestored);

    while let Some(event) = stream.message().await? {
        match event.payload {
            Some(daemon_event::Payload::ConnectionChanged(change)) => {
                let state = DaemonState::try_from(change.state).unwrap_or(DaemonState::Unspecified);

                send_ui(
                    &sender,
                    UiMessage::DaemonState {
                        state,
                        detail: change.detail,
                        can_reconnect: change.can_reconnect,
                        retry_attempt: change.retry_attempt,
                        next_retry_unix: change.next_retry_unix,
                    },
                );
            }
            Some(daemon_event::Payload::NewMessage(new_message)) => {
                if let Some(message) = new_message.message {
                    send_ui(&sender, UiMessage::NewMessage { message });
                }
            }
            Some(daemon_event::Payload::MessageUpdated(message_updated)) => {
                if let Some(message) = message_updated.message {
                    send_ui(&sender, UiMessage::MessageUpdated { message });
                }
            }
            Some(daemon_event::Payload::ChatUpdated(chat_updated)) => {
                let previous_chat_id = if chat_updated.previous_chat_id.is_empty() {
                    None
                } else {
                    Some(chat_updated.previous_chat_id)
                };

                if let Some(chat) = chat_updated.chat {
                    send_ui(
                        &sender,
                        UiMessage::ChatUpdated {
                            chat,
                            previous_chat_id,
                        },
                    );
                }
            }
            Some(daemon_event::Payload::ChatPresenceChanged(presence)) => {
                send_ui(
                    &sender,
                    UiMessage::ChatPresence {
                        chat_id: presence.chat_id,
                        is_composing: presence.is_composing,
                    },
                );
            }
            Some(daemon_event::Payload::HistorySyncProgress(progress)) => {
                let sync_type = HistorySyncType::try_from(progress.sync_type)
                    .unwrap_or(HistorySyncType::Unspecified);
                send_ui(
                    &sender,
                    UiMessage::HistorySyncProgress {
                        sync_type,
                        progress_percent: progress.progress_percent,
                        chunk_order: progress.chunk_order,
                        conversations_in_chunk: progress.conversations_in_chunk,
                        messages_in_chunk: progress.messages_in_chunk,
                        is_complete: progress.is_complete,
                    },
                );
            }
            Some(daemon_event::Payload::HistoryBackfilled(backfilled)) => {
                send_ui(
                    &sender,
                    UiMessage::HistoryBackfilled {
                        chat_id: backfilled.chat_id,
                        messages_added: backfilled.messages_added,
                    },
                );
            }
            _ => {}
        }
    }

    Ok(())
}
