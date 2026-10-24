//! COLR paint graphs on the cpu, in srgb space like browsers. layers stack
//! premultiplied; gradient stops mix straight, then premultiply, like skia
//! and firefox (the spec says premultiplied linear light).

use skrifa::color::{
    Brush, ColorGlyph, ColorGlyphCollection, ColorPainter, ColorPalettes, ColorStop, CompositeMode,
    Extend, Transform,
};
use skrifa::instance::{LocationRef, Size};
use skrifa::outline::{DrawSettings, OutlineGlyphCollection, OutlinePen};
use skrifa::raw::TableProvider;
use skrifa::raw::types::BoundingBox;
use skrifa::{FontRef, GlyphId, MetadataProvider};

use super::accum::Accumulator;
use super::{Raster, encode};
use crate::GlyphKind;

/// palette index for "the text's colour"
const FOREGROUND: u16 = 0xffff;
/// bigger than this is a broken font, not an emoji
const MAX_SIDE: i32 = 4096;

pub(crate) fn has(font: &FontRef, glyph: GlyphId) -> bool {
    ColorGlyphCollection::new(font).get(glyph).is_some()
}

/// whether any fill in the graph uses the text's colour
pub(crate) fn uses_foreground(glyph: &ColorGlyph) -> bool {
    let mut f = Foreground(false);
    let _ = glyph.paint(LocationRef::default(), &mut f);
    f.0
}

/// `foreground` is the text colour, straight srgb, for palette entry 0xffff
pub(crate) fn render(
    font: &FontRef,
    glyph: GlyphId,
    size: f32,
    location: LocationRef,
    foreground: Option<[u8; 4]>,
) -> Option<Raster> {
    let color = ColorGlyphCollection::new(font).get(glyph)?;
    let upem = font.head().ok()?.units_per_em() as f32;
    let s = size / upem;
    let outlines = font.outline_glyphs();
    let palettes = ColorPalettes::new(font);
    let palette = palettes
        .get(0)
        .map(|p| p.colors().to_vec())
        .unwrap_or_default();

    // y up, px
    let bbox = match color.bounding_box(location, Size::new(size)) {
        Some(b) => b,
        None => {
            let mut b = Bounds {
                outlines: &outlines,
                location,
                transforms: vec![Transform::default()],
                bbox: None,
            };
            color.paint(location, &mut b).ok()?;
            let u = b.bbox?;
            BoundingBox {
                x_min: u.x_min * s,
                y_min: u.y_min * s,
                x_max: u.x_max * s,
                y_max: u.y_max * s,
            }
        }
    };
    let (x0, y0) = (bbox.x_min.floor() as i32, bbox.y_min.floor() as i32);
    let (x1, y1) = (bbox.x_max.ceil() as i32, bbox.y_max.ceil() as i32);
    let (w, h) = (x1 - x0, y1 - y0);
    if w <= 0 || h <= 0 || w > MAX_SIDE || h > MAX_SIDE {
        return None;
    }
    let device = Transform {
        xx: s,
        yx: 0.0,
        xy: 0.0,
        yy: -s,
        dx: -x0 as f32,
        dy: y1 as f32,
    };
    let mut p = Painter {
        w: w as usize,
        h: h as usize,
        outlines: &outlines,
        location,
        palette: &palette,
        foreground: foreground.unwrap_or([0, 0, 0, 255]),
        transforms: vec![device],
        clips: Vec::new(),
        layers: vec![(vec![[0.0; 4]; (w * h) as usize], CompositeMode::SrcOver)],
    };
    color.paint(location, &mut p).ok()?;
    let (base, _) = p.layers.swap_remove(0);
    let mut ink = false;
    let mut data = Vec::with_capacity(base.len() * 4);
    for px in base {
        ink |= px[3] > 0.0;
        data.extend_from_slice(&encode(px));
    }
    ink.then_some(Raster {
        left: x0,
        top: y1,
        width: w as u32,
        height: h as u32,
        kind: GlyphKind::Color,
        data,
    })
}

type Rgba = [f32; 4];

struct Painter<'a> {
    w: usize,
    h: usize,
    outlines: &'a OutlineGlyphCollection<'a>,
    location: LocationRef<'a>,
    palette: &'a [skrifa::color::Color],
    foreground: [u8; 4],
    /// font units to bitmap px, innermost last
    transforms: Vec<Transform>,
    /// coverage per px, already intersected with the clips below it
    clips: Vec<Vec<f32>>,
    /// premultiplied, srgb encoded
    layers: Vec<(Vec<Rgba>, CompositeMode)>,
}

