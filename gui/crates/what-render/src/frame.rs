use std::time::Instant;

use log::{debug, error, trace, warn};
use what_core::LinearRgba;

use crate::surface::ms;
use crate::{Gpu, WindowSurface};

/// runs after the gpu work is submitted and before the commit that shows it.
/// the platform hooks frame callbacks, viewport and configure acks in here.
pub trait Presenter {
    fn before_commit(&mut self);
}

pub struct Frame {
    texture: wgpu::SurfaceTexture,
    pub view: wgpu::TextureView,
    pub encoder: wgpu::CommandEncoder,
    suboptimal: bool,
}

impl WindowSurface {
    /// none means skip this frame: occluded, timed out, or broken past one reconfigure
    pub fn begin(&self, gpu: &Gpu) -> Option<Frame> {
        let mut retried = false;
        let t = Instant::now();
        let (texture, suboptimal) = loop {
            match self.surface.get_current_texture() {
                wgpu::CurrentSurfaceTexture::Success(t) => break (t, false),
                wgpu::CurrentSurfaceTexture::Suboptimal(t) => break (t, true),
                wgpu::CurrentSurfaceTexture::Timeout | wgpu::CurrentSurfaceTexture::Occluded => {
                    debug!("surface busy, frame skipped");
                    return None;
                }
                wgpu::CurrentSurfaceTexture::Outdated | wgpu::CurrentSurfaceTexture::Lost
                    if !retried =>
                {
                    warn!("surface outdated, reconfiguring");
                    self.reconfigure(gpu);
                    retried = true;
                }
                wgpu::CurrentSurfaceTexture::Outdated | wgpu::CurrentSurfaceTexture::Lost => {
                    error!("surface still outdated after reconfigure");
                    return None;
                }
                wgpu::CurrentSurfaceTexture::Validation => {
                    error!("surface acquire failed validation");
                    return None;
                }
            }
        };
        trace!("acquire {:.2} ms", ms(t));
        let view = texture
            .texture
            .create_view(&wgpu::TextureViewDescriptor::default());
        let encoder = gpu
            .device
            .create_command_encoder(&wgpu::CommandEncoderDescriptor {
                label: Some("frame"),
            });
        Some(Frame {
            texture,
            view,
            encoder,
            suboptimal,
        })
    }

    pub fn present(&self, gpu: &Gpu, frame: Frame, presenter: &mut impl Presenter) {
        let t = Instant::now();
        gpu.queue.submit([frame.encoder.finish()]);
        let submit = ms(t);
        presenter.before_commit();
        let t = Instant::now();
        gpu.queue.present(frame.texture);
        trace!("submit {submit:.2} ms, present {:.2} ms", ms(t));
        if frame.suboptimal {
            self.reconfigure(gpu);
        }
    }

    /// step 1's whole renderer
    pub fn clear(&self, gpu: &Gpu, color: LinearRgba, presenter: &mut impl Presenter) -> bool {
        let Some(mut frame) = self.begin(gpu) else {
            return false;
        };
        frame
            .encoder
            .begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("clear"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: &frame.view,
                    depth_slice: None,
                    resolve_target: None,
                    ops: wgpu::Operations {
                        load: wgpu::LoadOp::Clear(wgpu::Color {
                            r: color.r as f64,
                            g: color.g as f64,
                            b: color.b as f64,
                            a: color.a as f64,
                        }),
                        store: wgpu::StoreOp::Store,
                    },
                })],
                ..Default::default()
            });
        self.present(gpu, frame, presenter);
        true
    }
}
