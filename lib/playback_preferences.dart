import 'catalog_sort.dart';

const playbackSpeeds = [.5, .75, 1.0, 1.25, 1.5, 2.0, 3.0];
const danmakuColors = [
  0xFFFFFFFF,
  0xFFFFE066,
  0xFF66E0FF,
  0xFFFF8FB8,
  0xFF8CF08C,
];

enum TelevisionDecoder { optimized, hardware, software }

enum DanmakuCoverage {
  quarter(.25, '顶部四分之一'),
  half(.5, '顶部一半'),
  full(1, '全部');

  const DanmakuCoverage(this.fraction, this.label);

  final double fraction;
  final String label;
}

const danmakuScaleMin = .6;
const danmakuScaleMax = 2.0;

const danmakuStrokeMin = 0.0;
const danmakuStrokeMax = 6.0;

const danmakuShadowMin = 0.0;
const danmakuShadowMax = 12.0;

const danmakuWordLength = 40;
const danmakuWordLimit = 60;

class PlaybackPreferences {
  const PlaybackPreferences({
    this.speed = 1,
    this.quality = 0,
    this.autoAdvance = true,
    this.danmaku = true,
    this.danmakuScale = 1,
    this.danmakuColor = 0xFFFFFFFF,
    this.danmakuOpacity = 1,
    this.danmakuBold = true,
    this.danmakuStroke = 0,
    this.danmakuShadow = 3,
    this.danmakuCoverage = DanmakuCoverage.half,
    this.danmakuWords = const [],
    this.preload = true,
    this.televisionDecoder = TelevisionDecoder.optimized,
    this.preopen,
    this.doubleTapSeek = true,
    this.swipeLevels = true,
    this.autoHideControls = true,
    this.hideSystemBars = true,
    this.autoPictureInPicture = false,
  });

  final double speed;
  final int quality;
  final bool autoAdvance;
  final bool danmaku;
  final double danmakuScale;
  final int danmakuColor;
  final double danmakuOpacity;
  final bool danmakuBold;
  final double danmakuStroke;
  final double danmakuShadow;
  final DanmakuCoverage danmakuCoverage;
  final List<String> danmakuWords;
  final bool preload;
  final TelevisionDecoder televisionDecoder;
  final bool? preopen;
  final bool doubleTapSeek;
  final bool swipeLevels;
  final bool autoHideControls;
  final bool hideSystemBars;
  final bool autoPictureInPicture;

  bool preopens({required bool television}) => preopen ?? !television;

  PlaybackPreferences copyWith({
    double? speed,
    int? quality,
    bool? autoAdvance,
    bool? danmaku,
    double? danmakuScale,
    int? danmakuColor,
    double? danmakuOpacity,
    bool? danmakuBold,
    double? danmakuStroke,
    double? danmakuShadow,
    DanmakuCoverage? danmakuCoverage,
    List<String>? danmakuWords,
    bool? preload,
    TelevisionDecoder? televisionDecoder,
    bool? preopen,
    bool? doubleTapSeek,
    bool? swipeLevels,
    bool? autoHideControls,
    bool? hideSystemBars,
    bool? autoPictureInPicture,
  }) => PlaybackPreferences(
    speed: speed ?? this.speed,
    quality: quality ?? this.quality,
    autoAdvance: autoAdvance ?? this.autoAdvance,
    danmaku: danmaku ?? this.danmaku,
    danmakuScale: danmakuScale ?? this.danmakuScale,
    danmakuColor: danmakuColor ?? this.danmakuColor,
    danmakuOpacity: danmakuOpacity ?? this.danmakuOpacity,
    danmakuBold: danmakuBold ?? this.danmakuBold,
    danmakuStroke: danmakuStroke ?? this.danmakuStroke,
    danmakuShadow: danmakuShadow ?? this.danmakuShadow,
    danmakuCoverage: danmakuCoverage ?? this.danmakuCoverage,
    danmakuWords: danmakuWords ?? this.danmakuWords,
    preload: preload ?? this.preload,
    televisionDecoder: televisionDecoder ?? this.televisionDecoder,
    preopen: preopen ?? this.preopen,
    doubleTapSeek: doubleTapSeek ?? this.doubleTapSeek,
    swipeLevels: swipeLevels ?? this.swipeLevels,
    autoHideControls: autoHideControls ?? this.autoHideControls,
    hideSystemBars: hideSystemBars ?? this.hideSystemBars,
    autoPictureInPicture: autoPictureInPicture ?? this.autoPictureInPicture,
  );

  Map<String, dynamic> toJson() => {
    'speed': speed,
    'quality': quality,
    'autoAdvance': autoAdvance,
    'danmaku': danmaku,
    'danmakuScale': danmakuScale,
    'danmakuColor': danmakuColor,
    'danmakuOpacity': danmakuOpacity,
    'danmakuBold': danmakuBold,
    'danmakuStroke': danmakuStroke,
    'danmakuShadow': danmakuShadow,
    'danmakuCoverage': danmakuCoverage.name,
    'danmakuWords': danmakuWords,
    'preload': preload,
    'televisionDecoder': televisionDecoder.name,
    if (preopen != null) 'preopen': preopen,
    'doubleTapSeek': doubleTapSeek,
    'swipeLevels': swipeLevels,
    'autoHideControls': autoHideControls,
    'hideSystemBars': hideSystemBars,
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
      danmakuScale: danmakuScale.clamp(danmakuScaleMin, danmakuScaleMax),
      danmakuColor: danmakuColors.contains(danmakuColor)
          ? danmakuColor
          : 0xFFFFFFFF,
      danmakuOpacity: (value['danmakuOpacity'] as num? ?? 1).toDouble().clamp(
        0.1,
        1.0,
      ),
      danmakuBold: value['danmakuBold'] as bool? ?? true,
      danmakuStroke: (value['danmakuStroke'] as num? ?? 0).toDouble().clamp(
        danmakuStrokeMin,
        danmakuStrokeMax,
      ),
      danmakuShadow: (value['danmakuShadow'] as num? ?? 3).toDouble().clamp(
        danmakuShadowMin,
        danmakuShadowMax,
      ),
      danmakuCoverage:
          DanmakuCoverage.values.asNameMap()[value['danmakuCoverage']] ??
          DanmakuCoverage.half,
      danmakuWords: normalizeDanmakuWords(value['danmakuWords']),
      preload: preload,
      televisionDecoder:
          TelevisionDecoder.values.asNameMap()[value['televisionDecoder']] ??
          TelevisionDecoder.optimized,
      preopen: value['preopen'] as bool?,
      doubleTapSeek: value['doubleTapSeek'] as bool? ?? true,
      swipeLevels: value['swipeLevels'] as bool? ?? true,
      autoHideControls: value['autoHideControls'] as bool? ?? true,
      hideSystemBars: value['hideSystemBars'] as bool? ?? true,
      autoPictureInPicture: value['autoPictureInPicture'] as bool? ?? false,
    );
  }
}

List<String> normalizeDanmakuWords(Object? value) {
  if (value is! List) return const [];
  final words = <String>[];
  final keys = <String>{};
  for (final entry in value) {
    if (entry is! String) continue;
    final word = entry.trim();
    if (word.length > danmakuWordLength) continue;
    final key = normalizedSearchText(word);
    if (key.isEmpty || !keys.add(key)) continue;
    words.add(word);
    if (words.length >= danmakuWordLimit) break;
  }
  return List.unmodifiable(words);
}
