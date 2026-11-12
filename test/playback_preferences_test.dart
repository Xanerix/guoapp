import 'dart:ui';

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
      () => PlaybackPreferences.fromJson(const {'speed': 1.3}),
      throwsFormatException,
    );
  });

  test('video fit defaults to auto and round trips through json', () {
    expect(
      PlaybackPreferences.fromJson(const {'speed': 1}).videoFit,
      VideoFit.auto,
    );
    expect(
      PlaybackPreferences.fromJson(const {'videoFit': 'unknown'}).videoFit,
      VideoFit.auto,
    );
    const preferences = PlaybackPreferences(videoFit: VideoFit.ratio4x3);
    expect(
      PlaybackPreferences.fromJson(preferences.toJson()).videoFit,
      VideoFit.ratio4x3,
    );
  });

  test('video fit sizes a 16:9 video inside a 400x400 box', () {
    const box = Size(400, 400);
    const video = Size(1920, 1080);
    Size size(VideoFit fit) => fit.size(box, video, 2);
    expect(size(VideoFit.auto), const Size(400, 225));
    expect(size(VideoFit.stretch), box);
    expect(size(VideoFit.crop).width, closeTo(711.1, .1));
    expect(size(VideoFit.crop).height, 400);
    expect(size(VideoFit.fitWidth), const Size(400, 225));
    expect(size(VideoFit.fitHeight).width, closeTo(711.1, .1));
    expect(size(VideoFit.original), const Size(960, 540));
    expect(size(VideoFit.ratio4x3), const Size(400, 300));
    expect(size(VideoFit.ratio16x9), const Size(400, 225));
  });

  test('video fit sizes a 9:16 video on a tall phone screen', () {
    const box = Size(360, 800);
    const video = Size(1080, 1920);
    Size size(VideoFit fit) => fit.size(box, video, 3);
    expect(size(VideoFit.auto), const Size(360, 640));
    expect(size(VideoFit.crop), const Size(450, 800));
    expect(size(VideoFit.fitWidth), const Size(360, 640));
    expect(size(VideoFit.fitHeight), const Size(450, 800));
    expect(size(VideoFit.original), const Size(360, 640));
    expect(size(VideoFit.ratio4x3), const Size(360, 480));
    expect(size(VideoFit.ratio16x9), const Size(360, 640));
  });
}
