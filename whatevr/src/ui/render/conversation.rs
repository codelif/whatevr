use std::{cell::RefCell, rc::Rc, time::Duration};

use gtk::prelude::*;

use crate::config::{
    CONVERSATION_EMPTY_PAGE, CONVERSATION_LOADING_PAGE, CONVERSATION_MESSAGES_PAGE,
    CONVERSATION_PLACEHOLDER_PAGE,
};
use crate::proto;
use crate::ui::{
    render::{
        avatar::set_avatar_image,
        composer::render_composer_state,
        message_row::{RenderedMessage, build_message_row},
    },
    state::UiState,
    widgets::Widgets,
};
use crate::util::{text::display_chat_name, time::format_message_meta};

pub fn render_conversation_header(widgets: &Widgets, state: &UiState) {
    let Some(selected_chat_id) = state.selected_chat_id.as_deref() else {
        widgets.conversation_header.set_visible(false);
        return;
    };

    let Some(chat) = state.chats.iter().find(|chat| chat.id == selected_chat_id) else {
        widgets.conversation_header.set_visible(false);
        return;
    };

    widgets.conversation_header.set_visible(true);
    widgets
        .conversation_avatar
        .set_text(Some(display_chat_name(chat)));
    set_avatar_image(&widgets.conversation_avatar, &chat.avatar_local_path);
    widgets.conversation_title.set_text(display_chat_name(chat));
}

pub fn render_conversation(widgets: &Widgets, state: &UiState) {
    let Some(selected_chat_id) = state.selected_chat_id.as_deref() else {
        widgets
            .conversation_stack
            .set_visible_child_name(CONVERSATION_PLACEHOLDER_PAGE);
        render_conversation_header(widgets, state);
        reset_rendered_messages(widgets);
        render_composer_state(widgets, state);
        return;
    };

    if !state.chats.iter().any(|chat| chat.id == selected_chat_id) {
        widgets
            .conversation_stack
            .set_visible_child_name(CONVERSATION_PLACEHOLDER_PAGE);
        render_conversation_header(widgets, state);
        reset_rendered_messages(widgets);
        render_composer_state(widgets, state);
        return;
    }

    widgets
        .conversation_stack
        .set_visible_child_name("selected");

    render_conversation_header(widgets, state);

    if state.loading_chat_id.as_deref() == Some(selected_chat_id) {
        widgets
            .conversation_loading_page
            .set_title("Loading messages");
        widgets
            .conversation_loading_page
            .set_description(Some("Reading the latest 50 messages from the local store."));
        widgets
            .conversation_content_stack
            .set_visible_child_name(CONVERSATION_LOADING_PAGE);
        reset_rendered_messages(widgets);
        render_composer_state(widgets, state);
        return;
    }

    if state.current_messages_chat_id.as_deref() != Some(selected_chat_id) {
        render_composer_state(widgets, state);
        return;
    }

    if state.current_messages.is_empty() {
        widgets
            .conversation_content_stack
            .set_visible_child_name(CONVERSATION_EMPTY_PAGE);
        reset_rendered_messages(widgets);
        render_composer_state(widgets, state);
        return;
    }

    let is_newly_rendered_chat = widgets
        .rendered_chat_id
        .borrow()
        .as_deref()
        .map(|rendered_chat_id| rendered_chat_id != selected_chat_id)
        .unwrap_or(true);

    sync_message_rows(widgets, selected_chat_id, &state.current_messages);

    if is_newly_rendered_chat {
        reveal_messages_at_bottom(widgets);
    } else {
        widgets.message_scroller.set_opacity(1.0);
        widgets
            .conversation_content_stack
            .set_visible_child_name(CONVERSATION_MESSAGES_PAGE);
    }

    render_composer_state(widgets, state);
}

fn reset_rendered_messages(widgets: &Widgets) {
    let mut rendered = widgets.rendered_messages.borrow_mut();

    for entry in rendered.drain(..) {
        widgets.message_box.remove(&entry.row);
    }

    *widgets.rendered_chat_id.borrow_mut() = None;
}

