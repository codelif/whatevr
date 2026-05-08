use std::{
    cell::{Cell, RefCell},
    rc::Rc,
    time::Duration,
};

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
            scroll_messages_to_bottom, show_messages_below_button,
        },
        history_sync::render_history_sync_strip,
        qr::render_qr_texture,
    },
    state::{
        UiState, apply_chat_update, apply_history_sync_progress, apply_message_update,
        apply_new_message, clear_local_ui_state, commit_pending_chat,
        next_message_request_generation, note_history_backfilled, note_history_exhausted,
        prepend_older_messages, replace_current_messages, sort_chats,
        sync_selected_chat_after_reload, take_selected_history_refresh_chat,
    },
    widgets::Widgets,
};
use crate::util::{text::format_login_state, time::format_qr_expiry};

// How many frames (~60Hz) we keep watching the adjustment after a prepend.
// Layout settles within a frame or two on a normal box; we bound it as a
// safety net.
const PREPEND_MAX_FRAMES: u32 = 30;
// Once `upper` stops growing, we want one more frame to confirm before
// releasing the lock; this prevents a stale "still settling" state from
// fighting user scrolls.
const PREPEND_REQUIRED_STABLE_FRAMES: u32 = 2;

struct PrependAnchor {
    generation: u64,
    upper_before: f64,
    bottom_offset: f64,
}

// Snapshot the *distance from the bottom* of the message stack right before
// we insert older rows. After insertion, GTK will grow `upper` by exactly the
// height of the new rows; restoring `value = upper - bottom_offset` keeps
// the user pinned to the same on-screen content.
fn begin_prepend(widgets: &Widgets) -> PrependAnchor {
    let adjustment = widgets.message_scroller.vadjustment();
    let upper_before = adjustment.upper();
    let value_before = adjustment.value();
    let bottom_offset = (upper_before - value_before).max(0.0);

    let generation = widgets.message_prepend_generation.get().wrapping_add(1);
    widgets.message_prepend_generation.set(generation);
    widgets
        .message_scroll_generation
        .set(widgets.message_scroll_generation.get().wrapping_add(1));
    widgets.message_prepend_in_progress.set(true);

    PrependAnchor {
        generation,
        upper_before,
        bottom_offset,
    }
}

// Drive the scroll position back onto the bottom-anchor each time `upper`
// grows. We *only* set the value when `upper` has changed since our last
// adjustment, so once layout settles we stop touching the adjustment and the
// user can scroll freely without us fighting them.
fn finish_prepend(widgets: &Widgets, anchor: PrependAnchor) {
    let scroller = widgets.message_scroller.clone();
    let prepend_generation = widgets.message_prepend_generation.clone();
    let prepend_in_progress = widgets.message_prepend_in_progress.clone();
    let older_loading = widgets.older_messages_loading.clone();
    let cooldown_until = widgets.older_fetch_cooldown_until.clone();

    let last_upper = Rc::new(Cell::new(anchor.upper_before));
    let frame_count = Rc::new(Cell::new(0u32));
    let stable_frames = Rc::new(Cell::new(0u32));
    let applied = Rc::new(Cell::new(false));

    let generation = anchor.generation;
    let upper_before = anchor.upper_before;
    let bottom_offset = anchor.bottom_offset;

    scroller.clone().add_tick_callback(move |scroller, _| {
        if prepend_generation.get() != generation {
            return glib::ControlFlow::Break;
        }

        let adj = scroller.vadjustment();
        let upper = adj.upper();

        if upper > upper_before + 0.5 {
            let upper_changed = (upper - last_upper.get()).abs() > 0.5;
            if !applied.get() || upper_changed {
                let max_value = (upper - adj.page_size()).max(adj.lower());
                let target = (upper - bottom_offset).max(adj.lower()).min(max_value);
                if (adj.value() - target).abs() > 0.5 {
                    adj.set_value(target);
                }
                applied.set(true);
                last_upper.set(upper);
                stable_frames.set(0);
            } else {
                stable_frames.set(stable_frames.get() + 1);
            }
        }

        frame_count.set(frame_count.get() + 1);

        let done = (applied.get() && stable_frames.get() >= PREPEND_REQUIRED_STABLE_FRAMES)
            || frame_count.get() >= PREPEND_MAX_FRAMES;

        if done {
            prepend_in_progress.set(false);
            older_loading.set_visible(false);
            // Brief cooldown so an immediate value-changed event (e.g. from
            // GTK's own settle, or a user scroll inertia tick) cannot kick a
            // second fetch on the same frame.
            cooldown_until.set(glib::monotonic_time() + 250_000);
            glib::ControlFlow::Break
        } else {
            glib::ControlFlow::Continue
        }
    });

    // Safety net: if upper never grows (no rows actually rendered, or the
    // chat changed mid-flight without bumping our generation), release the
    // lock so the UI doesn't deadlock the spinner / prepend gate.
    let prepend_generation_timeout = widgets.message_prepend_generation.clone();
    let prepend_in_progress_timeout = widgets.message_prepend_in_progress.clone();
    let older_loading_timeout = widgets.older_messages_loading.clone();
    glib::timeout_add_local_once(Duration::from_secs(2), move || {
        if prepend_generation_timeout.get() == generation {
            prepend_in_progress_timeout.set(false);
            older_loading_timeout.set_visible(false);
        }
    });
}

