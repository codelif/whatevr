use wayland_client::protocol::wl_pointer;
use wayland_client::{Connection, Dispatch, Proxy, QueueHandle};
use wayland_protocols::wp::cursor_shape::v1::client::wp_cursor_shape_device_v1::{
    self, WpCursorShapeDeviceV1,
};

use crate::State;

pub(crate) struct Pointer {
    pub wl: wl_pointer::WlPointer,
    pub shape: Option<WpCursorShapeDeviceV1>,
}

impl Pointer {
    pub(crate) fn release(self) {
        if let Some(s) = self.shape {
            s.destroy();
        }
        if self.wl.version() >= 3 {
            self.wl.release();
        }
    }
}

impl Dispatch<wl_pointer::WlPointer, ()> for State {
    fn event(
        state: &mut Self,
        _: &wl_pointer::WlPointer,
        event: wl_pointer::Event,
        _: &(),
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        // step 1 only sets the cursor; everything else waits for the scroll lab
        if let wl_pointer::Event::Enter { serial, .. } = event
            && let Some(shape) = state.seat.pointer.as_ref().and_then(|p| p.shape.as_ref())
        {
            shape.set_shape(serial, wp_cursor_shape_device_v1::Shape::Default);
        }
    }
}

wayland_client::delegate_noop!(State: ignore WpCursorShapeDeviceV1);
