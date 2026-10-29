//! Wayland client: windows, frame clock, pointer, keyboard, IME, clipboard, activation, portals.

mod event_loop;
mod globals;
mod keyboard;
mod output;
mod pointer;
mod presentation;
mod seat;
mod window;

use std::io;
use std::os::fd::BorrowedFd;
use std::path::PathBuf;
use std::ptr::NonNull;
use std::time::Duration;

use log::trace;
use raw_window_handle::{
    DisplayHandle, HandleError, HasDisplayHandle, RawDisplayHandle, RawWindowHandle,
    WaylandWindowHandle,
};
use wayland_backend::client::{Backend, WaylandError};
use wayland_client::globals::{BindError, GlobalError, registry_queue_init};
use wayland_client::{ConnectError, Connection, DispatchError, EventQueue, QueueHandle};
use what_core::{
    KeyEvent, LogicalSize, Modifiers, PhysicalSize, PointerEvent, Scale, Timestamp, Token, WindowId,
};

use event_loop::{EventLoop, Ready};
use globals::Globals;
use output::Output;
use presentation::FrameStats;
use seat::Seat;
use window::Window;

pub use presentation::{FrameInfo, PresentInfo};
pub use window::WindowConfig;

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("no wayland compositor: {0}")]
    Connect(#[from] ConnectError),
    #[error("wayland registry: {0}")]
    Globals(#[from] GlobalError),
    #[error("wayland bind: {0}")]
    Bind(#[from] BindError),
    #[error("compositor lacks {0}")]
    Missing(&'static str),
    #[error("wayland dispatch: {0}")]
    Dispatch(#[from] DispatchError),
    #[error("wayland connection: {0}")]
    Wayland(#[from] WaylandError),
    #[error(transparent)]
    Io(#[from] io::Error),
}

#[derive(Clone, Debug, Default)]
pub struct PlatformConfig {
    /// per-frame TSV timeline, written as presentation feedback arrives
    pub frame_log: Option<PathBuf>,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct WindowSize {
    pub logical: LogicalSize,
    /// the content size in pixels; lay out and draw into this, from the top left
    pub physical: PhysicalSize,
    pub scale: Scale,
    /// configure the surface to this. bigger than `physical` only during an
    /// interactive resize; the viewport crops it. hyprland samples ~1 px past
    /// the crop, so the area past the content must repeat its edge
    pub buffer: PhysicalSize,
}

/// everything the platform tells the app. every method gets the platform back
/// so it can request redraws, present, open windows or quit.
pub trait App {
    /// the window's logical size or scale changed; reconfigure the surface
    fn resized(&mut self, platform: &mut Platform, id: WindowId, size: WindowSize);
    /// draw now. call [`Platform::pre_present`] right before the commit
    fn frame(&mut self, platform: &mut Platform, id: WindowId, info: FrameInfo);
    fn close_requested(&mut self, platform: &mut Platform, id: WindowId);

    fn presented(&mut self, _: &mut Platform, _: WindowId, _: PresentInfo) {}
    fn key(&mut self, _: &mut Platform, _: WindowId, _: &KeyEvent) {}
    fn modifiers(&mut self, _: &mut Platform, _: WindowId, _: Modifiers) {}
    fn focus(&mut self, _: &mut Platform, _: WindowId, _: bool) {}
    /// one wl_pointer frame is several calls in a row, scroll last
    fn pointer(&mut self, _: &mut Platform, _: WindowId, _: &PointerEvent) {}
    fn fd_ready(&mut self, _: &mut Platform, _: Token) {}
    fn timer(&mut self, _: &mut Platform, _: Token) {}
}

pub(crate) enum Event {
    Resized(WindowId),
    CloseRequested(WindowId),
    Focus(WindowId, bool),
    Key(WindowId, KeyEvent),
    Modifiers(WindowId, Modifiers),
    Pointer(WindowId, PointerEvent),
    Presented(WindowId, PresentInfo),
}

pub(crate) struct State {
    qh: QueueHandle<State>,
    globals: Globals,
    windows: Vec<Window>,
    outputs: Vec<Output>,
    seat: Seat,
    presentation: FrameStats,
    /// filled by dispatch, drained to the app afterwards so handlers never need the app
    events: Vec<Event>,
    next_window: u32,
}

/// the wayland display, for wgpu's instance. cheap to clone.
#[derive(Clone, Debug)]
pub struct Display(Backend);

impl HasDisplayHandle for Display {
    fn display_handle(&self) -> Result<DisplayHandle<'_>, HandleError> {
        self.0.display_handle()
    }
}

pub struct Platform {
    conn: Connection,
    queue: EventQueue<State>,
    state: State,
    lp: EventLoop,
    running: bool,
    wants_write: bool,
    ready: Vec<Ready>,
}

impl Platform {
    pub fn new(config: PlatformConfig) -> Result<Self, Error> {
        let conn = Connection::connect_to_env()?;
        let (list, mut queue) = registry_queue_init::<State>(&conn)?;
        let qh = queue.handle();
        let globals = Globals::bind(&list, &qh)?;
        let mut state = State {
            qh,
            globals,
            windows: Vec::new(),
            outputs: Vec::new(),
            seat: Seat::new()?,
            presentation: FrameStats::new(config.frame_log.as_deref()),
            events: Vec::new(),
            next_window: 1,
        };
        list.contents().with_list(|globals| {
            for g in globals.iter().filter(|g| g.interface == "wl_output") {
                state.add_output(list.registry(), g.name, g.version);
            }
        });

        let lp = EventLoop::new()?;
        lp.add_internal(conn.backend().poll_fd(), event_loop::WAYLAND)?;
        lp.add_internal(state.seat.repeat.fd(), event_loop::REPEAT)?;

        // output scales and seat capabilities, before any window exists
        queue.roundtrip(&mut state)?;

        Ok(Self {
            conn,
            queue,
            state,
            lp,
            running: false,
            wants_write: false,
            ready: Vec::new(),
        })
    }

    pub fn display(&self) -> Display {
        Display(self.conn.backend())
    }

    pub fn create_window(&mut self, config: &WindowConfig) -> WindowId {
        Window::create(&mut self.state, config)
    }

    /// drop anything rendering to the window (the wgpu surface) before this
    pub fn close_window(&mut self, id: WindowId) {
        let Some(i) = self.state.windows.iter().position(|w| w.id == id) else {
            return;
        };
        let w = self.state.windows.swap_remove(i);
        if let Some(kb) = self.state.seat.keyboard.as_mut() {
            kb.unfocus(id);
        }
        if let Some(p) = self.state.seat.pointer.as_mut() {
            p.unfocus(id);
        }
        self.state.seat.repeat.stop();
        w.destroy();
    }

    /// handles for a wgpu surface. valid until [`Platform::close_window`].
    pub fn raw_handles(&self, id: WindowId) -> Option<(RawDisplayHandle, RawWindowHandle)> {
        let w = self.state.windows.iter().find(|w| w.id == id)?;
        let display = self.conn.backend().display_handle().ok()?.as_raw();
        let surface = NonNull::new(window::surface_ptr(w))?;
        Some((
            display,
            RawWindowHandle::Wayland(WaylandWindowHandle::new(surface)),
        ))
    }

    pub fn window_size(&self, id: WindowId) -> Option<WindowSize> {
        let w = self.state.windows.iter().find(|w| w.id == id)?;
        Some(WindowSize {
            logical: w.size,
            physical: w.content(),
            scale: w.scale,
            buffer: w.buffer,
        })
    }

    /// ask for a frame; it comes when the compositor wants one, never more than once per refresh
    pub fn request_redraw(&mut self, id: WindowId) {
        if let Some(w) = self.state.window_mut(id) {
            w.redraw = true;
        }
    }

    /// call between submitting the frame and the commit (wgpu's present). acks
    /// the configure, sets the buffer to surface mapping, and asks for the next
    /// frame callback and presentation feedback, all in the same commit.
    pub fn pre_present(&mut self, id: WindowId) {
        let state = &mut self.state;
        let Some(i) = state.windows.iter().position(|w| w.id == id) else {
            return;
        };
        let w = &mut state.windows[i];
        if let Some(serial) = w.pending_ack.take() {
            w.xdg_surface.ack_configure(serial);
        }
        let dest = (w.size.width.round() as i32, w.size.height.round() as i32);
        let buffer_scale = match &w.viewport {
            Some(vp) => {
                if w.applied_dest != dest {
                    vp.set_destination(dest.0, dest.1);
                    w.applied_dest = dest;
                }
                let content = w.content();
                let source = (w.buffer != content).then_some(content);
                if w.applied_source != source {
                    match source {
                        Some(c) => vp.set_source(0.0, 0.0, c.width as f64, c.height as f64),
                        None => vp.set_source(-1.0, -1.0, -1.0, -1.0),
                    }
                    w.applied_source = source;
                }
                1
            }
            None => (w.scale.as_120ths() / 120) as i32,
        };
        if w.applied_buffer_scale != buffer_scale {
            w.surface.set_buffer_scale(buffer_scale);
            w.applied_buffer_scale = buffer_scale;
        }
        if !w.callback_pending {
            w.surface.frame(&state.qh, id);
            w.callback_pending = true;
        }
        if let Some(seq) = w.current_seq.take() {
            let surface = w.surface.clone();
            state.presentation.end_frame(seq);
            state.request_feedback(id, &surface, seq);
        }
    }

    pub fn quit(&mut self) {
        self.running = false;
    }

    pub fn add_fd(&mut self, fd: BorrowedFd<'_>, token: Token) -> io::Result<()> {
        self.lp.add_fd(fd, token)
    }

    pub fn remove_fd(&mut self, fd: BorrowedFd<'_>) -> io::Result<()> {
        self.lp.remove_fd(fd)
    }

    /// one-shot; setting it again rearms
    pub fn set_timer(&mut self, token: Token, after: Duration) -> io::Result<()> {
        self.lp.set_timer(token, after)
    }

    pub fn cancel_timer(&mut self, token: Token) -> io::Result<()> {
        self.lp.cancel_timer(token)
    }

    pub fn now() -> Timestamp {
        presentation::now()
    }

    pub fn run(&mut self, app: &mut impl App) -> Result<(), Error> {
        self.running = true;
        while self.running {
            self.queue.dispatch_pending(&mut self.state)?;
            self.deliver(app);
            if !self.running {
                break;
            }
            self.frames(app);
            if !self.running {
                break;
            }
            self.flush()?;

            let Some(guard) = self.queue.prepare_read() else {
                continue;
            };
            let busy =
                !self.state.events.is_empty() || self.state.windows.iter().any(Window::frame_due);
            let mut ready = std::mem::take(&mut self.ready);
            self.lp.wait(busy.then_some(Duration::ZERO), &mut ready)?;

            if ready.iter().any(|r| matches!(r, Ready::Wayland)) {
                match guard.read() {
                    Ok(_) => {}
                    Err(WaylandError::Io(e)) if e.kind() == io::ErrorKind::WouldBlock => {}
                    Err(e) => return Err(e.into()),
                }
            } else {
                drop(guard);
            }

            for r in ready.drain(..) {
                match r {
                    Ready::Wayland => {}
                    Ready::Repeat => self.state.repeat_fire(),
                    Ready::Fd(t) => app.fd_ready(self, t),
                    Ready::Timer(t) => app.timer(self, t),
                }
            }
            self.ready = ready;
        }
        self.flush()
    }

    fn flush(&mut self) -> Result<(), Error> {
        let blocked = match self.conn.flush() {
            Ok(()) => false,
            Err(WaylandError::Io(e)) if e.kind() == io::ErrorKind::WouldBlock => true,
            Err(e) => return Err(e.into()),
        };
        if blocked != self.wants_write {
            self.lp
                .set_wayland_writable(self.conn.backend().poll_fd(), blocked)?;
            self.wants_write = blocked;
        }
        Ok(())
    }

    fn deliver(&mut self, app: &mut impl App) {
        let mut events = std::mem::take(&mut self.state.events);
        for ev in events.drain(..) {
            match ev {
                Event::Resized(id) => {
                    if let Some(size) = self.window_size(id) {
                        app.resized(self, id, size);
                    }
                }
                Event::CloseRequested(id) => app.close_requested(self, id),
                Event::Focus(id, on) => app.focus(self, id, on),
                Event::Key(id, ev) => app.key(self, id, &ev),
                Event::Modifiers(id, m) => app.modifiers(self, id, m),
                Event::Pointer(id, ev) => app.pointer(self, id, &ev),
                Event::Presented(id, info) => app.presented(self, id, info),
            }
            if !self.running {
                break;
            }
        }
        // keep the allocation; anything the app caused is already queued behind
        events.append(&mut self.state.events);
        self.state.events = events;
    }

    fn frames(&mut self, app: &mut impl App) {
        let due: Vec<WindowId> = self
            .state
            .windows
            .iter()
            .filter(|w| w.frame_due())
            .map(|w| w.id)
            .collect();
        for id in due {
            let Some(w) = self.state.window_mut(id) else {
                continue;
            };
            w.redraw = false;
            w.force = false;
            let callback_ms = w.last_callback_ms.take();
            let info = self.state.presentation.begin_frame(id, callback_ms);
            if let Some(w) = self.state.window_mut(id) {
                w.current_seq = Some(info.seq);
            }
            app.frame(self, id, info);
            if let Some(w) = self.state.window_mut(id)
                && w.current_seq.take().is_some()
            {
                trace!("{id:?} frame {} not presented", info.seq);
                self.state.presentation.skipped(id, info.seq);
            }
        }
    }
}
