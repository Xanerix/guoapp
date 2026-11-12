import 'dart:async';
import 'dart:io';
import 'dart:ui' show AppLifecycleState, Size;

import 'package:flutter/foundation.dart';
import 'package:media_kit/media_kit.dart';
import 'package:wakelock_plus/wakelock_plus.dart';

import 'app_device.dart';
import 'core_bridge.dart';
import 'danmaku_controller.dart';
import 'danmaku_style.dart';
import 'diary_service.dart';
import 'local_store.dart';
import 'models.dart';
import 'playback_loader.dart';
import 'playback_preferences.dart';
import 'playback_preloader.dart';
import 'playback_recovery.dart';

Size? videoDisplaySize(VideoParams parameters) {
  final width = (parameters.dw ?? 0) > 0 ? parameters.dw! : parameters.w ?? 0;
  final height = (parameters.dh ?? 0) > 0 ? parameters.dh! : parameters.h ?? 0;
  if (width <= 0 || height <= 0) return null;
  final rotation = (parameters.rotate ?? 0) % 180;
  return rotation == 90
      ? Size(height.toDouble(), width.toDouble())
      : Size(width.toDouble(), height.toDouble());
}

String _diagnostic(Object error) {
  final text = error.toString().replaceAll(
    RegExp(r'[a-z]+://\S+', caseSensitive: false),
    '[地址]',
  );
  return text.length > 300 ? text.substring(0, 300) : text;
}

/// mpv 的降级提示：硬解初始化失败会回退软解，音频设备不可用只是静音，
/// 两者都不影响继续播放，不应触发恢复流程。
const _benignPlayerErrors = [
  'Failed to allocate AVHWDeviceContext',
  'Could not open/initialize audio device',
];

bool isBenignPlayerError(String error) {
  for (final marker in _benignPlayerErrors) {
    if (error.contains(marker)) return true;
  }
  return false;
}

/// 取流服务探活结果：明确存活、确认失效、或状态未知（通信异常）。
enum _StreamProbe { alive, dead, unknown }

/// 待机播放器的独立生命周期状态。
///
/// 旧实现只用 [_standbyBusy] 一个标志，且 [_dropStandby] 的 stop 是
/// unawaited、不等待完成；于是「切集触发 dropStandby → 主播放器恢复 →
/// _prime 重新预打开」可能和尚未结束的 stop 交错，同一实例同时被 stop 和
/// open。引入显式状态机：stopping 期间禁止 _prime 复用，open 完成才 opened，
/// 挂死才 retiring，确保停止完成前旧实例不被复用。
enum _StandbyState { idle, opening, opened, stopping, retiring }

/// 播放会话的统一对外状态。
///
/// 迁移规则集中在一个位置推导（见 PlaybackSession._derivePhase），
/// 任何内部 bool 变更在 notify 前都会对齐到这里，避免十余个标志互相矛盾：
/// idle → opening / switching（解析地址、打开媒体）→ playing；
/// buffering 是 playing 的临时子态，持续超过预算转 recovering；
/// recovering 成功回 playing，打开预算耗尽或恢复次数用尽转 error；
/// paused / background / completed 都可回到 playing。
enum PlaybackPhase {
  idle,
  opening,
  buffering,
  playing,
  paused,
  switching,
  recovering,
  background,
  completed,
  error,
}

/// 应用生命周期阶段。UI 只负责上报原始 [AppLifecycleState]，
/// 由 PlaybackSession 唯一解释成这三个阶段，消除各页面口径不一致。
enum PlaybackLifecycleStage {
  /// 真前台：可见且可交互。
  foreground,

  /// 即将离开前台（iOS inactive：锁屏/控制中心/来电瞬间）。
  /// 此时进程仍可渲染，只保存快照与抓帧，不暂停播放器。
  preparing,

  /// 已进入后台（paused / hidden / detached），需要暂停与保存进度。
  background,
}

/// 切集来源。所有切集必须经 PlaybackSession.switchTo 进入，
/// 用 reason 区分策略与打点，避免 UI 各自解释边界条件。
enum SwitchReason {
  /// 选集面板点击。
  select,

  /// 上一集 / 下一集按钮、迷你播放器、播控条。
  previous,
  next,

  /// 竖屏/横屏上下滑动翻页。
  swipe,

  /// 当前集播完自动连播。
  completed,

  /// 本季播完衔接下一季第 1 集。
  season,

  /// 键盘 / 遥控器上下集。
  keyboard,

  /// 从历史记录、详情页等带进度进入播放。
  start,

  /// 其它未分类入口。
  other,
}

/// 异步播放任务超出预算。
///
/// Future.timeout 并不会取消原始任务：抛出它只代表「不再等待」，
/// 原始 Future 仍可能在后台结束，其结果必须被丢弃而不是写回状态。
class PlaybackTaskTimeout implements Exception {
  const PlaybackTaskTimeout(this.label, this.timeout);

  final String label;
  final Duration timeout;

  @override
  String toString() => '$label 超时（${timeout.inMilliseconds}ms）';
}

/// 恢复后确认真正开播的采样窗口与最小推进量。
const _restoreWindow = Duration(milliseconds: 1200);
const _restoreProgress = Duration(milliseconds: 300);

