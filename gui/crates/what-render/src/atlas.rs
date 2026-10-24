//! image pages. small images pack into shared 2048 px pages that repack into
//! 4096 px when full; big ones get a texture of their own. every packed
//! image carries a 1 px border copied from its own edge so filtering never
//! bleeds a neighbour in.

use std::collections::HashMap;

use etagere::{AllocId, AtlasAllocator, size2};
use log::debug;
use what_core::PhysicalSize;

use crate::{Gpu, ImageId};

const PAGE: u32 = 2048;
const PAGE_MAX: u32 = 4096;
/// bigger than this on either side and it gets its own texture
const OWN: u32 = 1024;
const FORMAT: wgpu::TextureFormat = wgpu::TextureFormat::Rgba8Unorm;

/// how the rgba8 bytes handed to `add_image` are encoded. both are srgb.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Alpha {
    /// what decoders give you: colour not multiplied by alpha
    Straight,
    /// already premultiplied, in srgb space like browsers do
    Premultiplied,
}

pub(crate) struct Page {
    texture: wgpu::Texture,
    pub bind_group: wgpu::BindGroup,
    width: u32,
    height: u32,
    /// none for an image that owns its page
    alloc: Option<AtlasAllocator>,
}

#[derive(Clone, Copy)]
struct Entry {
    page: usize,
    /// the image itself, inside its 1 px border
    x: u32,
    y: u32,
    w: u32,
    h: u32,
    alloc: Option<AllocId>,
}

pub(crate) struct Atlas {
    pub pages: Vec<Option<Page>>,
    entries: HashMap<ImageId, Entry>,
    next: u32,
    layout: wgpu::BindGroupLayout,
    sampler: wgpu::Sampler,
    max_page: u32,
}

/// where to sample an image from
pub(crate) struct Slot {
    pub page: usize,
    pub uv: [f32; 4],
}

impl Atlas {
    pub fn new(gpu: &Gpu, layout: wgpu::BindGroupLayout) -> Self {
        let sampler = gpu.device.create_sampler(&wgpu::SamplerDescriptor {
            label: Some("atlas"),
            mag_filter: wgpu::FilterMode::Linear,
            min_filter: wgpu::FilterMode::Linear,
            ..Default::default()
        });
        Self {
            pages: Vec::new(),
            entries: HashMap::new(),
            next: 0,
            layout,
            sampler,
            max_page: PAGE_MAX.min(gpu.device.limits().max_texture_dimension_2d),
        }
    }

    pub fn add(&mut self, gpu: &Gpu, size: PhysicalSize, rgba: &[u8], alpha: Alpha) -> ImageId {
        let (w, h) = (size.width.max(1), size.height.max(1));
        assert_eq!(
            rgba.len(),
            (size.width * size.height * 4) as usize,
            "image bytes don't match its size"
        );
        let id = ImageId(self.next);
        self.next += 1;
        let pixels = premultiplied(rgba, alpha);

        let entry = if w > OWN || h > OWN {
            let page = self.push_page(gpu, w, h, None);
            write(gpu, &self.page(page).texture, 0, 0, w, h, &pixels);
            Entry {
                page,
                x: 0,
                y: 0,
                w,
                h,
                alloc: None,
            }
        } else {
            let (page, alloc, x, y) = self.allocate(gpu, w + 2, h + 2);
            write(
                gpu,
                &self.page(page).texture,
                x,
                y,
                w + 2,
                h + 2,
                &pad(&pixels, w, h),
            );
            Entry {
                page,
                x: x + 1,
                y: y + 1,
                w,
                h,
                alloc: Some(alloc),
            }
        };
        self.entries.insert(id, entry);
        id
    }

    pub fn remove(&mut self, id: ImageId) {
        let Some(e) = self.entries.remove(&id) else {
            return;
        };
        match e.alloc {
            Some(a) => {
                if let Some(alloc) = self.pages[e.page].as_mut().and_then(|p| p.alloc.as_mut()) {
                    alloc.deallocate(a);
                }
            }
            None => self.pages[e.page] = None,
        }
    }

