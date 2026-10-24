@group(1) @binding(0) var page: texture_2d<f32>;

struct Glyph {
    // whole px
    @location(0) rect: vec4<f32>,
    // xy: top left texel in the page. z: 0 mask, 1 colour
    @location(1) texel: vec4<f32>,
    // premultiplied srgb
    @location(2) color: vec4<f32>,
    @location(3) clip_rect: vec4<f32>,
    @location(4) clip_radii: vec4<f32>,
}

struct Varyings {
    @builtin(position) position: vec4<f32>,
    @location(0) @interpolate(flat) rect: vec4<f32>,
    @location(1) @interpolate(flat) texel: vec4<f32>,
    @location(2) @interpolate(flat) color: vec4<f32>,
    @location(3) @interpolate(flat) clip_rect: vec4<f32>,
    @location(4) @interpolate(flat) clip_radii: vec4<f32>,
}

@vertex
fn vs(@builtin(vertex_index) vi: u32, g: Glyph) -> Varyings {
    var v: Varyings;
    v.position = to_ndc(g.rect.xy + unit_corner(vi) * g.rect.zw);
    v.rect = g.rect;
    v.texel = g.texel;
    v.color = g.color;
    v.clip_rect = g.clip_rect;
    v.clip_radii = g.clip_radii;
    return v;
}

@fragment
fn fs(v: Varyings) -> @location(0) vec4<f32> {
    let p = v.position.xy;
    let at = vec2<i32>(v.texel.xy + floor(p - v.rect.xy));
    let texel = textureLoad(page, at, 0);
    let clip = clip_coverage(p, v.clip_rect, v.clip_radii);
    if v.texel.z > 0.5 {
        // colour glyphs keep their own colours, the text only lends its opacity
        return texel * (v.color.a * clip);
    }
    return v.color * (texel.r * clip);
}
