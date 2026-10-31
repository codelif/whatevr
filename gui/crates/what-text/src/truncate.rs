//! max lines with an ellipsis, blink style: the last kept line drops whole
//! clusters from its logical end until the ellipsis fits. the shaped glyphs
//! are kept as they are, except where cutting changes how the rest shapes
//! (joining and conjunct scripts mid-word): that line is shaped again.

use std::ops::Range;

use icu_properties::props::Script;
use icu_properties::{CodePointMapData, CodePointMapDataBorrowed};
use parley::PositionedLayoutItem;
use what_core::Scale;

use crate::paragraph::{Clamp, PlacedGlyph, moved, place_glyph};
use crate::system::FaceKey;
use crate::{Paragraph, Span, TextSystem};

const SCRIPT: CodePointMapDataBorrowed<'static, Script> = CodePointMapData::<Script>::new();
const ELLIPSIS: &str = "\u{2026}";

/// one grapheme cluster of a line, layout units
#[derive(Clone, Debug)]
struct Cluster {
    text: Range<usize>,
    x0: f32,
    x1: f32,
    /// into the paragraph's placed glyphs
    glyphs: Range<usize>,
    space: bool,
    lig_cont: bool,
}

impl TextSystem {
    /// wrap to `width` logical px and keep at most `max_lines`; when text is
    /// left over, the last line ends in an ellipsis. true if it cut anything.
    pub fn clamp(&mut self, p: &mut Paragraph, width: f32, max_lines: u32) -> bool {
        p.wrap(Some(width));
        let max = max_lines.max(1) as usize;
        let n = p.layout.len().min(max);
        // a word too long to break overflows the last line: cut that too
        let overflow = p
            .layout
            .get(n.saturating_sub(1))
            .is_some_and(|l| l.metrics().advance > width * p.scale + 0.01);
        if p.layout.len() <= max && !overflow {
            return false;
        }
        let li = n.max(1) - 1;
        let scale = Scale::from_120ths((p.scale * 120.0).round() as u32);
        let rtl = p.layout.is_rtl();
        let Some((line_text, baseline, right)) = p.layout.get(li).map(|l| {
            let m = l.metrics();
            (l.text_range(), m.baseline.round(), m.offset + m.advance)
        }) else {
            return false;
        };

        let ell_w = self.ellipsis_width(p, scale);
        let avail = width * p.scale - ell_w;
        let spans = clusters(p, li);
        let mut order: Vec<usize> = (0..spans.len()).collect();
        order.sort_by_key(|&i| spans[i].text.start);
        let fits = |c: &Cluster| {
            if rtl {
                right - c.x0 <= avail + 0.01
            } else {
                c.x1 <= avail + 0.01
            }
        };
        let mut kept = order.iter().take_while(|&&i| fits(&spans[i])).count();
        let trim = |kept: &mut usize| {
            while *kept > 0 && *kept < spans.len() && spans[order[*kept]].lig_cont {
                *kept -= 1;
            }
            while *kept > 0 && spans[order[*kept - 1]].space {
                *kept -= 1;
            }
        };
        trim(&mut kept);
        let cut = |kept: usize| {
            if kept > 0 {
                spans[order[kept - 1]].text.end
            } else {
                line_text.start
            }
        };

        // the last line in its own coordinates, glyphs with their unsnapped x
        let at = cut(kept);
        let mid_word = !p.text[at..].starts_with(char::is_whitespace);
        let complex = p.text[..at].chars().next_back().is_some_and(joins);
        let mut last: Vec<(PlacedGlyph, f32)> = Vec::new();
        let last_width;
        if mid_word && complex {
            // shape the kept text and the ellipsis together, dropping more
            // clusters while the reshaped line runs long
            let mut q;
            loop {
                let at = cut(kept);
                let text = format!("{}{ELLIPSIS}", &p.text[line_text.start..at]);
                let spans = shift(&p.spans, line_text.start..at);
                q = self.paragraph(&text, &p.base, &spans, scale);
                if q.layout.width() <= width * p.scale + 0.01 || kept == 0 {
                    break;
                }
                kept -= 1;
                trim(&mut kept);
            }
            let dx = if rtl { right - q.layout.width() } else { 0.0 };
            append_line(&q, 0, dx, baseline, &mut p.faces, &mut last);
            last_width = q.layout.width();
        } else {
            let keep: Vec<&Cluster> = order[..kept].iter().map(|&i| &spans[i]).collect();
            last.extend(
                p.line_glyphs[li]
                    .clone()
                    .filter(|g| keep.iter().any(|c| c.glyphs.contains(g)))
                    .map(|g| (p.placed[g], p.raw_x[g])),
            );
            let ex = if rtl {
                keep.iter().map(|c| c.x0).fold(right, f32::min) - ell_w
            } else {
                keep.iter().map(|c| c.x1).fold(0.0, f32::max)
            };
            let e = self
                .ellipsis
                .as_ref()
                .map(|(_, _, e)| e)
                .expect("shaped above");
            append_line(e, 0, ex, baseline, &mut p.faces, &mut last);
            last_width = if rtl { right - ex } else { ex + ell_w };
        }

        let edges: Vec<(f32, f32)> = p
            .layout
            .lines()
            .take(li)
            .map(|l| {
                (
                    l.metrics().offset + l.metrics().advance,
                    l.metrics().advance,
                )
            })
            .collect();
        let total = edges.iter().map(|e| e.1).fold(last_width, f32::max);
        if rtl {
            // every kept line ends at the new right edge
            for (i, (end, _)) in edges.iter().enumerate() {
                let dx = total - end;
                for g in p.line_glyphs[i].clone() {
                    p.raw_x[g] += dx;
                    p.placed[g] = moved(p.placed[g], p.raw_x[g]);
                }
            }
            let dx = total - right;
            for (g, raw) in &mut last {
                *raw += dx;
                *g = moved(*g, *raw);
            }
        }

        let start = p.line_glyphs[li].start;
        p.placed.truncate(start);
        p.raw_x.truncate(start);
        for (g, raw) in last {
            p.placed.push(g);
            p.raw_x.push(raw);
        }
        p.line_glyphs.truncate(li);
        p.line_glyphs.push(start..p.placed.len());
        p.clamp = Some(Clamp {
            lines: li + 1,
            last_width,
            width: total,
        });
        true
    }

