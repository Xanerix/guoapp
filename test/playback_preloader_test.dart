import 'dart:async';

import 'package:duanju_app/models.dart';
import 'package:duanju_app/playback_preloader.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';

class _PreloadRepository extends FakeRepository {
  final released = <String>[];
  var calls = 0;
  Completer<PlaybackPlan?>? gate;
  bool emptyUrl = false;

  PlaybackPlan plan(int number) => PlaybackPlan(
    url: emptyUrl ? '' : 'https://media.test/$number.m3u8',
    session: 'session-$number',
    routeIndex: 0,
    routeCount: 1,
    quality: 1080,
    qualities: const [1080],
  );

  @override
  Future<PlaybackPlan?> preload(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool online = false,
  }) async {
    calls++;
    final pending = gate;
    if (pending != null) return pending.future;
    return plan(calls);
  }

  @override
  Future<void> release(String session) async => released.add(session);

  @override
  Future<void> cancelPreload() async {}
}

void main() {
  final show = drama('show', title: '测试剧');
  final episode = Episode({'currentEpisode': 2}, 2);

  /// prepare 内有 600ms 防抖，等它真正发起解析。
  Future<void> afterDebounce() =>
      Future<void>.delayed(const Duration(milliseconds: 700));

  test('上游会话票据失效时丢弃预加载结果并释放会话', () async {
    final repository = _PreloadRepository();
    final preloader = PlaybackPreloader(repository);
    addTearDown(preloader.dispose);

    var current = true;
    repository.gate = Completer<PlaybackPlan?>();
    preloader.prepare(show, episode, isCurrent: () => current);
    await afterDebounce();
    expect(repository.calls, 1);

    // 模拟切集：旧会话的预加载结果晚返回。
    current = false;
    repository.gate!.complete(repository.plan(99));
    await pumpEventQueue();

    expect(preloader.ready, isFalse);
    expect(preloader.take(show, episode), isNull);
    expect(repository.released, contains('session-99'));
  });

  test('pause 作废在途预加载，晚返回结果不会残留', () async {
    final repository = _PreloadRepository();
    final preloader = PlaybackPreloader(repository);
    addTearDown(preloader.dispose);

    repository.gate = Completer<PlaybackPlan?>();
    preloader.prepare(show, episode);
    await afterDebounce();
    expect(repository.calls, 1);

    preloader.pause();
    repository.gate!.complete(repository.plan(42));
    await pumpEventQueue();

    expect(preloader.ready, isFalse);
    expect(preloader.take(show, episode), isNull);
    expect(repository.released, contains('session-42'));
  });

  test('超过有效期的结果不会被取用，且会话被释放', () async {
    final repository = _PreloadRepository();
    var now = DateTime(2026, 1, 1, 12);
    final preloader = PlaybackPreloader(repository, now: () => now);
    addTearDown(preloader.dispose);

    preloader.prepare(show, episode);
    await afterDebounce();
    expect(preloader.ready, isTrue);

    now = now.add(const Duration(minutes: 3));
    expect(preloader.ready, isFalse);
    expect(preloader.take(show, episode), isNull);
    expect(repository.released, isNotEmpty);
  });

  test('指纹不匹配（换集/换清晰度）的结果不会被取用', () async {
    final repository = _PreloadRepository();
    final preloader = PlaybackPreloader(repository);
    addTearDown(preloader.dispose);

    preloader.prepare(show, episode);
    await afterDebounce();
    expect(preloader.ready, isTrue);

    expect(preloader.take(show, episode, quality: 720), isNull);
    expect(preloader.take(drama('other'), episode), isNull);
    expect(repository.released, isNotEmpty);
  });

  test('空地址的预加载结果被拒绝，不会交给待机播放器', () async {
    final repository = _PreloadRepository()..emptyUrl = true;
    final preloader = PlaybackPreloader(repository);
    addTearDown(preloader.dispose);

    preloader.prepare(show, episode);
    await afterDebounce();

    expect(preloader.ready, isFalse);
    // take() 会 clear 掉错误文案，所以先验证失败提示再取结果。
    expect(preloader.status, contains('暂不可用'));
    expect(preloader.take(show, episode), isNull);
    expect(repository.released, isNotEmpty);
  });
}
