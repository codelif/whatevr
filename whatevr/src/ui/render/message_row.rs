use gtk::{gdk, pango, prelude::*};

use crate::proto;
use crate::ui::render::avatar::{cached_texture, schedule_async_image_load};
use crate::util::time::format_message_meta;

#[derive(Clone)]
pub struct RenderedMessage {
    pub id: String,
    pub status: i32,
    pub row: gtk::Box,
    pub meta_label: gtk::Label,
}

pub fn build_message_row(message: &proto::Message) -> (gtk::Box, gtk::Label) {
    let outgoing = message.direction == proto::MessageDirection::Outgoing as i32;

    let bubble = gtk::Box::new(gtk::Orientation::Vertical, 4);
    bubble.add_css_class("message-bubble");

    if outgoing {
        bubble.add_css_class("outgoing");
    } else {
        bubble.add_css_class("incoming");
    }

    let has_image =
        !message.media_local_path.is_empty() && message.media_mime_type.starts_with("image/");

    if has_image {
        const MAX_IMG_W: i32 = 280;
        const MAX_IMG_H: i32 = 360;

        let raw_dims = if message.media_width > 0 && message.media_height > 0 {
            Some((message.media_width, message.media_height))
        } else {
            gdk::gdk_pixbuf::Pixbuf::file_info(&message.media_local_path).map(|(_, w, h)| (w, h))
        };

        if let Some((raw_w, raw_h)) = raw_dims {
            let scale_w = MAX_IMG_W as f64 / raw_w as f64;
            let tentative_h = (raw_h as f64 * scale_w).round() as i32;

            let (display_w, display_h) = if tentative_h <= MAX_IMG_H {
                (MAX_IMG_W, tentative_h.max(1))
            } else {
                let scale_h = MAX_IMG_H as f64 / raw_h as f64;
                (((raw_w as f64 * scale_h).round() as i32).max(1), MAX_IMG_H)
            };

            let picture = gtk::Picture::new();
            picture.set_size_request(display_w, display_h);
            picture.set_can_shrink(false);
            picture.set_content_fit(gtk::ContentFit::Fill);
            picture.add_css_class("image-placeholder");

            set_accessible_label(&picture, "Attached image preview");

            if let Some(texture) = cached_texture(&message.media_local_path) {
                picture.set_paintable(Some(&texture));
            } else {
                schedule_async_image_load(
                    picture.clone(),
                    message.media_local_path.clone(),
                    display_w,
                    display_h,
                );
            }

            bubble.append(&picture);
        }
    }

    if !message.text.is_empty() {
        let message_label = gtk::Label::new(Some(message.text.as_str()));
        message_label.set_xalign(0.0);
        message_label.set_wrap(true);
        message_label.set_wrap_mode(pango::WrapMode::WordChar);
        message_label.set_max_width_chars(62);
        message_label.set_selectable(true);
        message_label.add_css_class("message-text");
        bubble.append(&message_label);
    } else if !has_image {
        let message_label = gtk::Label::new(Some(""));
        message_label.add_css_class("message-text");
        bubble.append(&message_label);
    }

    let meta = gtk::Label::builder()
        .label(format_message_meta(message))
        .xalign(if outgoing { 1.0 } else { 0.0 })
        .css_classes(["caption", "dim-label"])
        .build();

    let column = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(4)
        .build();

    column.append(&bubble);
    column.append(&meta);

    let spacer = gtk::Box::new(gtk::Orientation::Horizontal, 0);
    spacer.set_hexpand(true);

    let row = gtk::Box::builder()
        .orientation(gtk::Orientation::Horizontal)
        .hexpand(true)
        .build();

    if outgoing {
        row.append(&spacer);
        row.append(&column);
    } else {
        row.append(&column);
        row.append(&spacer);
    }

    (row, meta)
}

fn set_accessible_label(widget: &impl IsA<gtk::Accessible>, label: &str) {
    widget.update_property(&[gtk::accessible::Property::Label(label)]);
}
