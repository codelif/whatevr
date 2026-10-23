use log::debug;
use wayland_client::globals::GlobalListContents;
use wayland_client::protocol::{wl_output, wl_registry};
use wayland_client::{Connection, Dispatch, Proxy, QueueHandle};

use crate::State;

pub(crate) struct Output {
    pub name: u32,
    pub wl: wl_output::WlOutput,
    pub scale: i32,
    pending_scale: i32,
}

impl State {
    pub(crate) fn add_output(
        &mut self,
        registry: &wl_registry::WlRegistry,
        name: u32,
        version: u32,
    ) {
        // scale arrived in v2, name in v4
        if version < 2 {
            return;
        }
        let wl = registry.bind(name, version.min(4), &self.qh, name);
        self.outputs.push(Output {
            name,
            wl,
            scale: 1,
            pending_scale: 1,
        });
    }
}

impl Dispatch<wl_registry::WlRegistry, GlobalListContents> for State {
    fn event(
        state: &mut Self,
        registry: &wl_registry::WlRegistry,
        event: wl_registry::Event,
        _: &GlobalListContents,
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        match event {
            wl_registry::Event::Global {
                name,
                interface,
                version,
            } if interface == "wl_output" => {
                state.add_output(registry, name, version);
            }
            wl_registry::Event::GlobalRemove { name } => {
                if let Some(i) = state.outputs.iter().position(|o| o.name == name) {
                    let out = state.outputs.swap_remove(i);
                    if out.wl.version() >= 3 {
                        out.wl.release();
                    }
                    state.update_all_scales();
                }
            }
            _ => {}
        }
    }
}

impl Dispatch<wl_output::WlOutput, u32> for State {
    fn event(
        state: &mut Self,
        _: &wl_output::WlOutput,
        event: wl_output::Event,
        name: &u32,
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        let Some(out) = state.outputs.iter_mut().find(|o| o.name == *name) else {
            return;
        };
        match event {
            wl_output::Event::Scale { factor } => out.pending_scale = factor,
            wl_output::Event::Name { name: n } => debug!("output {name} is {n}"),
            wl_output::Event::Done if out.pending_scale != out.scale => {
                debug!("output {name} scale {}", out.pending_scale);
                out.scale = out.pending_scale;
                state.update_all_scales();
            }
            _ => {}
        }
    }
}