impl Painter<'_> {
    fn transform(&self) -> Transform {
        *self.transforms.last().unwrap()
    }

    fn push_mask(&mut self, mut mask: Vec<f32>) {
        if let Some(outer) = self.clips.last() {
            for (m, o) in mask.iter_mut().zip(outer) {
                *m *= o;
            }
        }
        self.clips.push(mask);
    }

    fn rasterize(&self, merged: bool, draw: impl FnOnce(&mut MaskPen)) -> Vec<f32> {
        let acc = if merged {
            Accumulator::merged(self.w, self.h)
        } else {
            Accumulator::new(self.w, self.h)
        };
        let mut pen = MaskPen {
            acc,
            t: self.transform(),
        };
        draw(&mut pen);
        let mut mask = vec![0.0; self.w * self.h];
        pen.acc.finish(|i, c| mask[i] = c);
        mask
    }

    fn palette_color(&self, index: u16, alpha: f32) -> Rgba {
        let [r, g, b, a] = match self.palette.get(index as usize) {
            _ if index == FOREGROUND => self.foreground,
            Some(c) => [c.red, c.green, c.blue, c.alpha],
            None => [0, 0, 0, 255],
        };
        let a = a as f32 / 255.0 * alpha.clamp(0.0, 1.0);
        [r as f32 / 255.0, g as f32 / 255.0, b as f32 / 255.0, a]
    }
}

impl ColorPainter for Painter<'_> {
    fn push_transform(&mut self, t: Transform) {
        let cur = self.transform();
        self.transforms.push(cur * t);
    }

    fn pop_transform(&mut self) {
        if self.transforms.len() > 1 {
            self.transforms.pop();
        }
    }

    fn push_clip_glyph(&mut self, glyph: GlyphId) {
        let location = self.location;
        let mask = match self.outlines.get(glyph) {
            // shapes here often abut with opposite windings
            Some(o) => self.rasterize(true, |pen| {
                let _ = o.draw(DrawSettings::unhinted(Size::unscaled(), location), pen);
            }),
            None => vec![0.0; self.w * self.h],
        };
        self.push_mask(mask);
    }

    fn push_clip_box(&mut self, b: BoundingBox<f32>) {
        let mask = self.rasterize(false, |pen| {
            pen.move_to(b.x_min, b.y_min);
            pen.line_to(b.x_max, b.y_min);
            pen.line_to(b.x_max, b.y_max);
            pen.line_to(b.x_min, b.y_max);
            pen.close();
        });
        self.push_mask(mask);
    }

    fn pop_clip(&mut self) {
        self.clips.pop();
    }

    fn fill(&mut self, brush: Brush<'_>) {
        let Some(inv) = invert(self.transform()) else {
            return;
        };
        let paint = Paint::new(&brush, self);
        let (w, clip) = (self.w, self.clips.last());
        let layer = &mut self.layers.last_mut().unwrap().0;
        for (i, dst) in layer.iter_mut().enumerate() {
            let m = clip.map_or(1.0, |c| c[i]);
            if m == 0.0 {
                continue;
            }
            let (px, py) = ((i % w) as f32 + 0.5, (i / w) as f32 + 0.5);
            let (x, y) = inv.transform(px, py);
            let src = paint.at(x, y).map(|c| c * m);
            *dst = composite(CompositeMode::SrcOver, src, *dst);
        }
    }

    fn push_layer(&mut self, mode: CompositeMode) {
        self.layers.push((vec![[0.0; 4]; self.w * self.h], mode));
    }

    fn pop_layer(&mut self) {
        if self.layers.len() < 2 {
            return;
        }
        let (src, mode) = self.layers.pop().unwrap();
        let dst = &mut self.layers.last_mut().unwrap().0;
        for (d, s) in dst.iter_mut().zip(src) {
            *d = composite(mode, s, *d);
        }
    }
}

/// a brush resolved against the palette, ready to sample in brush space
enum Paint {
    Solid(Rgba),
    Gradient {
        kind: GradientKind,
        /// offset, straight colour
        stops: Vec<(f32, Rgba)>,
        extend: Extend,
    },
}

