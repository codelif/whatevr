//! textcompare: every case rendered by headless firefox (and chromium with
//! --chromium) and by what-text on lavapipe. compare.png has one row per
//! case: label, firefox, ours, chromium, then the differences. zoom.html
//! shows one case at a time, magnified, each sample line stacked so a glyph
//! sits right above or below the one it's compared with. differences are
//! grey, brighter where the two disagree more.

use std::path::{Path, PathBuf};
use std::process::Command;

use anyhow::{Context, bail};
use clap::Parser;
use what_core::{PhysicalSize, Scale, Srgba8};
use what_render::Pixmap;
use what_text::{GlyphKind, Style, TextSystem};

use textcompare::{Case, Ours, WIDTH, bundled, html};

#[derive(Parser)]
#[command(about)]
struct Args {
    /// where the pages, shots and sheets go
    #[arg(long, default_value = concat!(env!("CARGO_MANIFEST_DIR"), "/../../target/textcompare"))]
    out: PathBuf,
    /// system fonts and fallback on both sides instead of the bundled ones
    #[arg(long)]
    system: bool,
    /// differences up to this much per channel count as equal
    #[arg(long, default_value_t = 0)]
    tolerance: u8,
    /// also shoot every case in headless chromium
    #[arg(long)]
    chromium: bool,
}

const GAP: u32 = 8;
const LABEL: u32 = 150;
/// the difference image, multiplied so small errors still show
const GAIN: u32 = 4;
/// ink this close to a line's crop stays in it
const PAD: u32 = 2;
const GREY: [u8; 4] = [128, 128, 128, 255];

/// one case's images in sheet order: name, directory, pixels
struct Row {
    case: Case,
    images: Vec<(&'static str, &'static str, Pixmap)>,
}

fn main() -> anyhow::Result<()> {
    let args = Args::parse();
    let cases = Case::all();
    let mut dirs = vec!["html", "firefox", "ours", "diff-firefox"];
    if args.chromium {
        dirs.extend(["chromium", "diff-chromium"]);
    }
    for d in dirs {
        std::fs::create_dir_all(args.out.join(d))?;
    }
    let out = args.out.canonicalize()?;

    let mut jobs = Vec::new();
    for c in &cases {
        let page = page(&out, c);
        std::fs::write(&page, html(c, args.system))?;
        jobs.push(format!(
            r#"{{"html":{:?},"out":{:?},"dpr":{},"width":{WIDTH},"height":{}}}"#,
            page.display().to_string(),
            shot(&out, "firefox", c).display().to_string(),
            c.scale.as_f32(),
            c.height()
        ));
    }
    let jobs_file = out.join("jobs.json");
    std::fs::write(&jobs_file, format!("[{}]", jobs.join(",\n")))?;
    let helper = Path::new(env!("CARGO_MANIFEST_DIR")).join("firefox.py");
    let status = Command::new("python3")
        .arg("-I")
        .arg(&helper)
        .arg(&jobs_file)
        .status()
        .context("running python3")?;
    if !status.success() {
        bail!("{} failed", helper.display());
    }
    if args.chromium {
        for c in &cases {
            chromium(&out, c)?;
        }
    }

    let mut ts = if args.system {
        TextSystem::new()
    } else {
        bundled()
    };
    let mut ours = Ours::new();
    let mut rows = Vec::new();
    print!("{:<18} {:>9} {:>9}", "case", "differ", "max diff");
    if args.chromium {
        print!(" {:>9} {:>9}", "chromium", "max diff");
    }
    println!();
    for c in &cases {
        let load = |side: &str| {
            Pixmap::load_png(&shot(&out, side, c))
                .with_context(|| format!("{side} shot for {}", c.name()))
        };
        let mine = ours.render(&mut ts, c);
        mine.save_png(&shot(&out, "ours", c))?;
        let mut images = vec![
            ("firefox", "firefox", load("firefox")?),
            ("ours", "ours", mine.clone()),
        ];
        if args.chromium {
            images.push(("chromium", "chromium", load("chromium")?));
        }
        print!("{:<18}", c.name());
        let mut diffs = Vec::new();
        for (side, _, p) in images.iter().filter(|i| i.0 != "ours") {
            if p.size != mine.size {
                bail!("{}: {side} {:?}, ours {:?}", c.name(), p.size, mine.size);
            }
            let (diff, n, max) = difference(p, &mine, args.tolerance);
            print!(" {n:>9} {max:>9}");
            let (name, dir) = if *side == "firefox" {
                ("diff ff", "diff-firefox")
            } else {
                ("diff chr", "diff-chromium")
            };
            diff.save_png(&shot(&out, dir, c))?;
            diffs.push((name, dir, diff));
        }
        println!();
        images.extend(diffs);
        rows.push(Row { case: *c, images });
    }

    let width = rows
        .iter()
        .map(|r| LABEL + r.images.iter().map(|i| i.2.size.width + GAP).sum::<u32>())
        .max()
        .unwrap_or(0);
    let height: u32 = rows.iter().map(|r| r.images[0].2.size.height + GAP).sum();
    let mut sheet = blank(width, height);
    let mut y = 0;
    for r in &rows {
        let h = r.images[0].2.size.height;
        blit(
            &mut sheet,
            &label(&mut ts, &case_label(&r.case), LABEL - GAP, h),
            0,
            y,
        );
        let mut x = LABEL;
        for (_, _, p) in &r.images {
            blit(&mut sheet, p, x, y);
            x += p.size.width + GAP;
        }
        y += h + GAP;
    }
    let path = out.join("compare.png");
    sheet.save_png(&path)?;
    println!("{}", path.display());

    let path = out.join("zoom.html");
    std::fs::write(&path, zoom_page(&rows))?;
    println!("{}", path.display());
    Ok(())
}

