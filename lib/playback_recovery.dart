/// 自动故障恢复的 6 级动作（任务书「第六部分：故障自动恢复」）。
///
/// 1. [detect] 真实停滞检测：由 `PlaybackHealth` 与 `_queueRecovery` 的
///    二次采样承担，排除主动暂停、正常起播缓冲与位置真的在推进。
/// 2. [probe] 网络 / 缓冲区 / 播放器状态检测。
/// 3. [replay] 重发 `play()` / `seek + play`。
/// 4. [reopen] 重新 `open` 当前媒体地址（不换地址、不重建实例）。
/// 5. [alternative] / [refresh] 切备用线路 / 强制刷新站源地址。
/// 6. [rebuild] 重建 mpv 播放器并恢复播放位置。
///
/// [stop] 不是第 7 级，而是「次数/时间窗口耗尽」后的终态：落地可读错误
/// 与手动重试入口，绝不静默吞掉、也不允许无限恢复循环。
enum PlaybackRecoveryAction {
  detect,
  probe,
  replay,
  reopen,
  alternative,
  refresh,
  rebuild,
  stop,
}

/// 一次恢复决策的上下文。全部由 `PlaybackSession` 从真实状态采样，
/// 协调器本身不读播放器、不碰 UI，方便单测注入。
class PlaybackRecoveryContext {
  const PlaybackRecoveryContext({
    required this.now,
    this.networkReachable = true,
    this.buffering = false,
    this.playing = false,
    this.wanted = true,
    this.hasAlternative = false,
    this.streamEpochStale = false,
    this.local = false,
  });

  final DateTime now;
  final bool networkReachable;
  final bool buffering;
  final bool playing;
  final bool wanted;
  final bool hasAlternative;
  final bool streamEpochStale;
  final bool local;

  /// 第 1 级结论：画面确实在推进，就不该再恢复。
  bool get alreadyHealthy => playing && !buffering && wanted;
}

class PlaybackRecoveryDecision {
  const PlaybackRecoveryDecision({
    required this.token,
    required this.action,
    required this.level,
    required this.attempt,
    required this.backoff,
    required this.message,
  });

  /// 恢复任务票据；切集 / 关闭 / 用户重试会让票据作废。
  final int token;
  final PlaybackRecoveryAction action;
  final int level;
  final int attempt;
  final Duration backoff;
  final String message;

  bool get exhausted => action == PlaybackRecoveryAction.stop;
}

/// 触发源 → 起步级别。
///
/// 第 1–3 级里的「真实停滞检测 / 起播救援 / 重发播放」在会话层已有实现
/// （`PlaybackHealth`、`_ensurePlaying` 的 8 轮救援、`_checkBuffering` 的
/// 2 次 `_kickPlayer`）。对这些触发源再从头跑一遍只会重复无效操作，
/// 因此协调器从「尚未尝试过」的级别起步；原生事件、盖幕超时这类
/// 没经过低级检测的触发源仍从第 2 级开始，保证 6 级链真实可达。
int playbackRecoveryStartLevel(String reason) {
  // 原生事件、盖幕超时、前后台这类没经过低级检测的触发源仍从第 2 级开始，
  // 保证 6 级链真实可达（探活/重发/重开都不会被跳过）。
  if (reason.startsWith('native:') ||
      reason == 'network_restored' ||
      reason == 'surface_cover_timeout' ||
      reason == 'inactive_resume' ||
      reason == 'foreground_pending_error') {
    return 2;
  }
  // 卡播类：已确认「想播却没在播 / 播完却没推进 / 播放器报错」，跳过探活
  // （2 级）直接重发播放（3 级 replay），避免重复探活浪费一轮。
  if (reason == 'position_stalled_20s' ||
      reason == 'stopped_while_wanted' ||
      reason == 'player_error' ||
      reason == 'buffering_timeout' ||
      reason == 'replay_failed' ||
      reason == 'probe_failed') {
    return 3;
  }
  // 媒体层疑似有问题但还没到重建实例的程度：重开当前媒体（4 级 reopen）。
  if (reason == 'early_completion' || reason == 'ensure_playing_exhausted') {
    return 4;
  }
  // 其余（open_exception / recovery_exception / rebuild_* / probe_stream_dead /
  // *_failed 等）仍从第 5 级起步，直接刷新地址或重建实例。
  return 5;
}

