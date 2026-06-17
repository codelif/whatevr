#version 440

// Tiles a motif texture and recolours it using only its alpha as a stencil. The
// motif (a seamless, single-colour-on-transparent SVG) is sampled with a Repeat
// wrap mode, so tiling happens here in the shader at an arbitrary on-screen size
// (`tileSize`) that is independent of the texture resolution -- the SVG can be
// rasterised crisply once and scaled freely.
//
// `origin` is the item's top-left position in a stable coordinate space (the
// conversation view's container). Folding it into the sample coordinate locks
// the tile grid to that space, so banners or reply previews that move/resize the
// message view slide the view *over* a fixed pattern instead of dragging it.

layout(location = 0) in vec2 qt_TexCoord0;
layout(location = 0) out vec4 fragColor;

layout(binding = 1) uniform sampler2D source;

layout(std140, binding = 0) uniform buf {
    mat4 qt_Matrix;
    float qt_Opacity;
    vec4 tint;        // rgb = pattern colour
    vec2 resolution;  // item size in px
    vec2 origin;      // item top-left in the pinned coordinate space, in px
    vec2 tileSize;    // on-screen size of one motif tile (w, h), in px
    float intensity;  // 0..1 overall motif opacity (kept low for subtlety)
};

void main() {
    vec2 px = qt_TexCoord0 * resolution + origin;
    vec2 uv = px / max(tileSize, vec2(1.0));   // Repeat wrap tiles this seamlessly
    float coverage = texture(source, uv).a;
    float a = coverage * intensity * qt_Opacity;
    // Premultiplied output: rgb already multiplied by the final alpha.
    fragColor = vec4(tint.rgb * a, a);
}
