use std::{cell::RefCell, rc::Rc, time::Duration};

use gtk::prelude::*;

use crate::config::{
    CONVERSATION_EMPTY_PAGE, CONVERSATION_LOADING_PAGE, CONVERSATION_MESSAGES_PAGE,
    CONVERSATION_PLACEHOLDER_PAGE,
};
use crate::proto;
use crate::ui::{
    context::UiSender,
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

pub fn render_conversation(widgets: &Widgets, state: &UiState, sender: &UiSender) {
    let Some(selected_chat_id) = state.selected_chat_id.as_deref() else {
        widgets
            .conversation_stack
            .set_visible_child_name(CONVERSATION_PLACEHOLDER_PAGE);
        cancel_message_prepend(widgets);
        render_conversation_header(widgets, state);
        reset_rendered_messages(widgets);
        reset_messages_below(widgets);
        render_composer_state(widgets, state);
        return;
    };

    if !state.chats.iter().any(|chat| chat.id == selected_chat_id) {
        widgets
            .conversation_stack
            .set_visible_child_name(CONVERSATION_PLACEHOLDER_PAGE);
        cancel_message_prepend(widgets);
        render_conversation_header(widgets, state);
        reset_rendered_messages(widgets);
        reset_messages_below(widgets);
        render_composer_state(widgets, state);
        return;
    }

    widgets
        .conversation_stack
        .set_visible_child_name("selected");
    widgets.older_messages_loading.set_visible(
        widgets.message_prepend_in_progress.get()
            || state
                .older_fetch_in_flight
                .as_ref()
                .map(|fetch| fetch.chat_id.as_str() == selected_chat_id)
                .unwrap_or(false),
    );

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
        cancel_message_prepend(widgets);
        reset_rendered_messages(widgets);
        reset_messages_below(widgets);
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
        cancel_message_prepend(widgets);
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

    sync_message_rows(widgets, selected_chat_id, &state.current_messages, sender);

    if is_newly_rendered_chat {
        reset_messages_below(widgets);
        reveal_messages_at_bottom(widgets);
    } else {
        widgets
            .conversation_content_stack
            .set_visible_child_name(CONVERSATION_MESSAGES_PAGE);
    }

    render_composer_state(widgets, state);
}

fn reset_rendered_messages(widgets: &Widgets) {
    let mut rendered = widgets.rendered_messages.borrow_mut();

    for entry in rendered.drain(..) {
        remove_message_row_if_child(widgets, &entry.row);
    }

    *widgets.rendered_chat_id.borrow_mut() = None;
}

fn cancel_message_prepend(widgets: &Widgets) {
    widgets
        .message_prepend_generation
        .set(widgets.message_prepend_generation.get().wrapping_add(1));
    widgets.message_prepend_in_progress.set(false);
    widgets.message_scroller.set_opacity(1.0);
    widgets.older_messages_loading.set_visible(false);
}

fn sync_message_rows(
    widgets: &Widgets,
    chat_id: &str,
    messages: &[proto::Message],
    sender: &UiSender,
) {
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

    if same_chat && sync_prepended_message_rows(widgets, &mut rendered, messages, sender) {
        return;
    }

    let mut common = 0;

    while common < messages.len()
        && common < rendered.len()
        && rendered[common].id == messages[common].id
    {
        common += 1;
    }

    for i in 0..common {
        if rendered[i].same_content(&messages[i]) {
            let new_status = messages[i].status;
            if rendered[i].status != new_status {
                rendered[i]
                    .meta_label
                    .set_text(&format_message_meta(&messages[i]));
                rendered[i].status = new_status;
            }
        } else {
            let (row, meta_label) = build_message_row(&messages[i], sender);
            remove_message_row_if_child(widgets, &rendered[i].row);
            widgets.message_box.insert_child_after(
                &row,
                i.checked_sub(1).map(|previous| &rendered[previous].row),
            );

            rendered[i] = RenderedMessage::new(&messages[i], row, meta_label);
        }
    }

    while rendered.len() > common {
        if let Some(entry) = rendered.pop() {
            remove_message_row_if_child(widgets, &entry.row);
        }
    }

    for new_msg in &messages[common..] {
        let (row, meta_label) = build_message_row(new_msg, sender);

        widgets.message_box.append(&row);

        rendered.push(RenderedMessage::new(new_msg, row, meta_label));
    }
}

fn sync_prepended_message_rows(
    widgets: &Widgets,
    rendered: &mut Vec<RenderedMessage>,
    messages: &[proto::Message],
    sender: &UiSender,
) -> bool {
    if rendered.is_empty() || messages.is_empty() {
        return false;
    }

    let Some(anchor_index) = messages
        .iter()
        .position(|message| message.id == rendered[0].id)
    else {
        return false;
    };
    if anchor_index == 0 || messages.len() < anchor_index + rendered.len() {
        return false;
    }

    let existing_messages = &messages[anchor_index..anchor_index + rendered.len()];
    let trailing_messages = &messages[anchor_index + rendered.len()..];
    if rendered
        .iter()
        .zip(existing_messages)
        .any(|(rendered, message)| rendered.id != message.id)
    {
        return false;
    }

    for (rendered, message) in rendered.iter_mut().zip(existing_messages) {
        let new_status = message.status;
        if rendered.status != new_status {
            rendered.meta_label.set_text(&format_message_meta(message));
            rendered.status = new_status;
        }
    }

    let mut prepended = Vec::with_capacity(anchor_index);
    for message in &messages[..anchor_index] {
        let (row, meta_label) = build_message_row(message, sender);
        prepended.push(RenderedMessage::new(message, row, meta_label));
    }

    for entry in prepended.iter().rev() {
        widgets
            .message_box
            .insert_child_after(&entry.row, None::<&gtk::Widget>);
    }

    rendered.splice(0..0, prepended);

    for message in trailing_messages {
        let (row, meta_label) = build_message_row(message, sender);
        widgets.message_box.append(&row);
        rendered.push(RenderedMessage::new(message, row, meta_label));
    }

    true
}

impl RenderedMessage {
    fn same_content(&self, message: &proto::Message) -> bool {
        self.text == message.text
            && self.media_mime_type == message.media_mime_type
            && self.media_local_path == message.media_local_path
    }

    fn new(message: &proto::Message, row: gtk::Box, meta_label: gtk::Label) -> Self {
        Self {
            id: message.id.clone(),
            status: message.status,
            row,
            meta_label,
            text: message.text.clone(),
            media_mime_type: message.media_mime_type.clone(),
            media_local_path: message.media_local_path.clone(),
        }
    }
}

fn remove_message_row_if_child(widgets: &Widgets, row: &gtk::Box) {
    if row.parent().as_ref() == Some(widgets.message_box.upcast_ref()) {
        if let Some(window) = widgets
            .message_scroller
            .root()
            .and_downcast::<gtk::Window>()
        {
            gtk::prelude::GtkWindowExt::set_focus(&window, None::<&gtk::Widget>);
        }

        widgets.message_box.remove(row);
    }
}

pub fn scroll_messages_to_bottom(widgets: &Widgets) {
    keep_messages_at_bottom(widgets, false);
    reset_messages_below(widgets);
}

pub fn show_messages_below_button(widgets: &Widgets) {
    let count = widgets.messages_below_count.get().saturating_add(1);
    widgets.messages_below_count.set(count);
    widgets.scroll_to_bottom_badge.set_text(&count.to_string());
    widgets.scroll_to_bottom_badge.set_visible(count > 0);
    widgets.scroll_to_bottom_icon.set_visible(false);
    widgets.scroll_to_bottom_button.set_visible(true);
}

pub fn update_messages_below_button(widgets: &Widgets) {
    if is_scroller_near_bottom(widgets) {
        reset_messages_below(widgets);
    } else {
        widgets.scroll_to_bottom_button.set_visible(true);
    }
}

pub fn reset_messages_below(widgets: &Widgets) {
    widgets.messages_below_count.set(0);
    widgets.scroll_to_bottom_badge.set_text("0");
    widgets.scroll_to_bottom_badge.set_visible(false);
    widgets.scroll_to_bottom_icon.set_visible(true);
    widgets.scroll_to_bottom_button.set_visible(false);
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
