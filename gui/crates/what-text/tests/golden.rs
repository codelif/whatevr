//! textcompare's cases rendered on lavapipe, compared byte for byte.
//! WHAT_BLESS=1 rewrites the goldens.

use std::path::{Path, PathBuf};

use textcompare::{Case, Ours, bundled};
use what_render::Pixmap;

#[test]
fn goldens() {
    let mut ts = bundled();
    let mut ours = Ours::new();
    let dir = Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/golden");
    let out = PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("text-golden-failures");
    let bless = std::env::var_os("WHAT_BLESS").is_some_and(|v| v == "1");
    // stale failures from an earlier run would mislead
    let _ = std::fs::remove_dir_all(&out);
    let mut failures = Vec::new();
    for case in Case::all() {
        let name = case.name();
        let got = ours.render(&mut ts, &case);
        let file = format!("{name}.png");
        let path = dir.join(&file);
        if bless {
            std::fs::create_dir_all(&dir).unwrap();
            got.save_png(&path).unwrap();
            continue;
        }
        let Ok(want) = Pixmap::load_png(&path) else {
            failures.push(format!(
                "{file}: missing, run WHAT_BLESS=1 cargo test -p what-text"
            ));
            continue;
        };
        if let Some(n) = differing(&want, &got) {
            std::fs::create_dir_all(&out).unwrap();
            got.save_png(&out.join(format!("{name}.actual.png")))
                .unwrap();
            diff(&want, &got)
                .save_png(&out.join(format!("{name}.diff.png")))
                .unwrap();
            failures.push(format!(
                "{file}: {n} pixels differ, see {}.*.png",
                out.join(&name).display()
            ));
        }
    }
    assert!(failures.is_empty(), "\n{}", failures.join("\n"));
}

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
