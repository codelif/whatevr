//! the scroll lab in a window. SCROLL_LAB_INCOMING_MS sets how often a fake
//! message arrives (0 for never), SCROLL_LAB_LOG writes a tsv line per frame,
//! SCROLL_LAB_FRAME_LOG is the platform's presentation timeline. a summary
//! goes to stderr on exit.

use std::collections::BTreeMap;
use std::fs::File;
use std::io::{BufWriter, Write as _};
use std::os::fd::{AsFd, OwnedFd};
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use anyhow::Context;
use log::{LevelFilter, error, warn};
use rustix::event::{EventfdFlags, eventfd};
use scroll_lab::Lab;
use what_core::{KeyEvent, Keysym, LogicalSize, Modifiers, PointerEvent, Token, WindowId};
use what_platform::{
    App, FrameInfo, Platform, PlatformConfig, PresentInfo, WindowConfig, WindowSize,
};
use what_render::{Gpu, Presenter, Renderer, Scene, WindowSurface};
use what_text::TextSystem;
use what_ui::RendererSink;

#[global_allocator]
static ALLOC: what_core::alloc::Allocator = what_core::alloc::Allocator;

const WAKE: Token = Token(1);
const INCOMING: Token = Token(2);

/// one frame's numbers, written once its gpu time and presentation are in
#[derive(Default)]
struct Rec {
    cpu: Duration,
    gpu: Option<Duration>,
    late: Option<u32>,
    chat_moved: i32,
    side_moved: i32,
    sync: u32,
    jumps: usize,
}

struct Stats {
    log: Option<BufWriter<File>>,
    open: BTreeMap<u64, Rec>,
    total: Vec<Duration>,
    cpu: Vec<Duration>,
    late: u32,
    sync: u32,
    jumps: usize,
}

impl Stats {
    fn close(&mut self, seq: u64) {
        let Some(r) = self.open.get(&seq) else {
            return;
        };
        if r.gpu.is_none() || r.late.is_none() {
            return;
        }
        let r = self.open.remove(&seq).unwrap();
        let gpu = r.gpu.unwrap_or_default();
        self.total.push(r.cpu + gpu);
        self.cpu.push(r.cpu);
        self.late += r.late.unwrap_or(0);
        if let Some(f) = &mut self.log {
            let _ = writeln!(
                f,
                "{seq}\t{}\t{}\t{}\t{}\t{}\t{}\t{}",
                r.cpu.as_micros(),
                gpu.as_micros(),
                r.late.unwrap_or(0),
                r.chat_moved,
                r.side_moved,
                r.sync,
                r.jumps
            );
        }
        // frames whose feedback never came don't pile up
        while self.open.len() > 64 {
            self.open.pop_first();
        }
    }

    fn summary(&mut self) {
        let pct = |v: &mut Vec<Duration>, p: f64| {
            v.sort();
            v.get(((v.len() as f64 - 1.0) * p).round() as usize)
                .map_or(0.0, |d| d.as_secs_f64() * 1000.0)
        };
        let n = self.total.len();
        let over = self
            .total
            .iter()
            .filter(|d| **d > Duration::from_millis(2))
            .count();
        eprintln!(
            "frames {n}, cpu+gpu p50 {:.2} p99 {:.2} max {:.2} ms, cpu p99 {:.2} ms, over 2 ms {over}, late vblanks {}, sync shapes {}, jumps {}",
            pct(&mut self.total, 0.5),
            pct(&mut self.total, 0.99),
            pct(&mut self.total, 1.0),
            pct(&mut self.cpu, 0.99),
            self.late,
            self.sync,
            self.jumps,
        );
        if let Some(f) = &mut self.log {
            let _ = f.flush();
        }
    }
}

struct Window {
    gpu: Gpu,
    surface: Option<WindowSurface>,
    renderer: Option<Renderer>,
    lab: Lab,
    scene: Scene,
    wake: Arc<OwnedFd>,
    id: Option<WindowId>,
    incoming: Option<Duration>,
    /// gpu timestamps work here
    timing: bool,
    stats: Stats,
}

struct Commit<'a> {
    platform: &'a mut Platform,
    id: WindowId,
}

impl Presenter for Commit<'_> {
    fn before_commit(&mut self) {
        self.platform.pre_present(self.id);
    }
}

impl App for Window {
    fn resized(&mut self, platform: &mut Platform, id: WindowId, size: WindowSize) {
        self.id = Some(id);
        self.lab.resize(size.logical, size.scale);
        if let Some(s) = self.surface.as_mut() {
            s.resize(&self.gpu, size.buffer);
            return;
        }
        let Some((display, window)) = platform.raw_handles(id) else {
            return;
        };
        // SAFETY: close_requested drops the surface before closing the window
        match unsafe { WindowSurface::new(&self.gpu, display, window, size.buffer) } {
            Ok(s) => {
                let mut renderer = Renderer::new(&self.gpu, s.format());
                self.timing = renderer.enable_timing(&self.gpu);
                if !self.timing {
                    warn!("no gpu timestamps, frame times are cpu only");
                }
                self.renderer = Some(renderer);
                self.surface = Some(s);
            }
            Err(e) => {
                error!("{e}");
                platform.quit();
            }
        }
    }

