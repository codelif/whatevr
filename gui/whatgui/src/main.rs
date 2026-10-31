//! whatevr's native linux frontend.

mod chats;
mod clock;
mod daemon;
mod messages;
mod rows;

use std::collections::HashMap;
use std::os::fd::{AsFd, OwnedFd};
use std::path::PathBuf;
use std::sync::Arc;

use anyhow::Context;
use clap::Parser;
use log::{LevelFilter, debug, error, info, warn};
use rustix::event::{EventfdFlags, eventfd};
use what_chat::shell::{Conversation, Shell};
use what_core::{KeyEvent, Keysym, LogicalSize, Modifiers, PointerEvent, Token, WindowId};
use what_platform::{App, FrameInfo, Platform, PlatformConfig, WindowConfig, WindowSize};
use what_proto::v2::oneof::request::Method;
use what_proto::v2::oneof::subscribe::View;
use what_proto::v2::view::ViewUpdateView;
use what_proto::v2::view::oneof::event::Event;
use what_proto::v2::view::oneof::response::Result as Answer;
use what_proto::v2::view::oneof::upsert::Item as Row;
use what_proto::{Client, Incoming};
use what_render::{Gpu, Presenter, Renderer, Scene, WindowSurface, scenes};
use what_text::TextSystem;
use what_ui::RendererSink;

use crate::chats::Chats;
use crate::clock::Clock;
use crate::daemon::{Anchor, Applied, mark_read, session, subscribe, unsubscribe};
use crate::messages::Messages;

#[global_allocator]
static ALLOC: what_core::alloc::Allocator = what_core::alloc::Allocator;

const FRONTEND: &str = "in.codelif.whatevr";
const WAKE: Token = Token(1);
/// every so often, to roll time labels over at midnight
const TICK: Token = Token(2);

#[derive(Parser)]
#[command(about = "whatevr's native wayland frontend")]
struct Args {
    /// whatevrd's socket, instead of WHATEVR_SOCKET or the runtime dir
    #[arg(long)]
    socket: Option<PathBuf>,
}

/// which list a subscribe was for
#[derive(Clone, Copy)]
enum For {
    Chats,
    Messages,
}

struct Whatgui {
    gpu: Gpu,
    surface: Option<WindowSurface>,
    renderer: Option<Renderer>,
    /// WHATGUI_SCENE: a what-render test scene instead of the app
    test_scene: Option<(String, scenes::Assets)>,
    shell: Shell<Messages, Chats>,
    scene: Scene,
    client: Client,
    wake: Arc<OwnedFd>,
    id: Option<WindowId>,
    focused: bool,
    /// subscribes in flight
    asked: HashMap<u64, For>,
    /// the chat picked, whether or not its window is in yet
    open: Option<Arc<str>>,
    /// the newest message sent as read in the open chat
    marked: Option<Box<[u8]>>,
    clock: Clock,
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

impl Whatgui {
    fn send(&mut self, m: Method) -> Option<u64> {
        match self.client.send(m) {
            Ok(id) => Some(id),
            Err(e) => {
                debug!("not sent: {e}");
                None
            }
        }
    }

    fn ask(&mut self, view: View, limit: u32, what: For) {
        if let Some(id) = self.send(subscribe(view, limit)) {
            self.asked.insert(id, what);
            let dropped = match what {
                For::Chats => self.shell.chats.slide.asked(id),
                For::Messages => self.shell.chat.slide.asked(id),
            };
            if let Some(sub) = dropped {
                self.send(unsubscribe(sub));
            }
        }
    }

    fn tell_session(&mut self) {
        let chat = self.open.clone().unwrap_or_default();
        self.send(session(self.focused, &chat));
    }

    fn open_chat(&mut self, id: &str) {
        if self.open.as_deref() == Some(id) {
            return;
        }
        self.open = Some(id.into());
        self.marked = None;
        self.shell.selected = self.shell.chats.key_of(id);
        self.ask(
            Messages::view(id, Anchor::Edge),
            messages::LIMIT,
            For::Messages,
        );
        self.tell_session();
    }

