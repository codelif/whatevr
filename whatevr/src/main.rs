use std::{
    cell::{Cell, RefCell},
    collections::{HashMap, VecDeque},
    path::PathBuf,
    rc::Rc,
    sync::mpsc,
    thread,
    time::Duration,
};

use adw::prelude::*;
use chrono::{Local, TimeZone};
use gtk::{gdk, gio, glib, pango};
use http::Uri;
use hyper_util::rt::TokioIo;
use qrcode::{QrCode, types::Color};
use tokio::net::UnixStream;
use tonic::transport::{Channel, Endpoint};
use tower::service_fn;

mod proto {
    tonic::include_proto!("whatevr.v1");
}

use proto::chat_service_client::ChatServiceClient;
use proto::daemon_event;
use proto::daemon_service_client::DaemonServiceClient;
use proto::frontend_service_client::FrontendServiceClient;
use proto::login_event;
use proto::login_service_client::LoginServiceClient;
use proto::send_service_client::SendServiceClient;
use proto::{
    DaemonState, GetMessagesRequest, GetStatusRequest, ListChatsRequest, LogoutRequest,
    MarkChatReadRequest, ReconnectRequest, SendMediaRequest, SetChatPresenceRequest,
    SubscribeEventsRequest, SubscribeLoginEventsRequest,
};

const APP_ID: &str = "in.codelif.Whatevr";
const ROOT_LOADING_PAGE: &str = "loading";
const ROOT_LOGIN_PAGE: &str = "login";
const ROOT_MAIN_PAGE: &str = "main";
const SIDEBAR_LOADING_PAGE: &str = "loading";
const SIDEBAR_EMPTY_PAGE: &str = "empty";
const SIDEBAR_LIST_PAGE: &str = "list";
const CONVERSATION_PLACEHOLDER_PAGE: &str = "placeholder";
const CONVERSATION_LOADING_PAGE: &str = "loading";
const CONVERSATION_EMPTY_PAGE: &str = "empty";
const CONVERSATION_MESSAGES_PAGE: &str = "messages";
const COMPOSER_MAX_HEIGHT: i32 = 144;
const COMPOSER_HEIGHT_SLACK: i32 = 2;

#[derive(Clone)]
enum UiMessage {
    StatusLoaded(proto::GetStatusResponse),
    StatusFailed(String),
    DaemonState {
        state: DaemonState,
        detail: String,
        can_reconnect: bool,
        retry_attempt: i32,
        next_retry_unix: i64,
    },
    QrCode {
        code: String,
        expires_at: i64,
    },
    ChatsLoaded(Vec<proto::Chat>),
    ChatsFailed(String),
    MessagesLoaded {
        chat_id: String,
        generation: u64,
        messages: Vec<proto::Message>,
    },
    MessagesFailed {
        chat_id: String,
        generation: u64,
        error: String,
    },
    ShowConversationLoading {
        chat_id: String,
        generation: u64,
    },
    SendSucceeded {
        chat_id: String,
    },
    SendFailed {
        chat_id: String,
        error: String,
    },
    MediaSendSucceeded,
    MediaSendFailed {
        chat_id: String,
        error: String,
    },
    ChatPresence {
        chat_id: String,
        is_composing: bool,
    },
    NewMessage {
        message: proto::Message,
    },
    MessageUpdated {
        message: proto::Message,
    },
    ChatUpdated {
        chat: proto::Chat,
        previous_chat_id: Option<String>,
    },
    LogoutSucceeded,
    LogoutFailed(String),
    Notice(String),
    DaemonConnectionLost,
    DaemonConnectionRestored,
    ReconnectSucceeded,
    ReconnectFailed(String),
}

struct UiState {
    daemon_state: DaemonState,
    daemon_detail: String,
    can_reconnect: bool,
    retry_attempt: i32,
    next_retry_unix: i64,
    daemon_disconnected: bool,
    reconnect_in_flight: bool,
    chats: Vec<proto::Chat>,
    chats_loaded: bool,
    initial_chat_request_started: bool,
    selected_chat_id: Option<String>,
    pending_chat_id: Option<String>,
    loading_chat_id: Option<String>,
    message_request_generation: u64,
    current_messages_chat_id: Option<String>,
    current_messages: Vec<proto::Message>,
    composer_error: String,
    send_in_flight: bool,
    window_focused: bool,
    composing_peers: HashMap<String, bool>,
    last_sent_composing: bool,
}

impl Default for UiState {
    fn default() -> Self {
        Self {
            daemon_state: DaemonState::Starting,
            daemon_detail: String::new(),
            can_reconnect: false,
            retry_attempt: 0,
            next_retry_unix: 0,
            daemon_disconnected: false,
            reconnect_in_flight: false,
            chats: Vec::new(),
            chats_loaded: false,
            initial_chat_request_started: false,
            selected_chat_id: None,
            pending_chat_id: None,
            loading_chat_id: None,
            message_request_generation: 0,
            current_messages_chat_id: None,
            current_messages: Vec::new(),
            composer_error: String::new(),
            send_in_flight: false,
            window_focused: false,
            composing_peers: HashMap::new(),
            last_sent_composing: false,
        }
    }
}

#[derive(Clone)]
struct RenderedMessage {
    id: String,
    status: i32,
    row: gtk::Box,
    meta_label: gtk::Label,
}

#[derive(Clone)]
struct Widgets {
    root_stack: gtk::Stack,
    loading_page: adw::StatusPage,
    loading_spinner: gtk::Spinner,
    loading_retry_button: gtk::Button,
    login_status_label: gtk::Label,
    qr_picture: gtk::Picture,
    qr_error_label: gtk::Label,
    qr_expiry_label: gtk::Label,
    banner: adw::Banner,
    split_view: adw::NavigationSplitView,
    sidebar_stack: gtk::Stack,
    sidebar_loading_page: adw::StatusPage,
    sidebar_empty_page: adw::StatusPage,
    chat_list: gtk::ListBox,
    conversation_stack: gtk::Stack,
    conversation_content_stack: gtk::Stack,
    conversation_loading_page: adw::StatusPage,
    conversation_header: gtk::Box,
    conversation_avatar: adw::Avatar,
    conversation_title: gtk::Label,
    message_scroller: gtk::ScrolledWindow,
    message_box: gtk::Box,
    composer_scroller: gtk::ScrolledWindow,
    composer_text_view: gtk::TextView,
    composer_error_label: gtk::Label,
    composer_send_button: gtk::Button,
    composer_attach_button: gtk::Button,
    back_button: gtk::Button,
    logout_button: gtk::Button,
    syncing_chat_selection: Cell<bool>,
    message_scroll_generation: Rc<Cell<u64>>,
    rendered_chat_id: RefCell<Option<String>>,
    rendered_messages: RefCell<Vec<RenderedMessage>>,
}

fn main() -> glib::ExitCode {
    adw::init().expect("failed to initialize libadwaita");
    install_css();

    let app = adw::Application::builder().application_id(APP_ID).build();
    app.connect_activate(build_ui);
    app.run()
}

