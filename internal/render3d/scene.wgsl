// The whole scene's shaders: the sky behind, materials in their view modes, their shadows, the ground and the wires.

struct Frame {
  view_proj: mat4x4<f32>,
  inverse: mat4x4<f32>,
  light: mat4x4<f32>,
  eye: vec4<f32>,       // w: exposure
  sun_dir: vec4<f32>,   // w: 1 when shadows are on
  sun_color: vec4<f32>,
  sh: array<vec4<f32>, 9>,
  options: vec4<f32>,   // the view mode, the sky's rotation as cos and sin, how far the ground is below
  wire: vec4<f32>,      // w: how strongly the ground's grid shows
  grid: vec4<f32>,      // cell size, the distance it fades at, the shadow map's texel, whether the sky shows
  pixel: vec4<f32>,     // the size of a pixel in clip space, and the pixels of a line's unit of width
};

struct Material {
  base: vec4<f32>,
  emissive: vec4<f32>,   // w: normal scale
  factors: vec4<f32>,    // metallic, roughness, occlusion strength, alpha cutoff
  uv_sets: vec4<f32>,    // base, metal-rough, normal, occlusion
  flags: vec4<f32>,      // emissive's uv set, whether it has a normal map
};

@group(0) @binding(0) var<uniform> frame: Frame;
@group(0) @binding(1) var sky_map: texture_2d<f32>;
@group(0) @binding(2) var specular_map: texture_2d<f32>;
@group(0) @binding(3) var sky_sampler: sampler;
@group(0) @binding(4) var shadow_map: texture_depth_2d;
@group(0) @binding(5) var shadow_sampler: sampler_comparison;

@group(1) @binding(0) var<uniform> material: Material;
@group(1) @binding(1) var base_tex: texture_2d<f32>;
@group(1) @binding(2) var metal_rough_tex: texture_2d<f32>;
@group(1) @binding(3) var normal_tex: texture_2d<f32>;
@group(1) @binding(4) var occlusion_tex: texture_2d<f32>;
@group(1) @binding(5) var emissive_tex: texture_2d<f32>;
@group(1) @binding(6) var tex_sampler: sampler;

const PI: f32 = 3.14159265;
const SPECULAR_MIPS: f32 = 5.0;

const MODE_MATERIAL: f32 = 0.0;
const MODE_SOLID: f32 = 1.0;
const MODE_NORMALS: f32 = 2.0;
const MODE_UV: f32 = 3.0;

fn tonemap(c: vec3<f32>) -> vec3<f32> {
  // Narkowicz's fit of the ACES curve.
  let x = c * frame.eye.w * 0.6;
  let t = clamp((x * (2.51 * x + 0.03)) / (x * (2.43 * x + 0.59) + 0.14), vec3<f32>(0.0), vec3<f32>(1.0));
  return pow(t, vec3<f32>(1.0 / 2.2));
}

fn rotated(d: vec3<f32>) -> vec3<f32> {
  let c = frame.options.y;
  let s = frame.options.z;
  return vec3<f32>(c * d.x - s * d.z, d.y, s * d.x + c * d.z);
}

fn panorama_uv(d: vec3<f32>) -> vec2<f32> {
  let u = atan2(d.x, d.z) / (2.0 * PI) + 0.5;
  let v = 0.5 - asin(clamp(d.y, -1.0, 1.0)) / PI;
  return vec2<f32>(u, v);
}

fn diffuse_light(n: vec3<f32>) -> vec3<f32> {
  let d = rotated(n);
  let x = d.x;
  let y = d.y;
  let z = d.z;
  let c = frame.sh;
  return c[0].xyz * 0.282095
    + (c[1].xyz * y + c[2].xyz * z + c[3].xyz * x) * 0.488603
    + (c[4].xyz * x * y + c[5].xyz * y * z + c[7].xyz * x * z) * 1.092548
    + c[6].xyz * (0.315392 * (3.0 * z * z - 1.0))
    + c[8].xyz * (0.546274 * (x * x - y * y));
}

fn specular_light(r: vec3<f32>, roughness: f32) -> vec3<f32> {
  return textureSampleLevel(specular_map, sky_sampler, panorama_uv(rotated(r)), roughness * SPECULAR_MIPS).rgb;
}

