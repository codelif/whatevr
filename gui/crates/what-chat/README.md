# what-chat

whatevr's chat look and app shell: the theme, the bubble and chat list row builders, the plain data rows are drawn from, and the two panes around them.

The shell is generic over where rows come from, so scroll-lab's scripted test runs the same panes whatgui shows.

## Boundaries

- Depends on what-assets, what-core, what-render, what-text and what-ui inside the workspace.
- Never imports what-platform or what-proto: no window, no gpu device, no socket.
- One of the crates that know whatevr exists (the others are what-assets, what-proto and whatgui).