    /// everything waiting from the daemon; true if anything shown changed
    fn drain(&mut self) -> bool {
        let mut changed = false;
        while let Some(inc) = self.client.poll() {
            match inc {
                Incoming::Hello(_) => {
                    info!("connected to whatevrd");
                    self.asked.clear();
                    let here = self.shell.chats.here();
                    self.ask(Chats::view(here), chats::LIMIT, For::Chats);
                    if let Some(chat) = self.open.clone() {
                        let here = self.shell.chat.here();
                        self.ask(Messages::view(&chat, here), messages::LIMIT, For::Messages);
                    }
                    self.tell_session();
                }
                Incoming::Response(id, r) => {
                    let Some(what) = self.asked.remove(&id) else {
                        continue;
                    };
                    match &r.view().result {
                        Some(Answer::Subscribe(s)) => {
                            let answered = match what {
                                For::Chats => self.shell.chats.slide.answered(id, s.sub),
                                For::Messages => self.shell.chat.slide.answered(id, s.sub),
                            };
                            // a window nobody waits for anymore
                            if !answered {
                                self.send(unsubscribe(s.sub));
                            }
                        }
                        Some(Answer::Error(e)) => {
                            warn!("subscribe refused: {}", e.message);
                            match what {
                                For::Chats => self.shell.chats.slide.refused(id),
                                For::Messages => self.shell.chat.slide.refused(id),
                            };
                        }
                        _ => {}
                    }
                }
                Incoming::Event(e) => match &e.view().event {
                    Some(Event::Update(u)) => changed |= self.update(u),
                    Some(Event::OpenChat(o)) => {
                        let id = o.chat_id.to_string();
                        self.open_chat(&id);
                    }
                    _ => {}
                },
                Incoming::Disconnected => {
                    warn!("lost whatevrd, redialing");
                    self.asked.clear();
                    self.shell.chats.slide.disconnected();
                    self.shell.chat.slide.disconnected();
                }
                Incoming::Closed(e) => error!("{e}"),
            }
        }
        changed
    }

    fn update(&mut self, u: &ViewUpdateView<'_>) -> bool {
        let clock = &self.clock;
        let chats = &mut self.shell.chats;
        match chats.slide.update(u, |up| match &up.item {
            Some(Row::Chat(c)) => Some(rows::chat(c)),
            _ => None,
        }) {
            Applied::No => {}
            Applied::Filling => return false,
            a => {
                if let Applied::Swapped(Some(old)) = a {
                    self.send(unsubscribe(old));
                }
                let chats = &mut self.shell.chats;
                for (gone, to) in std::mem::take(&mut chats.slide.cur.replaced) {
                    if self.open.as_deref() == Some(&gone) {
                        // the chat folded into another
                        self.open = None;
                        self.open_chat(&to);
                    }
                }
                let chats = &mut self.shell.chats;
                chats.rebuild();
                self.shell.selected = self
                    .open
                    .as_deref()
                    .and_then(|id| self.shell.chats.key_of(id));
                if self.shell.chat.name.is_empty()
                    && let Some(r) = self.open.as_deref().and_then(|id| self.shell.chats.row(id))
                {
                    self.shell.chat.name = r.name.clone();
                }
                self.shell.chats_changed();
                return true;
            }
        }

        let msgs = &mut self.shell.chat;
        let before = msgs.newest_message();
        let mut name = None;
        let applied = msgs.slide.update(u, |up| match &up.item {
            Some(Row::Message(m)) => {
                if name.is_none() && !m.chat_name.is_empty() {
                    name = Some(m.chat_name.to_string());
                }
                Some(rows::message(m, clock))
            }
            _ => None,
        });
        match applied {
            Applied::No | Applied::Filling => false,
            Applied::Changed => {
                msgs.rebuild();
                if before.is_some() && msgs.newest_message() != before && msgs.slide.cur.at_end() {
                    self.shell.appended();
                } else {
                    self.shell.messages_changed();
                }
                true
            }
            Applied::Swapped(old) => {
                if let Some(old) = old {
                    self.send(unsubscribe(old));
                }
                let msgs = &mut self.shell.chat;
                let fresh = msgs.chat.as_deref() != self.open.as_deref();
                if fresh {
                    msgs.forget();
                    msgs.chat = self.open.clone();
                    msgs.name = self
                        .open
                        .as_deref()
                        .and_then(|id| self.shell.chats.row(id))
                        .map(|r| r.name.clone())
                        .or(name)
                        .unwrap_or_default();
                }
                let msgs = &mut self.shell.chat;
                msgs.rebuild();
                if fresh {
                    self.shell.opened(Platform::now());
                } else {
                    self.shell.messages_changed();
                }
                true
            }
        }
    }

    /// what the frame asked for: fresh windows, and read up to the newest
    /// message on screen
    fn after_frame(&mut self) {
        if !self.shell.chats.slide.busy()
            && let Some(v) = self.shell.chats.wanted()
        {
            self.ask(v, chats::LIMIT, For::Chats);
        }
        if let Some(chat) = self.shell.chat.chat.clone()
            && !self.shell.chat.slide.busy()
            && let Some(v) = self.shell.chat.wanted(&chat)
        {
            self.ask(v, messages::LIMIT, For::Messages);
        }
        self.mark_read();
    }

