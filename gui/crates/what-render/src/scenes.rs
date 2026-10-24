//! named test scenes, laid out in logical px and snapped. the golden tests,
//! tools/snapshot and `WHATGUI_SCENE` all draw these.

use what_core::{LogicalPoint, LogicalRect, LogicalSize, PhysicalSize, PremulRgba, Scale, Srgba8};

use crate::snap::{hairline, px, snap_rect};
use crate::{
    Alpha, Clip, Corners, Edges, Fill, Gpu, Image, ImageId, Quad, Renderer, Scene, Shadow,
};

pub const NAMES: &[&str] = &["borders", "fills", "shadows", "images", "clips", "layers"];

/// every scene draws exactly this logical area from the top left, whatever the target size
pub const SIZE: LogicalSize = LogicalSize::new(480.0, 480.0);

/// images the scenes draw, uploaded once
pub struct Assets {
    checker: ImageId,
    photo: ImageId,
    badge: ImageId,
}

impl Assets {
    pub fn new(renderer: &mut Renderer, gpu: &Gpu) -> Self {
        Self {
            checker: renderer.add_image(
                gpu,
                PhysicalSize::new(64, 64),
                &checker(),
                Alpha::Straight,
            ),
            photo: renderer.add_image(gpu, PhysicalSize::new(96, 64), &photo(), Alpha::Straight),
            badge: renderer.add_image(gpu, PhysicalSize::new(48, 48), &badge(), Alpha::Straight),
        }
    }
}

/// none for an unknown name
pub fn build(name: &str, assets: &Assets, scale: Scale) -> Option<Scene> {
    let size = SIZE;
    let mut b = Builder {
        scene: Scene::new(),
        scale,
    };
    match name {
        "borders" => borders(&mut b, size),
        "fills" => fills(&mut b, size),
        "shadows" => shadows(&mut b, size),
        "images" => images(&mut b, assets, size),
        "clips" => clips(&mut b, assets, size),
        "layers" => layers(&mut b, assets, size),
        _ => return None,
    }
    Some(b.scene)
}

const LIGHT: u32 = 0xf4f4f4;
const DARK: u32 = 0x202020;
const INK: u32 = 0x333333;

fn c(hex: u32) -> PremulRgba {
    Srgba8::hex(hex).premultiply()
}

fn ca(hex: u32, a: u8) -> PremulRgba {
    let s = Srgba8::hex(hex);
    Srgba8::new(s.r, s.g, s.b, a).premultiply()
}

struct Builder {
    scene: Scene,
    scale: Scale,
}

impl Builder {
    fn r(&self, x: f32, y: f32, w: f32, h: f32) -> crate::Rect {
        snap_rect(
            LogicalRect::new(LogicalPoint::new(x, y), LogicalSize::new(w, h)),
            self.scale,
        )
    }

    fn px(&self, v: f32) -> f32 {
        px(v, self.scale)
    }

    /// border widths snap like browsers do: whole pixels, at least one
    fn width(&self, v: f32) -> f32 {
        if v <= 0.0 {
            0.0
        } else {
            self.px(v).round().max(1.0)
        }
    }

    fn radii(&self, r: f32) -> Corners {
        Corners::all(self.px(r))
    }

    fn background(&mut self, x: f32, y: f32, w: f32, h: f32, hex: u32) {
        let rect = self.r(x, y, w, h);
        self.scene.quad(Quad {
            rect,
            fill: c(hex).into(),
            ..Default::default()
        });
    }
}

