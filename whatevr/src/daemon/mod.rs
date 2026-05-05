pub mod channel;
pub mod chat_service;
pub mod daemon_service;
pub mod errors;
pub mod frontend_service;
pub mod login_service;
pub mod send_service;

use tonic::Request;

use crate::config::RPC_TIMEOUT;

pub type DynError = Box<dyn std::error::Error + Send + Sync>;

pub fn rpc_request<T>(message: T) -> Request<T> {
    let mut request = Request::new(message);
    request.set_timeout(RPC_TIMEOUT);
    request
}
