import 'dart:async';
import 'dart:io';
import 'dart:ui' show Size;

import 'package:flutter/foundation.dart';
import 'package:media_kit/media_kit.dart';
import 'package:path_provider/path_provider.dart';

import 'core_bridge.dart';
import 'danmaku_controller.dart';
import 'danmaku_style.dart';
import 'diary_service.dart';
import 'follow_state.dart';
import 'local_store.dart';
import 'media_controls.dart';
import 'models.dart';
import 'playback_loader.dart';
import 'playback_preferences.dart';
import 'playback_preloader.dart';
import 'playback_recovery.dart';

final _mpvHardwareLog = RegExp(
  r'hwdec|videotoolbox|hardware decod|software decoding|CVOpenGLESTexture',
  caseSensitive: false,
);

const _swapChromaSource = '''
//!HOOK NATIVE
//!BIND HOOKED
//!DESC swap chroma
vec4 hook() {
    return HOOKED_tex(HOOKED_pos).xzyw;
}
''';

Future<String> _swapChromaShader() async {
  final directory = await getApplicationSupportDirectory();
  final file = File('${directory.path}/swap_chroma.glsl');
  await file.writeAsString(_swapChromaSource);
  return file.path;
}

Size? videoDisplaySize(VideoParams parameters) {
  final width = (parameters.dw ?? 0) > 0 ? parameters.dw! : parameters.w ?? 0;
  final height = (parameters.dh ?? 0) > 0 ? parameters.dh! : parameters.h ?? 0;
  if (width <= 0 || height <= 0) return null;
  final rotation = (parameters.rotate ?? 0) % 180;
  return rotation == 90
      ? Size(height.toDouble(), width.toDouble())
      : Size(width.toDouble(), height.toDouble());
}

bool audioOnly(Tracks tracks) {
  bool real(String id) => id != 'auto' && id != 'no';
  return tracks.audio.any((track) => real(track.id)) &&
      !tracks.video.any(
        (track) =>
            real(track.id) && track.albumart != true && track.image != true,
      );
}

final _diagnosticAddress = RegExp(r'[a-z]+://\S+', caseSensitive: false);

String _diagnostic(Object error) {
  final text = error.toString().replaceAll(_diagnosticAddress, '[地址]');
  return text.length > 300 ? text.substring(0, 300) : text;
}

String _diagnosticPlaybackTarget(String? address) {
  if (address == null || address.isEmpty) return 'unknown';
  final uri = Uri.tryParse(address);
  if (uri == null) return 'unknown';
  if (uri.scheme == 'file') return 'local_file';
  if (!const {'http', 'https', 'tcp', 'tls'}.contains(uri.scheme)) {
    return 'unknown';
  }
  final host = uri.host;
  if (host.isEmpty) return 'unknown';
  if (host.toLowerCase() == 'localhost' ||
      InternetAddress.tryParse(host)?.isLoopback == true) {
    return 'loopback port=${uri.port}';
  }
  return 'remote';
}

