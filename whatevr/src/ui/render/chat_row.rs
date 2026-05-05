use gtk::{pango, prelude::*};

use crate::proto;
use crate::ui::render::avatar::set_avatar_image;
use crate::util::{
    text::{chat_preview, display_chat_name, unread_count_label},
    time::format_chat_timestamp,
};

pub fn build_chat_row(chat: &proto::Chat) -> gtk::ListBoxRow {
    let row_box = build_chat_row_content(chat);

    let row = gtk::ListBoxRow::builder()
        .activatable(true)
        .selectable(true)
        .build();

    row.set_child(Some(&row_box));
    row
}

fn build_chat_row_content(chat: &proto::Chat) -> gtk::Box {
    let avatar = adw::Avatar::new(36, Some(display_chat_name(chat)), true);
    set_avatar_image(&avatar, &chat.avatar_local_path);

    let title = gtk::Label::builder()
        .label(display_chat_name(chat))
        .xalign(0.0)
        .ellipsize(pango::EllipsizeMode::End)
        .css_classes(["heading"])
        .build();

    let preview = gtk::Label::builder()
        .label(chat_preview(chat))
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
        .label(format_chat_timestamp(chat.last_message_time_unix))
        .xalign(1.0)
        .css_classes(["dim-label", "caption"])
        .build();

    let unread_label = gtk::Label::builder()
        .label(if chat.unread_count > 0 {
            unread_count_label(chat.unread_count)
        } else {
            String::new()
        })
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

    row_box
}
