//! step 4's exit test without a window: both lists scrolled as hard as a
//! touchpad and a wheel can, through prepends, incoming messages and resizes
//! mid-fling. no row may jump and no frame may shape on the spot. frames run
//! on a 60 hz wall clock so the shaping workers get the time they would on
//! screen; every 60th goes through lavapipe.

use std::time::{Duration, Instant};

use scroll_lab::fake::{BOTTOM, TOP};
use scroll_lab::{Lab, LabFrame};
use what_core::{
    AxisSource, Button, LogicalPoint, LogicalSize, PhysicalSize, PointerEvent, Scale, ScrollFrame,
    Timestamp,
};
use what_render::{Adapter, Gpu, Renderer, Scene, TextureFormat};
use what_text::TextSystem;
use what_ui::RendererSink;

#[global_allocator]
static ALLOC: what_core::alloc::Allocator = what_core::alloc::Allocator;

const FRAME_NS: u64 = 16_666_667;

struct Rig {
    lab: Lab,
    gpu: Gpu,
    renderer: Renderer,
    scene: Scene,
    size: (LogicalSize, Scale),
    n: u64,
    deadline: Instant,
    /// the next frame may shape on the spot (first anchoring, new width)
    excused: bool,
    sync: Vec<(u64, u32)>,
    cpu: Vec<Duration>,
}

impl Rig {
    fn new() -> Self {
        let gpu = Gpu::headless(Adapter::Lavapipe).expect("lavapipe");
        let renderer = Renderer::new(&gpu, TextureFormat::Rgba8Unorm);
        let mut lab = scroll_lab::lab(TextSystem::new(), || {});
        let size = (LogicalSize::new(960.0, 640.0), Scale::ONE);
        lab.resize(size.0, size.1);
        Self {
            lab,
            gpu,
            renderer,
            scene: Scene::default(),
            size,
            n: 0,
            deadline: Instant::now(),
            excused: true,
            sync: Vec::new(),
            cpu: Vec::new(),
        }
    }

    fn now(&self) -> Timestamp {
        Timestamp::from_nanos(1_000_000_000 + self.n * FRAME_NS)
    }

    fn ms(&self) -> u32 {
        (self.now().as_nanos() / 1_000_000) as u32
    }

    fn frame(&mut self) -> LabFrame {
        self.deadline += Duration::from_nanos(FRAME_NS);
        if let Some(d) = self.deadline.checked_duration_since(Instant::now()) {
            std::thread::sleep(d);
        } else {
            self.deadline = Instant::now();
        }
        self.scene.clear();
        let now = self.now();
        let f = self.lab.frame(
            now,
            &mut self.scene,
            &mut RendererSink {
                renderer: &mut self.renderer,
                gpu: &self.gpu,
            },
        );
        let sync = f.chat.sync_shaped + f.side.sync_shaped;
        if sync > 0 && !self.excused {
            self.sync.push((self.n, sync));
        }
        self.excused = false;
        self.cpu.push(f.cpu);
        if std::env::var_os("SCRIPTED_TRACE").is_some() {
            eprintln!(
                "{}\t{}\t{}\t{}\t{}\t{}\t{:?}\t{:?}\t{:?}\t{:?}",
                self.n,
                f.chat.moved,
                f.side.moved,
                sync,
                f.cpu.as_micros(),
                f.chat.jumped as u8,
                (f.chat.sync_shaped, f.side.sync_shaped),
                self.lab.chat_rows().first().map(|r| r.0),
                self.lab.chat.oldest_loaded(),
                self.lab.side_rows().first().map(|r| r.0)
            );
        }
        if self.n.is_multiple_of(60) {
            let s = self.size.1.as_f32();
            let px = PhysicalSize::new(
                (self.size.0.width * s).round() as u32,
                (self.size.0.height * s).round() as u32,
            );
            self.renderer
                .render_pixmap(
                    &self.gpu,
                    &self.scene,
                    px,
                    px,
                    self.lab.theme.bg.premultiply(),
                )
                .expect("render");
            self.deadline = Instant::now();
        }
        self.n += 1;
        f
    }

