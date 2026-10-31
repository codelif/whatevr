//! the lab: what-chat's shell over a fake chat and chat list, for the
//! scripted test and the window in main.rs.

pub mod fake;

use what_chat::shell::Shell;
use what_text::TextSystem;

use crate::fake::{Chat, Chats};

pub use what_chat::shell::ShellFrame as LabFrame;

pub type Lab = Shell<Chat, Chats>;

pub fn lab(text: TextSystem, waker: impl Fn() + Clone + Send + 'static) -> Lab {
    let mut lab = Shell::new(
        text,
        waker,
        Chat::new(0x5eed, 105_000, 5_000),
        Chats::new(0xc4a7, 5_000),
    );
    lab.selected = Some(1);
    lab
}

/// a fake message arrives
pub fn incoming(lab: &mut Lab) {
    lab.chat.incoming();
    lab.appended();
}
