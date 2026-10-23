// prepended to every pipeline's shader

struct Globals {
    // target size in physical px
    viewport: vec2<f32>,
}

@group(0) @binding(0) var<uniform> globals: Globals;

// triangle strip corner for vertex 0..3
fn unit_corner(vi: u32) -> vec2<f32> {
    return vec2<f32>(f32(vi & 1u), f32(vi >> 1u));
}

fn to_ndc(px: vec2<f32>) -> vec4<f32> {
    let ndc = px / globals.viewport * 2.0 - 1.0;
    return vec4<f32>(ndc.x, -ndc.y, 0.0, 1.0);
}

// v is (top left, top right, bottom right, bottom left); p is relative to the centre
fn pick(p: vec2<f32>, v: vec4<f32>) -> f32 {
    let top = select(v.x, v.y, p.x > 0.0);
    let bottom = select(v.w, v.z, p.x > 0.0);
    return select(top, bottom, p.y > 0.0);
}

// signed distance to a box with one elliptical corner radius r, p relative to the centre.
// exact for circles, iq's ellipse approximation otherwise (exact on the curve itself)
fn sd_box(p: vec2<f32>, half: vec2<f32>, r: vec2<f32>) -> f32 {
    let q = abs(p) - (half - r);
    if q.x > 0.0 && q.y > 0.0 && r.x > 0.0 && r.y > 0.0 {
        let k0 = length(q / r);
        let k1 = length(q / (r * r));
        return k0 * (k0 - 1.0) / k1;
    }
    let b = abs(p) - half;
    return min(max(b.x, b.y), 0.0) + length(max(b, vec2<f32>(0.0)));
}

fn sd_rounded(p: vec2<f32>, rect: vec4<f32>, radii: vec4<f32>) -> f32 {
    let half = rect.zw * 0.5;
    let c = p - rect.xy - half;
    let r = pick(c, radii);
    return sd_box(c, half, vec2<f32>(r));
}

// pixel centres sit half a pixel from snapped edges, so straight edges come out 0 or 1
fn coverage(d: f32) -> f32 {
    return saturate(0.5 - d);
}

fn clip_coverage(p: vec2<f32>, rect: vec4<f32>, radii: vec4<f32>) -> f32 {
    return coverage(sd_rounded(p, rect, radii));
}
