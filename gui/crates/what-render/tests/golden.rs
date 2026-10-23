//! pixel tests on lavapipe, which renders the same bits on every machine with
//! the same mesa and llvm. WHAT_BLESS=1 rewrites the goldens.

use std::collections::HashSet;
use std::path::{Path, PathBuf};

use what_core::{LinearRgba, PhysicalSize, Scale, Srgba8};
use what_render::{
    Adapter, Corners, Gpu, Pixmap, Quad, Rect, Renderer, Scene, Shadow, TextureFormat, scenes,
};

const SCALES: [(u32, &str); 3] = [(120, "1"), (150, "1.25"), (240, "2")];

fn setup() -> (Gpu, Renderer) {
    let gpu = Gpu::headless(Adapter::Lavapipe).unwrap_or_else(|e| panic!("{e}"));
    let renderer = Renderer::new(&gpu, TextureFormat::Rgba8UnormSrgb);
    (gpu, renderer)
}

fn black() -> LinearRgba {
    Srgba8::hex(0).to_linear()
}

fn render(
    gpu: &Gpu,
    r: &mut Renderer,
    scene: &Scene,
    size: PhysicalSize,
    content: PhysicalSize,
) -> Pixmap {
    r.render_pixmap(gpu, scene, size, content, black()).unwrap()
}

#[test]
fn goldens() {
    let (gpu, mut r) = setup();
    let assets = scenes::Assets::new(&mut r, &gpu);
    let dir = Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/golden");
    let out = PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("golden-failures");
    let bless = std::env::var_os("WHAT_BLESS").is_some_and(|v| v == "1");
    // stale failures from an earlier run would mislead
    let _ = std::fs::remove_dir_all(&out);
    let mut failures = Vec::new();
    for name in scenes::NAMES {
        for (scale, label) in SCALES {
            let scale = Scale::from_120ths(scale);
            let scene = scenes::build(name, &assets, scale).unwrap();
            let size = scale.to_physical(scenes::SIZE);
            let got = render(&gpu, &mut r, &scene, size, size);
            let file = format!("{name}@{label}.png");
            let path = dir.join(&file);
            if bless {
                std::fs::create_dir_all(&dir).unwrap();
                got.save_png(&path).unwrap();
                continue;
            }
            let Ok(want) = Pixmap::load_png(&path) else {
                failures.push(format!(
                    "{file}: missing, run WHAT_BLESS=1 cargo test -p what-render"
                ));
                continue;
            };
            if let Some(n) = differing(&want, &got) {
                std::fs::create_dir_all(&out).unwrap();
                let stem = out.join(format!("{name}@{label}"));
                got.save_png(&out.join(format!("{name}@{label}.actual.png")))
                    .unwrap();
                diff(&want, &got)
                    .save_png(&out.join(format!("{name}@{label}.diff.png")))
                    .unwrap();
                failures.push(format!(
                    "{file}: {n} pixels differ, see {}.*.png",
                    stem.display()
                ));
            }
        }
    }
    assert!(failures.is_empty(), "\n{}", failures.join("\n"));
}

/// none when identical
fn differing(a: &Pixmap, b: &Pixmap) -> Option<usize> {
    if a.size != b.size {
        return Some(usize::MAX);
    }
    let n = a
        .rgba
        .chunks(4)
        .zip(b.rgba.chunks(4))
        .filter(|(x, y)| x != y)
        .count();
    (n > 0).then_some(n)
}

/// mismatches in white over a dimmed grey copy, so it reads without colour vision
fn diff(want: &Pixmap, got: &Pixmap) -> Pixmap {
    let mut rgba = Vec::with_capacity(got.rgba.len());
    for (i, g) in got.rgba.chunks(4).enumerate() {
        let w = want.rgba.get(i * 4..i * 4 + 4);
        if w == Some(g) {
            let luma = ((g[0] as u32 * 3 + g[1] as u32 * 6 + g[2] as u32) / 10 / 4) as u8;
            rgba.extend_from_slice(&[luma, luma, luma, 255]);
        } else {
            rgba.extend_from_slice(&[255, 255, 255, 255]);
        }
    }
    Pixmap {
        size: got.size,
        rgba,
    }
}

fn colours(p: &Pixmap, x0: u32, y0: u32, x1: u32, y1: u32) -> HashSet<[u8; 4]> {
    let mut set = HashSet::new();
    for y in y0..y1.min(p.size.height) {
        for x in x0..x1.min(p.size.width) {
            set.insert(p.pixel(x, y));
        }
    }
    set
}

fn opaque(hex: u32) -> [u8; 4] {
    [(hex >> 16) as u8, (hex >> 8) as u8, hex as u8, 255]
}

