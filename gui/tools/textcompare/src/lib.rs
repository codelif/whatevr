//! the cases textcompare and its goldens share: the samples at three sizes,
//! light and dark, at 1x, 1.25x and 2x, as html for firefox and as a scene
//! for what-render.

use std::fmt::Write as _;
use std::path::{Path, PathBuf};

use what_core::{PremulRgba, Scale, Srgba8};
use what_render::{
    Clip, Glyph, GlyphFormat, GlyphImage, Gpu, Pixmap, Renderer, Scene, TextureFormat,
};
use what_text::{GlyphKind, Paragraph, Span, Style, TextSystem};

pub const SAMPLES: &str = include_str!("../../../crates/what-text/tests/fonts/samples.txt");
pub const SIZES: [f32; 3] = [13.0, 15.0, 20.0];
pub const SCALES: [(u32, &str); 3] = [(120, "1"), (150, "1.25"), (240, "2")];
/// css px, a multiple of 4 so every scale lands on whole pixels
pub const WIDTH: u32 = 520;
const MARGIN: f32 = 8.0;
const LINE_HEIGHT: f32 = 1.3;

/// bundled fonts in fallback order: (file, css family, weight, italic)
const FONTS: [(&str, &str, u16, bool); 8] = [
    ("NotoSans-Regular.ttf", "t-latin", 400, false),
    ("NotoSans-Bold.ttf", "t-latin", 700, false),
    ("NotoSans-Italic.ttf", "t-latin", 400, true),
    ("NotoSansDevanagari-Regular.ttf", "t-deva", 400, false),
    ("NotoNaskhArabic-Regular.ttf", "t-arab", 400, false),
    ("NotoSansCJKsc-Regular.otf", "t-cjk", 400, false),
    ("NotoColorEmoji.ttf", "t-emoji", 400, false),
    ("test_glyphs-glyf_colr_1.ttf", "t-colr", 400, false),
];

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Theme {
    Light,
    Dark,
}

impl Theme {
    pub const ALL: [Self; 2] = [Self::Light, Self::Dark];

    /// (background, text) as srgb hex, whatsapp's
    fn colors(self) -> (u32, u32) {
        match self {
            Self::Light => (0xffffff, 0x111b21),
            Self::Dark => (0x111b21, 0xe9edef),
        }
    }

    pub fn background(self) -> PremulRgba {
        Srgba8::hex(self.colors().0).premultiply()
    }

    fn name(self) -> &'static str {
        match self {
            Self::Light => "light",
            Self::Dark => "dark",
        }
    }
}

#[derive(Clone, Copy, Debug)]
pub struct Case {
    pub size: f32,
    pub theme: Theme,
    pub scale: Scale,
    pub scale_label: &'static str,
}

impl Case {
    pub fn all() -> Vec<Self> {
        let mut out = Vec::new();
        for (s, scale_label) in SCALES {
            for size in SIZES {
                for theme in Theme::ALL {
                    out.push(Self {
                        size,
                        theme,
                        scale: Scale::from_120ths(s),
                        scale_label,
                    });
                }
            }
        }
        out
    }

    pub fn name(&self) -> String {
        format!("{}px-{}@{}", self.size, self.theme.name(), self.scale_label)
    }

    /// css px
    pub fn height(&self) -> u32 {
        let lines = SAMPLES.lines().count() as f32;
        let h = (2.0 * MARGIN + lines * self.size * LINE_HEIGHT).ceil() as u32;
        h.next_multiple_of(4)
    }

    /// physical rows of each sample line's box
    pub fn lines(&self) -> Vec<std::ops::Range<u32>> {
        let s = self.scale.as_f32();
        let row = |i: usize| ((MARGIN + i as f32 * self.size * LINE_HEIGHT) * s).round() as u32;
        (0..SAMPLES.lines().count())
            .map(|i| row(i)..row(i + 1))
            .collect()
    }

    pub fn physical(&self) -> (u32, u32) {
        let s = self.scale.as_120ths();
        (WIDTH * s / 120, self.height() * s / 120)
    }
}

pub fn fonts_dir() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../../crates/what-text/tests/fonts")
}

pub fn bundled() -> TextSystem {
    let dir = fonts_dir();
    let paths: Vec<PathBuf> = FONTS.iter().map(|f| dir.join(f.0)).collect();
    let refs: Vec<&Path> = paths.iter().map(PathBuf::as_path).collect();
    TextSystem::with_fonts(&refs).unwrap_or_else(|e| panic!("{e}"))
}

