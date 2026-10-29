//! instanced pipelines: one draw per kind per layer, plus one per atlas page
//! change among a layer's images and glyphs.

use std::borrow::Cow;

use bytemuck::{Pod, Zeroable};
use log::warn;
use what_core::{PhysicalSize, PremulRgba};

use crate::atlas::{Alpha, Atlas};
use crate::edge::EdgeCopy;
use crate::glyphs::{GlyphFormat, GlyphImage, Glyphs};
use crate::scene::{Clip, Corners, Fill, LayerStart};
use crate::timing::Timing;
use crate::{Frame, Gpu, ImageId, Scene};

const COMMON: &str = include_str!("shaders/common.wgsl");

#[repr(C)]
#[derive(Clone, Copy, Pod, Zeroable)]
struct QuadInstance {
    rect: [f32; 4],
    radii: [f32; 4],
    border: [f32; 4],
    border_color: [f32; 4],
    start: [f32; 4],
    end: [f32; 4],
    gradient: [f32; 4],
    kind: [f32; 4],
    clip_rect: [f32; 4],
    clip_radii: [f32; 4],
}

#[repr(C)]
#[derive(Clone, Copy, Pod, Zeroable)]
struct ShadowInstance {
    rect: [f32; 4],
    radii: [f32; 4],
    color: [f32; 4],
    params: [f32; 4],
    box_rect: [f32; 4],
    box_radii: [f32; 4],
    clip_rect: [f32; 4],
    clip_radii: [f32; 4],
}

#[repr(C)]
#[derive(Clone, Copy, Pod, Zeroable)]
struct ImageInstance {
    rect: [f32; 4],
    uv: [f32; 4],
    radii: [f32; 4],
    params: [f32; 4],
    clip_rect: [f32; 4],
    clip_radii: [f32; 4],
}

#[repr(C)]
#[derive(Clone, Copy, Pod, Zeroable)]
struct GlyphInstance {
    rect: [f32; 4],
    texel: [f32; 4],
    color: [f32; 4],
    clip_rect: [f32; 4],
    clip_radii: [f32; 4],
}

#[repr(C)]
#[derive(Clone, Copy, Pod, Zeroable)]
struct Globals {
    viewport: [f32; 2],
    _pad: [f32; 2],
}

/// one vertex buffer per kind, doubled when it runs out
struct Instances<T> {
    cpu: Vec<T>,
    gpu: Option<wgpu::Buffer>,
    label: &'static str,
}

impl<T: Pod> Instances<T> {
    fn new(label: &'static str) -> Self {
        Self {
            cpu: Vec::new(),
            gpu: None,
            label,
        }
    }

    fn upload(&mut self, gpu: &Gpu) {
        let bytes: &[u8] = bytemuck::cast_slice(&self.cpu);
        if bytes.is_empty() {
            return;
        }
        if self
            .gpu
            .as_ref()
            .is_none_or(|b| b.size() < bytes.len() as u64)
        {
            self.gpu = Some(gpu.device.create_buffer(&wgpu::BufferDescriptor {
                label: Some(self.label),
                size: (bytes.len() as u64).next_power_of_two().max(4096),
                usage: wgpu::BufferUsages::VERTEX | wgpu::BufferUsages::COPY_DST,
                mapped_at_creation: false,
            }));
        }
        gpu.queue.write_buffer(self.gpu.as_ref().unwrap(), 0, bytes);
    }
}

/// a stretch of one layer's images or glyphs that share an atlas page
struct Run {
    layer: usize,
    page: usize,
    instances: std::ops::Range<u32>,
}

pub struct Renderer {
    format: wgpu::TextureFormat,
    globals: wgpu::Buffer,
    globals_group: wgpu::BindGroup,
    quad_pipeline: wgpu::RenderPipeline,
    shadow_pipeline: wgpu::RenderPipeline,
    image_pipeline: wgpu::RenderPipeline,
    glyph_pipeline: wgpu::RenderPipeline,
    quads: Instances<QuadInstance>,
    shadows: Instances<ShadowInstance>,
    images: Instances<ImageInstance>,
    glyph_instances: Instances<GlyphInstance>,
    runs: Vec<Run>,
    glyph_runs: Vec<Run>,
    layers: Vec<(LayerStart, LayerStart)>,
    atlas: Atlas,
    glyphs: Glyphs,
    edge: EdgeCopy,
    timing: Option<Timing>,
}