/// the step 2 exit test: 1 logical px borders and rules must stay crisp at any scale
fn borders(b: &mut Builder, size: LogicalSize) {
    b.background(0.0, 0.0, size.width, size.height, LIGHT);
    let hair = Edges::all(hairline(b.scale));
    // odd logical offsets land between physical pixels at fractional scales
    for (i, x) in [12.0, 70.3, 128.5, 186.7, 244.2, 302.9, 360.4, 418.6]
        .iter()
        .enumerate()
    {
        let rect = b.r(*x, 12.0 + (i % 3) as f32 * 0.4, 50.0, 30.0);
        b.scene.quad(Quad {
            rect,
            fill: c(0xffffff).into(),
            border: hair,
            border_color: c(INK),
            ..Default::default()
        });
    }
    for (i, r) in [2.0, 4.0, 8.0, 12.0, 15.0].iter().enumerate() {
        let rect = b.r(12.0 + i as f32 * 70.0, 56.0, 60.0, 30.0);
        let radii = b.radii(*r);
        b.scene.quad(Quad {
            rect,
            radii,
            fill: c(0xffffff).into(),
            border: hair,
            border_color: c(INK),
            ..Default::default()
        });
    }
    // per side widths; the last two have elliptical inner corners
    let sides = [
        Edges {
            bottom: hairline(b.scale),
            ..Default::default()
        },
        Edges {
            left: b.width(3.0),
            ..hair
        },
        Edges {
            top: b.width(4.0),
            right: hair.right,
            bottom: hair.bottom,
            left: hair.left,
        },
        Edges {
            top: b.width(1.0),
            right: b.width(6.0),
            bottom: b.width(2.0),
            left: b.width(6.0),
        },
    ];
    for (i, e) in sides.into_iter().enumerate() {
        let rect = b.r(12.0 + i as f32 * 90.0, 100.0, 76.0, 40.0);
        let radii = b.radii(if i < 2 { 0.0 } else { 12.0 });
        b.scene.quad(Quad {
            rect,
            radii,
            fill: c(0xffffff).into(),
            border: e,
            border_color: c(0x3b6fd8),
            ..Default::default()
        });
    }
    // hairline rules at fractional y, then the same on dark
    b.background(0.0, 156.0, size.width, size.height - 156.0, DARK);
    for i in 0..6 {
        let y = 164.0 + i as f32 * 7.3;
        let rect = b.r(12.0, y, 200.0, 0.0);
        let rect = crate::Rect {
            h: hairline(b.scale),
            ..rect
        };
        b.scene.quad(Quad {
            rect,
            fill: c(0xe8e8e8).into(),
            ..Default::default()
        });
    }
    for (i, x) in [228.0, 290.6, 352.25].iter().enumerate() {
        let rect = b.r(*x, 164.0, 54.0, 36.0);
        let radii = b.radii(i as f32 * 6.0);
        b.scene.quad(Quad {
            rect,
            radii,
            fill: c(0x2c2c2c).into(),
            border: hair,
            border_color: c(0xe8e8e8),
            ..Default::default()
        });
    }
}

/// left half white, right half black; every gradient is drawn on both
fn fills(b: &mut Builder, size: LogicalSize) {
    let half = size.width / 2.0;
    b.background(0.0, 0.0, half, size.height, 0xffffff);
    b.background(half, 0.0, half, size.height, 0x000000);
    // see-through bars straddling the split
    for (i, (hex, a)) in [(0x000000, 128), (0xffffff, 128), (0x3b6fd8, 160)]
        .iter()
        .enumerate()
    {
        let rect = b.r(half - 60.0, 12.0 + i as f32 * 36.0, 120.0, 28.0);
        let radii = b.radii(8.0);
        b.scene.quad(Quad {
            rect,
            radii,
            fill: ca(*hex, *a).into(),
            ..Default::default()
        });
    }
    let grads = [
        Fill::Linear {
            from: [0.0, 0.0],
            to: [0.0, 1.0],
            start: c(0x000000),
            end: c(0xffffff),
        },
        Fill::Linear {
            from: [0.0, 0.0],
            to: [1.0, 0.0],
            start: c(0x2040ff),
            end: c(0xffe040),
        },
        Fill::Linear {
            from: [0.0, 0.0],
            to: [1.0, 1.0],
            start: c(0xff4080),
            end: ca(0xff4080, 0),
        },
        Fill::Radial {
            center: [0.5, 0.5],
            radius: [0.5, 0.5],
            start: c(0xffffff),
            end: c(0x3b6fd8),
        },
        Fill::Radial {
            center: [0.2, 0.3],
            radius: [0.9, 0.6],
            start: ca(0xffe040, 255),
            end: ca(0xffe040, 0),
        },
    ];
    for side in 0..2 {
        for (i, fill) in grads.iter().enumerate() {
            let x = side as f32 * half + 12.0 + (i % 2) as f32 * 114.0;
            let y = 132.0 + (i / 2) as f32 * 84.0;
            let rect = b.r(x, y, 102.0, 72.0);
            let radii = b.radii(if i == 3 { 36.0 } else { 6.0 });
            b.scene.quad(Quad {
                rect,
                radii,
                fill: *fill,
                ..Default::default()
            });
        }
    }
}

