//! wgpu device and surface, instanced pipelines, atlases, headless PNG output.

mod frame;
mod gpu;
mod surface;

pub use frame::{Frame, Presenter};
pub use gpu::Gpu;
pub use surface::WindowSurface;

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("no usable gpu adapter: {0}")]
    Adapter(#[from] wgpu::RequestAdapterError),
    #[error("gpu device: {0}")]
    Device(#[from] wgpu::RequestDeviceError),
    #[error("gpu surface: {0}")]
    Surface(#[from] wgpu::CreateSurfaceError),
    #[error("adapter can't present to this surface")]
    Unsupported,
}
