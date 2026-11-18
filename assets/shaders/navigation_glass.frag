#version 460 core
#include <flutter/runtime_effect.glsl>

uniform vec2 u_textureSize;
uniform vec2 u_glassCenter;
uniform float u_halfStraightWidth;
uniform float u_radius;
uniform float u_scale;
uniform sampler2D u_backdrop;

out vec4 fragColor;

void main() {
  vec2 position = FlutterFragCoord().xy;
  vec2 fromCenter = position - u_glassCenter;
  vec2 fromSpine = vec2(
    fromCenter.x - clamp(fromCenter.x, -u_halfStraightWidth, u_halfStraightWidth),
    fromCenter.y
  );
  float distanceToSpine = length(fromSpine);
  vec2 normal = fromSpine / max(distanceToSpine, 1.0);
  float edge = 1.0 - smoothstep(0.0, 15.0 * u_scale, u_radius - distanceToSpine);
  vec2 uv = (position - normal * (9.0 * u_scale * edge)) / u_textureSize;
  vec2 blur = vec2(2.0 * u_scale) / u_textureSize;

  fragColor = texture(u_backdrop, uv) * 0.36
    + texture(u_backdrop, uv + blur) * 0.16
    + texture(u_backdrop, uv - blur) * 0.16
    + texture(u_backdrop, uv + vec2(blur.x, -blur.y)) * 0.16
    + texture(u_backdrop, uv + vec2(-blur.x, blur.y)) * 0.16;
}
