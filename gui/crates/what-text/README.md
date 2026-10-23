# what-text

Text layout and glyph rasterization: Parley wrapper, ellipsis, max lines, raster cache, COLRv1 painter.

Shapes and lays out text and rasterizes glyphs into coverage or colour bitmaps.

## Boundaries

- Depends only on what-core inside the workspace.
- Knows nothing about the GPU: returns glyph ids, positions and bitmaps.
- Knows nothing about whatevr.
