use tonic::Streaming;

use crate::daemon::{DynError, channel, rpc_request};
use crate::proto;
use crate::proto::daemon_service_client::DaemonServiceClient;
use crate::proto::{GetStatusRequest, ReconnectRequest, SubscribeEventsRequest};

pub async fn fetch_status() -> Result<proto::GetStatusResponse, DynError> {
    let channel = channel::connect().await?;
    let mut client = DaemonServiceClient::new(channel);

    Ok(client
        .get_status(rpc_request(GetStatusRequest {}))
        .await?
        .into_inner())
}

pub async fn subscribe_events() -> Result<Streaming<proto::DaemonEvent>, DynError> {
    let channel = channel::connect().await?;
    let mut client = DaemonServiceClient::new(channel);

    Ok(client
        .subscribe_events(SubscribeEventsRequest {})
        .await?
        .into_inner())
}

pub async fn reconnect() -> Result<(), DynError> {
    let channel = channel::connect().await?;
    let mut client = DaemonServiceClient::new(channel);

    client.reconnect(rpc_request(ReconnectRequest {})).await?;

    Ok(())
}
