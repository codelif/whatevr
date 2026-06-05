use std::{cell::RefCell, rc::Rc, time::Duration};

use adw::prelude::*;
use gtk::{gdk, gio, glib};

use crate::ui::{
    chat_actions::{open_chat_at_index, submit_composer_message},
    commands::{
        request_chats, request_messages, request_older_messages, request_send_media,
        request_set_chat_presence, request_status, schedule_typing_idle, stop_current_composing,
    },
    context::{UiSender, send_ui},
    dialogs::show_logout_confirmation,
    message::UiMessage,
    navigation::update_navigation_state,
    render::composer::{
        render_composer_state, resize_composer, scroll_composer_to_bottom_if_needed,
    },
    render::conversation::{scroll_messages_to_bottom, update_messages_below_button},
    state::{OlderFetchInFlight, UiState, next_message_request_generation},
    widgets::Widgets,
};

pub fn connect(
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &UiSender,
    refresh_button: &gtk::Button,
) {
    let refresh_sender = sender.clone();
    let refresh_state = state.clone();

    refresh_button.connect_clicked(move |_| {
        request_status(refresh_sender.clone());
        request_chats(refresh_sender.clone());

        let selected_chat_id = refresh_state.borrow().selected_chat_id.clone();

        if let Some(chat_id) = selected_chat_id {
            let generation = next_message_request_generation(&refresh_state);
            request_messages(refresh_sender.clone(), chat_id, generation);
        }
    });

    let logout_widgets = widgets.clone();
    let logout_state = state.clone();
    let logout_sender = sender.clone();

    widgets.logout_button.connect_clicked(move |_| {
        stop_current_composing(&mut logout_state.borrow_mut(), &logout_sender);
        show_logout_confirmation(&logout_widgets, &logout_sender);
    });

    let back_widgets = widgets.clone();
    let back_state = state.clone();

    widgets.back_button.connect_clicked(move |_| {
        back_widgets.split_view.set_show_content(false);
        update_navigation_state(&back_widgets, &back_state.borrow());
    });

    let collapse_widgets = widgets.clone();
    let collapse_state = state.clone();

    widgets.split_view.connect_collapsed_notify(move |_| {
        update_navigation_state(&collapse_widgets, &collapse_state.borrow());
    });

    let select_widgets = widgets.clone();
    let select_state = state.clone();
    let select_sender = sender.clone();

    widgets.chat_list.connect_row_selected(move |_, row| {
        if select_widgets.syncing_chat_selection.get() {
            return;
        }

        if let Some(row) = row {
            open_chat_at_index(
                row.index() as usize,
                false,
                &select_widgets,
                &select_state,
                &select_sender,
            );
        }
    });

    let activate_widgets = widgets.clone();
    let activate_state = state.clone();
    let activate_sender = sender.clone();

    widgets.chat_list.connect_row_activated(move |_, row| {
        if activate_widgets.split_view.shows_content() {
            return;
        }

        open_chat_at_index(
            row.index() as usize,
            false,
            &activate_widgets,
            &activate_state,
            &activate_sender,
        );
    });

    let send_widgets = widgets.clone();
    let send_state = state.clone();
    let send_sender = sender.clone();

    widgets.composer_send_button.connect_clicked(move |_| {
        submit_composer_message(&send_widgets, &send_state, &send_sender);
    });

    let bottom_widgets = widgets.clone();
    widgets.scroll_to_bottom_button.connect_clicked(move |_| {
        scroll_messages_to_bottom(&bottom_widgets);
    });

    let resize_widgets = widgets.clone();

    widgets
        .composer_scroller
        .connect_notify_local(Some("allocated-width"), move |_, _| {
            let idle_widgets = resize_widgets.clone();

            glib::idle_add_local_once(move || {
                resize_composer(&idle_widgets);
            });
        });

    let typing_state = state.clone();
    let typing_widgets = widgets.clone();
    let typing_sender = sender.clone();

    widgets
        .composer_text_view
        .buffer()
        .connect_changed(move |buf| {
            let non_empty = buf.char_count() > 0;

            let (chat_id, generation, should_send) = {
                let mut state = typing_state.borrow_mut();

                let chat_id = state.selected_chat_id.clone();
                state.typing_generation = state.typing_generation.wrapping_add(1);

                let generation = state.typing_generation;
                let should_send = non_empty && !state.last_sent_composing;

                if should_send {
                    state.last_sent_composing = true;
                }

                if !non_empty && state.last_sent_composing {
                    stop_current_composing(&mut state, &typing_sender);
                }

                (chat_id, generation, should_send)
            };

            if let Some(chat_id) = chat_id.clone() {
                if should_send {
                    request_set_chat_presence(typing_sender.clone(), chat_id.clone(), true);
                }

                if non_empty {
                    schedule_typing_idle(typing_sender.clone(), chat_id, generation);
                }
            }

            let state = typing_state.borrow();
            render_composer_state(&typing_widgets, &state);

            let idle_widgets = typing_widgets.clone();

            glib::idle_add_local_once(move || {
                resize_composer(&idle_widgets);
                scroll_composer_to_bottom_if_needed(&idle_widgets);
            });
        });

    let attach_widgets = widgets.clone();
    let attach_state = state.clone();
    let attach_sender = sender.clone();

    widgets.composer_attach_button.connect_clicked(move |_| {
        let chat_id = attach_state.borrow().selected_chat_id.clone();
        let Some(chat_id) = chat_id else {
            return;
        };

        let window = attach_widgets
            .conversation_stack
            .root()
            .and_downcast::<gtk::Window>();

        let dialog = gtk::FileDialog::new();
        dialog.set_title("Choose an image");

        let filter = gtk::FileFilter::new();
        filter.add_mime_type("image/*");
        filter.set_name(Some("Images"));

        let filters = gio::ListStore::new::<gtk::FileFilter>();
        filters.append(&filter);

        dialog.set_filters(Some(&filters));
        dialog.set_default_filter(Some(&filter));

        let sender = attach_sender.clone();

        dialog.open(window.as_ref(), None::<&gio::Cancellable>, move |result| {
            let Ok(file) = result else {
                send_ui(&sender, UiMessage::Notice("No image selected.".to_string()));
                return;
            };

            let Some(path) = file.path() else {
                send_ui(
                    &sender,
                    UiMessage::Notice("Only local image files can be attached.".to_string()),
                );
                return;
            };

            if let Some(path_str) = path.to_str() {
                request_send_media(sender.clone(), chat_id.clone(), path_str.to_string());
            } else {
                send_ui(
                    &sender,
                    UiMessage::Notice("This file path cannot be attached.".to_string()),
                );
            }
        });
    });

    let key_widgets = widgets.clone();
    let key_state = state.clone();
    let key_sender = sender.clone();

    let composer_key_controller = gtk::EventControllerKey::new();

    composer_key_controller.connect_key_pressed(move |_, key, _, modifiers| {
        if key == gdk::Key::Return || key == gdk::Key::KP_Enter {
            if modifiers.contains(gdk::ModifierType::SHIFT_MASK) {
                return glib::Propagation::Proceed;
            }

            submit_composer_message(&key_widgets, &key_state, &key_sender);
            return glib::Propagation::Stop;
        }

        glib::Propagation::Proceed
    });

    widgets
        .composer_text_view
        .add_controller(composer_key_controller);

    install_scroll_pagination(widgets, state, sender);
}

