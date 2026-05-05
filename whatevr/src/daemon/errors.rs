use tonic::Code;

pub fn friendly_daemon_error(err: &(dyn std::error::Error + 'static)) -> String {
    if let Some(status) = err.downcast_ref::<tonic::Status>() {
        return friendly_tonic_status(status);
    }

    friendly_daemon_error_text(&err.to_string())
}

pub fn friendly_tonic_status(status: &tonic::Status) -> String {
    match status.code() {
        Code::Unavailable => "whatevrd is not responding. Retrying...".to_string(),
        Code::DeadlineExceeded => "whatevrd took too long to respond. Try again.".to_string(),
        Code::FailedPrecondition => {
            "WhatsApp is not logged in. Please scan the QR code.".to_string()
        }
        Code::PermissionDenied => "Cannot access whatevrd. Check daemon permissions.".to_string(),
        Code::InvalidArgument => status.message().to_string(),
        _ => status.to_string(),
    }
}

pub fn friendly_daemon_error_text(s: &str) -> String {
    if s.contains("No such file or directory")
        || s.contains("Connection refused")
        || s.contains("os error 2")
    {
        "whatevrd is not running. Start the daemon and try again.".to_string()
    } else if s.contains("Permission denied") || s.contains("os error 13") {
        "Cannot access the whatevrd socket. Check daemon permissions.".to_string()
    } else if s.contains("Unavailable") || s.contains("deadline exceeded") {
        "whatevrd is not responding. Retrying...".to_string()
    } else if s.contains("FailedPrecondition") {
        "WhatsApp is not logged in. Please scan the QR code.".to_string()
    } else {
        s.to_string()
    }
}

pub fn is_daemon_transport_error(err: &(dyn std::error::Error + 'static)) -> bool {
    if let Some(status) = err.downcast_ref::<tonic::Status>() {
        return matches!(status.code(), Code::Unavailable | Code::DeadlineExceeded);
    }

    let s = err.to_string();

    s.contains("transport error")
        || s.contains("No such file or directory")
        || s.contains("Connection refused")
        || s.contains("os error 2")
        || s.contains("Unavailable")
}

pub fn is_whatsapp_connection_error_text(s: &str) -> bool {
    let s = s.to_lowercase();

    s.contains("websocket not connected")
        || s.contains("failed to read frame header")
        || s.contains("keepalive timed out")
}

pub fn whatsapp_connection_lost_detail() -> String {
    "WhatsApp connection lost. Reconnect now or wait for automatic retry.".to_string()
}
