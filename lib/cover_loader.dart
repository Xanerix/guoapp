import 'dart:async';

import 'core_bridge.dart';
import 'models.dart';

class CoverRequest {
  CoverRequest._(this._start);
  final Future<String> Function() _start;
  final _result = Completer<String>();
  Timer? _delay;
  bool _cancelled = false;

  Future<String> get file => _result.future;

  void cancel() {
    if (_cancelled || _result.isCompleted) return;
    _cancelled = true;
    _delay?.cancel();
    CoverLoader._waiting.remove(this);
  }

  void _run() {
    CoverLoader._running++;
    _start()
        .then(_result.complete, onError: _result.completeError)
        .whenComplete(() {
          CoverLoader._running--;
          CoverLoader._drain();
        });
  }
}

abstract final class CoverLoader {
  static const _limit = 6;
  static const _delay = Duration(milliseconds: 150);
  static final _waiting = <CoverRequest>[];
  static final _inflight = <String, Future<String>>{};
  static var _running = 0;

  static String _key(Drama drama) => '${drama.id}\u0000${drama.cover}';

  static Future<String>? cached(AppRepository repository, Drama drama) =>
      repository.coverCached(drama) ? repository.cover(drama) : null;

  /// 已经在下载中的海报。页面上的 CoverImage 与预热共用同一个请求，避免重复下载。
  static Future<String>? inflight(Drama drama) => _inflight[_key(drama)];

  /// 预热海报：分类切换前先把首屏封面取回来，揭幕时就不会出现灰色占位。
  static Future<String> preload(AppRepository repository, Drama drama) {
    final known = cached(repository, drama);
    if (known != null) return known;
    final key = _key(drama);
    final existing = _inflight[key];
    if (existing != null) return existing;
    final future = request(repository, drama).file;
    _inflight[key] = future;
    unawaited(
      future
          .then((_) {}, onError: (Object _) {})
          .whenComplete(() => _inflight.remove(key)),
    );
    return future;
  }

  static CoverRequest request(AppRepository repository, Drama drama) {
    final request = CoverRequest._(() => repository.cover(drama));
    request._delay = Timer(_delay, () {
      if (request._cancelled) return;
      _waiting.add(request);
      _drain();
    });
    return request;
  }

  static void _drain() {
    while (_running < _limit && _waiting.isNotEmpty) {
      _waiting.removeLast()._run();
    }
  }
}