fn page(out: &Path, c: &Case) -> PathBuf {
    out.join("html").join(format!("{}.html", c.name()))
}

fn shot(out: &Path, side: &str, c: &Case) -> PathBuf {
    out.join(side).join(format!("{}.png", c.name()))
}

fn chromium(out: &Path, c: &Case) -> anyhow::Result<()> {
    let status = Command::new("chromium")
        .args([
            "--headless",
            "--no-first-run",
            "--allow-file-access-from-files",
            "--hide-scrollbars",
            "--virtual-time-budget=3000",
        ])
        .arg(format!(
            "--user-data-dir={}",
            out.join("chromium-profile").display()
        ))
        .arg(format!("--force-device-scale-factor={}", c.scale.as_f32()))
        .arg(format!("--window-size={WIDTH},{}", c.height()))
        .arg(format!(
            "--screenshot={}",
            shot(out, "chromium", c).display()
        ))
        .arg(format!("file://{}", page(out, c).display()))
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .status()
        .context("running chromium")?;
    if !status.success() {
        bail!("chromium failed on {}", c.name());
    }
    Ok(())
}

/// the viewer with every case's lines, each cropped to the ink of all the
/// shots in it
fn zoom_page(rows: &[Row]) -> String {
    let sides = rows[0]
        .images
        .iter()
        .map(|(name, dir, _)| format!("[{name:?},{dir:?}]"))
        .collect::<Vec<_>>()
        .join(",");
    let cases = rows
        .iter()
        .map(|r| {
            let shots: Vec<&Pixmap> = r
                .images
                .iter()
                .filter(|i| !i.1.starts_with("diff"))
                .map(|i| &i.2)
                .collect();
            let lines = r
                .case
                .lines()
                .into_iter()
                .map(|l| {
                    let (x0, x1) = ink(&shots, l.clone()).unwrap_or((0, 1));
                    let w = shots[0].size.width;
                    let (x0, x1) = (x0.saturating_sub(PAD), (x1 + PAD).min(w));
                    let bottom = l.end.min(shots[0].size.height);
                    format!("[{},{bottom},{x0},{x1}]", l.start)
                })
                .collect::<Vec<_>>()
                .join(",");
            format!(
                r#"{{"name":{:?},"label":{:?},"lines":[{lines}]}}"#,
                r.case.name(),
                case_label(&r.case)
            )
        })
        .collect::<Vec<_>>()
        .join(",\n");
    include_str!("../zoom.html").replace(
        "/*DATA*/null",
        &format!(r#"{{"sides":[{sides}],"cases":[{cases}]}}"#),
    )
}

/// leftmost and one past the rightmost column in `rows` that isn't the
/// background in any of `shots`
fn ink(shots: &[&Pixmap], rows: std::ops::Range<u32>) -> Option<(u32, u32)> {
    let bg = shots[0].pixel(0, 0);
    let w = shots[0].size.width;
    let h = shots[0].size.height;
    let hit = |x: u32| {
        shots.iter().any(|p| {
            rows.clone()
                .take_while(|&y| y < h)
                .any(|y| p.pixel(x, y) != bg)
        })
    };
    let x0 = (0..w).find(|&x| hit(x))?;
    let x1 = (0..w).rev().find(|&x| hit(x))? + 1;
    Some((x0, x1))
}

fn blank(w: u32, h: u32) -> Pixmap {
    Pixmap {
        size: PhysicalSize::new(w, h),
        rgba: GREY.repeat((w * h) as usize),
    }
}

/// grey per pixel from the largest channel difference; also how many pixels
/// differ beyond `tolerance` and by how much at most
fn difference(a: &Pixmap, b: &Pixmap, tolerance: u8) -> (Pixmap, usize, u8) {
    let mut rgba = Vec::with_capacity(a.rgba.len());
    let (mut n, mut max) = (0, 0);
    for (p, q) in a.rgba.chunks_exact(4).zip(b.rgba.chunks_exact(4)) {
        let d = (0..3).map(|i| p[i].abs_diff(q[i])).max().unwrap();
        if d > tolerance {
            n += 1;
        }
        max = max.max(d);
        let v = (d as u32 * GAIN).min(255) as u8;
        rgba.extend_from_slice(&[v, v, v, 255]);
    }
    (Pixmap { size: a.size, rgba }, n, max)
}

fn blit(dst: &mut Pixmap, src: &Pixmap, x: u32, y: u32) {
    for row in 0..src.size.height {
        let s = (row * src.size.width * 4) as usize;
        let d = (((y + row) * dst.size.width + x) * 4) as usize;
        let n = (src.size.width * 4) as usize;
        dst.rgba[d..d + n].copy_from_slice(&src.rgba[s..s + n]);
    }
}

fn case_label(c: &Case) -> String {
    format!("{}px {:?} {}x", c.size, c.theme, c.scale_label)
}

/// white text on the sheet's grey, blended on the cpu: it only has to be legible
fn label(ts: &mut TextSystem, text: &str, w: u32, h: u32) -> Pixmap {
    let mut px = blank(w, h);
    let style = Style {
        size: 13.0,
        color: Srgba8::hex(0xffffff).premultiply(),
        ..Style::default()
    };
    let p = ts.paragraph(text, &style, &[], Scale::from_120ths(120));
    for g in p.glyphs() {
        let Some(r) = ts.rasterize(g.key) else {
            continue;
        };
        if r.kind != GlyphKind::Mask {
            continue;
        }
        for ry in 0..r.height as i32 {
            for rx in 0..r.width as i32 {
                let (x, y) = (4 + g.x + r.left + rx, 4 + g.y - r.top + ry);
                if x < 0 || y < 0 || x >= w as i32 || y >= h as i32 {
                    continue;
                }
                let a = r.data[(ry * r.width as i32 + rx) as usize] as u32;
                let i = ((y as u32 * w + x as u32) * 4) as usize;
                for ch in &mut px.rgba[i..i + 3] {
                    *ch = ((*ch as u32 * (255 - a) + 255 * a) / 255) as u8;
                }
            }
        }
    }
    px
}
