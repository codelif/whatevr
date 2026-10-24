//! whatevr's native linux frontend.

use std::path::PathBuf;

use anyhow::Context;
use log::{LevelFilter, debug, error, warn};
use what_core::{KeyEvent, Keysym, LogicalSize, Modifiers, Srgba8, WindowId};
use what_platform::{App, FrameInfo, Platform, PlatformConfig, WindowConfig, WindowSize};
use what_render::{Gpu, Presenter, Renderer, WindowSurface, scenes};

const CLEAR: Srgba8 = Srgba8::hex(0x202020);

struct Whatgui {
    gpu: Gpu,
    surface: Option<WindowSurface>,
    renderer: Option<Renderer>,
    /// WHATGUI_SCENE: a what-render test scene instead of the plain clear
    scene: Option<(String, scenes::Assets)>,
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

impl App for Whatgui {
    fn resized(&mut self, platform: &mut Platform, id: WindowId, size: WindowSize) {
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
                if let Some(name) = std::env::var("WHATGUI_SCENE")
                    .ok()
                    .filter(|n| !n.is_empty())
                {
                    if scenes::NAMES.contains(&name.as_str()) {
                        let assets = scenes::Assets::new(&mut renderer, &self.gpu);
                        self.scene = Some((name, assets));
                    } else {
                        warn!("no scene {name:?}, have {:?}", scenes::NAMES);
                    }
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

    fn frame(&mut self, platform: &mut Platform, id: WindowId, _: FrameInfo) {
        let (Some(surface), Some(renderer), Some(size)) =
            (&self.surface, &mut self.renderer, platform.window_size(id))
        else {
            return;
        };
        let scene = match &self.scene {
            Some((name, assets)) => scenes::build(name, assets, size.scale),
            None => None,
        }
        .unwrap_or_default();
        let Some(mut frame) = surface.begin(&self.gpu) else {
            return;
        };
        renderer.draw(
            &self.gpu,
            &mut frame,
            &scene,
            CLEAR.premultiply(),
            size.physical,
        );
        surface.present(&self.gpu, frame, &mut Commit { platform, id });
    }

    fn close_requested(&mut self, platform: &mut Platform, id: WindowId) {
        self.renderer = None;
        self.surface = None;
        platform.close_window(id);
        platform.quit();
    }

    fn key(&mut self, platform: &mut Platform, id: WindowId, ev: &KeyEvent) {
        debug!("{id:?} {ev:?}");
        if ev.matches(Keysym::from_char('q'), Modifiers::CTRL) {
            self.close_requested(platform, id);
        }
    }
}

fn main() -> anyhow::Result<()> {
    what_core::log::init("WHATGUI_LOG", LevelFilter::Info);
    let frame_log = std::env::var_os("WHATGUI_FRAME_LOG").map(PathBuf::from);
    let mut platform = Platform::new(PlatformConfig { frame_log }).context("wayland")?;
    let gpu = Gpu::new(platform.display()).context("gpu")?;
    platform.create_window(&WindowConfig {
        title: "whatevr".into(),
        app_id: "in.codelif.whatevr".into(),
        size: LogicalSize::new(960.0, 640.0),
        min_size: Some(LogicalSize::new(360.0, 480.0)),
    });
    let mut app = Whatgui {
        gpu,
        surface: None,
        renderer: None,
        scene: None,
    };
    platform.run(&mut app)?;
    Ok(())
}
