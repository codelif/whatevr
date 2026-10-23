//! wgpu device and surface, instanced pipelines, atlases, headless PNG output.

mod atlas;
mod edge;
mod frame;
mod gpu;
mod headless;
mod renderer;
mod scene;
pub mod scenes;
pub mod snap;
mod surface;

pub use atlas::Alpha;
pub use frame::{Frame, Presenter};
pub use gpu::{Adapter, Gpu};
pub use headless::Pixmap;
pub use renderer::Renderer;
pub use scene::{Clip, Corners, Edges, Fill, Image, ImageId, Quad, Rect, Scene, Shadow};
pub use surface::WindowSurface;
/// what [`Renderer::new`] takes, so callers don't need wgpu themselves
pub use wgpu::TextureFormat;

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("no usable gpu adapter: {0}")]
    Adapter(#[from] wgpu::RequestAdapterError),
    #[error("lavapipe not found, install mesa's vulkan-swrast")]
    NoLavapipe,
    #[error("gpu device: {0}")]
    Device(#[from] wgpu::RequestDeviceError),
    #[error("gpu surface: {0}")]
    Surface(#[from] wgpu::CreateSurfaceError),
    #[error("adapter can't present to this surface")]
    Unsupported,
    #[error("gpu wait: {0}")]
    Poll(#[from] wgpu::PollError),
    #[error("readback: {0}")]
    Map(#[from] wgpu::BufferAsyncError),
    #[error("readback: {0}")]
    MapRange(#[from] wgpu::MapRangeError),
    #[error("readback never finished")]
    Readback,
}
