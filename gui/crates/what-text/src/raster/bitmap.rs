//! CBDT colour bitmaps (Noto Color Emoji), shrunk to the exact size by
//! averaging the area each output pixel covers, in premultiplied linear light.
//! the result goes back to premultiplied srgb for the atlas.

use skrifa::bitmap::{BitmapData, BitmapStrikes, Origin};
use skrifa::instance::Size;
use skrifa::{FontRef, GlyphId};
use zune_png::PngDecoder;
use zune_png::zune_core::bytestream::ZCursor;
use zune_png::zune_core::options::DecoderOptions;

use super::{Raster, TO_LINEAR, encode, linear_to_srgb};
use crate::GlyphKind;

pub(crate) fn render(font: &FontRef, glyph: GlyphId, size: f32) -> Option<Raster> {
    let g = BitmapStrikes::new(font).glyph_for_size(Size::new(size), glyph)?;
    let (w, h, px) = match g.data {
        BitmapData::Png(bytes) => decode_png(bytes)?,
        BitmapData::Bgra(bytes) => {
            let (w, h) = (g.width as usize, g.height as usize);
            let px = bytes.get(..w * h * 4)?;
            (w, h, px.chunks_exact(4).map(bgra_premul).collect())
        }
        // ebdt/eblc masks only exist next to outlines, which win
        BitmapData::Mask(_) => return None,
    };
    if w == 0 || h == 0 {
        return None;
    }
    let scale = size / g.ppem_y;
    let top = match g.placement_origin {
        Origin::TopLeft => g.inner_bearing_y,
        Origin::BottomLeft => g.inner_bearing_y + h as f32,
    };
    // source pixel edges in output pixels, y down from the baseline
    let x0 = g.inner_bearing_x * scale;
    let y0 = -top * scale;
    let (cols, left) = weights(w, x0, scale);
    let (rows, top_px) = weights(h, y0, scale);
    let (ow, oh) = (cols.len(), rows.len());

    // columns first, then rows
    let mut tmp = vec![[0.0f32; 4]; ow * h];
    for y in 0..h {
        for (ox, taps) in cols.iter().enumerate() {
            let mut acc = [0.0; 4];
            for &(sx, wt) in taps {
                let s = px[y * w + sx];
                for c in 0..4 {
                    acc[c] += s[c] * wt;
                }
            }
            tmp[y * ow + ox] = acc;
        }
    }
    let mut data = Vec::with_capacity(ow * oh * 4);
    for taps in &rows {
        for ox in 0..ow {
            let mut acc = [0.0; 4];
            for &(sy, wt) in taps {
                let s = tmp[sy * ow + ox];
                for c in 0..4 {
                    acc[c] += s[c] * wt;
                }
            }
            data.extend_from_slice(&encode(linear_to_srgb(acc)));
        }
    }
    Some(Raster {
        left,
        top: -top_px,
        width: ow as u32,
        height: oh as u32,
        kind: GlyphKind::Color,
        data,
    })
}

/// for every output pixel, which source pixels land in it and how much of
/// it each one covers. `start` is where source pixel 0 begins.
fn weights(n: usize, start: f32, scale: f32) -> (Vec<Vec<(usize, f32)>>, i32) {
    let first = start.floor() as i32;
    let last = (start + n as f32 * scale).ceil() as i32;
    let out = (first..last)
        .map(|o| {
            let (a, b) = (o as f32, o as f32 + 1.0);
            let lo = (((a - start) / scale).floor().max(0.0)) as usize;
            let hi = (((b - start) / scale).ceil() as usize).min(n);
            (lo..hi)
                .filter_map(|i| {
                    let s0 = start + i as f32 * scale;
                    let cover = (b.min(s0 + scale) - a.max(s0)).max(0.0);
                    (cover > 0.0).then_some((i, cover))
                })
                .collect()
        })
        .collect();
    (out, first)
}

/// straight srgb rgba8 of any channel count to premultiplied linear
fn decode_png(bytes: &[u8]) -> Option<(usize, usize, Vec<[f32; 4]>)> {
    let opts = DecoderOptions::default()
        .png_set_strip_to_8bit(true)
        .png_set_add_alpha_channel(true);
    let mut d = PngDecoder::new_with_options(ZCursor::new(bytes), opts);
    let raw = d.decode_raw().ok()?;
    let (w, h) = d.dimensions()?;
    let n = raw.len().checked_div(w * h)?;
    let px = raw
        .chunks_exact(n)
        .map(|p| {
            let (rgb, a) = match n {
                1 => ([p[0]; 3], 255),
                2 => ([p[0]; 3], p[1]),
                3 => ([p[0], p[1], p[2]], 255),
                _ => ([p[0], p[1], p[2]], p[3]),
            };
            let a = a as f32 / 255.0;
            let [r, g, b] = rgb.map(|c| TO_LINEAR[c as usize] * a);
            [r, g, b, a]
        })
        .collect::<Vec<[f32; 4]>>();
    (px.len() == w * h).then_some((w, h, px))
}

/// cbdt's uncompressed format: premultiplied in srgb space, bgra order
fn bgra_premul(p: &[u8]) -> [f32; 4] {
    let a = p[3] as f32 / 255.0;
    if a == 0.0 {
        return [0.0; 4];
    }
    let lin = |c: u8| TO_LINEAR[((c as f32 / a).round().min(255.0)) as usize] * a;
    [lin(p[2]), lin(p[1]), lin(p[0]), a]
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn weights_cover_each_output_pixel_once_inside() {
        // 4 source px shrunk by half starting a quarter in: 0.25..2.25
        let (w, first) = weights(4, 0.25, 0.5);
        assert_eq!(first, 0);
        let sums: Vec<f32> = w.iter().map(|t| t.iter().map(|x| x.1).sum()).collect();
        assert_eq!(sums, [0.75, 1.0, 0.25]);
    }
}