class PlaybackSession extends ChangeNotifier {
  PlaybackSession({
    required this.repository,
    required this.store,
    required this.detail,
    required this._player,
    this._standby,
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
    _videoFit = preferences.videoFit;
    _unmutedVolume = store.playerVolume.clamp(1.0, 100.0);
    unawaited(_player.setVolume(_unmutedVolume));
    _loader = PlaybackLoader(repository);
    _preloader = PlaybackPreloader(repository)..addListener(_prime);
    danmaku = DanmakuController(repository)
      ..setEnabled(_danmakuEnabled)
      ..setMergeWindow(preferences.danmakuMerge)
      ..setBlockedWords(preferences.danmakuWords);
    store.addListener(_accessChanged);
    store.addListener(_syncDanmakuOptions);
    store.addListener(_syncMedia);
    if (Platform.isIOS) {
      for (final target in [_player, ?_standby]) {
        _mpvLogSubscriptions.add(
          target.stream.log.listen((log) => _logMpvHardware(target, log)),
        );
      }
    }
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
        DiaryService.add(
          '[Play] 停滞检测 位置=${player.state.position.inSeconds}s '
          '播放=${player.state.playing} 缓冲=${player.state.buffering}',
        );
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
    if (size != null) _videoSize = size;
    if (audioOnly(player.state.tracks)) _audioDetected = true;
    _buffering(player.state.buffering);
    final stream = player.stream;
    _subscriptions.addAll([
      stream.error.listen(_playerError),
      stream.completed.listen(_completed),
      stream.position.listen((position) {
        if (!_closed && _openedIndex == _index && position > Duration.zero) {
          _resumePosition = position.inMilliseconds / 1000;
          if (position >= const Duration(seconds: 2)) {
            unawaited(_logVideoOutput(player.state.videoParams));
          }
        }
        _sync();
      }),
      stream.playing.listen((playing) {
        if (!playing) {
          endBoost();
          _networkSpeed = 0;
        }
        _sync();
      }),
      stream.buffering.listen((buffering) {
        _buffering(buffering);
        _sync();
      }),
      for (final changes in [stream.duration, stream.buffer, stream.rate])
        changes.listen((_) => _sync()),
      stream.tracks.listen((tracks) {
        if (_audioDetected || !audioOnly(tracks)) return;
        _audioDetected = true;
        _notify();
      }),
      stream.videoParams.listen((parameters) {
        final size = videoDisplaySize(parameters);
        if (size == null || _closed) return;
        _videoSize = size;
        _notify();
        unawaited(_logVideoOutput(parameters));
      }),
    ]);
  }

  int? _networkSpeed;
  bool _speedReading = false;
  Duration? _speedResumeAt;
  DateTime? _speedResumeDeadline;

  void _holdNetworkSpeed() {
    _speedResumeAt = player.state.position;
    _speedResumeDeadline = DateTime.now().add(const Duration(seconds: 15));
  }

  final _mpvLogSubscriptions = <StreamSubscription<PlayerLog>>[];
  String _videoOutputSession = '';

  void _logMpvHardware(Player target, PlayerLog log) {
    final message = log.text.trim();
    if (_closed ||
        (!_mpvHardwareLog.hasMatch(log.prefix) &&
            !_mpvHardwareLog.hasMatch(message))) {
      return;
    }
    final role = identical(target, _player) ? 'active' : 'standby';
    unawaited(
      repository.recordVideoDiagnostic(
        'mpv player=$role [${log.prefix}/${log.level}] $message',
      ),
    );
  }

  Future<void> _logVideoOutput(VideoParams parameters) async {
    final platform = player.platform;
    if (platform is! NativePlayer) return;
    if (!player.state.playing ||
        player.state.buffering ||
        player.state.position < const Duration(seconds: 2)) {
      return;
    }
    final session = _plan?.session ?? '';
    if (session.isEmpty || session == _videoOutputSession) return;
    _videoOutputSession = session;
    final decoder = store.playbackPreferences.televisionDecoder.name;
    final values = <String, String>{};
    for (final name in [
      'hwdec',
      'hwdec-current',
      'current-vo',
      'video-codec',
      'container-fps',
    ]) {
      try {
        final value = await platform.getProperty(name);
        values[name] = value.isEmpty ? 'unavailable' : value;
      } catch (error) {
        values[name] = '读取失败(${error.runtimeType})';
      }
      if (_closed || _plan?.session != session || player.platform != platform) {
        return;
      }
    }
    await repository.recordVideoDiagnostic(
      'decoder=$decoder session=$session hwdec=${values['hwdec-current']} '
      'hwdec_option=${values['hwdec']} '
      'vo=${values['current-vo']} codec=${values['video-codec']} '
      'fps=${values['container-fps']} format=${parameters.pixelformat} '
      'hw_format=${parameters.hwPixelformat} size=${parameters.w}x${parameters.h}',
    );
  }

  final AppRepository repository;
  final LocalStore store;
  DramaDetail detail;
  Player _player;
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
  Timer? _sleepTimer;
  Timer? _standbyIdleTimer;
  Timer? _resumeTimer;
  Timer? _seekWatchdog;
  DateTime? _pausedSince;
  DateTime? _sleepAt;
  int _sleepMinutes = 0;
  bool _sleepAtEpisodeEnd = false;
  bool _advancingSeason = false;
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
  bool _visible = true;
  bool _playIntent = true;
  bool _pendingError = false;
  bool _boosting = false;
  bool _boostLocked = false;
  bool _locked = false;
  bool _engaged;
  bool _staleAudio = false;
  bool _audioDetected = false;
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
  Size? _videoSize;
  VideoFit _videoFit = VideoFit.auto;
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
  bool get locked => _locked;
  Size? get videoSize => _videoSize;
  double get aspectRatio => switch (_videoSize) {
    final size? => size.width / size.height,
    null => 9 / 16,
  };
  bool get aspectKnown => _videoSize != null;
  bool get audio => episode.audio || _audioDetected;
  VideoFit get videoFit => _videoFit;
  double get speed => _speed;
  DateTime? get sleepAt => _sleepAt;
  int get sleepMinutes => _sleepMinutes;
  bool get sleepAtEpisodeEnd => _sleepAtEpisodeEnd;
  int get quality => _quality;
  bool get local => _plan?.local == true;
  List<int> get qualities => _plan?.qualities ?? const [];
  bool get danmakuSupported => SourceSite.byId(detail.drama.source).danmaku;
  bool get danmakuEnabled => _danmakuEnabled;
  PlaybackPreferences get preferences => _preferences;
  DanmakuStyle get danmakuStyle => DanmakuStyle.of(store.playbackPreferences);
  bool get hasPrevious => _index > 0;
  bool get hasNext => _index + 1 < detail.episodes.length;
  String get qualityLabel {
    final plan = _plan;
    if (plan != null && plan.local) {
      return plan.quality > 0 ? '本地 ${plan.quality}P' : '本地';
    }
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
    videoFit: _videoFit,
    autoAdvance: true,
    preload: true,
  );

  double get _rate => _boosting ? store.playbackPreferences.boostSpeed : _speed;

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
    if (_closed) return;
    notifyListeners();
    _syncMedia();
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
        !store.allowsSource(detail.drama.source)) {
      danmaku.setPlan(null);
    }
  }

  void _syncDanmakuOptions() {
    if (_closed) return;
    final preferences = store.playbackPreferences;
    danmaku.setMergeWindow(preferences.danmakuMerge);
    danmaku.setBlockedWords(preferences.danmakuWords);
  }

  void _sync() {
    _syncDanmaku();
    _syncPreload();
    _syncMedia();
  }

  bool get _backgroundPlayback => store.playbackPreferences.backgroundPlayback;

  bool get _videoHidden =>
      Platform.isIOS && !_visible && _backgroundPlayback;

  void _syncMedia() {
    if (_closed) return;
    final controls = MediaControls.instance;
    if (!_backgroundPlayback || !_engaged || !_usable || store.locked) {
      controls.release(this);
      return;
    }
    if (!_playIntent && !controls.owns(this)) return;
    final state = player.state;
    final opened = ready && _openedIndex == _index;
    controls.update(
      this,
      MediaState(
        title: detail.drama.title,
        subtitle: episode.title,
        artwork: repository.knownCover(detail.drama) ?? '',
        active: _playIntent && _error == null,
        playing: _playIntent && state.playing,
        position: opened ? state.position : Duration.zero,
        duration: opened ? state.duration : Duration.zero,
        rate: state.rate,
        hasPrevious: hasPrevious,
        hasNext: hasNext,
      ),
      _mediaCommand,
    );
  }

  void _mediaCommand(MediaCommand command, Duration? position) {
    if (_closed) return;
    switch (command) {
      case MediaCommand.play:
        if (paused) togglePlayback();
      case MediaCommand.pause:
        _pause();
      case MediaCommand.toggle:
        if (paused) {
          togglePlayback();
        } else {
          _pause();
        }
      case MediaCommand.next:
        unawaited(next());
      case MediaCommand.previous:
        unawaited(previous());
      case MediaCommand.seek:
        if (position != null) unawaited(seekTo(position));
    }
  }

  void pause() => _pause();

  void _pause() {
    if (!_usable || !_playIntent) return;
    if (ready && player.state.playing) {
      togglePlayback();
      return;
    }
    _stop();
    _cancelResumeWatchdog();
    _pausedSince = DateTime.now();
    _scheduleStandbyIdle();
    _notify();
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
          store.allowsSource(detail.drama.source),
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
      if (_standbyIndex >= 0 ||
          duration <= 0 ||
          duration - position > 45000 ||
          ahead < 3000) {
        return;
      }
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
    final target = _diagnosticAddress.firstMatch(error)?.group(0);
    final plan = _plan;
    final planned = plan?.local == true
        ? 'local_file'
        : _diagnosticPlaybackTarget(plan?.url);
    DiaryService.add(
      '[Play] 播放器报错：${_diagnostic(error)} '
      'error_target=${_diagnosticPlaybackTarget(target)} plan_target=$planned',
    );
    _queueRecovery('player_error');
  }

  void _completed(bool completed) {
    if (!completed || _loading || _closed || !_acceptErrors || _error != null) {
      return;
    }
    final duration = player.state.duration;
    final position = player.state.position;
    if (duration <= Duration.zero
        ? position <= Duration.zero
        : position < duration - const Duration(seconds: 2)) {
      _queueRecovery('early_completion');
    } else if (_foreground && hasNext && !_sleepAtEpisodeEnd) {
      unawaited(play(_index + 1));
    } else if (_foreground &&
        !_sleepAtEpisodeEnd &&
        detail.nextSeason != null) {
      unawaited(_playNextSeason());
    } else {
      final sleeping = _sleepAtEpisodeEnd;
      if (sleeping) _clearSleep();
      _stop();
      if (sleeping) _notify();
    }
  }

  void _stop() {
    _playIntent = false;
    endBoost();
    unawaited(player.pause());
    unawaited(saveProgress());
  }

  void syncSeasons(List<DramaSeason> seasons) {
    if (_closed ||
        seasons.isEmpty ||
        identical(seasons, detail.seasons) ||
        !seasons.any((season) => season.drama.id == detail.drama.id)) {
      return;
    }
    detail = DramaDetail(
      detail.drama,
      detail.episodes,
      warning: detail.warning,
      seasons: seasons,
    );
  }

  bool get switchingSeason => _advancingSeason;

  Future<void> _playNextSeason() async {
    final season = detail.nextSeason;
    if (season == null) return;
    DiaryService.add('[Play] 本季播完，切换到${season.label}');
    await _switchSeason(season, resume: false);
  }

  Future<bool> playSeason(DramaSeason season) async {
    if (season.drama.id == detail.drama.id) return true;
    DiaryService.add('[Play] 手动切换到${season.label}');
    return _switchSeason(season, resume: true);
  }

  Future<bool> _switchSeason(DramaSeason season, {required bool resume}) async {
    if (_advancingSeason || !_usable) return false;
    _advancingSeason = true;
    final ticket = _generation;
    _dropStandby();
    _preloader.clear();
    _notify();
    try {
      await saveProgress();
      final next = await repository.detail(season.drama);
      if (!_current(ticket) || !_foreground) return false;
      if (next.episodes.isEmpty) throw AppFailure('${season.label}暂时没有可播放的分集');
      final watch = resume ? store.watched(season.drama.id) : null;
      final index = resumeEpisodeIndex(next.episodes, watch);
      final position =
          watch != null &&
              !watch.finished &&
              watch.episode == next.episodes[index].number
          ? watch.position
          : 0.0;
      detail = DramaDetail(
        repository.catalogUpdates.current(season.drama).merge(next.drama),
        next.episodes,
        warning: next.warning,
        seasons: detail.seasons,
      );
      _openedIndex = -1;
      _index = index;
      _online = false;
      _notify();
      await play(index, position: position);
      return true;
    } catch (error) {
      DiaryService.add('[Play] 切换${season.label}失败：${_diagnostic(error)}');
      if (_current(ticket)) _stop();
      _notify();
      return false;
    } finally {
      _advancingSeason = false;
    }
  }

  void _clearSleep() {
    _sleepTimer?.cancel();
    _sleepTimer = null;
    _sleepAt = null;
    _sleepMinutes = 0;
    _sleepAtEpisodeEnd = false;
  }

  void setSleepTimer({int minutes = 0, bool episodeEnd = false}) {
    if (_closed) return;
    _clearSleep();
    if (episodeEnd) {
      _sleepAtEpisodeEnd = true;
    } else if (minutes > 0) {
      final after = Duration(minutes: minutes);
      _sleepMinutes = minutes;
      _sleepAt = DateTime.now().add(after);
      _sleepTimer = Timer(after, () {
        _clearSleep();
        if (!_closed) _stop();
        _notify();
      });
    }
    _notify();
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
    final position = currentPosition;
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
    final symptom = switch (reason) {
      'position_stalled_20s' => '画面长时间没有进度',
      'early_completion' => '视频提前结束',
      'open_exception' => '视频打开失败',
      _ => '播放器报错',
    };
    _error = current.local
        ? '$symptom，本地视频没能播放。可重试或重新下载，也可以改为在线播放。'
        : '$symptom，自动恢复未成功。可重试、换一集或选择其他清晰度。';
    _notify();
  }

  Future<void> _release(String session) async {
    if (session.isEmpty) return;
    await repository.release(session).catchError((Object _) {});
  }

  Future<void> _serialize(Future<void> Function() operation) {
    final next = _operations.catchError((Object _) {}).then((_) => operation());
    _operations = next;
    return next;
  }

  Future<void> _configure(Player target, PlaybackPlan plan) async {
    final platform = target.platform;
    if (platform is! NativePlayer) return;
    final decoder = store.playbackPreferences.televisionDecoder;
    final hwdec = switch (decoder) {
      TelevisionDecoder.software => 'no',
      TelevisionDecoder.hardwareDirect when Platform.isAndroid => 'mediacodec',
      TelevisionDecoder.hardware ||
      TelevisionDecoder.hardwareSwapUv when Platform.isAndroid =>
        'mediacodec-copy',
      TelevisionDecoder.hardware when Platform.isIOS || Platform.isMacOS =>
        'videotoolbox-copy',
      TelevisionDecoder.optimized ||
      TelevisionDecoder.hardwareDirect when Platform.isIOS =>
        'videotoolbox,videotoolbox-copy',
      _ when Platform.isIOS => 'videotoolbox-copy',
      _ when Platform.isWindows => 'd3d11va',
      _ when Platform.isAndroid => 'auto-safe',
      _ when Platform.isMacOS => 'auto-safe',
      _ => null,
    };
    if (hwdec != null) {
      await platform.setProperty('hwdec', hwdec);
      if (Platform.isIOS) {
        String configured;
        try {
          configured = await platform.getProperty('hwdec');
          if (configured.isEmpty) configured = 'unavailable';
        } catch (error) {
          configured = '读取失败(${error.runtimeType})';
        }
        final role = identical(target, _player) ? 'active' : 'standby';
        unawaited(
          repository.recordVideoDiagnostic(
            'configure player=$role session=${plan.session} '
            'decoder=${decoder.name} hwdec_requested=$hwdec '
            'hwdec_option=$configured',
          ),
        );
      }
    }
    if (Platform.isAndroid) {
      await platform.setProperty('hwdec-codecs', 'all');
      await platform.setProperty('vd-lavc-skiploopfilter', 'nonkey');
      await platform.setProperty('vd-lavc-threads', '4');
      await platform.setProperty('vd-lavc-fast', 'yes');
      await platform.setProperty('video-sync', 'audio');
      await platform.setProperty('framedrop', 'vo');
      await platform.setProperty(
        'glsl-shaders',
        decoder == TelevisionDecoder.hardwareSwapUv
            ? await _swapChromaShader()
            : '',
      );
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
    if (Platform.isIOS) {
      await platform.setProperty('vid', _videoHidden ? 'no' : 'auto');
    }
  }

  Future<void> play(
    int index, {
    double position = 0,
    PlaybackRecoveryAction? recovery,
    bool playWhenReady = true,
  }) async {
    if (!_usable || index < 0 || index >= detail.episodes.length) return;
    endBoost();
    _staleAudio = false;
    _cancelResumeWatchdog();
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
    _seekWatchdog?.cancel();
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
        await _selectAudio(player, true);
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
    } catch (error) {
      if (_current(ticket)) {
        if (prepared != null && identical(_plan, prepared)) {
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
    _seekWatchdog?.cancel();
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
    final stopping = previous.stop().catchError((Object _) {});
    if (previousPlan != null) unawaited(_release(previousPlan.session));
    danmaku.setPlan(plan);
    _notify();
    _syncDanmaku();
    try {
      await _limitCache(next, false);
      await next.setVolume(volume);
      await _setRate(_rate);
      await stopping;
      await _selectAudio(next, true);
      if (_current(ticket) && _foreground && _playIntent) await next.play();
    } catch (_) {
      if (_current(ticket)) _queueRecovery('open_exception');
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
      await _limitCache(standby, true);
      await _selectAudio(standby, false);
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
    _cancelStandbyIdle();
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

  Future<int?> networkSpeed() async {
    final platform = player.platform;
    if (_closed || local || platform is! NativePlayer) return null;
    if (_speedReading || _loading || !_foreground || !player.state.playing) {
      return _networkSpeed;
    }
    final resumeAt = _speedResumeAt;
    if (resumeAt != null) {
      final progressed =
          player.state.position > resumeAt + const Duration(milliseconds: 500);
      if (!progressed && DateTime.now().isBefore(_speedResumeDeadline!)) {
        return _networkSpeed;
      }
      _speedResumeAt = null;
      _speedResumeDeadline = null;
    }
    if (player.state.buffering) return _networkSpeed;
    _speedReading = true;
    try {
      final value = await platform.getProperty('cache-speed');
      final speed = double.tryParse(value)?.round();
      if (speed != null) _networkSpeed = speed;
      return _networkSpeed;
    } catch (_) {
      return _networkSpeed;
    } finally {
      _speedReading = false;
    }
  }

  Future<void> _selectAudio(Player target, bool enabled) async {
    final platform = target.platform;
    if (platform is! NativePlayer || !Platform.isIOS) return;
    await platform.setProperty('aid', enabled ? 'auto' : 'no');
  }

  void start(int index, {double position = 0}) {
    if (!_usable) return;
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
    if (ready) unawaited(player.play());
  }

  Future<void> retry() =>
      play(_index, position: currentPosition, playWhenReady: true);

  Future<void> retryFromStart() => play(_index);

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
    endBoost();
    _playIntent = !player.state.playing;
    _health.reset();
    if (_playIntent) {
      _cancelStandbyIdle();
      final paused = _takePausedSeconds();
      if (paused >= 5) DiaryService.add('[Play] 恢复播放，已暂停 $paused 秒');
      if (!local && paused >= 5) _armResumeWatchdog();
      _holdNetworkSpeed();
    } else {
      _cancelResumeWatchdog();
      _pausedSince = DateTime.now();
      _scheduleStandbyIdle();
    }
    if (_playIntent && player.state.completed) {
      unawaited(play(_index));
      return;
    }
    if (_playIntent && _staleAudio) {
      _staleAudio = false;
      final position = player.state.position;
      if (player.state.duration > Duration.zero && position > Duration.zero) {
        unawaited(
          player
              .seek(position)
              .then(
                (_) => player.state.playing
                    ? Future<void>.value()
                    : player.playOrPause(),
              )
              .catchError((Object _) {}),
        );
      } else {
        unawaited(player.playOrPause());
      }
    } else {
      unawaited(player.playOrPause());
    }
    if (!_playIntent) unawaited(saveProgress());
    _syncDanmaku();
  }

  int _takePausedSeconds() {
    final since = _pausedSince;
    _pausedSince = null;
    if (since == null) return 0;
    return DateTime.now().difference(since).inSeconds;
  }

  void _armResumeWatchdog() {
    _cancelResumeWatchdog();
    final position = player.state.position;
    _resumeTimer = Timer(const Duration(seconds: 5), () {
      _resumeTimer = null;
      if (_closed || !_playIntent) return;
      if (player.state.position > position) return;
      DiaryService.add('[Play] 恢复后 5 秒没有进展，重新加载当前集');
      unawaited(_reviveCurrent());
    });
  }

  void _cancelResumeWatchdog() {
    _resumeTimer?.cancel();
    _resumeTimer = null;
  }

  Future<void> _reviveCurrent() =>
      _reopen(Duration(milliseconds: (_resumePosition * 1000).round()));

  Future<void> _reopen(Duration target) async {
    final position = target.inMilliseconds / 1000;
    final plan = _plan;
    if (plan == null || plan.local || plan.url.isEmpty) {
      if (!local) await play(_index, position: position);
      return;
    }
    try {
      _health.reset();
      _staleAudio = false;
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
      _openedIndex = _index;
    } catch (_) {
      if (_closed) return;
      await play(_index, position: position);
    }
  }

  void _scheduleStandbyIdle() {
    _cancelStandbyIdle();
    if (_standby == null) return;
    _standbyIdleTimer = Timer(const Duration(minutes: 1), () {
      _standbyIdleTimer = null;
      if (_closed || _playIntent || _standbyIndex < 0) return;
      _dropStandby();
    });
  }

  void _cancelStandbyIdle() {
    _standbyIdleTimer?.cancel();
    _standbyIdleTimer = null;
  }

  Future<bool> seekTo(Duration target) async {
    if (!_usable || !ready) return false;
    _cancelResumeWatchdog();
    final ticket = ++_seekSequence;
    final generation = _generation;
    danmaku.beginSeek();
    var succeeded = false;
    try {
      await player.seek(target);
      succeeded = true;
    } catch (error) {
      DiaryService.add('[Play] 跳转失败 目标=${target.inSeconds}s：$error');
    } finally {
      if (!_closed && ticket == _seekSequence && generation == _generation) {
        danmaku.endSeek(succeeded ? target : player.state.position);
      }
    }
    if (succeeded && ticket == _seekSequence) _watchSeek(target);
    return succeeded;
  }

  void _watchSeek(Duration target) {
    _seekWatchdog?.cancel();
    final ticket = _seekSequence;
    Duration? settled;
    _seekWatchdog = Timer(const Duration(milliseconds: 2500), () {
      if (!_seekWatchStillValid(ticket)) return;
      settled = player.state.position;
      _seekWatchdog = Timer(const Duration(milliseconds: 2500), () {
        _seekWatchdog = null;
        if (!_seekWatchStillValid(ticket)) return;
        final position = player.state.position;
        if ((position - target).abs() <= const Duration(seconds: 2)) return;
        if (position > settled! + const Duration(milliseconds: 800)) return;
        DiaryService.add(
          '[Play] 跳转未生效 目标=${target.inSeconds}s '
          '位置=${position.inSeconds}s，重新打开当前线路',
        );
        unawaited(_reopen(target));
      });
    });
  }

  bool _seekWatchStillValid(int ticket) =>
      !_closed &&
      ticket == _seekSequence &&
      _playIntent &&
      _foreground &&
      !_loading &&
      _error == null;

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
      unawaited(_setRate(_rate));
      _notify();
    });
  }

  void holdBoost() {
    if (!_usable || _boosting) return;
    _boostTimer?.cancel();
    _boostTimer = null;
    _boosting = true;
    unawaited(_setRate(_rate));
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

  void setLocked(bool locked) {
    if (_locked == locked) return;
    _locked = locked;
    _notify();
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
    final previous = _videoFit;
    _videoFit = value;
    _notify();
    try {
      await _savePreferences(_preferences);
    } catch (_) {
      if (_videoFit == value) {
        _videoFit = previous;
        _notify();
      }
      rethrow;
    }
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

  Future<void> setDanmakuSpeed(double value) async {
    final speed = value.clamp(danmakuSpeedMin, danmakuSpeedMax);
    await _savePreferences(_preferences.copyWith(danmakuSpeed: speed));
    _notify();
  }

  Future<void> setDanmakuCoverage(DanmakuCoverage value) async {
    await _savePreferences(_preferences.copyWith(danmakuCoverage: value));
    _notify();
  }

  Future<void> setDanmakuMerge(DanmakuMerge value) async {
    await _savePreferences(_preferences.copyWith(danmakuMerge: value));
    _notify();
  }

  Future<void> setDanmakuWords(List<String> words) async {
    final normalized = normalizeDanmakuWords(words);
    await _savePreferences(_preferences.copyWith(danmakuWords: normalized));
    _notify();
  }

  void setForeground(bool visible) {
    if (_closed) return;
    _setVisible(visible);
    final foreground = visible || _backgroundPlayback;
    if (_foreground == foreground) return;
    DiaryService.add(
      foreground ? '[Play] 应用回到前台' : '[Play] 应用不可见，暂停播放',
    );
    _foreground = foreground;
    _health.reset();
    _sync();
    if (!foreground) {
      endBoost();
      _playIntent = false;
      _pausedSince = DateTime.now();
      _cancelResumeWatchdog();
      _cancelStandbyIdle();
      _dropStandby();
      _preloader.clear();
      unawaited(player.pause());
      unawaited(saveProgress());
    } else {
      _holdNetworkSpeed();
      if (_pendingError) _queueRecovery('foreground_pending_error');
    }
  }

  void _setVisible(bool visible) {
    if (_visible == visible) return;
    _visible = visible;
    if (!visible && _backgroundPlayback) {
      DiaryService.add('[Play] 应用不可见，后台继续播放');
    }
    if (!Platform.isIOS) return;
    if (visible) {
      _dropStandby();
      _preloader.clear();
    }
    for (final target in [_player, ?_standby]) {
      final platform = target.platform;
      if (platform is! NativePlayer) continue;
      unawaited(
        platform
            .setProperty('vid', _videoHidden ? 'no' : 'auto')
            .catchError((Object _) {}),
      );
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
    store.removeListener(_accessChanged);
    store.removeListener(_syncDanmakuOptions);
    store.removeListener(_syncMedia);
    MediaControls.instance.release(this);
    _saveTimer?.cancel();
    _healthTimer?.cancel();
    _errorTimer?.cancel();
    _boostTimer?.cancel();
    _stallTimer?.cancel();
    _seekWatchdog?.cancel();
    _volumeTimer?.cancel();
    _sleepTimer?.cancel();
    _standbyIdleTimer?.cancel();
    _resumeTimer?.cancel();
    if (_volumeTimer != null) _saveVolume();
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    for (final subscription in _mpvLogSubscriptions) {
      unawaited(subscription.cancel());
    }
    danmaku.dispose();
    _preloader.dispose();
    final session = _plan?.session ?? '';
    unawaited(_release(session));
    final standbyPlan = _standbyPlan;
    if (standbyPlan != null) unawaited(_release(standbyPlan.session));
    final standby = _standby;
    unawaited(_loader.close().catchError((Object _) {}));
    unawaited(
      _operations.catchError((Object _) {}).then((_) async {
        await _rates.catchError((Object _) {});
        await player.dispose();
        await standby?.dispose();
      }),
    );
    super.dispose();
  }
}
