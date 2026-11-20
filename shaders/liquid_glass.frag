#include <flutter/runtime_effect.glsl>

uniform vec2 u_size;
uniform float u_radius;
uniform float u_refraction;
uniform float u_aberration;
uniform sampler2D u_texture;

out vec4 frag_color;

vec4 sample_background(vec2 position) {
  vec2 uv = clamp(position / u_size, 0.5 / u_size, 1.0 - 0.5 / u_size);
#ifdef IMPELLER_TARGET_OPENGLES
  uv.y = 1.0 - uv.y;
#endif
  return texture(u_texture, uv);
}

void main() {
  vec2 position = FlutterFragCoord().xy;
  vec2 half_size = u_size * 0.5;
  float radius = min(u_radius, min(half_size.x, half_size.y));
  vec2 centered = position - half_size;
  vec2 q = abs(centered) - half_size + radius;
  vec2 corner = max(q, vec2(0.0));
  float distance = min(max(q.x, q.y), 0.0) + length(corner) - radius;
  float band = max(1.0, min(min(half_size.x, half_size.y), max(radius, u_refraction * 2.0)));
  float edge = 1.0 - smoothstep(0.0, band, max(-distance, 0.0));
  if (edge < 0.001) {
    frag_color = sample_background(position);
    return;
  }
  vec2 normal;
  if (length(corner) > 0.0001) {
    normal = normalize(corner) * sign(centered);
  } else {
    normal = q.x > q.y ? vec2(sign(centered.x), 0.0) : vec2(0.0, sign(centered.y));
  }
  vec2 bent = position - normal * u_refraction * edge * edge;
  vec2 separation = normal * u_aberration * edge;
  vec4 red = sample_background(bent + separation);
  vec4 green = sample_background(bent);
  vec4 blue = sample_background(bent - separation);
  frag_color = vec4(min(vec3(red.r, green.g, blue.b), vec3(green.a)), green.a);
}
