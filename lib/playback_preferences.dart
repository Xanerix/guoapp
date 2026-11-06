const playbackSpeeds = [.5, .75, 1.0, 1.25, 1.5, 2.0, 3.0];
const danmakuScales = [.75, 1.0, 1.25, 1.5];
const danmakuColors = [
  0xFFFFFFFF,
  0xFFFFE066,
  0xFF66E0FF,
  0xFFFF8FB8,
  0xFF8CF08C,
];

enum TelevisionDecoder { optimized, hardware, software }

class PlaybackPreferences {
  const PlaybackPreferences({
    this.speed = 1,
    this.quality = 0,
    this.autoAdvance = true,
    this.danmaku = true,
    this.danmakuScale = 1,
    this.danmakuColor = 0xFFFFFFFF,
    this.preload = true,
    this.televisionDecoder = TelevisionDecoder.optimized,
    this.preopen,
    this.doubleTapSeek = true,
    this.swipeLevels = true,
    this.autoPictureInPicture = false,
  });

  final double speed;
  final int quality;
  final bool autoAdvance;
  final bool danmaku;
  final double danmakuScale;
  final int danmakuColor;
  final bool preload;
  final TelevisionDecoder televisionDecoder;
  final bool? preopen;
  final bool doubleTapSeek;
  final bool swipeLevels;
  final bool autoPictureInPicture;

  bool preopens({required bool television}) => preopen ?? !television;

  PlaybackPreferences copyWith({
    double? speed,
    int? quality,
    bool? autoAdvance,
    bool? danmaku,
    double? danmakuScale,
    int? danmakuColor,
    bool? preload,
    TelevisionDecoder? televisionDecoder,
    bool? preopen,
    bool? doubleTapSeek,
    bool? swipeLevels,
    bool? autoPictureInPicture,
  }) => PlaybackPreferences(
    speed: speed ?? this.speed,
    quality: quality ?? this.quality,
    autoAdvance: autoAdvance ?? this.autoAdvance,
    danmaku: danmaku ?? this.danmaku,
    danmakuScale: danmakuScale ?? this.danmakuScale,
    danmakuColor: danmakuColor ?? this.danmakuColor,
    preload: preload ?? this.preload,
    televisionDecoder: televisionDecoder ?? this.televisionDecoder,
    preopen: preopen ?? this.preopen,
    doubleTapSeek: doubleTapSeek ?? this.doubleTapSeek,
    swipeLevels: swipeLevels ?? this.swipeLevels,
    autoPictureInPicture: autoPictureInPicture ?? this.autoPictureInPicture,
  );

  Map<String, dynamic> toJson() => {
    'speed': speed,
    'quality': quality,
    'autoAdvance': autoAdvance,
    'danmaku': danmaku,
    'danmakuScale': danmakuScale,
    'danmakuColor': danmakuColor,
    'preload': preload,
    'televisionDecoder': televisionDecoder.name,
    if (preopen != null) 'preopen': preopen,
    'doubleTapSeek': doubleTapSeek,
    'swipeLevels': swipeLevels,
    'autoPictureInPicture': autoPictureInPicture,
  };

  factory PlaybackPreferences.fromJson(Map<String, dynamic> value) {
    final speed = (value['speed'] as num? ?? 1).toDouble();
    final quality = value['quality'] as int? ?? 0;
    final autoAdvance = value['autoAdvance'] as bool? ?? true;
    final danmaku = value['danmaku'] as bool? ?? true;
    final preload = value['preload'] as bool? ?? true;
    final danmakuScale = (value['danmakuScale'] as num? ?? 1).toDouble();
    final danmakuColor = value['danmakuColor'] as int? ?? 0xFFFFFFFF;
    if (!playbackSpeeds.contains(speed) || quality < 0 || quality > 4320) {
      throw const FormatException('播放偏好无效');
    }
    return PlaybackPreferences(
      speed: speed,
      quality: quality,
      autoAdvance: autoAdvance,
      danmaku: danmaku,
      danmakuScale: danmakuScales.contains(danmakuScale) ? danmakuScale : 1,
      danmakuColor: danmakuColors.contains(danmakuColor)
          ? danmakuColor
          : 0xFFFFFFFF,
      preload: preload,
      televisionDecoder:
          TelevisionDecoder.values.asNameMap()[value['televisionDecoder']] ??
          TelevisionDecoder.optimized,
      preopen: value['preopen'] as bool?,
      doubleTapSeek: value['doubleTapSeek'] as bool? ?? true,
      swipeLevels: value['swipeLevels'] as bool? ?? true,
      autoPictureInPicture: value['autoPictureInPicture'] as bool? ?? false,
    );
  }
}
