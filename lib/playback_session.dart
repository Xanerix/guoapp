import 'dart:async';
import 'dart:io';
import 'dart:ui' show Size;

import 'package:flutter/foundation.dart';
import 'package:media_kit/media_kit.dart';

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
  }) : _engaged = autoplay,
       _index = index,
       _epoch = store.profileEpoch {
    final preferences = store.playbackPreferences;
    _speed = preferences.speed;
    _quality = preferences.quality;
    _danmakuEnabled = preferences.danmaku;
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
      if (_closed || !_acceptErrors || _loading || _error != null) return;
      if (_health.stalled(
        position: player.state.position,
        playing: player.state.playing && _playIntent,
        foreground: _foreground,
        now: DateTime.now(),
      )) {
        unawaited(_recover('position_stalled_20s'));
      }
    });
    unawaited(play(index, position: position, playWhenReady: autoplay));
  }

  void _bind() {
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    _subscriptions.clear();
    final size = videoDisplaySize(player.state.videoParams);
    if (size != null) {
      _aspectRatio = size.width / size.height;
      _aspectKnown = true;
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
        _sync();
      }),
      stream.playing.listen((playing) {
        if (!playing) endBoost();
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
        _aspectRatio = size.width / size.height;
        _aspectKnown = true;
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
  final _recovery = PlaybackRecovery();
  final _health = PlaybackHealth();
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
  DateTime _standbyAt = DateTime(2000);
  double _openedAt = 0;
  double _speed = 1;
  int _quality = 0;
  bool _danmakuEnabled = true;
  double _aspectRatio = 9 / 16;
  bool _aspectKnown = false;
  double _resumePosition = 0;
  double _unmutedVolume = 100;

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
  bool get ready => !_loading && _error == null;

  bool get stalled => _stalled;

  bool get paused => !_playIntent;
  bool get boosting => _boosting;
  bool get boostLocked => _boostLocked;
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

  PlaybackPreferences get _preferences => store.playbackPreferences.copyWith(
    speed: _speed,
    quality: _quality,
    danmaku: _danmakuEnabled,
    autoAdvance: true,
    preload: true,
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
    } else if (_stalled) {
      _stalled = false;
      _notify();
    }
  }

  void _notify() {
    if (!_closed) notifyListeners();
  }

  void _accessChanged() {
    if (_closed) return;
    if (store.profileEpoch != _epoch || store.locked) {
      _preloader.clear();
      _dropStandby();
      _playIntent = false;
      endBoost();
      unawaited(player.pause());
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
    } else if (_foreground && hasNext) {
      unawaited(play(_index + 1));
    } else if (_foreground && _nextSeason != null) {
      // 本季已看完：同一站源内自动接上下一季第 1 集。
      unawaited(_playNextSeason());
    } else {
      DiaryService.add(
        '[Play] 本季已看完 季数=${detail.seasons.length} '
        '下一季=${_nextSeason?.label ?? '无'}',
      );
      _playIntent = false;
      endBoost();
      unawaited(player.pause());
      unawaited(saveProgress());
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
      await play(0, playWhenReady: true);
      if (_error?.contains('context canceled') ?? false) {
        // 换季解析被并发操作取消时重试一次，避免停在错误页。
        DiaryService.add('[Play] 换季解析被取消，重试一次');
        _error = null;
        _loading = false;
        await play(0, playWhenReady: true);
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
    }
  }

  void _queueRecovery(String reason) {
    if (_closed || !_acceptErrors || _error != null) return;
    _pendingError = true;
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
    _acceptErrors = false;
    danmaku.setPlan(null);
    _errorTimer?.cancel();
    _pendingError = false;
    // 用本次播放请求的起点，而不是旧播放器的位置：切集失败时旧位置属于
    // 上一集，用它重开会直接跳到片尾之外。
    final position = _resumePosition;
    if (Platform.isIOS && !current.local && _iosRebuilds < 3) {
      // iOS 上换线路/刷新地址救不活被回收的实例，直接重建一次；
      // 连续失败时改用软件解码，绕开刚恢复时 VideoToolbox 初始化失败。
      _iosRebuilds++;
      if (_iosRebuilds >= 2) _forceSoftwareDecode = true;
      _errorTimer?.cancel();
      DiaryService.add('[Play] iOS 播放异常，重建播放器 原因=$reason 次数=$_iosRebuilds');
      await _rebuildAndResume(position, _playIntent);
      return;
    }
    final action = current.local
        ? PlaybackRecoveryAction.stop
        : _recovery.next(current);
    DiaryService.add(
      '[Play] 自动恢复 原因=$reason 处理=${action.name} 线路=${current.routeIndex + 1}/${current.routeCount}',
    );
    if (action != PlaybackRecoveryAction.stop) {
      await play(_index, position: position, recovery: action);
      return;
    }
    _resumePosition = position;
    final ticket = _generation;
    try {
      await _serialize(() async {
        if (!_current(ticket)) return;
        try {
          await saveProgress();
          _openedIndex = -1;
          await player.stop();
        } finally {
          await _release(current.session);
        }
      });
    } catch (_) {}
    if (!_current(ticket)) return;
    _loading = false;
    _localFailure = current.local;
    _loginRequired = false;
    _error = current.local
        ? '本地视频读取失败，请重试或重新下载；也可以改为在线播放。'
        : '自动恢复未成功，请检查网络后重试，也可换一集或选择其他清晰度。';
    _notify();
  }

  Future<void> _release(String session) async {
    if (session.isEmpty) return;
    await repository.release(session).catchError((Object _) {});
  }

  /// 等待旧实例真正释放，再让新实例打开媒体；释放失败也不能阻塞恢复。
  Future<void> _retire(Player target) async {
    final watch = Stopwatch()..start();
    try {
      // 上限 1.5s：dispose 卡住时不能让整条恢复链跟着停住。
      await target.dispose().timeout(const Duration(milliseconds: 1500));
    } catch (error) {
      DiaryService.add(
        '[Play] 释放播放器异常 用时=${watch.elapsedMilliseconds}ms：'
        '${_diagnostic(error)}',
      );
    } finally {
      _onRetired?.call(target);
    }
  }

  Future<void> _serialize(Future<void> Function() operation) {
    final next = _operations.catchError((Object _) {}).then((_) => operation());
    _operations = next;
    return next;
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

  Future<void> play(
    int index, {
    double position = 0,
    PlaybackRecoveryAction? recovery,
    bool playWhenReady = true,
  }) async {
    if (!_usable || index < 0 || index >= detail.episodes.length) return;
    // 用户主动开新集/重试：作废还在等待的恢复任务，避免它反向覆盖。
    _restoreToken++;
    endBoost();
    _staleAudio = false;
    _pausedByUser = false;
    if (_playerSuspect) await _replacePlayer();
    if (index != _index) _online = false;
    if (recovery == null &&
        position == 0 &&
        _standbyOpen &&
        _standbyIndex == index &&
        !_online) {
      return _swap(index, playWhenReady);
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
    if (recovery == null) {
      _recovery.reset();
      _playIntent = playWhenReady;
    }
    _resumePosition = position;
    _openedAt = position;
    _index = index;
    _loading = true;
    _error = null;
    _localFailure = false;
    _loginRequired = false;
    _loadingMessage = switch (recovery) {
      PlaybackRecoveryAction.alternative => '正在切换备用线路',
      PlaybackRecoveryAction.refresh => '正在重新获取播放地址',
      _ => '正在准备播放',
    };
    _notify();
    PlaybackPlan? prepared;
    PlaybackPlan? retained;
    var installed = false;
    try {
      await _serialize(() async {
        if (!_current(ticket)) return;
        await saveProgress();
        if (!_current(ticket)) return;
        _openedIndex = -1;
        await player.stop();
        final previous = _plan;
        _plan = null;
        if (recovery == PlaybackRecoveryAction.alternative) {
          retained = previous;
        } else if (previous != null) {
          await _release(previous.session);
        }
      });
      if (!_current(ticket)) return;
      prepared = retained != null
          ? await _loader.fallback(retained!)
          : warmed != null
          ? await _loader.use(warmed)
          : await _loader.load(
              detail.drama,
              detail.episodes[index],
              quality: _quality,
              online: _online,
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
        await player.open(
          Media(
            plan.url,
            httpHeaders: plan.headers,
            start: position > 0
                ? Duration(milliseconds: (position * 1000).round())
                : null,
          ),
          play: _foreground && _playIntent,
        );
        if (!_current(ticket)) return;
        _openedIndex = index;
        _health.reset();
        await _setRate(_rate);
        if (!_current(ticket)) return;
        _loading = false;
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
            _localFailure = error is AppFailure && error.code == 'local_media';
            _loginRequired =
                error is AppFailure && error.code == 'source_login';
            _error = error is AppFailure ? error.message : '无法播放这一集，请重试或换一集。';
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

  Future<void> _swap(int index, bool playWhenReady) async {
    final plan = _standbyPlan!;
    final next = _standby!;
    final previous = _player;
    final previousPlan = _plan;
    final volume = previous.state.volume;
    unawaited(saveProgress());
    _preloader.clear();
    final ticket = ++_generation;
    _seekSequence++;
    _pendingError = false;
    _errorTimer?.cancel();
    _health.reset();
    _recovery.reset();
    _playIntent = playWhenReady;
    _resumePosition = 0;
    _openedAt = 0;
    _index = index;
    _error = null;
    _localFailure = false;
    _loginRequired = false;
    _loading = false;
    _standbyPlan = null;
    _standbyIndex = -1;
    _standbyOpen = false;
    _player = next;
    _standby = previous;
    _plan = plan;
    _bind();
    _openedIndex = index;
    _acceptErrors = true;
    unawaited(previous.stop().catchError((Object _) {}));
    if (previousPlan != null) unawaited(_release(previousPlan.session));
    danmaku.setPlan(plan);
    _notify();
    _syncDanmaku();
    try {
      await _limitCache(next, false);
      await next.setVolume(volume);
      await _setRate(_rate);
      if (_current(ticket) && _foreground && _playIntent) {
        await next.play();
        unawaited(_ensurePlaying(ticket));
      }
    } catch (_) {
      if (_current(ticket)) _queueRecovery('open_exception');
    }
  }

  /// iOS 上预加载的待机播放器切集后会偶发停在暂停态：首帧播约 1 秒就卡住，
  /// 用户点一下才真正继续。2 倍速或弱网下更容易触发。
  /// 起播后短时间轮询一次真实播放状态，没有真正播放就补发 play。
  Future<void> _ensurePlaying(int ticket) async {
    for (var attempt = 0; attempt < 4; attempt++) {
      await Future<void>.delayed(const Duration(milliseconds: 800));
      if (_closed ||
          ticket != _generation ||
          !_foreground ||
          !_playIntent ||
          _pausedByUser) {
        return;
      }
      if (player.state.playing) return;
      try {
        await player.play();
      } catch (_) {
        return;
      }
    }
  }

  void _prime() {
    final standby = _standby;
    final next = _index + 1;
    if (_closed ||
        standby == null ||
        _standbyIndex >= 0 ||
        !_preloader.ready ||
        next >= detail.episodes.length) {
      return;
    }
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
    unawaited(_open(standby, plan));
  }

  Future<void> _open(Player standby, PlaybackPlan plan) async {
    try {
      await _configure(standby, plan);
      // 待机播放器也保留完整缓存：限到 4MB 时高倍速/弱网切过去只缓冲了一两秒，
      // 首帧播完就断流，表现为卡在第 1 秒。
      await _limitCache(standby, false);
      await standby.open(
        Media(plan.url, httpHeaders: plan.headers),
        play: false,
      );
    } catch (_) {
      if (identical(_standbyPlan, plan)) _dropStandby();
      return;
    }
    if (_closed || !identical(_standbyPlan, plan)) return;
    _standbyOpen = true;
    _standbyAt = DateTime.now();
    _notify();
  }

  void _dropStandby() {
    final plan = _standbyPlan;
    final opened = _standbyOpen || plan != null;
    _standbyPlan = null;
    _standbyIndex = -1;
    _standbyOpen = false;
    if (opened) unawaited(_standby?.stop().catchError((Object _) {}));
    if (plan != null) unawaited(_release(plan.session));
  }

  Future<void> _limitCache(Player target, bool limited) async {
    final platform = target.platform;
    if (platform is! NativePlayer) return;
    await platform.setProperty(
      'demuxer-max-bytes',
      '${(limited ? 4 : 32) * 1024 * 1024}',
    );
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
      unawaited(play(index, position: position));
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
  }

  Future<void> retry() =>
      play(_index, position: currentPosition, playWhenReady: true);

  Future<void> switchOnline() {
    _online = true;
    return retry();
  }

  Future<bool> next() async {
    if (!hasNext) return false;
    await play(_index + 1);
    return true;
  }

  Future<bool> previous() async {
    if (!hasPrevious) return false;
    await play(_index - 1);
    return true;
  }

  void togglePlayback() {
    if (!_usable || !ready) return;
    _restoreToken++;
    endBoost();
    _playIntent = !player.state.playing;
    _pausedByUser = !_playIntent;
    _health.reset();
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

  void setForeground(bool visible) {
    if (_closed || _foreground == visible) return;
    // 任何一次前后台变化都作废正在进行的恢复任务。
    _restoreToken++;
    _foreground = visible;
    _health.reset();
    _sync();
    if (!visible) {
      // 锁屏/切后台时直接读播放器状态，不等异步 position 事件。
      final snapshot = player.state.position;
      if (_openedIndex == _index && snapshot > Duration.zero) {
        _resumePosition = snapshot.inMilliseconds / 1000;
      }
      // 只信用户意图：系统挂起/恢复期间底层 playing 可能与实际操作不一致。
      _resumeIntent = _playIntent;
      if (Platform.isIOS) {
        // iOS 会随挂起回收播放器、备用实例与取流会话，留着会命中死实例。
        _playerSuspect = true;
        _dropStandby();
        _preloader.clear();
      }
      endBoost();
      _playIntent = false;
      DiaryService.add(
        '[Play] 进入后台，保存恢复状态 集数=${_index + 1} '
        '位置=${_resumePosition.toStringAsFixed(1)} 续播=$_resumeIntent',
      );
      unawaited(player.pause());
      unawaited(saveProgress());
      return;
    }
    _iosRebuilds = 0;
    if (Platform.isIOS) {
      // 锁屏可能破坏播放器与渲染上下文，但不一定；先由原生取流服务探活决定。
      unawaited(_restoreAfterBackground(_restoreToken));
    } else if (_pendingError) {
      _queueRecovery('foreground_pending_error');
    }
  }

  bool _restorable(int token) =>
      !_closed && _foreground && token == _restoreToken;

  /// 取流探活结果：只有两次都明确返回 false 才算确认失效。
  Future<_StreamProbe> _probeStream() async {
    var sawDead = false;
    var sawError = false;
    for (var attempt = 0; attempt < 2; attempt++) {
      if (attempt > 0) {
        await Future<void>.delayed(const Duration(milliseconds: 150));
      }
      try {
        final alive = await repository.playbackAlive().timeout(
          const Duration(seconds: 2),
        );
        if (alive) return _StreamProbe.alive;
        sawDead = true;
      } catch (_) {
        sawError = true;
      }
    }
    // 一次 false 一次异常仍属"未确认"，按未知处理，避免误触发重建。
    return sawDead && !sawError ? _StreamProbe.dead : _StreamProbe.unknown;
  }

  Future<void> _restoreAfterBackground(int token) async {
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
    _recovery.reset();
    _health.reset();
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
      _aspectKnown = false;
      _playerSuspect = false;
      danmaku.setPlan(null);
      await previous.stop().catchError((Object _) {});
      _player = respawn();
      _bind();
      // 先把界面切到新实例，再等旧实例彻底释放，避免读到已销毁的播放器。
      await _retire(previous);
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
        _aspectKnown = false;
        danmaku.setPlan(null);
        await previous.stop().catchError((Object _) {});
        await _release(plan?.session ?? '');
        _player = respawn();
        _bind();
        // 旧实例释放完成前不打开新媒体，避免音频与渲染资源交叠。
        await _retire(previous);
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
      if (_closed || !_foreground || !_current(ticket)) return;
      // open() 返回不代表真的在播：必须看到位置从恢复点继续推进。
      final baseline =
          Duration(milliseconds: (position * 1000).round()) - _restoreProgress;
      final playing = await _confirmProgress(
        baseline,
        alive: () => _current(ticket),
      );
      if (_closed || !_foreground || !_current(ticket)) return;
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
      // 交给既有的有限恢复链：iOS 下最多再重建 3 次，之后换线路并最终报错。
      unawaited(_recover('rebuild_$stalled'));
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
    _closed = true;
    _generation++;
    _restoreToken++;
    store.removeListener(_accessChanged);
    store.removeListener(_syncBlockedWords);
    _saveTimer?.cancel();
    _healthTimer?.cancel();
    _errorTimer?.cancel();
    _boostTimer?.cancel();
    _stallTimer?.cancel();
    _volumeTimer?.cancel();
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
    unawaited(_loader.close().catchError((Object _) {}));
    unawaited(
      _operations.catchError((Object _) {}).then((_) async {
        await _rates.catchError((Object _) {});
        await _retire(current);
        if (standby != null) await _retire(standby);
      }),
    );
    super.dispose();
  }
}
