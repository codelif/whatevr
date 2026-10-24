//! render a scene into an offscreen texture and read the pixels back

use what_core::{PhysicalSize, PremulRgba};

use crate::atlas::extent;
use crate::{Error, Gpu, Renderer, Scene};

/// rgba8, srgb encoded, rows tightly packed
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Pixmap {
    pub size: PhysicalSize,
    pub rgba: Vec<u8>,
}

impl Pixmap {
    pub fn pixel(&self, x: u32, y: u32) -> [u8; 4] {
        let i = ((y * self.size.width + x) * 4) as usize;
        self.rgba[i..i + 4].try_into().unwrap()
    }
}

impl Renderer {
    /// like a window frame: the target is `size`, the scene's content `content`
    /// (smaller only to test the resize edge)
    pub fn render_pixmap(
        &mut self,
        gpu: &Gpu,
        scene: &Scene,
        size: PhysicalSize,
        content: PhysicalSize,
        clear: PremulRgba,
    ) -> Result<Pixmap, Error> {
        let (w, h) = (size.width.max(1), size.height.max(1));
        let texture = gpu.device.create_texture(&wgpu::TextureDescriptor {
            label: Some("headless"),
            size: extent(w, h),
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: self.format(),
            usage: wgpu::TextureUsages::RENDER_ATTACHMENT
                | wgpu::TextureUsages::COPY_SRC
                | wgpu::TextureUsages::COPY_DST,
            view_formats: &[],
        });
        let view = texture.create_view(&wgpu::TextureViewDescriptor::default());
        let mut encoder = gpu
            .device
            .create_command_encoder(&wgpu::CommandEncoderDescriptor {
                label: Some("headless"),
            });
        self.encode(
            gpu,
            &mut encoder,
            &view,
            &texture,
            scene,
            clear,
            content,
            true,
        );

        let row = (w * 4).next_multiple_of(wgpu::COPY_BYTES_PER_ROW_ALIGNMENT);
        let buffer = gpu.device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("readback"),
            size: (row * h) as u64,
            usage: wgpu::BufferUsages::COPY_DST | wgpu::BufferUsages::MAP_READ,
            mapped_at_creation: false,
        });
        encoder.copy_texture_to_buffer(
            wgpu::TexelCopyTextureInfo {
                texture: &texture,
                mip_level: 0,
                origin: wgpu::Origin3d::ZERO,
                aspect: wgpu::TextureAspect::All,
            },
            wgpu::TexelCopyBufferInfo {
                buffer: &buffer,
                layout: wgpu::TexelCopyBufferLayout {
                    offset: 0,
                    bytes_per_row: Some(row),
                    rows_per_image: Some(h),
                },
            },
            extent(w, h),
        );
        gpu.queue.submit([encoder.finish()]);

        let (tx, rx) = std::sync::mpsc::channel();
        buffer.map_async(wgpu::MapMode::Read, .., move |r| {
            let _ = tx.send(r);
        });
        gpu.device.poll(wgpu::PollType::wait_indefinitely())?;
        rx.recv().map_err(|_| Error::Readback)??;
        let mapped = buffer.get_mapped_range(..)?;
        let mut rgba = Vec::with_capacity((w * h * 4) as usize);
        for line in mapped.chunks(row as usize) {
            rgba.extend_from_slice(&line[..(w * 4) as usize]);
        }
        drop(mapped);
        if matches!(
            self.format(),
            wgpu::TextureFormat::Bgra8Unorm | wgpu::TextureFormat::Bgra8UnormSrgb
        ) {
            for px in rgba.chunks_exact_mut(4) {
                px.swap(0, 2);
            }
        }
        Ok(Pixmap {
            size: PhysicalSize::new(w, h),
            rgba,
        })
    }
}

#[cfg(feature = "png")]
impl Pixmap {
    pub fn save_png(&self, path: &std::path::Path) -> std::io::Result<()> {
        let file = std::io::BufWriter::new(std::fs::File::create(path)?);
        let mut enc = png::Encoder::new(file, self.size.width, self.size.height);
        enc.set_color(png::ColorType::Rgba);
        enc.set_depth(png::BitDepth::Eight);
        enc.set_source_srgb(png::SrgbRenderingIntent::Perceptual);
        let mut w = enc.write_header().map_err(std::io::Error::other)?;
        w.write_image_data(&self.rgba)
            .map_err(std::io::Error::other)?;
        w.finish().map_err(std::io::Error::other)
    }

    /// 8-bit rgba, which is all save_png writes, or 8-bit rgb
    pub fn load_png(path: &std::path::Path) -> std::io::Result<Self> {
        let file = std::io::BufReader::new(std::fs::File::open(path)?);
        let mut reader = png::Decoder::new(file)
            .read_info()
            .map_err(std::io::Error::other)?;
        let info = reader.info();
        let rgb = match (info.color_type, info.bit_depth) {
            (png::ColorType::Rgba, png::BitDepth::Eight) => false,
            (png::ColorType::Rgb, png::BitDepth::Eight) => true,
            _ => return Err(std::io::Error::other("not an 8-bit rgba or rgb png")),
        };
        let size = PhysicalSize::new(info.width, info.height);
        let mut rgba = vec![0; reader.output_buffer_size().unwrap_or(0)];
        let frame = reader
            .next_frame(&mut rgba)
            .map_err(std::io::Error::other)?;
        rgba.truncate(frame.buffer_size());
        if rgb {
            rgba = rgba
                .chunks_exact(3)
                .flat_map(|p| [p[0], p[1], p[2], 255])
                .collect();
        }
        Ok(Self { size, rgba })
    }
}