fn build_ui(app: &adw::Application) {
    let refresh_button = gtk::Button::builder()
        .icon_name("view-refresh-symbolic")
        .tooltip_text("Refresh chats and connection state")
        .build();
    let logout_button = gtk::Button::builder()
        .icon_name("system-log-out-symbolic")
        .tooltip_text("Log out and delete local session data")
        .build();
    let back_button = gtk::Button::builder()
        .icon_name("go-previous-symbolic")
        .tooltip_text("Back to chats")
        .visible(false)
        .build();

    let loading_page = adw::StatusPage::builder()
        .icon_name("network-transmit-receive-symbolic")
        .title("Starting whatevrd")
        .description("Preparing the local session and syncing pipeline.")
        .build();
    let loading_spinner = gtk::Spinner::builder()
        .spinning(true)
        .margin_top(18)
        .margin_bottom(18)
        .build();
    let loading_retry_button = gtk::Button::builder()
        .label("Retry")
        .css_classes(["suggested-action"])
        .halign(gtk::Align::Center)
        .visible(false)
        .build();
    let loading_box = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(12)
        .halign(gtk::Align::Center)
        .build();
    loading_box.append(&loading_spinner);
    loading_box.append(&loading_retry_button);
    loading_page.set_child(Some(&loading_box));

    let login_status_label = gtk::Label::builder().wrap(true).xalign(0.0).build();
    let qr_picture = gtk::Picture::builder().halign(gtk::Align::Center).build();
    qr_picture.set_size_request(252, 252);
    let qr_frame = gtk::Frame::builder()
        .child(&qr_picture)
        .halign(gtk::Align::Center)
        .css_classes(["card"])
        .margin_top(6)
        .margin_bottom(6)
        .build();
    let qr_error_label = gtk::Label::builder().wrap(true).xalign(0.0).build();
    let qr_expiry_label = gtk::Label::builder()
        .xalign(0.0)
        .css_classes(["dim-label"])
        .build();
    let login_box = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(16)
        .halign(gtk::Align::Center)
        .build();
    login_box.append(&login_status_label);
    login_box.append(&qr_frame);
    login_box.append(&qr_error_label);
    login_box.append(&qr_expiry_label);
    let login_page = adw::StatusPage::builder()
        .icon_name("chat-bubbles-symbolic")
        .title("Scan to sign in")
        .description("whatevrd keeps your WhatsApp session alive in the background, even when this window is closed.")
        .build();
    login_page.set_child(Some(&login_box));

    let banner = adw::Banner::new("Connecting to WhatsApp");
    banner.set_revealed(false);
    banner.set_button_label(Some(""));

    let chat_list = gtk::ListBox::builder()
        .selection_mode(gtk::SelectionMode::Single)
        .activate_on_single_click(true)
        .build();
    chat_list.add_css_class("navigation-sidebar");
    let chat_scroller = gtk::ScrolledWindow::builder()
        .hscrollbar_policy(gtk::PolicyType::Never)
        .vexpand(true)
        .child(&chat_list)
        .build();
    let sidebar_loading_page = adw::StatusPage::builder()
        .title("Loading chats")
        .description("Reading your local conversation list.")
        .icon_name("chat-bubbles-symbolic")
        .build();
    let sidebar_spinner = gtk::Spinner::builder().spinning(true).build();
    sidebar_loading_page.set_child(Some(&sidebar_spinner));
    let sidebar_empty_page = adw::StatusPage::builder()
        .title("No chats yet")
        .description("Chats will appear here once text history is stored locally.")
        .icon_name("chat-bubbles-symbolic")
        .build();
    let sidebar_stack = gtk::Stack::builder().vexpand(true).build();
    sidebar_stack.add_named(&sidebar_loading_page, Some(SIDEBAR_LOADING_PAGE));
    sidebar_stack.add_named(&sidebar_empty_page, Some(SIDEBAR_EMPTY_PAGE));
    sidebar_stack.add_named(&chat_scroller, Some(SIDEBAR_LIST_PAGE));
    sidebar_stack.set_visible_child_name(SIDEBAR_LOADING_PAGE);

    let sidebar_header = adw::HeaderBar::new();
    sidebar_header.set_title_widget(Some(&gtk::Box::new(gtk::Orientation::Horizontal, 0)));
    sidebar_header.pack_end(&logout_button);
    sidebar_header.pack_end(&refresh_button);

    let sidebar_toolbar = adw::ToolbarView::new();
    sidebar_toolbar.add_top_bar(&sidebar_header);
    let sidebar_content = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .vexpand(true)
        .build();
    sidebar_content.append(&gtk::Separator::new(gtk::Orientation::Horizontal));
    sidebar_content.append(&sidebar_stack);
    sidebar_toolbar.set_content(Some(&sidebar_content));

    let conversation_avatar = adw::Avatar::new(32, Some(""), true);
    let conversation_title = gtk::Label::builder()
        .xalign(0.5)
        .valign(gtk::Align::Center)
        .css_classes(["conversation-header-name"])
        .ellipsize(pango::EllipsizeMode::End)
        .build();
    let conversation_title_box = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .valign(gtk::Align::Center)
        .build();
    conversation_title_box.append(&conversation_title);
    let conversation_header = gtk::Box::builder()
        .orientation(gtk::Orientation::Horizontal)
        .spacing(8)
        .margin_top(6)
        .margin_bottom(6)
        .margin_start(10)
        .margin_end(10)
        .valign(gtk::Align::Center)
        .visible(false)
        .build();
    conversation_header.append(&conversation_avatar);
    conversation_header.append(&conversation_title_box);

    let content_header = adw::HeaderBar::new();
    content_header.pack_start(&back_button);
    content_header.set_title_widget(Some(&conversation_header));
    let message_box = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(12)
        .margin_top(18)
        .margin_bottom(18)
        .margin_start(18)
        .margin_end(18)
        .build();
    let message_scroller = gtk::ScrolledWindow::builder()
        .hscrollbar_policy(gtk::PolicyType::Never)
        .vexpand(true)
        .child(&message_box)
        .build();
    let composer_buffer = gtk::TextBuffer::new(None);
    let composer_text_view = gtk::TextView::with_buffer(&composer_buffer);
    composer_text_view.set_wrap_mode(gtk::WrapMode::WordChar);
    composer_text_view.set_top_margin(8);
    composer_text_view.set_bottom_margin(8);
    composer_text_view.set_left_margin(10);
    composer_text_view.set_right_margin(10);
    composer_text_view.set_accepts_tab(false);
    composer_text_view.set_valign(gtk::Align::Center);
    let composer_scroller = gtk::ScrolledWindow::builder()
        .child(&composer_text_view)
        .hscrollbar_policy(gtk::PolicyType::Never)
        .vscrollbar_policy(gtk::PolicyType::Never)
        .propagate_natural_height(true)
        .max_content_height(COMPOSER_MAX_HEIGHT)
        .valign(gtk::Align::Center)
        .build();
    let composer_frame = gtk::Frame::builder()
        .child(&composer_scroller)
        .hexpand(true)
        .valign(gtk::Align::Center)
        .css_classes(["composer-input"])
        .build();
    let composer_send_button = gtk::Button::builder()
        .icon_name("mail-send-symbolic")
        .tooltip_text("Send message")
        .css_classes(["suggested-action", "circular"])
        .valign(gtk::Align::End)
        .build();
    let composer_attach_button = gtk::Button::builder()
        .icon_name("mail-attachment-symbolic")
        .tooltip_text("Attach image")
        .css_classes(["flat", "circular"])
        .valign(gtk::Align::End)
        .build();
    let composer_error_label = gtk::Label::builder()
        .wrap(true)
        .xalign(0.0)
        .css_classes(["error"])
        .visible(false)
        .build();
    let composer_row = gtk::Box::builder()
        .orientation(gtk::Orientation::Horizontal)
        .spacing(12)
        .build();
    composer_row.append(&composer_attach_button);
    composer_row.append(&composer_frame);
    composer_row.append(&composer_send_button);
    let composer_box = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(8)
        .margin_top(14)
        .margin_bottom(18)
        .margin_start(18)
        .margin_end(18)
        .build();
    composer_box.append(&composer_error_label);
    composer_box.append(&composer_row);

    let conversation_placeholder_page = adw::StatusPage::builder()
        .title("Select a chat")
        .description("Choose a conversation from the sidebar to read the local message history.")
        .icon_name("chat-bubbles-symbolic")
        .build();
    let conversation_loading_page = adw::StatusPage::builder()
        .title("Loading messages")
        .description("Reading the latest 50 messages from the local store.")
        .icon_name("mail-message-symbolic")
        .build();
    let conversation_spinner = gtk::Spinner::builder().spinning(true).build();
    conversation_loading_page.set_child(Some(&conversation_spinner));
    let conversation_empty_page = adw::StatusPage::builder()
        .title("No text messages yet")
        .description("This conversation is stored, but there are no text messages available for display yet.")
        .icon_name("mail-message-symbolic")
        .build();

    let conversation_content_stack = gtk::Stack::builder().vexpand(true).build();

    conversation_content_stack
        .add_named(&conversation_loading_page, Some(CONVERSATION_LOADING_PAGE));

    conversation_content_stack.add_named(&conversation_empty_page, Some(CONVERSATION_EMPTY_PAGE));
    conversation_content_stack.add_named(&message_scroller, Some(CONVERSATION_MESSAGES_PAGE));
    conversation_content_stack.set_visible_child_name(CONVERSATION_LOADING_PAGE);

    let selected_conversation_toolbar = adw::ToolbarView::new();
    selected_conversation_toolbar.set_content(Some(&conversation_content_stack));
    selected_conversation_toolbar.add_bottom_bar(&composer_box);

    let conversation_stack = gtk::Stack::builder().vexpand(true).hexpand(true).build();
    conversation_stack.add_named(
        &conversation_placeholder_page,
        Some(CONVERSATION_PLACEHOLDER_PAGE),
    );
    conversation_stack.add_named(&selected_conversation_toolbar, Some("selected"));
    conversation_stack.set_visible_child_name(CONVERSATION_PLACEHOLDER_PAGE);

    let content_toolbar = adw::ToolbarView::new();
    content_toolbar.add_top_bar(&content_header);
    content_toolbar.set_content(Some(&conversation_stack));

    let sidebar_page = adw::NavigationPage::new(&sidebar_toolbar, "Chats");
    let content_page = adw::NavigationPage::new(&content_toolbar, "Conversation");
    let split_view = adw::NavigationSplitView::new();
    split_view.set_sidebar(Some(&sidebar_page));
    split_view.set_content(Some(&content_page));
    split_view.set_sidebar_width_fraction(0.30);
    split_view.set_min_sidebar_width(240.0);
    split_view.set_max_sidebar_width(340.0);
    split_view.set_show_content(false);
    split_view.set_vexpand(true);
    split_view.set_hexpand(true);

    let main_page = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .vexpand(true)
        .hexpand(true)
        .build();
    main_page.append(&banner);
    main_page.append(&split_view);

    let root_stack = gtk::Stack::builder().vexpand(true).hexpand(true).build();
    root_stack.add_named(&loading_page, Some(ROOT_LOADING_PAGE));
    root_stack.add_named(&login_page, Some(ROOT_LOGIN_PAGE));
    root_stack.add_named(&main_page, Some(ROOT_MAIN_PAGE));
    root_stack.set_visible_child_name(ROOT_LOADING_PAGE);

    let window = adw::ApplicationWindow::builder()
        .application(app)
        .title("whatevr")
        .default_width(1080)
        .default_height(760)
        .content(&root_stack)
        .build();

    let widgets = Rc::new(Widgets {
        root_stack,
        loading_page,
        loading_spinner,
        loading_retry_button,
        login_status_label,
        qr_picture,
        qr_error_label,
        qr_expiry_label,
        banner,
        split_view,
        sidebar_stack,
        sidebar_loading_page,
        sidebar_empty_page,
        chat_list,
        conversation_stack,
        conversation_content_stack,
        conversation_loading_page,
        conversation_header,
        conversation_avatar,
        conversation_title,
        message_scroller,
        message_box,
        composer_scroller,
        composer_text_view,
        composer_error_label,
        composer_send_button,
        composer_attach_button,
        back_button,
        logout_button,
        syncing_chat_selection: Cell::new(false),
        message_scroll_generation: Rc::new(Cell::new(0)),
        rendered_chat_id: RefCell::new(None),
        rendered_messages: RefCell::new(Vec::new()),
    });

    let state = Rc::new(RefCell::new(UiState::default()));
    let (sender, receiver) = mpsc::channel::<UiMessage>();

    connect_signals(&widgets, &state, &sender, &refresh_button);

    {
        let retry_sender = sender.clone();
        widgets.loading_retry_button.connect_clicked(move |_| {
            request_status(retry_sender.clone());
        });
    }

    {
        let reconnect_sender = sender.clone();
        let reconnect_state = state.clone();
        let reconnect_banner = widgets.banner.clone();
        widgets.banner.connect_button_clicked(move |_| {
            let should_retry_daemon = reconnect_state.borrow().daemon_disconnected;
            if should_retry_daemon {
                reconnect_banner.set_button_label(Some(""));
                request_status(reconnect_sender.clone());
                return;
            }

            {
                let mut s = reconnect_state.borrow_mut();
                if s.reconnect_in_flight {
                    return;
                }
                s.reconnect_in_flight = true;
            }
            reconnect_banner.set_button_label(Some(""));
            request_reconnect(reconnect_sender.clone());
        });
    }

    let focus_state = state.clone();
    let focus_sender = sender.clone();
    window.connect_is_active_notify(move |win| {
        let focused = win.is_active();
        let selected = {
            let mut s = focus_state.borrow_mut();
            s.window_focused = focused;
            s.selected_chat_id.clone()
        };
        if focused && !focus_state.borrow().daemon_disconnected {
            if let Some(chat_id) = selected {
                request_mark_chat_read(focus_sender.clone(), chat_id);
            }
        }
    });

    let widgets_for_receiver = widgets.clone();
    let state_for_receiver = state.clone();
    let sender_for_receiver = sender.clone();
    glib::timeout_add_local(Duration::from_millis(40), move || {
        while let Ok(message) = receiver.try_recv() {
            handle_ui_message(
                message,
                &widgets_for_receiver,
                &state_for_receiver,
                &sender_for_receiver,
            );
        }
        glib::ControlFlow::Continue
    });

    request_status(sender.clone());
    hold_frontend_session(sender.clone());
    subscribe_login_events(sender.clone());
    subscribe_daemon_events(sender.clone());

    window.present();
}

