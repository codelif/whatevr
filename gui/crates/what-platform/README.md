# what-platform

Wayland client: windows, frame clock, pointer, keyboard, IME, clipboard, activation, portals.

Talks to the compositor directly and turns its events into what-core input events and frame ticks.

## Boundaries

- Depends only on what-core inside the workspace.
- Never imported by what-render or what-ui; only the app wires it to them.
- Knows nothing about whatevr.
