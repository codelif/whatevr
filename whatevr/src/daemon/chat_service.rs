use crate::daemon::{DynError, channel, rpc_request};
use crate::proto;
use crate::proto::chat_service_client::ChatServiceClient;
use crate::proto::{
    GetMessagesRequest, ListChatsRequest, MarkChatReadRequest, SetChatPresenceRequest,
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
    let channel = channel::connect().await?;
    let mut client = ChatServiceClient::new(channel);

    let response = client
        .get_messages(rpc_request(GetMessagesRequest {
            chat_id,
            limit: 50,
            before_message_id: String::new(),
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

pub async fn set_chat_presence(chat_id: String, composing: bool) -> Result<(), DynError> {
    let channel = channel::connect().await?;
    let mut client = ChatServiceClient::new(channel);

    client
        .set_chat_presence(rpc_request(SetChatPresenceRequest { chat_id, composing }))
        .await?;

    Ok(())
}