fn connect_signals(
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &mpsc::Sender<UiMessage>,
    refresh_button: &gtk::Button,
) {
    let refresh_sender = sender.clone();
    let refresh_state = state.clone();
    refresh_button.connect_clicked(move |_| {
        request_status(refresh_sender.clone());
        request_chats(refresh_sender.clone());

        let selected_chat_id = refresh_state.borrow().selected_chat_id.clone();
        if let Some(chat_id) = selected_chat_id {
            let generation = next_message_request_generation(&refresh_state);
            request_messages(refresh_sender.clone(), chat_id, generation);
        }
    });

    let logout_widgets = widgets.clone();
    let logout_sender = sender.clone();
    widgets.logout_button.connect_clicked(move |_| {
        show_logout_confirmation(&logout_widgets, &logout_sender);
    });

    let back_widgets = widgets.clone();
    let back_state = state.clone();
    widgets.back_button.connect_clicked(move |_| {
        back_widgets.split_view.set_show_content(false);
        update_navigation_state(&back_widgets, &back_state.borrow());
    });

    let collapse_widgets = widgets.clone();
    let collapse_state = state.clone();
    widgets.split_view.connect_collapsed_notify(move |_| {
        update_navigation_state(&collapse_widgets, &collapse_state.borrow());
    });

    let select_widgets = widgets.clone();
    let select_state = state.clone();
    let select_sender = sender.clone();
    widgets.chat_list.connect_row_selected(move |_, row| {
        if select_widgets.syncing_chat_selection.get() {
            return;
        }

        if let Some(row) = row {
            open_chat_at_index(
                row.index() as usize,
                false,
                &select_widgets,
                &select_state,
                &select_sender,
            );
        }
    });

    let activate_widgets = widgets.clone();
    let activate_state = state.clone();
    let activate_sender = sender.clone();
    widgets.chat_list.connect_row_activated(move |_, row| {
        if activate_widgets.split_view.shows_content() {
            return;
        }

        open_chat_at_index(
            row.index() as usize,
            false,
            &activate_widgets,
            &activate_state,
            &activate_sender,
        );
    });

    let send_widgets = widgets.clone();
    let send_state = state.clone();
    let send_sender = sender.clone();
    widgets.composer_send_button.connect_clicked(move |_| {
        submit_composer_message(&send_widgets, &send_state, &send_sender);
    });

    let resize_widgets = widgets.clone();
    widgets
        .composer_scroller
        .connect_notify_local(Some("allocated-width"), move |_, _| {
            let idle_widgets = resize_widgets.clone();
            glib::idle_add_local_once(move || {
                resize_composer(&idle_widgets);
            });
        });

    // Typing indicator + send button state: fire when composer buffer changes
    let typing_state = state.clone();
    let typing_widgets = widgets.clone();
    let typing_sender = sender.clone();
    widgets
        .composer_text_view
        .buffer()
        .connect_changed(move |buf| {
            let non_empty = buf.char_count() > 0;
            let (chat_id, should_send) = {
                let mut s = typing_state.borrow_mut();
                let id = s.selected_chat_id.clone();
                let changed = non_empty != s.last_sent_composing;
                if changed {
                    s.last_sent_composing = non_empty;
                }
                (id, changed)
            };
            if should_send {
                if let Some(chat_id) = chat_id {
                    request_set_chat_presence(typing_sender.clone(), chat_id, non_empty);
                }
            }

            let s = typing_state.borrow();
            render_composer_state(&typing_widgets, &s);
            let idle_widgets = typing_widgets.clone();
            glib::idle_add_local_once(move || {
                resize_composer(&idle_widgets);
                scroll_composer_to_bottom_if_needed(&idle_widgets);
            });
        });

    // Attach button: open file picker for images
    let attach_widgets = widgets.clone();
    let attach_state = state.clone();
    let attach_sender = sender.clone();
    widgets.composer_attach_button.connect_clicked(move |_| {
        let chat_id = attach_state.borrow().selected_chat_id.clone();
        let Some(chat_id) = chat_id else { return };

        let window = attach_widgets
            .conversation_stack
            .root()
            .and_downcast::<gtk::Window>();

        let dialog = gtk::FileDialog::new();
        dialog.set_title("Choose an image");
        let filter = gtk::FileFilter::new();
        filter.add_mime_type("image/*");
        filter.set_name(Some("Images"));
        let filters = gio::ListStore::new::<gtk::FileFilter>();
        filters.append(&filter);
        dialog.set_filters(Some(&filters));
        dialog.set_default_filter(Some(&filter));

        let sender = attach_sender.clone();
        dialog.open(window.as_ref(), None::<&gio::Cancellable>, move |result| {
            if let Ok(file) = result {
                if let Some(path) = file.path() {
                    if let Some(path_str) = path.to_str() {
                        request_send_media(sender.clone(), chat_id.clone(), path_str.to_string());
                    }
                }
            }
        });
    });

    let key_widgets = widgets.clone();
    let key_state = state.clone();
    let key_sender = sender.clone();
    let composer_key_controller = gtk::EventControllerKey::new();
    composer_key_controller.connect_key_pressed(move |_, key, _, modifiers| {
        if key == gdk::Key::Return || key == gdk::Key::KP_Enter {
            if modifiers.contains(gdk::ModifierType::SHIFT_MASK) {
                return glib::Propagation::Proceed;
            }

            submit_composer_message(&key_widgets, &key_state, &key_sender);
            return glib::Propagation::Stop;
        }

        glib::Propagation::Proceed
    });
    widgets
        .composer_text_view
        .add_controller(composer_key_controller);
}

