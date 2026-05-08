use std::{cell::RefCell, rc::Rc, time::Duration};

use gtk::{glib, prelude::*};

use crate::config::{
    CONVERSATION_EMPTY_PAGE, CONVERSATION_LOADING_PAGE, CONVERSATION_MESSAGES_PAGE,
    CONVERSATION_PLACEHOLDER_PAGE,
};
use crate::proto;
use crate::ui::{
    context::UiSender, render::avatar::set_avatar_image, render::composer::render_composer_state,
    render::message_object::MessageObject, state::UiState, widgets::Widgets,
};
use crate::util::text::display_chat_name;

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

pub fn render_conversation(widgets: &Widgets, state: &UiState, _sender: &UiSender) {
    let Some(selected_chat_id) = state.selected_chat_id.as_deref() else {
        widgets
            .conversation_stack
            .set_visible_child_name(CONVERSATION_PLACEHOLDER_PAGE);
        cancel_message_prepend(widgets);
        render_conversation_header(widgets, state);
        reset_message_store(widgets);
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
        reset_message_store(widgets);
        reset_messages_below(widgets);
        render_composer_state(widgets, state);
        return;
    }

    widgets
        .conversation_stack
        .set_visible_child_name("selected");
    widgets.older_messages_loading.set_visible(
        widgets.scroll_state.loading.get()
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
        reset_message_store(widgets);
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
        reset_message_store(widgets);
        render_composer_state(widgets, state);
        return;
    }

    let is_newly_rendered_chat = widgets
        .rendered_chat_id
        .borrow()
        .as_deref()
        .map(|rendered_chat_id| rendered_chat_id != selected_chat_id)
        .unwrap_or(true);

    sync_message_store(widgets, selected_chat_id, &state.current_messages);

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

fn reset_message_store(widgets: &Widgets) {
    widgets.message_store.remove_all();
    *widgets.rendered_chat_id.borrow_mut() = None;
}

fn cancel_message_prepend(widgets: &Widgets) {
    let scroll = &widgets.scroll_state;

    if let Some(handler) = scroll.restore_upper_handler.borrow_mut().take() {
        widgets.message_scroller.vadjustment().disconnect(handler);
    }
    *scroll.restore_state.borrow_mut() = None;
    scroll.loading.set(false);
    scroll.prepend_armed.set(true);
    scroll.block_rearm_until_scroll_stops.set(false);
    scroll.suppress_value_handler.set(false);

    if let Some(source) = scroll.scroll_burst_source_id.borrow_mut().take() {
        source.remove();
    }

    widgets.message_scroller.set_opacity(1.0);
    widgets.older_messages_loading.set_visible(false);
}

// `sync_message_store` reconciles the gio::ListStore behind the message
// ListView with `messages`. It tries the two cheap fast paths first
// (prepend-only and append-only) so existing rows keep their identity —
// that's what lets the prepend anchor restore work.
fn sync_message_store(widgets: &Widgets, chat_id: &str, messages: &[proto::Message]) {
    let store = &widgets.message_store;

    let same_chat = widgets
        .rendered_chat_id
        .borrow()
        .as_deref()
        .map(|prev| prev == chat_id)
        .unwrap_or(false);

    if !same_chat {
        store.remove_all();
        *widgets.rendered_chat_id.borrow_mut() = Some(chat_id.to_string());
    }

    let n = store.n_items() as usize;

    if same_chat && n > 0 {
        if let Some(anchor_id) = first_store_id(store) {
            if let Some(anchor_idx) = messages.iter().position(|m| m.id == anchor_id) {
                if messages.len() >= anchor_idx + n
                    && existing_tail_matches(store, &messages[anchor_idx..anchor_idx + n])
                {
                    update_changed_items(store, &messages[anchor_idx..anchor_idx + n]);

                    if anchor_idx > 0 {
                        let prepend: Vec<MessageObject> = messages[..anchor_idx]
                            .iter()
                            .map(|m| MessageObject::new(m.clone()))
                            .collect();
                        store.splice(0, 0, &prepend);
                    }

                    for message in &messages[anchor_idx + n..] {
                        store.append(&MessageObject::new(message.clone()));
                    }

                    return;
                }
            }
        }
    }

    // Fallback: longest common prefix by id, then splice the tail.
    let mut common = 0usize;
    while common < messages.len() && (common as u32) < store.n_items() {
        let Some(item) = store.item(common as u32).and_downcast::<MessageObject>() else {
            break;
        };
        if item.id() != messages[common].id {
            break;
        }
        common += 1;
    }

    update_changed_items(store, &messages[..common]);

    let store_n = store.n_items() as usize;
    let new_tail: Vec<MessageObject> = messages[common..]
        .iter()
        .map(|m| MessageObject::new(m.clone()))
        .collect();
    store.splice(common as u32, (store_n - common) as u32, &new_tail);
}

fn first_store_id(store: &gtk::gio::ListStore) -> Option<String> {
    store
        .item(0)
        .and_downcast::<MessageObject>()
        .map(|m| m.id())
}

fn existing_tail_matches(store: &gtk::gio::ListStore, slice: &[proto::Message]) -> bool {
    for (i, expected) in slice.iter().enumerate() {
        let Some(item) = store.item(i as u32).and_downcast::<MessageObject>() else {
            return false;
        };
        if item.id() != expected.id {
            return false;
        }
    }
    true
}

fn update_changed_items(store: &gtk::gio::ListStore, expected: &[proto::Message]) {
    for (i, message) in expected.iter().enumerate() {
        let Some(item) = store.item(i as u32).and_downcast::<MessageObject>() else {
            continue;
        };
        let needs_update = {
            let current = item.message();
            current.text != message.text
                || current.status != message.status
                || current.media_local_path != message.media_local_path
                || current.media_mime_type != message.media_mime_type
                || current.media_width != message.media_width
                || current.media_height != message.media_height
        };
        if needs_update {
            item.set_message(message.clone());
            // Force the factory to rebind this row so the visible widgets
            // reflect the new state. With a virtualized ListView this is
            // a no-op for off-screen rows.
            store.items_changed(i as u32, 1, 1);
        }
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
    set_value_silently(widgets, &adjustment, scroll_to_bottom_value(&adjustment));

    let adjustment_for_tick = adjustment.clone();
    let tick_state = Rc::new(RefCell::new((0usize, 0usize, f64::NAN, f64::NAN, f64::NAN)));
    let tick_state_for_callback = tick_state.clone();
    let generation_for_tick = widgets.message_scroll_generation.clone();
    let widgets_for_tick = widgets.clone();

    widgets.message_scroller.add_tick_callback(move |_, _| {
        if generation_for_tick.get() != scroll_generation {
            return gtk::glib::ControlFlow::Break;
        }

        set_value_silently(
            &widgets_for_tick,
            &adjustment_for_tick,
            scroll_to_bottom_value(&adjustment_for_tick),
        );

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
                set_value_silently(
                    &widgets_for_tick,
                    &adjustment_for_tick,
                    scroll_to_bottom_value(&adjustment_for_tick),
                );
                widgets_for_tick.message_scroller.set_opacity(1.0);
            }

            // Record the resting position so the value-changed handler
            // doesn't see the reveal as a user-driven scroll upward.
            widgets_for_tick
                .scroll_state
                .last_value
                .set(adjustment_for_tick.value());

            gtk::glib::ControlFlow::Break
        } else {
            gtk::glib::ControlFlow::Continue
        }
    });

    let adjustment_for_idle = adjustment.clone();
    let generation_for_idle = widgets.message_scroll_generation.clone();
    let widgets_for_idle = widgets.clone();

    gtk::glib::idle_add_local_once(move || {
        if generation_for_idle.get() != scroll_generation {
            return;
        }

        set_value_silently(
            &widgets_for_idle,
            &adjustment_for_idle,
            scroll_to_bottom_value(&adjustment_for_idle),
        );
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

fn scroll_to_bottom_value(adjustment: &gtk::Adjustment) -> f64 {
    (adjustment.upper() - adjustment.page_size()).max(0.0)
}

fn set_value_silently(widgets: &Widgets, adjustment: &gtk::Adjustment, value: f64) {
    widgets.scroll_state.suppress_value_handler.set(true);
    adjustment.set_value(value);
    widgets.scroll_state.last_value.set(adjustment.value());

    let scroll_state = widgets.scroll_state.clone();
    glib::idle_add_local_once(move || {
        scroll_state.suppress_value_handler.set(false);
    });
}

pub fn is_scroller_near_bottom(widgets: &Widgets) -> bool {
    let adjustment = widgets.message_scroller.vadjustment();
    let max = adjustment.upper() - adjustment.page_size();

    if max <= 0.0 {
        return true;
    }

    adjustment.value() >= max - 40.0
}
