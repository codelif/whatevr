use std::{cell::RefCell, rc::Rc};

use crate::ui::{
    commands::{
        report_frontend_session_state, request_messages, request_send_text,
        schedule_conversation_loading, stop_current_composing,
    },
    context::UiSender,
    navigation::update_navigation_state,
    render::{
        composer::{composer_text, render_composer_state},
        conversation::render_conversation,
    },
    state::{UiState, composer_send_allowed},
    widgets::Widgets,
};

pub fn open_chat_at_index(
    index: usize,
    force_reload: bool,
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &UiSender,
) {
    let chat = {
        let state = state.borrow();
        state.chats.get(index).cloned()
    };

    let Some(chat) = chat else {
        return;
    };

    let chat_id = chat.id.clone();
    let mut should_request = true;
    let generation;

    {
        let mut state = state.borrow_mut();
        let already_selected = state.selected_chat_id.as_deref() == Some(chat_id.as_str());

        if !already_selected {
            stop_current_composing(&mut state, sender);
        }

        if already_selected
            && state.current_messages_chat_id.as_deref() == Some(chat_id.as_str())
            && !force_reload
        {
            should_request = false;
            generation = state.message_request_generation;
        } else if already_selected && force_reload {
            state.message_request_generation = state.message_request_generation.wrapping_add(1);
            generation = state.message_request_generation;
            state.loading_chat_id = Some(chat_id.clone());
            state.composer_error.clear();
        } else {
            state.message_request_generation = state.message_request_generation.wrapping_add(1);
            generation = state.message_request_generation;
            state.pending_chat_id = Some(chat_id.clone());
            state.loading_chat_id = None;
            state.composer_error.clear();
        }
    }

    report_frontend_session_state(state, sender.clone());

    if !should_request || force_reload {
        let state = state.borrow();
        render_conversation(widgets, &state, sender);
        update_navigation_state(widgets, &state);
    } else {
        let state = state.borrow();
        render_composer_state(widgets, &state);
    }

    if !should_request {
        return;
    }

    schedule_conversation_loading(sender.clone(), chat_id.clone(), generation);
    request_messages(sender.clone(), chat_id, generation);
}

pub fn open_chat_by_id(
    chat_id: String,
    force_reload: bool,
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &UiSender,
) -> bool {
    let index = {
        let state = state.borrow();
        state.chats.iter().position(|chat| chat.id == chat_id)
    };

    let Some(index) = index else {
        return false;
    };

    open_chat_at_index(index, force_reload, widgets, state, sender);
    true
}

pub fn submit_composer_message(
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &UiSender,
) {
    let text = composer_text(&widgets.composer_text_view);

    if text.trim().is_empty() {
        return;
    }

    let chat_id = {
        let mut state = state.borrow_mut();

        if !composer_send_allowed(&state) {
            return;
        }

        let Some(chat_id) = state.selected_chat_id.clone() else {
            return;
        };

        state.send_in_flight = true;
        state.composer_error.clear();

        stop_current_composing(&mut state, sender);

        chat_id
    };

    {
        let state = state.borrow();
        render_composer_state(widgets, &state);
    }

    request_send_text(sender.clone(), chat_id, text);
}