enum GradientKind {
    Linear {
        p0: (f32, f32),
        d: (f32, f32),
    },
    Radial {
        c0: (f32, f32),
        r0: f32,
        cd: (f32, f32),
        dr: f32,
    },
    Sweep {
        c: (f32, f32),
        start: f32,
        end: f32,
    },
}

impl Paint {
    fn new(brush: &Brush<'_>, p: &Painter<'_>) -> Self {
        let stops = |s: &[ColorStop]| -> Vec<(f32, Rgba)> {
            s.iter()
                .map(|s| (s.offset, p.palette_color(s.palette_index, s.alpha)))
                .collect()
        };
        match brush {
            Brush::Solid {
                palette_index,
                alpha,
            } => {
                let c = p.palette_color(*palette_index, *alpha);
                Self::Solid([c[0] * c[3], c[1] * c[3], c[2] * c[3], c[3]])
            }
            Brush::LinearGradient {
                p0,
                p1,
                color_stops,
                extend,
            } => Self::Gradient {
                kind: GradientKind::Linear {
                    p0: (p0.x, p0.y),
                    d: (p1.x - p0.x, p1.y - p0.y),
                },
                stops: stops(color_stops),
                extend: *extend,
            },
            Brush::RadialGradient {
                c0,
                r0,
                c1,
                r1,
                color_stops,
                extend,
            } => Self::Gradient {
                kind: GradientKind::Radial {
                    c0: (c0.x, c0.y),
                    r0: *r0,
                    cd: (c1.x - c0.x, c1.y - c0.y),
                    dr: r1 - r0,
                },
                stops: stops(color_stops),
                extend: *extend,
            },
            Brush::SweepGradient {
                c0,
                start_angle,
                end_angle,
                color_stops,
                extend,
            } => Self::Gradient {
                kind: GradientKind::Sweep {
                    c: (c0.x, c0.y),
                    start: *start_angle,
                    end: *end_angle,
                },
                stops: stops(color_stops),
                extend: *extend,
            },
        }
    }

    /// premultiplied, srgb space
    fn at(&self, x: f32, y: f32) -> Rgba {
        let Self::Gradient {
            kind,
            stops,
            extend,
        } = self
        else {
            let Self::Solid(c) = self else { unreachable!() };
            return *c;
        };
        let t = match *kind {
            GradientKind::Linear { p0, d } => {
                let len = d.0 * d.0 + d.1 * d.1;
                if len == 0.0 {
                    return [0.0; 4];
                }
                ((x - p0.0) * d.0 + (y - p0.1) * d.1) / len
            }
            GradientKind::Radial { c0, r0, cd, dr } => {
                match conical(x - c0.0, y - c0.1, r0, cd, dr) {
                    Some(t) => t,
                    None => return [0.0; 4],
                }
            }
            GradientKind::Sweep { c, start, end } => {
                // skrifa flips the font's angles to clockwise from +x, y up
                let a = (c.1 - y).atan2(x - c.0).to_degrees().rem_euclid(360.0);
                if end == start {
                    // spec: nothing for repeat and reflect; pad is a hard edge
                    if !matches!(extend, Extend::Pad) {
                        return [0.0; 4];
                    }
                    if a < start { 0.0 } else { 1.0 }
                } else {
                    (a - start) / (end - start)
                }
            }
        };
        color_line(stops, extend_t(t, *extend))
    }
}

/// largest t with |p - t*cd| = r0 + t*dr and a radius not below zero
fn conical(px: f32, py: f32, r0: f32, cd: (f32, f32), dr: f32) -> Option<f32> {
    let a = cd.0 * cd.0 + cd.1 * cd.1 - dr * dr;
    let b = px * cd.0 + py * cd.1 + r0 * dr;
    let c = px * px + py * py - r0 * r0;
    let ok = |t: f32| r0 + t * dr >= 0.0;
    if a.abs() < 1e-6 {
        if b == 0.0 {
            return None;
        }
        let t = c / (2.0 * b);
        return ok(t).then_some(t);
    }
    let disc = b * b - a * c;
    if disc < 0.0 {
        return None;
    }
    let sq = disc.sqrt();
    let (t1, t2) = ((b + sq) / a, (b - sq) / a);
    let (hi, lo) = if t1 > t2 { (t1, t2) } else { (t2, t1) };
    if ok(hi) {
        Some(hi)
    } else if ok(lo) {
        Some(lo)
    } else {
        None
    }
}

