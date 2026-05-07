use std::{
    cell::{Cell, RefCell},
    collections::HashMap,
    rc::Rc,
};

use crate::ui::render::{chat_row::RenderedChatRow, message_row::RenderedMessage};

#[derive(Clone)]
pub struct Widgets {
    pub root_stack: gtk::Stack,

    pub loading_page: adw::StatusPage,
    pub loading_spinner: gtk::Spinner,
    pub loading_retry_button: gtk::Button,

    pub login_status_label: gtk::Label,
    pub qr_picture: gtk::Picture,
    pub qr_error_label: gtk::Label,
    pub qr_expiry_label: gtk::Label,

    pub banner: adw::Banner,
    pub split_view: adw::NavigationSplitView,

    pub sidebar_stack: gtk::Stack,
    pub sidebar_loading_page: adw::StatusPage,
    pub sidebar_empty_page: adw::StatusPage,
    pub chat_list: gtk::ListBox,

    pub history_sync_strip: gtk::Box,
    pub history_sync_label: gtk::Label,
    pub history_sync_bar: gtk::ProgressBar,

    pub conversation_stack: gtk::Stack,
    pub conversation_content_stack: gtk::Stack,
    pub conversation_loading_page: adw::StatusPage,
    pub conversation_header: gtk::Box,
    pub conversation_avatar: adw::Avatar,
    pub conversation_title: gtk::Label,

    pub message_scroller: gtk::ScrolledWindow,
    pub message_box: gtk::Box,
    pub older_messages_loading: gtk::Box,
    pub scroll_to_bottom_button: gtk::Button,
    pub scroll_to_bottom_icon: gtk::Image,
    pub scroll_to_bottom_badge: gtk::Label,
    pub messages_below_count: Cell<u32>,
    pub message_prepend_in_progress: Rc<Cell<bool>>,
    pub message_prepend_generation: Rc<Cell<u64>>,

    pub composer_scroller: gtk::ScrolledWindow,
    pub composer_text_view: gtk::TextView,
    pub composer_error_label: gtk::Label,
    pub composer_send_button: gtk::Button,
    pub composer_attach_button: gtk::Button,

    pub back_button: gtk::Button,
    pub logout_button: gtk::Button,

    pub syncing_chat_selection: Cell<bool>,
    pub rendered_chat_rows: RefCell<HashMap<String, RenderedChatRow>>,
    pub rendered_chat_order: RefCell<Vec<String>>,
    pub message_scroll_generation: Rc<Cell<u64>>,
    pub rendered_chat_id: RefCell<Option<String>>,
    pub rendered_messages: RefCell<Vec<RenderedMessage>>,
}
