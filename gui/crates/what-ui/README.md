# what-ui

Element tree, layout, hit testing, focus, animation, virtual list and rich editor.

Builds each frame's element tree and turns it into what-render draw lists.

## Boundaries

- Depends on what-core, what-render and what-text inside the workspace.
- Never imports what-platform, so it runs headless.
- Knows nothing about whatevr.
