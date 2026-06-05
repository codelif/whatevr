use tonic::Streaming;

use crate::daemon::{DynError, channel, rpc_request};
use crate::proto;
use crate::proto::login_service_client::LoginServiceClient;
use crate::proto::{LogoutRequest, SubscribeLoginEventsRequest};

pub async fn subscribe_login_events() -> Result<Streaming<proto::LoginEvent>, DynError> {
    let channel = channel::connect().await?;
    let mut client = LoginServiceClient::new(channel);

    Ok(client
        .subscribe_login_events(SubscribeLoginEventsRequest {})
        .await?
        .into_inner())
}

pub async fn logout() -> Result<(), DynError> {
    let channel = channel::connect().await?;
    let mut client = LoginServiceClient::new(channel);

    client.logout(rpc_request(LogoutRequest {})).await?;

    Ok(())
}
