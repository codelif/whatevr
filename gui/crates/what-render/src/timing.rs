//! gpu time of the scene pass from timestamp queries. results come back a
//! few frames late through a small ring of readback buffers; a frame that
//! finds the ring full just goes untimed.

use std::sync::Arc;
use std::sync::atomic::{AtomicU8, Ordering};
use std::time::Duration;

use crate::Gpu;

const SLOTS: u32 = 8;
const BYTES: u64 = 16;
/// resolves have to land on 256 byte offsets; slot 0 alone hid that until
/// a stall (fullscreen) kept it busy
const STRIDE: u64 = wgpu::QUERY_RESOLVE_BUFFER_ALIGNMENT;
const _: () = assert!(STRIDE >= BYTES);

const FREE: u8 = 0;
/// encoded, its submit may not have happened yet
const ENCODED: u8 = 1;
const MAPPING: u8 = 2;
const MAPPED: u8 = 3;

struct Slot {
    readback: wgpu::Buffer,
    state: Arc<AtomicU8>,
    tag: u64,
}

pub(crate) struct Timing {
    queries: wgpu::QuerySet,
    resolve: wgpu::Buffer,
    slots: Vec<Slot>,
    period_ns: f64,
    pub tag: u64,
    current: Option<u32>,
    pub done: Vec<(u64, Duration)>,
}

