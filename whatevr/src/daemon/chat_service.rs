use crate::daemon::{DynError, channel, rpc_request};
use crate::proto;
use crate::proto::chat_service_client::ChatServiceClient;
use crate::proto::{
    DownloadMessageMediaRequest, GetMessagesRequest, ListChatsRequest, MarkChatReadRequest,
    SetChatPresenceRequest,
};

pub async fn load_chats() -> Result<Vec<proto::Chat>, DynError> {
    let channel = channel::connect().await?;
    let mut client = ChatServiceClient::new(channel);

    let response = client
        .list_chats(rpc_request(ListChatsRequest {
            limit: 100,
            offset: 0,
        }))
        .await?
        .into_inner();

    Ok(response.chats)
}

pub async fn load_messages(chat_id: String) -> Result<Vec<proto::Message>, DynError> {
    load_messages_before(chat_id, None).await
}

pub async fn load_messages_before(
    chat_id: String,
    before_message_id: Option<String>,
) -> Result<Vec<proto::Message>, DynError> {
    let channel = channel::connect().await?;
    let mut client = ChatServiceClient::new(channel);

    // Larger pages reduce paginate-flicker churn: each prepend covers more
    // screen, so users land further from the top edge after a fetch and
    // don't immediately re-trigger the next one.
    let limit = if before_message_id.is_some() { 100 } else { 50 };
    let response = client
        .get_messages(rpc_request(GetMessagesRequest {
            chat_id,
            limit,
            before_message_id: before_message_id.unwrap_or_default(),
        }))
        .await?
        .into_inner();

    Ok(response.messages)
}

pub async fn mark_chat_read(chat_id: String) -> Result<(), DynError> {
    let channel = channel::connect().await?;
    let mut client = ChatServiceClient::new(channel);

    client
        .mark_chat_read(rpc_request(MarkChatReadRequest { chat_id }))
        .await?;

    Ok(())
}

pub async fn download_message_media(message_id: String) -> Result<proto::Message, DynError> {
    let channel = channel::connect().await?;
    let mut client = ChatServiceClient::new(channel);

    let response = client
        .download_message_media(rpc_request(DownloadMessageMediaRequest { message_id }))
        .await?
        .into_inner();

    response
        .message
        .ok_or_else(|| "daemon returned an empty media download response".into())
}

pub async fn set_chat_presence(chat_id: String, composing: bool) -> Result<(), DynError> {
    let channel = channel::connect().await?;
    let mut client = ChatServiceClient::new(channel);

    client
        .set_chat_presence(rpc_request(SetChatPresenceRequest { chat_id, composing }))
        .await?;

    Ok(())
}