impl Renderer {
    /// pipelines are built for one target format: the surface's, or
    /// Rgba8Unorm for headless
    pub fn new(gpu: &Gpu, format: wgpu::TextureFormat) -> Self {
        let device = &gpu.device;
        let globals_layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("globals"),
            entries: &[wgpu::BindGroupLayoutEntry {
                binding: 0,
                visibility: wgpu::ShaderStages::VERTEX_FRAGMENT,
                ty: wgpu::BindingType::Buffer {
                    ty: wgpu::BufferBindingType::Uniform,
                    has_dynamic_offset: false,
                    min_binding_size: None,
                },
                count: None,
            }],
        });
        let page_layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("atlas page"),
            entries: &[
                wgpu::BindGroupLayoutEntry {
                    binding: 0,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Texture {
                        sample_type: wgpu::TextureSampleType::Float { filterable: true },
                        view_dimension: wgpu::TextureViewDimension::D2,
                        multisampled: false,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 1,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                    count: None,
                },
            ],
        });
        let glyph_page_layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("glyph page"),
            entries: &[wgpu::BindGroupLayoutEntry {
                binding: 0,
                visibility: wgpu::ShaderStages::FRAGMENT,
                ty: wgpu::BindingType::Texture {
                    sample_type: wgpu::TextureSampleType::Float { filterable: false },
                    view_dimension: wgpu::TextureViewDimension::D2,
                    multisampled: false,
                },
                count: None,
            }],
        });
        let globals = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("globals"),
            size: size_of::<Globals>() as u64,
            usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });
        let globals_group = device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("globals"),
            layout: &globals_layout,
            entries: &[wgpu::BindGroupEntry {
                binding: 0,
                resource: globals.as_entire_binding(),
            }],
        });

        let quad_pipeline = pipeline(
            gpu,
            "quad",
            include_str!("shaders/quad.wgsl"),
            format,
            &[&globals_layout],
            size_of::<QuadInstance>(),
            &wgpu::vertex_attr_array![
                0 => Float32x4, 1 => Float32x4, 2 => Float32x4, 3 => Float32x4, 4 => Float32x4,
                5 => Float32x4, 6 => Float32x4, 7 => Float32x4, 8 => Float32x4, 9 => Float32x4,
            ],
        );
        let shadow_pipeline = pipeline(
            gpu,
            "shadow",
            include_str!("shaders/shadow.wgsl"),
            format,
            &[&globals_layout],
            size_of::<ShadowInstance>(),
            &wgpu::vertex_attr_array![
                0 => Float32x4, 1 => Float32x4, 2 => Float32x4, 3 => Float32x4,
                4 => Float32x4, 5 => Float32x4, 6 => Float32x4, 7 => Float32x4,
            ],
        );
        let image_pipeline = pipeline(
            gpu,
            "image",
            include_str!("shaders/image.wgsl"),
            format,
            &[&globals_layout, &page_layout],
            size_of::<ImageInstance>(),
            &wgpu::vertex_attr_array![
                0 => Float32x4, 1 => Float32x4, 2 => Float32x4,
                3 => Float32x4, 4 => Float32x4, 5 => Float32x4,
            ],
        );
        let glyph_pipeline = pipeline(
            gpu,
            "glyph",
            include_str!("shaders/glyph.wgsl"),
            format,
            &[&globals_layout, &glyph_page_layout],
            size_of::<GlyphInstance>(),
            &wgpu::vertex_attr_array![
                0 => Float32x4, 1 => Float32x4, 2 => Float32x4, 3 => Float32x4, 4 => Float32x4,
            ],
        );

        Self {
            format,
            globals,
            globals_group,
            quad_pipeline,
            shadow_pipeline,
            image_pipeline,
            glyph_pipeline,
            quads: Instances::new("quads"),
            shadows: Instances::new("shadows"),
            images: Instances::new("images"),
            glyph_instances: Instances::new("glyphs"),
            runs: Vec::new(),
            glyph_runs: Vec::new(),
            layers: Vec::new(),
            atlas: Atlas::new(gpu, page_layout),
            glyphs: Glyphs::new(glyph_page_layout),
            edge: EdgeCopy::default(),
            timing: None,
        }
    }

    pub fn format(&self) -> wgpu::TextureFormat {
        self.format
    }

    /// rgba8, srgb encoded, rows tightly packed
    pub fn add_image(
        &mut self,
        gpu: &Gpu,
        size: PhysicalSize,
        rgba: &[u8],
        alpha: Alpha,
    ) -> ImageId {
        self.atlas.add(gpu, size, rgba, alpha)
    }

    pub fn remove_image(&mut self, id: ImageId) {
        self.atlas.remove(id);
    }

    /// whether `key` is in the glyph atlas. also keeps it there for this frame,
    /// so ask for every glyph you draw.
    pub fn has_glyph(&self, key: u128) -> bool {
        self.glyphs.get(key).is_some()
    }

    /// store a rasterized glyph; [`GlyphImage::EMPTY`] for one with no ink
    pub fn add_glyph(&mut self, gpu: &Gpu, key: u128, image: GlyphImage<'_>) {
        self.glyphs.add(gpu, key, image);
    }

    /// time the scene pass on the gpu from now on; false when the adapter
    /// has no timestamp queries
    pub fn enable_timing(&mut self, gpu: &Gpu) -> bool {
        if self.timing.is_none() {
            self.timing = Timing::new(gpu);
        }
        self.timing.is_some()
    }

    /// what the next draws' gpu times are reported under
    pub fn set_frame_tag(&mut self, tag: u64) {
        if let Some(t) = &mut self.timing {
            t.tag = tag;
        }
    }

    /// (tag, gpu time) of frames whose results came back, oldest first.
    /// they land a few frames after the draw.
    pub fn take_gpu_times(&mut self) -> Vec<(u64, std::time::Duration)> {
        self.timing
            .as_mut()
            .map(|t| std::mem::take(&mut t.done))
            .unwrap_or_default()
    }

    /// clears the whole frame, draws the scene, and repeats the content's
    /// edge past it when the frame is bigger than `content`
    pub fn draw(
        &mut self,
        gpu: &Gpu,
        frame: &mut Frame,
        scene: &Scene,
        clear: PremulRgba,
        content: PhysicalSize,
    ) {
        let texture = &frame.texture.texture;
        self.encode(
            gpu,
            &mut frame.encoder,
            &frame.view,
            texture,
            scene,
            clear,
            content,
            frame.copyable,
        );
    }

    #[allow(clippy::too_many_arguments)]
    pub(crate) fn encode(
        &mut self,
        gpu: &Gpu,
        encoder: &mut wgpu::CommandEncoder,
        view: &wgpu::TextureView,
        texture: &wgpu::Texture,
        scene: &Scene,
        clear: PremulRgba,
        content: PhysicalSize,
        copyable: bool,
    ) {
        self.prepare(scene);
        self.quads.upload(gpu);
        self.shadows.upload(gpu);
        self.images.upload(gpu);
        self.glyph_instances.upload(gpu);
        let globals = Globals {
            viewport: [texture.width() as f32, texture.height() as f32],
            _pad: [0.0; 2],
        };
        gpu.queue
            .write_buffer(&self.globals, 0, bytemuck::bytes_of(&globals));

        let timestamps = self.timing.as_mut().and_then(|t| t.begin(gpu));
        {
            let mut pass = encoder.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("scene"),
                timestamp_writes: timestamps,
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view,
                    depth_slice: None,
                    resolve_target: None,
                    ops: wgpu::Operations {
                        load: wgpu::LoadOp::Clear(wgpu::Color {
                            r: clear.r as f64,
                            g: clear.g as f64,
                            b: clear.b as f64,
                            a: clear.a as f64,
                        }),
                        store: wgpu::StoreOp::Store,
                    },
                })],
                ..Default::default()
            });
            pass.set_bind_group(0, &self.globals_group, &[]);
            let mut runs = self.runs.iter().peekable();
            let mut glyph_runs = self.glyph_runs.iter().peekable();
            for (layer, (start, end)) in self.layers.iter().enumerate() {
                if end.shadows > start.shadows {
                    pass.set_pipeline(&self.shadow_pipeline);
                    pass.set_vertex_buffer(0, self.shadows.gpu.as_ref().unwrap().slice(..));
                    pass.draw(0..4, start.shadows as u32..end.shadows as u32);
                }
                if end.quads > start.quads {
                    pass.set_pipeline(&self.quad_pipeline);
                    pass.set_vertex_buffer(0, self.quads.gpu.as_ref().unwrap().slice(..));
                    pass.draw(0..4, start.quads as u32..end.quads as u32);
                }
                let mut first = true;
                while let Some(run) = runs.next_if(|r| r.layer == layer) {
                    if first {
                        pass.set_pipeline(&self.image_pipeline);
                        pass.set_vertex_buffer(0, self.images.gpu.as_ref().unwrap().slice(..));
                        first = false;
                    }
                    let Some(page) = &self.atlas.pages[run.page] else {
                        continue;
                    };
                    pass.set_bind_group(1, &page.bind_group, &[]);
                    pass.draw(0..4, run.instances.clone());
                }
                let mut first = true;
                while let Some(run) = glyph_runs.next_if(|r| r.layer == layer) {
                    if first {
                        pass.set_pipeline(&self.glyph_pipeline);
                        let buffer = self.glyph_instances.gpu.as_ref().unwrap();
                        pass.set_vertex_buffer(0, buffer.slice(..));
                        first = false;
                    }
                    pass.set_bind_group(1, &self.glyphs.pages[run.page].bind_group, &[]);
                    pass.draw(0..4, run.instances.clone());
                }
            }
        }
        if let Some(t) = &mut self.timing {
            t.end(encoder);
        }
        self.glyphs.end_frame();

        let past = content.width < texture.width() || content.height < texture.height();
        if past && copyable {
            self.edge.apply(gpu, encoder, texture, content);
        }
    }

    fn prepare(&mut self, scene: &Scene) {
        self.quads.cpu.clear();
        self.quads.cpu.extend(scene.quads.iter().map(|q| {
            let (kind, gradient, start, end) = match q.fill {
                Fill::Solid(c) => (0.0, [0.0; 4], c, c),
                Fill::Linear {
                    from,
                    to,
                    start,
                    end,
                } => (1.0, [from[0], from[1], to[0], to[1]], start, end),
                Fill::Radial {
                    center,
                    radius,
                    start,
                    end,
                } => (
                    2.0,
                    [center[0], center[1], radius[0], radius[1]],
                    start,
                    end,
                ),
            };
            let (clip_rect, clip_radii) = clip(&q.clip);
            QuadInstance {
                rect: q.rect.to_array(),
                radii: q.radii.fit(q.rect.w, q.rect.h),
                border: q.border.to_array().map(|b| b.max(0.0)),
                border_color: rgba(q.border_color),
                start: rgba(start),
                end: rgba(end),
                gradient,
                kind: [kind, 0.0, 0.0, 0.0],
                clip_rect,
                clip_radii,
            }
        }));

        self.shadows.cpu.clear();
        self.shadows.cpu.extend(scene.shadows.iter().map(|s| {
            let r = s.rect;
            let w = (r.w + 2.0 * s.spread).max(0.0);
            let h = (r.h + 2.0 * s.spread).max(0.0);
            let rect = [
                r.x + s.offset[0] - s.spread,
                r.y + s.offset[1] - s.spread,
                w,
                h,
            ];
            let box_radii = s.radii.fit(r.w, r.h);
            let grown = box_radii.map(|c| spread_radius(c, s.spread));
            let radii = Corners {
                top_left: grown[0],
                top_right: grown[1],
                bottom_right: grown[2],
                bottom_left: grown[3],
            }
            .fit(w, h);
            let (clip_rect, clip_radii) = clip(&s.clip);
            ShadowInstance {
                rect,
                radii,
                color: rgba(s.color),
                params: [(s.blur / 2.0).max(0.0), 0.0, 0.0, 0.0],
                box_rect: r.to_array(),
                box_radii,
                clip_rect,
                clip_radii,
            }
        }));

        self.images.cpu.clear();
        self.glyph_instances.cpu.clear();
        self.runs.clear();
        self.glyph_runs.clear();
        self.layers.clear();
        for (layer, (start, end)) in scene.layer_ranges().enumerate() {
            for img in &scene.images[start.images..end.images] {
                let Some(slot) = self.atlas.slot(img.image) else {
                    warn!("drawing removed or unknown {:?}", img.image);
                    continue;
                };
                let i = self.images.cpu.len() as u32;
                match self.runs.last_mut() {
                    Some(r) if r.layer == layer && r.page == slot.page => r.instances.end = i + 1,
                    _ => self.runs.push(Run {
                        layer,
                        page: slot.page,
                        instances: i..i + 1,
                    }),
                }
                let (clip_rect, clip_radii) = clip(&img.clip);
                self.images.cpu.push(ImageInstance {
                    rect: img.rect.to_array(),
                    uv: slot.uv,
                    radii: img.radii.fit(img.rect.w, img.rect.h),
                    params: [img.opacity.clamp(0.0, 1.0), 0.0, 0.0, 0.0],
                    clip_rect,
                    clip_radii,
                });
            }
            for g in &scene.glyphs[start.glyphs..end.glyphs] {
                let Some(e) = self.glyphs.get(g.key) else {
                    warn!("drawing glyph {:#x} that isn't in the atlas", g.key);
                    continue;
                };
                let Some(page) = e.page else {
                    continue;
                };
                let i = self.glyph_instances.cpu.len() as u32;
                match self.glyph_runs.last_mut() {
                    Some(r) if r.layer == layer && r.page == page => r.instances.end = i + 1,
                    _ => self.glyph_runs.push(Run {
                        layer,
                        page,
                        instances: i..i + 1,
                    }),
                }
                let (clip_rect, clip_radii) = clip(&g.clip);
                let color = (e.format == GlyphFormat::Rgba) as u32 as f32;
                self.glyph_instances.cpu.push(GlyphInstance {
                    rect: [
                        (g.x + e.left) as f32,
                        (g.y - e.top) as f32,
                        e.w as f32,
                        e.h as f32,
                    ],
                    texel: [e.x as f32, e.y as f32, color, 0.0],
                    color: rgba(g.color),
                    clip_rect,
                    clip_radii,
                });
            }
            self.layers.push((start, end));
        }
    }
}

