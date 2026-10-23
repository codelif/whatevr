@group(1) @binding(0) var page: texture_2d<f32>;
@group(1) @binding(1) var page_sampler: sampler;

struct Image {
    @location(0) rect: vec4<f32>,
    // u0, v0, u1, v1 in the page
    @location(1) uv: vec4<f32>,
    @location(2) radii: vec4<f32>,
    // x: opacity
    @location(3) params: vec4<f32>,
    @location(4) clip_rect: vec4<f32>,
    @location(5) clip_radii: vec4<f32>,
}

struct Varyings {
    @builtin(position) position: vec4<f32>,
    @location(0) uv: vec2<f32>,
    @location(1) @interpolate(flat) rect: vec4<f32>,
    @location(2) @interpolate(flat) radii: vec4<f32>,
    @location(3) @interpolate(flat) params: vec4<f32>,
    @location(4) @interpolate(flat) clip_rect: vec4<f32>,
    @location(5) @interpolate(flat) clip_radii: vec4<f32>,
}

@vertex
fn vs(@builtin(vertex_index) vi: u32, i: Image) -> Varyings {
    var v: Varyings;
    let u = unit_corner(vi);
    v.position = to_ndc(i.rect.xy + u * i.rect.zw);
    v.uv = mix(i.uv.xy, i.uv.zw, u);
    v.rect = i.rect;
    v.radii = i.radii;
    v.params = i.params;
    v.clip_rect = i.clip_rect;
    v.clip_radii = i.clip_radii;
    return v;
}

@fragment
fn fs(v: Varyings) -> @location(0) vec4<f32> {
    let p = v.position.xy;
    // pages hold premultiplied linear light, srgb encoded, so filtering happens in linear
    let texel = textureSample(page, page_sampler, v.uv);
    let shape = coverage(sd_rounded(p, v.rect, v.radii));
    return texel * (v.params.x * shape * clip_coverage(p, v.clip_rect, v.clip_radii));
}