fn sync_message_rows(widgets: &Widgets, chat_id: &str, messages: &[proto::Message]) {
    let same_chat = widgets
        .rendered_chat_id
        .borrow()
        .as_deref()
        .map(|prev| prev == chat_id)
        .unwrap_or(false);

    if !same_chat {
        reset_rendered_messages(widgets);
        *widgets.rendered_chat_id.borrow_mut() = Some(chat_id.to_string());
    }

    let mut rendered = widgets.rendered_messages.borrow_mut();

    let mut common = 0;

    while common < messages.len() && common < rendered.len() {
        if rendered[common].id != messages[common].id {
            break;
        }

        common += 1;
    }

    for i in 0..common {
        let new_status = messages[i].status;

        if rendered[i].status != new_status {
            rendered[i]
                .meta_label
                .set_text(&format_message_meta(&messages[i]));
            rendered[i].status = new_status;
        }
    }

    while rendered.len() > common {
        if let Some(entry) = rendered.pop() {
            widgets.message_box.remove(&entry.row);
        }
    }

    for new_msg in &messages[common..] {
        let (row, meta_label) = build_message_row(new_msg);

        widgets.message_box.append(&row);

        rendered.push(RenderedMessage {
            id: new_msg.id.clone(),
            status: new_msg.status,
            row,
            meta_label,
        });
    }
}

pub fn scroll_messages_to_bottom(widgets: &Widgets) {
    keep_messages_at_bottom(widgets, false);
}

fn reveal_messages_at_bottom(widgets: &Widgets) {
    widgets.message_scroller.set_opacity(0.0);
    widgets
        .conversation_content_stack
        .set_visible_child_name(CONVERSATION_MESSAGES_PAGE);

    keep_messages_at_bottom(widgets, true);
}

fn keep_messages_at_bottom(widgets: &Widgets, reveal_after_layout: bool) {
    let scroll_generation = widgets.message_scroll_generation.get().wrapping_add(1);
    widgets.message_scroll_generation.set(scroll_generation);

    let adjustment = widgets.message_scroller.vadjustment();
    scroll_adjustment_to_bottom(&adjustment);

    let adjustment_for_tick = adjustment.clone();
    let tick_state = Rc::new(RefCell::new((0usize, 0usize, f64::NAN, f64::NAN, f64::NAN)));
    let tick_state_for_callback = tick_state.clone();
    let generation_for_tick = widgets.message_scroll_generation.clone();
    let message_scroller_for_tick = widgets.message_scroller.clone();

    widgets.message_scroller.add_tick_callback(move |_, _| {
        if generation_for_tick.get() != scroll_generation {
            return gtk::glib::ControlFlow::Break;
        }

        scroll_adjustment_to_bottom(&adjustment_for_tick);

        let upper = adjustment_for_tick.upper();
        let page_size = adjustment_for_tick.page_size();
        let value = adjustment_for_tick.value();
        let max = (upper - page_size).max(0.0);

        let mut tick_state = tick_state_for_callback.borrow_mut();

        let count = tick_state.0 + 1;

        let stable_count = if nearly_equal(tick_state.2, upper)
            && nearly_equal(tick_state.3, page_size)
            && nearly_equal(tick_state.4, max)
            && value >= max - 0.5
        {
            tick_state.1 + 1
        } else {
            0
        };

        *tick_state = (count, stable_count, upper, page_size, max);

        if count >= 90 || (count >= 6 && stable_count >= 4) {
            if reveal_after_layout {
                scroll_adjustment_to_bottom(&adjustment_for_tick);
                message_scroller_for_tick.set_opacity(1.0);
            }

            gtk::glib::ControlFlow::Break
        } else {
            gtk::glib::ControlFlow::Continue
        }
    });

    let adjustment_for_idle = adjustment.clone();
    let generation_for_idle = widgets.message_scroll_generation.clone();

    gtk::glib::idle_add_local_once(move || {
        if generation_for_idle.get() != scroll_generation {
            return;
        }

        scroll_adjustment_to_bottom(&adjustment_for_idle);
    });

    if reveal_after_layout {
        let generation_for_timeout = widgets.message_scroll_generation.clone();
        let message_scroller_for_timeout = widgets.message_scroller.clone();

        gtk::glib::timeout_add_local_once(Duration::from_secs(4), move || {
            if generation_for_timeout.get() != scroll_generation {
                return;
            }

            message_scroller_for_timeout.set_opacity(1.0);
        });
    }
}

fn nearly_equal(left: f64, right: f64) -> bool {
    if left.is_nan() || right.is_nan() {
        false
    } else {
        (left - right).abs() <= 0.5
    }
}

fn scroll_adjustment_to_bottom(adjustment: &gtk::Adjustment) {
    let max = adjustment.upper() - adjustment.page_size();
    adjustment.set_value(max.max(0.0));
}

pub fn is_scroller_near_bottom(widgets: &Widgets) -> bool {
    let adjustment = widgets.message_scroller.vadjustment();
    let max = adjustment.upper() - adjustment.page_size();

    if max <= 0.0 {
        return true;
    }

    adjustment.value() >= max - 40.0
}