    fn frames(&mut self, n: usize) {
        for _ in 0..n {
            self.frame();
        }
    }

    fn resize(&mut self, w: f32, h: f32) {
        let size = LogicalSize::new(w, h);
        if std::env::var_os("SCRIPTED_TRACE").is_some() {
            eprintln!("resize {w}x{h} before frame {}", self.n);
        }
        self.excused |= size.width != self.size.0.width;
        self.size.0 = size;
        self.lab.resize(size, self.size.1);
    }

    fn ev(&mut self, ev: PointerEvent) {
        let now = self.now();
        self.lab.pointer(&ev, now);
    }

    fn at(&mut self, x: f32, y: f32) {
        let time_ms = self.ms();
        self.ev(PointerEvent::Motion {
            pos: LogicalPoint::new(x, y),
            time_ms,
        });
    }

    fn scroll(&mut self, f: ScrollFrame) {
        self.ev(PointerEvent::Scroll(f));
    }

    /// fingers land (stopping any fling), move `dy` raw px a frame for
    /// `frames` frames, and lift into a fling
    fn flick(&mut self, dy: f32, frames: usize) {
        let t = self.ms();
        self.ev(PointerEvent::Hold {
            begin: true,
            cancelled: false,
            time_ms: t,
        });
        self.ev(PointerEvent::Hold {
            begin: false,
            cancelled: true,
            time_ms: t,
        });
        for _ in 0..frames {
            let time_ms = self.ms();
            self.scroll(ScrollFrame {
                source: Some(AxisSource::Finger),
                time_ms,
                delta: [0.0, dy],
                ..ScrollFrame::default()
            });
            self.frame();
        }
        let time_ms = self.ms();
        self.scroll(ScrollFrame {
            source: Some(AxisSource::Finger),
            time_ms,
            stop: [false, true],
            ..ScrollFrame::default()
        });
    }

    fn notch(&mut self, n: i32) {
        let time_ms = self.ms();
        self.scroll(ScrollFrame {
            source: Some(AxisSource::Wheel),
            time_ms,
            delta: [0.0, 15.0 * n as f32],
            value120: [0, 120 * n],
            ..ScrollFrame::default()
        });
    }

    fn click(&mut self, x: f32, y: f32) {
        let (pos, time_ms) = (LogicalPoint::new(x, y), self.ms());
        for pressed in [true, false] {
            self.ev(PointerEvent::Button {
                button: Button::Left,
                pressed,
                pos,
                time_ms,
            });
        }
    }

    fn press(&mut self, x: f32, y: f32, pressed: bool) {
        let (pos, time_ms) = (LogicalPoint::new(x, y), self.ms());
        self.ev(PointerEvent::Button {
            button: Button::Left,
            pressed,
            pos,
            time_ms,
        });
    }

    /// grabs the thumb in the strip at `x` and drags it up and back down a
    /// couple of px a frame, every frame a fresh screen. returns the frames
    /// the rows didn't follow on
    fn drag(&mut self, x: f32, rows: fn(&Lab) -> Option<u32>) -> usize {
        self.at(x, 300.0);
        self.frames(10);
        self.press(x, 300.0, true);
        self.frames(10);
        let (mut prev, mut stuck) = (rows(&self.lab), 0);
        for i in 0..240 {
            let y = if i < 120 {
                300.0 - i as f32 * 2.0
            } else {
                60.0 + (i - 120) as f32 * 2.0
            };
            self.at(x, y);
            self.frame();
            let now = rows(&self.lab);
            stuck += (now == prev) as usize;
            prev = now;
        }
        self.press(x, 300.0, false);
        self.frames(30);
        stuck
    }

    fn chat_has(&self, k: u32) -> bool {
        self.lab.chat_rows().iter().any(|r| r.0 == k)
    }

    fn side_first(&self) -> u32 {
        self.lab.side_rows().first().map_or(0, |r| r.0)
    }