class PlaybackSession extends ChangeNotifier {
  PlaybackSession({
    required this.repository,
    required this.store,
    required this.detail,
    required this._player,
    this._standby,
    this._respawn,
    this._onRetired,
    required int index,
    double position = 0,
    bool autoplay = true,
    this._swapStopTimeout = const Duration(seconds: 2),
    this._stopTimeout = const Duration(seconds: 4),
    this._intentStopTimeout = const Duration(seconds: 6),
    this._resolveTimeout = const Duration(seconds: 20),
    this._openTimeout = const Duration(seconds: 12),
    this._playTimeout = const Duration(seconds: 8),
    this._openBudget = const Duration(seconds: 25),
    this._bufferingBudget = const Duration(seconds: 25),
    this._standbyOpenTimeout = const Duration(seconds: 12),
    this._surfaceCoverTimeout = const Duration(seconds: 3),
    this._surfaceHardTimeout = const Duration(seconds: 5),
    this._recoveryMaxAttempts = PlaybackRecovery.defaultMaxAttempts,
    this._recoveryWindow = PlaybackRecovery.defaultWindow,
    this._recoveryBackoff = const Duration(seconds: 1),
  }) : _engaged = autoplay,
       _index = index,
       _epoch = store.profileEpoch {
    final preferences = store.playbackPreferences;
    _speed = preferences.speed;
    _quality = preferences.quality;
    _danmakuEnabled = preferences.danmaku;
    _videoFit = preferences.videoFit;
    _unmutedVolume = store.playerVolume.clamp(1.0, 100.0);
    unawaited(_player.setVolume(_unmutedVolume));
    _loader = PlaybackLoader(repository);
    _preloader = PlaybackPreloader(repository)..addListener(_prime);
    danmaku = DanmakuController(repository)
      ..setEnabled(_danmakuEnabled)
      ..setBlockedWords(preferences.danmakuWords);
    store.addListener(_accessChanged);
    store.addListener(_syncBlockedWords);
    _bind();
    _saveTimer = Timer.periodic(
      const Duration(seconds: 5),
      (_) => saveProgress(quiet: true),
    );
    _healthTimer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (_closed) return;
      final now = DateTime.now();
      // 起播 / 异常窗口不再整体屏蔽健康检查：持续 buffering 必须能被发现，
      // 否则 _loading=true 时播放器卡在缓冲永远不会进入任何恢复链。
      // 打开窗口本身另由 _openBudgetTimer 守护，保证不会永久 Loading。
      if (_checkBuffering(now)) return;
      if (_restoring || !_acceptErrors || _loading || _error != null) {
        return;
      }
      final state = player.state;
      if (_health.stalled(
        position: state.position,
        playing: state.playing && _playIntent,
        foreground: _foreground,
        now: now,
      )) {
        unawaited(_recover('position_stalled_20s'));
        return;
      }
      // stalled() 只覆盖「playing=true 但位置不动」，播放器静默变成
      // playing=false 时它永远不报警。这里单独兜底：用户想播、在前台、
      // 没在缓冲，播放器却持续停着 → 主动走恢复，避免卡死只能重启 App。
      if (_health.stoppedWhileWanted(
        wanted: _playIntent && _foreground && !_pausedByUser && !_rebuilding,
        playing: state.playing,
        buffering: state.buffering,
        now: now,
      )) {
        DiaryService.add(
          '[Play] 有播放意图但播放器已停止，转自动恢复 '
          '位置=${state.position} 集=${_index + 1}',
        );
        _queueRecovery('stopped_while_wanted');
      }
    });
    unawaited(play(index, position: position, playWhenReady: autoplay));
    // 播放时保持屏幕常亮，避免息屏后还要手动解锁才能秒恢复。
    unawaited(WakelockPlus.enable().catchError((Object _) {}));
  }

  void _bind() {
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    _subscriptions.clear();
    final size = videoDisplaySize(player.state.videoParams);
    if (size != null) {
      _videoSize = size;
      _aspectRatio = size.width / size.height;
      _aspectKnown = true;
      _aspectEpoch = _frameEpoch; // 初始尺寸证据绑定当前画面代次
    }
    _buffering(player.state.buffering);
    final stream = player.stream;
    _subscriptions.addAll([
      stream.error.listen(_playerError),
      stream.completed.listen(_completed),
      stream.position.listen((position) {
        if (!_closed && _openedIndex == _index && position > Duration.zero) {
          _resumePosition = position.inMilliseconds / 1000;
        }
        // 黑幕揭开：纹理已经出新画面（见 _maybeRevealSurface）。
        _maybeRevealSurface();
        _sync();
      }),
      stream.playing.listen((playing) {
        if (!playing) endBoost();
        // playing=true 只说明解码器起播，不代表纹理首帧已渲染，
        // 由 _maybeRevealSurface 统一判断。
        _maybeRevealSurface();
        _sync();
      }),
      stream.buffering.listen((buffering) {
        _buffering(buffering);
        _sync();
      }),
      for (final changes in [stream.duration, stream.buffer, stream.rate])
        changes.listen((_) => _sync()),
      stream.videoParams.listen((parameters) {
        final size = videoDisplaySize(parameters);
        if (size == null || _closed) return;
        _videoSize = size;
        _aspectRatio = size.width / size.height;
        _aspectKnown = true;
        _aspectEpoch = _frameEpoch; // 尺寸证据绑定当前画面代次，旧集残留不会污染新集
        // 视频轨道参数出现 = 新纹理已经有画面可渲染。
        _surfaceFrame = true;
        _maybeRevealSurface();
        _notify();
      }),
    ]);
  }

  final AppRepository repository;
  final LocalStore store;
  DramaDetail detail;
  Player _player;
  final Player Function()? _respawn;
  final void Function(Player player)? _onRetired;
  Player? _standby;
  late final DanmakuController danmaku;
  late final PlaybackLoader _loader;
  late final PlaybackPreloader _preloader;
  late final _recovery = PlaybackRecovery(
    maxAttempts: _recoveryMaxAttempts,
    window: _recoveryWindow,
    baseBackoff: _recoveryBackoff,
  );
  late final _health = PlaybackHealth(
    intentStopTimeout: _intentStopTimeout,
    bufferingTimeout: _bufferingBudget,
  );

  /// 旧播放器 stop 的最长等待；超时说明原生停止挂住，放弃交换改走重建。
  final Duration _swapStopTimeout;

  /// 任何 player.stop() 的最长等待上限。mpv 偶发 stop 挂起会让 [_serialize]
  /// 串行队列卡死，所有停止都必须走 [_boundedStop] 受此约束。
  final Duration _stopTimeout;

  /// 有播放意图但播放器持续静止多久后自动恢复。
  final Duration _intentStopTimeout;

  /// 解析地址 / open / play 各自的等待上限，以及整次打开的兜底预算。
  /// 都可在测试中注入短值，生产值不允许靠「加延时」掩盖挂起。
  final Duration _resolveTimeout;
  final Duration _openTimeout;
  final Duration _playTimeout;
  final Duration _openBudget;
  final Duration _bufferingBudget;

  /// 待机播放器预打开的等待上限。挂起时该实例不再复用，直接释放，
  /// 预加载降级为「只解析地址」，绝不影响当前正在播的这一集。
  final Duration _standbyOpenTimeout;

  /// 盖幕 3s 软兜底（进恢复链）与之后的硬兜底（强制揭幕，防永久黑屏）。
  /// 同样允许测试注入短值。
  final Duration _surfaceCoverTimeout;
  final Duration _surfaceHardTimeout;

  /// 恢复协调器的次数上限、时间窗口与起始退避；测试可注入短值验证
  /// 「次数耗尽」与「分级升级」，生产值保持 6 次 / 10 分钟 / 1s→8s。
  final int _recoveryMaxAttempts;
  final Duration _recoveryWindow;
  final Duration _recoveryBackoff;
  final int _epoch;
  final List<StreamSubscription<dynamic>> _subscriptions = [];
  Timer? _saveTimer;
  Timer? _healthTimer;
  Timer? _errorTimer;
  Timer? _boostTimer;
  Timer? _stallTimer;
  Timer? _volumeTimer;
  Future<void> _operations = Future<void>.value();
  Future<void> _rates = Future<void>.value();
  int _index;
  int _openedIndex = -1;
  int _generation = 0;
  int _seekSequence = 0;

  /// 唯一对外状态；只经 _setPhase 写入，由 _notify 对齐。
  PlaybackPhase _phase = PlaybackPhase.idle;
  bool _switching = false;
  Timer? _openBudgetTimer;
  DateTime? _openStartedAt;
  bool _bufferingRecovering = false;
  int _bufferingRecoveries = 0;
  static const _maxBufferingRecoveries = 2;
  bool _loading = true;
  bool _online = false;
  bool _localFailure = false;
  bool _loginRequired = false;
  bool _closed = false;
  bool _acceptErrors = false;
  bool _foreground = true;
  bool _playIntent = true;
  bool _pausedByUser = false;
  bool _pendingError = false;
  bool _resumeIntent = true;
  int _iosRebuilds = 0;
  bool _playerSuspect = false;
  bool _rebuilding = false;
  bool _advancingSeason = false;
  bool _rebuildQueued = false;
  int? _queuedToken;
  double _queuedPosition = 0;
  bool _queuedPlay = true;
  int _restoreToken = 0;
  int _restores = 0;
  bool _surfaceCover = false;
  DateTime? _surfaceCoverAt;
  Timer? _surfaceTimer;

  /// 3s 软兜底之后的硬兜底：恢复链也救不回来时不能让用户永远盯着黑幕。
  Timer? _surfaceHardTimer;

  // ---- 统一生命周期入口（阶段 C）----
  AppLifecycleState? _lastLifecycle;
  int _lifecycleEpoch = 0;
  PlaybackLifecycleStage _lifecycleStage = PlaybackLifecycleStage.foreground;
  bool _pipActive = false;

  /// 只经历过 inactive、没有真正 paused：回前台时需走一次轻量恢复检查。
  bool _lifecycleDormant = false;

  /// 后台播完当前集：不在后台启动解析，回前台再补切下一集。
  bool _pendingCompleted = false;

  // ---- 阶段 D：恢复协调器的外部输入 ----
  /// 网络是否可达。C-Native 的 `NWPathMonitor` 落地后由 EventChannel
  /// 转发到 [onNetworkChanged]；在此之前只有 Dart 单测会驱动它。
  bool _networkReachable = true;

  /// Go 本机取流监听的当前 epoch。`nativeOpenPlayback` 把建会话时的
  /// epoch 写进 `PlaybackPlan.streamEpoch`；监听重建后两者不一致，
  /// 旧 session 的 fallback 必然失败，恢复链必须改为强制刷新地址。
  int _streamEpoch = 0;

  /// 盖黑幕之后当前纹理是否已报告首帧参数（视频轨道真实就绪）。
  bool _surfaceFrame = false;

  /// 3s 软兜底已触发过：黑幕仍在，恢复链接手。
  bool _surfaceTimedOut = false;
  Duration _surfaceCoverPosition = Duration.zero;
  bool _forceSoftwareDecode = false;
  bool _boosting = false;
  bool _boostLocked = false;
  bool _engaged;
  bool _staleAudio = false;
  bool _stalled = false;
  String _loadingMessage = '正在准备播放';
  String? _error;
  String? _saveWarning;
  String? _savedProgressKey;
  bool _savedQuietly = false;
  PlaybackPlan? _plan;
  PlaybackPlan? _standbyPlan;
  int _standbyIndex = -1;
  bool _standbyOpen = false;
  bool _standbyBusy = false; // 旧播放器 stop 未完时，禁止 _prime 复用它
  _StandbyState _standbyState = _StandbyState.idle;
  DateTime _standbyAt = DateTime(2000);

  /// 每次 _prime / _dropStandby 都递增：在途的待机打开任务在回写前
  /// 必须校验票据，防止上一集的预打开晚完成后覆盖新一集的状态。
  int _standbyTicket = 0;

  /// 待机预打开挂起过一次后不再尝试复用该实例：降级为只解析地址。
  /// 目的不是「修好预加载」，而是保证它永远不能拖垮当前播放。
  bool _standbyHung = false;
  double _openedAt = 0;
  double _speed = 1;
  int _quality = 0;
  bool _danmakuEnabled = true;
  Size? _videoSize;
  VideoFit _videoFit = VideoFit.auto;
  double _aspectRatio = 9 / 16;
  bool _aspectKnown = false;

  /// 画面代次：每次进入「新纹理/新一集」自增，作废上一集残留的视频尺寸证据。
  /// [_aspectEpoch] 记录 videoParams 监听器绑定时的代次，二者不一致说明
  /// 尺寸来自旧集，[frameReady] 不能据此判定新集已就绪（见 [_beginNewFrame]）。
  int _frameEpoch = 0;
  int _aspectEpoch = 0;
  double _resumePosition = 0;
  double _unmutedVolume = 100;

  /// 统一对外状态。所有内部 bool 只是实现细节，读取状态一律以此为准。
  PlaybackPhase get phase => _phase;

  Player get player => _player;

  Player? get standby => _standbyOpen ? _standby : null;
  int get standbyIndex => _standbyOpen ? _standbyIndex : -1;
  int get index => _index;
  Episode get episode => detail.episodes[_index];
  bool get loading => _loading;
  String get loadingMessage => _loadingMessage;
  String? get error => _error;
  bool get localFailure => _localFailure;
  bool get loginRequired => _loginRequired;
  String? get saveWarning => _saveWarning;

  /// 恢复链的可观测状态（诊断/单测用）：已尝试次数、已到级别、是否耗尽。
  int get recoveryAttempts => _recovery.attempts;
  int get recoveryLevel => _recovery.lastLevel;
  bool get recoveryExhausted => _recovery.exhausted;
  bool get networkReachable => _networkReachable;
  int get streamEpoch => _streamEpoch;

  /// 画面/交互是否就绪。与 phase 保持一致：打开中、换集中、出错时均不就绪。
  bool get ready => !_loading && _error == null;

  bool get stalled => _stalled;

  /// 正在执行前后台恢复：期间不让健康检查再触发一次恢复，避免互相打断。
  bool get _restoring => _restores > 0;

  /// 恢复瞬间纹理重建会出现一两帧花屏，用一个短黑幕遮住。
  bool get surfaceCover => _surfaceCover;

  /// 3s 软兜底已触发：首帧迟迟不来，黑幕保留并交给恢复链。
  bool get surfaceTimedOut => _surfaceTimedOut;

  /// 真实画面就绪：组合视频轨道尺寸/参数、非打开中、无错误、非缓冲。
  /// 与 [ready] 的区别：ready 表示「控件可交互」（UI 层 40+ 处依赖，暂停时
  /// 仍为 true），frameReady 只表示「纹理上确实有画面」，供盖幕与诊断使用。
  bool get frameReady =>
      !_closed &&
      !_loading &&
      _error == null &&
      !player.state.buffering &&
      // 尺寸证据必须绑定当前画面代次：切集后上一集残留的宽高/aspect 不能
      // 让新集的 frameReady 提前返回 true（旧实现会误判未出画的集已就绪）。
      ((_aspectKnown && _aspectEpoch == _frameEpoch) ||
          (player.state.width ?? 0) > 0);

  /// 统一生命周期入口的当前阶段，供日志与测试观察。
  PlaybackLifecycleStage get lifecycleStage => _lifecycleStage;

  /// 真实发生过的生命周期迁移次数（重复上报不计），用于验证去重。
  int get lifecycleEpoch => _lifecycleEpoch;

  bool get pipActive => _pipActive;

  bool get paused => !_playIntent;
  bool get boosting => _boosting;
  bool get boostLocked => _boostLocked;
  Size? get videoSize => _videoSize;
  VideoFit get videoFit => _videoFit;
  double get aspectRatio => _aspectRatio;
  bool get aspectKnown => _aspectKnown;
  double get speed => _speed;
  int get quality => _quality;
  bool get local => _plan?.local == true;
  List<int> get qualities => _plan?.qualities ?? const [];
  bool get danmakuSupported => detail.drama.source == SourceSite.hongguo.id;
  bool get danmakuEnabled => _danmakuEnabled;
  PlaybackPreferences get preferences => _preferences;
  DanmakuStyle get danmakuStyle => DanmakuStyle.of(store.playbackPreferences);
  bool get hasPrevious => _index > 0;
  bool get hasNext => _index + 1 < detail.episodes.length;
  String get qualityLabel {
    final plan = _plan;
    if (plan?.local == true) return '本地原画';
    if (_quality > 0) return '${_quality}P';
    return plan != null && plan.quality > 0 ? '自动（${plan.quality}P）' : '自动';
  }

  double get currentPosition =>
      _openedIndex == _index && player.state.position > Duration.zero
      ? player.state.position.inMilliseconds / 1000
      : _resumePosition;

  bool get _usable => !_closed && store.profileEpoch == _epoch;
  bool _current(int ticket) => _usable && ticket == _generation;

  /// 只覆盖由会话实时驱动的那几项。
  /// autoAdvance / preload 必须沿用用户设置：以前这里硬编码 true，
  /// 导致任何一次偏好写入（改倍速、改清晰度）都把用户关掉的自动连播
  /// 悄悄改回开启（P1-9）。
  PlaybackPreferences get _preferences => store.playbackPreferences.copyWith(
    speed: _speed,
    quality: _quality,
    danmaku: _danmakuEnabled,
    videoFit: _videoFit,
  );

  double get _rate => _boosting ? 2 : _speed;

  void _buffering(bool buffering) {
    _stallTimer?.cancel();
    if (buffering) {
      _stallTimer = Timer(const Duration(milliseconds: 500), () {
        if (_closed || !player.state.buffering) return;
        _stalled = true;
        _notify();
      });
    } else {
      _stalled = false;
      // 缓冲结束即退出「检测级」，并清掉连续计数，避免下次缓冲被误判。
      if (_bufferingRecovering) {
        _bufferingRecovering = false;
        _bufferingRecoveries = 0;
        _health.resetBuffering();
      }
      _notify();
    }
  }

  /// 唯一的 phase 写入口；异常相关迁移留可观测日志。
  void _setPhase(PlaybackPhase next) {
    if (_phase == next) return;
    final previous = _phase;
    _phase = next;
    if (next == PlaybackPhase.error ||
        next == PlaybackPhase.recovering ||
        next == PlaybackPhase.background) {
      DiaryService.add(
        '[Play] 状态 ${previous.name} → ${next.name} 集=${_index + 1}',
      );
    }
  }

  /// 由内部字段推导唯一状态。每次 notify 前调用，杜绝 bool 与 phase 不同步。
  PlaybackPhase _derivePhase() {
    if (_closed) return PlaybackPhase.idle;
    if (_error != null) return PlaybackPhase.error;
    if (!_foreground) return PlaybackPhase.background;
    if (_bufferingRecovering || _restoring || _rebuilding || _pendingError) {
      return PlaybackPhase.recovering;
    }
    if (_loading) {
      return _switching ? PlaybackPhase.switching : PlaybackPhase.opening;
    }
    if (player.state.completed) return PlaybackPhase.completed;
    if (player.state.buffering) return PlaybackPhase.buffering;
    if (!_playIntent || _pausedByUser) return PlaybackPhase.paused;
    return PlaybackPhase.playing;
  }

  void _notify() {
    if (_closed) return;
    _setPhase(_derivePhase());
    notifyListeners();
  }

  void _accessChanged() {
    if (_closed) return;
    if (store.profileEpoch != _epoch || store.locked) {
      _preloader.clear();
      _dropStandby();
      _playIntent = false;
      endBoost();
      unawaited(player.pause());
      _notify();
    }
    if (store.profileEpoch != _epoch ||
        store.locked ||
        !store.allowsSource(SourceSite.hongguo.id)) {
      danmaku.setPlan(null);
    }
  }

  void _syncBlockedWords() {
    if (_closed) return;
    danmaku.setBlockedWords(store.playbackPreferences.danmakuWords);
  }

  void _sync() {
    _syncDanmaku();
    _syncPreload();
  }

  void _syncDanmaku() {
    if (_closed) return;
    final state = player.state;
    danmaku.update(
      position: state.position,
      duration: state.duration,
      speed: state.rate,
      playing: state.playing && _playIntent && !state.completed,
      buffering: state.buffering,
      foreground: _foreground,
      available:
          ready &&
          _openedIndex == _index &&
          _usable &&
          !store.locked &&
          store.allowsSource(SourceSite.hongguo.id),
    );
  }

  void _syncPreload() {
    if (_closed) return;
    // 预加载结果只对本次会话有效：切集后旧会话的解析结果一律丢弃。
    final ticket = _generation;
    if (!_foreground ||
        _plan?.local == true ||
        !ready ||
        _openedIndex != _index ||
        !_usable ||
        !_engaged ||
        store.locked ||
        !hasNext) {
      _preloader.clear();
      return;
    }
    final state = player.state;
    final duration = state.duration.inMilliseconds;
    final position = state.position.inMilliseconds;
    final ahead = state.buffer.inMilliseconds - position;
    if (_standby != null) {
      if (_standbyOpen &&
          DateTime.now().difference(_standbyAt) > const Duration(minutes: 8)) {
        _dropStandby();
      }
      if (_standbyIndex >= 0 || duration <= 0 || ahead < 3000) return;
    } else {
      if (!state.playing || state.buffering || !_playIntent) {
        _preloader.pause();
        return;
      }
      if (duration <= 0 ||
          position < 2000 ||
          (position < duration ~/ 2 && duration - position > 45000) ||
          ahead < 5000) {
        return;
      }
    }
    _preloader.prepare(
      detail.drama,
      detail.episodes[_index + 1],
      quality: _quality,
      online: _online,
      isCurrent: () => !_closed && ticket == _generation,
    );
  }

  void _playerError(String error) {
    if (_closed || !_acceptErrors || error.trim().isEmpty) return;
    DiaryService.add('[Play] 播放器报错：${_diagnostic(error)}');
    if (isBenignPlayerError(error)) return;
    _queueRecovery('player_error');
  }

  void _completed(bool completed) {
    if (!completed || _loading || _closed || !_acceptErrors || _error != null) {
      return;
    }
    final duration = player.state.duration;
    if (duration <= Duration.zero ||
        player.state.position < duration - const Duration(seconds: 2)) {
      _queueRecovery('early_completion');
      return;
    }
    // 用户关掉自动连播时停在本集末尾，不再强制衔接（P1-9）。
    final advance = store.playbackPreferences.autoAdvance;
    if (!_foreground && advance && (hasNext || _nextSeason != null)) {
      // 后台播完不在后台发起解析（网络/音频都可能已被系统回收）：
      // 记下待办，回前台由 onLifecycleEvent 补切（P1-8）。
      _pendingCompleted = true;
      DiaryService.add('[Play] 后台播完，回前台补切 集=${_index + 1}');
    } else if (_foreground && advance && hasNext) {
      unawaited(switchTo(_index + 1, reason: SwitchReason.completed));
    } else if (_foreground && advance && _nextSeason != null) {
      // 本季已看完：同一站源内自动接上下一季第 1 集。
      unawaited(_playNextSeason());
    } else {
      DiaryService.add(
        '[Play] 本集播完不连播 季数=${detail.seasons.length} '
        '下一季=${_nextSeason?.label ?? '无'} 自动连播=$advance',
      );
      _playIntent = false;
      endBoost();
      unawaited(player.pause());
      unawaited(saveProgress());
      _notify();
    }
  }

  /// 详情页的季列表是异步加载的，可能在播放会话创建之后才到；
  /// 这里同步给它，供本季播完自动衔接下一季。
  void syncSeasons(List<DramaSeason> seasons) {
    if (_closed || seasons.length <= 1) return;
    final current = detail.seasons;
    if (current.length == seasons.length &&
        current.isNotEmpty &&
        current.first.drama.id == seasons.first.drama.id) {
      return;
    }
    detail = DramaDetail(
      detail.drama,
      detail.episodes,
      warning: detail.warning,
      seasons: seasons,
    );
    _notify();
  }

  /// 当前季在季列表中的下一季；单季或顺序不可知时返回 null。
  DramaSeason? get _nextSeason {
    final seasons = detail.seasons;
    if (seasons.length <= 1) return null;
    final index = seasons.indexWhere(
      (season) => season.drama.id == detail.drama.id,
    );
    if (index < 0 || index + 1 >= seasons.length) return null;
    return seasons[index + 1];
  }

  /// 还有下一季可供滑动衔接。
  DramaSeason? get nextSeason => _nextSeason;

  /// 正在切换到下一季：期间不要回跳播放器页码。
  bool get advancingSeason => _advancingSeason;

  /// 滑动到最后一集之后时调用，切到下一季第 1 集。
  Future<void> playNextSeason() => _playNextSeason();

  Future<void> _playNextSeason() async {
    if (_advancingSeason) return;
    final season = _nextSeason;
    if (season == null || _closed || !_foreground || !_usable) return;
    _advancingSeason = true;
    final ticket = _generation;
    DiaryService.add('[Play] 本季结束，切换到${season.label}');
    // 备用实例与预加载都属于上一季，换季前丢干净，避免命中旧剧集。
    _dropStandby();
    _preloader.clear();
    try {
      // 先把上一季的收尾进度按旧剧集记下来，再替换详情。
      await saveProgress();
      if (!_current(ticket)) return;
      final next = await repository.detail(season.drama);
      if (!_current(ticket) || _closed || !_foreground) return;
      if (next.episodes.isEmpty) {
        DiaryService.add('[Play] 下一季没有可播放的分集');
        _playIntent = false;
        endBoost();
        unawaited(player.pause());
        return;
      }
      detail = DramaDetail(
        repository.catalogUpdates.current(season.drama).merge(next.drama),
        next.episodes,
        warning: next.warning,
        seasons: detail.seasons,
      );
      _openedIndex = -1;
      _index = 0;
      _online = false;
      _playIntent = true;
      _notify();
      // 换季必须经统一切集入口：standby 属于上一季，禁止命中。
      await switchTo(0, reason: SwitchReason.season, allowStandby: false);
      if (_error?.contains('context canceled') ?? false) {
        // 换季解析被并发操作取消时重试一次，避免停在错误页。
        DiaryService.add('[Play] 换季解析被取消，重试一次');
        _error = null;
        _loading = false;
        await switchTo(0, reason: SwitchReason.season, allowStandby: false);
      }
    } catch (error) {
      DiaryService.add('[Play] 切换下一季失败：${_diagnostic(error)}');
      if (!_closed) {
        _playIntent = false;
        endBoost();
        unawaited(player.pause());
      }
    } finally {
      _advancingSeason = false;
      _notify();
    }
  }

  void _queueRecovery(String reason) {
    if (_closed || !_acceptErrors || _error != null) return;
    _pendingError = true;
    _notify();
    if (!_foreground || (_errorTimer?.isActive ?? false)) return;
    final ticket = _generation;
    final position = player.state.position;
    _errorTimer = Timer(const Duration(milliseconds: 900), () {
      if (!_current(ticket) || !_foreground || !_acceptErrors) return;
      _pendingError = false;
      final state = player.state;
      if (state.playing &&
          !state.buffering &&
          (state.width ?? 0) > 0 &&
          state.position > position + const Duration(milliseconds: 300)) {
        // 画面确实在推进，才说明这次重建真正成功；open() 返回成功
        // 不代表地址可用（连接被拒也要等到 mpv 异步报错）。
        _iosRebuilds = 0;
        _forceSoftwareDecode = false;
        // 画面真的在推进 = 本次恢复链已达成目标，清掉次数，
        // 否则下一次正常播放中的偶发故障会被旧计数直接判死。
        _recovery.reset();
        return;
      }
      unawaited(_recover(reason));
    });
  }

  Future<void> _recover(String reason) async {
    final current = _plan;
    if (_closed ||
        !_acceptErrors ||
        !_foreground ||
        current == null ||
        _error != null) {
      return;
    }
    final initialTicket = _generation;
    // 取一次最新的取流代次（有界 2s；查询失败沿用缓存值，不阻塞恢复）。
    await _refreshStreamStatus();
    if (_closed ||
        !_acceptErrors ||
        !_foreground ||
        _error != null ||
        !_current(initialTicket)) {
      return;
    }
    final state = player.state;
    final context = PlaybackRecoveryContext(
      now: DateTime.now(),
      networkReachable: _networkReachable,
      buffering: state.buffering,
      playing: state.playing,
      wanted: _playIntent && !_pausedByUser && !_rebuilding,
      hasAlternative: current.hasAlternative,
      streamEpochStale: current.streamEpoch > 0 &&
          _streamEpoch > 0 &&
          current.streamEpoch != _streamEpoch,
      local: current.local,
    );
    final decision = _recovery.start(reason: reason, context: context);
    if (decision == null) return; // 已有恢复任务在跑：单一互斥，不并发
    _acceptErrors = false;
    danmaku.setPlan(null);
    _errorTimer?.cancel();
    _pendingError = false;
    // 用本次播放请求的起点，而不是旧播放器的位置：切集失败时旧位置属于
    // 上一集，用它重开会直接跳到片尾之外。
    final position = _resumePosition;
    final ticket = _generation;
    DiaryService.add(
      '[Play] 自动恢复 原因=$reason ${decision.message} '
      '次数=${decision.attempt}/$_recoveryMaxAttempts '
      '退避=${decision.backoff.inMilliseconds}ms '
      '线路=${current.routeIndex + 1}/${current.routeCount} '
      '会话代次=${current.streamEpoch}/$_streamEpoch',
    );
    try {
      if (decision.backoff > Duration.zero) {
        await Future<void>.delayed(decision.backoff);
        if (_closed ||
            !_current(ticket) ||
            _recovery.activeToken != decision.token) {
          _recovery.cancel();
          return;
        }
      }
      if (decision.exhausted) {
        await _finishRecovery(decision, current, position, ticket);
        return;
      }
      // 第 1 级结论：画面其实已在推进，不做任何动作。
      if (decision.action == PlaybackRecoveryAction.detect &&
          context.alreadyHealthy) {
        _recovery.complete(decision, recovered: true);
        return;
      }
      switch (decision.action) {
        case PlaybackRecoveryAction.detect:
        case PlaybackRecoveryAction.probe:
          final probe = await _probeStream();
          DiaryService.add('[Play] 恢复检测级 取流探活=${probe.name}');
          if (probe == _StreamProbe.dead) {
            // 监听确认失效，中间级动作都没意义：下一次直接升到重建级。
            _recovery.complete(decision, recovered: false);
            _acceptErrors = true;
            _queueRecovery('probe_stream_dead');
            return;
          }
          break;
        case PlaybackRecoveryAction.replay:
          await _kickPlayer();
          break;
        case PlaybackRecoveryAction.reopen:
          await _reopenCurrentMedia(position, ticket);
          break;
        case PlaybackRecoveryAction.alternative:
        case PlaybackRecoveryAction.refresh:
          _recovery.complete(decision, recovered: false);
          _acceptErrors = true;
          await play(_index, position: position, recovery: decision.action);
          return;
        case PlaybackRecoveryAction.rebuild:
          _iosRebuilds++;
          if (_iosRebuilds >= 2) _forceSoftwareDecode = true;
          _recovery.complete(decision, recovered: false);
          _acceptErrors = true;
          await _rebuildAndResume(position, _playIntent);
          return;
        case PlaybackRecoveryAction.stop:
          await _finishRecovery(decision, current, position, ticket);
          return;
      }
      _recovery.complete(decision, recovered: false);
      _acceptErrors = true;
      // 动作本身不保证已恢复：900ms 后二次确认，画面推进就 reset，
      // 否则由协调器的 _level+1 保证单调升级。
      _queueRecovery('${decision.action.name}_failed');
    } catch (error) {
      _recovery.complete(decision, recovered: false);
      // 禁止静默吞异常：记日志，并让统一入口继续升级。
      DiaryService.add('[Play] 恢复动作异常：${_diagnostic(error)}');
      if (!_closed && _error == null) {
        _acceptErrors = true;
        _queueRecovery('recovery_exception');
      }
    } finally {
      if (!decision.exhausted && _error == null && !_closed) {
        _acceptErrors = true;
      }
      _notify();
    }
  }

  /// 恢复链终态：停止当前播放器、释放站源会话、落地可读错误与手动重试。
  Future<void> _finishRecovery(
    PlaybackRecoveryDecision decision,
    PlaybackPlan current,
    double position,
    int ticket,
  ) async {
    _recovery.complete(decision, recovered: false);
    _resumePosition = position;
    try {
      await _serialize(() async {
        if (!_current(ticket)) return;
        try {
          await saveProgress();
          _openedIndex = -1;
          if (!await _boundedStop(player, context: '恢复终止')) {
            // 终止恢复时 stop 挂起：原地实例已不可靠，标记 suspect 让下次
            // 手动重试/切集时走重建换一个干净实例。
            _playerSuspect = true;
          }
        } finally {
          await _release(current.session);
        }
      });
    } catch (error) {
      DiaryService.add('[Play] 恢复终止时停止播放器失败：${_diagnostic(error)}');
    }
    if (!_current(ticket)) return;
    _loading = false;
    _switching = false;
    _clearOpenBudget();
    _localFailure = current.local;
    _loginRequired = false;
    _error = decision.message;
    _notify();
  }

  /// 第 4 级：不换地址、不重建实例，只对当前 Media 重新 open + play。
  /// 旧 session 必须保留，否则本地取流网关的 token 立刻失效。
  Future<void> _reopenCurrentMedia(double position, int ticket) async {
    final plan = _plan;
    if (plan == null || plan.url.isEmpty) {
      throw AppFailure('当前播放地址已失效，需要重新解析');
    }
    await _serialize(() async {
      if (!_current(ticket)) return;
      await _guarded<void>(
        ticket,
        '重新打开媒体',
        () => player.open(
          Media(
            plan.url,
            httpHeaders: plan.headers,
            start: position > 0
                ? Duration(milliseconds: (position * 1000).round())
                : null,
          ),
          play: _foreground && _playIntent,
        ),
        timeout: _openTimeout,
      );
      if (_current(ticket) && _playIntent && _foreground) {
        await _guarded<void>(
          ticket,
          '重开后启动播放',
          () => player.play(),
          timeout: _playTimeout,
        );
      }
    });
  }

  /// 查询取流代次，用于判断旧播放会话是否已随监听重建而作废。
  Future<void> _refreshStreamStatus() async {
    try {
      final status = await repository
          .playbackStatus()
          .timeout(const Duration(seconds: 2));
      if (!_closed) _streamEpoch = status.epoch;
    } catch (error) {
      DiaryService.add('[Play] 取流代次查询失败：${_diagnostic(error)}');
    }
  }

  /// 网络可达性变化的唯一入口。
  ///
  /// 状态机本身已就绪：本方法会根据可达性决定 reset + 重试，或转
  /// `network_restored` 恢复。但 **iOS 原生网络监听尚未接线**——需要在
  /// AppDelegate/SceneDelegate 接入 `NWPathMonitor`，并通过 `EventChannel`
  /// 把路径变化转发到这里（macOS + Xcode 环境才能落地与验证，当前 Windows
  /// 环境仅由 Dart 单测驱动，原生接线未验证）。
  /// TODO(ios): 接入 NWPathMonitor → EventChannel → onNetworkChanged 的转发链路。
  void onNetworkChanged(bool reachable) {
    if (_networkReachable == reachable) return;
    _networkReachable = reachable;
    DiaryService.add('[Play] 网络状态变化 可达=$reachable 集=${_index + 1}');
    _notify();
    if (!reachable || _closed) return;
    // 网络恢复给一次全新的机会；已有错误时 _queueRecovery 会被 guard
    // 挡住，必须走与用户手动重试相同的 retry() 路径。
    _recovery.reset();
    if (_error != null) {
      unawaited(retry());
    } else if (_foreground) {
      _queueRecovery('network_restored');
    }
  }

  Future<void> _release(String session) async {
    if (session.isEmpty) return;
    try {
      await repository.release(session);
    } catch (error) {
      DiaryService.add('[Play] 释放站源会话失败：${_diagnostic(error)}');
    }
  }

  /// 等待旧实例真正释放，再让新实例打开媒体。
  ///
  /// dispose 超时**不代表原生侧结束**：此时不能摘掉映射，否则新会话会把
  /// 仍在卸载音频/纹理的旧实例当成可用对象。等原 Future 真正结束再摘，
  /// 既不阻塞恢复链，也不留下无主实例。
  Future<void> _retire(Player target) async {
    final watch = Stopwatch()..start();
    var retired = false;
    void retire() {
      if (retired) return;
      retired = true;
      _onRetired?.call(target);
    }

    final Future<void> future;
    try {
      future = target.dispose();
    } catch (error) {
      DiaryService.add('[Play] 释放播放器异常：${_diagnostic(error)}');
      retire();
      return;
    }
    try {
      await future.timeout(const Duration(milliseconds: 1500));
      retire();
    } on TimeoutException {
      DiaryService.add(
        '[Play] 释放播放器超时 用时=${watch.elapsedMilliseconds}ms，'
        '等原生 dispose 结束后再摘映射',
      );
      unawaited(
        future
            .then<void>(
              (_) {},
              onError: (Object error) {
                DiaryService.add('[Play] 延迟释放播放器失败：${_diagnostic(error)}');
              },
            )
            .whenComplete(retire),
      );
    } catch (error) {
      DiaryService.add(
        '[Play] 释放播放器异常 用时=${watch.elapsedMilliseconds}ms：'
        '${_diagnostic(error)}',
      );
      retire();
    }
  }

  /// 有界停止：所有 player.stop() 必须经此，杜绝 mpv 偶发 stop 挂起把
  /// [_serialize] 串行队列卡死、导致后续切集/重建/恢复全部排队失败。
  ///
  /// - 正常完成返回 true；调用方负责随后 [_retire] 释放实例。
  /// - 超时（[_stopTimeout]）返回 false：旧实例可能仍在卸载音频/纹理，
  ///   绝不能继续在原实例上 open 新媒（会和新实例竞争），改为异步隔离释放，
  ///   让调用方的新播放任务照常推进；调用方应根据返回值转受控重建。
  Future<bool> _boundedStop(Player target, {required String context}) async {
    try {
      await target.stop().timeout(_stopTimeout);
      return true;
    } on TimeoutException {
      DiaryService.add(
        '[Play] $context stop 超时 ${_stopTimeout.inMilliseconds}ms，隔离旧实例',
      );
      // 旧实例不能与新实例竞争资源：异步释放，不阻塞当前串行任务。
      unawaited(_retire(target));
      return false;
    } catch (error) {
      DiaryService.add('[Play] $context stop 异常：${_diagnostic(error)}');
      return false;
    }
  }

  Future<void> _serialize(Future<void> Function() operation) {
    final next = _operations
        .then<void>(
          (_) {},
          onError: (Object error) {
            DiaryService.add('[Play] 上一个串行任务失败：${_diagnostic(error)}');
          },
        )
        .then((_) => operation());
    _operations = next;
    return next;
  }

  /// 统一的异步任务护栏。
  ///
  /// Future.timeout 不会取消原始任务：超时后只标记本次票据作废，原始
  /// Future 留在后台自行结束，并在返回时丢弃结果、释放它创建的资源。
  /// 任何晚于新会话返回的结果都不得写回 _plan / _index / _loading。
  Future<T> _guarded<T>(
    int ticket,
    String label,
    Future<T> Function() work, {
    required Duration timeout,
    void Function(T value)? onLate,
  }) async {
    final future = work();
    try {
      final value = await future.timeout(timeout);
      if (!_current(ticket)) {
        onLate?.call(value);
        throw PlaybackTaskTimeout(label, timeout);
      }
      return value;
    } on TimeoutException {
      unawaited(
        future.then<void>(
          (value) {
            DiaryService.add('[Play] 过期任务已返回并丢弃 任务=$label 票据=$ticket');
            onLate?.call(value);
          },
          onError: (Object error) {
            DiaryService.add(
              '[Play] 过期任务失败并丢弃 任务=$label 票据=$ticket：'
              '${_diagnostic(error)}',
            );
          },
        ),
      );
      throw PlaybackTaskTimeout(label, timeout);
    }
  }

  /// 整次打开的兜底预算：resolve / open / play 任一环节挂起，都不能让
  /// 播放页一直转圈。预算耗尽即作废票据并落地可读错误 + 手动重试。
  void _armOpenBudget(int ticket) {
    _clearOpenBudget();
    _openStartedAt = DateTime.now();
    _openBudgetTimer = Timer(_openBudget, () => _onOpenBudgetExpired(ticket));
  }

  void _clearOpenBudget() {
    _openBudgetTimer?.cancel();
    _openBudgetTimer = null;
    _openStartedAt = null;
  }

  void _onOpenBudgetExpired(int ticket) {
    _openBudgetTimer = null;
    if (_closed || !_current(ticket) || !_loading) return;
    final started = _openStartedAt;
    DiaryService.add(
      '[Play] 打开预算耗尽 预算=${_openBudget.inMilliseconds}ms '
      '已等待=${started == null ? 0 : DateTime.now().difference(started).inMilliseconds}ms '
      '集=${_index + 1}，作废当前任务并落地可读错误',
    );
    _clearOpenBudget();
    // 作废票据：晚返回的 resolve/open/play 一律不得再写状态。
    _generation++;
    _loader.invalidate();
    _acceptErrors = false;
    _pendingError = false;
    _errorTimer?.cancel();
    _errorTimer = null;
    _switching = false;
    _loading = false;
    _error = '播放准备超时，请检查网络后重试';
    _notify();
  }

  /// 第 1/2/3 级恢复的入口：真实检测到持续缓冲后，先只重发播放指令，
  /// 既不换线路也不重建播放器；连续多次仍无效才交给恢复链。
  bool _checkBuffering(DateTime now) {
    if (!_health.bufferingStalled(
      buffering: player.state.buffering,
      wanted: _playIntent && _foreground && !_pausedByUser && !_rebuilding,
      now: now,
    )) {
      return false;
    }
    _health.resetBuffering();
    _bufferingRecoveries++;
    DiaryService.add(
      '[Play] 持续缓冲超过 ${_bufferingBudget.inMilliseconds}ms，'
      '进入检测/重发播放级 次数=$_bufferingRecoveries 集=${_index + 1}',
    );
    if (_bufferingRecoveries <= _maxBufferingRecoveries) {
      _bufferingRecovering = true;
      _notify();
      unawaited(_kickPlayer());
    } else {
      _bufferingRecovering = false;
      _queueRecovery('buffering_timeout');
    }
    return true;
  }

  Future<void> _configure(Player target, PlaybackPlan plan) async {
    final platform = target.platform;
    if (platform is! NativePlayer) return;
    final hwdec = _forceSoftwareDecode
        ? 'no'
        : switch (store.playbackPreferences.televisionDecoder) {
            TelevisionDecoder.software => 'no',
            TelevisionDecoder.hardware when Platform.isAndroid =>
              'mediacodec-copy',
            TelevisionDecoder.hardware
                when Platform.isIOS || Platform.isMacOS =>
              'videotoolbox-copy',
            _ when Platform.isWindows => 'd3d11va-copy',
            _ when Platform.isAndroid => 'auto-safe',
            _ when Platform.isMacOS => 'auto-safe',
            _ => null,
          };
    if (hwdec != null) await platform.setProperty('hwdec', hwdec);
    if (Platform.isAndroid) {
      await platform.setProperty('hwdec-codecs', 'all');
      await platform.setProperty('vd-lavc-skiploopfilter', 'nonkey');
      await platform.setProperty('vd-lavc-threads', '4');
      await platform.setProperty('vd-lavc-fast', 'yes');
      await platform.setProperty('video-sync', 'audio');
      await platform.setProperty('framedrop', 'vo');
    }
    await platform.setProperty(
      'demuxer-lavf-o',
      [
        'seg_max_retry=3',
        'strict=experimental',
        'allowed_extensions=ALL',
        plan.local
            ? 'protocol_whitelist=[file,crypto,data]'
            : 'protocol_whitelist=[http,https,tcp,tls,crypto,data,file]',
        if (plan.decryptionKey.isNotEmpty)
          'decryption_key=${plan.decryptionKey}',
      ].join(','),
    );
    await platform.setProperty('network-timeout', '20');
  }

  /// 对外唯一的切集入口。
  ///
  /// 选集、上一集/下一集、滑动翻页、自动连播、换季、键盘、带进度进入
  /// 全部经由这里：统一做越界校验、恢复任务作废、切集打点与 standby 策略。
  /// `play()` 保留为底层「完整打开」实现，只供恢复链 / 重试 / 初始加载使用；
  /// UI 层不得再直接调用它，否则又会出现互不相同的边界判断。
  Future<void> switchTo(
    int index, {
    double position = 0,
    SwitchReason reason = SwitchReason.other,
    bool allowStandby = true,
    bool playWhenReady = true,
  }) async {
    if (_closed || !_usable) return;
    // 任何主动切集都作废「后台播完回前台补切」的待办，避免重复连播。
    _pendingCompleted = false;
    final count = detail.episodes.length;
    if (index < 0 || index >= count) {
      DiaryService.add('[Play] 忽略越界切集 目标=$index 集数=$count 原因=${reason.name}');
      return;
    }
    DiaryService.add(
      '[Play] 切集 ${_index + 1} → ${index + 1} 原因=${reason.name} '
      '位置=${position.toStringAsFixed(2)} standby=$allowStandby',
    );
    return play(
      index,
      position: position,
      playWhenReady: playWhenReady,
      allowStandby: allowStandby,
      switchReason: reason,
    );
  }

  /// 底层「完整打开」实现。切集请走 [switchTo]。
  Future<void> play(
    int index, {
    double position = 0,
    PlaybackRecoveryAction? recovery,
    bool playWhenReady = true,
    bool allowStandby = true,
    SwitchReason? switchReason,
  }) async {
    if (!_usable || index < 0 || index >= detail.episodes.length) return;
    // 是否已经打开过某一集，决定 phase 落在 opening 还是 switching。
    final switching = _openedIndex >= 0 && index != _openedIndex;
    // 用户主动开新集/重试：作废还在等待的恢复任务，避免它反向覆盖。
    _restoreToken++;
    endBoost();
    _staleAudio = false;
    _pausedByUser = false;
    if (_playerSuspect) await _replacePlayer();
    if (index != _index) _online = false;
    if (recovery == null &&
        allowStandby &&
        !_standbyHung &&
        position == 0 &&
        _standbyOpen &&
        _standbyIndex == index &&
        !_online) {
      return _swap(index, playWhenReady, switchReason);
    }
    _dropStandby();
    final warmed = recovery == null
        ? _preloader.take(
            detail.drama,
            detail.episodes[index],
            quality: _quality,
            online: _online,
          )
        : null;
    _preloader.clear();
    final ticket = ++_generation;
    _seekSequence++;
    danmaku.setPlan(null);
    _acceptErrors = false;
    _pendingError = false;
    _errorTimer?.cancel();
    _health.reset();
    _health.resetBuffering();
    if (recovery == null) {
      // 用户主动开新集/切集/重试：作废在途恢复任务，再给一条干净的链。
      _recovery.cancel();
      _recovery.reset();
      _playIntent = playWhenReady;
    }
    _resumePosition = position;
    _openedAt = position;
    _index = index;
    _switching = switching;
    _bufferingRecovering = false;
    _bufferingRecoveries = 0;
    _loading = true;
    _error = null;
    _localFailure = false;
    _loginRequired = false;
    _loadingMessage = switch (recovery) {
      PlaybackRecoveryAction.alternative => '正在切换备用线路',
      PlaybackRecoveryAction.refresh => '正在重新获取播放地址',
      PlaybackRecoveryAction.reopen => '正在重新打开媒体',
      PlaybackRecoveryAction.rebuild => '正在重建播放器',
      _ => '正在准备播放',
    };
    _notify();
    // 整次打开的兜底预算：任何一环挂起都必须有终点，不能永久 Loading。
    _armOpenBudget(ticket);
    PlaybackPlan? prepared;
    PlaybackPlan? retained;
    var installed = false;
    try {
      // open 新 URL 会重建 texture，盖黑幕遮花屏；同时作废上一集残留的尺寸证据。
      _coverSurface();
      _beginNewFrame();
      var stopped = true;
      await _serialize(() async {
        if (!_current(ticket)) return;
        await saveProgress();
        if (!_current(ticket)) return;
        _openedIndex = -1;
        stopped = await _boundedStop(player, context: '切集停止');
        final previous = _plan;
        _plan = null;
        if (recovery == PlaybackRecoveryAction.alternative) {
          retained = previous;
        } else if (previous != null) {
          await _release(previous.session);
        }
      });
      if (!_current(ticket)) return;
      if (!stopped && _respawn != null) {
        // 当前实例 stop 未完成、不可靠：不能在旧实例上 open 新媒，转受控重建
        // 换一个干净实例（_boundedStop 超时已异步隔离旧实例，不与之竞争）。
        await _rebuildAndResume(position, playWhenReady);
        return;
      }
      prepared = await _guarded<PlaybackPlan?>(
        ticket,
        '解析播放地址',
        () => retained != null
            ? _loader.fallback(retained!, isCurrent: () => _current(ticket))
            : warmed != null
            ? _loader.use(warmed, isCurrent: () => _current(ticket))
            : _loader.load(
                detail.drama,
                detail.episodes[index],
                quality: _quality,
                online: _online,
                // 第 5 级 refresh：站源地址可能已过期，强制跳过本地
                // 下载缓存重新解析，而不是拿旧地址再开一次。
                force: recovery == PlaybackRecoveryAction.refresh,
                isCurrent: () => _current(ticket),
              ),
        timeout: _resolveTimeout,
        onLate: (plan) {
          // 晚于新会话返回的解析结果必须释放它创建的原生 session。
          if (plan != null) unawaited(_release(plan.session));
        },
      );
      if (prepared == null) return;
      final plan = prepared;
      await _serialize(() async {
        if (!_current(ticket)) {
          await _release(plan.session);
          return;
        }
        if (plan.url.isEmpty) throw AppFailure('站源未返回播放地址，请重试');
        await _configure(player, plan);
        await _limitCache(player, !_engaged);
        _plan = plan;
        installed = true;
        _acceptErrors = true;
        await _guarded<void>(
          ticket,
          '打开媒体',
          () => player.open(
            Media(
              plan.url,
              httpHeaders: plan.headers,
              start: position > 0
                  ? Duration(milliseconds: (position * 1000).round())
                  : null,
            ),
            play: _foreground && _playIntent,
          ),
          timeout: _openTimeout,
        );
        // open(play: true) 只是在 mpv open 之前先设 pause=false。
        // mpv 真正进入播放态要等 demux/decoder init 完成。
        // 显式再 play 一次，确保指令不会被 mpv 初始化覆盖。
        if (_playIntent && _foreground) {
          await _guarded<void>(
            ticket,
            '启动播放',
            () => player.play(),
            timeout: _playTimeout,
          );
        }
        if (!_current(ticket)) return;
        _openedIndex = index;
        _health.reset();
        await _setRate(_rate);
        if (!_current(ticket)) return;
        _loading = false;
        _switching = false;
        _clearOpenBudget();
        _notify();
        danmaku.setPlan(plan);
        _syncDanmaku();
      });
      if (_current(ticket) && _playIntent) unawaited(_ensurePlaying(ticket));
    } catch (error) {
      if (_current(ticket)) {
        if (prepared != null && identical(_plan, prepared)) {
          DiaryService.add('[Play] 打开播放地址失败：${_diagnostic(error)}');
          _acceptErrors = true;
          _queueRecovery('open_exception');
        } else {
          if (prepared != null) await _release(prepared.session);
          if (_current(ticket)) {
            DiaryService.add('[Play] 无法播放：${_diagnostic(error)}');
            _loading = false;
            _switching = false;
            _clearOpenBudget();
            _localFailure = error is AppFailure && error.code == 'local_media';
            _loginRequired =
                error is AppFailure && error.code == 'source_login';
            _error = error is PlaybackTaskTimeout
                ? '${error.label}超时，请检查网络后重试'
                : error is AppFailure
                ? error.message
                : '无法播放这一集，请重试或换一集。';
            _notify();
          }
        }
      } else if (prepared != null && !installed) {
        await _release(prepared.session);
      }
    } finally {
      if (warmed != null && !installed) await _release(warmed.session);
      if (retained != null) await _release(retained!.session);
    }
  }

  Future<void> _swap(
    int index,
    bool playWhenReady,
    SwitchReason? reason,
  ) async {
    final plan = _standbyPlan!;
    final next = _standby!;
    final previous = _player;
    final previousPlan = _plan;
    final volume = previous.state.volume;
    unawaited(saveProgress());
    _preloader.clear();
    // 待机播放器已经 open 且纹理上已有首帧，swap 只是把它从暂停切到播放，
    // 没有纹理重建，所以这里不再盖黑幕——否则竖屏滑到下一集时会
    // 出现「画面 → 黑 → 画面」的闪一下。
    final ticket = ++_generation;
    DiaryService.add(
      '[Play] 命中待机播放器 集=${index + 1} 原因=${reason?.name ?? 'other'}',
    );
    _seekSequence++;
    _pendingError = false;
    _errorTimer?.cancel();
    _health.reset();
    _health.resetBuffering();
    _recovery.reset();
    _playIntent = playWhenReady;
    _resumePosition = 0;
    _openedAt = 0;
    _index = index;
    _error = null;
    _localFailure = false;
    _loginRequired = false;
    _loading = false;
    _switching = false;
    _clearOpenBudget();
    _standbyTicket++; // 作废针对旧待机实例的在途预打开任务
    _standbyPlan = null;
    _standbyIndex = -1;
    _standbyOpen = false;
    _standbyBusy = true; // 旧播放器正在 stop，期间不让 _prime 用它
    _standbyState = _StandbyState.stopping; // stop 完成前禁止 _prime 复用该实例
    _player = next;
    _standby = previous;
    _plan = plan;
    _bind();
    _openedIndex = index;
    _acceptErrors = true;
    final stopping = previous.stop().catchError((Object _) {});
    var abandoned = false;
    unawaited(
      stopping.whenComplete(() {
        // 超时放弃交换后，旧 stop 晚完成也不许在已重建的状态上触发预加载，
        // 更不能把刚挂住的实例重新放回 standby 位。
        if (abandoned || !_current(ticket)) return;
        _standbyBusy = false;
        _standbyState = _StandbyState.idle; // stop 完成，允许 _prime 重新预打开
        _prime(); // stop 完了，立刻尝试预加载下一集
      }),
    );
    if (previousPlan != null) unawaited(_release(previousPlan.session));
    danmaku.setPlan(plan);
    _notify();
    _syncDanmaku();
    try {
      await _limitCache(next, false);
      await next.setVolume(volume);
      await _setRate(_rate);

      // 等待上一集播放器完全停止，再启动新一集，避免两个播放器
      // 在换集瞬间同时处于播放启动阶段造成状态竞争。
      // mpv 偶发 stop 挂起：超时后不能继续普通交换（旧实例可能还在卸载
      // 音频/解码资源），放弃这次交换并走受控重建。
      try {
        await stopping.timeout(_swapStopTimeout);
      } catch (error) {
        abandoned = true;
        _standbyBusy = false;
        DiaryService.add(
          '[Play] 旧播放器 stop 超时或失败，放弃交换改用重建：'
          '${_diagnostic(error)}',
        );
        // 挂住的实例不能再待机复用：摘下待机位并异步释放。
        final hung = _standby;
        _standby = null;
        if (hung != null) unawaited(_retire(hung));
        // 待机播放器已经打开过媒体，重建时必须从它实际所在的位置继续，
        // 不能写死 0 —— 否则用户看到「跳回开头」甚至进度丢失。
        final resumeAt = next.state.position.inMilliseconds / 1000.0;
        DiaryService.add(
          '[Play] 交换失败改走重建 恢复位置=${resumeAt.toStringAsFixed(2)}s '
          '集=${index + 1}',
        );
        await _rebuildAndResume(resumeAt, playWhenReady);
        return;
      }

      // 旧实例 stop（音频会话已完全卸载）之后，再恢复新实例的音频轨：
      // 音频输出在此刻才初始化，避开 ao_audiounit setActive(NO) 的
      // 进程级竞态（上游 Xanerix/guoapp#94）。
      await _setAudioTrackEnabled(next, true);

      if (_current(ticket) && _foreground && _playIntent) {
        await next.play();
        unawaited(_ensurePlaying(ticket));
      }
    } catch (error) {
      DiaryService.add('[Play] 交换播放器失败：${_diagnostic(error)}');
      if (_current(ticket)) _queueRecovery('open_exception');
    }
  }

  /// 起播后持续健康检查，直到确认视频真正稳定播放。
  ///
  /// 关键修复：不是第一轮 position 推进就判定成功（那只是 mpv 起了个头，
  /// 可能 1 秒后又停），而是必须连续两轮 position 都推进才 return。
  /// 这样能覆盖"画面出来了但 1 秒后卡死"的窗口。
  Future<void> _ensurePlaying(int ticket) async {
    var last = player.state.position;
    var advancedCount = 0; // 连续 position 推进的计数
    var stalled = 0; // playing=true 时连续无推进的轮数
    var nudges = 0;
    var resynced = false;
    for (var attempt = 0; attempt < 8; attempt++) {
      await Future<void>.delayed(const Duration(milliseconds: 800));
      if (_closed ||
          ticket != _generation ||
          !_foreground ||
          !_playIntent ||
          _pausedByUser) {
        return;
      }
      final state = player.state;
      final advanced =
          state.position > last + const Duration(milliseconds: 120);
      last = state.position;

      // playing=true 且 position 推进 → 确认。
      // buffering 不清 advancedCount，因为刚起播 demux 会抖几次。
      // 只有 playing=false 才清——那是真的暂停了。
      if (state.playing) {
        if (advanced) {
          advancedCount++;
          stalled = 0;
          // 连续两轮 position 都推进 → 判定真正起播成功
          if (advancedCount >= 2) {
            DiaryService.add(
              '[Play] 起播确认成功 连续$advancedCount轮position推进 '
              '位置=${state.position} 集=${_index + 1}',
            );
            // 恢复动作后的真实起播确认：整条恢复链清零，
            // 避免下一集/下一次偶发故障继承已消耗的次数。
            _recovery.reset();
            return;
          }
          continue;
        }
        // playing=true 但 position 不动：demux 短暂静默很常见（尤其是
        // 换播放器实例起播或刚回前台），单轮不踢——连续两轮无推进才
        // 认定真的卡住，否则 pause/play 误踢会表现为"放一下卡一下"。
        stalled++;
      } else {
        // playing=false 且非 buffering → 真的停了，清计数
        stalled = 0;
        if (!state.buffering) advancedCount = 0;
      }

      // 正在缓冲说明网络在拉数据，别打断它，buffering 也不清 advancedCount。
      if (state.buffering) continue;

      // 触发救援
      try {
        if (!state.playing) {
          DiaryService.add(
            '[Play] 起播救援 #${attempt + 1} playing=false 补发play '
            '位置=${state.position} 集=${_index + 1}',
          );
          await player.play();
        } else if (stalled >= 2 && nudges < 2) {
          nudges++;
          DiaryService.add(
            '[Play] 起播救援 #${attempt + 1} 连续$stalled轮位置不动 踢解码器 '
            '位置=${state.position} 集=${_index + 1}',
          );
          await _kickPlayer();
        } else if (stalled >= 2 && !resynced) {
          resynced = true;
          DiaryService.add(
            '[Play] 起播救援 #${attempt + 1} 持续位置不动 seek+play 集=${_index + 1}',
          );
          await player.seek(state.position);
          await player.play();
        }
      } catch (_) {
        // 不要因为一次临时异常就放弃——继续下一轮救援，总有人能打中。
      }
    }
    DiaryService.add(
      '[Play] 起播救援耗尽 playing=${player.state.playing} '
      '位置=${player.state.position} buffering=${player.state.buffering} '
      'duration=${player.state.duration} 集=${_index + 1}',
    );
    final state = player.state;
    if (state.playing || state.buffering) return;
    // 救援耗尽仍然停着：交给有限的自动恢复链（换线路/刷新/重建）。
    // 不能静默结束——健康检测只认 playing=true 的停滞，发现不了这种状态。
    _queueRecovery('ensure_playing_exhausted');
  }

  /// 状态显示"播放中"但画面定住时，暂停再播放把卡住的解码器踢一下。
  Future<void> _kickPlayer() async {
    try {
      await player.pause();
      await player.play();
    } catch (error) {
      DiaryService.add('[Play] 重发播放指令失败：${_diagnostic(error)}');
    }
  }

  void _prime() {
    final standby = _standby;
    final next = _index + 1;
    if (_closed ||
        standby == null ||
        _standbyIndex >= 0 ||
        _standbyBusy || // 旧播放器 stop 未完，等完再说
        _standbyState != _StandbyState.idle || // stopping/opening/opened/retiring 都禁止复用
        _standbyHung || // 预打开挂起过：只解析地址，不再复用待机实例
        !_preloader.ready ||
        next >= detail.episodes.length) {
      return;
    }
    _standbyState = _StandbyState.opening; // 进入预打开，期间禁止再次复用
    _standbyIndex = next;
    final plan = _preloader.take(
      detail.drama,
      detail.episodes[next],
      quality: _quality,
      online: _online,
    );
    if (plan == null) {
      _standbyIndex = -1;
      return;
    }
    _standbyPlan = plan;
    unawaited(_open(standby, plan, ++_standbyTicket));
  }

  /// 待机预打开。带独立票据与超时：任何失败都只作废这一份待机计划，
  /// 绝不写回主播放状态，也绝不让挂起的 open 占住待机位。
  Future<void> _open(Player standby, PlaybackPlan plan, int ticket) async {
    bool current() =>
        !_closed &&
        ticket == _standbyTicket &&
        identical(_standbyPlan, plan) &&
        identical(_standby, standby);
    try {
      await Future<void>(() async {
        await _configure(standby, plan);
        if (!current()) return;
        // 待机播放器也保留完整缓存：限到 4MB 时高倍速/弱网切过去只缓冲了一两秒，
        // 首帧播完就断流，表现为卡在第 1 秒。
        await _limitCache(standby, false);
        if (!current()) return;
        // 待机只做预缓冲：禁用音频轨，预加载期不初始化音频输出。
        // iOS 上 mpv 的 ao_audiounit 在卸载文件时会调用进程级
        // AVAudioSession.setActive(NO)，待机实例若已启动音频输出会被
        // 正在播的实例 stop 时一起杀掉，新集以音频为主时钟会定格在开头
        // （上游 Xanerix/guoapp#94）。
        await _setAudioTrackEnabled(standby, false);
        if (!current()) return;
        await standby.open(
          Media(plan.url, httpHeaders: plan.headers),
          play: false,
        );
      }).timeout(_standbyOpenTimeout);
      if (!current()) return;
      _standbyOpen = true;
      _standbyState = _StandbyState.opened; // 预打开完成，可被 swap 接管
      _standbyAt = DateTime.now();
      _notify();
    } catch (error) {
      if (!current()) return;
      final timedOut = error is TimeoutException;
      DiaryService.add(
        '[Play] 待机预打开${timedOut ? '超时' : '失败'}，降级为只解析地址：'
        '${_diagnostic(error)}',
      );
      _dropStandby();
      if (timedOut) {
        // open 挂起的实例状态未知，不能放回待机位复用（会泄漏/串音）。
        // 真正 dispose，并保留引用直到会话结束做二次兜底。
        _standbyHung = true;
        _standbyState = _StandbyState.retiring; // 隔离释放中，绝不再复用
        // 置空后 dispose() 不会再二次释放它（_retire 已异步接管）。
        _standby = null;
        unawaited(_retire(standby));
      }
    }
  }

  void _dropStandby() {
    // 递增票据，作废所有在途的待机打开任务，避免晚完成后写回旧状态。
    _standbyTicket++;
    final plan = _standbyPlan;
    final opened = _standbyOpen || plan != null;
    _standbyPlan = null;
    _standbyIndex = -1;
    _standbyOpen = false;
    if (opened) {
      final target = _standby;
      if (target != null) {
        // 进入 stopping：stop 未完成前 _prime 禁止复用该实例（见 [_StandbyState]）。
        _standbyState = _StandbyState.stopping;
        unawaited(
          target.stop().then<void>(
            (_) {
              // stop 完成时清理状态：同实例才回到 idle，让 _prime 重新预打开。
              if (identical(_standby, target)) {
                _standbyState = _StandbyState.idle;
              }
            },
            onError: (Object error) {
              DiaryService.add('[Play] 待机播放器 stop 失败：${_diagnostic(error)}');
              if (identical(_standby, target)) {
                _standbyState = _StandbyState.idle;
              }
            },
          ),
        );
      }
    }
    if (plan != null) unawaited(_release(plan.session));
  }

  Future<void> _limitCache(Player target, bool limited) async {
    final platform = target.platform;
    if (platform is! NativePlayer) return;
    await platform.setProperty(
      'demuxer-max-bytes',
      '${(limited ? 4 : 32) * 1024 * 1024}',
    );
    // 关键：max-bytes 只是缓冲上限，mpv 的 demuxer-readahead-secs（默认 1s）
    // 决定实际预读量——暂停态的待机播放器只预读当前位置前方约 1 秒就停。
    // 预加载/换集路径（limited=false）必须把预读窗口放大，待机切进来才
    // 有真缓冲可播，否则首帧播完停 1~2 秒等网络。32MB 上限仍兜住内存。
    await platform.setProperty('demuxer-readahead-secs', limited ? '1' : '30');
  }

  /// 开关播放器的音频轨。预加载的待机实例以 aid=no 打开（只缓冲不出声），
  /// 换集等旧实例 stop 完成后再恢复 aid=auto，让音频输出干净地重新初始化。
  Future<void> _setAudioTrackEnabled(Player target, bool enabled) async {
    final platform = target.platform;
    if (platform is! NativePlayer) return;
    try {
      await platform.setProperty('aid', enabled ? 'auto' : 'no');
    } catch (_) {}
  }

  void start(int index, {double position = 0}) {
    if (!_usable) return;
    _restoreToken++;
    _pausedByUser = false;
    final wasEngaged = _engaged;
    _engaged = true;
    if (index != _index ||
        (position - _openedAt).abs() > .5 ||
        _error != null) {
      unawaited(
        switchTo(
          index,
          position: position,
          reason: SwitchReason.start,
          allowStandby: false,
        ),
      );
      return;
    }
    _playIntent = true;
    if (!wasEngaged) unawaited(_limitCache(player, false));
    if (ready) {
      if (_playerSuspect) {
        unawaited(_rebuildAndResume(currentPosition, true));
      } else {
        unawaited(player.play());
      }
    }
    _notify();
  }

  Future<void> retry() =>
      play(_index, position: currentPosition, playWhenReady: true);

  Future<void> switchOnline() {
    _online = true;
    return retry();
  }

  Future<bool> next() async {
    if (!hasNext) return false;
    await switchTo(_index + 1, reason: SwitchReason.next);
    return true;
  }

  Future<bool> previous() async {
    if (!hasPrevious) return false;
    await switchTo(_index - 1, reason: SwitchReason.previous);
    return true;
  }

  void togglePlayback() {
    if (!_usable || !ready) return;
    _restoreToken++;
    endBoost();
    _playIntent = !player.state.playing;
    _pausedByUser = !_playIntent;
    _health.reset();
    // 播放时保持屏幕常亮，暂停时释放。
    unawaited(
      (_playIntent ? WakelockPlus.enable() : WakelockPlus.disable()).catchError(
        (Object _) {},
      ),
    );
    if (_playIntent && _playerSuspect) {
      // 离开过前台的实例可能已失效，直接重建并从当前位置续播。
      unawaited(_rebuildAndResume(currentPosition, true));
      return;
    }
    if (_playIntent && player.state.completed) {
      unawaited(play(_index));
      return;
    }
    if (_playIntent && _staleAudio) {
      _staleAudio = false;
      unawaited(player.seek(player.state.position).then((_) => player.play()));
    } else {
      unawaited(player.playOrPause());
    }
    if (!_playIntent) unawaited(saveProgress());
    _syncDanmaku();
    _notify();
  }

  Future<bool> seekTo(Duration target) async {
    if (!_usable || !ready) return false;
    final ticket = ++_seekSequence;
    final generation = _generation;
    danmaku.beginSeek();
    var succeeded = false;
    try {
      await player.seek(target);
      succeeded = true;
    } catch (_) {
    } finally {
      if (!_closed && ticket == _seekSequence && generation == _generation) {
        danmaku.endSeek(succeeded ? target : player.state.position);
      }
    }
    return succeeded;
  }

  Duration? seekBy(int seconds) {
    final duration = player.state.duration;
    if (!ready || duration <= Duration.zero) return null;
    final target = Duration(
      milliseconds: (player.state.position.inMilliseconds + seconds * 1000)
          .clamp(0, duration.inMilliseconds),
    );
    unawaited(seekTo(target));
    return target;
  }

  Future<void> _setRate(double value) {
    _rates = _rates
        .catchError((Object _) {})
        .then((_) => player.setRate(value));
    return _rates.catchError((Object _) {});
  }

  void beginBoost() {
    if (!ready || _boostTimer != null || _boosting) return;
    _boostTimer = Timer(const Duration(milliseconds: 350), () {
      _boostTimer = null;
      if (_closed ||
          !ready ||
          !player.state.playing ||
          player.state.completed) {
        return;
      }
      _boosting = true;
      unawaited(_setRate(2));
      _notify();
    });
  }

  void holdBoost() {
    if (!_usable || _boosting) return;
    _boostTimer?.cancel();
    _boostTimer = null;
    _boosting = true;
    unawaited(_setRate(2));
    _notify();
  }

  void setBoostLocked(bool locked) {
    if (_boostLocked == locked) return;
    if (locked) {
      holdBoost();
      _boostLocked = true;
      _notify();
    } else {
      _boostLocked = false;
      endBoost();
      _notify();
    }
  }

  bool endBoost() {
    if (_boostLocked) return false;
    final boosted = _boosting;
    _boostTimer?.cancel();
    _boostTimer = null;
    _boosting = false;
    if (boosted) {
      unawaited(_setRate(_speed));
      _notify();
    }
    return boosted;
  }

  void setVolume(double value) {
    final volume = value.clamp(0.0, 100.0);
    if (volume > 0) {
      _unmutedVolume = volume;
      _volumeTimer?.cancel();
      _volumeTimer = Timer(const Duration(milliseconds: 500), _saveVolume);
    }
    _applyVolume(volume);
  }

  void _saveVolume() {
    _volumeTimer = null;
    unawaited(store.setPlayerVolume(_unmutedVolume).catchError((Object _) {}));
  }

  void _applyVolume(double volume) {
    if (!player.state.playing) _staleAudio = true;
    unawaited(player.setVolume(volume));
  }

  double changeVolume(double delta) {
    final volume = (player.state.volume + delta).clamp(0.0, 100.0);
    setVolume(volume);
    return volume;
  }

  double toggleMute() {
    final current = player.state.volume;
    if (current > 0) _unmutedVolume = current;
    final target = current > 0 ? 0.0 : _unmutedVolume;
    _applyVolume(target);
    return target;
  }

  Future<void> _savePreferences(PlaybackPreferences preferences) async {
    if (!_usable) throw StateError('当前用户已变更');
    await store.setPlaybackPreferences(preferences);
    if (!_usable) throw StateError('当前用户已变更');
  }

  Future<void> setSpeed(double value) async {
    if (value == _speed) return;
    await _savePreferences(_preferences.copyWith(speed: value));
    _speed = value;
    _notify();
    if (!_boosting) await _setRate(value);
  }

  Future<void> setVideoFit(VideoFit value) async {
    if (value == _videoFit) return;
    await _savePreferences(_preferences.copyWith(videoFit: value));
    _videoFit = value;
    _notify();
  }

  Future<void> setQuality(int value) async {
    if (value == _quality) return;
    await _savePreferences(_preferences.copyWith(quality: value));
    _quality = value;
    _preloader.clear();
    _notify();
    if (_plan?.local == true) return;
    await play(
      _index,
      position: currentPosition,
      playWhenReady: _error != null || player.state.playing,
    );
  }

  Future<void> toggleDanmaku() async {
    if (!danmakuSupported) return;
    final enabled = !_danmakuEnabled;
    await _savePreferences(_preferences.copyWith(danmaku: enabled));
    _danmakuEnabled = enabled;
    danmaku.setEnabled(enabled);
    _syncDanmaku();
    _notify();
  }

  Future<void> setDanmakuScale(double value) async {
    final scale = value.clamp(danmakuScaleMin, danmakuScaleMax);
    await _savePreferences(_preferences.copyWith(danmakuScale: scale));
    _notify();
  }

  Future<void> setDanmakuColor(int value) async {
    await _savePreferences(_preferences.copyWith(danmakuColor: value));
    _notify();
  }

  Future<void> setDanmakuOpacity(double value) async {
    final opacity = value.clamp(.1, 1.0);
    await _savePreferences(_preferences.copyWith(danmakuOpacity: opacity));
    _notify();
  }

  Future<void> setDanmakuBold(bool value) async {
    await _savePreferences(_preferences.copyWith(danmakuBold: value));
    _notify();
  }

  Future<void> setDanmakuStroke(double value) async {
    final stroke = value.clamp(danmakuStrokeMin, danmakuStrokeMax);
    await _savePreferences(_preferences.copyWith(danmakuStroke: stroke));
    _notify();
  }

  Future<void> setDanmakuShadow(double value) async {
    final shadow = value.clamp(danmakuShadowMin, danmakuShadowMax);
    await _savePreferences(_preferences.copyWith(danmakuShadow: shadow));
    _notify();
  }

  Future<void> setDanmakuCoverage(DanmakuCoverage value) async {
    await _savePreferences(_preferences.copyWith(danmakuCoverage: value));
    _notify();
  }

  Future<void> setDanmakuWords(List<String> words) async {
    final normalized = normalizeDanmakuWords(words);
    await _savePreferences(_preferences.copyWith(danmakuWords: normalized));
    _notify();
  }

  /// 唯一的应用生命周期入口。所有页面的 WidgetsBindingObserver 只上报
  /// 原始 [AppLifecycleState]，不解释、不各自判定 PiP，口径统一在这里。
  ///
  /// 修复 P0-3：旧实现把 inactive 判为「前台」，只经历 inactive↔resumed
  /// 的锁屏/控制中心序列既不保存快照也不触发恢复；现在 inactive 显式进入
  /// preparing（存快照 + 抓帧），resumed 即使没经历过 paused 也会补一次
  /// 轻量恢复检查。
  void onLifecycleEvent(AppLifecycleState state, {String source = 'ui'}) {
    if (_closed) return;
    // 多个 observer 与未来的原生 EventChannel 会上报同一状态：只处理一次。
    if (_lastLifecycle == state) return;
    _lastLifecycle = state;
    _lifecycleEpoch++;
    switch (state) {
      case AppLifecycleState.resumed:
        _onResumed(source);
      case AppLifecycleState.inactive:
        _onInactive(source);
      case AppLifecycleState.paused:
      case AppLifecycleState.hidden:
      case AppLifecycleState.detached:
        _onBackground(state, source);
    }
  }

  /// PiP 由播放页唯一上报；session 本身不依赖 UI 层模块。
  void setPictureInPicture(bool active) {
    if (_closed || _pipActive == active) return;
    _pipActive = active;
    _lifecycleEpoch++;
    DiaryService.add('[Play] 画中画 ${active ? '开启' : '关闭'}');
    _applyForeground(_foregroundTarget(), reason: 'pip');
  }

  bool _foregroundTarget() {
    final state = _lastLifecycle;
    if (state == null) return true;
    return state == AppLifecycleState.resumed || _pipActive;
  }

  void _snapshotForBackground() {
    // 快照当前实例：此刻若 _swap 正并发执行到 _player = next，
    // 延迟解析 getter 会暂停到错误的对象上（音频多响几秒）。
    final player = this.player;
    final snapshot = player.state.position;
    if (_openedIndex == _index && snapshot > Duration.zero) {
      _resumePosition = snapshot.inMilliseconds / 1000;
    }
    // 只信用户意图：系统挂起/恢复期间底层 playing 可能与实际操作不一致。
    _resumeIntent = _playIntent;
  }

  void _onInactive(String source) {
    _lifecycleStage = PlaybackLifecycleStage.preparing;
    _snapshotForBackground();
    _lifecycleDormant = true;
    DiaryService.add(
      '[Play] 生命周期 inactive（准备离开前台）来源=$source 集=${_index + 1} '
      '位置=${_resumePosition.toStringAsFixed(1)}',
    );
    // 不改 _foreground、不 pause：iOS 此刻进程仍可渲染，
    // PlayerVideo 会抓最后一帧；直接 pause 反而让回前台多一次重建。
    _notify();
  }

  void _onBackground(AppLifecycleState state, String source) {
    if (_pipActive) {
      // PiP 播放中系统会把 app 判为后台，但小窗仍在出画面。
      _lifecycleStage = PlaybackLifecycleStage.foreground;
      DiaryService.add('[Play] 生命周期 ${state.name} 但 PiP 活跃，保持前台');
      _notify();
      return;
    }
    _lifecycleStage = PlaybackLifecycleStage.background;
    _applyForeground(false, reason: '${state.name}:$source');
  }

  void _onResumed(String source) {
    final wasForeground = _foreground;
    final dormant = _lifecycleDormant;
    _lifecycleDormant = false;
    _lifecycleStage = PlaybackLifecycleStage.foreground;
    DiaryService.add(
      '[Play] 生命周期 resumed 来源=$source 原前台=$wasForeground '
      '仅inactive=$dormant 待补切=$_pendingCompleted',
    );
    if (_pendingCompleted) {
      // 后台播完：优先补切，跳过对本集末尾的恢复（那只会白跑一次解析）。
      _pendingCompleted = false;
      if (store.playbackPreferences.autoAdvance) {
        if (hasNext) {
          unawaited(switchTo(_index + 1, reason: SwitchReason.completed));
          return;
        }
        if (_nextSeason != null) {
          unawaited(_playNextSeason());
          return;
        }
      }
    }
    if (!wasForeground) {
      _applyForeground(true, reason: 'resumed:$source');
      return;
    }
    if (dormant && _playIntent && _error == null && !_loading) {
      // P0-3：只经历 inactive↔resumed 时旧实现完全不动作。
      // 补一次轻量检查：盖幕 + 原地续播/探活，不销毁实例。
      _restoreToken++;
      _coverSurface();
      if (Platform.isIOS) {
        unawaited(_tryResumeInPlace(_restoreToken));
      } else {
        _queueRecovery('inactive_resume');
      }
    }
    _notify();
  }

  void _applyForeground(bool visible, {required String reason}) {
    if (_closed) return;
    if (_pipActive) visible = true;
    if (_foreground == visible) {
      _notify();
      return;
    }
    DiaryService.add('[Play] 前后台迁移 → ${visible ? '前台' : '后台'} 原因=$reason');
    setForeground(visible);
  }

  void setForeground(bool visible) {
    if (_closed || _foreground == visible) return;
    // 任何一次前后台变化都作废正在进行的恢复任务。
    _restoreToken++;
    _foreground = visible;
    _health.reset();
    _sync();
    if (!visible) {
      // 锁屏/切后台时直接读播放器状态，不等异步 position 事件。
      _snapshotForBackground();
      // 不主动 pause player——iOS 切后台时系统会挂起 AVPlayer 并保留
      // 最后一帧 pixel buffer。回前台直接 play() 就能无缝接上，不会花屏。
      endBoost();
      _playIntent = false;
      // 锁屏/切后台时释放屏幕常亮，省电。
      unawaited(WakelockPlus.disable().catchError((Object _) {}));
      DiaryService.add(
        '[Play] 进入后台，保存恢复状态 集数=${_index + 1} '
        '位置=${_resumePosition.toStringAsFixed(1)} 续播=$_resumeIntent',
      );
      // 所有平台都主动 pause，停音频/省电。
      // iOS 即使不 pause，后台几秒内音频也会继续响（系统不会立即挂起）。
      // texture 重建靠回前台时的黑幕（_coverSurface）遮花屏帧，position/playing
      // 推进后自动揭开，不影响视觉。
      unawaited(player.pause());
      unawaited(saveProgress());
      _notify();
      return;
    }
    _iosRebuilds = 0;
    _coverSurface();
    if (Platform.isIOS) {
      // 先尝试原地恢复（秒级出画面），1.5s 内位置没推进再走兜底链路。
      unawaited(_tryResumeInPlace(_restoreToken));
    } else if (_pendingError) {
      _queueRecovery('foreground_pending_error');
    }
    // _coverSurface 只在「首次盖上」时 notify；连续恢复时 phase 会
    // 滞留在 background，这里强制对齐一次。
    _notify();
  }

  bool _restorable(int token) =>
      !_closed && _foreground && token == _restoreToken;

  /// 进入新一轮画面：作废上一集残留的视频尺寸证据，等待新纹理自己报告。
  ///
  /// 必须在「新媒/新实例/重建」边界调用（[play]、[_replacePlayer]、
  /// [_rebuildAndResume]），确保 [_aspectKnown] 与 [_aspectEpoch] 绑定当前集，
  /// 而不是沿用上一集的尺寸——这是切集后 [frameReady] 误判未出画集已就绪的根因。
  /// 注意：前后台恢复（[setForeground]）复用同一实例、媒体未变，绝不能调用它，
  /// 否则恢复后尺寸证据被清空且 videoParams 不会再触发，导致 frameReady 永远为 false。
  void _beginNewFrame() {
    _frameEpoch++;
    _aspectKnown = false;
  }

  /// 盖黑幕遮住纹理重建时的花屏帧。
  /// 揭开时机：纹理报告了首帧参数（videoParams），或 position 相对盖幕时
  /// 的位置推进 >500ms（解码器确实在出帧）→ 立即揭；3s 仍无画面则
  /// 保留黑幕并进入恢复链，8s 硬兜底强制揭开。
  void _coverSurface() {
    _surfaceTimer?.cancel();
    _surfaceHardTimer?.cancel();
    _surfaceHardTimer = null;
    _surfaceTimedOut = false;
    _surfaceCoverAt = DateTime.now();
    // 记录盖幕时的播放位置：续播恢复时 position 本来就在中途，
    // 不能用「position > 500ms」判断（那只会对新起播有效），
    // 必须看相对推进量，否则重建未完成就揭幕会露出花屏帧。
    _surfaceCoverPosition = player.state.position;
    // 新一轮盖幕：首帧标记作废，等新纹理自己报告。
    _surfaceFrame = false;
    if (!_surfaceCover) {
      _surfaceCover = true;
      _notify();
    }
    // 3s 软兜底：旧实现到点强行揭幕，会露出还没就绪的花屏/黑帧。
    // 现在改为保留黑幕并交给恢复链；另设 5s 硬兜底，防止恢复链也
    // 拒绝执行时用户永远盯着黑幕。
    _surfaceTimer = Timer(_surfaceCoverTimeout, () {
      _surfaceTimer = null;
      if (_closed || !_surfaceCover) return;
      if (frameReady) {
        _revealSurface();
        return;
      }
      _surfaceTimedOut = true;
      DiaryService.add('[Play] 盖幕 3s 仍无首帧，保留黑幕并进入恢复链');
      _queueRecovery('surface_cover_timeout');
      _surfaceHardTimer = Timer(_surfaceHardTimeout, () {
        _surfaceHardTimer = null;
        if (_closed || !_surfaceCover) return;
        DiaryService.add('[Play] 盖幕硬兜底 8s，强制揭幕');
        _revealSurface();
      });
      _notify();
    });
  }

  /// 揭开黑幕的唯一出口，两个 timer 都必须在这里收干净。
  void _revealSurface() {
    _surfaceTimer?.cancel();
    _surfaceTimer = null;
    _surfaceHardTimer?.cancel();
    _surfaceHardTimer = null;
    _surfaceCover = false;
    _surfaceCoverAt = null;
    _surfaceFrame = false;
    _surfaceTimedOut = false;
    _notify();
  }

  /// 黑幕是否该揭开了。
  ///
  /// 只有「新纹理已经有画面」才揭：videoParams 报告了视频轨道参数，或者
  /// position 相对盖幕时刻推进超过 0.5s（没有出帧 position 不可能持续前进）。
  /// 单纯 playing=true 不算——解码器起播时纹理可能还是空的，提前揭开
  /// 会露出 1~2 帧黑屏，看起来就是画面闪一下。
  void _maybeRevealSurface() {
    final since = _surfaceCoverAt;
    if (!_surfaceCover || since == null) return;
    // 最小 150ms 保底，避免刚盖上又揭。
    if (DateTime.now().difference(since).inMilliseconds < 150) return;
    // 缓冲中说明画面还没稳定，揭开会露出空纹理。
    if (player.state.buffering) return;
    final position = player.state.position;
    if (!_surfaceFrame &&
        position < _surfaceCoverPosition + const Duration(milliseconds: 500)) {
      return;
    }
    _revealSurface();
  }

  /// iOS 前台快速路径：跳过探活，直接激活音频会话 + play()，
  /// 1.8s 内 position 推进即视为秒级恢复；失败自动 fallback 到
  /// [_restoreAfterBackground] 走完整探活 + 重建链路。
  Future<void> _tryResumeInPlace(int token) async {
    _restores++;
    try {
      await _resumeFast(token);
    } finally {
      _restores--;
    }
  }

  Future<void> _resumeFast(int token) async {
    final watch = Stopwatch()..start();
    final index = _index;
    final playIntent = _resumeIntent;
    final activated = await AppDevice.activateAudio();
    if (!_restorable(token)) return;
    if (!activated) {
      DiaryService.add('[Play] iOS 音频会话激活失败，继续尝试原地恢复');
    }
    if (!playIntent) {
      // 用户主动暂停且回来想继续暂停：不动实例，只清理状态。
      _playerSuspect = false;
      _pendingError = false;
      _errorTimer?.cancel();
      DiaryService.add(
        '[Play] 续播=false，保留原实例不动 耗时=${watch.elapsedMilliseconds}ms',
      );
      return;
    }
    final generation = _generation;
    final before = player.state.position;
    _playerSuspect = false;
    _pendingError = false;
    _errorTimer?.cancel();
    // 不在确认成功前清零失败计数：resume 一旦实际失败，提前 reset 会把未成功的
    // 恢复链洗白，后续偶发故障反而继承更少次数。
    _playIntent = true;
    _resumeIntent = true;
    _sync();
    _notify();
    Object? failure;
    try {
      await player.play();
    } catch (error) {
      failure = error;
    }
    if (!_restorable(token) || generation != _generation) {
      if (_pausedByUser && player.state.playing) {
        unawaited(player.pause());
      }
      return;
    }
    var advanced = false;
    if (failure == null) {
      // 快速观察窗口：4 × 500ms = 2s 内 position 推进即算成功。
      var nudges = 0;
      var stalled = 0;
      for (var attempt = 0; attempt < 4; attempt++) {
        await Future<void>.delayed(const Duration(milliseconds: 500));
        if (!_restorable(token) ||
            generation != _generation ||
            index != _index) {
          if (_pausedByUser && player.state.playing) {
            unawaited(player.pause());
          }
          return;
        }
        final state = player.state;
        if (state.playing && state.position > before + _restoreProgress) {
          advanced = true;
          break;
        }
        // 缓冲中属于正常等待，不要打断。
        if (state.buffering) continue;
        if (!state.playing) {
          try {
            await player.play();
          } catch (_) {
            break;
          }
        } else if (attempt >= 1) {
          // 状态是"播放中"但位置不走：单轮静默常见，连续两轮无推进
          // 才轻踢一次，避免刚回前台 demux 恢复期被误踢出卡顿感。
          stalled++;
          if (stalled >= 2 && nudges < 2) {
            nudges++;
            await _kickPlayer();
          }
        }
      }
    }
    if (advanced) {
      DiaryService.add('[Play] 原地秒恢复成功 耗时=${watch.elapsedMilliseconds}ms');
      // 确认真正恢复成功后才清零失败计数（见上方说明）。
      _recovery.reset();
      _health.reset();
      return;
    }
    // 恢复观察期间用户已明确暂停：放弃重建，尊重暂停意图。
    if (_pausedByUser || !_playIntent) {
      DiaryService.add('[Play] 快速恢复期间用户暂停，取消兜底重建');
      if (player.state.playing) unawaited(player.pause());
      return;
    }
    final reason = failure != null ? 'play_exception' : 'no_progress';
    DiaryService.add(
      '[Play] 快速路径失败 原因=$reason 耗时=${watch.elapsedMilliseconds}ms，走兜底',
    );
    await _restoreAfterBackground(token);
  }

  /// 取流探活结果：只有两次都明确返回 false 才算确认失效。
  Future<_StreamProbe> _probeStream() async {
    var sawDead = false;
    var sawError = false;
    for (var attempt = 0; attempt < 2; attempt++) {
      if (attempt > 0) {
        await Future<void>.delayed(const Duration(milliseconds: 150));
      }
      try {
        final status = await repository.playbackStatus().timeout(
          const Duration(seconds: 2),
        );
        if (!_closed) _streamEpoch = status.epoch;
        if (status.alive) return _StreamProbe.alive;
        sawDead = true;
      } catch (error) {
        DiaryService.add('[Play] 取流探活异常：${_diagnostic(error)}');
        sawError = true;
      }
    }
    // 一次 false 一次异常仍属"未确认"，按未知处理，避免误触发重建。
    return sawDead && !sawError ? _StreamProbe.dead : _StreamProbe.unknown;
  }

  Future<void> _restoreAfterBackground(int token) async {
    _restores++;
    try {
      await _restoreFromBackground(token);
    } finally {
      _restores--;
    }
  }

  Future<void> _restoreFromBackground(int token) async {
    final watch = Stopwatch()..start();
    final index = _index;
    final position = _resumePosition;
    final playIntent = _resumeIntent;
    final activated = await AppDevice.activateAudio();
    if (!_restorable(token)) return;
    if (!activated) {
      // 音频会话只影响出声，失败不阻断画面恢复，也不反复重试激活。
      DiaryService.add('[Play] iOS 音频会话激活失败，继续恢复画面');
    }
    final probeWatch = Stopwatch()..start();
    final probe = await _probeStream();
    final probeMs = probeWatch.elapsedMilliseconds;
    if (!_restorable(token)) return;
    if (probe == _StreamProbe.alive) {
      if (playIntent) {
        // 取流服务还在：先原地续播，确认位置真的推进才算成功。
        await _resumeInPlace(token, watch, index, position, probeMs);
        return;
      }
      // 用户主动暂停且服务可用：保留原实例与冻结画面，不播放也不重建。
      _playerSuspect = false;
      _pendingError = false;
      _errorTimer?.cancel();
      DiaryService.add(
        '[Play] 续播=false 且取流服务正常，保留原实例 探活=${probeMs}ms '
        '总=${watch.elapsedMilliseconds}ms 位置=${position.toStringAsFixed(1)}',
      );
      return;
    }
    if (probe == _StreamProbe.unknown) {
      // 探活通信异常不等于服务死亡：保留实例，必要时让播放器自己报错再恢复。
      _pendingError = false;
      _errorTimer?.cancel();
      if (!playIntent) _playerSuspect = false;
      DiaryService.add(
        '[Play] 取流探活异常，保留原实例 探活=${probeMs}ms '
        '总=${watch.elapsedMilliseconds}ms',
      );
      if (playIntent) {
        await _resumeInPlace(token, watch, index, position, probeMs);
      }
      return;
    }
    // 确认失效，只有这时才需要重新加载。
    DiaryService.add(
      '[Play] 重建开始 原因=stream_lost 集数=${index + 1} '
      '位置=${position.toStringAsFixed(1)} 续播=$playIntent 探活=${probeMs}ms',
    );
    // 避开 paused → inactive → resumed 的连续抖动，免得在转场中销毁纹理。
    await Future<void>.delayed(const Duration(milliseconds: 250));
    if (!_restorable(token)) return;
    _pendingError = false;
    _errorTimer?.cancel();
    _recovery.reset();
    await _rebuildAndResume(position, playIntent, restoreToken: token);
  }

  /// 等待播放位置真的推进：缓冲中不算成功，最多等 [windows] 个窗口。
  Future<bool> _confirmProgress(
    Duration before, {
    int windows = 2,
    bool Function()? alive,
  }) async {
    for (var attempt = 0; attempt < windows; attempt++) {
      await Future<void>.delayed(_restoreWindow);
      if (_closed || !_foreground || _loading) return false;
      if (alive != null && !alive()) return false;
      final state = player.state;
      if (state.playing && state.position > before + _restoreProgress) {
        return true;
      }
      if (!state.buffering) return false;
    }
    return false;
  }

  /// 原实例仍然可用时直接继续播放，保留已缓冲画面，做到切回来无黑屏。
  Future<void> _resumeInPlace(
    int token,
    Stopwatch watch,
    int index,
    double position,
    int probeMs,
  ) async {
    final generation = _generation;
    final before = player.state.position;
    _playerSuspect = false;
    _pendingError = false;
    _errorTimer?.cancel();
    // 不在确认成功前清零失败计数：resume 一旦实际失败，提前 reset 会把未成功的
    // 恢复链洗白，后续偶发故障反而继承更少次数。
    _playIntent = true;
    _resumeIntent = true;
    _sync();
    _notify();
    final playWatch = Stopwatch()..start();
    Object? failure;
    try {
      await player.play();
    } catch (error) {
      failure = error;
    }
    if (!_restorable(token) || generation != _generation) {
      // 只有用户明确暂停时才回退播放；切集/重试由对应路径自己接管。
      if (_pausedByUser && player.state.playing) {
        unawaited(player.pause());
      }
      return;
    }
    var advanced = false;
    if (failure == null) {
      advanced = await _confirmProgress(
        before,
        alive: () =>
            _restorable(token) && generation == _generation && index == _index,
      );
      // 期间若已切集、暂停或开始别的恢复，旧任务必须放弃。
      if (!_restorable(token) || generation != _generation || index != _index) {
        if (_pausedByUser && player.state.playing) {
          unawaited(player.pause());
        }
        return;
      }
    }
    if (advanced) {
      DiaryService.add(
        '[Play] 原地恢复成功 探活=${probeMs}ms 播放=${playWatch.elapsedMilliseconds}ms '
        '总=${watch.elapsedMilliseconds}ms 位置=${currentPosition.toStringAsFixed(1)}',
      );
      // 确认真正恢复成功后才清零失败计数（见上方说明）。
      _recovery.reset();
      _health.reset();
      return;
    }
    final buffering = failure == null && player.state.buffering;
    final reason = failure != null
        ? 'play_exception'
        : buffering
        ? 'buffering_without_progress'
        : 'position_stalled';
    DiaryService.add(
      '[Play] 原地恢复失败 原因=$reason 探活=${probeMs}ms '
      '播放=${playWatch.elapsedMilliseconds}ms 总=${watch.elapsedMilliseconds}ms'
      '${failure == null ? '' : ' 详情=${_diagnostic(failure)}'}',
    );
    // 恢复观察期间用户已明确暂停：放弃重建，尊重暂停意图。
    if (_pausedByUser || !_playIntent) {
      DiaryService.add('[Play] 原地恢复期间用户暂停，取消重建');
      if (player.state.playing) unawaited(player.pause());
      return;
    }
    // 取较大值，避免恢复过程中位置回退。
    final resumeAt = currentPosition > position ? currentPosition : position;
    await _rebuildAndResume(resumeAt, true, restoreToken: token);
  }

  Future<void> _replacePlayer() async {
    // 离开过前台后实例可能已被 iOS 回收：当前已缓冲的流也许还能续播，
    // 但重新 open 新媒体（下一集、换清晰度、换线路）会失败或静默卡住，
    // 所以真正需要打开新媒体前先换一个干净实例。
    final respawn = _respawn;
    if (respawn == null) return;
    DiaryService.add('[Play] 打开新媒体前重建播放器');
    _generation++;
    // 与 play() 中的 open 共用同一条串行队列：换实例不会在别的打开动作
    // 进行途中发生，避免旧会话地址被换到新实例上打开。
    await _serialize(() async {
      final previous = _player;
      final volume = previous.state.volume;
      final standby = _standby;
      _standby = null;
      _dropStandby();
      _preloader.clear();
      _seekSequence++;
      _acceptErrors = false;
      _openedIndex = -1;
      _beginNewFrame();
      _playerSuspect = false;
      danmaku.setPlan(null);
      final stopped = await _boundedStop(previous, context: '换实例前停止旧实例');
      _player = respawn();
      _bind();
      // 先把界面切到新实例，再等旧实例彻底释放，避免读到已销毁的播放器。
      // stop 正常完成才 _retire；超时分支已由 _boundedStop 异步隔离释放。
      if (stopped) await _retire(previous);
      if (standby != null) await _retire(standby);
      unawaited(_player.setVolume(volume));
    });
    _notify();
  }

  Future<void> _rebuildAndResume(
    double position,
    bool playWhenReady, {
    int? restoreToken,
  }) async {
    if (_rebuilding) {
      // 正在重建时又来了恢复请求：记下最新位置与意图，完成后补跑一次，
      // 不能像以前那样直接丢弃；保留 token 以便新操作让排队任务作废。
      _rebuildQueued = true;
      _queuedPosition = position;
      _queuedPlay = playWhenReady;
      _queuedToken = restoreToken;
      return;
    }
    if (restoreToken != null && !_restorable(restoreToken)) return;
    final respawn = _respawn;
    if (respawn == null) {
      await play(_index, position: position, playWhenReady: playWhenReady);
      return;
    }
    final watch = Stopwatch()..start();
    final index = _index;
    _rebuilding = true;
    // 先作废所有进行中的 play/open，排队中的旧流程拿到队列后会自行放弃。
    final ticket = ++_generation;
    try {
      await _serialize(() async {
        final previous = _player;
        final volume = previous.state.volume;
        final plan = _plan;
        final standby = _standby;
        _standby = null;
        _playerSuspect = false;
        _dropStandby();
        _preloader.clear();
        _seekSequence++;
        _acceptErrors = false;
        _plan = null;
        _openedIndex = -1;
        _beginNewFrame();
        danmaku.setPlan(null);
        final stopped = await _boundedStop(previous, context: '重建前停止旧实例');
        await _release(plan?.session ?? '');
        _player = respawn();
        _bind();
        // 旧实例释放完成前不打开新媒体，避免音频与渲染资源交叠。
        // stop 正常完成才 _retire；超时分支已由 _boundedStop 异步隔离释放。
        if (stopped) await _retire(previous);
        final retired = standby;
        if (retired != null) await _retire(retired);
        await _player.setVolume(volume);
        await _setRate(_speed);
      });
      _notify();
      if (_closed || !_foreground || !_current(ticket)) {
        if (!_closed) DiaryService.add('[Play] 跳过过期重建');
        return;
      }
      if (restoreToken != null && !_restorable(restoreToken)) {
        // 用户在重建期间暂停了：只恢复暂停画面，不再自动播放。
        if (_foreground && _pausedByUser && index == _index) {
          await play(index, position: position, playWhenReady: false);
        }
        return;
      }
      final resetMs = watch.elapsedMilliseconds;
      await play(_index, position: position, playWhenReady: playWhenReady);
      if (!playWhenReady) {
        DiaryService.add(
          '[Play] 重建完成（保持暂停）重置=${resetMs}ms '
          '总=${watch.elapsedMilliseconds}ms 位置=${position.toStringAsFixed(1)}',
        );
        return;
      }
      // play() 内部会自增 generation，这里必须用新的 ticket 判断有效性，
      // 否则每次重建都会被当成"已过期"跳过确认，卡住也只能等 20s 健康检查。
      final live = _generation;
      if (_closed || !_foreground || !_current(live)) return;
      // open() 返回不代表真的在播：必须看到位置从恢复点继续推进。
      final baseline =
          Duration(milliseconds: (position * 1000).round()) - _restoreProgress;
      final playing = await _confirmProgress(
        baseline,
        alive: () => _current(live),
      );
      if (_closed || !_foreground || !_current(live)) return;
      if (playing) {
        DiaryService.add(
          '[Play] 重建恢复成功 重置=${resetMs}ms 打开=${watch.elapsedMilliseconds - resetMs}ms '
          '总=${watch.elapsedMilliseconds}ms 位置=${currentPosition.toStringAsFixed(1)}',
        );
        return;
      }
      final stalled = player.state.buffering ? 'buffering' : 'position_stalled';
      DiaryService.add(
        '[Play] 重建后仍未播放 原因=$stalled 重置=${resetMs}ms '
        '总=${watch.elapsedMilliseconds}ms 位置=${currentPosition.toStringAsFixed(1)}',
      );
      // 统一走恢复入口：由协调器按级别升级，不再直接调 _recover，
      // 也不再有 iOS 专属的「无条件重建 3 次」旁路。
      _queueRecovery('rebuild_$stalled');
    } finally {
      _rebuilding = false;
      if (_rebuildQueued) {
        _rebuildQueued = false;
        final next = _queuedPosition;
        final playQueued = _queuedPlay;
        final token = _queuedToken;
        if (!_closed && _foreground && (token == null || _restorable(token))) {
          unawaited(_rebuildAndResume(next, playQueued, restoreToken: token));
        }
      }
    }
  }

  Future<void> saveProgress({bool quiet = false}) async {
    if (!_engaged || _openedIndex < 0 || store.profileEpoch != _epoch) return;
    final position = player.state.position.inMilliseconds / 1000;
    final duration = player.state.duration.inMilliseconds / 1000;
    if (position < .1) return;
    final key = '${detail.drama.id}:$_openedIndex:$position:$duration';
    if (_savedProgressKey == key &&
        _saveWarning == null &&
        (quiet || !_savedQuietly)) {
      return;
    }
    final entry = WatchEntry(
      drama: repository.catalogUpdates.current(detail.drama),
      episode: detail.episodes[_openedIndex].number,
      position: position,
      duration: duration,
      updatedAt: DateTime.now(),
    );
    try {
      await store.saveWatch(entry, quiet: quiet);
      _savedProgressKey = key;
      _savedQuietly = quiet;
      if (_saveWarning != null) {
        _saveWarning = null;
        _notify();
      }
    } catch (_) {
      if (_saveWarning == null) {
        _saveWarning = '观看进度尚未保存，请检查存储空间后重试。';
        _notify();
      }
    }
  }

  @override
  void dispose() {
    unawaited(saveProgress());
    unawaited(WakelockPlus.disable().catchError((Object _) {}));
    _closed = true;
    _generation++;
    _restoreToken++;
    _recovery.cancel();
    store.removeListener(_accessChanged);
    store.removeListener(_syncBlockedWords);
    _saveTimer?.cancel();
    _healthTimer?.cancel();
    _errorTimer?.cancel();
    _boostTimer?.cancel();
    _stallTimer?.cancel();
    _volumeTimer?.cancel();
    _surfaceTimer?.cancel();
    _surfaceHardTimer?.cancel();
    _surfaceHardTimer = null;
    _clearOpenBudget();
    if (_volumeTimer != null) _saveVolume();
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    danmaku.dispose();
    _preloader.dispose();
    final session = _plan?.session ?? '';
    unawaited(_release(session));
    final standbyPlan = _standbyPlan;
    if (standbyPlan != null) unawaited(_release(standbyPlan.session));
    final standby = _standby;
    final current = _player;
    unawaited(
      _loader.close().then<void>(
        (_) {},
        onError: (Object error) {
          DiaryService.add('[Play] dispose 时关闭解析任务失败：${_diagnostic(error)}');
        },
      ),
    );
    unawaited(
      _operations
          .then<void>(
            (_) {},
            onError: (Object error) {
              DiaryService.add(
                '[Play] dispose 时等待串行任务失败：${_diagnostic(error)}',
              );
            },
          )
          .then((_) async {
            await _rates.then<void>(
              (_) {},
              onError: (Object error) {
                DiaryService.add(
                  '[Play] dispose 时等待倍速任务失败：${_diagnostic(error)}',
                );
              },
            );
            await _retire(current);
            if (standby != null) await _retire(standby);
          }),
    );
    super.dispose();
  }
}
