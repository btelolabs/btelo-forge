// The slow filters of internal/paint on a graphics card: the same steps as the code there, on the same numbers. A pixel
// is four floats. Lines go one to an invocation, each with a running sum, so a blur's cost does not grow with its size.

struct Params {
    size: vec4f,    // width, height, box, surroundings or not
    tones: vec4f,   // shadows, highlights, clarity, develop or not
    gain: vec4f,    // red, green, blue, exposure
    levels: vec4f,  // black, span, steep, 0
    colour: vec4f,  // vibrance, saturation, 0, 0
}

@group(0) @binding(0) var<uniform> p: Params;
@group(0) @binding(1) var<storage, read> from: array<vec4f>;
@group(0) @binding(2) var<storage, read_write> into: array<vec4f>;
@group(0) @binding(3) var<storage, read> also: array<vec4f>;

fn at(x: i32, y: i32) -> i32 { return y * i32(p.size.x) + x; }

@compute @workgroup_size(1)
fn across(@builtin(global_invocation_id) id: vec3u) {
    let y = i32(id.y);
    let w = i32(p.size.x);
    let r = i32(p.size.z);
    var sum = vec4f(0.0);
    for (var i = -r; i <= r; i++) {
        sum += from[at(clamp(i, 0, w - 1), y)];
    }
    let k = f32(2 * r + 1);
    for (var x = 0; x < w; x++) {
        into[at(x, y)] = sum / k;
        sum += from[at(min(x + r + 1, w - 1), y)] - from[at(max(x - r, 0), y)];
    }
}

@compute @workgroup_size(1)
fn down(@builtin(global_invocation_id) id: vec3u) {
    let x = i32(id.y);
    let h = i32(p.size.y);
    let r = i32(p.size.z);
    var sum = vec4f(0.0);
    for (var i = -r; i <= r; i++) {
        sum += from[at(x, clamp(i, 0, h - 1))];
    }
    let k = f32(2 * r + 1);
    for (var y = 0; y < h; y++) {
        into[at(x, y)] = sum / k;
        sum += from[at(x, min(y + r + 1, h - 1))] - from[at(x, max(y - r, 0))];
    }
}

const edge = 0.003; // surroundingsEdge

fn brightness(c: vec3f) -> f32 { return dot(c, vec3f(0.299, 0.587, 0.114)); }

// The brightness and its square, to box.
@compute @workgroup_size(64)
fn light(@builtin(global_invocation_id) id: vec3u) {
    let i = id.x;
    if (i >= arrayLength(&from)) { return; }
    let c = from[i];
    var l = 0.0;
    if (c.a > 0.0) { l = brightness(c.rgb) / c.a; }
    into[i] = vec4f(l, l * l, 0.0, 0.0);
}

// From the boxed brightness and square, the guided filter's gain and base, to box.
@compute @workgroup_size(64)
fn guide(@builtin(global_invocation_id) id: vec3u) {
    let i = id.x;
    if (i >= arrayLength(&from)) { return; }
    let m = from[i].x;
    let variance = max(from[i].y - m * m, 0.0);
    let g = variance / (variance + edge);
    into[i] = vec4f(g, (1.0 - g) * m, 0.0, 0.0);
}

fn lifted(v: f32, amount: f32) -> f32 {
    if (v <= 0.0) { return 0.0; }
    return pow(v, pow(2.0, -amount));
}

fn toLinear(v: f32) -> f32 {
    if (v <= 0.04045) { return v / 12.92; }
    return pow((v + 0.055) / 1.055, 2.4);
}

fn toGamma(v: f32) -> f32 {
    if (v <= 0.0031308) { return v * 12.92; }
    return 1.055 * pow(v, 1.0 / 2.4) - 0.055;
}

fn saturate3(c: vec3f, factor: f32) -> vec3f {
    let l = brightness(c);
    return vec3f(l) + (c - vec3f(l)) * factor;
}

fn hueOf(c: vec3f) -> f32 {
    let mx = max(c.r, max(c.g, c.b));
    let mn = min(c.r, min(c.g, c.b));
    let d = mx - mn;
    if (d == 0.0) { return 0.0; }
    var h = 0.0;
    if (mx == c.r) {
        h = (c.g - c.b) / d;
        if (c.g < c.b) { h += 6.0; }
    } else if (mx == c.g) {
        h = (c.b - c.r) / d + 2.0;
    } else {
        h = (c.r - c.g) / d + 4.0;
    }
    return h / 6.0;
}

fn vibrate(c: vec3f, amount: f32) -> vec3f {
    if (amount == 0.0) { return c; }
    let mx = max(c.r, max(c.g, c.b));
    let mn = min(c.r, min(c.g, c.b));
    var s = 0.0;
    if (mx > 0.0) { s = (mx - mn) / mx; }
    let turned = (hueOf(c) * 360.0 - 25.0 + 540.0) % 360.0;
    let off = (turned - 180.0) / 30.0;
    let k = amount * (1.0 - s) * (1.0 - 0.5 * exp(-off * off));
    return saturate3(c, 1.0 + k);
}

fn lit(v: f32, wb: f32) -> f32 {
    var o = toGamma(clamp(toLinear(v) * wb * p.gain.w, 0.0, 1.0));
    o = clamp((o - p.levels.x) / p.levels.y, 0.0, 1.0);
    return clamp((o - 0.5) * p.levels.z + 0.5, 0.0, 1.0);
}

// Each pixel with its surroundings: from is the picture, also the boxed gain and base, into what it becomes.
@compute @workgroup_size(64)
fn tones(@builtin(global_invocation_id) id: vec3u) {
    let i = id.x;
    if (i >= arrayLength(&from)) { return; }
    let px = from[i];
    if (px.a == 0.0) {
        into[i] = vec4f(0.0);
        return;
    }
    var c = px.rgb / px.a;
    if (p.size.w > 0.0) {
        let own = brightness(px.rgb) / px.a;
        let around = clamp(also[i].x * own + also[i].y, 0.0, 1.0);
        if (around >= 1.0 / 255.0) {
            let dark = (1.0 - around) * (1.0 - around);
            let bright = around * around;
            let bent = 1.0 - lifted(1.0 - lifted(around, p.tones.x * dark), p.tones.y * bright);
            c *= min(bent / around, 4.0);
        }
        if (p.tones.z != 0.0) {
            let l = brightness(c);
            let detail = (l - around) * (1.0 - abs(2.0 * l - 1.0)) * p.tones.z;
            c = clamp(c + vec3f(detail), vec3f(0.0), vec3f(1.0));
        }
    }
    if (p.tones.w > 0.0) {
        c = vec3f(lit(c.r, p.gain.x), lit(c.g, p.gain.y), lit(c.b, p.gain.z));
        c = vibrate(clamp(c, vec3f(0.0), vec3f(1.0)), p.colour.x);
        c = saturate3(c, p.colour.y);
    }
    into[i] = vec4f(clamp(c, vec3f(0.0), vec3f(1.0)) * px.a, px.a);
}