// The surface's split-sum response, by Karis's fit, so that no lookup texture is needed.
fn env_response(f0: vec3<f32>, roughness: f32, nv: f32) -> vec3<f32> {
  let c0 = vec4<f32>(-1.0, -0.0275, -0.572, 0.022);
  let c1 = vec4<f32>(1.0, 0.0425, 1.04, -0.04);
  let r = roughness * c0 + c1;
  let a004 = min(r.x * r.x, exp2(-9.28 * nv)) * r.x + r.y;
  let ab = vec2<f32>(-1.04, 1.04) * a004 + r.zw;
  return f0 * ab.x + vec3<f32>(ab.y);
}

fn ggx(nh: f32, a: f32) -> f32 {
  let a2 = a * a;
  let d = nh * nh * (a2 - 1.0) + 1.0;
  return a2 / (PI * d * d);
}

fn smith(nv: f32, nl: f32, a: f32) -> f32 {
  let k = a * 0.5;
  return 0.5 / (nl * (nv * (1.0 - k) + k) + nv * (nl * (1.0 - k) + k));
}

fn in_shadow(world: vec3<f32>, n: vec3<f32>) -> f32 {
  if (frame.sun_dir.w < 0.5) {
    return 1.0;
  }
  let texel = frame.grid.z;
  let p = frame.light * vec4<f32>(world + n * texel * 2.0, 1.0);
  let uv = vec2<f32>(p.x * 0.5 + 0.5, 0.5 - p.y * 0.5);
  // Ground reached by a long shadow from a low sun can lie past the map's far end, and is behind what the map holds.
  let depth = min(p.z, 0.9999);
  // Ground reached by a long shadow from a low sun can lie past the map's far end, and is behind what the map holds.
  let depth = min(p.z, 0.9999);
  // Ground reached by a long shadow from a low sun can lie past the map's far end, and is behind what the map holds.
  let depth = min(p.z, 0.9999);
  // Ground reached by a long shadow from a low sun can lie past the map's far end, and is behind what the map holds.
  let depth = min(p.z, 0.9999);
  var lit = 0.0;
  for (var y = -1; y <= 1; y = y + 1) {
    for (var x = -1; x <= 1; x = x + 1) {
      let at = uv + vec2<f32>(f32(x), f32(y)) * 1.25 / 2048.0;
      lit = lit + textureSampleCompareLevel(shadow_map, shadow_sampler, at, depth);
    }
  }
  let inside = step(0.0, uv.x) * step(uv.x, 1.0) * step(0.0, uv.y) * step(uv.y, 1.0);
  return mix(1.0, lit / 9.0, inside);
}

struct SkyOut {
  @builtin(position) position: vec4<f32>,
  @location(0) ndc: vec2<f32>,
};

@vertex
fn vs_sky(@builtin(vertex_index) i: u32) -> SkyOut {
  let x = f32((i << 1u) & 2u) * 2.0 - 1.0;
  let y = f32(i & 2u) * 2.0 - 1.0;
  var o: SkyOut;
  o.position = vec4<f32>(x, y, 1.0, 1.0);
  o.ndc = vec2<f32>(x, y);
  return o;
}

@fragment
fn fs_sky(i: SkyOut) -> @location(0) vec4<f32> {
  if (frame.grid.w < 0.5) {
    let t = clamp(i.ndc.y * 0.5 + 0.5, 0.0, 1.0);
    return vec4<f32>(mix(vec3<f32>(0.075, 0.08, 0.09), vec3<f32>(0.16, 0.17, 0.19), t), 1.0);
  }
  let far = frame.inverse * vec4<f32>(i.ndc, 1.0, 1.0);
  let d = normalize(far.xyz / far.w - frame.eye.xyz);
  let c = textureSampleLevel(sky_map, sky_sampler, panorama_uv(rotated(d)), 0.0).rgb;
  return vec4<f32>(tonemap(c), 1.0);
}

struct Vertex {
  @location(0) position: vec3<f32>,
  @location(1) normal: vec3<f32>,
  @location(2) tangent: vec4<f32>,
  @location(3) uv: vec2<f32>,
  @location(4) uv2: vec2<f32>,
  @location(5) color: vec4<f32>,
};

