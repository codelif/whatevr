use adw::prelude::*;
use gtk::glib;
use http::Uri;
use hyper_util::rt::TokioIo;
use std::path::PathBuf;
use std::time::Duration;
use tokio::net::UnixStream;
use tonic::transport::Endpoint;
use tower::service_fn;

mod proto {
    tonic::include_proto!("whatevr.v1");
}

use proto::daemon_service_client::DaemonServiceClient;
use proto::{DaemonState, GetStatusRequest};

const APP_ID: &str = "in.codelif.Whatevr";

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
        .spacing(12)
        .margin_top(24)
        .margin_bottom(24)
        .margin_start(24)
        .margin_end(24)
        .build();
    content.append(&title);
    content.append(&subtitle);
    content.append(&status_label);

    let clamp = adw::Clamp::builder().maximum_size(720).build();
    clamp.set_child(Some(&content));

    let root = gtk::Box::builder()
        .orientation(gtk::Orientation::Vertical)
        .build();
    root.append(&header);
    root.append(&clamp);

    let window = adw::ApplicationWindow::builder()
        .application(app)
        .title("whatevr")
        .default_width(520)
        .default_height(320)
        .content(&root)
        .build();

    let (sender, receiver) = async_channel::unbounded::<String>();
    request_status(sender.clone());

    refresh_button.connect_clicked(move |_| {
        request_status(sender.clone());
    });

    glib::timeout_add_local(Duration::from_millis(150), move || {
        while let Ok(message) = receiver.try_recv() {
            status_label.set_text(&message);
        }

        glib::ControlFlow::Continue
    });

    window.present();
}

fn request_status(sender: async_channel::Sender<String>) {
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

        let _ = sender.send_blocking(message);
    });
}

async fn fetch_daemon_status() -> Result<String, Box<dyn std::error::Error + Send + Sync>> {
    let socket_path = socket_path()?;
    let display_socket_path = socket_path.display().to_string();
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

    let mut client = DaemonServiceClient::new(channel);
    let response = client.get_status(GetStatusRequest {}).await?.into_inner();
    let state = DaemonState::try_from(response.state).unwrap_or(DaemonState::Unspecified);

    Ok(format!(
        "State: {} ({state:?})\nSocket: {}\nDatabase: {}\nData: {}\nCache: {}\nVersion: {}",
        response.state_label,
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

fn socket_path() -> Result<PathBuf, String> {
    let runtime_dir = std::env::var_os("XDG_RUNTIME_DIR")
        .ok_or_else(|| "XDG_RUNTIME_DIR is not set".to_string())?;

    Ok(PathBuf::from(runtime_dir)
        .join("whatevrd")
        .join("whatevrd.sock"))
}
