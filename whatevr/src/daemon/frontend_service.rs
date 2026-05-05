use crate::daemon::{DynError, channel, rpc_request};
use crate::proto;
use crate::proto::UpdateSessionStateRequest;
use crate::proto::frontend_service_client::FrontendServiceClient;

pub async fn hold_session<F>(session_id: String, on_connected: F) -> Result<(), DynError>
where
    F: FnOnce() + Send,
{
    let channel = channel::connect().await?;
    let mut client = FrontendServiceClient::new(channel);

    let mut stream = client
        .hold_session(proto::HoldSessionRequest {
            client_name: "whatevr".to_string(),
            session_id,
        })
        .await?
        .into_inner();

    on_connected();

    while stream.message().await?.is_some() {}

    Ok(())
}

pub async fn update_session_state(
    session_id: String,
    focused: bool,
    active_chat_id: String,
) -> Result<(), DynError> {
    let channel = channel::connect().await?;
    let mut client = FrontendServiceClient::new(channel);

    client
        .update_session_state(rpc_request(UpdateSessionStateRequest {
            session_id,
            focused,
            active_chat_id,
        }))
        .await?;

    Ok(())
}