/// the second sample's words get their own styles
fn spans(text: &str) -> Vec<Span> {
    let word = |w: &str| {
        let i = text.find(w).expect("sample word");
        Span::new(i..i + w.len())
    };
    vec![
        Span {
            weight: Some(700.0),
            ..word("Bold")
        },
        Span {
            italic: Some(true),
            ..word("Italic")
        },
    ]
}

fn css_hex(c: u32) -> String {
    format!("#{c:06x}")
}

fn escape(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
}

pub fn html(case: &Case, system_fonts: bool) -> String {
    let (bg, fg) = case.theme.colors();
    let dir = fonts_dir().canonicalize().expect("fonts dir");
    let mut h = String::from("<!doctype html><meta charset=utf-8><style>\n");
    let mut families: Vec<String> = Vec::new();
    if system_fonts {
        families.push("sans-serif".into());
    }
    for (file, family, weight, italic) in FONTS.iter().filter(|_| !system_fonts) {
        let style = if *italic { "italic" } else { "normal" };
        let url = format!("file://{}/{file}", dir.display());
        let _ = writeln!(
            h,
            "@font-face{{font-family:\"{family}\";src:url(\"{url}\");font-weight:{weight};font-style:{style}}}"
        );
        let quoted = format!("\"{family}\"");
        if !families.contains(&quoted) {
            families.push(quoted);
        }
    }
    let list = families.join(",");
    let _ = writeln!(
        h,
        "html,body{{margin:0;background:{}}}\nbody{{padding:{MARGIN}px;font:{}px/{LINE_HEIGHT} {list};color:{}}}\ndiv{{white-space:pre}}\nb{{font-weight:700}}",
        css_hex(bg),
        case.size,
        css_hex(fg)
    );
    h.push_str("</style>\n");
    for line in SAMPLES.lines() {
        let mut s = escape(line);
        if line.starts_with("Regular") {
            s = s
                .replace("Bold", "<b>Bold</b>")
                .replace("Italic", "<i>Italic</i>");
        }
        let _ = writeln!(h, "<div>{s}</div>");
    }
    h
}

pub struct Ours {
    pub gpu: Gpu,
    pub renderer: Renderer,
}

impl Ours {
    pub fn new() -> Self {
        let gpu = Gpu::headless(what_render::Adapter::Lavapipe).unwrap_or_else(|e| panic!("{e}"));
        let renderer = Renderer::new(&gpu, TextureFormat::Rgba8Unorm);
        Self { gpu, renderer }
    }

    pub fn render(&mut self, ts: &mut TextSystem, case: &Case) -> Pixmap {
        let (_, fg) = case.theme.colors();
        let color = Srgba8::hex(fg).premultiply();
        let style = Style {
            size: case.size,
            color,
            line_height: LINE_HEIGHT,
            ..Style::default()
        };
        let s = case.scale.as_f32();
        let mut scene = Scene::new();
        let text = SAMPLES.trim_end();
        let p = ts.paragraph(text, &style, &spans(text), case.scale);
        let m = (MARGIN * s).round() as i32;
        self.place(ts, &p, (m, m), &mut scene);
        let (w, h) = case.physical();
        let size = what_core::PhysicalSize::new(w, h);
        self.renderer
            .render_pixmap(&self.gpu, &scene, size, size, case.theme.background())
            .unwrap_or_else(|e| panic!("{e}"))
    }

    fn place(
        &mut self,
        ts: &mut TextSystem,
        p: &Paragraph,
        (ox, oy): (i32, i32),
        scene: &mut Scene,
    ) {
        for g in p.glyphs() {
            let key = g.key.0;
            if !self.renderer.has_glyph(key) {
                match ts.rasterize(g.key) {
                    Some(r) => {
                        let format = match r.kind {
                            GlyphKind::Mask => GlyphFormat::Mask,
                            GlyphKind::Color => GlyphFormat::Rgba,
                        };
                        let image = GlyphImage {
                            left: r.left,
                            top: r.top,
                            width: r.width,
                            height: r.height,
                            format,
                            data: &r.data,
                        };
                        self.renderer.add_glyph(&self.gpu, key, image);
                    }
                    None => self.renderer.add_glyph(&self.gpu, key, GlyphImage::EMPTY),
                }
            }
            scene.glyph(Glyph {
                x: ox + g.x,
                y: oy + g.y,
                key,
                color: g.color,
                clip: Clip::NONE,
            });
        }
    }
}

impl Default for Ours {
    fn default() -> Self {
        Self::new()
    }
}