    fn frame(&mut self, platform: &mut Platform, id: WindowId, info: FrameInfo) {
        let (Some(surface), Some(renderer), Some(size)) =
            (&self.surface, &mut self.renderer, platform.window_size(id))
        else {
            return;
        };
        self.scene.clear();
        let f = self.lab.frame(
            info.time,
            &mut self.scene,
            &mut RendererSink {
                renderer,
                gpu: &self.gpu,
            },
        );
        let jumps = f.chat_jumps + f.side_jumps;
        if jumps > 0 {
            warn!(
                "frame {}: {} chat, {} sidebar rows jumped",
                info.seq, f.chat_jumps, f.side_jumps
            );
        }
        let sync = f.chat.sync_shaped + f.side.sync_shaped;
        self.stats.sync += sync;
        self.stats.jumps += jumps;
        self.stats.open.insert(
            info.seq,
            Rec {
                cpu: f.cpu,
                chat_moved: f.chat.moved,
                side_moved: f.side.moved,
                sync,
                jumps,
                gpu: (!self.timing).then_some(Duration::ZERO),
                ..Rec::default()
            },
        );
        let Some(mut frame) = surface.begin(&self.gpu) else {
            return;
        };
        renderer.set_frame_tag(info.seq);
        renderer.draw(
            &self.gpu,
            &mut frame,
            &self.scene,
            self.lab.theme.bg.premultiply(),
            size.physical,
        );
        surface.present(&self.gpu, frame, &mut Commit { platform, id });
        for (seq, t) in renderer.take_gpu_times() {
            if let Some(r) = self.stats.open.get_mut(&seq) {
                r.gpu = Some(t);
                self.stats.close(seq);
            }
        }
        if self.lab.animating(Platform::now()) {
            platform.request_redraw(id);
        }
    }

    fn presented(&mut self, _: &mut Platform, _: WindowId, p: PresentInfo) {
        if let Some(r) = self.stats.open.get_mut(&p.seq) {
            r.late = Some(p.late);
            self.stats.close(p.seq);
        }
    }

    fn close_requested(&mut self, platform: &mut Platform, id: WindowId) {
        self.renderer = None;
        self.surface = None;
        platform.close_window(id);
        platform.quit();
    }

    fn key(&mut self, platform: &mut Platform, id: WindowId, ev: &KeyEvent) {
        if ev.matches(Keysym::from_char('q'), Modifiers::CTRL) {
            self.close_requested(platform, id);
        }
    }

    fn pointer(&mut self, platform: &mut Platform, id: WindowId, ev: &PointerEvent) {
        if self.lab.pointer(ev, Platform::now()) {
            platform.request_redraw(id);
        }
    }

    fn fd_ready(&mut self, platform: &mut Platform, token: Token) {
        if token == WAKE {
            let mut b = [0u8; 8];
            let _ = rustix::io::read(self.wake.as_fd(), &mut b);
            if let Some(id) = self.id {
                platform.request_redraw(id);
            }
        }
    }

    fn timer(&mut self, platform: &mut Platform, token: Token) {
        if token == INCOMING
            && let Some(every) = self.incoming
        {
            self.lab.incoming();
            if let Some(id) = self.id {
                platform.request_redraw(id);
            }
            if let Err(e) = platform.set_timer(INCOMING, every) {
                warn!("incoming timer: {e}");
            }
        }
    }
}

fn main() -> anyhow::Result<()> {
    what_core::log::init("SCROLL_LAB_LOG_LEVEL", LevelFilter::Info);
    let frame_log = std::env::var_os("SCROLL_LAB_FRAME_LOG").map(PathBuf::from);
    let log = match std::env::var_os("SCROLL_LAB_LOG") {
        Some(p) => {
            let mut f = BufWriter::new(File::create(&p).context("SCROLL_LAB_LOG")?);
            writeln!(
                f,
                "seq\tcpu_us\tgpu_us\tlate\tchat_moved\tside_moved\tsync\tjumps"
            )?;
            Some(f)
        }
        None => None,
    };
    let incoming = match std::env::var("SCROLL_LAB_INCOMING_MS") {
        Ok(v) => v.parse::<u64>().context("SCROLL_LAB_INCOMING_MS")?,
        Err(_) => 3000,
    };
    let incoming = (incoming > 0).then(|| Duration::from_millis(incoming));

    let mut platform = Platform::new(PlatformConfig { frame_log }).context("wayland")?;
    let gpu = Gpu::new(platform.display()).context("gpu")?;
    let wake = Arc::new(eventfd(0, EventfdFlags::CLOEXEC | EventfdFlags::NONBLOCK)?);
    platform.add_fd(wake.as_fd(), WAKE)?;
    if let Some(every) = incoming {
        platform.set_timer(INCOMING, every)?;
    }
    let w = wake.clone();
    let lab = Lab::new(TextSystem::new(), move || {
        let _ = rustix::io::write(w.as_fd(), &1u64.to_ne_bytes());
    });
    platform.create_window(&WindowConfig {
        title: "scroll lab".into(),
        app_id: "in.codelif.whatevr.scroll-lab".into(),
        size: LogicalSize::new(960.0, 640.0),
        min_size: Some(LogicalSize::new(480.0, 360.0)),
    });
    let mut app = Window {
        gpu,
        surface: None,
        renderer: None,
        lab,
        scene: Scene::default(),
        wake,
        id: None,
        incoming,
        timing: false,
        stats: Stats {
            log,
            open: BTreeMap::new(),
            total: Vec::new(),
            cpu: Vec::new(),
            late: 0,
            sync: 0,
            jumps: 0,
        },
    };
    platform.run(&mut app)?;
    app.stats.summary();
    Ok(())
}
