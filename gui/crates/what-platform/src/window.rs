use log::{debug, trace};
use wayland_client::protocol::{wl_callback, wl_output::WlOutput, wl_surface};
use wayland_client::{Connection, Dispatch, Proxy, QueueHandle, WEnum};
use wayland_protocols::wp::fractional_scale::v1::client::wp_fractional_scale_v1;
use wayland_protocols::wp::viewporter::client::wp_viewport::WpViewport;
use wayland_protocols::xdg::decoration::zv1::client::zxdg_toplevel_decoration_v1;
use wayland_protocols::xdg::shell::client::{xdg_surface, xdg_toplevel, xdg_wm_base};
use what_core::{LogicalSize, PhysicalSize, Scale, WindowId};

use crate::output::Output;

const BUCKET: u32 = 128;
use crate::{Event, State};

#[derive(Clone, Debug)]
pub struct WindowConfig {
    pub title: String,
    pub app_id: String,
    /// used until the compositor picks a size
    pub size: LogicalSize,
    pub min_size: Option<LogicalSize>,
}

pub(crate) struct Window {
    pub id: WindowId,
    pub surface: wl_surface::WlSurface,
    pub xdg_surface: xdg_surface::XdgSurface,
    pub toplevel: xdg_toplevel::XdgToplevel,
    pub decoration: Option<zxdg_toplevel_decoration_v1::ZxdgToplevelDecorationV1>,
    pub viewport: Option<WpViewport>,
    pub fractional: Option<wp_fractional_scale_v1::WpFractionalScaleV1>,

    pub size: LogicalSize,
    pending_size: Option<(i32, i32)>,
    pending_resizing: bool,
    /// the compositor says an interactive resize is in progress
    resizing: bool,
    /// >= the content size; bigger only while resizing, cropped by the viewport
    pub buffer: PhysicalSize,
    pub applied_source: Option<PhysicalSize>,
    pub pending_ack: Option<u32>,
    pub configured: bool,

    scale_fractional: Option<u32>,
    scale_preferred: Option<i32>,
    entered: Vec<WlOutput>,
    pub scale: Scale,
    pub applied_buffer_scale: i32,
    pub applied_dest: (i32, i32),

    /// a wl_surface.frame is in flight; the next frame waits for it
    pub callback_pending: bool,
    pub last_callback_ms: Option<u32>,
    /// the app wants a frame when the compositor is ready
    pub redraw: bool,
    /// a configure needs a buffer now, callback or not
    pub force: bool,
    pub current_seq: Option<u64>,
}

impl Window {
    pub(crate) fn create(state: &mut State, config: &WindowConfig) -> WindowId {
        let id = WindowId(state.next_window);
        state.next_window += 1;
        let qh = &state.qh;
        let g = &state.globals;

        let surface = g.compositor.create_surface(qh, id);
        let xdg_surface = g.wm_base.get_xdg_surface(&surface, qh, id);
        let toplevel = xdg_surface.get_toplevel(qh, id);
        toplevel.set_title(config.title.clone());
        toplevel.set_app_id(config.app_id.clone());
        if let Some(min) = config.min_size {
            toplevel.set_min_size(min.width.round() as i32, min.height.round() as i32);
        }
        let decoration = g.decoration.as_ref().map(|m| {
            let d = m.get_toplevel_decoration(&toplevel, qh, id);
            d.set_mode(zxdg_toplevel_decoration_v1::Mode::ServerSide);
            d
        });
        let viewport = g
            .viewporter
            .as_ref()
            .map(|v| v.get_viewport(&surface, qh, ()));
        // fractional scale is useless without a viewport to map the bigger buffer back
        let fractional = match (&g.fractional, &viewport) {
            (Some(m), Some(_)) => Some(m.get_fractional_scale(&surface, qh, id)),
            _ => None,
        };
        // first commit carries no buffer; it asks for the initial configure
        surface.commit();

        state.windows.push(Window {
            id,
            surface,
            xdg_surface,
            toplevel,
            decoration,
            viewport,
            fractional,
            size: config.size,
            pending_size: None,
            pending_resizing: false,
            resizing: false,
            buffer: PhysicalSize::new(0, 0),
            applied_source: None,
            pending_ack: None,
            configured: false,
            scale_fractional: None,
            scale_preferred: None,
            entered: Vec::new(),
            scale: Scale::ONE,
            applied_buffer_scale: 1,
            applied_dest: (0, 0),
            callback_pending: false,
            last_callback_ms: None,
            redraw: false,
            force: false,
            current_seq: None,
        });
        state.update_scale(id);
        id
    }

