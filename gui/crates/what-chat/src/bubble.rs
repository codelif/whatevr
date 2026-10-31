//! chat rows in the picked look: soft 8 px bubbles with tight corners
//! inside a run, time tucked into the last line, avatar + name at a run's
//! start, peeking quote cards, reaction clusters, edge to edge photos,
//! adaptive link previews, hairline day rows. sizes are the variant sheet's
//! css px.

use std::collections::HashSet;

use what_core::{Scale, Srgba8};
use what_text::{GlyphKey, Paragraph, Span, TextSystem};
use what_ui::{Row, RowBuilder};

use crate::content::{Body, Entry, Item, Msg, initials};
use crate::pen::Pen;
use crate::theme::{Theme, hsl, mix, style, ticks};

const R: f32 = 8.0;
const TIGHT: f32 = 4.0;
const LINE: f32 = 20.0;
/// css's unitless 1.45 the sheet's page ran on, for text outside a bubble
const LOOSE: f32 = 1.45;
const PHOTO_W: f32 = 252.0;

#[derive(Clone)]
pub struct Bubbles {
    theme: Theme,
    /// icons this builder already shipped a bitmap for
    shipped: HashSet<GlyphKey>,
}

impl Bubbles {
    pub fn new(theme: Theme) -> Self {
        Self {
            theme,
            shipped: HashSet::new(),
        }
    }
}

impl RowBuilder<Item> for Bubbles {
    fn build(&mut self, item: &Item, width: u32, scale: Scale, text: &mut TextSystem) -> Row {
        let mut pen = Pen::new(scale, text, &mut self.shipped);
        let w = width as f32 / pen.s;
        let h = match item {
            Item::Top => 12.0,
            Item::Bottom => 18.0,
            Item::Entry(e) => match e.as_ref() {
                Entry::Day { day, date } => day_row(&mut pen, &self.theme, day, date, w),
                Entry::System(s) => system_row(&mut pen, &self.theme, s, w),
                Entry::Msg(m) => msg_row(&mut pen, &self.theme, m, w),
            },
        };
        pen.finish(h)
    }
}

fn day_row(pen: &mut Pen<'_>, t: &Theme, day: &str, date: &str, w: f32) -> f32 {
    let s = format!("{day}\u{a0} {date}");
    let mut bold = Span::new(0..day.len());
    bold.weight = Some(600.0);
    bold.color = Some(t.text.premultiply());
    let p = pen.para(&s, &style(12.0, 12.0 * LOOSE, 400.0, t.muted), &[bold]);
    let (tw, th) = (p.width(), p.height());
    let x = ((w - tw) / 2.0).max(16.0);
    let y = 12.0;
    let ly = y + ((th - 1.0) / 2.0).floor();
    if x - 10.0 > 16.0 {
        pen.quad(16.0, ly, x - 10.0 - 16.0, 1.0, [0.0; 4], t.faint);
    }
    if x + tw + 10.0 < w - 16.0 {
        pen.quad(
            x + tw + 10.0,
            ly,
            w - 16.0 - (x + tw + 10.0),
            1.0,
            [0.0; 4],
            t.faint,
        );
    }
    pen.put(x, y, p);
    y + th + 4.0
}

fn system_row(pen: &mut Pen<'_>, t: &Theme, s: &str, w: f32) -> f32 {
    let mut p = pen.para(s, &style(12.0, 12.0 * LOOSE, 400.0, t.muted), &[]);
    p.wrap(Some((w - 64.0).max(1.0)));
    let (pw, ph) = (p.width(), p.height());
    pen.put((w - pw) / 2.0, 5.0, p);
    5.0 + ph + 5.0
}

/// time (and ticks for yours) as the tuck shows them
struct Meta {
    time: Paragraph,
    state: Option<u8>,
}

impl Meta {
    fn new(pen: &mut Pen<'_>, m: &Msg, color: Srgba8) -> Self {
        let time = pen.para(&m.time, &style(11.5, LINE, 400.0, color), &[]);
        Self {
            time,
            state: m.from.is_none().then_some(m.state),
        }
    }

