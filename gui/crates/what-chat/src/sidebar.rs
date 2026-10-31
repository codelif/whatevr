//! chat list rows as picked: 64 px, 40 px round avatar, time by the name,
//! accent count pill, one line preview with the sender or your ticks.
//! hover and selection fills are the shell's, drawn under the rows.

use std::collections::HashSet;
use std::sync::Arc;

use what_core::Scale;
use what_text::{GlyphKey, Span, TextSystem};
use what_ui::{Row, RowBuilder};

use crate::content::{ChatInfo, initials};
use crate::pen::Pen;
use crate::theme::{Theme, mix, style, ticks};

pub const ROW: f32 = 64.0;
const LOOSE: f32 = 1.45;

#[derive(Clone)]
pub struct SideRows {
    theme: Theme,
    shipped: HashSet<GlyphKey>,
}

impl SideRows {
    pub fn new(theme: Theme) -> Self {
        Self {
            theme,
            shipped: HashSet::new(),
        }
    }
}

impl RowBuilder<Arc<ChatInfo>> for SideRows {
    fn build(&mut self, c: &Arc<ChatInfo>, width: u32, scale: Scale, text: &mut TextSystem) -> Row {
        let t = &self.theme;
        let mut pen = Pen::new(scale, text, &mut self.shipped);
        let w = width as f32 / pen.s;

        pen.quad(
            14.0,
            12.0,
            40.0,
            40.0,
            [20.0; 4],
            mix(t.bg, t.id(c.color), 0.45),
        );
        let ini = pen.para(
            &initials(&c.name),
            &style(14.0, 14.0 * LOOSE, 600.0, t.text),
            &[],
        );
        let (iw, ih) = (ini.width(), ini.height());
        pen.put(14.0 + (40.0 - iw) / 2.0, 12.0 + (40.0 - ih) / 2.0, ini);

        let (x0, x1) = (66.0, w - 14.0);
        let unread = c.unread > 0;
        let time = pen.para(
            &c.time,
            &style(
                11.5,
                11.5 * LOOSE,
                400.0,
                if unread { t.acc } else { t.muted },
            ),
            &[],
        );
        let tw = time.width();
        let mut name = pen.para(&c.name, &style(14.5, 19.0, 600.0, t.text), &[]);
        pen.text.clamp(&mut name, (x1 - x0 - tw - 8.0).max(1.0), 1);
        let line2 = if unread { 20.0 } else { 18.0 };
        let top = (ROW - (19.0 + 2.0 + line2)) / 2.0;
        // time and name share a baseline
        let base = |p: &what_text::Paragraph| p.lines().next().map_or(0.0, |l| l.baseline);
        let ty = top + base(&name) - base(&time);
        pen.put(x0, top, name);
        pen.put(x1 - tw, ty, time);

        let y2 = top + 21.0;
        let mut avail = x1 - x0;
        if unread {
            let n = if c.unread > 99 {
                "99+".to_string()
            } else {
                c.unread.to_string()
            };
            let p = pen.para(&n, &style(11.5, 11.5 * LOOSE, 600.0, t.acc_ink), &[]);
            let (nw, nh) = (p.width(), p.height());
            let pw = (nw + 12.0).max(20.0);
            pen.quad(x1 - pw, y2, pw, 20.0, [10.0; 4], t.acc);
            pen.put(x1 - pw + (pw - nw) / 2.0, y2 + (20.0 - nh) / 2.0, p);
            avail -= pw + 8.0;
        }

        let mut x = x0;
        let own = c.from.as_deref() == Some("");
        let (s, spans) = match c.from.as_deref() {
            Some(f) if !f.is_empty() => {
                let mut sp = Span::new(0..f.len() + 2);
                sp.color = Some(t.text.premultiply());
                (format!("{f}: {}", c.preview), vec![sp])
            }
            _ => (c.preview.clone(), Vec::new()),
        };
        let tick = own.then(|| ticks(c.state));
        if let Some((_, tw)) = &tick {
            x += tw + 4.0;
            avail -= tw + 4.0;
        }
        let mut p = pen.para(&s, &style(13.5, 18.0, 400.0, t.muted), &spans);
        // wrapping puts an rtl line at the box's right edge; the sheet's ltr
        // block starts it on the left, so only text that overflows is cut
        if p.width() > avail {
            pen.text.clamp(&mut p, avail.max(1.0), 1);
        }
        let py = y2 + (line2 - 18.0) / 2.0;
        if let Some((icon, _)) = tick {
            // the svg sat 1 px under the baseline
            let ty = py + base(&p) + 1.0 - 10.0;
            let col = if c.state == 3 { t.acc } else { t.muted };
            pen.icon(c.state as u16, &icon, x0, ty, col);
        }
        pen.put(x, py, p);
        pen.finish(ROW)
    }
}