fn pipeline(
    gpu: &Gpu,
    label: &str,
    source: &str,
    format: wgpu::TextureFormat,
    groups: &[&wgpu::BindGroupLayout],
    stride: usize,
    attributes: &[wgpu::VertexAttribute],
) -> wgpu::RenderPipeline {
    let device = &gpu.device;
    let module = device.create_shader_module(wgpu::ShaderModuleDescriptor {
        label: Some(label),
        source: wgpu::ShaderSource::Wgsl(Cow::Owned(format!("{COMMON}\n{source}"))),
    });
    let groups: Vec<_> = groups.iter().map(|g| Some(*g)).collect();
    let layout = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
        label: Some(label),
        bind_group_layouts: &groups,
        immediate_size: 0,
    });
    device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
        label: Some(label),
        layout: Some(&layout),
        vertex: wgpu::VertexState {
            module: &module,
            entry_point: Some("vs"),
            compilation_options: Default::default(),
            buffers: &[Some(wgpu::VertexBufferLayout {
                array_stride: stride as u64,
                step_mode: wgpu::VertexStepMode::Instance,
                attributes,
            })],
        },
        primitive: wgpu::PrimitiveState {
            topology: wgpu::PrimitiveTopology::TriangleStrip,
            ..Default::default()
        },
        depth_stencil: None,
        multisample: wgpu::MultisampleState::default(),
        fragment: Some(wgpu::FragmentState {
            module: &module,
            entry_point: Some("fs"),
            compilation_options: Default::default(),
            targets: &[Some(wgpu::ColorTargetState {
                format,
                blend: Some(wgpu::BlendState::PREMULTIPLIED_ALPHA_BLENDING),
                write_mask: wgpu::ColorWrites::ALL,
            })],
        }),
        multiview_mask: None,
        cache: None,
    })
}

