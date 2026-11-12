import 'dart:async';
import 'dart:ui';

import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/playback_preferences.dart';
import 'package:duanju_app/playback_recovery.dart';
import 'package:duanju_app/playback_session.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:media_kit/media_kit.dart';

import 'support/fake_player.dart';
import 'support/fakes.dart';

class _PlaybackRepository extends FakeRepository {
  final resolved = <({int episode, int quality})>[];
  final forceResolves = <bool>[];
  final released = <String>[];
  final preloaded = <int>[];
  var fallbacks = 0;
  var resolveFailure = '';
  var _sessions = 0;

  /// Go 取流监听的模拟代次；修改它可验证「旧会话不走失效 fallback」。
  int streamEpoch = 0;
  bool streamAlive = true;
  int planEpoch = 0;

  @override
  Future<PlaybackStreamStatus> playbackStatus() async =>
      PlaybackStreamStatus(alive: streamAlive, epoch: streamEpoch);

  /// 让下一次站源解析停在 gate 上，用于模拟挂起与「旧任务晚返回」。
  bool hangNextResolve = false;
  final _resolveWaiters = <Completer<PlaybackPlan>>[];

  void releaseResolve() {
    for (final waiter in _resolveWaiters) {
      if (!waiter.isCompleted) waiter.complete(_plan());
    }
    _resolveWaiters.clear();
  }

  PlaybackPlan _plan({int route = 0}) {
    _sessions++;
    return PlaybackPlan(
      url: 'https://media.test/$_sessions.m3u8',
      session: 'session-$_sessions',
      routeIndex: route,
      routeCount: 2,
      quality: 1080,
      qualities: const [1080, 720],
      streamEpoch: planEpoch,
    );
  }

  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool force = false,
  }) async {
    resolved.add((episode: episode.number, quality: quality));
    forceResolves.add(force);
    if (resolveFailure.isNotEmpty) throw AppFailure(resolveFailure);
    if (hangNextResolve) {
      hangNextResolve = false;
      final waiter = Completer<PlaybackPlan>();
      _resolveWaiters.add(waiter);
      return waiter.future;
    }
    return _plan();
  }

  @override
  Future<PlaybackPlan> fallback(PlaybackPlan current) async {
    fallbacks++;
    return _plan(route: current.routeIndex + 1);
  }

  @override
  Future<PlaybackPlan?> preload(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool online = false,
  }) async {
    preloaded.add(episode.number);
    return _plan();
  }

  @override
  Future<void> release(String session) async => released.add(session);
}

final _show = drama('show', title: '测试剧');

Future<(PlaybackSession, FakePlayer)> _open(
  _PlaybackRepository repository,
  LocalStore store, {
  int index = 0,
  double position = 0,
  FakePlayer? standby,
  FakePlayer? seededPlayer,
  bool autoplay = true,
  FakePlayer Function()? respawn,
  void Function(Player)? onRetired,
  Duration swapStopTimeout = const Duration(seconds: 2),
  Duration intentStopTimeout = const Duration(seconds: 6),
  Duration resolveTimeout = const Duration(seconds: 20),
  Duration openTimeout = const Duration(seconds: 12),
  Duration playTimeout = const Duration(seconds: 8),
  Duration openBudget = const Duration(seconds: 25),
  Duration bufferingBudget = const Duration(seconds: 25),
  Duration standbyOpenTimeout = const Duration(seconds: 12),
  Duration surfaceCoverTimeout = const Duration(seconds: 3),
  Duration surfaceHardTimeout = const Duration(seconds: 5),
  int recoveryMaxAttempts = PlaybackRecovery.defaultMaxAttempts,
  Duration recoveryWindow = PlaybackRecovery.defaultWindow,
  Duration recoveryBackoff = const Duration(seconds: 1),
}) async {
  final player = seededPlayer ?? FakePlayer();
  final session = PlaybackSession(
    repository: repository,
    store: store,
    detail: dramaDetail(_show, count: 3),
    player: player,
    standby: standby,
    respawn: respawn,
    onRetired: onRetired,
    index: index,
    position: position,
    autoplay: autoplay,
    swapStopTimeout: swapStopTimeout,
    intentStopTimeout: intentStopTimeout,
    resolveTimeout: resolveTimeout,
    openTimeout: openTimeout,
    playTimeout: playTimeout,
    openBudget: openBudget,
    bufferingBudget: bufferingBudget,
    standbyOpenTimeout: standbyOpenTimeout,
    surfaceCoverTimeout: surfaceCoverTimeout,
    surfaceHardTimeout: surfaceHardTimeout,
    recoveryMaxAttempts: recoveryMaxAttempts,
    recoveryWindow: recoveryWindow,
    recoveryBackoff: recoveryBackoff,
  );
  addTearDown(() {
    if (_closed.add(session)) session.dispose();
  });
  await pumpEventQueue();
  return (session, player);
}

