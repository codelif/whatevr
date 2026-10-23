use log::{debug, info};
use wayland_client::QueueHandle;
use wayland_client::globals::GlobalList;
use wayland_client::protocol::{wl_compositor::WlCompositor, wl_seat::WlSeat};
use wayland_protocols::wp::cursor_shape::v1::client::wp_cursor_shape_manager_v1::WpCursorShapeManagerV1;
use wayland_protocols::wp::fractional_scale::v1::client::wp_fractional_scale_manager_v1::WpFractionalScaleManagerV1;
use wayland_protocols::wp::presentation_time::client::wp_presentation::WpPresentation;
use wayland_protocols::wp::viewporter::client::wp_viewporter::WpViewporter;
use wayland_protocols::xdg::decoration::zv1::client::zxdg_decoration_manager_v1::ZxdgDecorationManagerV1;
use wayland_protocols::xdg::shell::client::xdg_wm_base::XdgWmBase;

use crate::{Error, State};

pub(crate) struct Globals {
    pub compositor: WlCompositor,
    pub wm_base: XdgWmBase,
    pub viewporter: Option<WpViewporter>,
    pub fractional: Option<WpFractionalScaleManagerV1>,
    pub decoration: Option<ZxdgDecorationManagerV1>,
    pub cursor_shape: Option<WpCursorShapeManagerV1>,
    pub presentation: Option<WpPresentation>,
    pub seat: Option<WlSeat>,
}

impl Globals {
    pub(crate) fn bind(list: &GlobalList, qh: &QueueHandle<State>) -> Result<Self, Error> {
        // v6 for preferred_buffer_scale, v4 is enough for damage_buffer
        let compositor = list
            .bind(qh, 4..=6, ())
            .map_err(|_| Error::Missing("wl_compositor v4"))?;
        let wm_base = list
            .bind(qh, 1..=6, ())
            .map_err(|_| Error::Missing("xdg_wm_base"))?;
        let g = Self {
            compositor,
            wm_base,
            viewporter: list.bind(qh, 1..=1, ()).ok(),
            fractional: list.bind(qh, 1..=1, ()).ok(),
            decoration: list.bind(qh, 1..=1, ()).ok(),
            cursor_shape: list.bind(qh, 1..=1, ()).ok(),
            presentation: list.bind(qh, 1..=2, ()).ok(),
            seat: list.bind(qh, 1..=10, ()).ok(),
        };
        let missing: Vec<&str> = [
            ("wp_viewporter", g.viewporter.is_some()),
            ("wp_fractional_scale_manager_v1", g.fractional.is_some()),
            ("zxdg_decoration_manager_v1", g.decoration.is_some()),
            ("wp_cursor_shape_manager_v1", g.cursor_shape.is_some()),
            ("wp_presentation", g.presentation.is_some()),
            ("wl_seat", g.seat.is_some()),
        ]
        .into_iter()
        .filter(|(_, ok)| !ok)
        .map(|(name, _)| name)
        .collect();
        if missing.is_empty() {
            debug!("all optional globals present");
        } else {
            info!("compositor lacks: {}", missing.join(", "));
        }
        Ok(g)
    }
}

wayland_client::delegate_noop!(State: ignore WlCompositor);
wayland_client::delegate_noop!(State: ignore WpViewporter);
wayland_client::delegate_noop!(State: ignore WpFractionalScaleManagerV1);
wayland_client::delegate_noop!(State: ignore ZxdgDecorationManagerV1);
wayland_client::delegate_noop!(State: ignore WpCursorShapeManagerV1);