fn rgba(c: PremulRgba) -> [f32; 4] {
    [c.r, c.g, c.b, c.a]
}

fn clip(c: &Clip) -> ([f32; 4], [f32; 4]) {
    (c.rect.to_array(), c.radii.fit(c.rect.w, c.rect.h))
}

/// css backgrounds 3, box-shadow spread: small radii grow less than the
/// spread so sharp corners stay sharp
fn spread_radius(r: f32, spread: f32) -> f32 {
    if spread < 0.0 {
        return (r + spread).max(0.0);
    }
    if r >= spread || spread == 0.0 {
        return r + spread;
    }
    let ratio = r / spread;
    r + spread * (1.0 + (ratio - 1.0).powi(3))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn instance_strides_fit_downlevel_limits() {
        // gles downlevel caps a vertex stride at 255 bytes
        for size in [
            size_of::<QuadInstance>(),
            size_of::<ShadowInstance>(),
            size_of::<ImageInstance>(),
            size_of::<GlyphInstance>(),
        ] {
            assert!(size <= 255, "{size}");
        }
    }

    #[test]
    fn spread_keeps_sharp_corners_sharp() {
        assert_eq!(spread_radius(0.0, 10.0), 0.0);
        assert_eq!(spread_radius(20.0, 10.0), 30.0);
        // ratio 0.5: 5 + 10 * (1 - 0.125)
        assert_eq!(spread_radius(5.0, 10.0), 13.75);
        assert_eq!(spread_radius(5.0, -8.0), 0.0);
    }
}
