// A path tracer in a compute shader: it adds a sample to each pixel of a band of the view, and writes the mean so far. The
// triangles are found through a tree made on the processor; pictures are read from a buffer, as standard WebGPU has no way
// to choose a texture by a number.

struct Params {
  eye: vec4<f32>,       // w: exposure
  right: vec4<f32>,     // w: the width in pixels
  up: vec4<f32>,        // w: the height in pixels
  ahead: vec4<f32>,     // w: the number of this sample, from 0
  sun_dir: vec4<f32>,   // w: its size, the sine of its half angle
  sun: vec4<f32>,       // its light; w: bounces
  sky: vec4<f32>,       // cos and sin of the sky's rotation, whether it shows, how high the ground is
  band: vec4<f32>,      // the band's first row and its height, whether there is a ground, how far it fades out to
};

struct Node {
  low: vec3<f32>,
  a: u32,               // a leaf's first triangle, or an inner node's right child, the left being the next node
  high: vec3<f32>,
  count: u32,           // a leaf's triangles, or 0
};

struct Triangle {
  p: array<vec4<f32>, 3>,   // w: u
  n: array<vec4<f32>, 3>,   // w: v
  material: vec4<u32>,
};

struct Material {
  base: vec4<f32>,
  emissive: vec4<f32>,      // w: 1 when alpha is coverage
  metallic: f32,
  roughness: f32,
  picture: u32,             // where its texels start, in texels
  side: u32,                // 0 for none
};

@group(0) @binding(0) var<uniform> params: Params;
@group(0) @binding(1) var<storage, read> nodes: array<Node>;
@group(0) @binding(2) var<storage, read> triangles: array<Triangle>;
@group(0) @binding(3) var<storage, read> materials: array<Material>;
@group(0) @binding(4) var<storage, read> texels: array<u32>;
@group(0) @binding(5) var<storage, read_write> sums: array<vec4<f32>>;
@group(0) @binding(6) var<storage, read_write> shown: array<u32>;
@group(0) @binding(7) var sky_map: texture_2d<f32>;
@group(0) @binding(8) var sky_sampler: sampler;

const PI: f32 = 3.14159265;
const NO_HIT: f32 = 1e30;
const STACK: u32 = 48u;
const BRIGHTEST: f32 = 16.0;   // a single sample no brighter, for a few stray ones not to speckle the picture for long
const GROUND: u32 = 0xffffffffu;

var<private> state: u32;

fn random() -> f32 {
  state = state * 747796405u + 2891336453u;
  let w = ((state >> ((state >> 28u) + 4u)) ^ state) * 277803737u;
  return f32((w >> 22u) ^ w) / 4294967296.0;
}

struct Hit {
  distance: f32,
  triangle: u32,
  u: f32,
  v: f32,
};

fn box_near(low: vec3<f32>, high: vec3<f32>, origin: vec3<f32>, inverse: vec3<f32>, limit: f32) -> f32 {
  let a = (low - origin) * inverse;
  let b = (high - origin) * inverse;
  let near = max(max(min(a.x, b.x), min(a.y, b.y)), min(a.z, b.z));
  let far = min(min(max(a.x, b.x), max(a.y, b.y)), max(a.z, b.z));
  if (far >= max(near, 0.0) && near < limit) {
    return near;
  }
  return NO_HIT;
}

// covers decides whether a ray stops at a point of a surface with some coverage, by chance as to how much.
fn covers(t: u32, u: f32, v: f32) -> bool {
  let m = materials[triangles[t].material.x];
  if (m.emissive.w < 0.5) {
    return true;
  }
  return surface_color(m, surface_uv(t, u, v)).a > random();
}

fn surface_uv(t: u32, u: f32, v: f32) -> vec2<f32> {
  let q = triangles[t];
  let w = 1.0 - u - v;
  return vec2<f32>(q.p[0].w * w + q.p[1].w * u + q.p[2].w * v, q.n[0].w * w + q.n[1].w * u + q.n[2].w * v);
}