    pub fn slot(&self, id: ImageId) -> Option<Slot> {
        let e = self.entries.get(&id)?;
        let p = self.page(e.page);
        let (pw, ph) = (p.width as f32, p.height as f32);
        Some(Slot {
            page: e.page,
            uv: [
                e.x as f32 / pw,
                e.y as f32 / ph,
                (e.x + e.w) as f32 / pw,
                (e.y + e.h) as f32 / ph,
            ],
        })
    }

    fn page(&self, i: usize) -> &Page {
        self.pages[i].as_ref().expect("live page")
    }

    /// first page with room, else grow one, else a new page
    fn allocate(&mut self, gpu: &Gpu, w: u32, h: u32) -> (usize, AllocId, u32, u32) {
        let want = size2(w as i32, h as i32);
        for (i, p) in self.pages.iter_mut().enumerate() {
            if let Some(a) = p.as_mut().and_then(|p| p.alloc.as_mut())
                && let Some(got) = a.allocate(want)
            {
                return (
                    i,
                    got.id,
                    got.rectangle.min.x as u32,
                    got.rectangle.min.y as u32,
                );
            }
        }
        let growable = self.pages.iter().position(|p| {
            p.as_ref()
                .is_some_and(|p| p.alloc.is_some() && p.width < self.max_page)
        });
        if let Some(i) = growable
            && self.grow(gpu, i)
            && let Some(got) = self.pages[i]
                .as_mut()
                .unwrap()
                .alloc
                .as_mut()
                .unwrap()
                .allocate(want)
        {
            return (
                i,
                got.id,
                got.rectangle.min.x as u32,
                got.rectangle.min.y as u32,
            );
        }
        let size = PAGE.min(self.max_page);
        let alloc = AtlasAllocator::new(size2(size as i32, size as i32));
        let i = self.push_page(gpu, size, size, Some(alloc));
        let got = self.pages[i]
            .as_mut()
            .unwrap()
            .alloc
            .as_mut()
            .unwrap()
            .allocate(want)
            .expect("an image up to 1026 px fits an empty page");
        (
            i,
            got.id,
            got.rectangle.min.x as u32,
            got.rectangle.min.y as u32,
        )
    }

    /// etagere's allocator can't grow, so pack every image again into a bigger
    /// one and copy them over on the gpu. false if something didn't fit.
    fn grow(&mut self, gpu: &Gpu, i: usize) -> bool {
        let size = self.max_page;
        let mut alloc = AtlasAllocator::new(size2(size as i32, size as i32));
        let mut moves = Vec::new();
        for (id, e) in self
            .entries
            .iter()
            .filter(|(_, e)| e.page == i && e.alloc.is_some())
        {
            let Some(got) = alloc.allocate(size2(e.w as i32 + 2, e.h as i32 + 2)) else {
                return false;
            };
            moves.push((*id, *e, got));
        }
        let old = self.pages[i].take().expect("live page");
        let new = self.make_page(gpu, size, size, Some(alloc));
        let mut enc = gpu
            .device
            .create_command_encoder(&wgpu::CommandEncoderDescriptor {
                label: Some("atlas grow"),
            });
        for (id, e, got) in moves {
            let (x, y) = (got.rectangle.min.x as u32, got.rectangle.min.y as u32);
            enc.copy_texture_to_texture(
                texel(&old.texture, e.x - 1, e.y - 1),
                texel(&new.texture, x, y),
                extent(e.w + 2, e.h + 2),
            );
            self.entries.insert(
                id,
                Entry {
                    x: x + 1,
                    y: y + 1,
                    alloc: Some(got.id),
                    ..e
                },
            );
        }
        gpu.queue.submit([enc.finish()]);
        debug!("atlas page {i} grew to {size}");
        self.pages[i] = Some(new);
        true
    }

    fn push_page(&mut self, gpu: &Gpu, w: u32, h: u32, alloc: Option<AtlasAllocator>) -> usize {
        let page = self.make_page(gpu, w, h, alloc);
        match self.pages.iter().position(Option::is_none) {
            Some(i) => {
                self.pages[i] = Some(page);
                i
            }
            None => {
                self.pages.push(Some(page));
                self.pages.len() - 1
            }
        }
    }

