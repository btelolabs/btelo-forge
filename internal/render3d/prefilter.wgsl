// Blurs a panorama for one roughness, by importance-sampling the GGX distribution: what a rough surface reflects.

struct Params {
  roughness: vec4<f32>,
};

@group(0) @binding(0) var<uniform> params: Params;
@group(0) @binding(1) var source: texture_2d<f32>;
@group(0) @binding(2) var source_sampler: sampler;

const PI: f32 = 3.14159265;
const SAMPLES: u32 = 512u;

struct Out {
  @builtin(position) position: vec4<f32>,
  @location(0) uv: vec2<f32>,
};

@vertex
fn vs(@builtin(vertex_index) i: u32) -> Out {
  let x = f32((i << 1u) & 2u) * 2.0 - 1.0;
  let y = f32(i & 2u) * 2.0 - 1.0;
  var o: Out;
  o.position = vec4<f32>(x, y, 0.0, 1.0);
  o.uv = vec2<f32>(x * 0.5 + 0.5, 0.5 - y * 0.5);
  return o;
}

fn radical_inverse(v: u32) -> f32 {
  var b = v;
  b = (b << 16u) | (b >> 16u);
  b = ((b & 0x55555555u) << 1u) | ((b & 0xAAAAAAAAu) >> 1u);
  b = ((b & 0x33333333u) << 2u) | ((b & 0xCCCCCCCCu) >> 2u);
  b = ((b & 0x0F0F0F0Fu) << 4u) | ((b & 0xF0F0F0F0u) >> 4u);
  b = ((b & 0x00FF00FFu) << 8u) | ((b & 0xFF00FF00u) >> 8u);
  return f32(b) * 2.3283064365386963e-10;
}

fn panorama_uv(d: vec3<f32>) -> vec2<f32> {
  return vec2<f32>(atan2(d.x, d.z) / (2.0 * PI) + 0.5, 0.5 - asin(clamp(d.y, -1.0, 1.0)) / PI);
}

@fragment
fn fs(i: Out) -> @location(0) vec4<f32> {
  let lon = (i.uv.x - 0.5) * 2.0 * PI;
  let lat = (0.5 - i.uv.y) * PI;
  let n = vec3<f32>(cos(lat) * sin(lon), sin(lat), cos(lat) * cos(lon));
  let roughness = params.roughness.x;
  if (roughness <= 0.0) {
    return vec4<f32>(textureSampleLevel(source, source_sampler, i.uv, 0.0).rgb, 1.0);
  }
  let a = roughness * roughness;
  var up = vec3<f32>(0.0, 1.0, 0.0);
  if (abs(n.y) > 0.99) {
    up = vec3<f32>(1.0, 0.0, 0.0);
  }
  let tx = normalize(cross(up, n));
  let ty = cross(n, tx);
  var total = vec3<f32>(0.0);
  var weight = 0.0;
  let size = vec2<f32>(textureDimensions(source, 0));
  for (var s = 0u; s < SAMPLES; s = s + 1u) {
    let u = f32(s) / f32(SAMPLES);
    let v = radical_inverse(s);
    let phi = 2.0 * PI * u;
    let cos_t = sqrt((1.0 - v) / (1.0 + (a * a - 1.0) * v));
    let sin_t = sqrt(max(1.0 - cos_t * cos_t, 0.0));
    let h = tx * (cos(phi) * sin_t) + ty * (sin(phi) * sin_t) + n * cos_t;
    let l = 2.0 * dot(n, h) * h - n;
    let nl = dot(n, l);
    if (nl > 0.0) {
      // A sample standing for a large part of the sphere reads a blurrier level, which keeps out the speckle.
      let d = cos_t * cos_t * (a * a - 1.0) + 1.0;
      let pdf = (a * a / (PI * d * d)) * 0.25;
      let texel = 4.0 * PI / (size.x * size.y);
      let lod = clamp(0.5 * log2((1.0 / (f32(SAMPLES) * max(pdf, 0.0001))) / texel) + 1.0, 0.0, 7.0);
      total = total + textureSampleLevel(source, source_sampler, panorama_uv(l), lod).rgb * nl;
      weight = weight + nl;
    }
  }
  return vec4<f32>(total / max(weight, 0.0001), 1.0);
}