// Pixel distance to the top that arms an older-messages fetch.
const TOP_TRIGGER_PX: f64 = 120.0;
// How long the scroll-burst timer sleeps before considering the gesture
// over. Increase if a single fast fling still loads multiple pages.
const SCROLL_BURST_END_MS: u64 = 220;
// Anything smaller than this is treated as adjustment noise rather than
// a real upward scroll.
const DIRECTION_EPS: f64 = 1.0;

fn install_scroll_pagination(
    widgets: &Rc<crate::ui::widgets::Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &UiSender,
) {
    // value-changed handles the common case: user is mid-scroll, value
    // moves toward 0, and we cross the trigger threshold.
    {
        let scroll_widgets = widgets.clone();
        let pagination_state = state.clone();
        let pagination_sender = sender.clone();

        widgets
            .message_scroller
            .vadjustment()
            .connect_value_changed(move |adjustment| {
                update_messages_below_button(&scroll_widgets);

                let scroll = &scroll_widgets.scroll_state;
                let value = adjustment.value();

                if scroll.suppress_value_handler.get() {
                    scroll.last_value.set(value);
                    return;
                }

                let last_value = scroll.last_value.get();
                scroll.last_value.set(value);

                if scroll.loading.get() {
                    return;
                }

                if scroll.block_rearm_until_scroll_stops.get() {
                    return;
                }

                if !scroll.prepend_armed.get() {
                    return;
                }

                if adjustment.upper() <= adjustment.page_size() {
                    return;
                }

                if last_value.is_nan() {
                    return;
                }

                let moving_up = value < last_value - DIRECTION_EPS;

                if moving_up && value <= TOP_TRIGGER_PX {
                    arm_and_kick(&scroll_widgets, &pagination_state, &pagination_sender);
                }
            });
    }

    // EventControllerScroll handles the "already at top" case: when the
    // viewport is clamped at value=0, more upward scroll input does not
    // change the adjustment value, so notify::value never fires. Use the
    // raw scroll event plus an idle probe to detect that situation, and
    // also restart the burst-end timer on every scroll event.
    let scroll_controller =
        gtk::EventControllerScroll::new(gtk::EventControllerScrollFlags::VERTICAL);
    scroll_controller.set_propagation_phase(gtk::PropagationPhase::Capture);

    {
        let scroll_widgets = widgets.clone();
        let pagination_state = state.clone();
        let pagination_sender = sender.clone();

        scroll_controller.connect_scroll(move |_, _dx, dy| {
            restart_scroll_burst_timer(&scroll_widgets);

            let adjustment = scroll_widgets.message_scroller.vadjustment();
            if dy < 0.0 && adjustment.value() <= TOP_TRIGGER_PX {
                schedule_top_scroll_probe(&scroll_widgets, &pagination_state, &pagination_sender);
            }

            glib::Propagation::Proceed
        });
    }

    widgets.message_scroller.add_controller(scroll_controller);
}

