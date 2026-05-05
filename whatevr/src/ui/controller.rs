use std::{cell::RefCell, rc::Rc};

use gtk::{gdk, glib, prelude::*};

use crate::config::{
    CONVERSATION_LOADING_PAGE, ROOT_LOADING_PAGE, ROOT_LOGIN_PAGE, ROOT_MAIN_PAGE,
    SIDEBAR_LOADING_PAGE,
};
use crate::daemon;
use crate::proto::DaemonState;
use crate::ui::{
    banner::{show_banner_notice, show_banner_notice_preserving_action, update_banner},
    chat_actions::open_chat_by_id,
    commands::{
        hold_frontend_session, report_frontend_session_state, request_chats,
        request_mark_chat_read, request_messages, request_set_chat_presence, request_status,
        subscribe_daemon_events, subscribe_login_events,
    },
    context::{UiSender, send_ui},
    lifecycle::present_app_window,
    message::UiMessage,
    navigation::update_navigation_state,
    render::{
        chat_list::render_chat_list,
        composer::{clear_composer, render_composer_state},
        conversation::{
            is_scroller_near_bottom, render_conversation, render_conversation_header,
            scroll_messages_to_bottom,
        },
        qr::render_qr_texture,
    },
    state::{
        UiState, clear_local_ui_state, commit_pending_chat, next_message_request_generation,
        sort_chats, sync_selected_chat_after_reload, upsert_chat,
    },
    widgets::Widgets,
};
use crate::util::{text::format_login_state, time::format_qr_expiry};

fn schedule_chat_list_render(state: &Rc<RefCell<UiState>>, sender: UiSender) {
    let should_schedule = {
        let mut state = state.borrow_mut();
        if state.chat_list_render_scheduled {
            false
        } else {
            state.chat_list_render_scheduled = true;
            true
        }
    };

    if should_schedule {
        glib::idle_add_local_once(move || {
            send_ui(&sender, UiMessage::RenderChatList);
        });
    }
}