fn handle_ui_message(
    message: UiMessage,
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &mpsc::Sender<UiMessage>,
) {
    match message {
        UiMessage::StatusLoaded(status) => {
            let daemon_state =
                DaemonState::try_from(status.state).unwrap_or(DaemonState::Unspecified);
            widgets.loading_spinner.set_spinning(true);
            widgets.loading_spinner.set_visible(true);
            widgets.loading_retry_button.set_visible(false);
            apply_daemon_state(
                widgets,
                state,
                sender,
                daemon_state,
                status.detail,
                status.can_reconnect,
                status.retry_attempt,
                status.next_retry_unix,
            );
        }
        UiMessage::StatusFailed(error) => {
            {
                let mut s = state.borrow_mut();
                s.daemon_disconnected = true;
            }
            widgets
                .loading_page
                .set_title("Unable to connect to whatevrd");
            widgets
                .loading_page
                .set_description(Some(&friendly_daemon_error_text(&error)));
            widgets.loading_spinner.set_spinning(false);
            widgets.loading_spinner.set_visible(false);
            widgets.loading_retry_button.set_visible(true);
            widgets.root_stack.set_visible_child_name(ROOT_LOADING_PAGE);
            let state = state.borrow();
            update_banner(widgets, &state);
        }
        UiMessage::DaemonState {
            state: daemon_state,
            detail,
            can_reconnect,
            retry_attempt,
            next_retry_unix,
        } => {
            apply_daemon_state(
                widgets,
                state,
                sender,
                daemon_state,
                detail,
                can_reconnect,
                retry_attempt,
                next_retry_unix,
            );
        }
        UiMessage::QrCode { code, expires_at } => {
            match render_qr_texture(&code) {
                Ok(texture) => {
                    widgets.qr_picture.set_paintable(Some(&texture));
                    widgets.qr_error_label.set_text("");
                }
                Err(error) => {
                    widgets
                        .qr_picture
                        .set_paintable(None::<&gdk::MemoryTexture>);
                    widgets
                        .qr_error_label
                        .set_text(&format!("Unable to render QR code: {error}"));
                }
            }
            widgets
                .qr_expiry_label
                .set_text(&format_qr_expiry(expires_at));
            widgets.root_stack.set_visible_child_name(ROOT_LOGIN_PAGE);
        }
        UiMessage::ChatsLoaded(chats) => {
            {
                let mut state = state.borrow_mut();
                state.chats = chats;
                state.chats_loaded = true;
                sync_selected_chat_after_reload(&mut state);
            }

            let state = state.borrow();
            render_chat_list(widgets, &state);
            render_conversation(widgets, &state);
            update_navigation_state(widgets, &state);
        }
        UiMessage::ChatsFailed(error) => {
            let state = state.borrow();
            if state.chats_loaded {
                show_banner_notice(widgets, &error);
            } else {
                widgets
                    .sidebar_loading_page
                    .set_title("Unable to load chats");
                widgets.sidebar_loading_page.set_description(Some(&error));
                widgets
                    .sidebar_stack
                    .set_visible_child_name(SIDEBAR_LOADING_PAGE);
            }
        }
        UiMessage::MessagesLoaded {
            chat_id,
            generation,
            messages,
        } => {
            let (should_mark_read, should_clear_composer) = {
                let mut state = state.borrow_mut();
                if state.message_request_generation != generation {
                    return;
                }
                if state.pending_chat_id.as_deref() == Some(chat_id.as_str()) {
                    let should_clear_composer = commit_pending_chat(&mut state, chat_id.clone());
                    state.current_messages_chat_id = Some(chat_id.clone());
                    state.current_messages = messages;
                    state.loading_chat_id = None;
                    (true, should_clear_composer)
                } else if state.selected_chat_id.as_deref() == Some(chat_id.as_str()) {
                    state.current_messages_chat_id = Some(chat_id.clone());
                    state.current_messages = messages;
                    state.loading_chat_id = None;
                    (false, false)
                } else {
                    return;
                }
            };

            if should_clear_composer {
                clear_composer(&widgets.composer_text_view);
            }

            let state = state.borrow();
            render_chat_list(widgets, &state);
            render_conversation(widgets, &state);
            update_navigation_state(widgets, &state);
            if should_mark_read {
                request_mark_chat_read(sender.clone(), chat_id);
            }
        }
        UiMessage::MessagesFailed {
            chat_id,
            generation,
            error,
        } => {
            let (should_show_error, should_clear_composer) = {
                let mut state = state.borrow_mut();
                if state.message_request_generation != generation {
                    return;
                }
                if state.pending_chat_id.as_deref() == Some(chat_id.as_str()) {
                    let should_clear_composer = commit_pending_chat(&mut state, chat_id.clone());
                    state.loading_chat_id = Some(chat_id.clone());
                    (true, should_clear_composer)
                } else {
                    (
                        state.selected_chat_id.as_deref() == Some(chat_id.as_str()),
                        false,
                    )
                }
            };

            if !should_show_error {
                return;
            }

            if should_clear_composer {
                clear_composer(&widgets.composer_text_view);
            }

            let state = state.borrow();
            render_chat_list(widgets, &state);
            update_navigation_state(widgets, &state);
            widgets
                .conversation_stack
                .set_visible_child_name("selected");
            render_conversation_header(widgets, &state);
            widgets
                .conversation_loading_page
                .set_title("Unable to load messages");
            widgets
                .conversation_loading_page
                .set_description(Some(&error));
            widgets
                .conversation_content_stack
                .set_visible_child_name(CONVERSATION_LOADING_PAGE);
        }
        UiMessage::ShowConversationLoading {
            chat_id,
            generation,
        } => {
            let (should_mark_read, should_clear_composer) = {
                let mut state = state.borrow_mut();
                if state.message_request_generation != generation
                    || state.pending_chat_id.as_deref() != Some(chat_id.as_str())
                {
                    return;
                }

                let should_clear_composer = commit_pending_chat(&mut state, chat_id.clone());
                state.loading_chat_id = Some(chat_id.clone());
                (true, should_clear_composer)
            };

            if should_clear_composer {
                clear_composer(&widgets.composer_text_view);
            }

            let state = state.borrow();
            render_chat_list(widgets, &state);
            render_conversation(widgets, &state);
            update_navigation_state(widgets, &state);
            if should_mark_read {
                request_mark_chat_read(sender.clone(), chat_id);
            }
        }
        UiMessage::SendSucceeded { chat_id } => {
            let should_clear_composer = {
                let mut state = state.borrow_mut();
                state.send_in_flight = false;
                let selected = state.selected_chat_id.as_deref() == Some(chat_id.as_str());
                if selected {
                    state.composer_error.clear();
                }
                selected
            };

            if should_clear_composer {
                clear_composer(&widgets.composer_text_view);
            }

            let state = state.borrow();
            render_composer_state(widgets, &state);
            if should_clear_composer {
                widgets.composer_text_view.grab_focus();
            }
        }
        UiMessage::SendFailed { chat_id, error } => {
            let mut should_render = false;
            {
                let mut state = state.borrow_mut();
                state.send_in_flight = false;
                if state.selected_chat_id.as_deref() == Some(chat_id.as_str()) {
                    state.composer_error = error;
                    should_render = true;
                }
            }

            if should_render {
                let state = state.borrow();
                render_conversation(widgets, &state);
            }
        }
        UiMessage::MediaSendSucceeded => {}
        UiMessage::MediaSendFailed { chat_id, error } => {
            let mut should_render = false;
            {
                let mut state = state.borrow_mut();
                if state.selected_chat_id.as_deref() == Some(chat_id.as_str()) {
                    state.composer_error = error;
                    should_render = true;
                }
            }
            if should_render {
                let state = state.borrow();
                render_conversation(widgets, &state);
            }
        }
        UiMessage::ChatPresence {
            chat_id,
            is_composing,
        } => {
            let selected_matches = {
                let mut s = state.borrow_mut();
                s.composing_peers.insert(chat_id.clone(), is_composing);
                s.selected_chat_id.as_deref() == Some(chat_id.as_str())
            };
            if selected_matches {
                render_conversation_header(widgets, &state.borrow());
            }
        }
        UiMessage::NewMessage { message } => {
            let chat_id = message.chat_id.clone();
            let was_near_bottom = is_scroller_near_bottom(widgets);
            let (is_for_selected, mark_read) = {
                let mut s = state.borrow_mut();
                let selected = s.selected_chat_id.as_deref() == Some(chat_id.as_str());
                let mark_read = selected && s.window_focused;
                if selected && s.current_messages_chat_id.as_deref() == Some(chat_id.as_str()) {
                    if let Some(existing) = s
                        .current_messages
                        .iter_mut()
                        .find(|existing| existing.id == message.id)
                    {
                        *existing = message;
                    } else {
                        s.current_messages.push(message);
                    }
                }
                (selected, mark_read)
            };

            if is_for_selected {
                let state = state.borrow();
                render_conversation(widgets, &state);
                if was_near_bottom {
                    scroll_messages_to_bottom(widgets);
                }
            }
            if mark_read {
                request_mark_chat_read(sender.clone(), chat_id);
            }
        }
        UiMessage::MessageUpdated { message } => {
            let chat_id = message.chat_id.clone();
            let updated = {
                let mut s = state.borrow_mut();
                if s.selected_chat_id.as_deref() != Some(chat_id.as_str()) {
                    false
                } else if let Some(existing) = s
                    .current_messages
                    .iter_mut()
                    .find(|existing| existing.id == message.id)
                {
                    *existing = message;
                    true
                } else {
                    false
                }
            };

            if updated {
                let state = state.borrow();
                render_conversation(widgets, &state);
            }
        }
        UiMessage::ChatUpdated {
            chat,
            previous_chat_id,
        } => {
            let (header_changed, updated_row_index) = {
                let mut s = state.borrow_mut();
                let new_chat_id = chat.id.clone();
                let old_chat_id = previous_chat_id
                    .clone()
                    .unwrap_or_else(|| new_chat_id.clone());
                let old_index = s
                    .chats
                    .iter()
                    .position(|existing| existing.id == old_chat_id);

                if let Some(prev_id) = previous_chat_id.as_deref() {
                    if s.selected_chat_id.as_deref() == Some(prev_id) {
                        s.selected_chat_id = Some(chat.id.clone());
                        if s.current_messages_chat_id.as_deref() == Some(prev_id) {
                            s.current_messages_chat_id = Some(chat.id.clone());
                            for message in s.current_messages.iter_mut() {
                                message.chat_id = chat.id.clone();
                            }
                        }
                        if let Some(value) = s.composing_peers.remove(prev_id) {
                            s.composing_peers.insert(chat.id.clone(), value);
                        }
                    }
                    s.chats.retain(|existing| existing.id != prev_id);
                }

                let header_changed = s
                    .selected_chat_id
                    .as_deref()
                    .map(|selected| selected == chat.id)
                    .unwrap_or(false)
                    && {
                        let prev = s.chats.iter().find(|existing| existing.id == chat.id);
                        match prev {
                            Some(existing) => {
                                existing.name != chat.name
                                    || existing.is_group != chat.is_group
                                    || existing.avatar_local_path != chat.avatar_local_path
                            }
                            None => true,
                        }
                    };

                upsert_chat(&mut s.chats, chat);
                let new_index = s
                    .chats
                    .iter()
                    .position(|existing| existing.id == new_chat_id);
                let updated_row_index = old_index.filter(|old_index| Some(*old_index) == new_index);
                (header_changed, updated_row_index)
            };

            let state_borrow = state.borrow();
            if let Some(index) = updated_row_index {
                if let Some(chat) = state_borrow.chats.get(index) {
                    update_chat_row_at_index(&widgets.chat_list, index, chat);
                }
            } else {
                render_chat_list(widgets, &state_borrow);
            }
            if header_changed {
                render_conversation_header(widgets, &state_borrow);
            }
        }
        UiMessage::LogoutSucceeded => {
            {
                let mut state = state.borrow_mut();
                clear_local_ui_state(&mut state);
            }
            clear_composer(&widgets.composer_text_view);
            let state = state.borrow();
            widgets.root_stack.set_visible_child_name(ROOT_LOGIN_PAGE);
            render_chat_list(widgets, &state);
            render_conversation(widgets, &state);
            update_navigation_state(widgets, &state);
            show_banner_notice(widgets, "Logged out and deleted local session data");
            drop(state);
            request_status(sender.clone());
        }
        UiMessage::LogoutFailed(error) => {
            show_banner_notice(widgets, &format!("Logout failed: {error}"));
        }
        UiMessage::Notice(text) => {
            let state = state.borrow();
            show_banner_notice_preserving_action(widgets, &state, &text);
        }
        UiMessage::DaemonConnectionLost => {
            {
                let mut s = state.borrow_mut();
                s.daemon_disconnected = true;
            }
            let state = state.borrow();
            update_banner(widgets, &state);
            render_composer_state(widgets, &state);
        }
        UiMessage::DaemonConnectionRestored => {
            let was_disconnected = {
                let mut s = state.borrow_mut();
                let was_disconnected = s.daemon_disconnected;
                s.daemon_disconnected = false;
                was_disconnected
            };
            request_status(sender.clone());
            request_chats(sender.clone());
            let selected = state.borrow().selected_chat_id.clone();
            if let Some(chat_id) = selected {
                let generation = next_message_request_generation(state);
                request_messages(sender.clone(), chat_id, generation);
            }
            let state = state.borrow();
            render_composer_state(widgets, &state);
            update_banner(widgets, &state);
            if was_disconnected {
                show_banner_notice(widgets, "Connected to whatevrd");
            }
        }
        UiMessage::ReconnectSucceeded => {
            {
                let mut s = state.borrow_mut();
                s.reconnect_in_flight = false;
            }
            let state = state.borrow();
            show_banner_notice_preserving_action(widgets, &state, "Reconnect requested");
        }
        UiMessage::ReconnectFailed(error) => {
            {
                let mut s = state.borrow_mut();
                s.reconnect_in_flight = false;
                s.can_reconnect = true;
            }
            let state = state.borrow();
            update_banner(widgets, &state);
            show_banner_notice_preserving_action(
                widgets,
                &state,
                &format!("Unable to ask whatevrd to reconnect: {}", error),
            );
        }
    }
}

fn apply_daemon_state(
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &mpsc::Sender<UiMessage>,
    daemon_state: DaemonState,
    detail: String,
    can_reconnect: bool,
    retry_attempt: i32,
    next_retry_unix: i64,
) {
    let mut should_request_chats = false;

    {
        let mut state = state.borrow_mut();
        state.daemon_state = daemon_state;
        state.daemon_detail = detail.clone();
        state.can_reconnect = can_reconnect;
        state.retry_attempt = retry_attempt;
        state.next_retry_unix = next_retry_unix;
        state.daemon_disconnected = false;
        widgets.loading_spinner.set_spinning(true);
        widgets.loading_spinner.set_visible(true);
        widgets.loading_retry_button.set_visible(false);
        // Clear reconnect_in_flight once daemon confirms connection attempt.
        if matches!(
            daemon_state,
            DaemonState::Online | DaemonState::Connecting | DaemonState::Reconnecting
        ) {
            state.reconnect_in_flight = false;
        }

        if matches!(daemon_state, DaemonState::NeedLogin) {
            state.initial_chat_request_started = false;
            clear_local_ui_state(&mut state);
        }

        match daemon_state {
            DaemonState::Starting | DaemonState::Unspecified => {
                widgets.root_stack.set_visible_child_name(ROOT_LOADING_PAGE);
                widgets.loading_page.set_title("Starting whatevrd");
                widgets
                    .loading_page
                    .set_description(Some("Preparing the local session and syncing pipeline."));
            }
            DaemonState::NeedLogin => {
                widgets.root_stack.set_visible_child_name(ROOT_LOGIN_PAGE);
            }
            _ => {
                widgets.root_stack.set_visible_child_name(ROOT_MAIN_PAGE);
                if !state.initial_chat_request_started {
                    state.initial_chat_request_started = true;
                    should_request_chats = true;
                }
            }
        }
    }

    if should_request_chats {
        request_chats(sender.clone());
    }

    let state = state.borrow();
    render_chat_list(widgets, &state);
    render_conversation(widgets, &state);
    update_navigation_state(widgets, &state);

    widgets
        .login_status_label
        .set_text(&format_login_state(daemon_state, &detail));
    update_banner(widgets, &state);
}

