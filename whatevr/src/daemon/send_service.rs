use crate::daemon::{DynError, channel, rpc_request};
use crate::proto;
use crate::proto::SendMediaRequest;
use crate::proto::send_service_client::SendServiceClient;

pub async fn send_text(chat_id: String, text: String) -> Result<String, DynError> {
    let channel = channel::connect().await?;
    let mut client = SendServiceClient::new(channel);

    let request_chat_id = chat_id.clone();

    let response = client
        .send_text(rpc_request(proto::SendTextRequest {
            chat_id: request_chat_id,
            text,
        }))
        .await?
        .into_inner();

    Ok(response
        .message
        .map(|message| message.chat_id)
        .filter(|chat_id| !chat_id.is_empty())
        .unwrap_or(chat_id))
}

pub async fn send_media(chat_id: String, file_path: String) -> Result<(), DynError> {
    let channel = channel::connect().await?;
    let mut client = SendServiceClient::new(channel);

    client
        .send_media(rpc_request(SendMediaRequest {
            chat_id,
            file_path,
            caption: String::new(),
        }))
        .await?;

    Ok(())
}
