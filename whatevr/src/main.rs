use adw::prelude::*;
use gtk::glib;
use http::Uri;
use hyper_util::rt::TokioIo;
use qrcode::{QrCode, types::Color};
use std::path::PathBuf;
use std::time::Duration;
use tokio::net::UnixStream;
use tonic::transport::{Channel, Endpoint};
use tower::service_fn;

mod proto {
    tonic::include_proto!("whatevr.v1");
}

use proto::daemon_service_client::DaemonServiceClient;
use proto::login_event;
use proto::login_service_client::LoginServiceClient;
use proto::{DaemonState, GetStatusRequest, SubscribeLoginEventsRequest};

const APP_ID: &str = "in.codelif.Whatevr";

enum UiMessage {
    Status(String),
    LoginState { state: DaemonState, detail: String },
    QrCode { code: String, expires_at: i64 },
}

fn main() -> glib::ExitCode {
    adw::init().expect("failed to initialize libadwaita");

    let app = adw::Application::builder().application_id(APP_ID).build();
    app.connect_activate(build_ui);
    app.run()
}

fn build_ui(app: &adw::Application) {
    let status_label = gtk::Label::builder()
        .label("Connecting to whatevrd...")
        .wrap(true)
        .selectable(true)
        .xalign(0.0)
        .build();

    let login_status_label = gtk::Label::builder()
        .label("Waiting for login state...")
        .wrap(true)
        .xalign(0.0)
        .build();

    let qr_picture = gtk::Picture::builder()
        .halign(gtk::Align::Center)
        .visible(false)
        .build();

    let qr_error_label = gtk::Label::builder().wrap(true).xalign(0.0).build();

    let qr_expiry_label = gtk::Label::builder()
        .css_classes(["dim-label"])
        .xalign(0.0)
        .build();

    let qr_box = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(12)
        .visible(false)
        .build();
    qr_box.append(&login_status_label);
    qr_box.append(&qr_picture);
    qr_box.append(&qr_error_label);
    qr_box.append(&qr_expiry_label);

    let refresh_button = gtk::Button::builder()
        .icon_name("view-refresh-symbolic")
        .tooltip_text("Refresh daemon status")
        .build();

    let header = adw::HeaderBar::new();
    header.pack_end(&refresh_button);

    let title = gtk::Label::builder()
        .label("whatevr")
        .css_classes(["title-1"])
        .xalign(0.0)
        .build();

    let subtitle = gtk::Label::builder()
        .label("Official GTK frontend for whatevrd")
        .css_classes(["dim-label"])
        .xalign(0.0)
        .build();

    let content = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .spacing(16)
        .margin_top(24)
        .margin_bottom(24)
        .margin_start(24)
        .margin_end(24)
        .build();
    content.append(&title);
    content.append(&subtitle);
    content.append(&status_label);
    content.append(&qr_box);

    let clamp = adw::Clamp::builder().maximum_size(760).build();
    clamp.set_child(Some(&content));

    let root = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .build();
    root.append(&header);
    root.append(&clamp);

    let window = adw::ApplicationWindow::builder()
        .application(app)
        .title("whatevr")
        .default_width(620)
        .default_height(620)
        .content(&root)
        .build();

    let (sender, receiver) = async_channel::unbounded::<UiMessage>();
    request_status(sender.clone());
    subscribe_login_events(sender.clone());

    refresh_button.connect_clicked(move |_| {
        request_status(sender.clone());
    });

    glib::timeout_add_local(Duration::from_millis(150), move || {
        while let Ok(message) = receiver.try_recv() {
            match message {
                UiMessage::Status(message) => status_label.set_text(&message),
                UiMessage::LoginState { state, detail } => {
                    login_status_label.set_text(&format_login_state(state, &detail));
                    qr_box.set_visible(matches!(state, DaemonState::NeedLogin));
                    if !matches!(state, DaemonState::NeedLogin) {
                        qr_picture.set_visible(false);
                        qr_error_label.set_text("");
                        qr_expiry_label.set_text("");
                    }
                }
                UiMessage::QrCode { code, expires_at } => match render_qr_texture(&code) {
                    Ok(texture) => {
                        qr_box.set_visible(true);
                        qr_picture.set_paintable(Some(&texture));
                        qr_picture.set_visible(true);
                        qr_error_label.set_text("");
                        qr_expiry_label.set_text(&format!("QR expires at Unix time {expires_at}"));
                    }
                    Err(err) => {
                        qr_picture.set_visible(false);
                        qr_error_label.set_text(&format!("Unable to render QR code: {err}"));
                    }
                },
            }
        }

        glib::ControlFlow::Continue
    });

    window.present();
}