fn update_banner(widgets: &Widgets, state: &UiState) {
    if state.daemon_disconnected {
        widgets
            .banner
            .set_title("Unable to connect to whatevrd. Retrying...");
        widgets.banner.set_button_label(Some("Retry"));
        widgets.banner.set_revealed(true);
        return;
    }

    let daemon_state = state.daemon_state;
    let detail = &state.daemon_detail;

    match daemon_state {
        DaemonState::Online => {
            widgets.banner.set_button_label(Some(""));
            widgets.banner.set_revealed(false);
        }
        DaemonState::Connecting => {
            widgets.banner.set_title(if detail.is_empty() {
                "Connecting to WhatsApp..."
            } else {
                detail
            });
            widgets.banner.set_button_label(Some(""));
            widgets.banner.set_revealed(true);
        }
        DaemonState::Reconnecting => {
            widgets.banner.set_title(if detail.is_empty() {
                "Reconnecting to WhatsApp..."
            } else {
                detail
            });
            let btn = if state.can_reconnect && !state.reconnect_in_flight {
                "Reconnect now"
            } else {
                ""
            };
            widgets.banner.set_button_label(Some(btn));
            widgets.banner.set_revealed(true);
        }
        DaemonState::Offline => {
            widgets.banner.set_title(if detail.is_empty() {
                "Offline. Local chats remain available."
            } else {
                detail
            });
            let btn = if state.can_reconnect && !state.reconnect_in_flight {
                "Reconnect now"
            } else {
                ""
            };
            widgets.banner.set_button_label(Some(btn));
            widgets.banner.set_revealed(true);
        }
        _ => {
            widgets.banner.set_button_label(Some(""));
            widgets.banner.set_revealed(false);
        }
    }
}

fn show_banner_notice(widgets: &Widgets, text: &str) {
    widgets.banner.set_button_label(Some(""));
    widgets.banner.set_title(text);
    widgets.banner.set_revealed(true);
}

fn show_banner_notice_preserving_action(widgets: &Widgets, state: &UiState, text: &str) {
    if state.daemon_disconnected
        || matches!(
            state.daemon_state,
            DaemonState::Offline | DaemonState::Reconnecting
        )
    {
        update_banner(widgets, state);
    } else {
        show_banner_notice(widgets, text);
    }
}

fn next_message_request_generation(state: &Rc<RefCell<UiState>>) -> u64 {
    let mut state = state.borrow_mut();
    state.message_request_generation = state.message_request_generation.wrapping_add(1);
    state.message_request_generation
}

fn commit_pending_chat(state: &mut UiState, chat_id: String) -> bool {
    let was_selected = state.selected_chat_id.as_deref() == Some(chat_id.as_str());
    state.selected_chat_id = Some(chat_id.clone());
    state.pending_chat_id = None;
    state.composer_error.clear();

    if !was_selected {
        state.current_messages_chat_id = None;
        state.current_messages.clear();
        state.last_sent_composing = false;
    }

    !was_selected
}

fn clear_local_ui_state(state: &mut UiState) {
    state.chats.clear();
    state.chats_loaded = true;
    state.selected_chat_id = None;
    state.pending_chat_id = None;
    state.loading_chat_id = None;
    state.current_messages_chat_id = None;
    state.current_messages.clear();
    state.composer_error.clear();
    state.send_in_flight = false;
    state.composing_peers.clear();
    state.last_sent_composing = false;
}