impl Timing {
    pub fn new(gpu: &Gpu) -> Option<Self> {
        if !gpu
            .device
            .features()
            .contains(wgpu::Features::TIMESTAMP_QUERY)
        {
            return None;
        }
        let queries = gpu.device.create_query_set(&wgpu::QuerySetDescriptor {
            label: Some("frame timing"),
            ty: wgpu::QueryType::Timestamp,
            count: SLOTS * 2,
        });
        let resolve = gpu.device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("frame timing resolve"),
            size: STRIDE * SLOTS as u64,
            usage: wgpu::BufferUsages::QUERY_RESOLVE | wgpu::BufferUsages::COPY_SRC,
            mapped_at_creation: false,
        });
        let slots = (0..SLOTS)
            .map(|_| Slot {
                readback: gpu.device.create_buffer(&wgpu::BufferDescriptor {
                    label: Some("frame timing readback"),
                    size: BYTES,
                    usage: wgpu::BufferUsages::MAP_READ | wgpu::BufferUsages::COPY_DST,
                    mapped_at_creation: false,
                }),
                state: Arc::new(AtomicU8::new(FREE)),
                tag: 0,
            })
            .collect();
        Some(Self {
            queries,
            resolve,
            slots,
            period_ns: gpu.queue.get_timestamp_period() as f64,
            tag: 0,
            current: None,
            done: Vec::new(),
        })
    }

    /// collect finished slots and start mapping the submitted ones, then
    /// pick a slot for the frame about to be encoded
    pub fn begin(&mut self, gpu: &Gpu) -> Option<wgpu::RenderPassTimestampWrites<'_>> {
        // anything encoded before this call has been submitted by now
        for s in &self.slots {
            if s.state.load(Ordering::Acquire) == ENCODED {
                s.state.store(MAPPING, Ordering::Release);
                let state = s.state.clone();
                s.readback.map_async(wgpu::MapMode::Read, .., move |r| {
                    state.store(if r.is_ok() { MAPPED } else { FREE }, Ordering::Release);
                });
            }
        }
        let _ = gpu.device.poll(wgpu::PollType::Poll);
        for s in &self.slots {
            if s.state.load(Ordering::Acquire) != MAPPED {
                continue;
            }
            let ticks: Option<[u64; 2]> = s
                .readback
                .slice(..)
                .get_mapped_range()
                .ok()
                .map(|v| bytemuck::pod_read_unaligned(&v[..BYTES as usize]));
            s.readback.unmap();
            s.state.store(FREE, Ordering::Release);
            if let Some(t) = ticks {
                let ns = t[1].saturating_sub(t[0]) as f64 * self.period_ns;
                self.done.push((s.tag, Duration::from_nanos(ns as u64)));
            }
        }
        let i = self
            .slots
            .iter()
            .position(|s| s.state.load(Ordering::Acquire) == FREE)?;
        self.current = Some(i as u32);
        Some(wgpu::RenderPassTimestampWrites {
            query_set: &self.queries,
            beginning_of_pass_write_index: Some(i as u32 * 2),
            end_of_pass_write_index: Some(i as u32 * 2 + 1),
        })
    }

    /// after the pass: copy this frame's two timestamps out
    pub fn end(&mut self, encoder: &mut wgpu::CommandEncoder) {
        let Some(i) = self.current.take() else {
            return;
        };
        let at = i as u64 * STRIDE;
        encoder.resolve_query_set(&self.queries, i * 2..i * 2 + 2, &self.resolve, at);
        let s = &mut self.slots[i as usize];
        encoder.copy_buffer_to_buffer(&self.resolve, at, &s.readback, 0, BYTES);
        s.tag = self.tag;
        s.state.store(ENCODED, Ordering::Release);
    }
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::Ordering;

    use what_core::{PhysicalSize, PremulRgba};

    use super::{MAPPING, SLOTS, Timing};
    use crate::{Adapter, Gpu, Quad, Rect, Renderer, Scene};

    #[test]
    fn every_slot_resolves() {
        let gpu = Gpu::headless(Adapter::Lavapipe).unwrap();
        let Some(mut t) = Timing::new(&gpu) else {
            eprintln!("no timestamp queries here, skipping");
            return;
        };
        let target = gpu.device.create_texture(&wgpu::TextureDescriptor {
            label: None,
            size: wgpu::Extent3d {
                width: 4,
                height: 4,
                depth_or_array_layers: 1,
            },
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: wgpu::TextureFormat::Rgba8Unorm,
            usage: wgpu::TextureUsages::RENDER_ATTACHMENT,
            view_formats: &[],
        });
        let view = target.create_view(&Default::default());
        for i in 0..SLOTS {
            // the slots before it still waiting on their readback
            for s in &t.slots[..i as usize] {
                s.state.store(MAPPING, Ordering::Release);
            }
            let mut encoder = gpu.device.create_command_encoder(&Default::default());
            {
                let writes = t.begin(&gpu);
                assert!(writes.is_some(), "slot {i} taken");
                encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
                    timestamp_writes: writes,
                    color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                        view: &view,
                        depth_slice: None,
                        resolve_target: None,
                        ops: wgpu::Operations::default(),
                    })],
                    ..Default::default()
                });
            }
            assert_eq!(t.current, Some(i));
            t.end(&mut encoder);
            gpu.queue.submit([encoder.finish()]);
        }
    }

    #[test]
    fn frames_come_back_tagged() {
        let gpu = Gpu::headless(Adapter::Lavapipe).unwrap();
        let mut r = Renderer::new(&gpu, wgpu::TextureFormat::Rgba8Unorm);
        if !r.enable_timing(&gpu) {
            eprintln!("no timestamp queries here, skipping");
            return;
        }
        let mut scene = Scene::new();
        scene.quad(Quad {
            rect: Rect::new(0.0, 0.0, 64.0, 64.0),
            ..Quad::default()
        });
        let size = PhysicalSize::new(64, 64);
        let mut got = Vec::new();
        for tag in 0..6 {
            r.set_frame_tag(tag);
            r.render_pixmap(&gpu, &scene, size, size, PremulRgba::TRANSPARENT)
                .unwrap();
            got.extend(r.take_gpu_times());
        }
        let tags: Vec<u64> = got.iter().map(|g| g.0).collect();
        assert!(
            !tags.is_empty() && tags.windows(2).all(|w| w[0] < w[1]),
            "{tags:?}"
        );
    }
}
