//! whatevr protocol types and a framed unix-socket client.

mod client;
mod frame;

pub use client::{Client, Error, Incoming, SendError, socket_path};
pub use frame::MAX_FRAME;

#[allow(clippy::all, missing_docs)]
pub mod v2 {
    include!(concat!(env!("OUT_DIR"), "/whatevr.v2.mod.rs"));
    pub use __buffa::{oneof, view};
}
