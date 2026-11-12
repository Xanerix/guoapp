import 'dart:async';

import 'package:media_kit/media_kit.dart';

class FakePlayer implements Player {
  @override
  PlayerState state = const PlayerState();
  final opened = <Media>[];
  final rates = <double>[];
  final seeks = <Duration>[];
  var stops = 0;
  var disposed = false;

  /// 模拟 mpv 的 stop 挂起：调用后永远不返回。
  bool hangStop = false;

  /// 模拟「补发 play 也不生效」：播放器静默停在 playing=false。
  bool ignorePlay = false;

  /// 模拟 open 挂起：每次 open 都停在独立 gate 上，测试可择机放行，
  /// 用来验证过期任务晚返回不会写回状态。
  bool hangOpen = false;
  final _openGates = <Completer<void>>[];

  /// 模拟 dispose 挂起（原生释放超时）。
  bool hangDispose = false;
  Completer<void>? _disposeGate;

  void completeOpen(int index) {
    final gate = _openGates[index];
    if (!gate.isCompleted) gate.complete();
  }

  void completeAllOpens() {
    for (final gate in _openGates) {
      if (!gate.isCompleted) gate.complete();
    }
  }

  void completeDispose() {
    _disposeGate?.complete();
    disposed = true;
  }

  final _playing = StreamController<bool>.broadcast(sync: true);
  final _completed = StreamController<bool>.broadcast(sync: true);
  final _position = StreamController<Duration>.broadcast(sync: true);
  final _duration = StreamController<Duration>.broadcast(sync: true);
  final _videoParams = StreamController<VideoParams>.broadcast(sync: true);
  final _error = StreamController<String>.broadcast(sync: true);
  final _buffering = StreamController<bool>.broadcast(sync: true);

  @override
  PlatformPlayer? platform;

  @override
  late final PlayerStream stream = PlayerStream(
    const Stream.empty(),
    _playing.stream,
    _completed.stream,
    _position.stream,
    _duration.stream,
    const Stream.empty(),
    const Stream.empty(),
    const Stream.empty(),
    _buffering.stream,
    const Stream.empty(),
    const Stream.empty(),
    const Stream.empty(),
    const Stream.empty(),
    const Stream.empty(),
    _videoParams.stream,
    const Stream.empty(),
    const Stream.empty(),
    const Stream.empty(),
    const Stream.empty(),
    const Stream.empty(),
    const Stream.empty(),
    const Stream.empty(),
    const Stream.empty(),
    const Stream.empty(),
    _error.stream,
  );

  void progress(Duration position, {Duration? duration}) {
    state = state.copyWith(
      position: position,
      duration: duration ?? state.duration,
    );
    if (duration != null) _duration.add(duration);
    _position.add(position);
  }

  void finish() {
    state = state.copyWith(
      position: state.duration,
      completed: true,
      playing: false,
    );
    _completed.add(true);
  }

  void fail(String message) => _error.add(message);

  void buffer(bool buffering) {
    state = state.copyWith(buffering: buffering);
    _buffering.add(buffering);
  }

  void resize(int width, int height) =>
      _videoParams.add(VideoParams(w: width, h: height));

  @override
  Future<void> open(Playable playable, {bool play = true}) async {
    opened.add(playable as Media);
    if (hangOpen) {
      final gate = Completer<void>();
      _openGates.add(gate);
      return gate.future;
    }
    state = state.copyWith(
      playing: play,
      completed: false,
      position: playable.start ?? Duration.zero,
    );
    _playing.add(play);
  }

  @override
  Future<void> stop() async {
    stops++;
    if (hangStop) return Completer<void>().future;
    state = state.copyWith(playing: false, position: Duration.zero);
  }

  @override
  Future<void> play() async {
    if (ignorePlay) return;
    state = state.copyWith(playing: true);
    _playing.add(true);
  }

  @override
  Future<void> pause() async {
    state = state.copyWith(playing: false);
    _playing.add(false);
  }

  @override
  Future<void> playOrPause() => state.playing ? pause() : play();

  @override
  Future<void> seek(Duration duration) async {
    seeks.add(duration);
    state = state.copyWith(position: duration);
  }

  @override
  Future<void> setRate(double rate) async {
    rates.add(rate);
    state = state.copyWith(rate: rate);
  }

  @override
  Future<void> setVolume(double volume) async {
    state = state.copyWith(volume: volume);
  }

  @override
  Future<void> dispose() async {
    if (hangDispose) {
      _disposeGate = Completer<void>();
      return _disposeGate!.future;
    }
    disposed = true;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => super.noSuchMethod(invocation);
}
