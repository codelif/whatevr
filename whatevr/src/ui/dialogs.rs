use std::rc::Rc;

use adw::prelude::*;
use gtk::gio;

use crate::ui::{commands::request_logout, context::UiSender, widgets::Widgets};

pub fn show_logout_confirmation(widgets: &Rc<Widgets>, sender: &UiSender) {
    let Some(window) = widgets.root_stack.root().and_downcast::<gtk::Window>() else {
        request_logout(sender.clone());
        return;
    };

    let dialog = gtk::AlertDialog::builder()
        .message("Log out?")
        .detail("This will delete the WhatsApp session, local chats, messages, and cached media from this device.")
        .modal(true)
        .buttons(["Cancel", "Log out"])
        .cancel_button(0)
        .default_button(0)
        .build();

    let sender = sender.clone();

    dialog.choose(Some(&window), None::<&gio::Cancellable>, move |response| {
        if response == Ok(1) {
            request_logout(sender.clone());
        }
    });
}
