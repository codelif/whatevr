//! during an interactive resize the buffer is bigger than the content and the
//! compositor crops it, but samples ~1 px past the crop. copy the last content
//! column and row into the 2 px past them so that pixel matches the edge.
//! a texture can't be copied onto itself, hence the hop through a strip.

use what_core::PhysicalSize;

use crate::Gpu;
use crate::atlas::extent;

const WIDTH: u32 = 2;

#[derive(Default)]
pub(crate) struct EdgeCopy {
    column: Option<wgpu::Texture>,
    row: Option<wgpu::Texture>,
}

impl EdgeCopy {
    pub fn apply(
        &mut self,
        gpu: &Gpu,
        encoder: &mut wgpu::CommandEncoder,
        target: &wgpu::Texture,
        content: PhysicalSize,
    ) {
        let (tw, th) = (target.width(), target.height());
        let (cw, ch) = (content.width.min(tw), content.height.min(th));
        if cw == 0 || ch == 0 {
            return;
        }
        if cw < tw {
            let strip = strip(gpu, &mut self.column, target.format(), 1, ch);
            encoder.copy_texture_to_texture(at(target, cw - 1, 0), at(strip, 0, 0), extent(1, ch));
            for x in cw..(cw + WIDTH).min(tw) {
                encoder.copy_texture_to_texture(at(strip, 0, 0), at(target, x, 0), extent(1, ch));
            }
        }
        if ch < th {
            // includes the columns just filled, so the corner comes along
            let w = (cw + WIDTH).min(tw);
            let strip = strip(gpu, &mut self.row, target.format(), w, 1);
            encoder.copy_texture_to_texture(at(target, 0, ch - 1), at(strip, 0, 0), extent(w, 1));
            for y in ch..(ch + WIDTH).min(th) {
                encoder.copy_texture_to_texture(at(strip, 0, 0), at(target, 0, y), extent(w, 1));
            }
        }
    }
}

/// grow-only, so a drag doesn't make a texture per frame
fn strip<'a>(
    gpu: &Gpu,
    slot: &'a mut Option<wgpu::Texture>,
    format: wgpu::TextureFormat,
    w: u32,
    h: u32,
) -> &'a wgpu::Texture {
    let fits = slot
        .as_ref()
        .is_some_and(|t| t.format() == format && t.width() >= w && t.height() >= h);
    if !fits {
        let (ow, oh) = slot.as_ref().map_or((0, 0), |t| (t.width(), t.height()));
        *slot = Some(gpu.device.create_texture(&wgpu::TextureDescriptor {
            label: Some("edge strip"),
            size: extent(w.max(ow), h.max(oh)),
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format,
            usage: wgpu::TextureUsages::COPY_SRC | wgpu::TextureUsages::COPY_DST,
            view_formats: &[],
        }));
    }
    slot.as_ref().unwrap()
}

fn at(texture: &wgpu::Texture, x: u32, y: u32) -> wgpu::TexelCopyTextureInfo<'_> {
    wgpu::TexelCopyTextureInfo {
        texture,
        mip_level: 0,
        origin: wgpu::Origin3d { x, y, z: 0 },
        aspect: wgpu::TextureAspect::All,
    }
}
