use std::time::Instant;

use log::{debug, info, trace};
use raw_window_handle::{RawDisplayHandle, RawWindowHandle};
use what_core::PhysicalSize;

use crate::{Error, Gpu};

pub struct WindowSurface {
    pub(crate) surface: wgpu::Surface<'static>,
    pub(crate) config: wgpu::SurfaceConfiguration,
}

impl WindowSurface {
    /// # Safety
    /// both handles must stay valid until this is dropped
    pub unsafe fn new(
        gpu: &Gpu,
        display: RawDisplayHandle,
        window: RawWindowHandle,
        size: PhysicalSize,
    ) -> Result<Self, Error> {
        // SAFETY: forwarded to the caller
        let surface = unsafe {
            gpu.instance
                .create_surface_unsafe(wgpu::SurfaceTargetUnsafe::RawHandle {
                    raw_display_handle: Some(display),
                    raw_window_handle: window,
                })
        }?;
        let caps = surface.get_capabilities(&gpu.adapter);
        let Some(&format) = caps
            .formats
            .iter()
            .find(|f| f.is_srgb())
            .or(caps.formats.first())
        else {
            return Err(Error::Unsupported);
        };
        // mailbox never blocks in acquire, fifo can stall forever on a hidden window (mesa)
        let present_mode = if caps.present_modes.contains(&wgpu::PresentMode::Mailbox) {
            wgpu::PresentMode::Mailbox
        } else {
            wgpu::PresentMode::Fifo
        };
        let alpha_mode = if caps.alpha_modes.contains(&wgpu::CompositeAlphaMode::Opaque) {
            wgpu::CompositeAlphaMode::Opaque
        } else {
            caps.alpha_modes[0]
        };
        let config = wgpu::SurfaceConfiguration {
            usage: wgpu::TextureUsages::RENDER_ATTACHMENT,
            format,
            width: size.width.max(1),
            height: size.height.max(1),
            present_mode,
            desired_maximum_frame_latency: 2,
            alpha_mode,
            view_formats: Vec::new(),
            ..surface
                .get_default_config(&gpu.adapter, size.width.max(1), size.height.max(1))
                .ok_or(Error::Unsupported)?
        };
        info!("surface {format:?} {present_mode:?} {alpha_mode:?}");
        surface.configure(&gpu.device, &config);
        Ok(Self { surface, config })
    }

    pub fn size(&self) -> PhysicalSize {
        PhysicalSize::new(self.config.width, self.config.height)
    }

    pub fn format(&self) -> wgpu::TextureFormat {
        self.config.format
    }

    pub fn resize(&mut self, gpu: &Gpu, size: PhysicalSize) {
        let (w, h) = (size.width.max(1), size.height.max(1));
        if (w, h) == (self.config.width, self.config.height) {
            return;
        }
        debug!("surface {w}x{h}");
        self.config.width = w;
        self.config.height = h;
        let t = Instant::now();
        self.surface.configure(&gpu.device, &self.config);
        trace!("configure {w}x{h} {:.2} ms", ms(t));
    }

    pub(crate) fn reconfigure(&self, gpu: &Gpu) {
        let t = Instant::now();
        self.surface.configure(&gpu.device, &self.config);
        trace!("reconfigure {:.2} ms", ms(t));
    }
}

pub(crate) fn ms(since: Instant) -> f64 {
    since.elapsed().as_secs_f64() * 1e3
}