    fn width(&self, gap: f32) -> f32 {
        self.time.width() + self.state.map_or(0.0, |s| gap + ticks(s).1)
    }

    /// right edge at `right`, on the 20 px line whose top is `top`
    fn put(self, pen: &mut Pen<'_>, right: f32, top: f32, gap: f32, tick: Srgba8) {
        let x = right - self.width(gap);
        let tw = self.time.width();
        pen.put(x, top, self.time);
        if let Some(s) = self.state {
            let (icon, _) = ticks(s);
            pen.icon(s as u16, &icon, x + tw + gap, top + 5.0, tick);
        }
    }
}

/// width and height of text with the meta tucked into its last line, laid
/// out like css's shrink to fit: one line per hard break when everything
/// fits in `avail`, else wrapped at `avail`, the meta on a line of its own
/// when the last one has no room
fn tucked(p: &mut Paragraph, avail: f32, meta: f32) -> (f32, f32) {
    let last_right = |p: &Paragraph| {
        p.lines()
            .last()
            .map_or(0.0, |l| if l.rtl { p.width() } else { l.width })
    };
    let fit = p.width().max(last_right(p) + meta);
    if fit <= avail {
        return (fit, p.height());
    }
    p.wrap(Some(avail));
    if last_right(p) + meta <= avail {
        (avail, p.height())
    } else {
        (avail, p.height() + LINE)
    }
}