    fn report(&mut self) {
        self.cpu.sort();
        let p = |q: f64| self.cpu[((self.cpu.len() - 1) as f64 * q) as usize];
        eprintln!(
            "{} frames, ui cpu p50 {:?} p99 {:?} max {:?}",
            self.cpu.len(),
            p(0.5),
            p(0.99),
            p(1.0)
        );
        let (c, s) = (&self.lab.chat_check, &self.lab.side_check);
        assert!(c.jumps.is_empty(), "chat rows jumped: {:?}", c.jumps);
        assert!(s.jumps.is_empty(), "sidebar rows jumped: {:?}", s.jumps);
        assert!(
            self.sync.is_empty(),
            "shaped on the spot (frame, rows): {:?}",
            self.sync
        );
        // debug builds are ~10x slower; the budget only means something optimized
        if !cfg!(debug_assertions) {
            assert!(p(0.99) < Duration::from_millis(2), "ui p99 over budget");
        }
    }
}

#[test]
fn hard_scrolling_never_jumps_or_stalls() {
    let mut r = Rig::new();
    r.frames(30);

    // motion that changes nothing on screen asks for no frame
    let still = |r: &mut Rig, x: f32, y: f32| {
        let (now, time_ms) = (r.now(), r.ms());
        let ev = PointerEvent::Motion {
            pos: LogicalPoint::new(x, y),
            time_ms,
        };
        r.lab.pointer(&ev, now)
    };
    still(&mut r, 640.0, 300.0);
    r.frames(60);
    assert!(!still(&mut r, 650.0, 310.0), "chat motion redrew");
    assert!(still(&mut r, 160.0, 120.0), "a new sidebar hover didn't");
    r.frames(60);
    assert!(!still(&mut r, 170.0, 125.0), "same sidebar row redrew");

    // chat: the thumb dragged to the oldest loaded rows, then flung on up
    // through every prepend to the very top
    r.click(960.0 - 8.0, 50.0);
    r.frames(30);
    r.at(640.0, 320.0);
    let mut i = 0;
    while !r.chat_has(TOP) {
        r.flick(-60.0, 5);
        r.frames(12);
        i += 1;
        if i == 4 {
            scroll_lab::incoming(&mut r.lab);
            r.resize(960.0, 720.0);
        }
        if i == 8 {
            r.resize(1100.0, 720.0);
        }
        assert!(i < 400, "never reached the top");
    }
    assert!(!r.lab.chat.has_more());
    r.frames(30);

    // and back down hard, a wheel burst in the middle
    for i in 0..30 {
        r.flick(80.0, 4);
        r.frames(8);
        if i == 10 {
            r.resize(900.0, 600.0);
            for _ in 0..20 {
                r.notch(1);
                r.frames(2);
            }
        }
        if i % 7 == 0 {
            scroll_lab::incoming(&mut r.lab);
        }
    }
    r.frames(60);

    // corner button back to the newest message
    r.click(r.size.0.width - 40.0, r.size.0.height - 72.0);
    r.frames(60);
    assert!(
        r.chat_has(BOTTOM),
        "corner button didn't land on the newest"
    );

    // thumb drags: every frame a new screen, which may trail a frame
    // behind but never wait on the ui thread
    let x = r.size.0.width - 8.0;
    let stuck = r.drag(x, |l| l.chat_rows().first().map(|r| r.0));
    eprintln!("chat drag: rows stayed put on {stuck} of 240 frames");
    assert!(stuck < 120, "chat rows didn't follow the thumb");
    let stuck = r.drag(330.0 - 8.0, |l| l.side_rows().first().map(|r| r.0));
    eprintln!("sidebar drag: rows stayed put on {stuck} of 240 frames");
    assert!(stuck < 120, "sidebar rows didn't follow the thumb");
    r.click(r.size.0.width - 40.0, r.size.0.height - 72.0);
    r.frames(60);

    // sidebar: down through thousands of chats, a resize mid-fling, back up
    r.at(160.0, 300.0);
    for i in 0..50 {
        r.flick(70.0, 4);
        r.frames(10);
        if i == 15 {
            r.resize(960.0, 680.0);
        }
    }
    assert!(
        r.side_first() > 2000,
        "sidebar only got to {}",
        r.side_first()
    );
    for _ in 0..60 {
        r.flick(-70.0, 4);
        r.frames(10);
        if r.side_first() == 0 {
            break;
        }
    }
    r.frames(60);
    r.report();
}