fn intersect(origin: vec3<f32>, dir: vec3<f32>, limit: f32) -> Hit {
  var best = Hit(limit, GROUND, 0.0, 0.0);
  let inverse = 1.0 / dir;
  var stack: array<u32, STACK>;
  var top = 1u;
  stack[0] = 0u;
  loop {
    if (top == 0u) {
      break;
    }
    top -= 1u;
    var at = stack[top];
    loop {
      let n = nodes[at];
      if (box_near(n.low, n.high, origin, inverse, best.distance) == NO_HIT) {
        break;
      }
      if (n.count > 0u) {
        for (var i = 0u; i < n.count; i += 1u) {
          let t = n.a + i;
          let a = triangles[t].p[0].xyz;
          let e1 = triangles[t].p[1].xyz - a;
          let e2 = triangles[t].p[2].xyz - a;
          let p = cross(dir, e2);
          let det = dot(e1, p);
          if (abs(det) < 1e-12) {
            continue;
          }
          let s = origin - a;
          let u = dot(s, p) / det;
          if (u < 0.0 || u > 1.0) {
            continue;
          }
          let q = cross(s, e1);
          let v = dot(dir, q) / det;
          if (v < 0.0 || u + v > 1.0) {
            continue;
          }
          let d = dot(e2, q) / det;
          if (d > 1e-6 && d < best.distance && covers(t, u, v)) {
            best = Hit(d, t, u, v);
          }
        }
        break;
      }
      if (top < STACK) {
        stack[top] = n.a;
        top += 1u;
      }
      at = at + 1u;
    }
  }
  return best;
}

fn sky_light(d: vec3<f32>) -> vec3<f32> {
  let c = params.sky.x;
  let s = params.sky.y;
  let r = vec3<f32>(c * d.x - s * d.z, d.y, s * d.x + c * d.z);
  let uv = vec2<f32>(atan2(r.x, r.z) / (2.0 * PI) + 0.5, 0.5 - asin(clamp(r.y, -1.0, 1.0)) / PI);
  return textureSampleLevel(sky_map, sky_sampler, uv, 0.0).rgb;
}

fn texel(m: Material, x: u32, y: u32) -> vec4<f32> {
  let v = texels[m.picture + (y % m.side) * m.side + (x % m.side)];
  let c = vec4<f32>(f32(v & 255u), f32((v >> 8u) & 255u), f32((v >> 16u) & 255u), f32(v >> 24u)) / 255.0;
  return vec4<f32>(pow(c.rgb, vec3<f32>(2.2)), c.a);
}

fn surface_color(m: Material, uv: vec2<f32>) -> vec4<f32> {
  if (m.side == 0u) {
    return m.base;
  }
  let at = fract(uv) * f32(m.side) - 0.5;
  let f = fract(at);
  let x = u32(floor(at.x) + f32(m.side));
  let y = u32(floor(at.y) + f32(m.side));
  let top = mix(texel(m, x, y), texel(m, x + 1u, y), f.x);
  let low = mix(texel(m, x, y + 1u), texel(m, x + 1u, y + 1u), f.x);
  return mix(top, low, f.y) * m.base;
}

fn basis(n: vec3<f32>) -> mat3x3<f32> {
  let s = select(-1.0, 1.0, n.z >= 0.0);
  let a = -1.0 / (s + n.z);
  let b = n.x * n.y * a;
  return mat3x3<f32>(vec3<f32>(1.0 + s * n.x * n.x * a, s * b, -s * n.x), vec3<f32>(b, s + n.y * n.y * a, -n.y), n);
}

fn around(n: vec3<f32>) -> vec3<f32> {
  let r = sqrt(random());
  let phi = 2.0 * PI * random();
  return basis(n) * vec3<f32>(r * cos(phi), r * sin(phi), sqrt(max(1.0 - r * r, 0.0)));
}