fn extend_t(t: f32, e: Extend) -> f32 {
    match e {
        Extend::Repeat => t - t.floor(),
        Extend::Reflect => {
            let m = t.rem_euclid(2.0);
            if m > 1.0 { 2.0 - m } else { m }
        }
        _ => t.clamp(0.0, 1.0),
    }
}

/// straight stops in, premultiplied out
fn color_line(stops: &[(f32, Rgba)], t: f32) -> Rgba {
    let (Some(first), Some(last)) = (stops.first(), stops.last()) else {
        return [0.0; 4];
    };
    let premul = |c: Rgba| [c[0] * c[3], c[1] * c[3], c[2] * c[3], c[3]];
    if t <= first.0 {
        return premul(first.1);
    }
    if t >= last.0 {
        return premul(last.1);
    }
    for pair in stops.windows(2) {
        let ((o0, c0), (o1, c1)) = (pair[0], pair[1]);
        if t <= o1 {
            let f = if o1 > o0 { (t - o0) / (o1 - o0) } else { 1.0 };
            return premul(std::array::from_fn(|i| c0[i] + (c1[i] - c0[i]) * f));
        }
    }
    premul(last.1)
}

/// w3c compositing and blending level 1, premultiplied in and out
fn composite(mode: CompositeMode, s: Rgba, d: Rgba) -> Rgba {
    use CompositeMode::*;
    let (sa, da) = (s[3], d[3]);
    let pd = |fs: f32, fd: f32| -> Rgba { std::array::from_fn(|i| s[i] * fs + d[i] * fd) };
    match mode {
        Clear => [0.0; 4],
        Src => s,
        Dest => d,
        SrcOver => pd(1.0, 1.0 - sa),
        DestOver => pd(1.0 - da, 1.0),
        SrcIn => pd(da, 0.0),
        DestIn => pd(0.0, sa),
        SrcOut => pd(1.0 - da, 0.0),
        DestOut => pd(0.0, 1.0 - sa),
        SrcAtop => pd(da, 1.0 - sa),
        DestAtop => pd(1.0 - da, sa),
        Xor => pd(1.0 - da, 1.0 - sa),
        Plus => std::array::from_fn(|i| (s[i] + d[i]).min(1.0)),
        Unknown => pd(1.0, 1.0 - sa),
        blend => {
            let straight = |c: Rgba| -> [f32; 3] {
                if c[3] > 0.0 {
                    [c[0] / c[3], c[1] / c[3], c[2] / c[3]]
                } else {
                    [0.0; 3]
                }
            };
            let (cs, cb) = (straight(s), straight(d));
            let b = blend_color(blend, cb, cs);
            let mut out = [0.0; 4];
            for i in 0..3 {
                out[i] = s[i] * (1.0 - da) + d[i] * (1.0 - sa) + sa * da * b[i];
            }
            out[3] = sa + da - sa * da;
            out
        }
    }
}

fn blend_color(mode: CompositeMode, cb: [f32; 3], cs: [f32; 3]) -> [f32; 3] {
    use CompositeMode::*;
    let sep = |f: fn(f32, f32) -> f32| -> [f32; 3] { std::array::from_fn(|i| f(cb[i], cs[i])) };
    match mode {
        Multiply => sep(|b, s| b * s),
        Screen => sep(screen),
        Overlay => sep(|b, s| hard_light(s, b)),
        Darken => sep(f32::min),
        Lighten => sep(f32::max),
        ColorDodge => sep(|b, s| {
            if b == 0.0 {
                0.0
            } else if s >= 1.0 {
                1.0
            } else {
                (b / (1.0 - s)).min(1.0)
            }
        }),
        ColorBurn => sep(|b, s| {
            if b >= 1.0 {
                1.0
            } else if s == 0.0 {
                0.0
            } else {
                1.0 - ((1.0 - b) / s).min(1.0)
            }
        }),
        HardLight => sep(hard_light),
        SoftLight => sep(|b, s| {
            if s <= 0.5 {
                b - (1.0 - 2.0 * s) * b * (1.0 - b)
            } else {
                let d = if b <= 0.25 {
                    ((16.0 * b - 12.0) * b + 4.0) * b
                } else {
                    b.sqrt()
                };
                b + (2.0 * s - 1.0) * (d - b)
            }
        }),
        Difference => sep(|b, s| (b - s).abs()),
        Exclusion => sep(|b, s| b + s - 2.0 * b * s),
        HslHue => set_lum(set_sat(cs, sat(cb)), lum(cb)),
        HslSaturation => set_lum(set_sat(cb, sat(cs)), lum(cb)),
        HslColor => set_lum(cs, lum(cb)),
        HslLuminosity => set_lum(cb, lum(cs)),
        _ => cs,
    }
}