fn request_status(sender: async_channel::Sender<UiMessage>) {
    std::thread::spawn(move || {
        let message = match tokio::runtime::Builder::new_multi_thread()
            .enable_all()
            .build()
        {
            Ok(runtime) => runtime
                .block_on(fetch_daemon_status())
                .unwrap_or_else(|err| format!("Unable to reach whatevrd:\n{err}")),
            Err(err) => format!("Unable to create async runtime:\n{err}"),
        };

        let _ = sender.send_blocking(UiMessage::Status(message));
    });
}

fn subscribe_login_events(sender: async_channel::Sender<UiMessage>) {
    std::thread::spawn(move || {
        let result = match tokio::runtime::Builder::new_multi_thread()
            .enable_all()
            .build()
        {
            Ok(runtime) => runtime.block_on(stream_login_events(sender.clone())),
            Err(err) => Err(format!("Unable to create async runtime:\n{err}").into()),
        };

        if let Err(err) = result {
            let _ = sender.send_blocking(UiMessage::Status(format!(
                "Unable to subscribe to login events:\n{err}"
            )));
        }
    });
}

async fn fetch_daemon_status() -> Result<String, Box<dyn std::error::Error + Send + Sync>> {
    let display_socket_path = socket_path()?.display().to_string();
    let channel = connect_channel().await?;
    let mut client = DaemonServiceClient::new(channel);
    let response = client.get_status(GetStatusRequest {}).await?.into_inner();
    let state = DaemonState::try_from(response.state).unwrap_or(DaemonState::Unspecified);

    Ok(format!(
        "State: {} ({})\nSocket: {}\nDatabase: {}\nData: {}\nCache: {}\nVersion: {}",
        response.state_label,
        state_name(state),
        if response.socket_path.is_empty() {
            display_socket_path
        } else {
            response.socket_path
        },
        response.database_path,
        response.data_dir,
        response.cache_dir,
        response.version,
    ))
}

async fn stream_login_events(
    sender: async_channel::Sender<UiMessage>,
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
                let _ = sender
                    .send(UiMessage::LoginState {
                        state,
                        detail: change.detail,
                    })
                    .await;
            }
            Some(login_event::Payload::QrCode(qr)) => {
                let _ = sender
                    .send(UiMessage::QrCode {
                        code: qr.code,
                        expires_at: qr.expires_at_unix,
                    })
                    .await;
            }
            None => {}
        }
    }

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

fn render_qr_texture(code: &str) -> Result<gtk::gdk::MemoryTexture, String> {
    const QUIET_ZONE: usize = 4;
    const SCALE: usize = 6;

    let code = QrCode::new(code.as_bytes()).map_err(|err| err.to_string())?;
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
    Ok(gtk::gdk::MemoryTexture::new(
        image_width as i32,
        image_width as i32,
        gtk::gdk::MemoryFormat::R8g8b8a8,
        &bytes,
        image_width * 4,
    ))
}

fn format_login_state(state: DaemonState, detail: &str) -> String {
    if detail.is_empty() {
        return format!("Login state: {}", state_name(state));
    }

    format!("Login state: {}\n{detail}", state_name(state))
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