fn ggx_half(n: vec3<f32>, alpha: f32) -> vec3<f32> {
  let x = random();
  let cos_t = sqrt((1.0 - x) / (1.0 + (alpha * alpha - 1.0) * x));
  let sin_t = sqrt(max(1.0 - cos_t * cos_t, 0.0));
  let phi = 2.0 * PI * random();
  return basis(n) * vec3<f32>(sin_t * cos(phi), sin_t * sin(phi), cos_t);
}

fn fresnel(f0: vec3<f32>, c: f32) -> vec3<f32> {
  return f0 + (vec3<f32>(1.0) - f0) * pow(clamp(1.0 - c, 0.0, 1.0), 5.0);
}

fn ggx_d(nh: f32, alpha: f32) -> f32 {
  let a = alpha * alpha;
  let d = nh * nh * (a - 1.0) + 1.0;
  return a / (PI * d * d);
}

fn smith(nv: f32, nl: f32, alpha: f32) -> f32 {
  let k = alpha / 2.0;
  return 1.0 / ((nv * (1.0 - k) + k) * (nl * (1.0 - k) + k));
}

fn tonemap(c: vec3<f32>) -> vec3<f32> {
  let x = c * params.eye.w * 0.6;
  let t = clamp((x * (2.51 * x + 0.03)) / (x * (2.43 * x + 0.59) + 0.14), vec3<f32>(0.0), vec3<f32>(1.0));
  return pow(t, vec3<f32>(1.0 / 2.2));
}

fn background(d: vec3<f32>, y: f32) -> vec3<f32> {
  if (params.sky.z < 0.5) {
    return mix(vec3<f32>(0.0066, 0.0075, 0.0091), vec3<f32>(0.022, 0.025, 0.03), y);
  }
  return sky_light(d);
}