fn show_logout_confirmation(widgets: &Rc<Widgets>, sender: &mpsc::Sender<UiMessage>) {
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

fn update_navigation_state(widgets: &Widgets, state: &UiState) {
    let show_content = if widgets.split_view.is_collapsed() {
        state.selected_chat_id.is_some() && !matches!(state.daemon_state, DaemonState::NeedLogin)
    } else {
        true
    };

    widgets.split_view.set_show_content(show_content);
    widgets
        .back_button
        .set_visible(widgets.split_view.is_collapsed() && show_content);
}

fn render_chat_list(widgets: &Widgets, state: &UiState) {
    widgets.syncing_chat_selection.set(true);
    clear_list_box(&widgets.chat_list);

    if !state.chats_loaded {
        widgets.sidebar_loading_page.set_title("Loading chats");
        widgets
            .sidebar_loading_page
            .set_description(Some("Reading your local conversation list."));
        widgets
            .sidebar_stack
            .set_visible_child_name(SIDEBAR_LOADING_PAGE);
        widgets.chat_list.unselect_all();
        widgets.syncing_chat_selection.set(false);
        return;
    }

    if state.chats.is_empty() {
        widgets
            .sidebar_empty_page
            .set_description(Some(match state.daemon_state {
                DaemonState::Connecting | DaemonState::Reconnecting => {
                    "Chats will appear here as the local store catches up."
                }
                _ => "Chats will appear here once text history is stored locally.",
            }));
        widgets
            .sidebar_stack
            .set_visible_child_name(SIDEBAR_EMPTY_PAGE);
        widgets.chat_list.unselect_all();
        widgets.syncing_chat_selection.set(false);
        return;
    }

    let effective_selected_chat_id = state
        .pending_chat_id
        .as_deref()
        .or(state.selected_chat_id.as_deref());
    let mut selected_index = None;
    for (index, chat) in state.chats.iter().enumerate() {
        let row = build_chat_row(chat);
        widgets.chat_list.append(&row);

        if effective_selected_chat_id == Some(chat.id.as_str()) {
            selected_index = Some(index);
        }
    }

    widgets
        .sidebar_stack
        .set_visible_child_name(SIDEBAR_LIST_PAGE);

    if let Some(index) = selected_index {
        if let Some(row) = widgets.chat_list.row_at_index(index as i32) {
            widgets.chat_list.select_row(Some(&row));
        }
    } else {
        widgets.chat_list.unselect_all();
    }

    widgets.syncing_chat_selection.set(false);
}

fn upsert_chat(chats: &mut Vec<proto::Chat>, chat: proto::Chat) {
    if let Some(existing) = chats.iter_mut().find(|existing| existing.id == chat.id) {
        *existing = chat;
    } else {
        chats.push(chat);
    }
    chats.sort_by_key(|chat| std::cmp::Reverse(chat.last_message_time_unix));
}

fn render_conversation_header(widgets: &Widgets, state: &UiState) {
    let Some(selected_chat_id) = state.selected_chat_id.as_deref() else {
        widgets.conversation_header.set_visible(false);
        return;
    };
    let Some(chat) = state.chats.iter().find(|chat| chat.id == selected_chat_id) else {
        widgets.conversation_header.set_visible(false);
        return;
    };

    widgets.conversation_header.set_visible(true);
    widgets
        .conversation_avatar
        .set_text(Some(display_chat_name(chat)));
    set_avatar_image(&widgets.conversation_avatar, &chat.avatar_local_path);
    widgets.conversation_title.set_text(display_chat_name(chat));
}

fn render_conversation(widgets: &Widgets, state: &UiState) {
    let Some(selected_chat_id) = state.selected_chat_id.as_deref() else {
        widgets
            .conversation_stack
            .set_visible_child_name(CONVERSATION_PLACEHOLDER_PAGE);
        render_conversation_header(widgets, state);
        reset_rendered_messages(widgets);
        render_composer_state(widgets, state);
        return;
    };

    if !state.chats.iter().any(|chat| chat.id == selected_chat_id) {
        widgets
            .conversation_stack
            .set_visible_child_name(CONVERSATION_PLACEHOLDER_PAGE);
        render_conversation_header(widgets, state);
        reset_rendered_messages(widgets);
        render_composer_state(widgets, state);
        return;
    }

    widgets
        .conversation_stack
        .set_visible_child_name("selected");

    render_conversation_header(widgets, state);

    if state.loading_chat_id.as_deref() == Some(selected_chat_id) {
        widgets
            .conversation_loading_page
            .set_title("Loading messages");
        widgets
            .conversation_loading_page
            .set_description(Some("Reading the latest 50 messages from the local store."));
        widgets
            .conversation_content_stack
            .set_visible_child_name(CONVERSATION_LOADING_PAGE);
        reset_rendered_messages(widgets);
        render_composer_state(widgets, state);
        return;
    }

    if state.current_messages_chat_id.as_deref() != Some(selected_chat_id) {
        render_composer_state(widgets, state);
        return;
    }

    if state.current_messages.is_empty() {
        widgets
            .conversation_content_stack
            .set_visible_child_name(CONVERSATION_EMPTY_PAGE);
        reset_rendered_messages(widgets);
        render_composer_state(widgets, state);
        return;
    }

    let is_newly_rendered_chat = widgets
        .rendered_chat_id
        .borrow()
        .as_deref()
        .map(|rendered_chat_id| rendered_chat_id != selected_chat_id)
        .unwrap_or(true);

    sync_message_rows(widgets, selected_chat_id, &state.current_messages);

    if is_newly_rendered_chat {
        reveal_messages_at_bottom(widgets);
    } else {
        widgets.message_scroller.set_opacity(1.0);
        scroll_messages_to_bottom(widgets);
        widgets
            .conversation_content_stack
            .set_visible_child_name(CONVERSATION_MESSAGES_PAGE);
    }
    render_composer_state(widgets, state);
}

fn reset_rendered_messages(widgets: &Widgets) {
    let mut rendered = widgets.rendered_messages.borrow_mut();
    for entry in rendered.drain(..) {
        widgets.message_box.remove(&entry.row);
    }
    *widgets.rendered_chat_id.borrow_mut() = None;
}

fn sync_message_rows(widgets: &Widgets, chat_id: &str, messages: &[proto::Message]) {
    let same_chat = widgets
        .rendered_chat_id
        .borrow()
        .as_deref()
        .map(|prev| prev == chat_id)
        .unwrap_or(false);

    if !same_chat {
        reset_rendered_messages(widgets);
        *widgets.rendered_chat_id.borrow_mut() = Some(chat_id.to_string());
    }

    let mut rendered = widgets.rendered_messages.borrow_mut();

    let mut common = 0;
    while common < messages.len() && common < rendered.len() {
        if rendered[common].id != messages[common].id {
            break;
        }
        common += 1;
    }

    for i in 0..common {
        let new_status = messages[i].status;
        if rendered[i].status != new_status {
            rendered[i]
                .meta_label
                .set_text(&format_message_meta(&messages[i]));
            rendered[i].status = new_status;
        }
    }

    while rendered.len() > common {
        if let Some(entry) = rendered.pop() {
            widgets.message_box.remove(&entry.row);
        }
    }

    for new_msg in &messages[common..] {
        let (row, meta_label) = build_message_row(new_msg);
        widgets.message_box.append(&row);
        rendered.push(RenderedMessage {
            id: new_msg.id.clone(),
            status: new_msg.status,
            row,
            meta_label,
        });
    }
}

fn set_avatar_image(avatar: &adw::Avatar, path: &str) {
    if path.is_empty() {
        avatar.set_custom_image(None::<&gdk::Texture>);
        return;
    }
    match load_texture_cached(path) {
        Some(texture) => avatar.set_custom_image(Some(&texture)),
        None => avatar.set_custom_image(None::<&gdk::Texture>),
    }
}

const TEXTURE_CACHE_CAP: usize = 256;

#[derive(Default)]
struct TextureCache {
    entries: HashMap<String, gdk::Texture>,
    order: VecDeque<String>,
}

thread_local! {
    static TEXTURE_CACHE: RefCell<TextureCache> = RefCell::new(TextureCache::default());
}

fn cached_texture(path: &str) -> Option<gdk::Texture> {
    TEXTURE_CACHE.with(|cache| cache.borrow().entries.get(path).cloned())
}

fn store_cached_texture(path: String, texture: gdk::Texture) {
    TEXTURE_CACHE.with(|cache| {
        let mut cache = cache.borrow_mut();
        if cache.entries.contains_key(&path) {
            return;
        }
        cache.entries.insert(path.clone(), texture);
        cache.order.push_back(path);
        while cache.order.len() > TEXTURE_CACHE_CAP {
            if let Some(evicted) = cache.order.pop_front() {
                cache.entries.remove(&evicted);
            }
        }
    });
}

fn load_texture_cached(path: &str) -> Option<gdk::Texture> {
    if let Some(texture) = cached_texture(path) {
        return Some(texture);
    }
    let texture = gdk::Texture::from_file(&gio::File::for_path(path)).ok()?;
    store_cached_texture(path.to_string(), texture.clone());
    Some(texture)
}

fn schedule_async_image_load(picture: gtk::Picture, path: String, display_w: i32, display_h: i32) {
    let (tx, rx) = std::sync::mpsc::channel::<Option<Vec<u8>>>();
    let path_for_thread = path.clone();
    thread::spawn(move || {
        let _ = tx.send(std::fs::read(&path_for_thread).ok());
    });

    let picture_weak = picture.downgrade();
    glib::idle_add_local(move || match rx.try_recv() {
        Ok(Some(bytes)) => {
            let glib_bytes = glib::Bytes::from(&bytes);
            let stream = gio::MemoryInputStream::from_bytes(&glib_bytes);
            if let Ok(pixbuf) = gdk::gdk_pixbuf::Pixbuf::from_stream_at_scale(
                &stream,
                display_w,
                display_h,
                false,
                None::<&gio::Cancellable>,
            ) {
                let texture = gdk::Texture::for_pixbuf(&pixbuf);
                store_cached_texture(path.clone(), texture.clone());
                if let Some(pic) = picture_weak.upgrade() {
                    pic.set_paintable(Some(&texture));
                }
            }
            glib::ControlFlow::Break
        }
        Ok(None) => glib::ControlFlow::Break,
        Err(std::sync::mpsc::TryRecvError::Empty) => glib::ControlFlow::Continue,
        Err(std::sync::mpsc::TryRecvError::Disconnected) => glib::ControlFlow::Break,
    });
}

fn open_chat_at_index(
    index: usize,
    force_reload: bool,
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &mpsc::Sender<UiMessage>,
) {
    let chat = {
        let state = state.borrow();
        state.chats.get(index).cloned()
    };

    let Some(chat) = chat else {
        return;
    };

    let chat_id = chat.id.clone();
    let mut should_request = true;
    let generation;

    {
        let mut state = state.borrow_mut();
        let already_selected = state.selected_chat_id.as_deref() == Some(chat_id.as_str());

        if already_selected
            && state.current_messages_chat_id.as_deref() == Some(chat_id.as_str())
            && !force_reload
        {
            should_request = false;
            generation = state.message_request_generation;
        } else if already_selected && force_reload {
            state.message_request_generation = state.message_request_generation.wrapping_add(1);
            generation = state.message_request_generation;
            state.loading_chat_id = Some(chat_id.clone());
            state.composer_error.clear();
        } else {
            state.message_request_generation = state.message_request_generation.wrapping_add(1);
            generation = state.message_request_generation;
            state.pending_chat_id = Some(chat_id.clone());
            state.loading_chat_id = None;
            state.composer_error.clear();
        }
    }

    if !should_request || force_reload {
        let state = state.borrow();
        render_conversation(widgets, &state);
        update_navigation_state(widgets, &state);
    } else {
        let state = state.borrow();
        render_composer_state(widgets, &state);
    }

    if !should_request {
        return;
    }

    schedule_conversation_loading(sender.clone(), chat_id.clone(), generation);
    request_messages(sender.clone(), chat_id, generation);
}

fn sync_selected_chat_after_reload(state: &mut UiState) {
    if let Some(selected_chat_id) = state.selected_chat_id.clone() {
        if !state.chats.iter().any(|chat| chat.id == selected_chat_id) {
            state.selected_chat_id = None;
            state.pending_chat_id = None;
            state.loading_chat_id = None;
            state.current_messages_chat_id = None;
            state.current_messages.clear();
            state.composer_error.clear();
            state.send_in_flight = false;
        }
    }
}

fn composer_send_allowed(state: &UiState) -> bool {
    if state.daemon_disconnected || state.send_in_flight {
        return false;
    }
    matches!(
        state.daemon_state,
        DaemonState::Online
            | DaemonState::Connecting
            | DaemonState::Reconnecting
            | DaemonState::Offline
    )
}

fn render_composer_state(widgets: &Widgets, state: &UiState) {
    let has_selected_chat = state.selected_chat_id.is_some();
    let enabled = has_selected_chat && composer_send_allowed(state);
    let has_text = !composer_text(&widgets.composer_text_view).trim().is_empty();

    widgets.composer_text_view.set_sensitive(enabled);
    widgets
        .composer_send_button
        .set_sensitive(enabled && has_text);
    widgets.composer_attach_button.set_sensitive(enabled);
    widgets.composer_text_view.set_visible(has_selected_chat);
    widgets.composer_send_button.set_visible(has_selected_chat);
    widgets
        .composer_attach_button
        .set_visible(has_selected_chat);

    if state.composer_error.is_empty() || !has_selected_chat {
        widgets.composer_error_label.set_visible(false);
        widgets.composer_error_label.set_text("");
    } else {
        widgets.composer_error_label.set_visible(true);
        widgets.composer_error_label.set_text(&state.composer_error);
    }

    resize_composer(widgets);
}

fn resize_composer(widgets: &Widgets) {
    let width = widgets.composer_scroller.allocated_width();
    let for_size = if width > 0 { width } else { -1 };
    let (_, natural_height, _, _) = widgets
        .composer_text_view
        .measure(gtk::Orientation::Vertical, for_size);
    let content_height = natural_height + COMPOSER_HEIGHT_SLACK;
    let target_height = content_height.clamp(1, COMPOSER_MAX_HEIGHT);
    let needs_scroll = content_height > COMPOSER_MAX_HEIGHT;

    widgets
        .composer_scroller
        .set_min_content_height(target_height);

    let scrollbar_policy = if needs_scroll {
        gtk::PolicyType::Always
    } else {
        gtk::PolicyType::Never
    };
    if widgets.composer_scroller.vscrollbar_policy() != scrollbar_policy {
        widgets
            .composer_scroller
            .set_vscrollbar_policy(scrollbar_policy);
    }

    if !needs_scroll {
        widgets.composer_scroller.vadjustment().set_value(0.0);
    } else {
        scroll_composer_to_bottom(widgets);
    }
}

fn scroll_composer_to_bottom(widgets: &Widgets) {
    let buffer = widgets.composer_text_view.buffer();
    let mut end_iter = buffer.end_iter();
    widgets
        .composer_text_view
        .scroll_to_iter(&mut end_iter, 0.0, true, 0.0, 1.0);

    let adjustment = widgets.composer_scroller.vadjustment();
    let bottom = (adjustment.upper() - adjustment.page_size()).max(adjustment.lower());
    adjustment.set_value(bottom);
}

fn scroll_composer_to_bottom_if_needed(widgets: &Widgets) {
    if widgets.composer_scroller.vscrollbar_policy() != gtk::PolicyType::Never {
        scroll_composer_to_bottom(widgets);
    }
}

fn submit_composer_message(
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &mpsc::Sender<UiMessage>,
) {
    let text = composer_text(&widgets.composer_text_view);
    if text.trim().is_empty() {
        return;
    }

    let chat_id = {
        let mut state = state.borrow_mut();
        if !composer_send_allowed(&state) {
            return;
        }

        let Some(chat_id) = state.selected_chat_id.clone() else {
            return;
        };

        state.send_in_flight = true;
        state.composer_error.clear();
        chat_id
    };

    {
        let state = state.borrow();
        render_composer_state(widgets, &state);
    }

    request_send_text(sender.clone(), chat_id, text);
}

fn composer_text(text_view: &gtk::TextView) -> String {
    let buffer = text_view.buffer();
    buffer
        .text(&buffer.start_iter(), &buffer.end_iter(), false)
        .to_string()
}

fn clear_composer(text_view: &gtk::TextView) {
    text_view.buffer().set_text("");
}

fn request_status(sender: mpsc::Sender<UiMessage>) {
    spawn_async(async move {
        match fetch_status().await {
            Ok(status) => {
                let _ = sender.send(UiMessage::StatusLoaded(status));
            }
            Err(error) => {
                let _ = sender.send(UiMessage::StatusFailed(error.to_string()));
            }
        }
    });
}

fn subscribe_login_events(sender: mpsc::Sender<UiMessage>) {
    spawn_async(async move {
        let mut backoff = Duration::from_secs(2);
        loop {
            match stream_login_events(sender.clone()).await {
                Ok(()) => break,
                Err(_) => {
                    tokio::time::sleep(backoff).await;
                    backoff = (backoff * 2).min(Duration::from_secs(30));
                }
            }
        }
    });
}

fn hold_frontend_session(sender: mpsc::Sender<UiMessage>) {
    spawn_async(async move {
        let mut backoff = Duration::from_secs(2);
        loop {
            match stream_frontend_session(sender.clone()).await {
                Ok(()) => break,
                Err(_) => {
                    tokio::time::sleep(backoff).await;
                    backoff = (backoff * 2).min(Duration::from_secs(30));
                }
            }
        }
    });
}

fn subscribe_daemon_events(sender: mpsc::Sender<UiMessage>) {
    spawn_async(async move {
        let mut backoff = Duration::from_secs(2);
        loop {
            let _ = stream_daemon_events(sender.clone()).await;
            let _ = sender.send(UiMessage::DaemonConnectionLost);
            tokio::time::sleep(backoff).await;
            backoff = (backoff * 2).min(Duration::from_secs(30));
        }
    });
}

fn request_chats(sender: mpsc::Sender<UiMessage>) {
    spawn_async(async move {
        match load_chats().await {
            Ok(chats) => {
                let _ = sender.send(UiMessage::ChatsLoaded(chats));
            }
            Err(error) => {
                let _ = sender.send(UiMessage::ChatsFailed(error.to_string()));
            }
        }
    });
}

fn schedule_conversation_loading(
    sender: mpsc::Sender<UiMessage>,
    chat_id: String,
    generation: u64,
) {
    thread::spawn(move || {
        thread::sleep(Duration::from_secs(1));
        let _ = sender.send(UiMessage::ShowConversationLoading {
            chat_id,
            generation,
        });
    });
}

fn request_messages(sender: mpsc::Sender<UiMessage>, chat_id: String, generation: u64) {
    spawn_async(async move {
        match load_messages(chat_id.clone()).await {
            Ok(messages) => {
                let _ = sender.send(UiMessage::MessagesLoaded {
                    chat_id,
                    generation,
                    messages,
                });
            }
            Err(error) => {
                let _ = sender.send(UiMessage::MessagesFailed {
                    chat_id,
                    generation,
                    error: error.to_string(),
                });
            }
        }
    });
}

fn request_mark_chat_read(sender: mpsc::Sender<UiMessage>, chat_id: String) {
    spawn_async(async move {
        if let Err(error) = mark_chat_read(chat_id).await {
            if is_daemon_transport_error(&*error) {
                let _ = sender.send(UiMessage::DaemonConnectionLost);
                return;
            }
            let _ = sender.send(UiMessage::Notice(format!(
                "Unable to mark chat read: {error}"
            )));
        }
    });
}

fn request_send_text(sender: mpsc::Sender<UiMessage>, chat_id: String, text: String) {
    spawn_async(async move {
        match send_text(chat_id.clone(), text).await {
            Ok(chat_id) => {
                let _ = sender.send(UiMessage::SendSucceeded { chat_id });
            }
            Err(error) => {
                let _ = sender.send(UiMessage::SendFailed {
                    chat_id,
                    error: error.to_string(),
                });
            }
        }
    });
}

fn request_send_media(sender: mpsc::Sender<UiMessage>, chat_id: String, file_path: String) {
    spawn_async(async move {
        match send_media(chat_id.clone(), file_path).await {
            Ok(()) => {
                let _ = sender.send(UiMessage::MediaSendSucceeded);
            }
            Err(error) => {
                let _ = sender.send(UiMessage::MediaSendFailed {
                    chat_id,
                    error: error.to_string(),
                });
            }
        }
    });
}

fn request_set_chat_presence(sender: mpsc::Sender<UiMessage>, chat_id: String, composing: bool) {
    spawn_async(async move {
        if let Err(error) = set_chat_presence(chat_id, composing).await {
            let _ = sender.send(UiMessage::Notice(format!(
                "Unable to set chat presence: {error}"
            )));
        }
    });
}

fn request_logout(sender: mpsc::Sender<UiMessage>) {
    spawn_async(async move {
        match logout().await {
            Ok(()) => {
                let _ = sender.send(UiMessage::LogoutSucceeded);
            }
            Err(error) => {
                let _ = sender.send(UiMessage::LogoutFailed(error.to_string()));
            }
        }
    });
}

fn request_reconnect(sender: mpsc::Sender<UiMessage>) {
    spawn_async(async move {
        match do_reconnect().await {
            Ok(()) => {
                let _ = sender.send(UiMessage::ReconnectSucceeded);
            }
            Err(error) => {
                let _ = sender.send(UiMessage::ReconnectFailed(friendly_daemon_error(&*error)));
            }
        }
    });
}

fn spawn_async<Fut>(future: Fut)
where
    Fut: std::future::Future<Output = ()> + Send + 'static,
{
    thread::spawn(move || {
        let runtime = match tokio::runtime::Builder::new_multi_thread()
            .enable_all()
            .build()
        {
            Ok(runtime) => runtime,
            Err(_) => return,
        };

        runtime.block_on(future);
    });
}

async fn fetch_status() -> Result<proto::GetStatusResponse, Box<dyn std::error::Error + Send + Sync>>
{
    let channel = connect_channel().await?;
    let mut client = DaemonServiceClient::new(channel);
    Ok(client.get_status(GetStatusRequest {}).await?.into_inner())
}

async fn stream_login_events(
    sender: mpsc::Sender<UiMessage>,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = LoginServiceClient::new(channel);
    let mut stream = client
        .subscribe_login_events(SubscribeLoginEventsRequest {})
        .await?
        .into_inner();

    while let Some(event) = stream.message().await? {
        match event.payload {
            Some(login_event::Payload::LoginStateChanged(_)) => {}
            Some(login_event::Payload::QrCode(qr)) => {
                let _ = sender.send(UiMessage::QrCode {
                    code: qr.code,
                    expires_at: qr.expires_at_unix,
                });
            }
            None => {}
        }
    }

    Ok(())
}

async fn stream_frontend_session(
    _sender: mpsc::Sender<UiMessage>,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = FrontendServiceClient::new(channel);
    let mut stream = client
        .hold_session(proto::HoldSessionRequest {
            client_name: "whatevr".to_string(),
        })
        .await?
        .into_inner();

    while stream.message().await?.is_some() {}

    Ok(())
}

async fn stream_daemon_events(
    sender: mpsc::Sender<UiMessage>,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = DaemonServiceClient::new(channel);
    let mut stream = client
        .subscribe_events(SubscribeEventsRequest {})
        .await?
        .into_inner();
    let _ = sender.send(UiMessage::DaemonConnectionRestored);

    while let Some(event) = stream.message().await? {
        match event.payload {
            Some(daemon_event::Payload::ConnectionChanged(change)) => {
                let state = DaemonState::try_from(change.state).unwrap_or(DaemonState::Unspecified);
                let _ = sender.send(UiMessage::DaemonState {
                    state,
                    detail: change.detail,
                    can_reconnect: change.can_reconnect,
                    retry_attempt: change.retry_attempt,
                    next_retry_unix: change.next_retry_unix,
                });
            }
            Some(daemon_event::Payload::NewMessage(new_message)) => {
                if let Some(message) = new_message.message {
                    let _ = sender.send(UiMessage::NewMessage { message });
                }
            }
            Some(daemon_event::Payload::MessageUpdated(message_updated)) => {
                if let Some(message) = message_updated.message {
                    let _ = sender.send(UiMessage::MessageUpdated { message });
                }
            }
            Some(daemon_event::Payload::ChatUpdated(chat_updated)) => {
                let previous_chat_id = if chat_updated.previous_chat_id.is_empty() {
                    None
                } else {
                    Some(chat_updated.previous_chat_id)
                };
                if let Some(chat) = chat_updated.chat {
                    let _ = sender.send(UiMessage::ChatUpdated {
                        chat,
                        previous_chat_id,
                    });
                }
            }
            Some(daemon_event::Payload::ChatPresenceChanged(presence)) => {
                let _ = sender.send(UiMessage::ChatPresence {
                    chat_id: presence.chat_id,
                    is_composing: presence.is_composing,
                });
            }
            _ => {}
        }
    }

    Ok(())
}

async fn load_chats() -> Result<Vec<proto::Chat>, Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = ChatServiceClient::new(channel);
    let response = client
        .list_chats(ListChatsRequest {
            limit: 100,
            offset: 0,
        })
        .await?
        .into_inner();
    Ok(response.chats)
}

