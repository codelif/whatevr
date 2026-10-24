//! glyph pages: r8 coverage for outlines, rgba8 for colour glyphs. read
//! with textureLoad at whole pixels, so no padding. when a format runs out of
//! room the glyphs unused for longest go first; anything touched this frame
//! stays, and a new page opens instead.

use std::cell::Cell;
use std::collections::HashMap;

use etagere::{AllocId, AtlasAllocator, size2};
use log::{debug, warn};

use crate::Gpu;
use crate::atlas::extent;

const PAGE: u32 = 1024;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum GlyphFormat {
    /// one byte of coverage per pixel
    Mask,
    /// rgba8, srgb, premultiplied
    Rgba,
}

impl GlyphFormat {
    fn texture(self) -> wgpu::TextureFormat {
        match self {
            Self::Mask => wgpu::TextureFormat::R8Unorm,
            Self::Rgba => wgpu::TextureFormat::Rgba8Unorm,
        }
    }

    fn bytes(self) -> u32 {
        match self {
            Self::Mask => 1,
            Self::Rgba => 4,
        }
    }
}

/// a rasterized glyph. `left` is px from the pen to the first column, `top`
/// px from the baseline up to the first row. rows tightly packed.
#[derive(Clone, Copy, Debug)]
pub struct GlyphImage<'a> {
    pub left: i32,
    pub top: i32,
    pub width: u32,
    pub height: u32,
    pub format: GlyphFormat,
    pub data: &'a [u8],
}

impl GlyphImage<'_> {
    /// a glyph with no ink, like a space, so the key reads as known
    pub const EMPTY: GlyphImage<'static> = GlyphImage {
        left: 0,
        top: 0,
        width: 0,
        height: 0,
        format: GlyphFormat::Mask,
        data: &[],
    };
}

pub(crate) struct Page {
    format: GlyphFormat,
    alloc: AtlasAllocator,
    texture: wgpu::Texture,
    pub bind_group: wgpu::BindGroup,
}

pub(crate) struct Entry {
    /// none for an empty glyph
    pub page: Option<usize>,
    alloc: Option<AllocId>,
    pub x: u32,
    pub y: u32,
    pub w: u32,
    pub h: u32,
    pub left: i32,
    pub top: i32,
    pub format: GlyphFormat,
    used: Cell<u64>,
}

pub(crate) struct Glyphs {
    pub pages: Vec<Page>,
    entries: HashMap<u128, Entry>,
    layout: wgpu::BindGroupLayout,
    frame: u64,
}

impl Glyphs {
    pub fn new(layout: wgpu::BindGroupLayout) -> Self {
        Self {
            pages: Vec::new(),
            entries: HashMap::new(),
            layout,
            frame: 0,
        }
    }

    /// marks the glyph used this frame, so making room can't drop it
    pub fn get(&self, key: u128) -> Option<&Entry> {
        let e = self.entries.get(&key)?;
        e.used.set(self.frame);
        Some(e)
    }

    pub fn end_frame(&mut self) {
        self.frame += 1;
    }

    pub fn add(&mut self, gpu: &Gpu, key: u128, img: GlyphImage<'_>) {
        let (w, h) = (img.width, img.height);
        let mut entry = Entry {
            page: None,
            alloc: None,
            x: 0,
            y: 0,
            w,
            h,
            left: img.left,
            top: img.top,
            format: img.format,
            used: Cell::new(self.frame),
        };
        self.remove(key);
        if w > 0 && h > 0 {
            if img.data.len() != (w * h * img.format.bytes()) as usize {
                warn!("glyph {key:#x}: {} bytes for {w}x{h}", img.data.len());
                return;
            }
            if w > PAGE || h > PAGE {
                warn!("glyph {key:#x} is {w}x{h}, bigger than a page");
                return;
            }
            let (page, id, x, y) = self.allocate(gpu, img.format, w, h);
            gpu.queue.write_texture(
                wgpu::TexelCopyTextureInfo {
                    texture: &self.pages[page].texture,
                    mip_level: 0,
                    origin: wgpu::Origin3d { x, y, z: 0 },
                    aspect: wgpu::TextureAspect::All,
                },
                img.data,
                wgpu::TexelCopyBufferLayout {
                    offset: 0,
                    bytes_per_row: Some(w * img.format.bytes()),
                    rows_per_image: Some(h),
                },
                extent(w, h),
            );
            entry.page = Some(page);
            entry.alloc = Some(id);
            entry.x = x;
            entry.y = y;
        }
        self.entries.insert(key, entry);
    }

    fn remove(&mut self, key: u128) {
        if let Some(e) = self.entries.remove(&key)
            && let (Some(p), Some(a)) = (e.page, e.alloc)
        {
            self.pages[p].alloc.deallocate(a);
        }
    }

    fn allocate(
        &mut self,
        gpu: &Gpu,
        format: GlyphFormat,
        w: u32,
        h: u32,
    ) -> (usize, AllocId, u32, u32) {
        let want = size2(w as i32, h as i32);
        let place = |pages: &mut [Page], i: usize| {
            pages[i]
                .alloc
                .allocate(want)
                .map(|a| (i, a.id, a.rectangle.min.x as u32, a.rectangle.min.y as u32))
        };
        for i in 0..self.pages.len() {
            if self.pages[i].format == format
                && let Some(got) = place(&mut self.pages, i)
            {
                return got;
            }
        }

        // free the stalest glyphs of this format until one page has room
        let mut stale: Vec<(u64, u128)> = self
            .entries
            .iter()
            .filter(|(_, e)| e.format == format && e.page.is_some() && e.used.get() < self.frame)
            .map(|(k, e)| (e.used.get(), *k))
            .collect();
        stale.sort_unstable();
        let mut freed = 0;
        for (_, key) in stale {
            let page = self.entries[&key].page.unwrap();
            self.remove(key);
            freed += 1;
            if let Some(got) = place(&mut self.pages, page) {
                debug!("glyph atlas evicted {freed} glyphs");
                return got;
            }
        }

        let i = self.pages.len();
        self.pages.push(self.make_page(gpu, format));
        debug!("glyph atlas page {i} ({format:?})");
        place(&mut self.pages, i).expect("a glyph up to a page fits an empty page")
    }

    fn make_page(&self, gpu: &Gpu, format: GlyphFormat) -> Page {
        let texture = gpu.device.create_texture(&wgpu::TextureDescriptor {
            label: Some("glyph page"),
            size: extent(PAGE, PAGE),
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: format.texture(),
            usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
            view_formats: &[],
        });
        let view = texture.create_view(&wgpu::TextureViewDescriptor::default());
        let bind_group = gpu.device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("glyph page"),
            layout: &self.layout,
            entries: &[wgpu::BindGroupEntry {
                binding: 0,
                resource: wgpu::BindingResource::TextureView(&view),
            }],
        });
        Page {
            format,
            alloc: AtlasAllocator::new(size2(PAGE as i32, PAGE as i32)),
            texture,
            bind_group,
        }
    }
}
