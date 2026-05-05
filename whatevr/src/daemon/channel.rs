use std::path::PathBuf;

use http::Uri;
use hyper_util::rt::TokioIo;
use tokio::net::UnixStream;
use tonic::transport::{Channel, Endpoint};
use tower::service_fn;

use crate::config::RPC_CONNECT_TIMEOUT;
use crate::daemon::DynError;

pub async fn connect() -> Result<Channel, DynError> {
    let socket_path = socket_path()?;
    let endpoint = Endpoint::try_from("http://[::]:50051")?.connect_timeout(RPC_CONNECT_TIMEOUT);

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