async fn load_messages(
    chat_id: String,
) -> Result<Vec<proto::Message>, Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = ChatServiceClient::new(channel);
    let response = client
        .get_messages(GetMessagesRequest {
            chat_id,
            limit: 50,
            before_message_id: String::new(),
        })
        .await?
        .into_inner();
    Ok(response.messages)
}

async fn mark_chat_read(chat_id: String) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = ChatServiceClient::new(channel);
    client
        .mark_chat_read(MarkChatReadRequest { chat_id })
        .await?;
    Ok(())
}

async fn send_text(
    chat_id: String,
    text: String,
) -> Result<String, Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = proto::send_service_client::SendServiceClient::new(channel);
    let request_chat_id = chat_id.clone();
    let response = client
        .send_text(proto::SendTextRequest {
            chat_id: request_chat_id,
            text,
        })
        .await?
        .into_inner();
    Ok(response
        .message
        .map(|message| message.chat_id)
        .filter(|chat_id| !chat_id.is_empty())
        .unwrap_or(chat_id))
}

async fn send_media(
    chat_id: String,
    file_path: String,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = SendServiceClient::new(channel);
    client
        .send_media(SendMediaRequest {
            chat_id,
            file_path,
            caption: String::new(),
        })
        .await?;
    Ok(())
}

async fn set_chat_presence(
    chat_id: String,
    composing: bool,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = ChatServiceClient::new(channel);
    client
        .set_chat_presence(SetChatPresenceRequest { chat_id, composing })
        .await?;
    Ok(())
}

async fn logout() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = LoginServiceClient::new(channel);
    client.logout(LogoutRequest {}).await?;
    Ok(())
}

async fn do_reconnect() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = DaemonServiceClient::new(channel);
    client.reconnect(ReconnectRequest {}).await?;
    Ok(())
}

fn friendly_daemon_error(err: &dyn std::error::Error) -> String {
    friendly_daemon_error_text(&err.to_string())
}

fn friendly_daemon_error_text(s: &str) -> String {
    if s.contains("No such file or directory")
        || s.contains("Connection refused")
        || s.contains("os error 2")
    {
        "whatevrd is not running. Start the daemon and try again.".to_string()
    } else if s.contains("Permission denied") || s.contains("os error 13") {
        "Cannot access the whatevrd socket. Check daemon permissions.".to_string()
    } else if s.contains("Unavailable") || s.contains("deadline exceeded") {
        "whatevrd is not responding. Retrying...".to_string()
    } else if s.contains("FailedPrecondition") {
        "WhatsApp is not logged in. Please scan the QR code.".to_string()
    } else {
        s.to_string()
    }
}

fn is_daemon_transport_error(err: &dyn std::error::Error) -> bool {
    let s = err.to_string();
    s.contains("transport error")
        || s.contains("No such file or directory")
        || s.contains("Connection refused")
        || s.contains("os error 2")
        || s.contains("Unavailable")
}

async fn connect_channel() -> Result<Channel, Box<dyn std::error::Error + Send + Sync>> {
    let socket_path = socket_path()?;
    let endpoint = Endpoint::try_from("http://[::]:50051")?;

    let channel = endpoint
        .connect_with_connector(service_fn(move |_: Uri| {
            let socket_path = socket_path.clone();
            async move {
                let stream = UnixStream::connect(socket_path).await?;
                Ok::<_, std::io::Error>(TokioIo::new(stream))
            }
        }))
        .await?;

    Ok(channel)
}

fn socket_path() -> Result<PathBuf, String> {
    let runtime_dir = std::env::var_os("XDG_RUNTIME_DIR")
        .ok_or_else(|| "XDG_RUNTIME_DIR is not set".to_string())?;

    Ok(PathBuf::from(runtime_dir)
        .join("whatevrd")
        .join("whatevrd.sock"))
}