/// step 2's exit test: square boxes with 1 logical px borders and 1 px rules
/// contain only their exact colours at every scale, no blended pixels
#[test]
fn hairlines_are_crisp() {
    let (gpu, mut r) = setup();
    let assets = scenes::Assets::new(&mut r, &gpu);
    for scale in [120, 150, 180, 210, 240] {
        let scale = Scale::from_120ths(scale);
        let s = scale.as_f32();
        let scene = scenes::build("borders", &assets, scale).unwrap();
        let size = scale.to_physical(scenes::SIZE);
        let p = render(&gpu, &mut r, &scene, size, size);
        // first row: square-cornered boxes on the light background
        let got = colours(&p, 0, 0, size.width, (50.0 * s) as u32);
        let want = HashSet::from([opaque(0xf4f4f4), opaque(0xffffff), opaque(0x333333)]);
        assert_eq!(got, want, "boxes at {s}x");
        // rules on dark
        let got = colours(
            &p,
            0,
            (158.0 * s) as u32,
            (220.0 * s) as u32,
            (210.0 * s) as u32,
        );
        let want = HashSet::from([opaque(0x202020), opaque(0xe8e8e8)]);
        assert_eq!(got, want, "rules at {s}x");
    }
}

/// a frame bigger than its content repeats the content's last column and row
/// into the 2 px past them; beyond that is the clear colour
#[test]
fn resize_edge_repeats() {
    let (gpu, mut r) = setup();
    let assets = scenes::Assets::new(&mut r, &gpu);
    let scale = Scale::from_120ths(150);
    let scene = scenes::build("borders", &assets, scale).unwrap();
    let content = scale.to_physical(scenes::SIZE);
    let size = PhysicalSize::new(content.width + 37, content.height + 21);
    let p = render(&gpu, &mut r, &scene, size, content);
    let (cw, ch) = (content.width, content.height);
    for y in 0..ch {
        let edge = p.pixel(cw - 1, y);
        assert_eq!(
            (p.pixel(cw, y), p.pixel(cw + 1, y)),
            (edge, edge),
            "row {y}"
        );
        assert_eq!(p.pixel(cw + 2, y), [0, 0, 0, 255], "row {y}");
    }
    for x in 0..cw + 2 {
        let edge = p.pixel(x, ch - 1);
        assert_eq!(
            (p.pixel(x, ch), p.pixel(x, ch + 1)),
            (edge, edge),
            "column {x}"
        );
        assert_eq!(p.pixel(x, ch + 2), [0, 0, 0, 255], "column {x}");
    }
}

/// wallace's shadow against brute-force gaussian blur of the same rounded box
#[test]
fn shadow_matches_gaussian() {
    let (gpu, mut r) = setup();
    let size = PhysicalSize::new(200, 160);
    let (bx, by, bw, bh, radius, sigma) = (60.0, 50.0, 80.0, 60.0, 12.0, 8.0);
    let mut scene = Scene::new();
    scene.shadow(Shadow {
        rect: Rect::new(bx, by, bw, bh),
        radii: Corners::all(radius),
        blur: sigma * 2.0,
        color: Srgba8::hex(0xffffff).to_linear(),
        ..Default::default()
    });
    let p = render(&gpu, &mut r, &scene, size, size);

    // signed distance to the casting box
    let inside = |x: f32, y: f32| {
        let (hx, hy) = (bw / 2.0, bh / 2.0);
        let qx = (x - bx - hx).abs() - (hx - radius);
        let qy = (y - by - hy).abs() - (hy - radius);
        qx.max(qy).min(0.0) + (qx.max(0.0).powi(2) + qy.max(0.0).powi(2)).sqrt() - radius
    };
    let mut worst: f32 = 0.0;
    for y in 0..size.height {
        for x in 0..size.width {
            let (cx, cy) = (x as f32 + 0.5, y as f32 + 0.5);
            // the shadow is cut out under the box; skip it and its edge
            if inside(cx, cy) < 1.0 {
                continue;
            }
            let reach = 4.0 * sigma;
            let step = 0.5;
            let mut sum = 0.0;
            let mut total = 0.0;
            let mut sy = -reach;
            while sy <= reach {
                let mut sx = -reach;
                while sx <= reach {
                    let w = (-(sx * sx + sy * sy) / (2.0 * sigma * sigma)).exp();
                    total += w;
                    if inside(cx + sx, cy + sy) < 0.0 {
                        sum += w;
                    }
                    sx += step;
                }
                sy += step;
            }
            let want = sum / total;
            let got = srgb_to_linear(p.pixel(x, y)[0]);
            worst = worst.max((got - want).abs());
        }
    }
    // 8 bit quantisation alone is up to ~0.005 near white
    assert!(worst < 0.02, "worst coverage error {worst}");
}

fn srgb_to_linear(c: u8) -> f32 {
    let c = c as f32 / 255.0;
    if c <= 0.04045 {
        c / 12.92
    } else {
        ((c + 0.055) / 1.055).powf(2.4)
    }
}

#[test]
fn quad_with_no_scene_clears() {
    let (gpu, mut r) = setup();
    let size = PhysicalSize::new(8, 8);
    let p = render(&gpu, &mut r, &Scene::new(), size, size);
    assert_eq!(colours(&p, 0, 0, 8, 8), HashSet::from([[0, 0, 0, 255]]));
    let mut s = Scene::new();
    s.quad(Quad {
        rect: Rect::new(2.0, 2.0, 4.0, 4.0),
        fill: Srgba8::hex(0xffffff).to_linear().into(),
        ..Default::default()
    });
    let p = render(&gpu, &mut r, &s, size, size);
    assert_eq!(p.pixel(2, 2), [255, 255, 255, 255]);
    assert_eq!(p.pixel(1, 2), [0, 0, 0, 255]);
}