    /// layout units; shapes the ellipsis in the paragraph's base style when
    /// the cached one doesn't match
    fn ellipsis_width(&mut self, p: &Paragraph, scale: Scale) -> f32 {
        let stale = self
            .ellipsis
            .as_ref()
            .is_none_or(|(s, sc, _)| *s != p.base || *sc != scale);
        if stale {
            let e = self.paragraph(ELLIPSIS, &p.base, &[], scale);
            self.ellipsis = Some((p.base.clone(), scale, e));
        }
        self.ellipsis
            .as_ref()
            .map_or(0.0, |(_, _, e)| e.layout.width())
    }
}

/// scripts whose letters change shape with their neighbours, so a cut
/// mid-word needs shaping again
fn joins(c: char) -> bool {
    !matches!(
        SCRIPT.get(c),
        Script::Latin
            | Script::Greek
            | Script::Cyrillic
            | Script::Armenian
            | Script::Georgian
            | Script::Hebrew
            | Script::Thai
            | Script::Han
            | Script::Hiragana
            | Script::Katakana
            | Script::Hangul
            | Script::Common
            | Script::Inherited
    )
}

/// spans that fall in `range`, moved to start at 0
fn shift(spans: &[Span], range: Range<usize>) -> Vec<Span> {
    spans
        .iter()
        .filter(|s| s.range.start < range.end && s.range.end > range.start)
        .map(|s| Span {
            range: s.range.start.max(range.start) - range.start
                ..s.range.end.min(range.end) - range.start,
            ..s.clone()
        })
        .collect()
}

/// clusters of line `li` in visual order, each with its glyphs
fn clusters(p: &Paragraph, li: usize) -> Vec<Cluster> {
    let Some(line) = p.layout.get(li) else {
        return Vec::new();
    };
    let mut out = Vec::new();
    let mut gi = p.line_glyphs[li].start;
    let mut run_clusters = Vec::new();
    let mut run_index = None;
    let mut pos = 0;
    for item in line.items() {
        let PositionedLayoutItem::GlyphRun(gr) = item else {
            continue;
        };
        let run = gr.run();
        if run_index != Some(run.index()) {
            run_index = Some(run.index());
            run_clusters = run.visual_clusters().collect::<Vec<_>>();
            pos = 0;
        }
        let n = gr.glyphs().count();
        let mut x = gr.offset();
        let mut taken = 0;
        while pos < run_clusters.len() {
            let c = &run_clusters[pos];
            let k = c.glyphs().count();
            if taken >= n && !(k == 0 && c.is_ligature_continuation()) {
                break;
            }
            let adv = c.advance();
            out.push(Cluster {
                text: c.text_range(),
                x0: x,
                x1: x + adv,
                glyphs: gi..gi + k,
                space: c.is_space_or_nbsp(),
                lig_cont: c.is_ligature_continuation(),
            });
            gi += k;
            taken += k;
            x += adv;
            pos += 1;
        }
    }
    debug_assert_eq!(gi, p.line_glyphs[li].end, "clusters and glyphs disagree");
    out
}

