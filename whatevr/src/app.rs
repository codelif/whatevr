use std::{cell::RefCell, rc::Rc};

use adw::prelude::*;
use gtk::{gio, glib};

use crate::{
    config::APP_ID,
    ui::{
        context::{AppContext, send_ui},
        lifecycle::present_app_window,
        message::UiMessage,
        style, window,
    },
    util::uri::chat_id_from_uri,
};

pub fn run() -> glib::ExitCode {
    let app = adw::Application::builder()
        .application_id(APP_ID)
        .flags(gio::ApplicationFlags::HANDLES_OPEN)
        .build();

    app.connect_startup(|_| style::install());

    let app_context: Rc<RefCell<Option<AppContext>>> = Rc::new(RefCell::new(None));

    {
        let app_context = app_context.clone();

        app.connect_activate(move |app| {
            if let Some(context) = app_context.borrow().as_ref() {
                present_app_window(&context.widgets);
                return;
            }

            let context = window::build(app);
            *app_context.borrow_mut() = Some(context);
        });
    }

    {
        let app_context = app_context.clone();

        app.connect_open(move |app, files, _hint| {
            let context = if let Some(context) = app_context.borrow().as_ref() {
                context.clone()
            } else {
                let context = window::build(app);
                *app_context.borrow_mut() = Some(context.clone());
                context
            };

            present_app_window(&context.widgets);

            for file in files {
                let uri = file.uri();

                if let Some(chat_id) = chat_id_from_uri(uri.as_str()) {
                    send_ui(&context.sender, UiMessage::OpenChat { chat_id });
                }
            }
        });
    }

    app.run()
}
