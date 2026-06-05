use gtk::prelude::*;

use crate::config::{COMPOSER_HEIGHT_SLACK, COMPOSER_MAX_HEIGHT};
use crate::ui::{state::UiState, state::composer_send_allowed, widgets::Widgets};

pub fn render_composer_state(widgets: &Widgets, state: &UiState) {
    let has_selected_chat = state.selected_chat_id.is_some();
    let enabled = has_selected_chat && composer_send_allowed(state);
    let has_text = !composer_text(&widgets.composer_text_view).trim().is_empty();

    widgets.composer_text_view.set_sensitive(enabled);
    widgets
        .composer_send_button
        .set_sensitive(enabled && has_text);
    widgets.composer_attach_button.set_sensitive(enabled);

    widgets.composer_text_view.set_visible(has_selected_chat);
    widgets.composer_send_button.set_visible(has_selected_chat);
    widgets
        .composer_attach_button
        .set_visible(has_selected_chat);

    if state.composer_error.is_empty() || !has_selected_chat {
        widgets.composer_error_label.set_visible(false);
        widgets.composer_error_label.set_text("");
    } else {
        widgets.composer_error_label.set_visible(true);
        widgets.composer_error_label.set_text(&state.composer_error);
    }

    resize_composer(widgets);
}

pub fn resize_composer(widgets: &Widgets) {
    let width = widgets.composer_scroller.allocated_width();
    let for_size = if width > 0 { width } else { -1 };

    let (_, natural_height, _, _) = widgets
        .composer_text_view
        .measure(gtk::Orientation::Vertical, for_size);

    let content_height = natural_height + COMPOSER_HEIGHT_SLACK;
    let target_height = content_height.clamp(1, COMPOSER_MAX_HEIGHT);
    let needs_scroll = content_height > COMPOSER_MAX_HEIGHT;

    widgets
        .composer_scroller
        .set_min_content_height(target_height);

    let scrollbar_policy = if needs_scroll {
        gtk::PolicyType::Always
    } else {
        gtk::PolicyType::Never
    };

    if widgets.composer_scroller.vscrollbar_policy() != scrollbar_policy {
        widgets
            .composer_scroller
            .set_vscrollbar_policy(scrollbar_policy);
    }

    if !needs_scroll {
        widgets.composer_scroller.vadjustment().set_value(0.0);
    } else {
        scroll_composer_to_bottom(widgets);
    }
}

pub fn scroll_composer_to_bottom(widgets: &Widgets) {
    let buffer = widgets.composer_text_view.buffer();
    let mut end_iter = buffer.end_iter();

    widgets
        .composer_text_view
        .scroll_to_iter(&mut end_iter, 0.0, true, 0.0, 1.0);

    let adjustment = widgets.composer_scroller.vadjustment();
    let bottom = (adjustment.upper() - adjustment.page_size()).max(adjustment.lower());

    adjustment.set_value(bottom);
}

pub fn scroll_composer_to_bottom_if_needed(widgets: &Widgets) {
    if widgets.composer_scroller.vscrollbar_policy() != gtk::PolicyType::Never {
        scroll_composer_to_bottom(widgets);
    }
}

pub fn composer_text(text_view: &gtk::TextView) -> String {
    let buffer = text_view.buffer();

    buffer
        .text(&buffer.start_iter(), &buffer.end_iter(), false)
        .to_string()
}

pub fn clear_composer(text_view: &gtk::TextView) {
    text_view.buffer().set_text("");
}