/// line `li` of `src` placed `dx` layout units right on whole-px `baseline`,
/// its faces merged into `faces`
fn append_line(
    src: &Paragraph,
    li: usize,
    dx: f32,
    baseline: f32,
    faces: &mut Vec<(FaceKey, crate::paragraph::Face)>,
    out: &mut Vec<(PlacedGlyph, f32)>,
) {
    let Some(line) = src.layout.get(li) else {
        return;
    };
    let m = line.metrics();
    for item in line.items() {
        let PositionedLayoutItem::GlyphRun(run) = item else {
            continue;
        };
        let key = FaceKey::of(run.run());
        let Some((_, face)) = src.faces.iter().find(|(k, _)| *k == key) else {
            continue;
        };
        if !faces.iter().any(|(k, _)| *k == key) {
            faces.push((key, face.clone()));
        }
        let size = run.run().font_size();
        let color = run.style().brush;
        for g in run.positioned_glyphs() {
            if let Some(p) = place_glyph(
                face,
                g.id,
                size,
                color,
                g.x + dx,
                baseline,
                g.y - m.baseline,
            ) {
                out.push((p, g.x + dx));
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use std::path::Path;

    use what_core::Scale;

    use crate::{Style, TextSystem};

    fn system() -> TextSystem {
        let dir = Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fonts");
        let files = ["NotoSans-Regular.ttf", "NotoNaskhArabic-Regular.ttf"].map(|f| dir.join(f));
        TextSystem::with_fonts(&files.iter().map(|p| p.as_path()).collect::<Vec<_>>()).unwrap()
    }

    #[test]
    fn short_text_is_left_alone() {
        let mut ts = system();
        let mut p = ts.paragraph("hello", &Style::default(), &[], Scale::from_120ths(120));
        assert!(!ts.clamp(&mut p, 200.0, 2));
        assert_eq!(p.lines().count(), 1);
    }

    #[test]
    fn cuts_to_lines_and_fits_the_width() {
        let mut ts = system();
        let text = "the quick brown fox jumps over the lazy dog and keeps on running far away";
        for scale in [120, 150, 240] {
            let mut p = ts.paragraph(text, &Style::default(), &[], Scale::from_120ths(scale));
            let full = p.glyphs().count();
            assert!(ts.clamp(&mut p, 120.0, 2));
            let lines: Vec<_> = p.lines().collect();
            assert_eq!(lines.len(), 2);
            assert!(p.width() <= 120.0 + 1e-3, "{}", p.width());
            assert!(lines[1].width > 60.0, "{lines:?}");
            assert!(p.glyphs().count() < full);
            assert_eq!(p.height(), lines[1].top + lines[1].height);
        }
    }

    #[test]
    fn only_a_word_wider_than_the_line_splits() {
        let mut ts = system();
        // no break opportunity anywhere inside it, like the tail of a link
        let word = "lb=yRvSl6VI&utm_medium=Share&utm_source=whatevr";
        let mut p = ts.paragraph(
            &format!("see {word}"),
            &Style::default(),
            &[],
            Scale::from_120ths(120),
        );
        p.wrap(Some(150.0));
        let lines: Vec<_> = p.lines().collect();
        assert!(lines.len() > 2, "{lines:?}");
        assert!(lines.iter().all(|l| l.width <= 150.0 + 1e-3), "{lines:?}");
        // "see" fits but the word doesn't, so the first break is the space
        let mut q = ts.paragraph("see ", &Style::default(), &[], Scale::from_120ths(120));
        q.wrap(None);
        assert!((lines[0].width - q.width()).abs() < 0.5, "{lines:?}");
    }

    #[test]
    fn arabic_mid_word_reshapes() {
        let mut ts = system();
        let text = "سأصلبعدعشردقائقتقريباًمرحبابكمجميعاسأصلبعدعشردقائق";
        let mut p = ts.paragraph(text, &Style::default(), &[], Scale::from_120ths(120));
        assert!(ts.clamp(&mut p, 80.0, 1));
        assert!(p.width() <= 80.0 + 1e-3, "{}", p.width());
        assert!(p.lines().next().unwrap().rtl);
    }
}