struct Surface {
  @builtin(position) position: vec4<f32>,
  @location(0) world: vec3<f32>,
  @location(1) normal: vec3<f32>,
  @location(2) tangent: vec4<f32>,
  @location(3) uv: vec2<f32>,
  @location(4) uv2: vec2<f32>,
  @location(5) color: vec4<f32>,
};

@vertex
fn vs_surface(v: Vertex) -> Surface {
  var o: Surface;
  o.position = frame.view_proj * vec4<f32>(v.position, 1.0);
  o.world = v.position;
  o.normal = v.normal;
  o.tangent = v.tangent;
  o.uv = v.uv;
  o.uv2 = v.uv2;
  o.color = v.color;
  return o;
}

fn pick_uv(set: f32, i: Surface) -> vec2<f32> {
  if (set > 0.5) {
    return i.uv2;
  }
  return i.uv;
}

fn surface_normal(i: Surface, front: bool) -> vec3<f32> {
  var n = normalize(i.normal);
  if (!front) {
    n = -n;
  }
  if (material.flags.y < 0.5) {
    return n;
  }
  let uv = pick_uv(material.uv_sets.z, i);
  var t = i.tangent.xyz;
  var sign = i.tangent.w;
  if (sign == 0.0) {
    // The file gave no tangents: they come from how the texture runs across the surface on screen.
    let dpx = dpdx(i.world);
    let dpy = dpdy(i.world);
    let dux = dpdx(uv);
    let duy = dpdy(uv);
    t = dpx * duy.y - dpy * dux.y;
    sign = 1.0;
  }
  t = normalize(t - n * dot(n, t));
  let b = cross(n, t) * sign;
  var m = textureSample(normal_tex, tex_sampler, uv).xyz * 2.0 - 1.0;
  m = vec3<f32>(m.xy * material.emissive.w, m.z);
  return normalize(t * m.x + b * m.y + n * m.z);
}

fn shade(n: vec3<f32>, world: vec3<f32>, base: vec3<f32>, metallic: f32, roughness: f32, occlusion: f32) -> vec3<f32> {
  let v = normalize(frame.eye.xyz - world);
  let nv = max(dot(n, v), 0.001);
  let f0 = mix(vec3<f32>(0.04), base, metallic);
  let diffuse = base * (1.0 - metallic);
  let r = reflect(-v, n);
  // Horizon occlusion: a reflection pointing into the surface sees none of the sky.
  let horizon = clamp(1.0 + dot(r, n), 0.0, 1.0);
  var color = (diffuse * diffuse_light(n) + specular_light(r, roughness) * env_response(f0, roughness, nv) * horizon * horizon) * occlusion;

  let l = frame.sun_dir.xyz;
  let nl = dot(n, l);
  if (nl > 0.0 && frame.sun_color.w > 0.0) {
    let h = normalize(v + l);
    let a = max(roughness * roughness, 0.003);
    let nh = max(dot(n, h), 0.0);
    let vh = max(dot(v, h), 0.0);
    let fresnel = f0 + (vec3<f32>(1.0) - f0) * pow(1.0 - vh, 5.0);
    let spec = fresnel * ggx(nh, a) * smith(nv, nl, a);
    let light = frame.sun_color.xyz * frame.sun_color.w * in_shadow(world, n);
    color = color + (diffuse * (vec3<f32>(1.0) - fresnel) / PI + spec) * light * nl;
  }
  return color;
}

@fragment
fn fs_surface(i: Surface, @builtin(front_facing) front: bool) -> @location(0) vec4<f32> {
  let mode = frame.options.x;
  let base_uv = pick_uv(material.uv_sets.x, i);
  let base = textureSample(base_tex, tex_sampler, base_uv) * material.base * i.color;
  if (base.a < material.factors.w) {
    discard;
  }
  let n = surface_normal(i, front);
  if (mode == MODE_NORMALS) {
    return vec4<f32>(n * 0.5 + 0.5, 1.0);
  }
  if (mode == MODE_UV) {
    let cell = floor(base_uv * 16.0);
    let check = (cell.x + cell.y) % 2.0;
    let tone = mix(0.22, 0.86, abs(check));
    return vec4<f32>(vec3<f32>(tone) * vec3<f32>(fract(base_uv.x), fract(base_uv.y), 1.0) * 0.5 + vec3<f32>(tone) * 0.5, 1.0);
  }
  if (mode == MODE_SOLID) {
    return vec4<f32>(tonemap(shade(n, i.world, vec3<f32>(0.62), 0.0, 0.55, 1.0)), 1.0);
  }
  let mr = textureSample(metal_rough_tex, tex_sampler, pick_uv(material.uv_sets.y, i));
  let metallic = material.factors.x * mr.b;
  let roughness = clamp(material.factors.y * mr.g, 0.04, 1.0);
  let ao = textureSample(occlusion_tex, tex_sampler, pick_uv(material.uv_sets.w, i)).r;
  let occlusion = mix(1.0, ao, material.factors.z);
  let emissive = textureSample(emissive_tex, tex_sampler, pick_uv(material.flags.x, i)).rgb * material.emissive.xyz;
  return vec4<f32>(tonemap(shade(n, i.world, base.rgb, metallic, roughness, occlusion) + emissive), base.a);
}

