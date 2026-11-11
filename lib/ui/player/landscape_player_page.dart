import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:media_kit/media_kit.dart';

import '../../app_device.dart';
import '../../local_store.dart';
import '../../models.dart';
import '../../playback_preferences.dart';
import '../../playback_session.dart';
import '../app_scope.dart';
import '../downloads/download_actions.dart';
import '../theme/app_theme.dart';
import '../theme/motion.dart';
import '../widgets/contained_slider.dart';
import '../widgets/motion/motion_widgets.dart';
import 'danmaku_overlay.dart';
import '../widgets/follow_button.dart';
import 'episode_grid.dart';
import 'mini_player.dart';
import 'playback_handle.dart';
import 'playback_sheets.dart';
import 'fade_presence.dart';
import 'player_surface.dart';
import 'player_video.dart';
import 'swipe_controls.dart';
import 'window_fullscreen.dart';

enum _Panel { episodes, settings, speed, quality }

class LandscapePlayerPage extends StatefulWidget {
  const LandscapePlayerPage({
    super.key,
    required this.store,
    required this.handle,
    this.onFullscreen,
    this.onMiniWindow,
  });
  final LocalStore store;
  final PlaybackHandle handle;
  final VoidCallback? onFullscreen;
  final VoidCallback? onMiniWindow;

  @override
  State<LandscapePlayerPage> createState() => _LandscapePlayerPageState();
}

