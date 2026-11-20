import 'package:duanju_app/models.dart';
import 'package:duanju_app/playback_session.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';

Episode _episode(String? title, {String? mediaType, int number = 1}) =>
    Episode({'title': ?title, 'mediaType': ?mediaType}, number);

void main() {
  test('numbered titles are not names', () {
    for (final title in ['第1集', '第 12 集', '第3章', '01', 'EP05', 'e7', '第2话']) {
      expect(_episode(title).named, isFalse, reason: title);
    }
    expect(_episode(null, number: 4).label, '第 4 集');
  });

  test('real titles are names', () {
    final episode = _episode(' 第1章 雨夜耳语 ');
    expect(episode.named, isTrue);
    expect(episode.label, '第1章 雨夜耳语');
    expect(_episode('正片').named, isTrue);
  });

  test('title list needs most episodes named', () {
    final numbered = [for (var i = 1; i <= 3; i++) _episode('第$i集')];
    expect(episodesNamed([...numbered, _episode('大结局')]), isFalse);
    expect(
      episodesNamed([_episode('开场'), _episode('入睡'), numbered.first]),
      isTrue,
    );
    expect(episodesNamed(const []), isFalse);
  });

  test('declared audio', () {
    expect(_episode('a', mediaType: 'audio').audio, isTrue);
    expect(_episode('a', mediaType: 'video').audio, isFalse);
    expect(_episode('a').audio, isFalse);
  });

  test('audio only tracks', () {
    final auto = [VideoTrack.auto(), VideoTrack.no()];
    final sound = [
      AudioTrack.auto(),
      AudioTrack.no(),
      AudioTrack('1', null, null),
    ];
    expect(audioOnly(Tracks(video: auto, audio: sound)), isTrue);
    expect(
      audioOnly(
        Tracks(
          video: [...auto, VideoTrack('1', null, null, albumart: true)],
          audio: sound,
        ),
      ),
      isTrue,
    );
    expect(
      audioOnly(
        Tracks(video: [...auto, VideoTrack('1', null, null)], audio: sound),
      ),
      isFalse,
    );
    expect(
      audioOnly(
        Tracks(video: auto, audio: [AudioTrack.auto(), AudioTrack.no()]),
      ),
      isFalse,
    );
  });
}
