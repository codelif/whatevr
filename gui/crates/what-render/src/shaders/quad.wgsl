struct Quad {
    @location(0) rect: vec4<f32>,
    @location(1) radii: vec4<f32>,
    // top, right, bottom, left
    @location(2) border: vec4<f32>,
    @location(3) border_color: vec4<f32>,
    @location(4) start: vec4<f32>,
    @location(5) end: vec4<f32>,
    // linear: from.xy, to.xy. radial: center.xy, radius.xy. in the quad's 0..1 box
    @location(6) gradient: vec4<f32>,
    // x: 0 solid, 1 linear, 2 radial
    @location(7) kind: vec4<f32>,
    @location(8) clip_rect: vec4<f32>,
    @location(9) clip_radii: vec4<f32>,
}

struct Varyings {
    @builtin(position) position: vec4<f32>,
    @location(0) @interpolate(flat) rect: vec4<f32>,
    @location(1) @interpolate(flat) radii: vec4<f32>,
    @location(2) @interpolate(flat) border: vec4<f32>,
    @location(3) @interpolate(flat) border_color: vec4<f32>,
    @location(4) @interpolate(flat) start: vec4<f32>,
    @location(5) @interpolate(flat) end: vec4<f32>,
    @location(6) @interpolate(flat) gradient: vec4<f32>,
    @location(7) @interpolate(flat) kind: vec4<f32>,
    @location(8) @interpolate(flat) clip_rect: vec4<f32>,
    @location(9) @interpolate(flat) clip_radii: vec4<f32>,
}

@vertex
fn vs(@builtin(vertex_index) vi: u32, q: Quad) -> Varyings {
    var v: Varyings;
    v.position = to_ndc(q.rect.xy + unit_corner(vi) * q.rect.zw);
    v.rect = q.rect;
    v.radii = q.radii;
    v.border = q.border;
    v.border_color = q.border_color;
    v.start = q.start;
    v.end = q.end;
    v.gradient = q.gradient;
    v.kind = q.kind;
    v.clip_rect = q.clip_rect;
    v.clip_radii = q.clip_radii;
    return v;
}

fn fill(p: vec2<f32>, v: Varyings) -> vec4<f32> {
    let kind = u32(v.kind.x);
    if kind == 0u {
        return v.start;
    }
    var t: f32;
    if kind == 1u {
        // the line runs in pixels so perpendiculars stay perpendicular on non-square quads
        let a = v.rect.xy + v.gradient.xy * v.rect.zw;
        let d = (v.gradient.zw - v.gradient.xy) * v.rect.zw;
        t = dot(p - a, d) / max(dot(d, d), 1e-6);
    } else {
        let c = v.rect.xy + v.gradient.xy * v.rect.zw;
        let r = max(v.gradient.zw * v.rect.zw, vec2<f32>(1e-6));
        t = length((p - c) / r);
    }
    return mix(v.start, v.end, saturate(t));
}

@fragment
fn fs(v: Varyings) -> @location(0) vec4<f32> {
    let p = v.position.xy;
    let outer = coverage(sd_rounded(p, v.rect, v.radii));
    var color = fill(p, v) * outer;

    let b = v.border;
    if any(b > vec4<f32>(0.0)) {
        // css inner edge: the box shrunk by each side, corners become (r - side, r - top/bottom) ellipses
        let half = (v.rect.zw - vec2<f32>(b.w + b.y, b.x + b.z)) * 0.5;
        let c = p - (v.rect.xy + vec2<f32>(b.w, b.x) + half);
        let r = pick(c, v.radii);
        let side = vec2<f32>(select(b.w, b.y, c.x > 0.0), select(b.x, b.z, c.y > 0.0));
        let inner = coverage(sd_box(c, half, max(vec2<f32>(r) - side, vec2<f32>(0.0))));
        let ring = v.border_color * max(outer - inner, 0.0);
        color = ring + color * (1.0 - ring.a);
    }
    return color * clip_coverage(p, v.clip_rect, v.clip_radii);
}