    pub(crate) fn destroy(self) {
        if let Some(f) = self.fractional {
            f.destroy();
        }
        if let Some(v) = self.viewport {
            v.destroy();
        }
        if let Some(d) = self.decoration {
            d.destroy();
        }
        self.toplevel.destroy();
        self.xdg_surface.destroy();
        self.surface.destroy();
    }

    pub(crate) fn content(&self) -> PhysicalSize {
        self.scale.to_physical(self.size)
    }

    /// recomputes the buffer size, true if it changed
    fn update_buffer(&mut self) -> bool {
        let content = self.content();
        let buffer = if self.resizing && self.viewport.is_some() {
            // every new swapchain size costs ~13 ms on its first submit, so a drag
            // reuses buffers until it crosses the next 128 px line outward
            let grow = |need: u32, have: u32| {
                if need <= have {
                    have
                } else {
                    need.div_ceil(BUCKET) * BUCKET
                }
            };
            PhysicalSize::new(
                grow(content.width, self.buffer.width),
                grow(content.height, self.buffer.height),
            )
        } else {
            content
        };
        let changed = buffer != self.buffer;
        self.buffer = buffer;
        changed
    }

    pub(crate) fn frame_due(&self) -> bool {
        self.configured && (self.force || (self.redraw && !self.callback_pending))
    }

    /// fractional-scale-v1, else wl_surface.preferred_buffer_scale, else the
    /// biggest wl_output.scale we're on, else the only output's, else 1
    fn effective_scale(&self, outputs: &[Output]) -> Scale {
        let s = if let Some(f) = self.scale_fractional {
            Scale::from_120ths(f)
        } else if let Some(p) = self.scale_preferred {
            Scale::from_integer(p.max(1) as u32)
        } else {
            let max = self
                .entered
                .iter()
                .filter_map(|o| outputs.iter().find(|out| &out.wl == o))
                .map(|o| o.scale)
                .max()
                // compositors only say where we are after the first buffer; with one
                // output there's nowhere else to be
                .or(match outputs {
                    [only] => Some(only.scale),
                    _ => None,
                })
                .unwrap_or(1);
            Scale::from_integer(max.max(1) as u32)
        };
        if self.viewport.is_some() || s.is_integer() {
            s
        } else {
            // without a viewport only integer buffer scales exist; round up so text stays sharp
            Scale::from_integer(s.as_120ths().div_ceil(120))
        }
    }
}

impl State {
    pub(crate) fn window_mut(&mut self, id: WindowId) -> Option<&mut Window> {
        self.windows.iter_mut().find(|w| w.id == id)
    }

    pub(crate) fn update_scale(&mut self, id: WindowId) {
        let Some(i) = self.windows.iter().position(|w| w.id == id) else {
            return;
        };
        let scale = self.windows[i].effective_scale(&self.outputs);
        let w = &mut self.windows[i];
        if scale != w.scale {
            debug!("{id:?} scale {} -> {}", w.scale.as_f32(), scale.as_f32());
            w.scale = scale;
            w.update_buffer();
            w.redraw = true;
            if w.configured {
                self.events.push(Event::Resized(id));
            }
        }
    }

    pub(crate) fn update_all_scales(&mut self) {
        let ids: Vec<WindowId> = self.windows.iter().map(|w| w.id).collect();
        for id in ids {
            self.update_scale(id);
        }
    }
}

impl Dispatch<xdg_wm_base::XdgWmBase, ()> for State {
    fn event(
        _: &mut Self,
        base: &xdg_wm_base::XdgWmBase,
        event: xdg_wm_base::Event,
        _: &(),
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        if let xdg_wm_base::Event::Ping { serial } = event {
            base.pong(serial);
        }
    }
}

impl Dispatch<wl_surface::WlSurface, WindowId> for State {
    fn event(
        state: &mut Self,
        _: &wl_surface::WlSurface,
        event: wl_surface::Event,
        id: &WindowId,
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        let Some(w) = state.window_mut(*id) else {
            return;
        };
        match event {
            wl_surface::Event::Enter { output } => w.entered.push(output),
            wl_surface::Event::Leave { output } => w.entered.retain(|o| o != &output),
            wl_surface::Event::PreferredBufferScale { factor } => w.scale_preferred = Some(factor),
            _ => return,
        }
        state.update_scale(*id);
    }
}