/// 统一恢复协调器：单一计数、单一互斥、单一时间窗口、退避与取消票据。
///
/// 升级由「动作失败后再次入队」驱动（`_queueRecovery`），而不是在一个
/// 长任务里串行跑完 6 级——每一步都可被切集/关闭打断，也不会出现
/// 多个恢复任务同时争抢播放器。
class PlaybackRecovery {
  PlaybackRecovery({
    this.maxAttempts = defaultMaxAttempts,
    this.window = defaultWindow,
    this.baseBackoff = defaultBaseBackoff,
    this.maxBackoff = defaultMaxBackoff,
  });

  static const defaultMaxAttempts = 6;
  static const defaultWindow = Duration(minutes: 10);
  static const defaultBaseBackoff = Duration(seconds: 1);
  static const defaultMaxBackoff = Duration(seconds: 8);

  final int maxAttempts;
  final Duration window;
  final Duration baseBackoff;
  final Duration maxBackoff;

  int _attempts = 0;
  int _level = 0;
  int _token = 0;
  int? _active;
  DateTime? _firstAt;
  bool _exhausted = false;
  bool _networkBlocked = false;

  int get attempts => _attempts;
  int get lastLevel => _level;
  bool get active => _active != null;
  bool get exhausted => _exhausted;
  bool get networkBlocked => _networkBlocked;
  int? get activeToken => _active;

  /// 用户主动开新集 / 切集 / 手动重试 / 确认恢复成功：整条恢复链清零。
  void reset() {
    _attempts = 0;
    _level = 0;
    _firstAt = null;
    _exhausted = false;
    _networkBlocked = false;
    _active = null;
    _token++;
  }

  /// 作废在途恢复任务（切集、关闭、dispose）。不重置已消耗的次数，
  /// 防止「每次切集都把恢复次数洗白」造成无限循环。
  void cancel() {
    _active = null;
    _token++;
  }