pub fn handle_ui_message(
    message: UiMessage,
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &UiSender,
) {
    match message {
        UiMessage::StatusLoaded(status) => {
            let daemon_state =
                DaemonState::try_from(status.state).unwrap_or(DaemonState::Unspecified);
            widgets.loading_spinner.set_spinning(true);
            widgets.loading_spinner.set_visible(true);
            widgets.loading_retry_button.set_visible(false);
            apply_daemon_state(
                widgets,
                state,
                sender,
                daemon_state,
                status.detail,
                status.can_reconnect,
                status.retry_attempt,
                status.next_retry_unix,
            );
        }
        UiMessage::StatusFailed(error) => {
            {
                let mut s = state.borrow_mut();
                s.daemon_disconnected = true;
            }
            widgets
                .loading_page
                .set_title("Unable to connect to whatevrd");
            widgets
                .loading_page
                .set_description(Some(&daemon::errors::friendly_daemon_error_text(&error)));
            widgets.loading_spinner.set_spinning(false);
            widgets.loading_spinner.set_visible(false);
            widgets.loading_retry_button.set_visible(true);
            widgets.root_stack.set_visible_child_name(ROOT_LOADING_PAGE);
            let state = state.borrow();
            update_banner(widgets, &state);
        }
        UiMessage::DaemonState {
            state: daemon_state,
            detail,
            can_reconnect,
            retry_attempt,
            next_retry_unix,
        } => {
            apply_daemon_state(
                widgets,
                state,
                sender,
                daemon_state,
                detail,
                can_reconnect,
                retry_attempt,
                next_retry_unix,
            );
        }
        UiMessage::QrCode { code, expires_at } => {
            match render_qr_texture(&code) {
                Ok(texture) => {
                    widgets.qr_picture.set_paintable(Some(&texture));
                    widgets.qr_error_label.set_text("");
                }
                Err(error) => {
                    widgets
                        .qr_picture
                        .set_paintable(None::<&gdk::MemoryTexture>);
                    widgets
                        .qr_error_label
                        .set_text(&format!("Unable to render QR code: {error}"));
                }
            }
            widgets
                .qr_expiry_label
                .set_text(&format_qr_expiry(expires_at));
            widgets.root_stack.set_visible_child_name(ROOT_LOGIN_PAGE);
        }
        UiMessage::ChatsLoaded(chats) => {
            let pending_open_chat_id = {
                let mut state = state.borrow_mut();
                state.chats = chats;
                sort_chats(&mut state.chats);
                state.chats_loaded = true;
                sync_selected_chat_after_reload(&mut state);
                state.pending_open_chat_id.clone()
            };
            report_frontend_session_state(state, sender.clone());

            let mut opened_pending = false;
            if let Some(chat_id) = pending_open_chat_id {
                opened_pending = open_chat_by_id(chat_id, false, widgets, state, sender);
                if opened_pending {
                    state.borrow_mut().pending_open_chat_id = None;
                }
            }

            if opened_pending {
                return;
            }

            {
                let state = state.borrow();
                render_chat_list(widgets, &state);
                render_conversation(widgets, &state);
                update_navigation_state(widgets, &state);
            }
        }
        UiMessage::ChatsFailed(error) => {
            let state = state.borrow();
            if state.chats_loaded {
                show_banner_notice(widgets, &error);
            } else {
                widgets
                    .sidebar_loading_page
                    .set_title("Unable to load chats");
                widgets.sidebar_loading_page.set_description(Some(&error));
                widgets
                    .sidebar_stack
                    .set_visible_child_name(SIDEBAR_LOADING_PAGE);
            }
        }
        UiMessage::MessagesLoaded {
            chat_id,
            generation,
            messages,
        } => {
            let (should_mark_read, should_clear_composer) = {
                let mut state = state.borrow_mut();
                if state.message_request_generation != generation {
                    return;
                }
                if state.pending_chat_id.as_deref() == Some(chat_id.as_str()) {
                    let should_clear_composer = commit_pending_chat(&mut state, chat_id.clone());
                    state.current_messages_chat_id = Some(chat_id.clone());
                    state.current_messages = messages;
                    state.loading_chat_id = None;
                    (true, should_clear_composer)
                } else if state.selected_chat_id.as_deref() == Some(chat_id.as_str()) {
                    state.current_messages_chat_id = Some(chat_id.clone());
                    state.current_messages = messages;
                    state.loading_chat_id = None;
                    (false, false)
                } else {
                    return;
                }
            };

            if should_clear_composer {
                clear_composer(&widgets.composer_text_view);
            }

            let state = state.borrow();
            render_chat_list(widgets, &state);
            render_conversation(widgets, &state);
            update_navigation_state(widgets, &state);
            if should_mark_read {
                request_mark_chat_read(sender.clone(), chat_id);
            }
        }
        UiMessage::MessagesFailed {
            chat_id,
            generation,
            error,
        } => {
            let (should_show_error, should_clear_composer) = {
                let mut state = state.borrow_mut();
                if state.message_request_generation != generation {
                    return;
                }
                if state.pending_chat_id.as_deref() == Some(chat_id.as_str()) {
                    let should_clear_composer = commit_pending_chat(&mut state, chat_id.clone());
                    state.loading_chat_id = Some(chat_id.clone());
                    (true, should_clear_composer)
                } else {
                    (
                        state.selected_chat_id.as_deref() == Some(chat_id.as_str()),
                        false,
                    )
                }
            };

            if !should_show_error {
                return;
            }

            if should_clear_composer {
                clear_composer(&widgets.composer_text_view);
            }

            let state = state.borrow();
            render_chat_list(widgets, &state);
            update_navigation_state(widgets, &state);
            widgets
                .conversation_stack
                .set_visible_child_name("selected");
            render_conversation_header(widgets, &state);
            widgets
                .conversation_loading_page
                .set_title("Unable to load messages");
            widgets
                .conversation_loading_page
                .set_description(Some(&error));
            widgets
                .conversation_content_stack
                .set_visible_child_name(CONVERSATION_LOADING_PAGE);
        }
        UiMessage::ShowConversationLoading {
            chat_id,
            generation,
        } => {
            let (should_mark_read, should_clear_composer) = {
                let mut state = state.borrow_mut();
                if state.message_request_generation != generation
                    || state.pending_chat_id.as_deref() != Some(chat_id.as_str())
                {
                    return;
                }

                let should_clear_composer = commit_pending_chat(&mut state, chat_id.clone());
                state.loading_chat_id = Some(chat_id.clone());
                (true, should_clear_composer)
            };

            if should_clear_composer {
                clear_composer(&widgets.composer_text_view);
            }

            let state = state.borrow();
            render_chat_list(widgets, &state);
            render_conversation(widgets, &state);
            update_navigation_state(widgets, &state);
            if should_mark_read {
                request_mark_chat_read(sender.clone(), chat_id);
            }
        }
        UiMessage::SendSucceeded { chat_id } => {
            let should_clear_composer = {
                let mut state = state.borrow_mut();
                state.send_in_flight = false;
                let selected = state.selected_chat_id.as_deref() == Some(chat_id.as_str());
                if selected {
                    state.composer_error.clear();
                }
                selected
            };

            if should_clear_composer {
                clear_composer(&widgets.composer_text_view);
            }

            let state = state.borrow();
            render_composer_state(widgets, &state);
            if should_clear_composer {
                widgets.composer_text_view.grab_focus();
            }
        }
        UiMessage::SendFailed { chat_id, error } => {
            let connection_lost = daemon::errors::is_whatsapp_connection_error_text(&error);
            let mut should_render = false;
            {
                let mut state = state.borrow_mut();
                state.send_in_flight = false;
                if state.selected_chat_id.as_deref() == Some(chat_id.as_str()) {
                    state.composer_error = error;
                    should_render = true;
                }
            }

            if should_render {
                let state = state.borrow();
                render_conversation(widgets, &state);
            }
            if connection_lost {
                send_ui(
                    &sender,
                    UiMessage::WhatsAppConnectionLost(
                        daemon::errors::whatsapp_connection_lost_detail(),
                    ),
                );
            }
        }
        UiMessage::MediaSendSucceeded => {}
        UiMessage::MediaSendFailed { chat_id, error } => {
            let connection_lost = daemon::errors::is_whatsapp_connection_error_text(&error);
            let mut should_render = false;
            {
                let mut state = state.borrow_mut();
                if state.selected_chat_id.as_deref() == Some(chat_id.as_str()) {
                    state.composer_error = error;
                    should_render = true;
                }
            }
            if should_render {
                let state = state.borrow();
                render_conversation(widgets, &state);
            }
            if connection_lost {
                send_ui(
                    &sender,
                    UiMessage::WhatsAppConnectionLost(
                        daemon::errors::whatsapp_connection_lost_detail(),
                    ),
                );
            }
        }
        UiMessage::ChatPresence {
            chat_id,
            is_composing,
        } => {
            let selected_matches = {
                let mut s = state.borrow_mut();
                s.composing_peers.insert(chat_id.clone(), is_composing);
                s.selected_chat_id.as_deref() == Some(chat_id.as_str())
            };
            if selected_matches {
                render_conversation_header(widgets, &state.borrow());
            }
        }
        UiMessage::TypingIdle {
            chat_id,
            generation,
        } => {
            let should_send = {
                let mut s = state.borrow_mut();
                let matches_active = s.selected_chat_id.as_deref() == Some(chat_id.as_str())
                    && s.typing_generation == generation
                    && s.last_sent_composing;
                if matches_active {
                    s.last_sent_composing = false;
                }
                matches_active
            };
            if should_send {
                request_set_chat_presence(sender.clone(), chat_id, false);
            }
        }
        UiMessage::NewMessage { message } => {
            let chat_id = message.chat_id.clone();
            let was_near_bottom = is_scroller_near_bottom(widgets);
            let (is_for_selected, mark_read) = {
                let mut s = state.borrow_mut();
                let selected = s.selected_chat_id.as_deref() == Some(chat_id.as_str());
                let mark_read = selected && s.window_focused;
                if selected && s.current_messages_chat_id.as_deref() == Some(chat_id.as_str()) {
                    if let Some(existing) = s
                        .current_messages
                        .iter_mut()
                        .find(|existing| existing.id == message.id)
                    {
                        *existing = message;
                    } else {
                        s.current_messages.push(message);
                    }
                }
                (selected, mark_read)
            };

            if is_for_selected {
                let state = state.borrow();
                render_conversation(widgets, &state);
                if was_near_bottom {
                    scroll_messages_to_bottom(widgets);
                } else {
                    show_banner_notice_preserving_action(
                        widgets,
                        &state,
                        "New messages below. Scroll down to read.",
                    );
                }
            }
            if mark_read {
                request_mark_chat_read(sender.clone(), chat_id);
            }
        }
        UiMessage::MessageUpdated { message } => {
            let chat_id = message.chat_id.clone();
            let updated = {
                let mut s = state.borrow_mut();
                if s.selected_chat_id.as_deref() != Some(chat_id.as_str()) {
                    false
                } else if let Some(existing) = s
                    .current_messages
                    .iter_mut()
                    .find(|existing| existing.id == message.id)
                {
                    *existing = message;
                    true
                } else {
                    false
                }
            };

            if updated {
                let state = state.borrow();
                render_conversation(widgets, &state);
            }
        }
        UiMessage::ChatUpdated {
            chat,
            previous_chat_id,
        } => {
            let header_changed = {
                let mut s = state.borrow_mut();

                if let Some(prev_id) = previous_chat_id.as_deref() {
                    if s.selected_chat_id.as_deref() == Some(prev_id) {
                        s.selected_chat_id = Some(chat.id.clone());
                        if s.current_messages_chat_id.as_deref() == Some(prev_id) {
                            s.current_messages_chat_id = Some(chat.id.clone());
                            for message in s.current_messages.iter_mut() {
                                message.chat_id = chat.id.clone();
                            }
                        }
                        if let Some(value) = s.composing_peers.remove(prev_id) {
                            s.composing_peers.insert(chat.id.clone(), value);
                        }
                    }
                    s.chats.retain(|existing| existing.id != prev_id);
                }

                let header_changed = s
                    .selected_chat_id
                    .as_deref()
                    .map(|selected| selected == chat.id)
                    .unwrap_or(false)
                    && {
                        let prev = s.chats.iter().find(|existing| existing.id == chat.id);
                        match prev {
                            Some(existing) => {
                                existing.name != chat.name
                                    || existing.is_group != chat.is_group
                                    || existing.avatar_local_path != chat.avatar_local_path
                            }
                            None => true,
                        }
                    };

                upsert_chat(&mut s.chats, chat);
                header_changed
            };

            let state_borrow = state.borrow();
            if header_changed {
                render_conversation_header(widgets, &state_borrow);
            }
            drop(state_borrow);
            schedule_chat_list_render(state, sender.clone());
        }
        UiMessage::RenderChatList => {
            {
                let mut state = state.borrow_mut();
                state.chat_list_render_scheduled = false;
                sort_chats(&mut state.chats);
            }
            let state = state.borrow();
            render_chat_list(widgets, &state);
            update_navigation_state(widgets, &state);
        }
        UiMessage::LogoutSucceeded => {
            {
                let mut state = state.borrow_mut();
                clear_local_ui_state(&mut state);
            }
            clear_composer(&widgets.composer_text_view);
            let state = state.borrow();
            widgets.root_stack.set_visible_child_name(ROOT_LOGIN_PAGE);
            render_chat_list(widgets, &state);
            render_conversation(widgets, &state);
            update_navigation_state(widgets, &state);
            show_banner_notice(widgets, "Logged out and deleted local session data");
            drop(state);
            request_status(sender.clone());
        }
        UiMessage::LogoutFailed(error) => {
            show_banner_notice(widgets, &format!("Logout failed: {error}"));
        }
        UiMessage::Notice(text) => {
            let state = state.borrow();
            show_banner_notice_preserving_action(widgets, &state, &text);
        }
        UiMessage::WhatsAppConnectionLost(detail) => {
            {
                let mut s = state.borrow_mut();
                s.daemon_state = DaemonState::Offline;
                s.daemon_detail = detail;
                s.can_reconnect = true;
                s.retry_attempt = 0;
                s.next_retry_unix = 0;
                s.reconnect_in_flight = false;
            }
            let state = state.borrow();
            update_banner(widgets, &state);
            render_composer_state(widgets, &state);
        }
        UiMessage::DaemonConnectionLost => {
            {
                let mut s = state.borrow_mut();
                s.daemon_disconnected = true;
            }
            let state = state.borrow();
            update_banner(widgets, &state);
            render_composer_state(widgets, &state);
        }
        UiMessage::DaemonConnectionRestored => {
            let was_disconnected = {
                let mut s = state.borrow_mut();
                let was_disconnected = s.daemon_disconnected;
                s.daemon_disconnected = false;
                was_disconnected
            };
            request_status(sender.clone());
            request_chats(sender.clone());
            let selected = state.borrow().selected_chat_id.clone();
            if let Some(chat_id) = selected {
                let generation = next_message_request_generation(state);
                request_messages(sender.clone(), chat_id, generation);
            }
            report_frontend_session_state(state, sender.clone());
            let state = state.borrow();
            render_composer_state(widgets, &state);
            update_banner(widgets, &state);
            if was_disconnected {
                show_banner_notice(widgets, "Connected to whatevrd");
            }
        }
        UiMessage::ReconnectSucceeded => {
            {
                let mut s = state.borrow_mut();
                s.reconnect_in_flight = false;
            }
            let state = state.borrow();
            show_banner_notice_preserving_action(widgets, &state, "Reconnect requested");
        }
        UiMessage::ReconnectFailed(error) => {
            {
                let mut s = state.borrow_mut();
                s.reconnect_in_flight = false;
                s.can_reconnect = true;
            }
            let state = state.borrow();
            update_banner(widgets, &state);
            show_banner_notice_preserving_action(
                widgets,
                &state,
                &format!("Unable to ask whatevrd to reconnect: {}", error),
            );
        }
        UiMessage::OpenChat { chat_id } => {
            present_app_window(widgets);
            if !open_chat_by_id(chat_id.clone(), false, widgets, state, sender) {
                {
                    let mut s = state.borrow_mut();
                    s.pending_open_chat_id = Some(chat_id);
                    s.chats_loaded = false;
                }
                request_chats(sender.clone());
                let state = state.borrow();
                render_chat_list(widgets, &state);
            }
        }
        UiMessage::FrontendSessionConnected => {
            report_frontend_session_state(state, sender.clone());
        }
        UiMessage::BootstrapAfterFirstFrame => {
            let session_id = {
                let mut state = state.borrow_mut();
                if !state.initial_chat_request_started {
                    state.initial_chat_request_started = true;
                    request_chats(sender.clone());
                }
                state.frontend_session_id.clone()
            };
            request_status(sender.clone());
            hold_frontend_session(sender.clone(), session_id);
            subscribe_login_events(sender.clone());
            subscribe_daemon_events(sender.clone());
        }
    }
}

