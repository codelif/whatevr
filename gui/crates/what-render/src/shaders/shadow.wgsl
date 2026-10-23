// evan wallace's rounded box shadow: erf across x exactly, 4 gaussian samples down y.
// https://madebyevan.com/shaders/fast-rounded-rectangle-shadows/

struct Shadow {
    // the blurred shape: casting box moved by offset, grown by spread
    @location(0) rect: vec4<f32>,
    @location(1) radii: vec4<f32>,
    @location(2) color: vec4<f32>,
    // x: sigma
    @location(3) params: vec4<f32>,
    // the casting box, shadow is cut out under it
    @location(4) box_rect: vec4<f32>,
    @location(5) box_radii: vec4<f32>,
    @location(6) clip_rect: vec4<f32>,
    @location(7) clip_radii: vec4<f32>,
}

struct Varyings {
    @builtin(position) position: vec4<f32>,
    @location(0) @interpolate(flat) rect: vec4<f32>,
    @location(1) @interpolate(flat) radii: vec4<f32>,
    @location(2) @interpolate(flat) color: vec4<f32>,
    @location(3) @interpolate(flat) params: vec4<f32>,
    @location(4) @interpolate(flat) box_rect: vec4<f32>,
    @location(5) @interpolate(flat) box_radii: vec4<f32>,
    @location(6) @interpolate(flat) clip_rect: vec4<f32>,
    @location(7) @interpolate(flat) clip_radii: vec4<f32>,
}

@vertex
fn vs(@builtin(vertex_index) vi: u32, s: Shadow) -> Varyings {
    var v: Varyings;
    let margin = ceil(3.0 * s.params.x) + 1.0;
    let lo = s.rect.xy - margin;
    let size = s.rect.zw + 2.0 * margin;
    v.position = to_ndc(lo + unit_corner(vi) * size);
    v.rect = s.rect;
    v.radii = s.radii;
    v.color = s.color;
    v.params = s.params;
    v.box_rect = s.box_rect;
    v.box_radii = s.box_radii;
    v.clip_rect = s.clip_rect;
    v.clip_radii = s.clip_radii;
    return v;
}

fn gaussian(x: f32, sigma: f32) -> f32 {
    return exp(-(x * x) / (2.0 * sigma * sigma)) / (sqrt(2.0 * 3.14159265) * sigma);
}

// abramowitz and stegun 7.1.27, max error 5e-4
fn erf2(x: vec2<f32>) -> vec2<f32> {
    let s = sign(x);
    let a = abs(x);
    var y = 1.0 + (0.278393 + (0.230389 + 0.078108 * (a * a)) * a) * a;
    y = y * y;
    return s - s / (y * y);
}

// coverage of one row of the box at height y, blurred along x
fn shadow_x(x: f32, y: f32, sigma: f32, corner: f32, half: vec2<f32>) -> f32 {
    let delta = min(half.y - corner - abs(y), 0.0);
    let curved = half.x - corner + sqrt(max(0.0, corner * corner - delta * delta));
    let integral = 0.5 + 0.5 * erf2((x + vec2<f32>(-curved, curved)) * (sqrt(0.5) / sigma));
    return integral.y - integral.x;
}

fn shadow(p: vec2<f32>, rect: vec4<f32>, radii: vec4<f32>, sigma: f32) -> f32 {
    let half = rect.zw * 0.5;
    let c = p - rect.xy - half;
    let corner = pick(c, radii);
    let lo = clamp(-3.0 * sigma, c.y - half.y, c.y + half.y);
    let hi = clamp(3.0 * sigma, c.y - half.y, c.y + half.y);
    let step = (hi - lo) / 4.0;
    var y = lo + step * 0.5;
    var value = 0.0;
    for (var i = 0; i < 4; i++) {
        value += shadow_x(c.x, c.y - y, sigma, corner, half) * gaussian(y, sigma) * step;
        y += step;
    }
    return value;
}

@fragment
fn fs(v: Varyings) -> @location(0) vec4<f32> {
    let p = v.position.xy;
    let sigma = v.params.x;
    var a: f32;
    if sigma < 0.01 {
        a = coverage(sd_rounded(p, v.rect, v.radii));
    } else {
        a = shadow(p, v.rect, v.radii, sigma);
    }
    let outside_box = 1.0 - coverage(sd_rounded(p, v.box_rect, v.box_radii));
    return v.color * (a * outside_box * clip_coverage(p, v.clip_rect, v.clip_radii));
}
