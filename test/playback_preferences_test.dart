import 'package:duanju_app/playback_preferences.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('old payloads fall back to the danmaku defaults', () {
    final preferences = PlaybackPreferences.fromJson(const {
      'speed': 1.5,
      'quality': 720,
    });
    expect(preferences.speed, 1.5);
    expect(preferences.quality, 720);
    expect(preferences.danmakuScale, 1);
    expect(preferences.danmakuOpacity, 1);
    expect(preferences.danmakuBold, isTrue);
    expect(preferences.danmakuStroke, 0);
    expect(preferences.danmakuShadow, 3);
    expect(preferences.danmakuCoverage, DanmakuCoverage.half);
    expect(preferences.danmakuWords, isEmpty);
  });

  test('danmaku values round trip through json', () {
    const preferences = PlaybackPreferences(
      danmakuScale: 1.4,
      danmakuColor: 0xFFFFE066,
      danmakuOpacity: .6,
      danmakuBold: false,
      danmakuStroke: 2,
      danmakuShadow: 6,
      danmakuCoverage: DanmakuCoverage.full,
      danmakuWords: ['剧透', '广告'],
    );
    final restored = PlaybackPreferences.fromJson(preferences.toJson());
    expect(restored.toJson(), preferences.toJson());
    expect(restored.danmakuColor, 0xFFFFE066);
    expect(restored.danmakuCoverage, DanmakuCoverage.full);
    expect(restored.danmakuWords, ['剧透', '广告']);
  });

  test('out of range danmaku values are clamped', () {
    final preferences = PlaybackPreferences.fromJson(const {
      'danmakuScale': 9,
      'danmakuOpacity': 0,
      'danmakuStroke': -4,
      'danmakuShadow': 99,
      'danmakuCoverage': 'unknown',
      'danmakuColor': 0xFF123456,
    });
    expect(preferences.danmakuScale, danmakuScaleMax);
    expect(preferences.danmakuOpacity, .1);
    expect(preferences.danmakuStroke, danmakuStrokeMin);
    expect(preferences.danmakuShadow, danmakuShadowMax);
    expect(preferences.danmakuCoverage, DanmakuCoverage.half);
    expect(preferences.danmakuColor, 0xFFFFFFFF);
  });

  test('blocked words are trimmed, deduplicated and capped', () {
    expect(normalizeDanmakuWords(null), isEmpty);
    expect(normalizeDanmakuWords('剧透'), isEmpty);
    expect(normalizeDanmakuWords(const [' 剧透 ', '', '剧透', 12]), ['剧透']);
    expect(normalizeDanmakuWords(const ['SB', 'ｓｂ', '!!']), ['SB']);

    final many = [for (var i = 0; i < danmakuWordLimit + 5; i++) 'w$i'];
    expect(normalizeDanmakuWords(many), hasLength(danmakuWordLimit));

    final long = 'a' * (danmakuWordLength + 1);
    expect(normalizeDanmakuWords([long]), isEmpty);
  });

  test('invalid speed still rejects the payload', () {
    expect(
      () => PlaybackPreferences.fromJson(const {'speed': 1.1}),
      throwsFormatException,
    );
  });
}
