use gtk::prelude::*;

use crate::config::{SIDEBAR_EMPTY_PAGE, SIDEBAR_LIST_PAGE, SIDEBAR_LOADING_PAGE};
use crate::proto::DaemonState;
use crate::ui::{
    render::chat_row::{build_chat_row, update_chat_row},
    state::UiState,
    widgets::Widgets,
};

pub fn render_chat_list(widgets: &Widgets, state: &UiState) {
    widgets.syncing_chat_selection.set(true);

    if !state.chats_loaded {
        widgets.sidebar_loading_page.set_title("Loading chats");
        widgets
            .sidebar_loading_page
            .set_description(Some("Reading your local conversation list."));
        widgets
            .sidebar_stack
            .set_visible_child_name(SIDEBAR_LOADING_PAGE);
        widgets.chat_list.unselect_all();
        clear_rendered_chat_rows(widgets);
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
        clear_rendered_chat_rows(widgets);
        widgets.syncing_chat_selection.set(false);
        return;
    }

    let effective_selected_chat_id = state
        .pending_chat_id
        .as_deref()
        .or(state.selected_chat_id.as_deref());

    let desired_order: Vec<String> = state.chats.iter().map(|chat| chat.id.clone()).collect();

    patch_chat_rows(widgets, &state.chats, &desired_order);

    widgets
        .sidebar_stack
        .set_visible_child_name(SIDEBAR_LIST_PAGE);

    select_chat_row(widgets, effective_selected_chat_id);

    widgets.syncing_chat_selection.set(false);
}

fn patch_chat_rows(widgets: &Widgets, chats: &[crate::proto::Chat], desired_order: &[String]) {
    let desired: std::collections::HashSet<&str> =
        desired_order.iter().map(String::as_str).collect();

    {
        let mut rows = widgets.rendered_chat_rows.borrow_mut();
        rows.retain(|chat_id, rendered| {
            let keep = desired.contains(chat_id.as_str());
            if !keep {
                remove_chat_row_if_child(widgets, &rendered.row);
            }
            keep
        });

        for chat in chats {
            if let Some(rendered) = rows.get_mut(&chat.id) {
                update_chat_row(rendered, chat);
            } else {
                let rendered = build_chat_row(chat);
                rows.insert(chat.id.clone(), rendered);
            }
        }
    }

    if widgets.rendered_chat_order.borrow().as_slice() != desired_order {
        let rows = widgets.rendered_chat_rows.borrow();
        for (index, chat_id) in desired_order.iter().enumerate() {
            let Some(rendered) = rows.get(chat_id) else {
                continue;
            };
            if rendered.row.index() != index as i32 {
                remove_chat_row_if_child(widgets, &rendered.row);
                widgets.chat_list.insert(&rendered.row, index as i32);
            }
        }
        *widgets.rendered_chat_order.borrow_mut() = desired_order.to_vec();
    }
}

fn select_chat_row(widgets: &Widgets, selected_chat_id: Option<&str>) {
    let Some(selected_chat_id) = selected_chat_id else {
        widgets.chat_list.unselect_all();
        return;
    };

    let rows = widgets.rendered_chat_rows.borrow();
    if let Some(rendered) = rows.get(selected_chat_id) {
        widgets.chat_list.select_row(Some(&rendered.row));
    } else {
        widgets.chat_list.unselect_all();
    }
}

fn clear_rendered_chat_rows(widgets: &Widgets) {
    let mut rows = widgets.rendered_chat_rows.borrow_mut();
    for (_, rendered) in rows.drain() {
        remove_chat_row_if_child(widgets, &rendered.row);
    }
    widgets.rendered_chat_order.borrow_mut().clear();
}

fn remove_chat_row_if_child(widgets: &Widgets, row: &gtk::ListBoxRow) {
    if row.parent().as_ref() == Some(widgets.chat_list.upcast_ref()) {
        widgets.chat_list.remove(row);
    }
}
