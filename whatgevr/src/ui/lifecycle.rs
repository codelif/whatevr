use std::{cell::Cell, rc::Rc};

use adw::prelude::*;
use gtk::glib;

use crate::ui::{
    context::{UiSender, send_ui},
    message::UiMessage,
    widgets::Widgets,
};

pub fn present_app_window(widgets: &Widgets) {
    if let Some(window) = widgets.root_stack.root().and_downcast::<gtk::Window>() {
        window.present();
    }
}

pub fn schedule_bootstrap_after_first_frame(window: &adw::ApplicationWindow, sender: UiSender) {
    let frame_count = Rc::new(Cell::new(0u8));

    window.add_tick_callback(move |_, _| {
        let next = frame_count.get().saturating_add(1);
        frame_count.set(next);

        if next < 2 {
            return glib::ControlFlow::Continue;
        }

        send_ui(&sender, UiMessage::BootstrapAfterFirstFrame);

        glib::ControlFlow::Break
    });
}
