# what-render

wgpu device and surface, instanced pipelines, atlases, headless PNG output.

Draws frames of instanced primitives into a window surface or an offscreen image.

## Boundaries

- Depends only on what-core inside the workspace.
- Sees a raw window handle, never Wayland types.
- Owns all atlas textures; what-text hands it coverage bitmaps.
- Knows nothing about whatevr.
