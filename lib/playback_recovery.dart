import 'models.dart';

enum PlaybackRecoveryAction { alternative, refresh, stop }

class PlaybackRecovery {
  static const maxAttempts = 3;
  int _attempts = 0;
  bool _refreshed = false;

  void reset() {
    _attempts = 0;
    _refreshed = false;
  }

  PlaybackRecoveryAction next(PlaybackPlan plan) {
    if (_attempts >= maxAttempts) {
      return PlaybackRecoveryAction.stop;
    }
    if (plan.hasAlternative) {
      _attempts++;
      return PlaybackRecoveryAction.alternative;
    }
    if (!_refreshed) {
      _attempts++;
      _refreshed = true;
      return PlaybackRecoveryAction.refresh;
    }
    return PlaybackRecoveryAction.stop;
  }
}

class PlaybackHealth {
  PlaybackHealth({this.intentStopTimeout = const Duration(seconds: 6)});

  static const stallTimeout = Duration(seconds: 20);

  /// 用户明确要播放、播放器却一直停在非播放态，持续这么久就判定为
  /// 「静默停止」，需要主动恢复（position 停滞检测覆盖不到这种状态）。
  final Duration intentStopTimeout;

  Duration? _position;
  DateTime? _lastProgress;
  DateTime? _stoppedAt;

  void reset() {
    _position = null;
    _lastProgress = null;
    _stoppedAt = null;
  }

  bool stalled({
    required Duration position,
    required bool playing,
    required bool foreground,
    required DateTime now,
  }) {
    if (!playing || !foreground || position != _position) {
      _position = position;
      _lastProgress = now;
      return false;
    }
    _lastProgress ??= now;
    return now.difference(_lastProgress!) >= stallTimeout;
  }

  /// 播放器停在 playing=false（且非缓冲）持续超时。
  ///
  /// [wanted] 表示当前是否真的应该播放（播放意图 + 前台 + 非用户暂停）。
  /// 只要重新开始播放或进入缓冲就清零，避免正常起播/切集被误判。
  bool stoppedWhileWanted({
    required bool wanted,
    required bool playing,
    required bool buffering,
    required DateTime now,
  }) {
    if (!wanted || playing || buffering) {
      _stoppedAt = null;
      return false;
    }
    _stoppedAt ??= now;
    return now.difference(_stoppedAt!) >= intentStopTimeout;
  }
}