fn restart_scroll_burst_timer(widgets: &Rc<crate::ui::widgets::Widgets>) {
    let scroll = widgets.scroll_state.clone();

    if let Some(source) = scroll.scroll_burst_source_id.borrow_mut().take() {
        source.remove();
    }

    let widgets_for_timeout = widgets.clone();
    let source =
        glib::timeout_add_local_once(Duration::from_millis(SCROLL_BURST_END_MS), move || {
            on_scroll_burst_finished(&widgets_for_timeout);
        });
    *scroll.scroll_burst_source_id.borrow_mut() = Some(source);
}

fn on_scroll_burst_finished(widgets: &Rc<crate::ui::widgets::Widgets>) {
    let scroll = &widgets.scroll_state;
    *scroll.scroll_burst_source_id.borrow_mut() = None;

    if scroll.block_rearm_until_scroll_stops.get() {
        scroll.block_rearm_until_scroll_stops.set(false);
        scroll.prepend_armed.set(true);

        // Take the post-restore value as the new baseline so the next
        // value-changed comparison starts fresh — otherwise the residual
        // delta from the restore could read as another upward scroll.
        let value = widgets.message_scroller.vadjustment().value();
        scroll.last_value.set(value);
    }
}

fn schedule_top_scroll_probe(
    widgets: &Rc<crate::ui::widgets::Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &UiSender,
) {
    let scroll = &widgets.scroll_state;

    if scroll.top_scroll_probe_scheduled.get() {
        return;
    }
    scroll.top_scroll_probe_scheduled.set(true);

    let widgets_for_idle = widgets.clone();
    let state_for_idle = state.clone();
    let sender_for_idle = sender.clone();

    glib::idle_add_local_once(move || {
        let scroll = &widgets_for_idle.scroll_state;
        scroll.top_scroll_probe_scheduled.set(false);

        if scroll.loading.get() {
            return;
        }
        if scroll.block_rearm_until_scroll_stops.get() {
            return;
        }
        if !scroll.prepend_armed.get() {
            return;
        }

        let adjustment = widgets_for_idle.message_scroller.vadjustment();
        if adjustment.upper() <= adjustment.page_size() {
            return;
        }

        if adjustment.value() <= TOP_TRIGGER_PX {
            arm_and_kick(&widgets_for_idle, &state_for_idle, &sender_for_idle);
        }
    });
}

fn arm_and_kick(
    widgets: &Rc<crate::ui::widgets::Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &UiSender,
) {
    let scroll = &widgets.scroll_state;

    // One-prepend-per-burst lock: regardless of whether the daemon
    // actually has older messages, we hold off on triggering a second
    // prepend until the current scroll burst has ended.
    scroll.prepend_armed.set(false);
    scroll.block_rearm_until_scroll_stops.set(true);

    if try_kick_older_fetch(state, sender) {
        widgets.older_messages_loading.set_visible(true);
    }
}

fn try_kick_older_fetch(state: &Rc<RefCell<UiState>>, sender: &UiSender) -> bool {
    let request = {
        let mut s = state.borrow_mut();

        let Some(chat_id) = s.selected_chat_id.clone() else {
            return false;
        };
        if s.current_messages_chat_id.as_deref() != Some(chat_id.as_str()) {
            return false;
        }
        if s.older_fetch_in_flight.is_some() {
            return false;
        }
        if s.chats_with_exhausted_history.contains(&chat_id) {
            // We've already learned that the local store has nothing
            // older for this chat; don't keep poking the daemon.
            // Cleared on logout / reload (clear_local_ui_state) or when
            // a new history-sync chunk arrives for the chat
            // (note_history_backfilled).
            return false;
        }
        let Some(anchor) = s.current_messages.first().map(|m| m.id.clone()) else {
            return false;
        };

        s.message_request_generation = s.message_request_generation.wrapping_add(1);
        let generation = s.message_request_generation;

        s.older_fetch_in_flight = Some(OlderFetchInFlight {
            chat_id: chat_id.clone(),
            anchor_message_id: anchor.clone(),
            generation,
        });

        (chat_id, anchor, generation)
    };

    let (chat_id, anchor, generation) = request;
    request_older_messages(sender.clone(), chat_id, anchor, generation);
    true
}
