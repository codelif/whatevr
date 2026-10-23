//! Renders scripted UI states to PNG for pixel diffs.

use std::path::PathBuf;

use anyhow::{Context, bail};
use clap::Parser;
use log::LevelFilter;
use what_core::{Scale, Srgba8};
use what_render::{Adapter, Gpu, Renderer, TextureFormat, scenes};

/// render a what-render test scene to a png
#[derive(Parser)]
struct Args {
    /// scene name, see --list
    scene: Option<String>,
    /// output scale, e.g. 1, 1.25, 2
    #[arg(long, default_value_t = 1.0)]
    scale: f32,
    /// output file, default <scene>@<scale>.png
    #[arg(short, long)]
    out: Option<PathBuf>,
    /// use the real gpu instead of lavapipe (pixels then won't match the goldens)
    #[arg(long)]
    gpu: bool,
    /// print the scene names and exit
    #[arg(long)]
    list: bool,
}

fn main() -> anyhow::Result<()> {
    what_core::log::init("WHATGUI_LOG", LevelFilter::Warn);
    let args = Args::parse();
    if args.list {
        for name in scenes::NAMES {
            println!("{name}");
        }
        return Ok(());
    }
    let Some(name) = args.scene else {
        bail!("no scene given, see --list");
    };
    let scale = Scale::from_120ths((args.scale * 120.0).round() as u32);
    let gpu = Gpu::headless(if args.gpu {
        Adapter::Default
    } else {
        Adapter::Lavapipe
    })?;
    let mut renderer = Renderer::new(&gpu, TextureFormat::Rgba8UnormSrgb);
    let assets = scenes::Assets::new(&mut renderer, &gpu);
    let scene = scenes::build(&name, &assets, scale)
        .with_context(|| format!("no scene {name:?}, see --list"))?;
    let size = scale.to_physical(scenes::SIZE);
    let pixmap = renderer.render_pixmap(&gpu, &scene, size, size, Srgba8::hex(0).to_linear())?;
    let out = args
        .out
        .unwrap_or_else(|| PathBuf::from(format!("{name}@{}.png", args.scale)));
    pixmap.save_png(&out)?;
    println!("{}", out.display());
    Ok(())
}