fn trace(start: vec3<f32>, first: vec3<f32>, y: f32) -> vec3<f32> {
  var origin = start;
  var dir = first;
  var light = vec3<f32>(0.0);
  var carried = vec3<f32>(1.0);
  let bounces = u32(params.sun.w);
  for (var bounce = 0u; bounce <= bounces; bounce += 1u) {
    var limit = NO_HIT;
    let ground = params.band.z > 0.5 && dir.y < 0.0;
    if (ground) {
      limit = (params.sky.w - origin.y) / dir.y;
    }
    let hit = intersect(origin, dir, limit);
    if (hit.triangle == GROUND && !ground) {
      if (bounce == 0u) {
        return background(dir, y);
      }
      return light + carried * sky_light(dir);
    }
    var at = origin + dir * hit.distance;
    var normal = vec3<f32>(0.0, 1.0, 0.0);
    var base = vec3<f32>(0.5);
    var metallic = 0.0;
    var roughness = 0.9;
    if (hit.triangle != GROUND) {
      let q = triangles[hit.triangle];
      let m = materials[q.material.x];
      let w = 1.0 - hit.u - hit.v;
      normal = normalize(q.n[0].xyz * w + q.n[1].xyz * hit.u + q.n[2].xyz * hit.v);
      let flat_n = normalize(cross(q.p[1].xyz - q.p[0].xyz, q.p[2].xyz - q.p[0].xyz));
      if (dot(flat_n, dir) > 0.0 && dot(normal, dir) > 0.0) {
        normal = -normal;
      } else if (dot(normal, dir) > 0.0) {
        normal = normalize(normal - dir * dot(normal, dir) * 1.01);
      }
      base = surface_color(m, surface_uv(hit.triangle, hit.u, hit.v)).rgb;
      metallic = m.metallic;
      roughness = max(m.roughness, 0.04);
      light += carried * m.emissive.rgb;
    } else {
      at.y = params.sky.w;
      // The ground ends in the sky far off, as it does in the plain view, by chance for how much.
      let fade = clamp(1.0 - length(at.xz - params.eye.xz) / params.band.w, 0.0, 1.0);
      if (bounce == 0u && random() > fade * fade) {
        return background(dir, y);
      }
    }
    let f0 = mix(vec3<f32>(0.04), base, metallic);
    let diffuse = base * (1.0 - metallic);
    let view = -dir;
    let nv = max(dot(normal, view), 1e-3);
    let alpha = roughness * roughness;
    let shifted = at + normal * 1e-4 * (1.0 + length(at - params.eye.xyz));
    // The sun: straight at it, for every surface.
    if (params.sun_dir.w >= 0.0) {
      let l = normalize(params.sun_dir.xyz + basis(params.sun_dir.xyz) * vec3<f32>((random() - 0.5) * 2.0, (random() - 0.5) * 2.0, 0.0) * params.sun_dir.w);
      let nl = dot(normal, l);
      if (nl > 0.0 && intersect(shifted, l, NO_HIT).distance == NO_HIT) {
        let h = normalize(l + view);
        let nh = max(dot(normal, h), 0.0);
        let f = fresnel(f0, dot(view, h));
        let spec = f * ggx_d(nh, alpha) * smith(nv, nl, alpha) / 4.0;
        light += carried * (diffuse / PI * (vec3<f32>(1.0) - f) + min(spec, vec3<f32>(8.0))) * params.sun.rgb * nl;
      }
    }
    // Then on one way, chosen as much as it matters.
    let mirror = fresnel(f0, nv);
    let weight_m = dot(mirror, vec3<f32>(0.333));
    let weight_d = dot(diffuse, vec3<f32>(0.333)) * (1.0 - weight_m);
    let share = clamp(weight_m / max(weight_m + weight_d, 1e-4), 0.05, 0.95);
    if (random() < share) {
      let h = ggx_half(normal, alpha);
      let l = reflect(dir, h);
      let nl = dot(normal, l);
      if (nl <= 0.0) {
        break;
      }
      let vh = max(dot(view, h), 1e-3);
      carried *= fresnel(f0, vh) * smith(nv, nl, alpha) * nl * nv * vh / max(dot(normal, h), 1e-3) * 4.0 / nv / 4.0 / share;
      dir = l;
    } else {
      carried *= diffuse * (vec3<f32>(1.0) - mirror) / (1.0 - share);
      dir = around(normal);
    }
    origin = shifted;
    if (bounce >= 2u) {
      let keep = clamp(max(carried.r, max(carried.g, carried.b)), 0.05, 1.0);
      if (random() > keep) {
        break;
      }
      carried /= keep;
    }
  }
  return light;
}

@compute @workgroup_size(16, 16)
fn main(@builtin(global_invocation_id) id: vec3<u32>) {
  let w = u32(params.right.w);
  let h = u32(params.up.w);
  let x = id.x;
  let y = id.y + u32(params.band.x);
  if (x >= w || y >= h || id.y >= u32(params.band.y)) {
    return;
  }
  let sample = u32(params.ahead.w);
  state = (x * 1973u + y * 9277u + sample * 26699u) | 1u;
  random();
  let px = (f32(x) + random()) / f32(w) * 2.0 - 1.0;
  let py = 1.0 - (f32(y) + random()) / f32(h) * 2.0;
  let dir = normalize(params.ahead.xyz + params.right.xyz * px + params.up.xyz * py);
  var c = trace(params.eye.xyz, dir, py * 0.5 + 0.5);
  if (!(c.x == c.x && c.y == c.y && c.z == c.z)) {
    c = vec3<f32>(0.0);
  }
  c = min(c, vec3<f32>(BRIGHTEST));
  let i = y * w + x;
  var sum = c;
  if (sample > 0u) {
    sum += sums[i].rgb;
  }
  sums[i] = vec4<f32>(sum, 1.0);
  let t = tonemap(sum / f32(sample + 1u));
  shown[i] = u32(t.r * 255.0 + 0.5) | (u32(t.g * 255.0 + 0.5) << 8u) | (u32(t.b * 255.0 + 0.5) << 16u) | (255u << 24u);
}
