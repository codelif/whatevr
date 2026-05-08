use std::{
    cell::{Cell, RefCell},
    collections::HashMap,
    rc::Rc,
};

use gtk::{gio, glib};

use crate::ui::render::chat_row::RenderedChatRow;

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
    pub message_list_view: gtk::ListView,
    pub message_store: gio::ListStore,
    pub older_messages_loading: gtk::Box,
    pub scroll_to_bottom_button: gtk::Button,
    pub scroll_to_bottom_icon: gtk::Image,
    pub scroll_to_bottom_badge: gtk::Label,
    pub messages_below_count: Cell<u32>,

    // Scroll/prepend state machine. See `signals.rs` and `controller.rs`
    // for the algorithm. Mirrors the design described in
    // https://docs.gtk.org/gtk4/class.ListView.html plus a burst-lock so
    // a single fast fling cannot prepend more than one page at a time.
    pub scroll_state: Rc<ScrollState>,

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
}

#[derive(Default)]
pub struct ScrollState {
    // True while a prepend operation is in flight (insert + scroll restore).
    pub loading: Cell<bool>,
    // True when a prepend is currently allowed.
    pub prepend_armed: Cell<bool>,
    // True after a successful prepend, until the current scroll burst ends.
    pub block_rearm_until_scroll_stops: Cell<bool>,
    // True while we programmatically set vadj.value, so the value-changed
    // handler does not interpret it as user scrolling.
    pub suppress_value_handler: Cell<bool>,
    // Last observed adjustment value. NaN means "not yet initialized".
    pub last_value: Cell<f64>,
    // Restart-on-scroll timer used to detect the end of a scroll burst.
    pub scroll_burst_source_id: RefCell<Option<glib::SourceId>>,
    // True while an idle probe to maybe trigger a prepend from the top
    // is already queued.
    pub top_scroll_probe_scheduled: Cell<bool>,
    // Snapshot of vadj.upper / vadj.value taken before a prepend, used to
    // restore the visible position after GTK recalculates the new upper.
    pub restore_state: RefCell<Option<RestoreState>>,
    // Handler id for the temporary notify::upper subscription installed
    // during a prepend. Cleared when the restore lands.
    pub restore_upper_handler: RefCell<Option<glib::SignalHandlerId>>,
}

pub struct RestoreState {
    pub old_upper: f64,
    pub old_value: f64,
    pub tries: u32,
}

impl ScrollState {
    pub fn new() -> Self {
        let state = Self::default();
        state.prepend_armed.set(true);
        state.last_value.set(f64::NAN);
        state
    }
}