fn apply_daemon_state(
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &UiSender,
    daemon_state: DaemonState,
    detail: String,
    can_reconnect: bool,
    retry_attempt: i32,
    next_retry_unix: i64,
) {
    let mut should_request_chats = false;

    {
        let mut state = state.borrow_mut();
        state.daemon_state = daemon_state;
        state.daemon_detail = detail.clone();
        state.can_reconnect = can_reconnect;
        state.retry_attempt = retry_attempt;
        state.next_retry_unix = next_retry_unix;
        state.daemon_disconnected = false;
        widgets.loading_spinner.set_spinning(true);
        widgets.loading_spinner.set_visible(true);
        widgets.loading_retry_button.set_visible(false);
        // Clear reconnect_in_flight once daemon confirms connection attempt.
        if matches!(
            daemon_state,
            DaemonState::Online | DaemonState::Connecting | DaemonState::Reconnecting
        ) {
            state.reconnect_in_flight = false;
        }

        if matches!(daemon_state, DaemonState::NeedLogin) {
            state.initial_chat_request_started = false;
            clear_local_ui_state(&mut state);
        }

        match daemon_state {
            DaemonState::Starting | DaemonState::Unspecified => {
                if state.chats_loaded || state.initial_chat_request_started {
                    widgets.root_stack.set_visible_child_name(ROOT_MAIN_PAGE);
                } else {
                    widgets.root_stack.set_visible_child_name(ROOT_LOADING_PAGE);
                    widgets.loading_page.set_title("Starting whatevrd");
                    widgets
                        .loading_page
                        .set_description(Some("Preparing the local session and syncing pipeline."));
                }
            }
            DaemonState::NeedLogin => {
                widgets.root_stack.set_visible_child_name(ROOT_LOGIN_PAGE);
            }
            _ => {
                widgets.root_stack.set_visible_child_name(ROOT_MAIN_PAGE);
                if !state.initial_chat_request_started {
                    state.initial_chat_request_started = true;
                    should_request_chats = true;
                }
            }
        }
    }

    if should_request_chats {
        request_chats(sender.clone());
    }

    let state = state.borrow();
    render_chat_list(widgets, &state);
    render_conversation(widgets, &state);
    update_navigation_state(widgets, &state);

    widgets
        .login_status_label
        .set_text(&format_login_state(daemon_state, &detail));
    update_banner(widgets, &state);
}
