use std::{cell::RefCell, rc::Rc};

use adw::prelude::*;
use gtk::{gdk, gio, glib};

use crate::ui::{
    chat_actions::{open_chat_at_index, submit_composer_message},
    commands::{
        request_chats, request_messages, request_send_media, request_set_chat_presence,
        request_status, schedule_typing_idle, stop_current_composing,
    },
    context::{UiSender, send_ui},
    dialogs::show_logout_confirmation,
    message::UiMessage,
    navigation::update_navigation_state,
    render::composer::{
        render_composer_state, resize_composer, scroll_composer_to_bottom_if_needed,
    },
    state::{UiState, next_message_request_generation},
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
}