fn screen(b: f32, s: f32) -> f32 {
    b + s - b * s
}

fn hard_light(b: f32, s: f32) -> f32 {
    if s <= 0.5 {
        b * 2.0 * s
    } else {
        screen(b, 2.0 * s - 1.0)
    }
}

fn lum(c: [f32; 3]) -> f32 {
    0.3 * c[0] + 0.59 * c[1] + 0.11 * c[2]
}

fn clip_color(c: [f32; 3]) -> [f32; 3] {
    let l = lum(c);
    let n = c[0].min(c[1]).min(c[2]);
    let x = c[0].max(c[1]).max(c[2]);
    c.map(|v| {
        let mut v = v;
        if n < 0.0 {
            v = l + (v - l) * l / (l - n);
        }
        if x > 1.0 {
            v = l + (v - l) * (1.0 - l) / (x - l);
        }
        v
    })
}

fn set_lum(c: [f32; 3], l: f32) -> [f32; 3] {
    let d = l - lum(c);
    clip_color(c.map(|v| v + d))
}

fn sat(c: [f32; 3]) -> f32 {
    c[0].max(c[1]).max(c[2]) - c[0].min(c[1]).min(c[2])
}

fn set_sat(c: [f32; 3], s: f32) -> [f32; 3] {
    let (max, min) = (c[0].max(c[1]).max(c[2]), c[0].min(c[1]).min(c[2]));
    if max <= min {
        return [0.0; 3];
    }
    c.map(|v| (v - min) * s / (max - min))
}

fn invert(t: Transform) -> Option<Transform> {
    let det = t.xx * t.yy - t.xy * t.yx;
    if det.abs() < 1e-12 {
        return None;
    }
    let (xx, xy, yx, yy) = (t.yy / det, -t.xy / det, -t.yx / det, t.xx / det);
    Some(Transform {
        xx,
        yx,
        xy,
        yy,
        dx: -(xx * t.dx + xy * t.dy),
        dy: -(yx * t.dx + yy * t.dy),
    })
}

/// a pen that maps font units through a transform into a coverage mask
struct MaskPen {
    acc: Accumulator,
    t: Transform,
}

impl OutlinePen for MaskPen {
    fn move_to(&mut self, x: f32, y: f32) {
        let (x, y) = self.t.transform(x, y);
        self.acc.move_to(x, y);
    }

    fn line_to(&mut self, x: f32, y: f32) {
        let (x, y) = self.t.transform(x, y);
        self.acc.line_to(x, y);
    }

    fn quad_to(&mut self, cx0: f32, cy0: f32, x: f32, y: f32) {
        let (cx0, cy0) = self.t.transform(cx0, cy0);
        let (x, y) = self.t.transform(x, y);
        self.acc.quad_to(cx0, cy0, x, y);
    }

    fn curve_to(&mut self, cx0: f32, cy0: f32, cx1: f32, cy1: f32, x: f32, y: f32) {
        let (cx0, cy0) = self.t.transform(cx0, cy0);
        let (cx1, cy1) = self.t.transform(cx1, cy1);
        let (x, y) = self.t.transform(x, y);
        self.acc.cubic_to(cx0, cy0, cx1, cy1, x, y);
    }

    fn close(&mut self) {
        self.acc.close();
    }
}

/// for glyphs without a clip box: the union of every clip glyph, font units
struct Bounds<'a> {
    outlines: &'a OutlineGlyphCollection<'a>,
    location: LocationRef<'a>,
    transforms: Vec<Transform>,
    bbox: Option<BoundingBox<f32>>,
}

struct BoxPen<'a> {
    t: Transform,
    bbox: &'a mut Option<BoundingBox<f32>>,
}

impl BoxPen<'_> {
    fn add(&mut self, x: f32, y: f32) {
        let (x, y) = self.t.transform(x, y);
        let b = self.bbox.get_or_insert(BoundingBox {
            x_min: x,
            y_min: y,
            x_max: x,
            y_max: y,
        });
        b.x_min = b.x_min.min(x);
        b.y_min = b.y_min.min(y);
        b.x_max = b.x_max.max(x);
        b.y_max = b.y_max.max(y);
    }
}

