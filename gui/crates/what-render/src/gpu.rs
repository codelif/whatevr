use std::fmt::Debug;

use log::info;
use raw_window_handle::HasDisplayHandle;

use crate::Error;

/// which gpu a headless [`Gpu`] runs on
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Adapter {
    /// mesa's cpu vulkan driver: the same pixels on every machine with the
    /// same mesa and llvm, so goldens compare exactly
    Lavapipe,
    /// whatever wgpu picks, WGPU_BACKEND and friends apply
    Default,
}

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
        Self::with_adapter(instance, adapter)
    }

    /// no window, for tests and the snapshot tool
    pub fn headless(which: Adapter) -> Result<Self, Error> {
        match which {
            Adapter::Default => {
                let desc = wgpu::InstanceDescriptor {
                    backends: wgpu::Backends::VULKAN | wgpu::Backends::GL,
                    ..wgpu::InstanceDescriptor::new_without_display_handle()
                }
                .with_env();
                let instance = wgpu::Instance::new(desc);
                let adapter =
                    pollster::block_on(instance.request_adapter(&wgpu::RequestAdapterOptions {
                        power_preference: wgpu::PowerPreference::from_env().unwrap_or_default(),
                        ..Default::default()
                    }))?;
                Self::with_adapter(instance, adapter)
            }
            // no env overrides here: a WGPU_BACKEND left in the shell must not swap the reference renderer
            Adapter::Lavapipe => {
                let instance = wgpu::Instance::new(wgpu::InstanceDescriptor {
                    backends: wgpu::Backends::VULKAN,
                    ..wgpu::InstanceDescriptor::new_without_display_handle()
                });
                let adapter =
                    pollster::block_on(instance.enumerate_adapters(wgpu::Backends::VULKAN))
                        .into_iter()
                        .find(|a| {
                            let i = a.get_info();
                            i.device_type == wgpu::DeviceType::Cpu && i.name.contains("llvmpipe")
                        })
                        .ok_or(Error::NoLavapipe)?;
                Self::with_adapter(instance, adapter)
            }
        }
    }

    fn with_adapter(instance: wgpu::Instance, adapter: wgpu::Adapter) -> Result<Self, Error> {
        let ai = adapter.get_info();
        info!(
            "gpu: {} ({:?}, {} {})",
            ai.name, ai.backend, ai.driver, ai.driver_info
        );

        let (device, queue) =
            pollster::block_on(adapter.request_device(&wgpu::DeviceDescriptor {
                label: Some("what-render"),
                // free when the adapter has it; frame timing reads it
                required_features: adapter.features() & wgpu::Features::TIMESTAMP_QUERY,
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