    fn mark_read(&mut self) {
        let Some(chat) = self.open.clone() else {
            return;
        };
        if !self.focused || self.shell.chat.chat.as_deref() != Some(&*chat) {
            return;
        }
        let Some(row) = self.shell.chats.row(&chat) else {
            return;
        };
        if row.unread == 0 && !row.marked_unread {
            return;
        }
        let msgs = &self.shell.chat;
        let Some((id, sort)) = self
            .shell
            .chat_rows()
            .iter()
            .rev()
            .find_map(|r| msgs.message(r.0))
        else {
            return;
        };
        if self.marked.as_deref().is_some_and(|m| m >= sort) {
            return;
        }
        let (id, sort) = (id.clone(), sort.into());
        if self.send(mark_read(&chat, &id)).is_some() {
            self.marked = Some(sort);
        }
    }
}

impl App for Whatgui {
    fn resized(&mut self, platform: &mut Platform, id: WindowId, size: WindowSize) {
        self.id = Some(id);
        self.shell.resize(size.logical, size.scale);
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
                        self.test_scene = Some((name, assets));
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

    fn frame(&mut self, platform: &mut Platform, id: WindowId, info: FrameInfo) {
        let (Some(surface), Some(renderer), Some(size)) =
            (&self.surface, &mut self.renderer, platform.window_size(id))
        else {
            return;
        };
        if let Some((name, assets)) = &self.test_scene {
            let scene = scenes::build(name, assets, size.scale).unwrap_or_default();
            let Some(mut frame) = surface.begin(&self.gpu) else {
                return;
            };
            let bg = self.shell.theme.bg.premultiply();
            renderer.draw(&self.gpu, &mut frame, &scene, bg, size.physical);
            surface.present(&self.gpu, frame, &mut Commit { platform, id });
            return;
        }
        self.scene.clear();
        let f = self.shell.frame(
            info.time,
            &mut self.scene,
            &mut RendererSink {
                renderer,
                gpu: &self.gpu,
            },
        );
        if f.chat_jumps + f.side_jumps > 0 {
            warn!(
                "frame {}: {} chat, {} chat list rows jumped",
                info.seq, f.chat_jumps, f.side_jumps
            );
        }
        let Some(mut frame) = surface.begin(&self.gpu) else {
            return;
        };
        renderer.draw(
            &self.gpu,
            &mut frame,
            &self.scene,
            self.shell.theme.bg.premultiply(),
            size.physical,
        );
        surface.present(&self.gpu, frame, &mut Commit { platform, id });
        self.after_frame();
        if self.shell.animating(Platform::now()) {
            platform.request_redraw(id);
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

    fn focus(&mut self, platform: &mut Platform, id: WindowId, on: bool) {
        self.focused = on;
        self.tell_session();
        // a chat shown while unfocused gets read now
        platform.request_redraw(id);
    }

    fn pointer(&mut self, platform: &mut Platform, id: WindowId, ev: &PointerEvent) {
        let mut redraw = self.shell.pointer(ev, Platform::now());
        if let Some(k) = self.shell.take_pick()
            && let Some(chat) = self.shell.chats.id(k).cloned()
        {
            self.open_chat(&chat);
            redraw = true;
        }
        if redraw {
            platform.request_redraw(id);
        }
    }

    fn fd_ready(&mut self, platform: &mut Platform, token: Token) {
        if token == WAKE {
            let mut b = [0u8; 8];
            let _ = rustix::io::read(self.wake.as_fd(), &mut b);
            self.drain();
            // the lists' workers wake through here too, so always draw
            if let Some(id) = self.id {
                platform.request_redraw(id);
            }
        }
    }

    fn timer(&mut self, platform: &mut Platform, token: Token) {
        if token == TICK {
            if self.shell.chats.tick() {
                self.shell.chats_changed();
                if let Some(id) = self.id {
                    platform.request_redraw(id);
                }
            }
            if let Err(e) = platform.set_timer(TICK, std::time::Duration::from_secs(60)) {
                warn!("tick timer: {e}");
            }
        }
    }
}

fn main() -> anyhow::Result<()> {
    let args = Args::parse();
    what_core::log::init("WHATGUI_LOG", LevelFilter::Info);
    let frame_log = std::env::var_os("WHATGUI_FRAME_LOG").map(PathBuf::from);
    let mut platform = Platform::new(PlatformConfig { frame_log }).context("wayland")?;
    let gpu = Gpu::new(platform.display()).context("gpu")?;
    let wake = Arc::new(eventfd(0, EventfdFlags::CLOEXEC | EventfdFlags::NONBLOCK)?);
    platform.add_fd(wake.as_fd(), WAKE)?;
    platform.set_timer(TICK, std::time::Duration::from_secs(60))?;
    let waker = {
        let w = wake.clone();
        move || {
            let _ = rustix::io::write(w.as_fd(), &1u64.to_ne_bytes());
        }
    };
    let socket = args.socket.unwrap_or_else(what_proto::socket_path);
    let client = Client::connect(&socket, FRONTEND, waker.clone());
    let clock = Clock::new();
    let shell = Shell::new(
        TextSystem::new(),
        waker,
        Messages::new(clock.clone()),
        Chats::new(clock.clone()),
    );
    platform.create_window(&WindowConfig {
        title: "whatevr".into(),
        app_id: FRONTEND.into(),
        size: LogicalSize::new(960.0, 640.0),
        min_size: Some(LogicalSize::new(360.0, 480.0)),
    });
    let mut app = Whatgui {
        gpu,
        surface: None,
        renderer: None,
        test_scene: None,
        shell,
        scene: Scene::default(),
        client,
        wake,
        id: None,
        focused: false,
        asked: HashMap::new(),
        open: None,
        marked: None,
        clock,
    };
    platform.run(&mut app)?;
    Ok(())
}