impl OutlinePen for BoxPen<'_> {
    fn move_to(&mut self, x: f32, y: f32) {
        self.add(x, y);
    }
    fn line_to(&mut self, x: f32, y: f32) {
        self.add(x, y);
    }
    fn quad_to(&mut self, cx0: f32, cy0: f32, x: f32, y: f32) {
        self.add(cx0, cy0);
        self.add(x, y);
    }
    fn curve_to(&mut self, cx0: f32, cy0: f32, cx1: f32, cy1: f32, x: f32, y: f32) {
        self.add(cx0, cy0);
        self.add(cx1, cy1);
        self.add(x, y);
    }
    fn close(&mut self) {}
}

impl ColorPainter for Bounds<'_> {
    fn push_transform(&mut self, t: Transform) {
        let cur = *self.transforms.last().unwrap();
        self.transforms.push(cur * t);
    }
    fn pop_transform(&mut self) {
        if self.transforms.len() > 1 {
            self.transforms.pop();
        }
    }
    fn push_clip_glyph(&mut self, glyph: GlyphId) {
        if let Some(o) = self.outlines.get(glyph) {
            let mut pen = BoxPen {
                t: *self.transforms.last().unwrap(),
                bbox: &mut self.bbox,
            };
            let _ = o.draw(
                DrawSettings::unhinted(Size::unscaled(), self.location),
                &mut pen,
            );
        }
    }
    fn push_clip_box(&mut self, _: BoundingBox<f32>) {}
    fn pop_clip(&mut self) {}
    fn fill(&mut self, _: Brush<'_>) {}
    fn push_layer(&mut self, _: CompositeMode) {}
}

struct Foreground(bool);

impl ColorPainter for Foreground {
    fn push_transform(&mut self, _: Transform) {}
    fn pop_transform(&mut self) {}
    fn push_clip_glyph(&mut self, _: GlyphId) {}
    fn push_clip_box(&mut self, _: BoundingBox<f32>) {}
    fn pop_clip(&mut self) {}
    fn fill(&mut self, brush: Brush<'_>) {
        self.0 |= match brush {
            Brush::Solid { palette_index, .. } => palette_index == FOREGROUND,
            Brush::LinearGradient { color_stops, .. }
            | Brush::RadialGradient { color_stops, .. }
            | Brush::SweepGradient { color_stops, .. } => {
                color_stops.iter().any(|s| s.palette_index == FOREGROUND)
            }
        };
    }
    fn push_layer(&mut self, _: CompositeMode) {}
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn porter_duff_basics() {
        let red = [1.0, 0.0, 0.0, 1.0];
        let half_blue = [0.0, 0.0, 0.5, 0.5];
        assert_eq!(
            composite(CompositeMode::SrcOver, half_blue, red),
            [0.5, 0.0, 0.5, 1.0]
        );
        assert_eq!(
            composite(CompositeMode::DestIn, half_blue, red),
            [0.5, 0.0, 0.0, 0.5]
        );
        assert_eq!(composite(CompositeMode::Clear, half_blue, red), [0.0; 4]);
    }

    #[test]
    fn multiply_on_opaque_matches_product() {
        let a = [0.5, 0.25, 1.0, 1.0];
        let b = [0.5, 1.0, 0.5, 1.0];
        assert_eq!(
            composite(CompositeMode::Multiply, a, b),
            [0.25, 0.25, 0.5, 1.0]
        );
    }

    #[test]
    fn radial_between_two_circles() {
        // concentric, r 10 -> 20: a point 15 out sits halfway
        let t = conical(15.0, 0.0, 10.0, (0.0, 0.0), 10.0).unwrap();
        assert!((t - 0.5).abs() < 1e-5, "{t}");
    }

    #[test]
    fn invert_round_trips() {
        let t = Transform {
            xx: 2.0,
            yx: 0.5,
            xy: -1.0,
            yy: 3.0,
            dx: 7.0,
            dy: -4.0,
        };
        let back = invert(t).unwrap() * t;
        for (g, w) in back.elements().iter().zip(Transform::IDENTITY.elements()) {
            assert!((g - w).abs() < 1e-5);
        }
    }
}
