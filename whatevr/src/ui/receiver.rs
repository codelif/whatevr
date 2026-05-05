use std::{cell::RefCell, rc::Rc};

use gtk::glib;

use crate::config::UI_MESSAGE_BATCH_SIZE;
use crate::ui::{context::UiSender, message::UiMessage, state::UiState, widgets::Widgets};

pub fn attach<F>(
    receiver: async_channel::Receiver<UiMessage>,
    widgets: Rc<Widgets>,
    state: Rc<RefCell<UiState>>,
    sender: UiSender,
    handler: F,
) where
    F: Fn(UiMessage, &Rc<Widgets>, &Rc<RefCell<UiState>>, &UiSender) + 'static,
{
    glib::MainContext::default().spawn_local(async move {
        while let Ok(message) = receiver.recv().await {
            handler(message, &widgets, &state, &sender);

            for _ in 1..UI_MESSAGE_BATCH_SIZE {
                match receiver.try_recv() {
                    Ok(message) => handler(message, &widgets, &state, &sender),
                    Err(async_channel::TryRecvError::Empty) => break,
                    Err(async_channel::TryRecvError::Closed) => return,
                }
            }

            yield_to_gtk().await;
        }
    });
}

async fn yield_to_gtk() {
    let (tx, rx) = async_channel::bounded(1);

    glib::idle_add_local_once(move || {
        let _ = tx.try_send(());
    });

    let _ = rx.recv().await;
}