final _closed = <PlaybackSession>{};

void _close(PlaybackSession session) {
  _closed.add(session);
  session.dispose();
}

Duration _seconds(num value) => Duration(milliseconds: (value * 1000).round());

Future<void> _settle(FakePlayer player) async {
  player.state = player.state.copyWith(buffer: _seconds(25));
  player.progress(_seconds(5), duration: _seconds(100));
  await Future<void>.delayed(const Duration(milliseconds: 700));
  await pumpEventQueue();
}

/// 轮询等待异步恢复链走完，避免用固定 sleep 猜时序。
Future<bool> _waitFor(
  bool Function() condition, {
  Duration timeout = const Duration(seconds: 6),
}) async {
  final deadline = DateTime.now().add(timeout);
  while (DateTime.now().isBefore(deadline)) {
    if (condition()) return true;
    await Future<void>.delayed(const Duration(milliseconds: 100));
  }
  return condition();
}

void main() {
  test('benign player diagnostics do not count as failures', () {
    expect(
      isBenignPlayerError('Failed to allocate AVHWDeviceContext.'),
      isTrue,
    );
    expect(
      isBenignPlayerError(
        'Could not open/initialize audio device -> no sound.',
      ),
      isTrue,
    );
    expect(
      isBenignPlayerError('Failed to open https://example.test/a.m3u8'),
      isFalse,
    );
    expect(isBenignPlayerError(''), isFalse);
  });

  test('blocked words saved elsewhere reach the playing session', () async {
    final store = await openStore();
    final (session, _) = await _open(_PlaybackRepository(), store);

    await store.setPlaybackPreferences(
      store.playbackPreferences.copyWith(danmakuWords: const ['剧透']),
    );
    expect(session.danmaku.blockedWords, ['剧透']);
  });

  test('opens the episode at the resume position and saves progress', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(
      repository,
      store,
      index: 1,
      position: 30,
    );

    expect(session.ready, isTrue);
    expect(session.episode.number, 2);
    expect(repository.resolved.single, (episode: 2, quality: 0));
    expect(player.opened.single.uri, 'https://media.test/1.m3u8');
    expect(player.opened.single.start, _seconds(30));
    expect(player.rates, [1.0]);
    expect(session.qualityLabel, '自动（1080P）');

    player.progress(_seconds(40), duration: _seconds(100));
    await session.saveProgress();
    final watch = store.watched(_show.id)!;
    expect(watch.episode, 2);
    expect(watch.position, 40);
    expect(watch.duration, 100);
  });

  test('a finished episode moves on to the next one', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);

    player.progress(_seconds(99.5), duration: _seconds(100));
    player.finish();
    await pumpEventQueue();

    expect(session.index, 1);
    expect(session.ready, isTrue);
    expect(player.opened, hasLength(2));
    expect(player.opened.last.start, isNull);
    expect(repository.released, contains('session-1'));
    expect(store.watched(_show.id)!.episode, 1);
  });

  test('the last episode stops at its end instead of looping', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store, index: 2);

    player.progress(_seconds(99.5), duration: _seconds(100));
    player.finish();
    await pumpEventQueue();

    expect(session.index, 2);
    expect(player.opened, hasLength(1));
    expect(store.watched(_show.id)!.episode, 3);
  });

  test('a player error switches to the other route at the same spot', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);

    player.progress(_seconds(20), duration: _seconds(100));
    player.fail('connection reset by https://media.test/1.m3u8?token=x');
    await Future<void>.delayed(const Duration(milliseconds: 1000));
    await pumpEventQueue();

    expect(repository.fallbacks, 1);
    expect(repository.resolved, hasLength(1));
    expect(player.opened, hasLength(2));
    expect(player.opened.last.start, _seconds(20));
    expect(session.ready, isTrue);
    expect(repository.released, contains('session-1'));
  });

  test('a quality change is saved and reloads where it was', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);

    player.progress(_seconds(15), duration: _seconds(100));
    await session.setQuality(720);

    expect(store.playbackPreferences.quality, 720);
    expect(repository.resolved.last, (episode: 1, quality: 720));
    expect(player.opened.last.start, _seconds(15));
    expect(session.qualityLabel, '720P');
  });

  test('speed changes are saved and applied unless boosting', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);

    await session.setSpeed(1.5);
    expect(store.playbackPreferences.speed, 1.5);
    expect(player.rates.last, 1.5);

    session.beginBoost();
    await Future<void>.delayed(const Duration(milliseconds: 400));
    expect(session.boosting, isTrue);
    expect(player.rates.last, 2);
    expect(session.endBoost(), isTrue);
    await pumpEventQueue();
    expect(player.rates.last, 1.5);
    expect(session.endBoost(), isFalse);
  });

  test('a volume change while paused flushes audio before resuming', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);

    player.progress(_seconds(12), duration: _seconds(100));
    session.togglePlayback();
    await pumpEventQueue();
    expect(player.state.playing, isFalse);

    expect(session.toggleMute(), 0);
    session.togglePlayback();
    await pumpEventQueue();
    expect(player.seeks, [_seconds(12)]);
    expect(player.state.playing, isTrue);

    session.togglePlayback();
    session.togglePlayback();
    await pumpEventQueue();
    expect(player.seeks, hasLength(1));
  });

  test('a failed resolve shows the reason and retry loads again', () async {
    final repository = _PlaybackRepository()..resolveFailure = '站源暂时不可用';
    final store = await openStore();
    final (session, player) = await _open(repository, store);

    expect(session.ready, isFalse);
    expect(session.error, '站源暂时不可用');
    expect(player.opened, isEmpty);

    repository.resolveFailure = '';
    await session.retry();
    expect(session.error, isNull);
    expect(session.ready, isTrue);
    expect(player.opened, hasLength(1));
  });

  test('switching users pauses and stops recording progress', () async {
    final repository = _PlaybackRepository();
    final store = await openStore(viewerPreferences);
    final (session, player) = await _open(repository, store);

    player.progress(_seconds(30), duration: _seconds(100));
    await store.switchProfile('viewer');
    await pumpEventQueue();

    expect(player.state.playing, isFalse);
    await session.saveProgress();
    expect(store.watched(_show.id), isNull);
    expect(() => session.setSpeed(2), throwsStateError);
  });

  test('closing saves progress and releases the session and player', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);

    player.progress(_seconds(50), duration: _seconds(100));
    _close(session);
    await pumpEventQueue();

    expect(store.watched(_show.id)!.position, 50);
    expect(repository.released, contains('session-1'));
    expect(player.disposed, isTrue);
  });

  test('video size sets the aspect ratio', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);

    player.resize(1920, 1080);
    expect(session.aspectRatio, closeTo(16 / 9, .001));
    expect(
      videoDisplaySize(const VideoParams(w: 1920, h: 1080, rotate: 90)),
      const Size(1080, 1920),
    );
  });

  test('a paused preopen records nothing until it starts', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(
      repository,
      store,
      position: 30,
      autoplay: false,
    );

    expect(player.opened.single.start, _seconds(30));
    expect(player.state.playing, isFalse);
    player.progress(_seconds(31), duration: _seconds(100));
    await session.saveProgress();
    expect(store.watched(_show.id), isNull);

    session.start(0, position: 30);
    await pumpEventQueue();
    expect(player.state.playing, isTrue);
    expect(player.opened, hasLength(1));
    await session.saveProgress();
    expect(store.watched(_show.id)!.position, 31);
  });

  test(
    'starting a different episode than the preopened one loads it',
    () async {
      final repository = _PlaybackRepository();
      final store = await openStore();
      final (session, player) = await _open(
        repository,
        store,
        position: 30,
        autoplay: false,
      );

      session.start(2);
      await pumpEventQueue();

      expect(session.index, 2);
      expect(repository.resolved.last.episode, 3);
      expect(player.opened.last.start, isNull);
      expect(player.state.playing, isTrue);
    },
  );

  test('the next episode waits paused in the standby player', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final standby = FakePlayer();
    final (session, player) = await _open(repository, store, standby: standby);

    await _settle(player);

    expect(repository.preloaded, [2]);
    expect(session.standbyIndex, 1);
    expect(identical(session.standby, standby), isTrue);
    expect(standby.opened.single.uri, 'https://media.test/2.m3u8');
    expect(standby.state.playing, isFalse);

    player.finish();
    await pumpEventQueue();

    expect(session.index, 1);
    expect(identical(session.player, standby), isTrue);
    expect(standby.state.playing, isTrue);
    expect(standby.opened, hasLength(1));
    expect(repository.resolved, hasLength(1));
    expect(player.stops, greaterThan(0));
    expect(repository.released, contains('session-1'));
    expect(session.standbyIndex, -1);
  });

  test('jumping elsewhere drops the waiting next episode', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final standby = FakePlayer();
    final (session, player) = await _open(repository, store, standby: standby);
    await _settle(player);

    await session.play(2);

    expect(identical(session.player, player), isTrue);
    expect(session.standbyIndex, -1);
    expect(standby.stops, greaterThan(0));
    expect(repository.released, contains('session-2'));
    expect(repository.resolved.last.episode, 3);
  });

  test('a locked boost survives pausing and switching episodes', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);

    session.setBoostLocked(true);
    await pumpEventQueue();
    expect(player.rates.last, 2);

    session.togglePlayback();
    await pumpEventQueue();
    expect(session.boosting, isTrue);

    await session.play(1);
    expect(player.rates.last, 2);
    expect(session.boostLocked, isTrue);

    session.setBoostLocked(false);
    await pumpEventQueue();
    expect(player.rates.last, 1);
    expect(session.boosting, isFalse);
  });

  test('holding boosts at once and letting go restores the speed', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);

    session.holdBoost();
    await pumpEventQueue();
    expect(player.rates.last, 2);

    session.endBoost();
    await pumpEventQueue();
    expect(player.rates.last, 1);
  });

  test('the next episode opens early and keeps loading while paused', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final standby = FakePlayer();
    final (session, player) = await _open(repository, store, standby: standby);

    session.togglePlayback();
    player.state = player.state.copyWith(buffer: _seconds(4));
    player.progress(_seconds(.5), duration: _seconds(100));
    await Future<void>.delayed(const Duration(milliseconds: 700));
    await pumpEventQueue();

    expect(player.state.playing, isFalse);
    expect(session.standbyIndex, 1);
  });

  test('a paused preopen does not open the next episode', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final standby = FakePlayer();
    final (session, player) = await _open(
      repository,
      store,
      standby: standby,
      autoplay: false,
    );

    await _settle(player);

    expect(repository.preloaded, isEmpty);
    expect(session.standbyIndex, -1);
  });

  test('switching to the waiting episode swaps players at once', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final standby = FakePlayer();
    final (session, player) = await _open(repository, store, standby: standby);
    await _settle(player);

    final switching = session.play(1);

    expect(identical(session.player, standby), isTrue);
    expect(session.ready, isTrue);
    expect(session.paused, isFalse);
    await switching;
    expect(standby.state.playing, isTrue);
    expect(store.watched(_show.id)!.episode, 1);
  });

  test('only buffering that lasts counts as stalled', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);

    player.buffer(true);
    await Future<void>.delayed(const Duration(milliseconds: 200));
    player.buffer(false);
    await Future<void>.delayed(const Duration(milliseconds: 400));
    expect(session.stalled, isFalse);

    player.buffer(true);
    await Future<void>.delayed(const Duration(milliseconds: 600));
    expect(session.stalled, isTrue);
    player.buffer(false);
    expect(session.stalled, isFalse);
  });

  test('health watchdog only fires for a wanted, silent stop', () {
    final health = PlaybackHealth(
      intentStopTimeout: const Duration(seconds: 2),
    );
    final start = DateTime(2026, 1, 1);
    final at = start.add;

    bool stopped({
      bool wanted = true,
      bool playing = false,
      bool buffering = false,
      Duration? time,
    }) => health.stoppedWhileWanted(
      wanted: wanted,
      playing: playing,
      buffering: buffering,
      now: at(time ?? Duration.zero),
    );

    // 没有播放意图 / 正在播 / 正在缓冲，都不算故障。
    expect(stopped(wanted: false), isFalse);
    expect(stopped(playing: true), isFalse);
    expect(stopped(buffering: true), isFalse);
    expect(stopped(), isFalse);
    // 静默停止满 2s 才判定。
    expect(stopped(time: const Duration(seconds: 2)), isTrue);

    // 中途恢复播放会清零计时，重新停也要再等满一个窗口。
    expect(stopped(playing: true, time: const Duration(seconds: 3)), isFalse);
    expect(
      stopped(time: const Duration(seconds: 4)),
      isFalse,
      reason: '恢复播放后计时必须清零',
    );
    expect(stopped(time: const Duration(seconds: 6)), isTrue);
  });

  test(
    'a silently stopped player is recovered without a position stall',
    () async {
      final repository = _PlaybackRepository();
      final store = await openStore();
      final (session, player) = await _open(
        repository,
        store,
        intentStopTimeout: const Duration(milliseconds: 200),
      );
      await _settle(player);
      expect(session.ready, isTrue);

      // 播放器静默停在 playing=false，补发 play 也不生效：
      // position 不再推进，stalled() 永远不会报警，只能靠播放意图兜底。
      player.ignorePlay = true;
      player.state = player.state.copyWith(playing: false);

      final recovered = await _waitFor(() => repository.fallbacks > 0);
      expect(recovered, isTrue, reason: '静默停止应当触发自动恢复');
      expect(session.error, isNull);
    },
  );

  test(
    'a hung stop abandons the swap and rebuilds on a fresh player',
    () async {
      final repository = _PlaybackRepository();
      final store = await openStore();
      final standby = FakePlayer();
      final replacement = FakePlayer();
      final (session, player) = await _open(
        repository,
        store,
        standby: standby,
        respawn: () => replacement,
        swapStopTimeout: const Duration(milliseconds: 100),
      );
      await _settle(player);
      expect(session.standbyIndex, 1);

      // 待机实例已缓冲到 7s：交换失败重建时必须从 7s 续播，不能写死 0。
      standby.progress(_seconds(7));
      // 旧播放器 stop 挂起：不能继续在它停不下来的情况下初始化新实例音频。
      player.hangStop = true;
      await session.switchTo(1, reason: SwitchReason.next);
      await pumpEventQueue();

      expect(session.index, 1);
      expect(session.ready, isTrue);
      expect(identical(session.player, replacement), isTrue);
      // 只在预加载时打开过的待机实例没有被当成新实例继续播。
      expect(standby.opened, hasLength(1));
      // 新换的实例确实承载了播放（假播放器不出 position，之后可能再恢复一次）。
      expect(replacement.opened, isNotEmpty);
      // 重建必须用待机实例真实所在的位置，而不是硬编码 0。
      expect(replacement.opened.first.start, const Duration(seconds: 7));
      // 挂住的实例被摘下待机位并释放，之后不再被 _prime 复用。
      expect(player.disposed, isTrue);
      expect(standby.disposed, isTrue);
      expect(session.standbyIndex, -1);
    },
  );

  // ── 阶段 A：状态机、任务隔离、超时与不永久 Loading ──────────────

  test('解析地址超时落地可读错误，不永久 Loading', () async {
    final repository = _PlaybackRepository()..hangNextResolve = true;
    final store = await openStore();
    final (session, player) = await _open(
      repository,
      store,
      resolveTimeout: const Duration(milliseconds: 120),
    );

    final failed = await _waitFor(() => session.error != null);
    expect(failed, isTrue, reason: '解析挂起必须超时，不能一直 Loading');
    expect(session.loading, isFalse);
    expect(session.ready, isFalse);
    expect(session.phase, PlaybackPhase.error);
    expect(session.error, contains('超时'));
    expect(player.opened, isEmpty);

    // 旧解析任务晚返回：只能丢弃并释放它创建的 session，不得改状态。
    repository.releaseResolve();
    await pumpEventQueue();
    expect(session.loading, isFalse);
    expect(session.ready, isFalse);
    expect(session.index, 0);
    expect(repository.released, isNotEmpty);
  });

  test('打开预算耗尽时作废票据并落地错误，不永久 Loading', () async {
    final repository = _PlaybackRepository()..hangNextResolve = true;
    final store = await openStore();
    final (session, player) = await _open(
      repository,
      store,
      resolveTimeout: const Duration(seconds: 10),
      openBudget: const Duration(milliseconds: 150),
    );

    final failed = await _waitFor(() => session.error != null);
    expect(failed, isTrue);
    expect(session.error, contains('播放准备超时'));
    expect(session.loading, isFalse);
    expect(session.phase, PlaybackPhase.error);
    expect(player.opened, isEmpty);

    repository.releaseResolve();
    await pumpEventQueue();
    expect(session.error, contains('播放准备超时'));
    expect(session.loading, isFalse);
    expect(session.index, 0);
  });

  test('打开媒体挂起时有界结束，旧 open 晚返回不改写状态', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final seeded = FakePlayer()..hangOpen = true;
    final (session, player) = await _open(
      repository,
      store,
      seededPlayer: seeded,
      openTimeout: const Duration(milliseconds: 100),
      openBudget: const Duration(milliseconds: 700),
    );

    final ended = await _waitFor(() => !session.loading);
    expect(ended, isTrue, reason: 'open 挂起不能让页面一直转圈');
    expect(session.phase, PlaybackPhase.error);
    expect(session.error, isNotNull);

    player.completeAllOpens();
    await pumpEventQueue();
    expect(session.loading, isFalse);
    expect(session.error, isNotNull);
  });

  test('快速连续切集只保留最后一张票据的状态', () async {
    final repository = _PlaybackRepository()..hangNextResolve = true;
    final store = await openStore();
    final (session, player) = await _open(
      repository,
      store,
      resolveTimeout: const Duration(seconds: 10),
      openBudget: const Duration(seconds: 10),
    );

    unawaited(session.play(1));
    unawaited(session.play(2));
    final settled = await _waitFor(() => session.index == 2 && session.ready);
    expect(settled, isTrue);

    // 最早的解析任务最后才返回，不得把状态改回第 1 集。
    repository.releaseResolve();
    await pumpEventQueue();
    expect(session.index, 2);
    expect(session.ready, isTrue);
    expect(session.loading, isFalse);
    expect(player.opened, hasLength(1));
  });

  test('持续缓冲进入检测级，不换线路也不重建播放器', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(
      repository,
      store,
      bufferingBudget: const Duration(milliseconds: 200),
    );

    player.progress(_seconds(5), duration: _seconds(100));
    player.buffer(true);
    final detecting = await _waitFor(
      () => session.phase == PlaybackPhase.recovering,
    );
    expect(detecting, isTrue, reason: '持续 buffering 必须进入恢复检测级');
    expect(repository.fallbacks, 0, reason: '检测级不得直接换线路');
    expect(player.opened, hasLength(1), reason: '检测级不得重建播放器');
    expect(session.loading, isFalse);

    player.buffer(false);
    await pumpEventQueue();
    expect(session.phase, PlaybackPhase.playing);
  });

  test('dispose 挂起时不提前摘映射，原生释放完成后才摘', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final seeded = FakePlayer()..hangDispose = true;
    var retired = 0;
    final (session, player) = await _open(
      repository,
      store,
      seededPlayer: seeded,
      onRetired: (_) => retired++,
    );

    _close(session);
    await Future<void>.delayed(const Duration(milliseconds: 1700));
    expect(retired, 0, reason: 'dispose 超时期间不能摘掉仍在卸载的实例');

    player.completeDispose();
    await pumpEventQueue();
    expect(retired, 1);
    expect(player.disposed, isTrue);
  });

  test('phase 随起播、暂停、进入后台正确迁移', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);

    player.progress(_seconds(5), duration: _seconds(100));
    await pumpEventQueue();
    expect(session.phase, PlaybackPhase.playing);

    session.togglePlayback();
    await pumpEventQueue();
    expect(session.phase, PlaybackPhase.paused);

    session.setForeground(false);
    await pumpEventQueue();
    expect(session.phase, PlaybackPhase.background);
  });

  // ── 阶段 B：统一切集入口、standby 票据与失败隔离 ────────────────

  test('switchTo 忽略越界切集，不触发解析也不改状态', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);
    final resolved = repository.resolved.length;
    final opened = player.opened.length;

    await session.switchTo(-1, reason: SwitchReason.select);
    await session.switchTo(3, reason: SwitchReason.swipe);
    await pumpEventQueue();

    expect(session.index, 0);
    expect(session.error, isNull);
    expect(repository.resolved.length, resolved);
    expect(player.opened.length, opened);
  });

  test('禁止 standby 的切集走完整打开，不交换播放器', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final standby = FakePlayer();
    final (session, player) = await _open(repository, store, standby: standby);
    await _settle(player);
    expect(session.standbyIndex, 1);

    await session.switchTo(1, reason: SwitchReason.season, allowStandby: false);
    await pumpEventQueue();

    expect(identical(session.player, player), isTrue);
    expect(session.index, 1);
    expect(session.error, isNull);
    expect(session.standbyIndex, -1);
    // 待机实例只被预打开过一次，没有被误当成主播放器交换进来。
    expect(standby.opened, hasLength(1));
  });

  test('next / previous 在边界不越界，统一经 switchTo', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, _) = await _open(repository, store);

    expect(await session.previous(), isFalse);
    expect(session.index, 0);

    await session.switchTo(2, reason: SwitchReason.select);
    await pumpEventQueue();
    expect(session.index, 2);
    expect(await session.next(), isFalse);
    expect(session.index, 2);

    expect(await session.previous(), isTrue);
    await pumpEventQueue();
    expect(session.index, 1);
  });

  test('待机预打开挂起：降级、释放实例，当前播放不受影响', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final standby = FakePlayer();
    final (session, player) = await _open(
      repository,
      store,
      standby: standby,
      standbyOpenTimeout: const Duration(milliseconds: 100),
    );
    standby.hangOpen = true;
    await _settle(player);

    final released = await _waitFor(() => standby.disposed);
    expect(released, isTrue, reason: '挂起的待机实例必须真正 dispose，不能泄漏');
    expect(session.standbyIndex, -1);
    expect(session.standby, isNull);
    expect(session.index, 0);
    expect(session.error, isNull);
    expect(identical(session.player, player), isTrue);

    // 降级后切集仍可走完整打开，不依赖已经作废的待机实例。
    await session.switchTo(1, reason: SwitchReason.select);
    await pumpEventQueue();
    expect(session.index, 1);
    expect(session.error, isNull);
    expect(player.opened.length, greaterThan(1));
    // 挂起的 open 放行后不得改写已切集后的状态。
    standby.completeAllOpens();
    await pumpEventQueue();
    expect(session.index, 1);
    expect(session.standby, isNull);
  });

  // ---- 阶段 C：统一生命周期入口 / 首帧与盖幕 / 后台补切 ----

  test('inactive → resumed 触发轻量恢复，不再被幂等 guard 跳过（P0-3）', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);
    await pumpEventQueue();

    session.onLifecycleEvent(AppLifecycleState.inactive, source: 'test');
    expect(session.lifecycleStage, PlaybackLifecycleStage.preparing);
    expect(session.lifecycleEpoch, 1);
    expect(session.phase, isNot(PlaybackPhase.background));
    expect(player.state.playing, isTrue, reason: 'inactive 只存快照，不应主动 pause');

    session.onLifecycleEvent(AppLifecycleState.resumed, source: 'test');
    expect(session.lifecycleStage, PlaybackLifecycleStage.foreground);
    expect(session.lifecycleEpoch, 2);
    expect(session.surfaceCover, isTrue, reason: '只经历 inactive 的恢复序列也必须盖幕检查首帧');
  });

  test('多个 observer 重复上报同一状态只迁移一次', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, _) = await _open(repository, store);
    await pumpEventQueue();

    for (var i = 0; i < 4; i++) {
      session.onLifecycleEvent(AppLifecycleState.paused, source: 'observer$i');
    }
    expect(session.lifecycleEpoch, 1);
    expect(session.lifecycleStage, PlaybackLifecycleStage.background);
    expect(session.phase, PlaybackPhase.background);

    for (var i = 0; i < 3; i++) {
      session.onLifecycleEvent(AppLifecycleState.resumed, source: 'observer$i');
    }
    expect(session.lifecycleEpoch, 2);
    expect(session.lifecycleStage, PlaybackLifecycleStage.foreground);
    expect(session.phase, isNot(PlaybackPhase.background));
  });

  test('hidden / detached 同样进入后台', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, _) = await _open(repository, store);
    await pumpEventQueue();

    session.onLifecycleEvent(AppLifecycleState.hidden);
    expect(session.lifecycleStage, PlaybackLifecycleStage.background);
    session.onLifecycleEvent(AppLifecycleState.resumed);
    session.onLifecycleEvent(AppLifecycleState.detached);
    expect(session.lifecycleStage, PlaybackLifecycleStage.background);
    expect(session.lifecycleEpoch, 3);
  });

  test('PiP 活跃时系统判后台也保持前台', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, _) = await _open(repository, store);
    await pumpEventQueue();

    session.setPictureInPicture(true);
    expect(session.pipActive, isTrue);
    session.onLifecycleEvent(AppLifecycleState.paused);
    expect(session.lifecycleStage, PlaybackLifecycleStage.foreground);
    expect(session.phase, isNot(PlaybackPhase.background));

    session.setPictureInPicture(false);
    expect(session.pipActive, isFalse);
    expect(
      session.phase,
      PlaybackPhase.background,
      reason: '关闭 PiP 后按当前 paused 状态重算',
    );
  });

  test('后台播完回前台补切下一集，后台不发起解析（P1-8）', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);
    await pumpEventQueue();

    session.onLifecycleEvent(AppLifecycleState.paused);
    expect(session.phase, PlaybackPhase.background);

    player.progress(_seconds(100), duration: _seconds(100));
    player.finish();
    await pumpEventQueue();
    expect(session.index, 0, reason: '后台不得启动下一集解析');
    expect(repository.resolved.length, 1);

    session.onLifecycleEvent(AppLifecycleState.resumed);
    await pumpEventQueue();
    expect(session.index, 1);
    expect(repository.resolved.length, 2);
  });

  test('用户关闭自动连播后偏好写入不再被改回 true（P1-9）', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    await store.setPlaybackPreferences(
      const PlaybackPreferences(autoAdvance: false),
    );
    final (session, player) = await _open(repository, store);
    await pumpEventQueue();

    await session.setSpeed(1.25);
    expect(store.playbackPreferences.autoAdvance, isFalse);

    session.onLifecycleEvent(AppLifecycleState.paused);
    player.progress(_seconds(100), duration: _seconds(100));
    player.finish();
    await pumpEventQueue();
    session.onLifecycleEvent(AppLifecycleState.resumed);
    await pumpEventQueue();
    expect(session.index, 0);
  });

  test('盖幕软兜底保留黑幕进恢复链，硬兜底强制揭开（C7）', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, _) = await _open(
      repository,
      store,
      surfaceCoverTimeout: const Duration(milliseconds: 30),
      surfaceHardTimeout: const Duration(milliseconds: 50),
    );
    await pumpEventQueue();

    session.onLifecycleEvent(AppLifecycleState.inactive);
    session.onLifecycleEvent(AppLifecycleState.resumed);
    expect(session.surfaceCover, isTrue);

    await Future<void>.delayed(const Duration(milliseconds: 45));
    expect(session.surfaceTimedOut, isTrue);
    expect(session.surfaceCover, isTrue, reason: '3s 兜底不得强行揭幕露花屏');

    await Future<void>.delayed(const Duration(milliseconds: 70));
    expect(session.surfaceCover, isFalse, reason: '硬兜底必须揭开，防永久黑屏');
  });

  test('frameReady 独立反映画面就绪，不改变 ready 的交互语义', () async {
    final repository = _PlaybackRepository();
    final store = await openStore();
    final (session, player) = await _open(repository, store);
    await pumpEventQueue();
    expect(session.ready, isTrue);
    expect(session.frameReady, isFalse, reason: '还没有视频轨道参数');

    player.resize(1920, 1080);
    await pumpEventQueue();
    expect(session.frameReady, isTrue);
    expect(session.ready, isTrue);

    player.buffer(true);
    await pumpEventQueue();
    expect(session.frameReady, isFalse);
    expect(session.ready, isTrue, reason: '缓冲中 UI 控件仍应可用');

    session.togglePlayback();
    await pumpEventQueue();
    expect(session.ready, isTrue, reason: '暂停时控件不能失效');
  });
}