struct ShadowOut {
  @builtin(position) position: vec4<f32>,
  @location(0) uv: vec2<f32>,
  @location(1) uv2: vec2<f32>,
};

@vertex
fn vs_shadow(v: Vertex) -> ShadowOut {
  var o: ShadowOut;
  o.position = frame.light * vec4<f32>(v.position, 1.0);
  o.uv = v.uv;
  o.uv2 = v.uv2;
  return o;
}

// Only for cut-out materials: their holes let the light through.
@fragment
fn fs_shadow(i: ShadowOut) -> @location(0) vec4<f32> {
  var uv = i.uv;
  if (material.uv_sets.x > 0.5) {
    uv = i.uv2;
  }
  if ((textureSample(base_tex, tex_sampler, uv) * material.base).a < material.factors.w) {
    discard;
  }
  return vec4<f32>(0.0);
}

struct GroundOut {
  @builtin(position) position: vec4<f32>,
  @location(0) world: vec3<f32>,
};

@vertex
fn vs_ground(@builtin(vertex_index) i: u32) -> GroundOut {
  let corners = array<vec2<f32>, 6>(vec2<f32>(-1.0, -1.0), vec2<f32>(1.0, -1.0), vec2<f32>(1.0, 1.0), vec2<f32>(-1.0, -1.0), vec2<f32>(1.0, 1.0), vec2<f32>(-1.0, 1.0));
  let c = corners[i];
  let reach = frame.grid.y;
  let world = vec3<f32>(frame.eye.x + c.x * reach, frame.options.w, frame.eye.z + c.y * reach);
  var o: GroundOut;
  o.position = frame.view_proj * vec4<f32>(world, 1.0);
  o.world = world;
  return o;
}

fn grid_lines(p: vec2<f32>, cell: f32) -> f32 {
  let q = p / cell;
  let w = fwidth(q);
  let d = abs(fract(q - 0.5) - 0.5) / max(w, vec2<f32>(0.0001));
  return 1.0 - min(min(d.x, d.y), 1.0);
}

@fragment
fn fs_ground(i: GroundOut) -> @location(0) vec4<f32> {
  let distance = length(i.world.xz - frame.eye.xz);
  let fade = clamp(1.0 - distance / frame.grid.y, 0.0, 1.0);
  let cell = frame.grid.x;
  let lines = max(grid_lines(i.world.xz, cell) * 0.35, grid_lines(i.world.xz, cell * 10.0) * 0.7);
  let dark = 1.0 - in_shadow(i.world, vec3<f32>(0.0, 1.0, 0.0));
  let shadow = dark * 0.5 * fade;
  let grid = lines * frame.wire.w * fade * fade;
  let alpha = 1.0 - (1.0 - shadow) * (1.0 - grid);
  if (alpha <= 0.002) {
    discard;
  }
  let color = (vec3<f32>(0.75, 0.78, 0.82) * grid) / max(alpha, 0.002);
  return vec4<f32>(color, alpha);
}

const LINE_CORE: f32 = 0.55;      // half the width of a line's core, in units of line width
const LINE_RIM: f32 = 1.55;       // and of the dark rim around it
const LINE_PULL: f32 = 0.0012;    // how far a line comes towards the eye off its surface, of the distance to it
const LINE_SHORT: f32 = 4.0;      // lines this short show at LINE_FAINT, so that a dense mesh stays a mesh and not a mass
const LINE_FULL: f32 = 32.0;      // and from this long on they are whole
const LINE_FAINT: f32 = 0.4;
const BONE_WIDTH: f32 = 0.7;      // the links between bones are this many times the width of a mesh line

