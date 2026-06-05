use gtk::prelude::*;

use crate::proto::HistorySyncType;
use crate::ui::{state::HistorySyncStatus, widgets::Widgets};

pub fn render_history_sync_strip(widgets: &Widgets, status: Option<&HistorySyncStatus>) {
    let Some(status) = status else {
        widgets.history_sync_strip.set_visible(false);
        return;
    };

    widgets.history_sync_strip.set_visible(true);
    widgets
        .history_sync_label
        .set_text(&format_history_sync_label(status));
    widgets
        .history_sync_bar
        .set_fraction(progress_fraction(status));
}

fn format_history_sync_label(status: &HistorySyncStatus) -> String {
    let prefix = sync_type_prefix(status.sync_type);
    let detail = format_history_sync_detail(status);
    let headline = if status.progress_percent == 0 {
        prefix.to_string()
    } else {
        let percent = status.progress_percent.min(100);
        format!("{prefix} {percent}%")
    };

    if detail.is_empty() {
        headline
    } else {
        format!("{headline}\n{detail}")
    }
}

fn format_history_sync_detail(status: &HistorySyncStatus) -> String {
    let mut parts = Vec::new();
    if status.conversations_in_chunk > 0 {
        parts.push(format!("{} chats", status.conversations_in_chunk));
    }
    if status.messages_in_chunk > 0 {
        parts.push(format!("{} messages", status.messages_in_chunk));
    }
    parts.join(" · ")
}

fn progress_fraction(status: &HistorySyncStatus) -> f64 {
    if status.progress_percent == 0 {
        // Indeterminate-ish: keep the bar mostly empty until the daemon
        // starts reporting progress, instead of bouncing visibly.
        0.05
    } else {
        (status.progress_percent.min(100) as f64) / 100.0
    }
}

fn sync_type_prefix(sync_type: HistorySyncType) -> &'static str {
    match sync_type {
        HistorySyncType::Recent | HistorySyncType::OnDemand => "Catching up on recent messages…",
        HistorySyncType::PushName | HistorySyncType::NonBlockingData => "Syncing contact info…",
        _ => "Syncing chat history…",
    }
}
