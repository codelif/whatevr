use std::fmt::Debug;

use log::info;
use raw_window_handle::HasDisplayHandle;

use crate::Error;

pub struct Gpu {
    pub instance: wgpu::Instance,
    pub adapter: wgpu::Adapter,
    pub device: wgpu::Device,
    pub queue: wgpu::Queue,
}

impl Gpu {
    /// gles needs the wayland display up front; vulkan doesn't care.
    /// WGPU_BACKEND and friends still override the defaults.
    pub fn new(
        display: impl HasDisplayHandle + Debug + Send + Sync + 'static,
    ) -> Result<Self, Error> {
        let desc = wgpu::InstanceDescriptor {
            backends: wgpu::Backends::VULKAN | wgpu::Backends::GL,
            ..wgpu::InstanceDescriptor::new_with_display_handle(Box::new(display))
        }
        .with_env();
        let instance = wgpu::Instance::new(desc);
        let adapter = pollster::block_on(instance.request_adapter(&wgpu::RequestAdapterOptions {
            power_preference: wgpu::PowerPreference::from_env().unwrap_or_default(),
            ..Default::default()
        }))?;
        let ai = adapter.get_info();
        info!(
            "gpu: {} ({:?}, {} {})",
            ai.name, ai.backend, ai.driver, ai.driver_info
        );

        let (device, queue) =
            pollster::block_on(adapter.request_device(&wgpu::DeviceDescriptor {
                label: Some("what-render"),
                required_features: wgpu::Features::empty(),
                // downlevel so gles works, but take the adapter's real texture size limits
                required_limits:
                    wgpu::Limits::downlevel_defaults().using_resolution(adapter.limits()),
                ..Default::default()
            }))?;
        Ok(Self {
            instance,
            adapter,
            device,
            queue,
        })
    }
}
