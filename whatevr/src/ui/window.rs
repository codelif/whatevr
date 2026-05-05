use std::{
    cell::{Cell, RefCell},
    rc::Rc,
};

use adw::prelude::*;
use gtk::{glib, pango};

use crate::config::*;
use crate::ui::{
    commands::{
        report_frontend_session_state, request_mark_chat_read, request_reconnect, request_status,
        stop_current_composing,
    },
    context::AppContext,
    controller::handle_ui_message,
    lifecycle::schedule_bootstrap_after_first_frame,
    message::UiMessage,
    receiver, signals,
    state::UiState,
    widgets::Widgets,
};

fn set_accessible_label(widget: &impl IsA<gtk::Accessible>, label: &str) {
    widget.update_property(&[gtk::accessible::Property::Label(label)]);
}

pub fn build(app: &adw::Application) -> AppContext {
    let refresh_button = gtk::Button::builder()
        .icon_name("view-refresh-symbolic")
        .tooltip_text("Refresh chats and connection state")
        .build();
    set_accessible_label(&refresh_button, "Refresh chats and connection state");
    let logout_button = gtk::Button::builder()
        .icon_name("system-log-out-symbolic")
        .tooltip_text("Log out and delete local session data")
        .build();
    set_accessible_label(&logout_button, "Log out and delete local session data");
    let back_button = gtk::Button::builder()
        .icon_name("go-previous-symbolic")
        .tooltip_text("Back to chats")
        .visible(false)
        .build();
    set_accessible_label(&back_button, "Back to chats");

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
    set_accessible_label(&qr_picture, "WhatsApp login QR code");
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
    set_accessible_label(&composer_send_button, "Send message");
    let composer_attach_button = gtk::Button::builder()
        .icon_name("mail-attachment-symbolic")
        .tooltip_text("Attach image")
        .css_classes(["flat", "circular"])
        .valign(gtk::Align::End)
        .build();
    set_accessible_label(&composer_attach_button, "Attach image");
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
    root_stack.set_visible_child_name(ROOT_MAIN_PAGE);

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
    let (sender, receiver) = async_channel::unbounded::<UiMessage>();

    signals::connect(&widgets, &state, &sender, &refresh_button);

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
            if !focused {
                stop_current_composing(&mut s, &focus_sender);
            }
            s.selected_chat_id.clone()
        };
        report_frontend_session_state(&focus_state, focus_sender.clone());
        if focused && !focus_state.borrow().daemon_disconnected {
            if let Some(chat_id) = selected {
                request_mark_chat_read(focus_sender.clone(), chat_id);
            }
        }
    });

    receiver::attach(
        receiver,
        widgets.clone(),
        state.clone(),
        sender.clone(),
        handle_ui_message,
    );

    let close_app = app.clone();
    window.connect_close_request(move |_| {
        close_app.quit();
        glib::Propagation::Proceed
    });

    window.present();
    schedule_bootstrap_after_first_frame(&window, sender.clone());

    AppContext { widgets, sender }
}
