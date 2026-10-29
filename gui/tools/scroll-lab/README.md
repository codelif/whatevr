# scroll-lab

Scrolling stress harness: a 105k-message fake group chat next to a 5,000-chat sidebar.

Exercises both virtual lists (bottom and top gravity) with multilingual bubbles, quotes, link cards, photos and reactions under touchpad, wheel, kinetic scrolling and thumb drags, with history prepends and incoming messages. Every frame is checked for rows that jump.

- `SCROLL_LAB_INCOMING_MS`: how often a fake message arrives, default 3000, 0 for never.
- `SCROLL_LAB_LOG`: a tsv line per frame (cpu, gpu, late vblanks, movement, sync builds, jumps).
- `SCROLL_LAB_FRAME_LOG`: the platform's presentation timeline.
- `tests/scripted.rs`: the headless exit test on lavapipe; `SCRIPTED_TRACE=1` prints every frame.

## Boundaries

- Never talks to whatevrd.