fn build_chat_row(chat: &proto::Chat) -> gtk::ListBoxRow {
    let row_box = build_chat_row_content(chat);
    let row = gtk::ListBoxRow::builder()
        .activatable(true)
        .selectable(true)
        .build();
    row.set_child(Some(&row_box));
    row
}

fn update_chat_row_at_index(chat_list: &gtk::ListBox, index: usize, chat: &proto::Chat) {
    if let Some(row) = chat_list.row_at_index(index as i32) {
        row.set_child(Some(&build_chat_row_content(chat)));
    }
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

fn build_message_row(message: &proto::Message) -> (gtk::Box, gtk::Label) {
    let outgoing = message.direction == proto::MessageDirection::Outgoing as i32;

    let bubble = gtk::Box::new(gtk::Orientation::Vertical, 4);
    bubble.add_css_class("message-bubble");

    if outgoing {
        bubble.add_css_class("outgoing");
    } else {
        bubble.add_css_class("incoming");
    }

    // Show image if present
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

    // Show text/caption if present
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
        // Fallback: show empty text label so bubble isn't empty
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

fn render_qr_texture(code: &str) -> Result<gdk::MemoryTexture, String> {
    const QUIET_ZONE: usize = 4;
    const SCALE: usize = 6;

    let code = QrCode::new(code.as_bytes()).map_err(|error| error.to_string())?;
    let qr_width = code.width();
    let module_width = qr_width + QUIET_ZONE * 2;
    let image_width = module_width * SCALE;
    let colors = code.to_colors();
    let mut pixels = Vec::with_capacity(image_width * image_width * 4);

    for pixel_y in 0..image_width {
        let module_y = pixel_y / SCALE;
        for pixel_x in 0..image_width {
            let module_x = pixel_x / SCALE;
            let dark = if module_x < QUIET_ZONE
                || module_y < QUIET_ZONE
                || module_x >= qr_width + QUIET_ZONE
                || module_y >= qr_width + QUIET_ZONE
            {
                false
            } else {
                let qr_x = module_x - QUIET_ZONE;
                let qr_y = module_y - QUIET_ZONE;
                colors[qr_y * qr_width + qr_x] == Color::Dark
            };

            if dark {
                pixels.extend_from_slice(&[0, 0, 0, 255]);
            } else {
                pixels.extend_from_slice(&[255, 255, 255, 255]);
            }
        }
    }

    let bytes = glib::Bytes::from_owned(pixels);
    Ok(gdk::MemoryTexture::new(
        image_width as i32,
        image_width as i32,
        gdk::MemoryFormat::R8g8b8a8,
        &bytes,
        image_width * 4,
    ))
}

fn display_chat_name(chat: &proto::Chat) -> &str {
    if chat.name.trim().is_empty() {
        &chat.id
    } else {
        &chat.name
    }
}

fn chat_preview(chat: &proto::Chat) -> String {
    if chat.last_message.trim().is_empty() {
        "No text messages yet".to_string()
    } else {
        chat.last_message
            .split_whitespace()
            .collect::<Vec<_>>()
            .join(" ")
    }
}

fn unread_count_label(count: i32) -> String {
    if count > 99 {
        "99+".to_string()
    } else {
        count.to_string()
    }
}

fn format_login_state(state: DaemonState, detail: &str) -> String {
    if detail.is_empty() {
        return format!("State: {}", state_name(state));
    }

    format!("State: {}\n{}", state_name(state), detail)
}

fn format_qr_expiry(expires_at: i64) -> String {
    match Local.timestamp_opt(expires_at, 0).single() {
        Some(datetime) => format!("Expires at {}", datetime.format("%H:%M")),
        None => "QR expiration unavailable".to_string(),
    }
}

fn format_chat_timestamp(timestamp_unix: i64) -> String {
    let Some(datetime) = Local.timestamp_opt(timestamp_unix, 0).single() else {
        return String::new();
    };
    let now = Local::now();

    if datetime.date_naive() == now.date_naive() {
        datetime.format("%H:%M").to_string()
    } else if now
        .date_naive()
        .signed_duration_since(datetime.date_naive())
        .num_days()
        < 6
    {
        datetime.format("%a").to_string()
    } else {
        datetime.format("%d %b").to_string()
    }
}

fn format_message_meta(message: &proto::Message) -> String {
    let timestamp = Local
        .timestamp_opt(message.timestamp_unix, 0)
        .single()
        .map(|datetime| datetime.format("%H:%M").to_string())
        .unwrap_or_else(|| "--:--".to_string());

    if message.direction == proto::MessageDirection::Outgoing as i32 {
        let status = match proto::MessageStatus::try_from(message.status)
            .unwrap_or(proto::MessageStatus::Unspecified)
        {
            proto::MessageStatus::Sent => "Sent",
            proto::MessageStatus::Delivered => "Delivered",
            proto::MessageStatus::Read => "Read",
            proto::MessageStatus::Failed => "Failed",
            proto::MessageStatus::Pending => "Pending",
            proto::MessageStatus::Unspecified => "",
        };

        if status.is_empty() {
            timestamp
        } else {
            format!("{}  •  {}", timestamp, status)
        }
    } else {
        timestamp
    }
}

fn state_name(state: DaemonState) -> &'static str {
    match state {
        DaemonState::Unspecified => "UNSPECIFIED",
        DaemonState::Starting => "STARTING",
        DaemonState::NeedLogin => "NEED_LOGIN",
        DaemonState::Connecting => "CONNECTING",
        DaemonState::Online => "ONLINE",
        DaemonState::Reconnecting => "RECONNECTING",
        DaemonState::Offline => "OFFLINE",
    }
}

fn scroll_messages_to_bottom(widgets: &Widgets) {
    keep_messages_at_bottom(widgets, false);
}

fn reveal_messages_at_bottom(widgets: &Widgets) {
    widgets.message_scroller.set_opacity(0.0);
    widgets
        .conversation_content_stack
        .set_visible_child_name(CONVERSATION_MESSAGES_PAGE);

    keep_messages_at_bottom(widgets, true);
}

fn keep_messages_at_bottom(widgets: &Widgets, reveal_after_layout: bool) {
    let scroll_generation = widgets.message_scroll_generation.get().wrapping_add(1);
    widgets.message_scroll_generation.set(scroll_generation);

    let adjustment = widgets.message_scroller.vadjustment();
    scroll_adjustment_to_bottom(&adjustment);

    let adjustment_for_tick = adjustment.clone();
    let tick_state = Rc::new(RefCell::new((0usize, 0usize, f64::NAN, f64::NAN, f64::NAN)));
    let tick_state_for_callback = tick_state.clone();
    let generation_for_tick = widgets.message_scroll_generation.clone();
    let message_scroller_for_tick = widgets.message_scroller.clone();
    widgets.message_scroller.add_tick_callback(move |_, _| {
        if generation_for_tick.get() != scroll_generation {
            return glib::ControlFlow::Break;
        }

        scroll_adjustment_to_bottom(&adjustment_for_tick);

        let upper = adjustment_for_tick.upper();
        let page_size = adjustment_for_tick.page_size();
        let value = adjustment_for_tick.value();
        let max = (upper - page_size).max(0.0);
        let mut tick_state = tick_state_for_callback.borrow_mut();
        let count = tick_state.0 + 1;
        let stable_count = if nearly_equal(tick_state.2, upper)
            && nearly_equal(tick_state.3, page_size)
            && nearly_equal(tick_state.4, max)
            && value >= max - 0.5
        {
            tick_state.1 + 1
        } else {
            0
        };
        *tick_state = (count, stable_count, upper, page_size, max);

        if count >= 90 || (count >= 6 && stable_count >= 4) {
            if reveal_after_layout {
                scroll_adjustment_to_bottom(&adjustment_for_tick);
                message_scroller_for_tick.set_opacity(1.0);
            }
            glib::ControlFlow::Break
        } else {
            glib::ControlFlow::Continue
        }
    });

    let adjustment_for_idle = adjustment.clone();
    let generation_for_idle = widgets.message_scroll_generation.clone();
    glib::idle_add_local_once(move || {
        if generation_for_idle.get() != scroll_generation {
            return;
        }

        scroll_adjustment_to_bottom(&adjustment_for_idle);
    });

    if reveal_after_layout {
        let generation_for_timeout = widgets.message_scroll_generation.clone();
        let message_scroller_for_timeout = widgets.message_scroller.clone();
        glib::timeout_add_local_once(Duration::from_secs(4), move || {
            if generation_for_timeout.get() != scroll_generation {
                return;
            }

            message_scroller_for_timeout.set_opacity(1.0);
        });
    }
}

fn nearly_equal(left: f64, right: f64) -> bool {
    if left.is_nan() || right.is_nan() {
        false
    } else {
        (left - right).abs() <= 0.5
    }
}

fn scroll_adjustment_to_bottom(adjustment: &gtk::Adjustment) {
    let max = adjustment.upper() - adjustment.page_size();
    adjustment.set_value(max.max(0.0));
}

fn is_scroller_near_bottom(widgets: &Widgets) -> bool {
    let adjustment = widgets.message_scroller.vadjustment();
    let max = adjustment.upper() - adjustment.page_size();
    if max <= 0.0 {
        return true;
    }
    adjustment.value() >= max - 40.0
}

fn clear_list_box(list_box: &gtk::ListBox) {
    while let Some(child) = list_box.first_child() {
        list_box.remove(&child);
    }
}

fn install_css() {
    let provider = gtk::CssProvider::new();
    provider.load_from_data(
        r#"
        .message-bubble {
            color: @window_fg_color;
            border-radius: 12px;
            padding: 8px 12px;
            border: 1px solid alpha(@window_fg_color, 0.12);
        }

        .message-bubble.incoming {
            background-color: @card_bg_color;
            border-color: alpha(@window_fg_color, 0.16);
            border-bottom-left-radius: 5px;
        }

        .message-bubble.outgoing {
            background-color: alpha(@accent_bg_color, 0.18);
            border-color: alpha(@accent_bg_color, 0.28);
            border-bottom-right-radius: 5px;
        }

        label.message-text {
            background-color: transparent;
            color: inherit;
            padding: 0;
        }

        .composer-input {
            border-radius: 16px;
            border: 1px solid alpha(@window_fg_color, 0.15);
            background-color: @card_bg_color;
        }

        .composer-input > * {
            border-radius: inherit;
            background-color: transparent;
        }

        .unread-badge {
            background-color: @accent_bg_color;
            color: @accent_fg_color;
            border-radius: 999px;
            font-weight: 700;
            padding: 2px 8px;
            min-width: 26px;
        }

        picture.image-placeholder {
            background-color: alpha(@window_fg_color, 0.06);
            border-radius: 8px;
        }

        .conversation-header-name {
            color: alpha(@window_fg_color, 0.82);
            font-weight: 700;
        }
    "#,
    );

    if let Some(display) = gdk::Display::default() {
        gtk::style_context_add_provider_for_display(
            &display,
            &provider,
            gtk::STYLE_PROVIDER_PRIORITY_APPLICATION,
        );
    }
}