fn msg_row(pen: &mut Pen<'_>, t: &Theme, m: &Msg, w: f32) -> f32 {
    let out = m.from.is_none();
    let inner = w - 32.0;
    let colmax = inner * 0.8;
    let fill = if out { t.out } else { t.inc };
    let meta_w = if out { 58.0 } else { 40.0 };
    let mut y = if m.first { 10.0 } else { 2.0 };

    // corners: tight where the run goes on
    let mut rad = [R; 4];
    if !m.first {
        rad[if out { 1 } else { 0 }] = TIGHT;
    }
    if !m.last {
        rad[if out { 2 } else { 3 }] = TIGHT;
    }

    // run start: avatar and name above the run, outside the bubbles
    let mut name_w = 0.0;
    let name = match &m.from {
        Some(who) if m.first => {
            let mut p = pen.para(
                &who.name,
                &style(12.5, 12.5 * LOOSE, 600.0, t.id(who.color)),
                &[],
            );
            pen.text.clamp(&mut p, colmax - 1.0, 1);
            name_w = p.width() + 1.0;
            Some((who, p))
        }
        _ => None,
    };

    // the bubble's insides, measured before anything is placed
    #[allow(clippy::large_enum_variant)]
    enum Inside {
        Text {
            p: Paragraph,
            link: Option<Link>,
            th: f32,
        },
        Photo {
            ratio: f32,
            hue: f32,
            caption: Option<(Paragraph, f32)>,
        },
    }
    struct Link {
        title: Paragraph,
        host: Paragraph,
        hue: f32,
        wide: bool,
    }
    let body = style(14.5, LINE, 400.0, t.text);
    let (inside, bw) = match &m.body {
        Body::Photo { ratio, hue } => {
            let iw = PHOTO_W.min(colmax);
            let caption = (!m.text.is_empty()).then(|| {
                let mut p = pen.para(&m.text, &body, &[]);
                let (_, h) = tucked(&mut p, iw - 22.0, meta_w);
                (p, h)
            });
            (
                Inside::Photo {
                    ratio: *ratio,
                    hue: *hue,
                    caption,
                },
                iw,
            )
        }
        other => {
            let avail = (colmax - 22.0).max(1.0);
            let mut p = pen.para(&m.text, &body, &[]);
            let link = match other {
                Body::Link {
                    title,
                    host,
                    hue,
                    wide,
                } => Some(Link {
                    title: pen.para(title, &style(13.5, 18.0, 600.0, t.text), &[]),
                    host: pen.para(host, &style(12.0, 18.0, 400.0, t.muted), &[]),
                    hue: *hue,
                    wide: *wide,
                }),
                _ => None,
            };
            let card = link.as_ref().map_or(0.0, |l| {
                (if l.wide { 0.0 } else { 64.0 }) + 20.0 + l.title.width().max(l.host.width())
            });
            let text_fit = p.width().max(
                p.lines()
                    .last()
                    .map_or(0.0, |l| if l.rtl { p.width() } else { l.width })
                    + meta_w,
            );
            let cw = text_fit.max(card).min(avail);
            let (_, th) = tucked(&mut p, cw, meta_w);
            (Inside::Text { p, link, th }, cw + 22.0)
        }
    };

    // the peeking quote card above the bubble
    let quote = m.quote.as_ref().map(|q| {
        let who = q.from.as_ref().map_or("You", |f| &*f.name);
        let qf = if q.from.is_none() { t.out } else { t.inc };
        let wp = pen.para(who, &style(13.0, 17.0, 600.0, mix(t.bg, t.text, 0.7)), &[]);
        let tp = pen.para(
            &q.text,
            &style(13.0, 17.0, 400.0, mix(t.bg, t.muted, 0.7)),
            &[],
        );
        let mc = 22.0 + wp.width().max(tp.width());
        (wp, tp, mix(t.bg, qf, 0.7), mc)
    });
    let col_w = colmax.min(
        bw.max(name_w)
            .max(quote.as_ref().map_or(0.0, |q| q.3 + 28.0)),
    );
    let (col_l, bx) = if out {
        (w - 16.0 - col_w, w - 16.0 - bw)
    } else {
        (48.0, 48.0)
    };

    if let Some((who, p)) = name {
        pen.quad(
            16.0,
            y,
            26.0,
            26.0,
            [13.0; 4],
            mix(t.bg, t.id(who.color), 0.45),
        );
        let ini = pen.para(
            &initials(&who.name),
            &style(10.5, 10.5 * LOOSE, 600.0, t.text),
            &[],
        );
        let (iw, ih) = (ini.width(), ini.height());
        pen.put(16.0 + (26.0 - iw) / 2.0, y + (26.0 - ih) / 2.0, ini);
        let ph = p.height();
        pen.put(49.0, y + (26.0 - ph) / 2.0, p);
        y += 29.0;
    }

    if let Some((mut wp, mut tp, qfill, mc)) = quote {
        let cw = if out {
            mc.min(col_w * 0.88).min(col_w - 28.0)
        } else {
            (col_w - 28.0).min(col_w * 0.88)
        };
        pen.text.clamp(&mut wp, cw - 22.0, 1);
        pen.text.clamp(&mut tp, cw - 22.0, 1);
        let ch = 5.0 + wp.height() + tp.height() + 13.0;
        let cx = if out {
            col_l + col_w - 14.0 - cw
        } else {
            col_l + 14.0
        };
        pen.quad(cx, y, cw, ch, [R.min(10.0); 4], qfill);
        let wh = wp.height();
        pen.put(cx + 11.0, y + 5.0, wp);
        pen.put(cx + 11.0, y + 5.0 + wh, tp);
        y += ch - 8.0;
    }

    let by = y;
    let bh = match inside {
        Inside::Text { p, link, th } => {
            let (x, cw) = (bx + 11.0, bw - 22.0);
            // the card is sized first: it paints over the bubble, so the
            // bubble goes down before it
            let card = link.map(|mut l| {
                let tx = if l.wide { 10.0 } else { 74.0 };
                pen.text.clamp(&mut l.title, cw - tx - 10.0, 2);
                pen.text.clamp(&mut l.host, cw - tx - 10.0, 1);
                let text_h = l.title.height() + l.host.height();
                let ch = if l.wide {
                    110.0 + 6.0 + text_h + 7.0
                } else {
                    6.0 + text_h + 6.0
                };
                (l, tx, ch)
            });
            let link_h = card.as_ref().map_or(0.0, |c| 2.0 + c.2 + 6.0);
            let bh = 6.0 + link_h + th + 7.0;
            pen.quad(bx, by, bw, bh, rad, fill);
            if let Some((l, tx, ch)) = card {
                let lr = R.min(8.0);
                let (a, b) = (hsl(l.hue, 0.32, 0.55), hsl(l.hue + 40.0, 0.36, 0.32));
                let cy = by + 6.0 + 2.0;
                let ty = if l.wide { cy + 116.0 } else { cy + 6.0 };
                pen.quad(x, cy, cw, ch, [lr; 4], t.link_bg);
                if l.wide {
                    pen.gradient(x, cy, cw, 110.0, [lr, lr, 0.0, 0.0], 135.0, a, b);
                } else {
                    pen.gradient(x, cy, 64.0, ch, [lr, 0.0, 0.0, lr], 135.0, a, b);
                }
                let title_h = l.title.height();
                pen.put(x + tx, ty, l.title);
                pen.put(x + tx, ty + title_h, l.host);
            }
            pen.put(bx + 11.0, by + 6.0 + link_h, p);
            let meta = Meta::new(pen, m, t.muted);
            let tick = if m.state == 3 { t.acc } else { t.muted };
            meta.put(pen, bx + bw - 11.0, by + bh - 7.0 - LINE, 6.0, tick);
            bh
        }
        Inside::Photo {
            ratio,
            hue,
            caption,
        } => {
            let ih = bw / ratio;
            let (a, b) = (hsl(hue, 0.28, 0.60), hsl(hue + 50.0, 0.32, 0.30));
            match caption {
                Some((p, th)) => {
                    let bh = ih + 5.0 + th + 7.0;
                    pen.quad(bx, by, bw, bh, rad, fill);
                    pen.gradient(bx, by, bw, ih, [rad[0], rad[1], 0.0, 0.0], 160.0, a, b);
                    pen.put(bx + 11.0, by + ih + 5.0, p);
                    let meta = Meta::new(pen, m, t.muted);
                    let tick = if m.state == 3 { t.acc } else { t.muted };
                    meta.put(pen, bx + bw - 11.0, by + bh - 7.0 - LINE, 6.0, tick);
                    bh
                }
                None => {
                    pen.gradient(bx, by, bw, ih, rad, 160.0, a, b);
                    let white = Srgba8::hex(0xffffff);
                    let meta = Meta::new(pen, m, white);
                    let pw = meta.width(5.0) + 14.0;
                    let (px, py) = (bx + bw - 7.0 - pw, by + ih - 6.0 - 22.0);
                    pen.quad(px, py, pw, 22.0, [9.0; 4], Srgba8::new(0, 0, 0, 107));
                    meta.put(pen, px + pw - 7.0, py + 1.0, 5.0, white);
                    ih
                }
            }
        }
    };
    let bottom = by + bh;

    if m.reactions.is_empty() {
        return bottom;
    }
    // the cluster hangs 14 px under the bubble, ringed in the ground colour
    let emo: Vec<Paragraph> = m
        .reactions
        .iter()
        .map(|(e, _)| pen.para(e, &style(12.0, 12.0 * LOOSE, 400.0, t.text), &[]))
        .collect();
    let total: u32 = m.reactions.iter().map(|r| r.1).sum();
    let tot = pen.para(
        &total.to_string(),
        &style(12.0, 12.0 * LOOSE, 400.0, t.text),
        &[],
    );
    let lh = tot.height();
    let ew: f32 = emo.iter().map(|p| p.width()).sum::<f32>() - 2.0 * (emo.len() - 1) as f32;
    let cw = 4.0 + ew + 5.0 + tot.width() + 8.0;
    let ch = lh + 2.0;
    let cy = bottom + 14.0 - ch;
    let cx = if out { bx + 8.0 } else { bx + bw - 8.0 - cw };
    pen.quad(
        cx - 2.5,
        cy - 2.5,
        cw + 5.0,
        ch + 5.0,
        [12.0 + 2.5; 4],
        t.bg,
    );
    pen.quad(cx, cy, cw, ch, [12.0; 4], if out { t.inc } else { t.inc2 });
    let mut x = cx + 4.0;
    for p in emo {
        let pw = p.width();
        pen.put(x, cy + 1.0, p);
        x += pw - 2.0;
    }
    pen.put(x + 2.0 + 5.0, cy + 1.0, tot);
    bottom + 14.0
}
