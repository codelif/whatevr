use std::{
    cell::{Cell, RefCell},
    path::PathBuf,
    rc::Rc,
    sync::mpsc,
    thread,
    time::Duration,
};

use adw::prelude::*;
use chrono::{Local, TimeZone};
use gtk::{gdk, glib, pango};
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
use proto::login_event;
use proto::login_service_client::LoginServiceClient;
use proto::{
    DaemonState, GetMessagesRequest, GetStatusRequest, ListChatsRequest, MarkChatReadRequest,
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

#[derive(Clone)]
enum UiMessage {
    StatusLoaded(proto::GetStatusResponse),
    StatusFailed(String),
    DaemonState {
        state: DaemonState,
        detail: String,
    },
    QrCode {
        code: String,
        expires_at: i64,
    },
    ChatsLoaded(Vec<proto::Chat>),
    ChatsFailed(String),
    MessagesLoaded {
        chat_id: String,
        messages: Vec<proto::Message>,
    },
    MessagesFailed {
        chat_id: String,
        error: String,
    },
    SendSucceeded {
        chat_id: String,
    },
    SendFailed {
        chat_id: String,
        error: String,
    },
    DataChanged {
        chat_id: Option<String>,
        refresh_chats: bool,
    },
    Notice(String),
}

struct UiState {
    daemon_state: DaemonState,
    daemon_detail: String,
    chats: Vec<proto::Chat>,
    chats_loaded: bool,
    initial_chat_request_started: bool,
    selected_chat_id: Option<String>,
    current_messages_chat_id: Option<String>,
    current_messages: Vec<proto::Message>,
    composer_error: String,
    send_in_flight: bool,
}

impl Default for UiState {
    fn default() -> Self {
        Self {
            daemon_state: DaemonState::Starting,
            daemon_detail: String::new(),
            chats: Vec::new(),
            chats_loaded: false,
            initial_chat_request_started: false,
            selected_chat_id: None,
            current_messages_chat_id: None,
            current_messages: Vec::new(),
            composer_error: String::new(),
            send_in_flight: false,
        }
    }
}

#[derive(Clone)]
struct Widgets {
    root_stack: gtk::Stack,
    loading_page: adw::StatusPage,
    login_status_label: gtk::Label,
    qr_picture: gtk::Picture,
    qr_error_label: gtk::Label,
    qr_expiry_label: gtk::Label,
    window_title: adw::WindowTitle,
    banner: adw::Banner,
    split_view: adw::NavigationSplitView,
    sidebar_stack: gtk::Stack,
    sidebar_loading_page: adw::StatusPage,
    sidebar_empty_page: adw::StatusPage,
    chat_list: gtk::ListBox,
    conversation_stack: gtk::Stack,
    conversation_content_stack: gtk::Stack,
    conversation_loading_page: adw::StatusPage,
    conversation_avatar: adw::Avatar,
    conversation_title: gtk::Label,
    conversation_subtitle: gtk::Label,
    message_scroller: gtk::ScrolledWindow,
    message_box: gtk::Box,
    composer_text_view: gtk::TextView,
    composer_error_label: gtk::Label,
    composer_send_button: gtk::Button,
    back_button: gtk::Button,
    syncing_chat_selection: Cell<bool>,
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
    let back_button = gtk::Button::builder()
        .icon_name("go-previous-symbolic")
        .tooltip_text("Back to chats")
        .visible(false)
        .build();

    let window_title = adw::WindowTitle::new("whatevr", "Starting");
    let header = adw::HeaderBar::new();
    header.pack_start(&back_button);
    header.set_title_widget(Some(&window_title));
    header.pack_end(&refresh_button);

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
    loading_page.set_child(Some(&loading_spinner));

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

    let conversation_avatar = adw::Avatar::new(40, Some(""), true);
    let conversation_title = gtk::Label::builder()
        .xalign(0.0)
        .css_classes(["title-4"])
        .ellipsize(pango::EllipsizeMode::End)
        .build();
    let conversation_subtitle = gtk::Label::builder()
        .xalign(0.0)
        .css_classes(["dim-label"])
        .ellipsize(pango::EllipsizeMode::End)
        .build();
    let conversation_title_box = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(2)
        .build();
    conversation_title_box.append(&conversation_title);
    conversation_title_box.append(&conversation_subtitle);
    let conversation_header = gtk::Box::builder()
        .orientation(gtk::Orientation::Horizontal)
        .spacing(12)
        .margin_top(18)
        .margin_bottom(18)
        .margin_start(18)
        .margin_end(18)
        .build();
    conversation_header.append(&conversation_avatar);
    conversation_header.append(&conversation_title_box);
    let header_separator = gtk::Separator::builder()
        .orientation(gtk::Orientation::Horizontal)
        .build();
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
    composer_text_view.set_top_margin(10);
    composer_text_view.set_bottom_margin(10);
    composer_text_view.set_left_margin(12);
    composer_text_view.set_right_margin(12);
    composer_text_view.set_accepts_tab(false);
    let composer_scroller = gtk::ScrolledWindow::builder()
        .child(&composer_text_view)
        .hscrollbar_policy(gtk::PolicyType::Never)
        .min_content_height(68)
        .max_content_height(140)
        .build();
    let composer_frame = gtk::Frame::builder()
        .child(&composer_scroller)
        .hexpand(true)
        .css_classes(["composer-frame"])
        .build();
    let composer_send_button = gtk::Button::builder()
        .icon_name("mail-send-symbolic")
        .tooltip_text("Send message")
        .css_classes(["suggested-action"])
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

    let conversation_separator = gtk::Separator::builder()
        .orientation(gtk::Orientation::Horizontal)
        .build();
    let selected_conversation_shell = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .vexpand(true)
        .hexpand(true)
        .build();
    selected_conversation_shell.append(&conversation_header);
    selected_conversation_shell.append(&header_separator);
    selected_conversation_shell.append(&conversation_content_stack);
    selected_conversation_shell.append(&conversation_separator);
    selected_conversation_shell.append(&composer_box);

    let conversation_stack = gtk::Stack::builder().vexpand(true).hexpand(true).build();
    conversation_stack.add_named(
        &conversation_placeholder_page,
        Some(CONVERSATION_PLACEHOLDER_PAGE),
    );
    conversation_stack.add_named(&selected_conversation_shell, Some("selected"));
    conversation_stack.set_visible_child_name(CONVERSATION_PLACEHOLDER_PAGE);

    let sidebar_page = adw::NavigationPage::new(&sidebar_stack, "Chats");
    let content_page = adw::NavigationPage::new(&conversation_stack, "Conversation");
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

    let toolbar_view = adw::ToolbarView::new();
    toolbar_view.add_top_bar(&header);
    toolbar_view.set_content(Some(&root_stack));

    let window = adw::ApplicationWindow::builder()
        .application(app)
        .title("whatevr")
        .default_width(1080)
        .default_height(760)
        .content(&toolbar_view)
        .build();

    let widgets = Rc::new(Widgets {
        root_stack,
        loading_page,
        login_status_label,
        qr_picture,
        qr_error_label,
        qr_expiry_label,
        window_title,
        banner,
        split_view,
        sidebar_stack,
        sidebar_loading_page,
        sidebar_empty_page,
        chat_list,
        conversation_stack,
        conversation_content_stack,
        conversation_loading_page,
        conversation_avatar,
        conversation_title,
        conversation_subtitle,
        message_scroller,
        message_box,
        composer_text_view,
        composer_error_label,
        composer_send_button,
        back_button,
        syncing_chat_selection: Cell::new(false),
    });

    let state = Rc::new(RefCell::new(UiState::default()));
    let (sender, receiver) = mpsc::channel::<UiMessage>();

    connect_signals(&widgets, &state, &sender, &refresh_button);

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

        if let Some(chat_id) = refresh_state.borrow().selected_chat_id.clone() {
            request_messages(refresh_sender.clone(), chat_id);
        }
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
            apply_daemon_state(widgets, state, sender, daemon_state, String::new());
        }
        UiMessage::StatusFailed(error) => {
            widgets.loading_page.set_title("Unable to reach whatevrd");
            widgets.loading_page.set_description(Some(&error));
            widgets.root_stack.set_visible_child_name(ROOT_LOADING_PAGE);
        }
        UiMessage::DaemonState {
            state: daemon_state,
            detail,
        } => {
            apply_daemon_state(widgets, state, sender, daemon_state, detail);
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
        UiMessage::MessagesLoaded { chat_id, messages } => {
            {
                let mut state = state.borrow_mut();
                if state.selected_chat_id.as_deref() != Some(chat_id.as_str()) {
                    return;
                }

                state.current_messages_chat_id = Some(chat_id);
                state.current_messages = messages;
            }

            let state = state.borrow();
            render_conversation(widgets, &state);
            scroll_messages_to_bottom(widgets);
        }
        UiMessage::MessagesFailed { chat_id, error } => {
            let state = state.borrow();
            if state.selected_chat_id.as_deref() != Some(chat_id.as_str()) {
                return;
            }

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
        UiMessage::SendSucceeded { chat_id } => {
            let should_clear_composer = {
                let state = state.borrow();
                state.selected_chat_id.as_deref() == Some(chat_id.as_str())
            };

            {
                let mut state = state.borrow_mut();
                state.send_in_flight = false;
                if should_clear_composer {
                    state.composer_error.clear();
                }
            }

            if should_clear_composer {
                clear_composer(&widgets.composer_text_view);
            }
            request_chats(sender.clone());
            if should_clear_composer {
                request_messages(sender.clone(), chat_id);
            }

            let state = state.borrow();
            render_conversation(widgets, &state);
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
        UiMessage::DataChanged {
            chat_id,
            refresh_chats,
        } => {
            if refresh_chats {
                request_chats(sender.clone());
            }
            if let Some(selected_chat_id) = state.borrow().selected_chat_id.clone() {
                if chat_id.as_deref().is_none()
                    || chat_id.as_deref() == Some(selected_chat_id.as_str())
                {
                    request_messages(sender.clone(), selected_chat_id);
                }
            }
        }
        UiMessage::Notice(text) => {
            show_banner_notice(widgets, &text);
        }
    }
}

fn apply_daemon_state(
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &mpsc::Sender<UiMessage>,
    daemon_state: DaemonState,
    detail: String,
) {
    let mut should_request_chats = false;

    {
        let mut state = state.borrow_mut();
        state.daemon_state = daemon_state;
        state.daemon_detail = detail.clone();
        if matches!(daemon_state, DaemonState::NeedLogin) {
            state.initial_chat_request_started = false;
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
    widgets
        .window_title
        .set_subtitle(window_subtitle(daemon_state));
    update_banner(widgets, daemon_state, &detail);
}

fn update_banner(widgets: &Widgets, daemon_state: DaemonState, detail: &str) {
    match daemon_state {
        DaemonState::Online => widgets.banner.set_revealed(false),
        DaemonState::Connecting => {
            widgets.banner.set_title(if detail.is_empty() {
                "Connecting to WhatsApp"
            } else {
                detail
            });
            widgets.banner.set_revealed(true);
        }
        DaemonState::Reconnecting => {
            widgets.banner.set_title(if detail.is_empty() {
                "Reconnecting to WhatsApp"
            } else {
                detail
            });
            widgets.banner.set_revealed(true);
        }
        DaemonState::Offline => {
            widgets.banner.set_title(if detail.is_empty() {
                "Offline. Local chats remain available."
            } else {
                detail
            });
            widgets.banner.set_revealed(true);
        }
        _ => widgets.banner.set_revealed(false),
    }
}

fn show_banner_notice(widgets: &Widgets, text: &str) {
    widgets.banner.set_title(text);
    widgets.banner.set_revealed(true);
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

    let mut selected_index = None;
    for (index, chat) in state.chats.iter().enumerate() {
        let row = build_chat_row(chat);
        widgets.chat_list.append(&row);

        if state.selected_chat_id.as_deref() == Some(chat.id.as_str()) {
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

fn render_conversation(widgets: &Widgets, state: &UiState) {
    let Some(selected_chat_id) = state.selected_chat_id.as_deref() else {
        widgets
            .conversation_stack
            .set_visible_child_name(CONVERSATION_PLACEHOLDER_PAGE);
        render_composer_state(widgets, state);
        return;
    };

    let Some(chat) = state.chats.iter().find(|chat| chat.id == selected_chat_id) else {
        widgets
            .conversation_stack
            .set_visible_child_name(CONVERSATION_PLACEHOLDER_PAGE);
        render_composer_state(widgets, state);
        return;
    };

    widgets
        .conversation_stack
        .set_visible_child_name("selected");

    widgets
        .conversation_avatar
        .set_text(Some(display_chat_name(chat)));
    widgets.conversation_title.set_text(display_chat_name(chat));
    widgets.conversation_subtitle.set_text(if chat.is_group {
        "Group chat"
    } else {
        "Direct chat"
    });

    if state.current_messages_chat_id.as_deref() != Some(selected_chat_id) {
        widgets
            .conversation_loading_page
            .set_title("Loading messages");
        widgets
            .conversation_loading_page
            .set_description(Some("Reading the latest 50 messages from the local store."));
        widgets
            .conversation_content_stack
            .set_visible_child_name(CONVERSATION_LOADING_PAGE);
        render_composer_state(widgets, state);
        return;
    }

    if state.current_messages.is_empty() {
        widgets
            .conversation_content_stack
            .set_visible_child_name(CONVERSATION_EMPTY_PAGE);
        render_composer_state(widgets, state);
        return;
    }

    clear_box(&widgets.message_box);
    for message in &state.current_messages {
        widgets.message_box.append(&build_message_row(message));
    }

    widgets
        .conversation_content_stack
        .set_visible_child_name(CONVERSATION_MESSAGES_PAGE);
    render_composer_state(widgets, state);
}

fn open_chat_at_index(
    index: usize,
    force_reload: bool,
    widgets: &Rc<Widgets>,
    state: &Rc<RefCell<UiState>>,
    sender: &mpsc::Sender<UiMessage>,
) {
    let mut should_request = true;

    let chat = {
        let state = state.borrow();
        state.chats.get(index).cloned()
    };

    let Some(chat) = chat else {
        return;
    };

    let chat_id = chat.id.clone();

    {
        let mut state = state.borrow_mut();
        let already_selected = state.selected_chat_id.as_deref() == Some(chat_id.as_str());
        state.selected_chat_id = Some(chat_id.clone());
        state.composer_error.clear();

        if !already_selected {
            state.current_messages_chat_id = None;
            state.current_messages.clear();
            clear_composer(&widgets.composer_text_view);
        }

        if already_selected
            && state.current_messages_chat_id.as_deref() == Some(chat_id.as_str())
            && !force_reload
        {
            should_request = false;
        }
    }

    {
        let state = state.borrow();
        render_conversation(widgets, &state);
        update_navigation_state(widgets, &state);
    }

    if !should_request {
        return;
    }

    request_messages(sender.clone(), chat_id.clone());
    request_mark_chat_read(sender.clone(), chat_id);
}

fn sync_selected_chat_after_reload(state: &mut UiState) {
    if let Some(selected_chat_id) = state.selected_chat_id.clone() {
        if !state.chats.iter().any(|chat| chat.id == selected_chat_id) {
            state.selected_chat_id = None;
            state.current_messages_chat_id = None;
            state.current_messages.clear();
            state.composer_error.clear();
            state.send_in_flight = false;
        }
    }
}

fn render_composer_state(widgets: &Widgets, state: &UiState) {
    let has_selected_chat = state.selected_chat_id.is_some();
    let enabled =
        has_selected_chat && state.daemon_state == DaemonState::Online && !state.send_in_flight;

    widgets.composer_text_view.set_sensitive(enabled);
    widgets.composer_send_button.set_sensitive(enabled);
    widgets.composer_text_view.set_visible(has_selected_chat);
    widgets.composer_send_button.set_visible(has_selected_chat);

    if state.composer_error.is_empty() || !has_selected_chat {
        widgets.composer_error_label.set_visible(false);
        widgets.composer_error_label.set_text("");
    } else {
        widgets.composer_error_label.set_visible(true);
        widgets.composer_error_label.set_text(&state.composer_error);
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
        if state.daemon_state != DaemonState::Online || state.send_in_flight {
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
        if let Err(error) = stream_login_events(sender.clone()).await {
            let _ = sender.send(UiMessage::Notice(format!("Login stream ended: {error}")));
        }
    });
}

fn subscribe_daemon_events(sender: mpsc::Sender<UiMessage>) {
    spawn_async(async move {
        if let Err(error) = stream_daemon_events(sender.clone()).await {
            let _ = sender.send(UiMessage::Notice(format!("Event stream ended: {error}")));
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

fn request_messages(sender: mpsc::Sender<UiMessage>, chat_id: String) {
    spawn_async(async move {
        match load_messages(chat_id.clone()).await {
            Ok(messages) => {
                let _ = sender.send(UiMessage::MessagesLoaded { chat_id, messages });
            }
            Err(error) => {
                let _ = sender.send(UiMessage::MessagesFailed {
                    chat_id,
                    error: error.to_string(),
                });
            }
        }
    });
}

fn request_mark_chat_read(sender: mpsc::Sender<UiMessage>, chat_id: String) {
    spawn_async(async move {
        if let Err(error) = mark_chat_read(chat_id).await {
            let _ = sender.send(UiMessage::Notice(format!(
                "Unable to mark chat read: {error}"
            )));
        }
    });
}

fn request_send_text(sender: mpsc::Sender<UiMessage>, chat_id: String, text: String) {
    spawn_async(async move {
        match send_text(chat_id.clone(), text).await {
            Ok(()) => {
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
            Some(login_event::Payload::LoginStateChanged(change)) => {
                let state = DaemonState::try_from(change.state).unwrap_or(DaemonState::Unspecified);
                let _ = sender.send(UiMessage::DaemonState {
                    state,
                    detail: change.detail,
                });
            }
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

async fn stream_daemon_events(
    sender: mpsc::Sender<UiMessage>,
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = DaemonServiceClient::new(channel);
    let mut stream = client
        .subscribe_events(SubscribeEventsRequest {})
        .await?
        .into_inner();

    while let Some(event) = stream.message().await? {
        match event.payload {
            Some(daemon_event::Payload::ConnectionChanged(change)) => {
                let state = DaemonState::try_from(change.state).unwrap_or(DaemonState::Unspecified);
                let _ = sender.send(UiMessage::DaemonState {
                    state,
                    detail: change.detail,
                });
            }
            Some(daemon_event::Payload::NewMessage(new_message)) => {
                let chat_id = new_message.message.map(|message| message.chat_id);
                let _ = sender.send(UiMessage::DataChanged {
                    chat_id,
                    refresh_chats: true,
                });
            }
            Some(daemon_event::Payload::MessageUpdated(message_updated)) => {
                let chat_id = message_updated.message.map(|message| message.chat_id);
                let _ = sender.send(UiMessage::DataChanged {
                    chat_id,
                    refresh_chats: false,
                });
            }
            Some(daemon_event::Payload::ChatUpdated(chat_updated)) => {
                let chat_id = chat_updated.chat.map(|chat| chat.id);
                let _ = sender.send(UiMessage::DataChanged {
                    chat_id,
                    refresh_chats: true,
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
) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    let channel = connect_channel().await?;
    let mut client = proto::send_service_client::SendServiceClient::new(channel);
    client
        .send_text(proto::SendTextRequest { chat_id, text })
        .await?;
    Ok(())
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
    let avatar = adw::Avatar::new(36, Some(display_chat_name(chat)), true);
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

    let row = gtk::ListBoxRow::builder()
        .activatable(true)
        .selectable(true)
        .build();
    row.set_child(Some(&row_box));
    row
}

fn build_message_row(message: &proto::Message) -> gtk::Box {
    let outgoing = message.direction == proto::MessageDirection::Outgoing as i32;

    let message_label = gtk::Label::new(Some(message.text.as_str()));
    message_label.set_xalign(0.0);
    message_label.set_wrap(true);
    message_label.set_wrap_mode(pango::WrapMode::WordChar);
    message_label.set_max_width_chars(62);
    message_label.set_selectable(true);
    message_label.add_css_class("message-text");

    let bubble = gtk::Box::new(gtk::Orientation::Vertical, 0);
    bubble.add_css_class("message-bubble");

    if outgoing {
        bubble.add_css_class("outgoing");
    } else {
        bubble.add_css_class("incoming");
    }

    bubble.append(&message_label);

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

    row
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

fn chat_preview(chat: &proto::Chat) -> &str {
    if chat.last_message.trim().is_empty() {
        "No text messages yet"
    } else {
        &chat.last_message
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

fn window_subtitle(state: DaemonState) -> &'static str {
    match state {
        DaemonState::Starting | DaemonState::Unspecified => "Starting",
        DaemonState::NeedLogin => "Waiting for sign in",
        DaemonState::Connecting => "Connecting",
        DaemonState::Online => "Ready",
        DaemonState::Reconnecting => "Reconnecting",
        DaemonState::Offline => "Offline",
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
    let scroller = widgets.message_scroller.clone();
    glib::idle_add_local_once(move || {
        let adjustment = scroller.vadjustment();
        adjustment.set_value(adjustment.upper() - adjustment.page_size());
    });
}

fn clear_box(container: &gtk::Box) {
    while let Some(child) = container.first_child() {
        container.remove(&child);
    }
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

        .composer-frame {
            border-radius: 16px;
        }

        .unread-badge {
            background-color: @accent_bg_color;
            color: @accent_fg_color;
            border-radius: 999px;
            font-weight: 700;
            padding: 2px 8px;
            min-width: 26px;
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