impl Dispatch<wl_callback::WlCallback, WindowId> for State {
    fn event(
        state: &mut Self,
        _: &wl_callback::WlCallback,
        event: wl_callback::Event,
        id: &WindowId,
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        let wl_callback::Event::Done { callback_data } = event else {
            return;
        };
        let Some(w) = state.window_mut(*id) else {
            return;
        };
        w.callback_pending = false;
        w.last_callback_ms = Some(callback_data);
        trace!("{id:?} frame callback {callback_data}");
        if !w.redraw && !w.force {
            state.presentation.burst_end(*id);
        }
    }
}

impl Dispatch<xdg_surface::XdgSurface, WindowId> for State {
    fn event(
        state: &mut Self,
        _: &xdg_surface::XdgSurface,
        event: xdg_surface::Event,
        id: &WindowId,
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        let xdg_surface::Event::Configure { serial } = event else {
            return;
        };
        let Some(w) = state.window_mut(*id) else {
            return;
        };
        let mut size = w.size;
        if let Some((width, height)) = w.pending_size.take() {
            // zero means "you pick", so keep what we have
            if width > 0 {
                size.width = width as f32;
            }
            if height > 0 {
                size.height = height as f32;
            }
        }
        let first = !w.configured;
        w.configured = true;
        w.resizing = w.pending_resizing;
        w.pending_ack = Some(serial);
        // every configure gets a buffer in the commit that acks it
        w.force = true;
        let resized = first || size != w.size;
        w.size = size;
        let rebuffered = w.update_buffer();
        if resized || rebuffered {
            debug!(
                "{id:?} configure {}x{} buffer {}x{} serial {serial}",
                size.width, size.height, w.buffer.width, w.buffer.height
            );
            state.events.push(Event::Resized(*id));
        }
    }
}

impl Dispatch<xdg_toplevel::XdgToplevel, WindowId> for State {
    fn event(
        state: &mut Self,
        _: &xdg_toplevel::XdgToplevel,
        event: xdg_toplevel::Event,
        id: &WindowId,
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        match event {
            xdg_toplevel::Event::Configure {
                width,
                height,
                states,
            } => {
                let states: Vec<u32> = states
                    .chunks_exact(4)
                    .map(|c| u32::from_ne_bytes([c[0], c[1], c[2], c[3]]))
                    .collect();
                let resizing = states.contains(&(xdg_toplevel::State::Resizing as u32));
                let states: Vec<String> = states
                    .into_iter()
                    .map(|s| match xdg_toplevel::State::try_from(s) {
                        Ok(s) => format!("{s:?}"),
                        Err(_) => format!("unknown({s})"),
                    })
                    .collect();
                trace!(
                    "{id:?} toplevel configure {width}x{height} [{}]",
                    states.join(" ")
                );
                if let Some(w) = state.window_mut(*id) {
                    w.pending_size = Some((width, height));
                    w.pending_resizing = resizing;
                }
            }
            xdg_toplevel::Event::Close => state.events.push(Event::CloseRequested(*id)),
            xdg_toplevel::Event::ConfigureBounds { width, height } => {
                debug!("{id:?} bounds {width}x{height}");
            }
            xdg_toplevel::Event::WmCapabilities { capabilities } => {
                debug!("{id:?} wm capabilities {} bytes", capabilities.len());
            }
            _ => {}
        }
    }
}

impl Dispatch<zxdg_toplevel_decoration_v1::ZxdgToplevelDecorationV1, WindowId> for State {
    fn event(
        _: &mut Self,
        _: &zxdg_toplevel_decoration_v1::ZxdgToplevelDecorationV1,
        event: zxdg_toplevel_decoration_v1::Event,
        id: &WindowId,
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        if let zxdg_toplevel_decoration_v1::Event::Configure { mode } = event {
            let mode = match mode {
                WEnum::Value(m) => format!("{m:?}"),
                WEnum::Unknown(v) => format!("unknown({v})"),
            };
            debug!("{id:?} decoration mode {mode}");
        }
    }
}

impl Dispatch<wp_fractional_scale_v1::WpFractionalScaleV1, WindowId> for State {
    fn event(
        state: &mut Self,
        _: &wp_fractional_scale_v1::WpFractionalScaleV1,
        event: wp_fractional_scale_v1::Event,
        id: &WindowId,
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        if let wp_fractional_scale_v1::Event::PreferredScale { scale } = event {
            if let Some(w) = state.window_mut(*id) {
                w.scale_fractional = Some(scale);
            }
            state.update_scale(*id);
        }
    }
}

wayland_client::delegate_noop!(State: ignore WpViewport);

/// keeps the surface pointer reachable for raw-window-handle without exposing the proxy
pub(crate) fn surface_ptr(w: &Window) -> *mut std::ffi::c_void {
    w.surface.id().as_ptr().cast()
}
