use std::rc::Rc;

use crate::ui::message::UiMessage;
use crate::ui::widgets::Widgets;

pub type UiSender = async_channel::Sender<UiMessage>;

#[derive(Clone)]
pub struct AppContext {
    pub widgets: Rc<Widgets>,
    pub sender: UiSender,
}

pub fn send_ui(sender: &UiSender, message: UiMessage) {
    let _ = sender.try_send(message);
}
