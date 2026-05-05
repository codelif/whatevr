use gtk::prelude::*;

use crate::config::{SIDEBAR_EMPTY_PAGE, SIDEBAR_LIST_PAGE, SIDEBAR_LOADING_PAGE};
use crate::proto::DaemonState;
use crate::ui::{render::chat_row::build_chat_row, state::UiState, widgets::Widgets};

pub fn render_chat_list(widgets: &Widgets, state: &UiState) {
    widgets.syncing_chat_selection.set(true);
    clear_list_box(&widgets.chat_list);

    if !state.chats_loaded {
        widgets.sidebar_loading_page.set_title("Loading chats");
        widgets
            .sidebar_loading_page
            .set_description(Some("Reading your local conversation list."));
        widgets
            .sidebar_stack
            .set_visible_child_name(SIDEBAR_LOADING_PAGE);
        widgets.chat_list.unselect_all();
        widgets.syncing_chat_selection.set(false);
        return;
    }

    if state.chats.is_empty() {
        widgets
            .sidebar_empty_page
            .set_description(Some(match state.daemon_state {
                DaemonState::Connecting | DaemonState::Reconnecting => {
                    "Chats will appear here as the local store catches up."
                }
                _ => "Chats will appear here once text history is stored locally.",
            }));

        widgets
            .sidebar_stack
            .set_visible_child_name(SIDEBAR_EMPTY_PAGE);
        widgets.chat_list.unselect_all();
        widgets.syncing_chat_selection.set(false);
        return;
    }

    let effective_selected_chat_id = state
        .pending_chat_id
        .as_deref()
        .or(state.selected_chat_id.as_deref());

    let mut selected_index = None;

    for (index, chat) in state.chats.iter().enumerate() {
        let row = build_chat_row(chat);
        widgets.chat_list.append(&row);

        if effective_selected_chat_id == Some(chat.id.as_str()) {
            selected_index = Some(index);
        }
    }

    widgets
        .sidebar_stack
        .set_visible_child_name(SIDEBAR_LIST_PAGE);

    if let Some(index) = selected_index {
        if let Some(row) = widgets.chat_list.row_at_index(index as i32) {
            widgets.chat_list.select_row(Some(&row));
        }
    } else {
        widgets.chat_list.unselect_all();
    }

    widgets.syncing_chat_selection.set(false);
}

fn clear_list_box(list_box: &gtk::ListBox) {
    while let Some(child) = list_box.first_child() {
        list_box.remove(&child);
    }
}
