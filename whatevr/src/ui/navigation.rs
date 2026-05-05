use gtk::prelude::*;

use crate::proto::DaemonState;
use crate::ui::{state::UiState, widgets::Widgets};

pub fn update_navigation_state(widgets: &Widgets, state: &UiState) {
    let show_content = if widgets.split_view.is_collapsed() {
        state.selected_chat_id.is_some() && !matches!(state.daemon_state, DaemonState::NeedLogin)
    } else {
        true
    };

    widgets.split_view.set_show_content(show_content);
    widgets
        .back_button
        .set_visible(widgets.split_view.is_collapsed() && show_content);
}
