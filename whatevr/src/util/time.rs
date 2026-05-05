use chrono::{Local, TimeZone};

use crate::proto;

pub fn format_qr_expiry(expires_at: i64) -> String {
    match Local.timestamp_opt(expires_at, 0).single() {
        Some(datetime) => format!("Expires at {}", datetime.format("%H:%M")),
        None => "QR expiration unavailable".to_string(),
    }
}

pub fn format_chat_timestamp(timestamp_unix: i64) -> String {
    let Some(datetime) = Local.timestamp_opt(timestamp_unix, 0).single() else {
        return String::new();
    };

    let now = Local::now();

    if datetime.date_naive() == now.date_naive() {
        datetime.format("%H:%M").to_string()
    } else if now
        .date_naive()
        .signed_duration_since(datetime.date_naive())
        .num_days()
        < 6
    {
        datetime.format("%a").to_string()
    } else {
        datetime.format("%d %b").to_string()
    }
}

pub fn format_message_meta(message: &proto::Message) -> String {
    let timestamp = Local
        .timestamp_opt(message.timestamp_unix, 0)
        .single()
        .map(|datetime| datetime.format("%H:%M").to_string())
        .unwrap_or_else(|| "--:--".to_string());

    if message.direction == proto::MessageDirection::Outgoing as i32 {
        let status = match proto::MessageStatus::try_from(message.status)
            .unwrap_or(proto::MessageStatus::Unspecified)
        {
            proto::MessageStatus::Sent => "Sent",
            proto::MessageStatus::Delivered => "Delivered",
            proto::MessageStatus::Read => "Read",
            proto::MessageStatus::Failed => "Failed",
            proto::MessageStatus::Pending => "Pending",
            proto::MessageStatus::Unspecified => "",
        };

        if status.is_empty() {
            timestamp
        } else {
            format!("{}  •  {}", timestamp, status)
        }
    } else {
        timestamp
    }
}