fn preserve_scroll_position_for_update(widgets: &Widgets) -> impl FnOnce() + 'static {
    let message_scroller = widgets.message_scroller.clone();
    let adjustment = widgets.message_scroller.vadjustment();
    let value_before = adjustment.value();

    move || {
        let adjustment_for_idle = adjustment.clone();
        glib::idle_add_local_once(move || {
            adjustment_for_idle.set_value(value_before);
        });

        let adjustment_for_tick = adjustment.clone();
        message_scroller.add_tick_callback(move |_, _| {
            adjustment_for_tick.set_value(value_before);
            gtk::glib::ControlFlow::Break
        });
    }
}

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
        glib::timeout_add_local_once(Duration::from_millis(80), move || {
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
                render_conversation(widgets, &state, sender);
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
                    replace_current_messages(&mut state, chat_id.clone(), messages);
                    state.loading_chat_id = None;
                    (true, should_clear_composer)
                } else if state.selected_chat_id.as_deref() == Some(chat_id.as_str()) {
                    replace_current_messages(&mut state, chat_id.clone(), messages);
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
            render_conversation(widgets, &state, sender);
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
            render_conversation(widgets, &state, sender);
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
                render_conversation(widgets, &state, sender);
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
                render_conversation(widgets, &state, sender);
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
            let was_near_bottom = is_scroller_near_bottom(widgets);
            let transition = {
                let mut s = state.borrow_mut();
                apply_new_message(&mut s, message)
            };

            if transition.is_for_selected_chat {
                let state = state.borrow();
                render_conversation(widgets, &state, sender);
                if was_near_bottom {
                    scroll_messages_to_bottom(widgets);
                } else {
                    show_messages_below_button(widgets);
                }
            }
            if transition.should_mark_read {
                request_mark_chat_read(sender.clone(), transition.chat_id);
            }
        }
        UiMessage::MessageUpdated { message } => {
            let restore_scroll = preserve_scroll_position_for_update(widgets);
            let updated = {
                let mut s = state.borrow_mut();
                apply_message_update(&mut s, message)
            };

            if updated {
                let state = state.borrow();
                render_conversation(widgets, &state, sender);
                restore_scroll();
            }
        }
        UiMessage::ChatUpdated {
            chat,
            previous_chat_id,
        } => {
            let header_changed = {
                let mut s = state.borrow_mut();
                apply_chat_update(&mut s, chat, previous_chat_id.as_deref())
            };

            let state_borrow = state.borrow();
            if header_changed {
                render_conversation_header(widgets, &state_borrow);
            }
            drop(state_borrow);
            schedule_chat_list_render(state, sender.clone());
        }
        UiMessage::HistorySyncProgress {
            sync_type,
            progress_percent,
            chunk_order,
            conversations_in_chunk,
            messages_in_chunk,
            is_complete,
        } => {
            let (snapshot, refresh_chat_id) = {
                let mut s = state.borrow_mut();
                let update = apply_history_sync_progress(
                    &mut s,
                    sync_type,
                    progress_percent,
                    chunk_order,
                    conversations_in_chunk,
                    messages_in_chunk,
                    is_complete,
                );
                let refresh_chat_id = if update.completed_visible_sync {
                    take_selected_history_refresh_chat(&mut s)
                } else {
                    None
                };
                (update.snapshot, refresh_chat_id)
            };
            render_history_sync_strip(widgets, snapshot.as_ref());
            if snapshot.is_none() && is_complete {
                schedule_chat_list_render(state, sender.clone());
            }
            if let Some(chat_id) = refresh_chat_id {
                let generation = next_message_request_generation(state);
                request_messages(sender.clone(), chat_id, generation);
            }
        }
        UiMessage::HistoryBackfilled {
            chat_id,
            messages_added,
        } => {
            let should_render_list = {
                let mut s = state.borrow_mut();
                note_history_backfilled(&mut s, &chat_id);
                messages_added > 0 && s.selected_chat_id.as_deref() != Some(chat_id.as_str())
            };

            if should_render_list {
                schedule_chat_list_render(state, sender.clone());
            }
        }
        UiMessage::OlderMessagesLoaded {
            chat_id,
            anchor_message_id,
            generation,
            messages,
        } => {
            let added = {
                let mut s = state.borrow_mut();
                let in_flight = matches!(
                    &s.older_fetch_in_flight,
                    Some(in_flight)
                        if in_flight.chat_id == chat_id
                            && in_flight.anchor_message_id == anchor_message_id
                            && in_flight.generation == generation
                );
                if !in_flight {
                    return;
                }
                s.older_fetch_in_flight = None;
                if messages.is_empty() {
                    note_history_exhausted(&mut s, chat_id.clone());
                    0
                } else {
                    prepend_older_messages(&mut s, &chat_id, messages)
                }
            };

            if added > 0 {
                let anchor = begin_prepend(widgets);
                let state_borrow = state.borrow();
                render_conversation(widgets, &state_borrow, sender);
                finish_prepend(widgets, anchor);
            } else {
                widgets.older_messages_loading.set_visible(false);
            }
        }
        UiMessage::OlderMessagesFailed {
            chat_id,
            anchor_message_id,
            generation,
            error,
        } => {
            let was_in_flight = {
                let mut s = state.borrow_mut();
                let matched = matches!(
                    &s.older_fetch_in_flight,
                    Some(in_flight)
                        if in_flight.chat_id == chat_id
                            && in_flight.anchor_message_id == anchor_message_id
                            && in_flight.generation == generation
                );
                if matched {
                    s.older_fetch_in_flight = None;
                }
                matched
            };

            if was_in_flight {
                widgets.older_messages_loading.set_visible(false);
                let state_borrow = state.borrow();
                show_banner_notice_preserving_action(
                    widgets,
                    &state_borrow,
                    &format!("Could not load older messages: {error}"),
                );
            }
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
            render_conversation(widgets, &state, sender);
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
    render_conversation(widgets, &state, sender);
    update_navigation_state(widgets, &state);

    widgets
        .login_status_label
        .set_text(&format_login_state(daemon_state, &detail));
    update_banner(widgets, &state);
}