fn shadows(b: &mut Builder, size: LogicalSize) {
    let half = size.width / 2.0;
    b.background(0.0, 0.0, half, size.height, 0xeeeeee);
    b.background(half, 0.0, half, size.height, 0x1a1a1a);
    // shadows draw before quads inside a layer, so the backgrounds need their own
    b.scene.layer();
    // offset, spread, blur, alpha, radius
    let specs: [([f32; 2], f32, f32, u8, f32); 6] = [
        ([0.0, 1.0], 0.0, 3.0, 77, 8.0),
        ([0.0, 8.0], 0.0, 24.0, 64, 12.0),
        ([0.0, 0.0], 6.0, 0.0, 90, 8.0),
        ([0.0, 10.0], -4.0, 12.0, 128, 4.0),
        ([4.0, 4.0], 0.0, 8.0, 160, 0.0),
        ([0.0, 2.0], 2.0, 6.0, 110, 26.0),
    ];
    for side in 0..2 {
        for (i, (offset, spread, blur, alpha, radius)) in specs.iter().enumerate() {
            let x = side as f32 * half + 24.0 + (i % 3) as f32 * 76.0;
            let y = 28.0 + (i / 3) as f32 * 120.0;
            let rect = b.r(x, y, 52.0, 52.0);
            let radii = b.radii(*radius);
            let card = if side == 0 { 0xffffff } else { 0x2c2c2c };
            b.scene.shadow(Shadow {
                rect,
                radii,
                offset: [b.px(offset[0]), b.px(offset[1])],
                spread: b.px(*spread),
                blur: b.px(*blur),
                color: ca(0x000000, *alpha),
                ..Default::default()
            });
            // the last card is see-through: no shadow may show under it
            let fill = if i == 5 { ca(card, 96) } else { c(card) };
            b.scene.quad(Quad {
                rect,
                radii,
                fill: fill.into(),
                ..Default::default()
            });
        }
    }
}

/// left half light, right half dark, the same images on each
fn images(b: &mut Builder, a: &Assets, size: LogicalSize) {
    let half = size.width / 2.0;
    b.background(0.0, 0.0, half, size.height, LIGHT);
    b.background(half, 0.0, half, size.height, DARK);
    let s = b.scale.as_f32();
    for side in 0..2 {
        let x = side as f32 * half + 12.0;
        // 1:1 in physical pixels, half that, and twice
        let rect = b.r(x, 12.0, 64.0 / s, 64.0 / s);
        b.scene.image(Image::new(rect, a.checker));
        let rect = b.r(x + 76.0, 12.0, 32.0 / s, 32.0 / s);
        b.scene.image(Image::new(rect, a.checker));
        let rect = b.r(x, 88.0, 128.0 / s, 128.0 / s);
        b.scene.image(Image::new(rect, a.checker));
        // rounded photo, round avatar, half transparent copy, straight alpha badge
        let rect = b.r(x, 228.0, 96.0, 64.0);
        let radii = b.radii(10.0);
        b.scene.image(Image {
            radii,
            ..Image::new(rect, a.photo)
        });
        let rect = b.r(x + 112.0, 228.0, 64.0, 64.0);
        let radii = b.radii(32.0);
        b.scene.image(Image {
            radii,
            ..Image::new(rect, a.photo)
        });
        let rect = b.r(x, 304.0, 96.0, 64.0);
        b.scene.image(Image {
            opacity: 0.5,
            ..Image::new(rect, a.photo)
        });
        let rect = b.r(x + 112.0, 312.0, 48.0, 48.0);
        b.scene.image(Image::new(rect, a.badge));
    }
}