class _LandscapePlayerPageState extends State<LandscapePlayerPage>
    with WidgetsBindingObserver {
  final List<StreamSubscription<dynamic>> _subscriptions = [];
  final _links = {_Panel.speed: LayerLink(), _Panel.quality: LayerLink()};
  late final _pages = PageController(initialPage: _session.index);
  Player? _bound;
  Timer? _hideTimer;
  Timer? _tapTimer;
  Timer? _streakTimer;
  Timer? _clockTimer;
  Timer? _seekTimer;
  bool? _full;
  bool _visible = true;
  bool _scrolling = false;
  bool _holding = false;
  bool _dragging = false;
  double? _seekValue;
  double? _seekTarget;
  int _streak = 0;
  int _streakTaps = 0;
  bool _descExpanded = false;
  _Panel? _panel;
  int _sidePanelMount = 0;
  bool _sidePanelShown = false;
  DateTime _now = DateTime.now();
  ({int level, bool charging})? _battery;

  PlaybackSession get _session => widget.handle.session;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    if (widget.onFullscreen == null) {
      unawaited(AppDevice.setPlayerOrientation(PlayerOrientation.auto));
    }
    _session.addListener(_bind);
    _bind();
    _scheduleHide();
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final full = MediaQuery.orientationOf(context) == Orientation.landscape;
    if (full == _full) return;
    _full = full;
    _panel = null;
    _visible = true;
    _scheduleHide();
    unawaited(
      SystemChrome.setEnabledSystemUIMode(
        full ? SystemUiMode.immersiveSticky : SystemUiMode.edgeToEdge,
      ).catchError((Object _) {}),
    );
    _clockTimer?.cancel();
    _clockTimer = null;
    // 横竖切换会把 PageView 挪到另一个子树，滚动位置按 initialPage 重建，
    // 这里在布局完成后重新对齐到当前集，避免画面和实际集数不一致。
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !_pages.hasClients) return;
      if ((_pages.page ?? _session.index.toDouble()).round() !=
          _session.index) {
        _pages.jumpToPage(_session.index);
      }
    });
    if (full) {
      _tick();
      _clockTimer = Timer.periodic(const Duration(seconds: 20), (_) => _tick());
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _session.removeListener(_bind);
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    _hideTimer?.cancel();
    _tapTimer?.cancel();
    _streakTimer?.cancel();
    _clockTimer?.cancel();
    _seekTimer?.cancel();
    if (_holding) _session.endBoost();
    _pages.dispose();
    if (widget.onFullscreen == null) {
      if (_full == true && desktopPlatform) {
        unawaited(setWindowFullScreen(false));
      }
      unawaited(AppDevice.releaseOrientation());
      unawaited(
        SystemChrome.setEnabledSystemUIMode(
          SystemUiMode.edgeToEdge,
        ).catchError((Object _) {}),
      );
    }
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) =>
      _session.setForeground(playerVisible(state));

  void _bind() {
    final player = _session.player;
    if (identical(player, _bound)) {
      _refresh();
      return;
    }
    _bound = player;
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    _subscriptions.clear();
    final stream = player.stream;
    _subscriptions.add(
      stream.position.listen((position) {
        final target = _seekTarget;
        if (target != null && (position.inMilliseconds - target).abs() < 1000) {
          _endSeek();
        }
      }),
    );
    _subscriptions.add(
      stream.playing.listen((playing) {
        if (playing) _scheduleHide();
        _refresh();
      }),
    );
    for (final changes in [stream.duration, stream.buffering]) {
      _subscriptions.add(changes.listen((_) => _refresh()));
    }
    _refresh();
  }

  void _refresh() {
    final page = _pages.hasClients ? _pages.page : null;
    if (!_scrolling &&
        !_session.advancingSeason &&
        page != null &&
        page.round() != _session.index &&
        _session.index < _session.detail.episodes.length) {
      _pages.jumpToPage(_session.index);
    }
    if (mounted) setState(() {});
  }

  bool _scrolled(ScrollNotification notification) {
    if (notification.depth != 0) return false;
    final metrics = notification.metrics;
    if (notification is ScrollStartNotification &&
        notification.dragDetails != null) {
      _hideTimer?.cancel();
      if (!_scrolling) setState(() => _scrolling = true);
    } else if (notification is ScrollEndNotification) {
      // 只有用户真正拖动过的滚动才切集：横竖切换重建 PageView、
      // jumpToPage 等程序化滚动同样会发 ScrollEnd，误判会导致跳回第一集。
      final dragged = _scrolling;
      if (_scrolling) setState(() => _scrolling = false);
      if (!dragged) return false;
      final page = metrics.viewportDimension <= 0
          ? _session.index
          : (metrics.pixels / metrics.viewportDimension).round();
      final count = _session.detail.episodes.length;
      if (page != _session.index && page >= 0 && page < count) {
        _show();
        unawaited(_session.play(page));
      } else if (page >= count && _session.nextSeason != null) {
        // 滑过最后一集：衔接下一季第 1 集。
        _show();
        unawaited(_session.playNextSeason());
      }
    }
    return false;
  }

  void _tick() {
    _now = DateTime.now();
    _refresh();
    unawaited(
      AppDevice.battery().then((battery) {
        if (mounted) setState(() => _battery = battery);
      }),
    );
  }

  void _endSeek() {
    _seekTimer?.cancel();
    _seekTarget = null;
    _seekValue = null;
  }

  bool get _pinned =>
      _session.paused || !_session.ready || _dragging || _panel != null;

  void _scheduleHide() {
    _hideTimer?.cancel();
    _hideTimer = Timer(const Duration(seconds: 3), () {
      if (!mounted || _pinned || !_visible) return;
      setState(() => _visible = false);
    });
  }

  void _show() {
    if (!_visible) setState(() => _visible = true);
    _scheduleHide();
  }

  void _toggleControls() {
    if (_visible && !_pinned) {
      _hideTimer?.cancel();
      setState(() => _visible = false);
    } else {
      _show();
    }
  }

  void _tapUp(TapUpDetails details, double width, bool full) {
    if (!full) {
      // 竖屏手持横片：单击视频 = 暂停/播放（红果行为）。
      _session.togglePlayback();
      return;
    }
    final x = details.localPosition.dx;
    final side =
        widget.onFullscreen == null ||
            !_session.store.playbackPreferences.doubleTapSeek
        ? 0
        : x < width / 4
        ? -1
        : x > width * 3 / 4
        ? 1
        : 0;
    if (_streak != 0) {
      if (side == _streak.sign) return _streakTap(side);
      setState(_commitStreak);
    }
    if (_tapTimer?.isActive ?? false) {
      _tapTimer!.cancel();
      // 双击：中间 → 暂停/恢复；左右 → 快进/快退
      if (side == 0) {
        _session.togglePlayback();
        _toggleControls();
      } else {
        _startStreak(side);
      }
      return;
    }
    // 单击：中间区域暂停/恢复 + 切控制条；左右区域留给双击快进快退
    if (side == 0) {
      _session.togglePlayback();
      _toggleControls();
    } else {
      _tapTimer = Timer(const Duration(milliseconds: 300), _toggleControls);
    }
  }

  void _startStreak(int side) {
    final duration = _session.player.state.duration;
    if (!_session.ready || duration <= Duration.zero) return;
    _streak = side;
    _streakTaps = 0;
    unawaited(HapticFeedback.selectionClick());
    _armStreak();
  }

  void _streakTap(int side) {
    _streakTaps++;
    if (_streakTaps.isEven) {
      _streak += side;
      unawaited(HapticFeedback.selectionClick());
    }
    _armStreak();
  }

  void _armStreak() {
    _streakTimer?.cancel();
    _streakTimer = Timer(const Duration(milliseconds: 700), () {
      if (mounted) setState(_commitStreak);
    });
    setState(() {});
  }

  void _commitStreak() {
    _streakTimer?.cancel();
    final steps = _streak;
    _streak = 0;
    final state = _session.player.state;
    if (steps == 0 || !_session.ready || state.duration <= Duration.zero) {
      return;
    }
    final target = (state.position.inMilliseconds + steps * 10000).clamp(
      0,
      state.duration.inMilliseconds,
    );
    unawaited(_session.seekTo(Duration(milliseconds: target)));
  }

  void _pressStart() {
    if (!_session.ready) return;
    _session.holdBoost();
    unawaited(HapticFeedback.selectionClick());
    setState(() => _holding = true);
  }

  void _pressEnd() {
    if (!_holding) return;
    _session.endBoost();
    setState(() => _holding = false);
  }

  void _rotate(bool full) => unawaited(
    desktopPlatform
        ? setWindowFullScreen(!full)
        : AppDevice.setPlayerOrientation(
            full ? PlayerOrientation.portrait : PlayerOrientation.landscape,
          ),
  );

  void _back(bool full) {
    if (_panel != null) {
      setState(() => _panel = null);
      _scheduleHide();
    } else if (full) {
      _rotate(true);
    }
  }

  void _openPanel(_Panel panel) {
    _hideTimer?.cancel();
    setState(() => _panel = panel);
  }

  void _closePanel() {
    setState(() => _panel = null);
    _scheduleHide();
  }

  void _saveFailed() {
    if (!mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('播放偏好未能保存，请重试')));
  }

  Future<void> _change(Future<void> Function() change) async {
    try {
      await change();
    } catch (_) {
      _saveFailed();
    }
  }

  Future<void> _openSettings() {
    _hideTimer?.cancel();
    return showPlayerSheet(
      context,
      (_) => PlaybackSettings(session: _session, onFailure: _saveFailed),
      dark: false,
    ).whenComplete(() {
      if (mounted) _scheduleHide();
    });
  }

  @override
  Widget build(BuildContext context) {
    final full = MediaQuery.orientationOf(context) == Orientation.landscape;
    final theme = Theme.of(context);
    final dark = playerTheme(context);
    final video = Theme(
      data: dark,
      child: Builder(builder: (context) => _video(context, full)),
    );
    return PopScope(
      canPop: !full && _panel == null,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) _back(full);
      },
      child: AnnotatedRegion<SystemUiOverlayStyle>(
        value: full
            ? AppTheme.systemBars(Brightness.dark)
            : AppTheme.systemBars(theme.brightness).copyWith(
                statusBarIconBrightness: Brightness.light,
                statusBarBrightness: Brightness.dark,
              ),
        child: full
            ? Scaffold(backgroundColor: Colors.black, body: video)
            : Scaffold(
                backgroundColor: Colors.black,
                body: Theme(
                  data: dark,
                  child: Column(
                    children: [
                      _appBar(context),
                      // 整块区域（视频 + 信息流）都在 PageView 里，整屏上下滑。
                      Expanded(child: video),
                      _episodeBar(context),
                    ],
                  ),
                ),
              ),
      ),
    );
  }

  Widget _video(BuildContext context, bool full) {
    final session = _session;
    final visible = _visible || _pinned;
    final sidePanel = switch (_panel) {
      final panel? when full && _sidePanel(panel) => _panelBody(context, panel),
      _ => null,
    };
    if (sidePanel != null && !_sidePanelShown) _sidePanelMount++;
    _sidePanelShown = sidePanel != null;
    return Stack(
      fit: StackFit.expand,
      children: [
        NotificationListener<ScrollNotification>(
          onNotification: _scrolled,
          child: PageView.builder(
            controller: _pages,
            scrollDirection: Axis.vertical,
            itemCount:
                session.detail.episodes.length +
                (session.nextSeason != null ? 1 : 0),
            itemBuilder: (context, index) => full
                ? _episode(context, index, full)
                : _pageContent(context, index),
          ),
        ),
        FadePresence(
          duration: Motion.medium,
          child: session.ready && session.stalled
              ? const IgnorePointer(
                  child: Center(child: CircularProgressIndicator()),
                )
              : null,
        ),
        FadePresence(
          duration: Motion.medium,
          child: session.loading ? PlaybackLoading(session: session) : null,
        ),
        FadePresence(
          duration: Motion.medium,
          child: switch (session.error) {
            final error? => PlaybackFailure(session: session, error: error),
            null => null,
          },
        ),
        FadePresence(
          scaleFrom: .9,
          child: _streak != 0 ? _streakBadge(full) : null,
        ),
        if (full)
          IgnorePointer(
            ignoring: !visible,
            child: AnimatedOpacity(
              opacity: visible ? 1 : 0,
              duration: const Duration(milliseconds: 180),
              child: Listener(
                onPointerDown: (_) => _scheduleHide(),
                child: _fullControls(context),
              ),
            ),
          ),
        if (full && !visible && session.store.playbackPreferences.progressLine)
          Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            height: 2,
            child: IgnorePointer(child: _thinProgress(context)),
          ),
        Positioned(
          left: 0,
          right: 0,
          top: full ? 24 : 12,
          child: FadePresence(
            child: session.boosting
                ? IgnorePointer(child: Center(child: _boostBadge()))
                : null,
          ),
        ),
        if (session.saveWarning case final warning?)
          Positioned(
            left: 12,
            right: 12,
            bottom: full ? 120 : 64,
            child: _saveWarning(warning),
          ),
        Positioned.fill(
          child: FadePresence(
            child: switch (_panel) {
              final panel? when full => GestureDetector(
                onTap: _closePanel,
                child: ColoredBox(
                  color: _sidePanel(panel)
                      ? const Color(0x4D000000)
                      : Colors.transparent,
                ),
              ),
              _ => null,
            },
          ),
        ),
        Positioned(
          top: 0,
          right: 0,
          bottom: 0,
          width: 360,
          child: FadePresence(
            // A fresh key on every show keeps reopen-during-fade from reusing
            // the closing panel's state, matching the old unmount behavior.
            key: ValueKey(_sidePanelMount),
            duration: Motion.long,
            slideFrom: const Offset(1, 0),
            child: sidePanel,
          ),
        ),
        if (_panel case final panel? when full)
          if (!_sidePanel(panel))
            Positioned(
              left: 0,
              top: 0,
              child: CompositedTransformFollower(
                link: _links[panel]!,
                showWhenUnlinked: false,
                targetAnchor: Alignment.topCenter,
                followerAnchor: Alignment.bottomCenter,
                offset: const Offset(0, -6),
                child: _popup(context, panel),
              ),
            ),
      ],
    );
  }

  /// 最后一集之后的一页：继续上滑衔接下一季。
  Widget _nextSeasonPage(BuildContext context) {
    final season = _session.nextSeason;
    if (season == null) return const SizedBox.shrink();
    final advancing = _session.advancingSeason;
    return ColoredBox(
      color: Colors.black,
      child: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          spacing: 12,
          children: [
            Text(
              season.label,
              style: const TextStyle(
                color: Colors.white,
                fontSize: 20,
                fontWeight: FontWeight.w800,
              ),
            ),
            if (season.episodes > 0)
              Text(
                '共 ${season.episodes} 集',
                style: TextStyle(
                  color: Colors.white.withValues(alpha: .72),
                  fontSize: 13,
                ),
              ),
            if (advancing) const CircularProgressIndicator(),
            Text(
              advancing ? '正在加载${season.label}' : '继续上滑播放${season.label}',
              style: TextStyle(
                color: Colors.white.withValues(alpha: .64),
                fontSize: 13,
              ),
            ),
            if (!advancing)
              FilledButton(
                onPressed: () => unawaited(_session.playNextSeason()),
                child: Text('播放${season.label}'),
              ),
          ],
        ),
      ),
    );
  }

  Widget _episode(BuildContext context, int index, bool full) {
    final session = _session;
    if (index >= session.detail.episodes.length) {
      return _nextSeasonPage(context);
    }
    final current = index == session.index;
    final player = current
        ? session.player
        : index == session.standbyIndex
        ? session.standby
        : null;
    final width = MediaQuery.sizeOf(context).width;
    return Stack(
      fit: StackFit.expand,
      children: [
        if (player != null)
          PlayerVideo(
            key: ValueKey(player),
            handle: widget.handle,
            player: player,
            covered: session.surfaceCover,
          )
        else
          IgnorePointer(
            child: Center(
              child: Text(
                '第 ${session.detail.episodes[index].number} 集',
                style: TextStyle(
                  color: Colors.white.withValues(alpha: .72),
                  fontSize: 15,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
          ),
        if (current) ...[
          DanmakuOverlay(
            controller: session.danmaku,
            aspectRatio: session.aspectRatio,
            style: session.danmakuStyle,
          ),
          Center(
            child: IgnorePointer(
              child: AnimatedOpacity(
                opacity: session.ready && session.paused ? 1 : 0,
                duration: Motion.of(context, Motion.short),
                curve: Motion.standard,
                child: AnimatedScale(
                  scale: session.ready && session.paused ? 1 : .8,
                  duration: Motion.of(context, Motion.short),
                  curve: Motion.enter,
                  child: Container(
                    width: 72,
                    height: 72,
                    decoration: BoxDecoration(
                      color: Colors.black.withValues(alpha: .35),
                      shape: BoxShape.circle,
                    ),
                    child: Icon(
                      Icons.play_arrow_rounded,
                      size: 44,
                      color: Colors.white.withValues(alpha: .9),
                    ),
                  ),
                ),
              ),
            ),
          ),
          SwipeControls(
            session: session,
            enabled: full,
            levels: full && session.store.playbackPreferences.swipeLevels,
            // 中间区域的上下滑用于翻集，左右两侧仍调节亮度/音量。
            verticalRegion: session.detail.episodes.length > 1
                ? (position) =>
                      position.dx < width * .18 || position.dx > width * .82
                : null,
            child: LayoutBuilder(
              builder: (context, constraints) => GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTapUp: (details) =>
                    _tapUp(details, constraints.maxWidth, full),
                onLongPressStart: (_) => _pressStart(),
                onLongPressEnd: (_) => _pressEnd(),
                onLongPressCancel: _pressEnd,
              ),
            ),
          ),
        ],
      ],
    );
  }

  bool _sidePanel(_Panel panel) =>
      panel == _Panel.episodes || panel == _Panel.settings;

  Widget _fullControls(BuildContext context) {
    final session = _session;
    final padding = MediaQuery.viewPaddingOf(context);
    final left = math.max(padding.left, 20.0);
    final right = math.max(padding.right, 20.0);
    const shadow = [Shadow(color: Color(0x66000000), blurRadius: 2)];
    final ready = session.ready;
    return Stack(
      fit: StackFit.expand,
      children: [
        const IgnorePointer(
          child: Align(
            alignment: Alignment.topCenter,
            child: _Shade(height: 110, top: true),
          ),
        ),
        const IgnorePointer(
          child: Align(
            alignment: Alignment.bottomCenter,
            child: _Shade(height: 150, top: false),
          ),
        ),
        Positioned(
          left: left,
          right: right,
          top: 8,
          height: 56,
          child: Row(
            children: [
              _icon(Icons.arrow_back_rounded, '退出全屏', () => _rotate(true)),
              const SizedBox(width: 4),
              Expanded(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      session.detail.drama.title,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: Colors.white,
                        fontSize: 16,
                        fontWeight: FontWeight.w700,
                        shadows: shadow,
                      ),
                    ),
                    Text(
                      '第 ${session.episode.number} 集',
                      style: TextStyle(
                        color: Colors.white.withValues(alpha: .78),
                        fontSize: 12,
                        shadows: shadow,
                      ),
                    ),
                  ],
                ),
              ),
              FollowIcon(
                store: widget.store,
                drama: session.detail.drama,
                color: Colors.white,
                activeColor: Theme.of(context).colorScheme.primaryFixedDim,
                size: 22,
                extent: 44,
              ),
              const SizedBox(width: 8),
              if (_battery case final battery?) ...[
                _batteryMark(battery),
                const SizedBox(width: 12),
              ],
              Text(
                '${_two(_now.hour)}:${_two(_now.minute)}',
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                  fontFeatures: [FontFeature.tabularFigures()],
                ),
              ),
              const SizedBox(width: 4),
              if (session.danmakuSupported) _danmakuButton(context),
              if (widget.onMiniWindow case final onMiniWindow?)
                _icon(Icons.picture_in_picture_alt_rounded, '小窗', onMiniWindow),
              _icon(
                Icons.settings_rounded,
                '播放设置',
                () => _openPanel(_Panel.settings),
              ),
            ],
          ),
        ),
        Positioned(
          left: left + 8,
          right: right + 8,
          bottom: 60,
          height: 28,
          child: _progress(context, thumb: 8),
        ),
        Positioned(
          left: left,
          right: right,
          bottom: 8,
          height: 48,
          child: Row(
            children: [
              _playButton(size: 44, iconSize: 30),
              const SizedBox(width: 4),
              _clock(13),
              const SizedBox(width: 8),
              _icon(
                Icons.skip_previous_rounded,
                '上一集',
                session.hasPrevious
                    ? () => unawaited(session.previous())
                    : null,
                iconSize: 28,
              ),
              _icon(
                Icons.skip_next_rounded,
                '下一集',
                session.hasNext ? () => unawaited(session.next()) : null,
                iconSize: 28,
              ),
              const Spacer(),
              _icon(
                Icons.playlist_play_rounded,
                '选集',
                () => _openPanel(_Panel.episodes),
                iconSize: 28,
              ),
              CompositedTransformTarget(
                link: _links[_Panel.quality]!,
                child: _textButton(
                  session.qualityLabel,
                  '清晰度',
                  ready && !session.local
                      ? () => _openPanel(_Panel.quality)
                      : null,
                ),
              ),
              CompositedTransformTarget(
                link: _links[_Panel.speed]!,
                child: _textButton(
                  '${session.speed}x',
                  '倍速',
                  ready ? () => _openPanel(_Panel.speed) : null,
                  bold: true,
                ),
              ),
              _icon(Icons.fullscreen_exit_rounded, '退出全屏', () => _rotate(true)),
            ],
          ),
        ),
      ],
    );
  }

  Widget _playButton({required double size, required double iconSize}) {
    final playing = !_session.paused;
    return _icon(
      playing ? Icons.pause_rounded : Icons.play_arrow_rounded,
      playing ? '暂停' : '播放',
      _session.ready ? _session.togglePlayback : null,
      size: size,
      iconSize: iconSize,
    );
  }

  Widget _clock(double fontSize) => PlaybackProgressBuilder(
    player: _session.player,
    active: _visible || _pinned,
    builder: (context) {
      final state = _session.player.state;
      final position = _seekValue != null
          ? Duration(milliseconds: _seekValue!.round())
          : state.position;
      return Text(
        '${formatClock(position)} / ${formatClock(state.duration)}',
        maxLines: 1,
        style: TextStyle(
          fontSize: fontSize,
          color: Colors.white.withValues(alpha: .86),
          fontFeatures: const [FontFeature.tabularFigures()],
        ),
      );
    },
  );

  Widget _danmakuButton(BuildContext context) {
    final enabled = _session.danmakuEnabled;
    return _icon(
      enabled ? Icons.subtitles_rounded : Icons.subtitles_off_rounded,
      enabled ? '关闭弹幕' : '打开弹幕',
      () => _change(_session.toggleDanmaku),
      color: enabled ? Theme.of(context).colorScheme.primary : null,
      size: 40,
    );
  }

  Widget _icon(
    IconData icon,
    String label,
    VoidCallback? onPressed, {
    double size = 44,
    double iconSize = 24,
    Color? color,
  }) => IconButton(
    onPressed: onPressed,
    icon: ToggleIconSwitcher(child: Icon(icon, key: ValueKey(icon))),
    iconSize: iconSize,
    tooltip: label,
    color: color ?? Colors.white,
    disabledColor: Colors.white.withValues(alpha: .36),
    style: IconButton.styleFrom(
      fixedSize: Size.square(size),
      padding: EdgeInsets.zero,
    ),
  );

  Widget _textButton(
    String text,
    String label,
    VoidCallback? onPressed, {
    bool bold = false,
  }) => Semantics(
    label: label,
    button: true,
    child: TextButton(
      onPressed: onPressed,
      style: TextButton.styleFrom(
        fixedSize: const Size.fromHeight(40),
        padding: const EdgeInsets.symmetric(horizontal: 12),
        foregroundColor: Colors.white,
        disabledForegroundColor: Colors.white.withValues(alpha: .6),
        textStyle: TextStyle(
          fontSize: 14,
          fontWeight: bold ? FontWeight.w700 : FontWeight.w500,
        ),
      ),
      child: Text(text),
    ),
  );

  Widget _progress(BuildContext context, {required double thumb}) =>
      PlaybackProgressBuilder(
        player: _session.player,
        active: _visible || _pinned,
        builder: (context) => _progressBar(context, thumb: thumb),
      );

  Widget _progressBar(BuildContext context, {required double thumb}) {
    final session = _session;
    final state = session.player.state;
    final duration = state.duration;
    final seekable = session.ready && duration > Duration.zero;
    final max = duration.inMilliseconds > 0
        ? duration.inMilliseconds.toDouble()
        : 1.0;
    final position = (_seekValue ?? state.position.inMilliseconds.toDouble())
        .clamp(0.0, max);
    final colors = Theme.of(context).colorScheme;
    return SliderTheme(
      data: SliderTheme.of(context).copyWith(
        trackHeight: 3,
        padding: EdgeInsets.zero,
        activeTrackColor: colors.primary,
        thumbColor: colors.primary,
        inactiveTrackColor: Colors.white.withValues(alpha: .25),
        secondaryActiveTrackColor: Colors.white.withValues(alpha: .4),
        thumbShape: RoundSliderThumbShape(enabledThumbRadius: thumb),
        overlayShape: SliderComponentShape.noOverlay,
        trackShape: const RoundedRectSliderTrackShape(),
      ),
      child: ContainedSlider(
        slider: Slider(
          value: position,
          max: max,
          secondaryTrackValue: state.buffer.inMilliseconds.toDouble().clamp(
            0.0,
            max,
          ),
          semanticFormatterCallback: (value) =>
              formatClock(Duration(milliseconds: value.round())),
          onChangeStart: seekable
              ? (value) {
                  _seekTimer?.cancel();
                  _seekTarget = null;
                  _hideTimer?.cancel();
                  setState(() {
                    _dragging = true;
                    _seekValue = value;
                  });
                }
              : null,
          onChanged: seekable
              ? (value) => setState(() => _seekValue = value)
              : null,
          onChangeEnd: (value) {
            _dragging = false;
            if (seekable) {
              _seekTarget = value;
              _seekTimer?.cancel();
              _seekTimer = Timer(const Duration(seconds: 2), () {
                if (mounted) setState(_endSeek);
              });
              unawaited(session.seekTo(Duration(milliseconds: value.round())));
              setState(() {});
            } else {
              setState(_endSeek);
            }
            _scheduleHide();
          },
        ),
      ),
    );
  }

  Widget _thinProgress(BuildContext context) => PlaybackProgressBuilder(
    player: _session.player,
    builder: (context) {
      final state = _session.player.state;
      final duration = state.duration.inMilliseconds;
      return LinearProgressIndicator(
        value: duration > 0 ? state.position.inMilliseconds / duration : 0,
        minHeight: 2,
        color: Theme.of(context).colorScheme.primary,
        backgroundColor: Colors.white.withValues(alpha: .2),
      );
    },
  );

  Widget _streakBadge(bool full) {
    final forward = _streak > 0;
    return IgnorePointer(
      child: Align(
        alignment: Alignment(forward ? .75 : -.75, 0),
        child: Container(
          height: 40,
          padding: const EdgeInsets.symmetric(horizontal: 14),
          decoration: BoxDecoration(
            color: Colors.black.withValues(alpha: .5),
            borderRadius: BorderRadius.circular(20),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            spacing: 6,
            children: [
              Icon(
                forward
                    ? Icons.fast_forward_rounded
                    : Icons.fast_rewind_rounded,
                size: 20,
                color: Colors.white,
              ),
              Text(
                '${forward ? '快进' : '快退'} ${_streak.abs() * 10} 秒',
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 14,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _boostBadge() => Container(
    height: 32,
    padding: const EdgeInsets.symmetric(horizontal: 14),
    decoration: BoxDecoration(
      color: Colors.black.withValues(alpha: .5),
      borderRadius: BorderRadius.circular(16),
    ),
    child: const Row(
      mainAxisSize: MainAxisSize.min,
      spacing: 6,
      children: [
        Icon(Icons.fast_forward_rounded, size: 16, color: Colors.white),
        Text(
          '2 倍速播放中',
          style: TextStyle(
            color: Colors.white,
            fontSize: 14,
            fontWeight: FontWeight.w600,
          ),
        ),
      ],
    ),
  );

  Widget _batteryMark(({int level, bool charging}) battery) {
    const levels = [
      Icons.battery_0_bar_rounded,
      Icons.battery_1_bar_rounded,
      Icons.battery_2_bar_rounded,
      Icons.battery_3_bar_rounded,
      Icons.battery_4_bar_rounded,
      Icons.battery_5_bar_rounded,
      Icons.battery_6_bar_rounded,
      Icons.battery_full_rounded,
    ];
    final level = battery.level.clamp(0, 100);
    return Semantics(
      label: '电量 $level%',
      excludeSemantics: true,
      child: Row(
        mainAxisSize: MainAxisSize.min,
        spacing: 2,
        children: [
          Icon(
            battery.charging
                ? Icons.battery_charging_full_rounded
                : levels[(level * 7 / 100).round()],
            size: 18,
            color: Colors.white,
          ),
          Text(
            '$level%',
            style: const TextStyle(
              color: Colors.white,
              fontSize: 13,
              fontWeight: FontWeight.w600,
              fontFeatures: [FontFeature.tabularFigures()],
            ),
          ),
        ],
      ),
    );
  }

  Widget _saveWarning(String warning) => Material(
    color: const Color(0xE6322424),
    borderRadius: BorderRadius.circular(10),
    child: Padding(
      padding: const EdgeInsets.fromLTRB(14, 4, 4, 4),
      child: Row(
        children: [
          Expanded(
            child: Text(
              warning,
              style: const TextStyle(color: Colors.white, fontSize: 13),
            ),
          ),
          TextButton(
            onPressed: _session.saveProgress,
            child: const Text('重试保存'),
          ),
        ],
      ),
    ),
  );

  Widget _panelBody(BuildContext context, _Panel panel) {
    final colors = Theme.of(context).colorScheme;
    final right = MediaQuery.viewPaddingOf(context).right;
    final session = _session;
    final isEpisodes = panel == _Panel.episodes;
    final isSettings = panel == _Panel.settings;
    return Material(
      color: colors.surfaceContainerLow.withValues(alpha: .94),
      child: Padding(
        padding: EdgeInsets.only(right: right, top: 8),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            SheetHeader(
              children: [
                Text(
                  isEpisodes
                      ? '选集'
                      : isSettings
                      ? '播放设置'
                      : '',
                  style: sheetTitle,
                ),
                Expanded(
                  child: Text(
                    isEpisodes ? '共 ${session.detail.episodes.length} 集' : '',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: 13,
                      color: colors.onSurfaceVariant,
                    ),
                  ),
                ),
                IconButton(
                  onPressed: _closePanel,
                  icon: const Icon(Icons.close_rounded),
                  tooltip: '关闭',
                ),
              ],
            ),
            Expanded(
              child: isEpisodes
                  ? EpisodeGrid(
                      episodes: session.detail.episodes,
                      current: session.index,
                      selecting: false,
                      selected: const {},
                      touch: true,
                      onSelectionChanged: () {},
                      onPick: (index) {
                        _closePanel();
                        if (index != session.index) {
                          unawaited(session.play(index));
                        }
                      },
                    )
                  : _settingsContent(context),
            ),
          ],
        ),
      ),
    );
  }

  Widget _settingsContent(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final session = _session;
    return SingleChildScrollView(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            '播放速度',
            style: TextStyle(fontSize: 14, color: colors.onSurfaceVariant),
          ),
          const SizedBox(height: 8),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: playbackSpeeds.reversed
                .map(
                  (speed) => _settingChip<double>(
                    value: speed,
                    selected: session.speed == speed,
                    label: '${speed}x',
                    onTap: () => _change(() => session.setSpeed(speed)),
                  ),
                )
                .toList(),
          ),
          if (session.qualities.isNotEmpty) ...[
            const SizedBox(height: 20),
            Text(
              '画质',
              style: TextStyle(fontSize: 14, color: colors.onSurfaceVariant),
            ),
            const SizedBox(height: 8),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                ...session.qualities.map(
                  (q) => _settingChip<int>(
                    value: q,
                    selected: session.quality == q,
                    label: '${q}P',
                    onTap: () => _change(() => session.setQuality(q)),
                  ),
                ),
                _settingChip<int>(
                  value: 0,
                  selected: session.quality == 0,
                  label: '自动',
                  onTap: () => _change(() => session.setQuality(0)),
                ),
              ],
            ),
          ],
        ],
      ),
    );
  }

  Widget _settingChip<T>({
    required T value,
    required bool selected,
    required String label,
    required VoidCallback onTap,
  }) {
    final colors = Theme.of(context).colorScheme;
    return GestureDetector(
      onTap: onTap,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 150),
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        decoration: BoxDecoration(
          color: selected ? colors.primary : colors.surfaceContainerHighest,
          borderRadius: BorderRadius.circular(10),
          border: Border.all(
            color: selected ? colors.primary : colors.outlineVariant,
          ),
        ),
        child: Text(
          label,
          style: TextStyle(
            fontSize: 14,
            fontWeight: FontWeight.w600,
            color: selected ? colors.onPrimary : colors.onSurface,
          ),
        ),
      ),
    );
  }

  Widget _popup(BuildContext context, _Panel panel) {
    final session = _session;
    return Material(
      color: const Color(0xE0141414),
      borderRadius: BorderRadius.circular(12),
      child: Padding(
        padding: const EdgeInsets.all(6),
        child: panel == _Panel.speed
            ? _options<double>(
                values: playbackSpeeds.reversed.toList(),
                selected: session.speed,
                label: (value) => '${value}x',
                onSelected: (value) => _change(() => session.setSpeed(value)),
              )
            : _options<int>(
                values: [...session.qualities, 0],
                selected: session.quality,
                label: (value) => value == 0 ? '自动' : '${value}P',
                onSelected: (value) => _change(() => session.setQuality(value)),
              ),
      ),
    );
  }

  Widget _options<T>({
    required List<T> values,
    required T selected,
    required String Function(T) label,
    required void Function(T) onSelected,
  }) {
    final colors = Theme.of(context).colorScheme;
    return IntrinsicWidth(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          for (final value in values)
            InkWell(
              borderRadius: BorderRadius.circular(8),
              onTap: () {
                _closePanel();
                onSelected(value);
              },
              child: Container(
                height: 36,
                constraints: const BoxConstraints(minWidth: 72),
                padding: const EdgeInsets.symmetric(horizontal: 14),
                alignment: Alignment.center,
                child: Text(
                  label(value),
                  style: TextStyle(
                    fontSize: 14,
                    fontWeight: value == selected
                        ? FontWeight.w700
                        : FontWeight.w500,
                    color: value == selected ? colors.primary : Colors.white,
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }

  Widget _appBar(BuildContext context) {
    final session = _session;
    final padding = MediaQuery.paddingOf(context);
    return ColoredBox(
      color: Colors.black,
      child: Padding(
        padding: EdgeInsets.only(top: padding.top),
        child: SizedBox(
          height: 52,
          child: Row(
            children: [
              IconButton(
                onPressed: () => unawaited(Navigator.maybePop(context)),
                icon: const Icon(Icons.arrow_back_ios_new_rounded, size: 20),
                color: Colors.white,
                tooltip: '返回',
              ),
              Text(
                '第 ${session.episode.number} 集',
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 16,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const Spacer(),
              TextButton.icon(
                onPressed: _openSettings,
                icon: const Icon(Icons.speed_rounded, size: 18),
                label: Text('${_speedLabel(session.speed)}x'),
                style: TextButton.styleFrom(
                  foregroundColor: Colors.white,
                  textStyle: const TextStyle(
                    fontSize: 14,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              PopupMenuButton<String>(
                icon: const Icon(Icons.more_vert_rounded, color: Colors.white),
                tooltip: '更多',
                onSelected: (value) {
                  if (value == 'settings') {
                    unawaited(_openSettings());
                  } else if (value == 'mini') {
                    widget.onMiniWindow?.call();
                  }
                },
                itemBuilder: (context) => [
                  const PopupMenuItem(value: 'settings', child: Text('播放设置')),
                  if (widget.onMiniWindow != null)
                    const PopupMenuItem(value: 'mini', child: Text('小窗播放')),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  String _speedLabel(double speed) => speed == speed.roundToDouble()
      ? speed.toStringAsFixed(0)
      : speed.toString();

  Widget _fullscreenPill() => Material(
    color: Colors.white.withValues(alpha: .18),
    borderRadius: BorderRadius.circular(16),
    child: InkWell(
      borderRadius: BorderRadius.circular(16),
      onTap: widget.onFullscreen ?? () => _rotate(false),
      child: const Padding(
        padding: EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          spacing: 4,
          children: [
            Icon(Icons.fullscreen_rounded, size: 16, color: Colors.white),
            Text(
              '全屏观看',
              style: TextStyle(
                color: Colors.white,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
          ],
        ),
      ),
    ),
  );

  /// 竖屏手持时的一页：视频垂直居中，信息流贴底。
  /// 整页都在 PageView 里，所以手指放在任何位置上下滑都能换集。
  Widget _pageContent(BuildContext context, int index) {
    final session = _session;
    if (index >= session.detail.episodes.length) {
      return _nextSeasonPage(context);
    }
    final current = index == session.index;
    final player = current
        ? session.player
        : index == session.standbyIndex
        ? session.standby
        : null;
    return ColoredBox(
      color: Colors.black,
      child: Column(
        children: [
          Expanded(
            child: Center(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  AspectRatio(
                    aspectRatio: 16 / 9,
                    child: Stack(
                      fit: StackFit.expand,
                      children: [
                        if (player != null)
                          PlayerVideo(
                            key: ValueKey(player),
                            handle: widget.handle,
                            player: player,
                            covered: session.surfaceCover,
                          )
                        else
                          IgnorePointer(
                            child: Center(
                              child: Text(
                                '第 ${session.detail.episodes[index].number} 集',
                                style: TextStyle(
                                  color: Colors.white.withValues(alpha: .72),
                                  fontSize: 15,
                                  fontWeight: FontWeight.w600,
                                ),
                              ),
                            ),
                          ),
                        if (current) ...[
                          DanmakuOverlay(
                            controller: session.danmaku,
                            aspectRatio: session.aspectRatio,
                            style: session.danmakuStyle,
                          ),
                          IgnorePointer(
                            child: AnimatedScale(
                              duration: Motion.short,
                              scale: session.paused ? 1 : .8,
                              child: AnimatedOpacity(
                                duration: Motion.short,
                                opacity: session.paused ? 1 : 0,
                                child: Container(
                                  padding: const EdgeInsets.all(14),
                                  decoration: BoxDecoration(
                                    color: Colors.black.withValues(alpha: .45),
                                    shape: BoxShape.circle,
                                  ),
                                  child: const Icon(
                                    Icons.play_arrow_rounded,
                                    color: Colors.white,
                                    size: 48,
                                  ),
                                ),
                              ),
                            ),
                          ),
                          SwipeControls(
                            session: session,
                            enabled: false,
                            child: LayoutBuilder(
                              builder: (context, constraints) =>
                                  GestureDetector(
                                    behavior: HitTestBehavior.opaque,
                                    onTapUp: (details) => _tapUp(
                                      details,
                                      constraints.maxWidth,
                                      false,
                                    ),
                                    onLongPressStart: (_) => _pressStart(),
                                    onLongPressEnd: (_) => _pressEnd(),
                                    onLongPressCancel: _pressEnd,
                                  ),
                            ),
                          ),
                        ],
                      ],
                    ),
                  ),
                  const SizedBox(height: 10),
                  _fullscreenPill(),
                ],
              ),
            ),
          ),
          _feed(context),
          if (current) _portraitPlayerBar(context),
        ],
      ),
    );
  }

  /// 竖屏手持横片的底部播控条：进度线 + 时间 + 全屏按钮。
  /// 显示在信息流顶部、剧集选项上方。
  Widget _portraitPlayerBar(BuildContext context) {
    final session = _session;
    final colors = Theme.of(context).colorScheme;
    return Container(
      color: Colors.black,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          SizedBox(height: 4, child: _thinProgress(context)),
          Row(
            children: [
              _playButton(size: 36, iconSize: 22),
              const SizedBox(width: 2),
              _clock(12),
              const Spacer(),
              Text(
                '${session.episode.number}/${session.detail.episodes.length}',
                style: TextStyle(color: colors.onSurfaceVariant, fontSize: 12),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Widget _feed(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final session = _session;
    final drama = session.detail.drama;
    final meta = [
      SourceSite.byId(drama.source).name,
      '共 ${session.detail.episodes.length} 集',
      if (drama.releaseLabel != '状态未知') drama.releaseLabel,
      if (drama.category.isNotEmpty) drama.category,
    ];
    final description = drama.description.trim();
    return Stack(
      children: [
        Positioned.fill(
          child: SingleChildScrollView(
            padding: const EdgeInsets.fromLTRB(16, 14, 84, 16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  drama.title,
                  style: const TextStyle(
                    fontSize: 19,
                    fontWeight: FontWeight.w800,
                    height: 1.35,
                    color: Colors.white,
                  ),
                ),
                const SizedBox(height: 6),
                Text(
                  meta.join(' · '),
                  style: TextStyle(
                    fontSize: 13,
                    color: colors.onSurfaceVariant,
                  ),
                ),
                const SizedBox(height: 12),
                Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Expanded(
                      child: Text(
                        description.isEmpty ? '暂无简介' : description,
                        maxLines: _descExpanded ? null : 3,
                        overflow: _descExpanded ? null : TextOverflow.ellipsis,
                        style: TextStyle(
                          fontSize: 14,
                          height: 1.6,
                          color: description.isEmpty
                              ? colors.onSurfaceVariant
                              : colors.onSurface,
                        ),
                      ),
                    ),
                    if (description.length > 40)
                      TextButton(
                        onPressed: () =>
                            setState(() => _descExpanded = !_descExpanded),
                        style: TextButton.styleFrom(
                          visualDensity: VisualDensity.compact,
                          padding: const EdgeInsets.only(left: 8),
                          minimumSize: const Size(0, 32),
                          tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                        ),
                        child: Text(_descExpanded ? '收起' : '展开'),
                      ),
                  ],
                ),
              ],
            ),
          ),
        ),
        Positioned(top: 8, right: 4, child: _rail(context)),
      ],
    );
  }

  Widget _rail(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final session = _session;
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (session.danmakuSupported) ...[
          IconButton(
            onPressed: () => _change(session.toggleDanmaku),
            icon: ToggleIconSwitcher(
              child: Icon(
                session.danmakuEnabled
                    ? Icons.subtitles_rounded
                    : Icons.subtitles_off_rounded,
                key: ValueKey(session.danmakuEnabled),
              ),
            ),
            iconSize: 24,
            tooltip: session.danmakuEnabled ? '关闭弹幕' : '打开弹幕',
            color: session.danmakuEnabled
                ? colors.primary
                : colors.onSurfaceVariant,
          ),
          Text(
            '弹幕',
            style: TextStyle(
              fontSize: 11,
              color: session.danmakuEnabled
                  ? colors.primary
                  : colors.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: 6),
        ],
        FollowIcon(
          store: widget.store,
          drama: session.detail.drama,
          color: colors.onSurfaceVariant,
          activeColor: colors.primary,
          size: 26,
        ),
        Text(
          '收藏',
          style: TextStyle(fontSize: 11, color: colors.onSurfaceVariant),
        ),
        const SizedBox(height: 6),
        IconButton(
          onPressed: _share,
          icon: const Icon(Icons.share_outlined, size: 24),
          color: colors.onSurfaceVariant,
          tooltip: '分享',
        ),
        Text(
          '分享',
          style: TextStyle(fontSize: 11, color: colors.onSurfaceVariant),
        ),
      ],
    );
  }

  Future<void> _share() async {
    final drama = _session.detail.drama;
    await Clipboard.setData(
      ClipboardData(
        text: '${drama.title}（${SourceSite.byId(drama.source).name}）',
      ),
    );
    if (!mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('已复制剧名，可粘贴分享')));
  }

  Widget _episodeBar(BuildContext context) {
    final count = _session.detail.episodes.length;
    return SafeArea(
      top: false,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 6, 16, 8),
        child: Row(
          children: [
            Expanded(
              child: SizedBox(
                height: 48,
                child: Material(
                  color: const Color(0xFF1C1C1E),
                  borderRadius: BorderRadius.circular(12),
                  clipBehavior: Clip.antiAlias,
                  child: InkWell(
                    onTap: () => unawaited(_openEpisodes()),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 14),
                      child: Row(
                        spacing: 8,
                        children: [
                          const Text(
                            '选集',
                            style: TextStyle(
                              fontSize: 15,
                              fontWeight: FontWeight.w700,
                              color: Colors.white,
                            ),
                          ),
                          Expanded(
                            child: Text(
                              '共 $count 集 · 免费观看',
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: TextStyle(
                                fontSize: 13,
                                color: Colors.white.withValues(alpha: .6),
                              ),
                            ),
                          ),
                          Icon(
                            Icons.keyboard_arrow_up_rounded,
                            size: 20,
                            color: Colors.white.withValues(alpha: .6),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
            ),
            const SizedBox(width: 8),
            SizedBox.square(
              dimension: 48,
              child: Material(
                color: const Color(0xFF1C1C1E),
                borderRadius: BorderRadius.circular(12),
                clipBehavior: Clip.antiAlias,
                child: InkWell(
                  onTap: () => unawaited(_openSettings()),
                  child: const Icon(
                    Icons.tune_rounded,
                    size: 22,
                    color: Colors.white,
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _openEpisodes() => showPlayerSheet(
    context,
    (_) => _EpisodeSheet(session: _session, store: widget.store),
    dark: false,
  );
}

String _two(int value) => value.toString().padLeft(2, '0');

class _Shade extends StatelessWidget {
  const _Shade({required this.height, required this.top});
  final double height;
  final bool top;

  @override
  Widget build(BuildContext context) => SizedBox(
    width: double.infinity,
    height: height,
    child: DecoratedBox(
      decoration: BoxDecoration(
        gradient: LinearGradient(
          begin: top ? Alignment.topCenter : Alignment.bottomCenter,
          end: top ? Alignment.bottomCenter : Alignment.topCenter,
          colors: const [Color(0x99000000), Color(0x00000000)],
        ),
      ),
    ),
  );
}

class _EpisodeSheet extends StatefulWidget {
  const _EpisodeSheet({required this.session, required this.store});
  final PlaybackSession session;
  final LocalStore store;

  @override
  State<_EpisodeSheet> createState() => _EpisodeSheetState();
}

class _EpisodeSheetState extends State<_EpisodeSheet> {
  final _selected = <int>{};
  bool _selecting = false;

  PlaybackSession get _session => widget.session;

  bool get _canDownload =>
      AppScope.of(context).repository.supportsDownloads &&
      widget.store.canDownload;

  void _select(bool selecting) => setState(() {
    _selecting = selecting;
    _selected.clear();
  });

  void _download() {
    final detail = _session.detail;
    final episodes = [
      for (final index in _selected.toList()..sort()) detail.episodes[index],
    ];
    _select(false);
    unawaited(downloadEpisodes(context, widget.store, detail, episodes));
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final session = _session;
    final episodes = session.detail.episodes;
    final drama = session.detail.drama;
    final all = _selected.length == episodes.length;
    final status = drama.releaseLabel == '状态未知'
        ? ''
        : '${drama.releaseLabel} · ';
    return PopScope(
      canPop: !_selecting,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) _select(false);
      },
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          SheetHeader(
            children: _selecting
                ? [
                    Expanded(
                      child: Text(
                        '已选 ${_selected.length} 集',
                        style: sheetTitle,
                      ),
                    ),
                    TextButton(
                      onPressed: () => _select(false),
                      child: const Text('取消'),
                    ),
                    TextButton(
                      onPressed: () => setState(() {
                        all
                            ? _selected.clear()
                            : _selected.addAll(
                                List.generate(episodes.length, (i) => i),
                              );
                      }),
                      child: Text(all ? '取消全选' : '全选'),
                    ),
                    FilledButton(
                      onPressed: _selected.isEmpty ? null : _download,
                      child: const Text('下载'),
                    ),
                  ]
                : [
                    const Text('选集', style: sheetTitle),
                    Expanded(
                      child: Text(
                        '$status共 ${episodes.length} 集',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          fontSize: 13,
                          color: colors.onSurfaceVariant,
                        ),
                      ),
                    ),
                    if (_canDownload)
                      IconButton(
                        onPressed: () => _select(true),
                        icon: const Icon(Icons.download_rounded),
                        tooltip: '下载选集',
                      ),
                    IconButton(
                      onPressed: () => Navigator.pop(context),
                      icon: const Icon(Icons.close_rounded),
                      tooltip: '关闭',
                    ),
                  ],
          ),
          Expanded(
            child: ListenableBuilder(
              listenable: session,
              builder: (context, _) => EpisodeGrid(
                episodes: episodes,
                current: session.index,
                selecting: _selecting,
                selected: _selected,
                touch: true,
                onSelectionChanged: () => setState(() {}),
                onPick: (index) {
                  Navigator.pop(context);
                  if (index != session.index) unawaited(session.play(index));
                },
              ),
            ),
          ),
        ],
      ),
    );
  }
}