struct LineOut {
  @builtin(position) position: vec4<f32>,
  @location(0) color: vec4<f32>,
  @location(1) across: f32,       // from the line's middle, in units of line width
};

// A line is a strip facing the eye, a fixed width on screen, its edges faded by the shader rather than left to the
// samples.
@vertex
fn vs_line(@builtin(vertex_index) index: u32, @location(0) start: vec3<f32>, @location(1) end: vec3<f32>, @location(2) color: vec4<f32>) -> LineOut {
  var o = strip(index, mix(start, frame.eye.xyz, LINE_PULL), mix(end, frame.eye.xyz, LINE_PULL), frame.pixel.z);
  o.color = vec4<f32>(color.rgb, color.a * o.color.a);
  return o;
}

@vertex
fn vs_bone(@builtin(vertex_index) index: u32, @location(0) start: vec3<f32>, @location(1) end: vec3<f32>, @location(2) color: vec4<f32>) -> LineOut {
  var o = strip(index, start, end, frame.pixel.z * BONE_WIDTH);
  o.color = color;
  return o;
}

// strip is the corner index of the line between two points, its colour's alpha saying how much a line this long shows.
fn strip(index: u32, start: vec3<f32>, end: vec3<f32>, width: f32) -> LineOut {
  var o: LineOut;
  var a = frame.view_proj * vec4<f32>(start, 1.0);
  var b = frame.view_proj * vec4<f32>(end, 1.0);
  if (a.z < 0.0 && b.z < 0.0) {
    return o;
  }
  // The part behind the eye has no place on screen.
  if (a.z < 0.0) {
    a = mix(a, b, a.z / (a.z - b.z));
  } else if (b.z < 0.0) {
    b = mix(b, a, b.z / (b.z - a.z));
  }
  let span = (b.xy / b.w - a.xy / a.w) / frame.pixel.xy;
  let pixels = length(span);
  if (pixels < 0.001) {
    return o;
  }
  let along = span / pixels;
  let beside = vec2<f32>(-along.y, along.x);
  let far_end = index == 1u || index == 2u || index == 4u;
  let side = select(-1.0, 1.0, index >= 2u && index != 3u);
  let reach = (LINE_RIM + 1.0) * width;
  let p = select(a, b, far_end);
  let shift = beside * side * reach + along * select(-0.5, 0.5, far_end) * width;
  o.position = vec4<f32>(p.xy + shift * frame.pixel.xy * p.w, p.zw);
  o.across = side * (LINE_RIM + 1.0);
  o.color = vec4<f32>(1.0, 1.0, 1.0, smoothstep(0.5, 2.0, pixels) * mix(LINE_FAINT, 1.0, smoothstep(LINE_SHORT, LINE_FULL, pixels)));
  return o;
}

@fragment
fn fs_line_rim(i: LineOut) -> @location(0) vec4<f32> {
  return vec4<f32>(0.0, 0.0, 0.0, 0.5 * i.color.a * (1.0 - smoothstep(LINE_RIM - 0.5, LINE_RIM + 0.5, abs(i.across))));
}

@fragment
fn fs_line_core(i: LineOut) -> @location(0) vec4<f32> {
  return vec4<f32>(i.color.rgb, i.color.a * (1.0 - smoothstep(LINE_CORE - 0.5, LINE_CORE + 0.5, abs(i.across))));
}

struct OverlayOut {
  @builtin(position) position: vec4<f32>,
  @location(0) color: vec4<f32>,
};

@vertex
fn vs_overlay(@location(0) position: vec3<f32>, @location(1) color: vec4<f32>, @location(2) shift: vec2<f32>) -> OverlayOut {
  var o: OverlayOut;
  let p = frame.view_proj * vec4<f32>(position, 1.0);
  o.position = vec4<f32>(p.xy + shift * frame.pixel.xy * p.w, p.zw);
  o.color = color;
  return o;
}

@fragment
fn fs_overlay(i: OverlayOut) -> @location(0) vec4<f32> {
  return i.color;
}