fn clips(b: &mut Builder, a: &Assets, size: LogicalSize) {
    b.background(0.0, 0.0, size.width, size.height, DARK);
    // a scrolled chat column: bubbles run past the rounded viewport and get cut
    let view = b.r(24.0, 24.0, 220.0, size.height - 48.0);
    let clip = Clip {
        rect: view,
        radii: b.radii(16.0),
    };
    b.scene.quad(Quad {
        rect: view,
        radii: clip.radii,
        fill: c(0x2c2c2c).into(),
        ..Default::default()
    });
    for i in 0..6 {
        let mine = i % 2 == 1;
        let x = if mine { 104.0 } else { 12.0 };
        let rect = b.r(x, -18.0 + i as f32 * 56.0, 150.0, 44.0);
        let radii = b.radii(12.0);
        b.scene.quad(Quad {
            rect,
            radii,
            fill: c(if mine { 0x2f5fc4 } else { 0x3a3a3a }).into(),
            clip,
            ..Default::default()
        });
    }
    let rect = b.r(150.0, size.height - 90.0, 120.0, 80.0);
    b.scene.image(Image {
        clip,
        ..Image::new(rect, a.photo)
    });
    // plain rect clip on a gradient
    let rect = b.r(270.0, 24.0, 180.0, 120.0);
    let cut = b.r(290.0, 44.0, 140.0, 60.0);
    b.scene.quad(Quad {
        rect,
        fill: Fill::Linear {
            from: [0.0, 0.0],
            to: [1.0, 0.0],
            start: c(0xff4080),
            end: c(0x40c0ff),
        },
        clip: Clip::rect(cut),
        ..Default::default()
    });
}

fn layers(b: &mut Builder, a: &Assets, size: LogicalSize) {
    b.background(0.0, 0.0, size.width, size.height, LIGHT);
    // same layer: the quad pushed after the image still lands under it
    let img = b.r(24.0, 24.0, 96.0, 64.0);
    b.scene.image(Image::new(img, a.photo));
    let under = b.r(72.0, 56.0, 96.0, 64.0);
    b.scene.quad(Quad {
        rect: under,
        fill: c(0x3b6fd8).into(),
        ..Default::default()
    });
    // next layer: on top of both, shadow included
    b.scene.layer();
    let top = b.r(48.0, 40.0, 96.0, 64.0);
    let radii = b.radii(8.0);
    b.scene.shadow(Shadow {
        rect: top,
        radii,
        offset: [0.0, b.px(4.0)],
        blur: b.px(12.0),
        color: ca(0x000000, 96),
        ..Default::default()
    });
    b.scene.quad(Quad {
        rect: top,
        radii,
        fill: ca(0xffffff, 230).into(),
        border: Edges::all(hairline(b.scale)),
        border_color: c(INK),
        ..Default::default()
    });
}

fn checker() -> Vec<u8> {
    let mut out = Vec::with_capacity(64 * 64 * 4);
    for y in 0..64u32 {
        for x in 0..64u32 {
            let on = ((x / 8) + (y / 8)) % 2 == 0;
            // a 1 px frame so scaling and edge padding show up
            let edge = x == 0 || y == 0 || x == 63 || y == 63;
            let p = match (edge, on) {
                (true, _) => [0xd8, 0x30, 0x60, 255],
                (_, true) => [0x20, 0x20, 0x20, 255],
                _ => [0xf0, 0xf0, 0xf0, 255],
            };
            out.extend_from_slice(&p);
        }
    }
    out
}

/// smooth colour field, shows filtering when scaled
fn photo() -> Vec<u8> {
    let mut out = Vec::with_capacity(96 * 64 * 4);
    for y in 0..64u32 {
        for x in 0..96u32 {
            let r = (x * 255 / 95) as u8;
            let g = (y * 255 / 63) as u8;
            let b = 255 - ((x + y) * 255 / 158) as u8;
            out.extend_from_slice(&[r, g, b, 255]);
        }
    }
    out
}

/// straight alpha disc with a soft rim
fn badge() -> Vec<u8> {
    let mut out = Vec::with_capacity(48 * 48 * 4);
    for y in 0..48 {
        for x in 0..48 {
            let d = ((x as f32 + 0.5 - 24.0).powi(2) + (y as f32 + 0.5 - 24.0).powi(2)).sqrt();
            let a = ((22.0 - d) / 6.0).clamp(0.0, 1.0);
            out.extend_from_slice(&[0x40, 0xc0, 0x70, (a * 255.0).round() as u8]);
        }
    }
    out
}
