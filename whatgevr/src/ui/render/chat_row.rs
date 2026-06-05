use gtk::{pango, prelude::*};

use crate::proto;
use crate::ui::render::avatar::set_avatar_image;
use crate::util::{
    text::{chat_preview, display_chat_name, unread_count_label},
    time::format_chat_timestamp,
};

#[derive(Clone)]
pub struct RenderedChatRow {
    pub id: String,
    pub row: gtk::ListBoxRow,
    avatar: adw::Avatar,
    title: gtk::Label,
    preview: gtk::Label,
    timestamp: gtk::Label,
    unread_label: gtk::Label,
    name: String,
    preview_text: String,
    timestamp_text: String,
    unread_count: i32,
    avatar_local_path: String,
}

pub fn build_chat_row(chat: &proto::Chat) -> RenderedChatRow {
    let name = display_chat_name(chat).to_string();
    let preview_text = chat_preview(chat);
    let timestamp_text = format_chat_timestamp(chat.last_message_time_unix);

    let avatar = adw::Avatar::new(36, Some(&name), true);
    set_avatar_image(&avatar, &chat.avatar_local_path);

    let title = gtk::Label::builder()
        .label(&name)
        .xalign(0.0)
        .ellipsize(pango::EllipsizeMode::End)
        .css_classes(["heading"])
        .build();

    let preview = gtk::Label::builder()
        .label(&preview_text)
        .xalign(0.0)
        .ellipsize(pango::EllipsizeMode::End)
        .css_classes(["dim-label"])
        .build();

    preview.set_single_line_mode(true);

    let text_box = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(4)
        .hexpand(true)
        .build();

    text_box.append(&title);
    text_box.append(&preview);

    let timestamp = gtk::Label::builder()
        .label(&timestamp_text)
        .xalign(1.0)
        .css_classes(["dim-label", "caption"])
        .build();

    let unread_label = gtk::Label::builder()
        .label(unread_text(chat.unread_count))
        .visible(chat.unread_count > 0)
        .css_classes(["unread-badge"])
        .halign(gtk::Align::End)
        .build();

    let trailing = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(8)
        .valign(gtk::Align::Center)
        .build();

    trailing.append(&timestamp);
    trailing.append(&unread_label);

    let row_box = gtk::Box::builder()
        .orientation(gtk::Orientation::Horizontal)
        .spacing(12)
        .margin_top(10)
        .margin_bottom(10)
        .margin_start(12)
        .margin_end(12)
        .build();

    row_box.append(&avatar);
    row_box.append(&text_box);
    row_box.append(&trailing);

    let row = gtk::ListBoxRow::builder()
        .activatable(true)
        .selectable(true)
        .build();

    row.set_child(Some(&row_box));

    RenderedChatRow {
        id: chat.id.clone(),
        row,
        avatar,
        title,
        preview,
        timestamp,
        unread_label,
        name,
        preview_text,
        timestamp_text,
        unread_count: chat.unread_count,
        avatar_local_path: chat.avatar_local_path.clone(),
    }
}

pub fn update_chat_row(rendered: &mut RenderedChatRow, chat: &proto::Chat) {
    let name = display_chat_name(chat).to_string();
    if rendered.name != name {
        rendered.avatar.set_text(Some(&name));
        rendered.title.set_text(&name);
        rendered.name = name;
    }

    if rendered.avatar_local_path != chat.avatar_local_path {
        set_avatar_image(&rendered.avatar, &chat.avatar_local_path);
        rendered.avatar_local_path = chat.avatar_local_path.clone();
    }

    let preview_text = chat_preview(chat);
    if rendered.preview_text != preview_text {
        rendered.preview.set_text(&preview_text);
        rendered.preview_text = preview_text;
    }

    let timestamp_text = format_chat_timestamp(chat.last_message_time_unix);
    if rendered.timestamp_text != timestamp_text {
        rendered.timestamp.set_text(&timestamp_text);
        rendered.timestamp_text = timestamp_text;
    }

    if rendered.unread_count != chat.unread_count {
        rendered
            .unread_label
            .set_text(&unread_text(chat.unread_count));
        rendered.unread_label.set_visible(chat.unread_count > 0);
        rendered.unread_count = chat.unread_count;
    }
}

fn unread_text(unread_count: i32) -> String {
    if unread_count > 0 {
        unread_count_label(unread_count)
    } else {
        String::new()
    }
}
