import 'package:flutter/painting.dart';

import 'playback_preferences.dart';

class DanmakuStyle {
  const DanmakuStyle({
    this.scale = 1,
    this.color = const Color(0xFFFFFFFF),
    this.opacity = 1,
    this.bold = true,
    this.stroke = 0,
    this.shadow = 3,
    this.coverage = DanmakuCoverage.half,
  });

  factory DanmakuStyle.of(PlaybackPreferences preferences) => DanmakuStyle(
    scale: preferences.danmakuScale,
    color: Color(preferences.danmakuColor),
    opacity: preferences.danmakuOpacity,
    bold: preferences.danmakuBold,
    stroke: preferences.danmakuStroke,
    shadow: preferences.danmakuShadow,
    coverage: preferences.danmakuCoverage,
  );

  final double scale;
  final Color color;
  final double opacity;
  final bool bold;
  final double stroke;
  final double shadow;
  final DanmakuCoverage coverage;

  bool get hasStroke => stroke > 0;
  bool get hasShadow => shadow > 0;

  @override
  bool operator ==(Object other) =>
      other is DanmakuStyle &&
      other.scale == scale &&
      other.color == color &&
      other.opacity == opacity &&
      other.bold == bold &&
      other.stroke == stroke &&
      other.shadow == shadow &&
      other.coverage == coverage;

  @override
  int get hashCode =>
      Object.hash(scale, color, opacity, bold, stroke, shadow, coverage);
}
