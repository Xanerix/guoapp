import 'package:flutter_test/flutter_test.dart';
import 'package:duanju_app/playback_preferences.dart';

void main() {
  test('danmaku size and color survive a round trip', () {
    final restored = PlaybackPreferences.fromJson(
      const PlaybackPreferences(
        danmakuScale: .75,
        danmakuColor: 0xFFFFE066,
      ).toJson(),
    );
    expect(restored.danmakuScale, .75);
    expect(restored.danmakuColor, 0xFFFFE066);
  });

  test('unknown danmaku size and color fall back to defaults', () {
    final restored = PlaybackPreferences.fromJson({
      'danmakuScale': 9,
      'danmakuColor': 0xFF123456,
    });
    expect(restored.danmakuScale, 1);
    expect(restored.danmakuColor, 0xFFFFFFFF);
  });
}