    fn make_page(&self, gpu: &Gpu, width: u32, height: u32, alloc: Option<AtlasAllocator>) -> Page {
        let texture = gpu.device.create_texture(&wgpu::TextureDescriptor {
            label: Some("atlas page"),
            size: extent(width, height),
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: FORMAT,
            usage: wgpu::TextureUsages::TEXTURE_BINDING
                | wgpu::TextureUsages::COPY_DST
                | wgpu::TextureUsages::COPY_SRC,
            view_formats: &[],
        });
        let view = texture.create_view(&wgpu::TextureViewDescriptor::default());
        let bind_group = gpu.device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("atlas page"),
            layout: &self.layout,
            entries: &[
                wgpu::BindGroupEntry {
                    binding: 0,
                    resource: wgpu::BindingResource::TextureView(&view),
                },
                wgpu::BindGroupEntry {
                    binding: 1,
                    resource: wgpu::BindingResource::Sampler(&self.sampler),
                },
            ],
        });
        Page {
            texture,
            bind_group,
            width,
            height,
            alloc,
        }
    }
}

fn write(gpu: &Gpu, tex: &wgpu::Texture, x: u32, y: u32, w: u32, h: u32, bytes: &[u8]) {
    gpu.queue.write_texture(
        texel(tex, x, y),
        bytes,
        wgpu::TexelCopyBufferLayout {
            offset: 0,
            bytes_per_row: Some(w * 4),
            rows_per_image: Some(h),
        },
        extent(w, h),
    );
}

fn texel(texture: &wgpu::Texture, x: u32, y: u32) -> wgpu::TexelCopyTextureInfo<'_> {
    wgpu::TexelCopyTextureInfo {
        texture,
        mip_level: 0,
        origin: wgpu::Origin3d { x, y, z: 0 },
        aspect: wgpu::TextureAspect::All,
    }
}

pub(crate) fn extent(width: u32, height: u32) -> wgpu::Extent3d {
    wgpu::Extent3d {
        width,
        height,
        depth_or_array_layers: 1,
    }
}

/// the image with its outermost pixels repeated once all round
fn pad(px: &[u8], w: u32, h: u32) -> Vec<u8> {
    let (w, h) = (w as usize, h as usize);
    let pw = w + 2;
    let mut out = vec![0; pw * (h + 2) * 4];
    for y in 0..h + 2 {
        let sy = y.saturating_sub(1).min(h - 1);
        for x in 0..pw {
            let sx = x.saturating_sub(1).min(w - 1);
            let s = (sy * w + sx) * 4;
            let d = (y * pw + x) * 4;
            out[d..d + 4].copy_from_slice(&px[s..s + 4]);
        }
    }
    out
}

pub(crate) fn premultiplied(rgba: &[u8], alpha: Alpha) -> Vec<u8> {
    let mut out = rgba.to_vec();
    if alpha == Alpha::Premultiplied {
        return out;
    }
    for px in out.chunks_exact_mut(4) {
        match px[3] {
            255 => {}
            0 => px[..3].fill(0),
            a => {
                for c in &mut px[..3] {
                    *c = ((*c as u32 * a as u32 + 127) / 255) as u8;
                }
            }
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn premultiply_in_srgb() {
        let px = premultiplied(
            &[255, 255, 255, 128, 10, 20, 30, 255, 9, 9, 9, 0],
            Alpha::Straight,
        );
        assert_eq!(&px[..4], &[128, 128, 128, 128]);
        assert_eq!(&px[4..8], &[10, 20, 30, 255]);
        assert_eq!(&px[8..], &[0, 0, 0, 0]);
    }

    #[test]
    fn padding_repeats_edges() {
        let px = [1, 1, 1, 1, 2, 2, 2, 2];
        let out = pad(&px, 2, 1);
        let first: Vec<u8> = out.chunks(4).map(|p| p[0]).collect();
        assert_eq!(first, [1, 1, 2, 2, 1, 1, 2, 2, 1, 1, 2, 2]);
    }
}