  /// 开始一次恢复尝试；已有任务在跑时返回 null（互斥）。
  PlaybackRecoveryDecision? start({
    required String reason,
    required PlaybackRecoveryContext context,
  }) {
    if (_active != null) return null;
    final now = context.now;
    if (_firstAt != null && now.difference(_firstAt!) > window) {
      // 10 分钟内一直没真正恢复成功：不再自动续命，交手动重试。
      _exhausted = true;
    }
    if (!context.networkReachable) {
      // 网络不可达时换线路/重建都毫无意义，也不允许干等成永久 Loading。
      // 错误落地后由网络恢复事件调用 reset() 重新给一次机会。
      _networkBlocked = true;
      return PlaybackRecoveryDecision(
        token: ++_token,
        action: PlaybackRecoveryAction.stop,
        level: _level,
        attempt: _attempts,
        backoff: Duration.zero,
        message: '网络不可用，请检查网络后重试',
      );
    }
    _networkBlocked = false;
    if (_exhausted || _attempts >= maxAttempts) {
      _exhausted = true;
      return PlaybackRecoveryDecision(
        token: ++_token,
        action: PlaybackRecoveryAction.stop,
        level: _level,
        attempt: _attempts,
        backoff: Duration.zero,
        message: '自动恢复未成功，请检查网络后重试，也可换一集或选择其他清晰度',
      );
    }
    final startLevel = playbackRecoveryStartLevel(reason);
    var level = startLevel > _level + 1 ? startLevel : _level + 1;
    // 本机取流监听已重建：旧 session 指向的线路全部作废，低级动作和
    // fallback 都注定失败，直接跳到强制刷新（第 5 级的 refresh 分支）。
    if (context.streamEpochStale && level < 5) level = 5;
    if (level > 6) {
      _exhausted = true;
      return PlaybackRecoveryDecision(
        token: ++_token,
        action: PlaybackRecoveryAction.stop,
        level: _level,
        attempt: _attempts,
        backoff: Duration.zero,
        message: '自动恢复未成功，请检查网络后重试，也可换一集或选择其他清晰度',
      );
    }
    final attempt = ++_attempts;
    _firstAt ??= now;
    _level = level;
    final backoff = _backoffFor(attempt);
    final token = ++_token;
    _active = token;
    var action = switch (level) {
      1 => PlaybackRecoveryAction.detect,
      2 => PlaybackRecoveryAction.probe,
      3 => PlaybackRecoveryAction.replay,
      4 => PlaybackRecoveryAction.reopen,
      5 => context.streamEpochStale
          ? PlaybackRecoveryAction.refresh
          : context.hasAlternative
          ? PlaybackRecoveryAction.alternative
          : PlaybackRecoveryAction.refresh,
      _ => PlaybackRecoveryAction.rebuild,
    };
    var message = '第$level级 ${action.name}';
    if (context.local) {
      // 本地文件没有线路可换、没有站源可刷新，重建播放器也不会让
      // 缺失/损坏的文件变可读：直接落地可读错误与手动重试入口。
      action = PlaybackRecoveryAction.stop;
      message = '本地视频读取失败，请重试或重新下载；也可以改为在线播放。';
    }
    return PlaybackRecoveryDecision(
      token: token,
      action: action,
      level: level,
      attempt: attempt,
      backoff: backoff,
      message: message,
    );
  }

  /// 结束一次尝试。只有 [recovered] 为真（画面确认恢复）才清零计数；
  /// 失败时保留级别，下一次 [start] 自动升到更高一级。
  void complete(PlaybackRecoveryDecision decision, {required bool recovered}) {
    if (_active != decision.token) return;
    _active = null;
    if (recovered) reset();
  }

  Duration _backoffFor(int attempt) {
    if (attempt <= 1) return Duration.zero;
    var shift = attempt - 1;
    if (shift > 3) shift = 3;
    final scaled = baseBackoff.inMilliseconds << shift;
    final capped = scaled > maxBackoff.inMilliseconds
        ? maxBackoff.inMilliseconds
        : scaled;
    return Duration(milliseconds: capped < 0 ? 0 : capped);
  }
}

class PlaybackHealth {
  PlaybackHealth({
    this.intentStopTimeout = const Duration(seconds: 6),
    this.bufferingTimeout = const Duration(seconds: 25),
  });

  static const stallTimeout = Duration(seconds: 20);

  /// 用户明确要播放、播放器却一直停在非播放态，持续这么久就判定为
  /// 「静默停止」，需要主动恢复（position 停滞检测覆盖不到这种状态）。
  final Duration intentStopTimeout;

  /// 持续缓冲多久算「需要进入检测级」。注意：超过它也不等于故障，
  /// 调用方只能做检测/重发播放，不能直接重建播放器。
  final Duration bufferingTimeout;

  Duration? _position;
  DateTime? _lastProgress;
  DateTime? _stoppedAt;
  DateTime? _bufferingSince;

  void reset() {
    _position = null;
    _lastProgress = null;
    _stoppedAt = null;
    _bufferingSince = null;
  }

  void resetBuffering() {
    _bufferingSince = null;
  }

  /// 持续缓冲检测：只有「确实想播、且一直处于 buffering」才计时。
  /// 主动暂停、后台、正常起播缓冲都会被清零，不会误判为故障。
  bool bufferingStalled({
    required bool buffering,
    required bool wanted,
    required DateTime now,
  }) {
    if (!buffering || !wanted) {
      _bufferingSince = null;
      return false;
    }
    _bufferingSince ??= now;
    return now.difference(_bufferingSince!) >= bufferingTimeout;
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
