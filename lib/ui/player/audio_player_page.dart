import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:media_kit/media_kit.dart';

import '../../local_store.dart';
import '../../models.dart';
import '../../playback_preferences.dart';
import '../../playback_session.dart';
import '../app_scope.dart';
import '../downloads/download_actions.dart';
import '../downloads/download_picker_page.dart';
import '../shell/app_shell.dart';
import '../theme/app_theme.dart';
import '../widgets/choice_panel.dart';
import '../widgets/contained_slider.dart';
import '../widgets/cover_backdrop.dart';
import '../widgets/cover_image.dart';
import '../widgets/follow_button.dart';
import 'cast_page.dart';
import 'episode_grid.dart';
import 'mini_player.dart';
import 'playback_handle.dart';
import 'playback_sheets.dart';
import 'player_home_button.dart';
import 'player_surface.dart';
import 'portrait_player_page.dart';
import 'player_input.dart';

const _seekStep = Duration(seconds: 10);

class AudioPlayerPage extends StatefulWidget {
  const AudioPlayerPage({
    super.key,
    required this.store,
    required this.handle,
    this.onMiniWindow,
  });
  final LocalStore store;
  final PlaybackHandle handle;
  final VoidCallback? onMiniWindow;

  @override
  State<AudioPlayerPage> createState() => _AudioPlayerPageState();
}

class _AudioPlayerPageState extends State<AudioPlayerPage>
    with WidgetsBindingObserver {
  late final _inputs = PlayerInputController(
    session: () => _session,
    seekStep: _seekStep.inSeconds,
  );
  final List<StreamSubscription<dynamic>> _subscriptions = [];
  Player? _bound;
  double? _seekValue;
  bool _showPlaylist = true;

  PlaybackSession get _session => widget.handle.session;

  bool get _canDownload =>
      AppScope.of(context).repository.supportsDownloads &&
      widget.store.canDownload;

  bool get _touch => AppScope.formFactorOf(context) != FormFactor.window;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _session.addListener(_changed);
    _bind();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _session.removeListener(_changed);
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) =>
      _session.setForeground(playerVisible(state));

  void _bind() {
    final player = _session.player;
    if (identical(player, _bound)) return;
    _bound = player;
    for (final subscription in _subscriptions) {
      unawaited(subscription.cancel());
    }
    _subscriptions.clear();
    final stream = player.stream;
    for (final changes in [
      stream.duration,
      stream.buffering,
      stream.playing,
      stream.volume,
    ]) {
      _subscriptions.add(changes.listen((_) => _refresh()));
    }
  }

  void _changed() {
    _bind();
    _refresh();
  }

  void _refresh() {
    if (mounted) setState(() {});
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

  void _seekBy(Duration offset) {
    final state = _session.player.state;
    if (!_session.ready || state.duration <= Duration.zero) return;
    final target = state.position + offset;
    unawaited(
      _session.seekTo(
        target < Duration.zero
            ? Duration.zero
            : target > state.duration
            ? state.duration
            : target,
      ),
    );
  }

  Future<void> _openSettings({BuildContext? anchor}) {
    if (anchor != null) {
      final theme = playerTheme(context);
      return showAnchoredPanel<void>(
        context: anchor,
        width: 340,
        theme: theme,
        builder: (context) => ConstrainedBox(
          constraints: const BoxConstraints(maxHeight: 460),
          child: PlaybackSettings(
            session: _session,
            onFailure: _saveFailed,
            onMiniWindow: widget.onMiniWindow == null
                ? null
                : () {
                    Navigator.pop(context);
                    widget.onMiniWindow!();
                  },
          ),
        ),
      );
    }
    return showPlayerSheet(
      context,
      (_) => PlaybackSettings(
        session: _session,
        onFailure: _saveFailed,
        onMiniWindow: widget.onMiniWindow,
      ),
    );
  }

  Future<void> _openEpisodes() => showPlayerSheet(
    context,
    (_) => EpisodeSheet(
      session: _session,
      onSeasons: () => unawaited(_openSeasons()),
      canDownload: _canDownload,
      onDownload: (indexes) => unawaited(
        downloadEpisodes(context, widget.store, _session.detail, [
          for (final index in indexes) _session.detail.episodes[index],
        ]),
      ),
    ),
  );

  Future<void> _openSeasons({bool reopenEpisodes = true}) async {
    DramaSeason? picked;
    await showPlayerSheet(
      context,
      fitContent: true,
      (_) =>
          SeasonSheet(session: _session, onSeason: (season) => picked = season),
    );
    if (!mounted) return;
    if (picked case final season?) {
      await _switchSeason(season);
    } else if (reopenEpisodes) {
      await _openEpisodes();
    }
  }

  Future<void> _openSeasonsMenu(BuildContext anchor) async {
    final seasons = _session.detail.seasons;
    final current = _session.detail.drama.id;
    final picked = await showAnchoredPanel<DramaSeason>(
      context: anchor,
      width: 280,
      theme: playerTheme(context),
      builder: (context) => Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const SheetHeader(
            children: [
              Expanded(
                child: Text(
                  '切换分季',
                  style: TextStyle(fontSize: 16, fontWeight: FontWeight.w700),
                ),
              ),
            ],
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 4, 16, 16),
            child: Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final season in seasons)
                  ChoiceChip(
                    label: Text(season.label),
                    selected: season.drama.id == current,
                    showCheckmark: false,
                    shape: const StadiumBorder(),
                    onSelected: (_) => Navigator.pop(context, season),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
    if (!mounted || picked == null) return;
    await _switchSeason(picked);
  }

  Future<void> _switchSeason(DramaSeason season) async {
    if (_session.switchingSeason ||
        season.drama.id == _session.detail.drama.id) {
      return;
    }
    if (!await _session.playSeason(season) && mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('${season.label}切换失败，请稍后重试')));
    }
  }

  Future<void> _openSpeed({BuildContext? anchor}) => _choices(
    anchor: anchor,
    title: '倍速',
    choices: [
      for (final speed in playbackSpeeds)
        (
          label: '${speed}x',
          selected: speed == _session.speed,
          apply: () => _change(() => _session.setSpeed(speed)),
        ),
    ],
  );

  Future<void> _openSleep({BuildContext? anchor}) => _choices(
    anchor: anchor,
    title: sleepTimerLabel(_session),
    choices: [
      for (final choice in sleepChoices)
        (
          label: choice.label,
          selected: choice.selected(_session),
          apply: () => choice.apply(_session),
        ),
    ],
  );

  Future<void> _choices({
    BuildContext? anchor,
    required String title,
    required List<({String label, bool selected, VoidCallback apply})> choices,
  }) {
    Widget body(BuildContext context, {bool compact = false}) => Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        SheetHeader(
          children: [
            Expanded(
              child: Text(
                title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: compact
                    ? const TextStyle(fontSize: 16, fontWeight: FontWeight.w700)
                    : sheetTitle,
              ),
            ),
            if (!compact)
              IconButton(
                onPressed: () => Navigator.pop(context),
                icon: const Icon(Icons.close_rounded),
                tooltip: '关闭',
              ),
          ],
        ),
        Padding(
          padding: EdgeInsets.fromLTRB(16, 4, 16, compact ? 16 : 24),
          child: Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final choice in choices)
                ChoiceChip(
                  label: Text(choice.label),
                  selected: choice.selected,
                  showCheckmark: false,
                  shape: const StadiumBorder(),
                  onSelected: (_) {
                    Navigator.pop(context);
                    choice.apply();
                  },
                ),
            ],
          ),
        ),
      ],
    );
    if (anchor != null) {
      return showAnchoredPanel<void>(
        context: anchor,
        width: 300,
        theme: playerTheme(context),
        builder: (context) => body(context, compact: true),
      );
    }
    return showPlayerSheet(
      context,
      fitContent: true,
      (context) => body(context),
    );
  }

  void _openDownloads() => openPage<void>(
    context,
    (_) => DownloadPickerPage(store: widget.store, detail: _session.detail),
  );

  @override
  Widget build(BuildContext context) => AnnotatedRegion<SystemUiOverlayStyle>(
    value: AppTheme.systemBars(Brightness.dark),
    child: Theme(
      data: playerTheme(context),
      child: Scaffold(
        backgroundColor: Colors.black,
        body: DesktopPlayerShortcuts(
          store: widget.store,
          controller: _inputs,
          fallback: {
            const SingleActivator(LogicalKeyboardKey.space):
                _session.togglePlayback,
            const SingleActivator(LogicalKeyboardKey.arrowLeft): () =>
                _seekBy(-_seekStep),
            const SingleActivator(LogicalKeyboardKey.arrowRight): () =>
                _seekBy(_seekStep),
            const SingleActivator(LogicalKeyboardKey.arrowUp): () =>
                _session.changeVolume(5),
            const SingleActivator(LogicalKeyboardKey.arrowDown): () =>
                _session.changeVolume(-5),
            const SingleActivator(LogicalKeyboardKey.keyM): _session.toggleMute,
            const SingleActivator(LogicalKeyboardKey.pageUp): () {
              if (_session.hasPrevious) unawaited(_session.previous());
            },
            const SingleActivator(LogicalKeyboardKey.pageDown): () {
              if (_session.hasNext) unawaited(_session.next());
            },
          },
          child: Focus(
            autofocus: true,
            child: Stack(
              fit: StackFit.expand,
              children: [
                CoverBackdrop(
                  drama: _session.detail.drama,
                  sigma: 48,
                  opacity: 1,
                  overlay: const LinearGradient(
                    begin: Alignment.topCenter,
                    end: Alignment.bottomCenter,
                    colors: [Color(0x59000000), Color(0xB3000000)],
                  ),
                ),
                LayoutBuilder(
                  builder: (context, box) {
                    if (box.maxWidth >= 820) {
                      return SafeArea(child: _desktopWide(box));
                    }
                    if (box.maxWidth > box.maxHeight && box.maxWidth >= 600) {
                      return SafeArea(child: _compactWide(box));
                    }
                    return _narrow();
                  },
                ),
              ],
            ),
          ),
        ),
      ),
    ),
  );

  Widget _narrow() {
    final padding = MediaQuery.paddingOf(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Expanded(
          child: Stack(
            fit: StackFit.expand,
            children: [
              _artwork(fade: true),
              Positioned(
                left: padding.left,
                right: padding.right,
                top: padding.top,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [_header(wide: false), ?_saveWarning()],
                ),
              ),
            ],
          ),
        ),
        Padding(
          padding: EdgeInsets.fromLTRB(
            24 + padding.left,
            8,
            24 + padding.right,
            12 + padding.bottom,
          ),
          child: _panel(wide: false),
        ),
      ],
    );
  }

  Widget _compactWide(BoxConstraints box) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      _header(wide: true),
      ?_saveWarning(),
      Expanded(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(32, 8, 32, 24),
          child: Row(
            spacing: 40,
            children: [
              Expanded(
                child: _artwork(
                  fade: false,
                  maxHeight: math.min(340, box.maxHeight * .62),
                ),
              ),
              Expanded(
                child: Center(
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 440),
                    child: SingleChildScrollView(child: _panel(wide: true)),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    ],
  );

  Widget _desktopWide(BoxConstraints box) {
    final panelWidth = (box.maxWidth * .33).clamp(320.0, 420.0);
    final stage = Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 480),
        child: LayoutBuilder(
          builder: (context, stageBox) {
            final artHeight = math.min(
              320.0,
              math.max(150.0, stageBox.maxHeight - 290),
            );
            return SingleChildScrollView(
              padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 12),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  _artwork(fade: false, maxHeight: artHeight),
                  const SizedBox(height: 24),
                  _panel(
                    wide: true,
                    hasSidebar: true,
                    sidebarOpen: _showPlaylist,
                  ),
                ],
              ),
            );
          },
        ),
      ),
    );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _header(wide: true),
        ?_saveWarning(),
        Expanded(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 4, 24, 24),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Expanded(child: stage),
                if (_showPlaylist) ...[
                  const SizedBox(width: 16),
                  SizedBox(width: panelWidth, child: _playlistCard()),
                ],
              ],
            ),
          ),
        ),
      ],
    );
  }

  Widget _playlistCard() {
    final session = _session;
    final episodes = session.detail.episodes;
    final seasons = session.detail.seasons;
    final currentSeason = seasons
        .where((season) => season.drama.id == session.detail.drama.id)
        .firstOrNull;
    return ClipRRect(
      borderRadius: BorderRadius.circular(20),
      child: ColoredBox(
        color: Colors.black.withValues(alpha: .36),
        child: DecoratedBox(
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(20),
            border: Border.all(color: Colors.white.withValues(alpha: .1)),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 14, 12, 4),
                child: Row(
                  spacing: 8,
                  children: [
                    const Text(
                      '选集',
                      style: TextStyle(
                        color: Colors.white,
                        fontSize: 16,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    Expanded(
                      child: Text(
                        '${session.index + 1} / ${episodes.length}',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          color: Colors.white.withValues(alpha: .62),
                          fontSize: 13,
                          fontFeatures: const [FontFeature.tabularFigures()],
                        ),
                      ),
                    ),
                    if (seasons.isNotEmpty)
                      Builder(
                        builder: (anchor) => TextButton.icon(
                          onPressed: () => unawaited(_openSeasonsMenu(anchor)),
                          style: TextButton.styleFrom(
                            foregroundColor: Colors.white.withValues(alpha: .9),
                            padding: const EdgeInsets.symmetric(horizontal: 10),
                            minimumSize: const Size(0, 34),
                          ),
                          icon: const Icon(Icons.layers_rounded, size: 16),
                          label: Text(currentSeason?.label ?? '分季'),
                        ),
                      ),
                  ],
                ),
              ),
              Expanded(
                child: Padding(
                  padding: const EdgeInsets.only(bottom: 12),
                  child: EpisodeGrid(
                    episodes: episodes,
                    current: session.index,
                    selecting: false,
                    selected: const {},
                    touch: _touch,
                    inset: 12,
                    onSelectionChanged: () {},
                    onPick: (index) {
                      if (index != session.index) {
                        unawaited(session.play(index));
                      }
                    },
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _header({required bool wide}) {
    final home = PlayerHomeButton.available(context);
    final drama = _session.detail.drama;
    final source = SourceSite.byId(drama.source).name;
    if (wide) {
      return SizedBox(
        height: 56,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12),
          child: Row(
            spacing: 8,
            children: [
              IconButton(
                onPressed: () => unawaited(Navigator.maybePop(context)),
                icon: const Icon(Icons.arrow_back_rounded),
                color: Colors.white,
                tooltip: '返回',
              ),
              if (home) const PlayerHomeButton(),
              Expanded(
                child: Row(
                  spacing: 8,
                  children: [
                    Flexible(
                      child: Text(
                        drama.title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                          color: Colors.white,
                          fontSize: 16,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                    ),
                    if (source.isNotEmpty)
                      Text(
                        '· $source',
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          color: Colors.white.withValues(alpha: .65),
                          fontSize: 13,
                        ),
                      ),
                  ],
                ),
              ),
              if (widget.onMiniWindow case final mini?)
                IconButton(
                  onPressed: mini,
                  icon: const Icon(Icons.picture_in_picture_alt_rounded),
                  color: Colors.white.withValues(alpha: .85),
                  tooltip: '小窗播放',
                ),
            ],
          ),
        ),
      );
    }
    return SizedBox(
      height: 56,
      child: Row(
        children: [
          const SizedBox(width: 4),
          IconButton(
            onPressed: () => unawaited(Navigator.maybePop(context)),
            icon: const Icon(Icons.keyboard_arrow_down_rounded, size: 32),
            color: Colors.white,
            tooltip: '返回',
          ),
          if (home) const PlayerHomeButton(),
          Expanded(
            child: Text(
              source,
              textAlign: TextAlign.center,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: Colors.white.withValues(alpha: .85),
                fontSize: 16,
              ),
            ),
          ),
          SizedBox(width: home ? 100 : 52),
        ],
      ),
    );
  }

  Widget? _saveWarning() {
    final warning = _session.saveWarning;
    if (warning == null) return null;
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 0, 12, 8),
      child: Material(
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
      ),
    );
  }

  Widget _artwork({required bool fade, double? maxHeight}) {
    return PlayerWheelRegion(
      controller: _inputs,
      child: _artworkBody(fade: fade, maxHeight: maxHeight),
    );
  }

  Widget _artworkBody({required bool fade, double? maxHeight}) {
    if (_session.error case final error?) {
      return PlaybackFailure(session: _session, error: error);
    }
    final drama = _session.detail.drama;
    if (fade) {
      return ShaderMask(
        blendMode: BlendMode.dstIn,
        shaderCallback: (bounds) => const LinearGradient(
          begin: Alignment.topCenter,
          end: Alignment.bottomCenter,
          colors: [
            Colors.transparent,
            Colors.white,
            Colors.white,
            Colors.transparent,
          ],
          stops: [0, .22, .72, 1],
        ).createShader(bounds),
        child: DramaCover(drama: drama, radius: 0, details: false),
      );
    }
    final cover = AspectRatio(
      aspectRatio: 3 / 4,
      child: DecoratedBox(
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(18),
          boxShadow: const [
            BoxShadow(
              color: Color(0x73000000),
              blurRadius: 32,
              offset: Offset(0, 12),
            ),
          ],
        ),
        child: DramaCover(drama: drama, radius: 18, details: false),
      ),
    );
    return Center(
      child: maxHeight != null
          ? ConstrainedBox(
              constraints: BoxConstraints(
                maxHeight: maxHeight,
                maxWidth: maxHeight * 3 / 4,
              ),
              child: cover,
            )
          : cover,
    );
  }

  Widget _panel({
    required bool wide,
    bool hasSidebar = false,
    bool sidebarOpen = false,
  }) => Column(
    mainAxisSize: MainAxisSize.min,
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      _info(),
      const SizedBox(height: 18),
      PlaybackProgressBuilder(
        player: _session.player,
        builder: (context) => _progress(),
      ),
      const SizedBox(height: 14),
      _transport(wide: wide, hasSidebar: hasSidebar, sidebarOpen: sidebarOpen),
      if (wide && !_touch) ...[const SizedBox(height: 14), _volumeBar()],
      const SizedBox(height: 16),
      _actions(wide: wide),
    ],
  );

  Widget _info() {
    final drama = _session.detail.drama;
    return Row(
      children: [
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            spacing: 6,
            children: [
              Text(
                _session.episode.label,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                  color: Colors.white,
                  fontSize: 22,
                  fontWeight: FontWeight.w700,
                  height: 1.3,
                ),
              ),
              Text(
                drama.title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  color: Colors.white.withValues(alpha: .7),
                  fontSize: 15,
                ),
              ),
            ],
          ),
        ),
        FollowIcon(
          store: widget.store,
          drama: drama,
          color: Colors.white,
          activeColor: Theme.of(context).colorScheme.primaryFixedDim,
          size: 28,
        ),
      ],
    );
  }

  String get _status {
    if (_session.loading) return _session.loadingMessage;
    if (_session.sleepAtEpisodeEnd || _session.sleepAt != null) {
      return sleepTimerLabel(_session);
    }
    return '';
  }

  Widget _progress() {
    final session = _session;
    final state = session.player.state;
    final duration = state.duration;
    final seekable = session.ready && duration > Duration.zero;
    final max = duration.inMilliseconds > 0
        ? duration.inMilliseconds.toDouble()
        : 1.0;
    final position = (_seekValue ?? state.position.inMilliseconds.toDouble())
        .clamp(0.0, max);
    final dragging = _seekValue != null;
    final faint = Colors.white.withValues(alpha: .6);
    final caption = TextStyle(
      color: faint,
      fontSize: 12,
      fontFeatures: const [FontFeature.tabularFigures()],
    );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      spacing: 8,
      children: [
        SizedBox(
          height: 24,
          child: SliderTheme(
            data: SliderTheme.of(context).copyWith(
              trackHeight: dragging ? 4 : 2,
              padding: EdgeInsets.zero,
              activeTrackColor: Colors.white.withValues(alpha: .9),
              thumbColor: Colors.white,
              inactiveTrackColor: Colors.white.withValues(alpha: .2),
              secondaryActiveTrackColor: Colors.white.withValues(alpha: .35),
              thumbShape: RoundSliderThumbShape(
                enabledThumbRadius: dragging ? 8 : 5,
              ),
              overlayShape: SliderComponentShape.noOverlay,
              trackShape: const RoundedRectSliderTrackShape(),
            ),
            child: ContainedSlider(
              slider: Slider(
                value: position,
                max: max,
                secondaryTrackValue: state.buffer.inMilliseconds
                    .toDouble()
                    .clamp(0.0, max),
                semanticFormatterCallback: (value) =>
                    formatClock(Duration(milliseconds: value.round())),
                onChangeStart: seekable
                    ? (value) => setState(() => _seekValue = value)
                    : null,
                onChanged: seekable
                    ? (value) => setState(() => _seekValue = value)
                    : null,
                onChangeEnd: (value) async {
                  if (seekable) {
                    await session.seekTo(Duration(milliseconds: value.round()));
                  }
                  if (mounted) setState(() => _seekValue = null);
                },
              ),
            ),
          ),
        ),
        Row(
          children: [
            Text(
              formatClock(Duration(milliseconds: position.round())),
              style: caption,
            ),
            Expanded(
              child: Text(
                _status,
                textAlign: TextAlign.center,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: caption,
              ),
            ),
            Text(formatClock(duration), style: caption),
          ],
        ),
      ],
    );
  }

  Widget _volumeBar() {
    final volume = _session.player.state.volume.clamp(0.0, 100.0);
    final icon = volume == 0
        ? Icons.volume_off_rounded
        : volume < 50
        ? Icons.volume_down_rounded
        : Icons.volume_up_rounded;
    return Row(
      spacing: 8,
      children: [
        IconButton(
          onPressed: _session.toggleMute,
          icon: Icon(icon, size: 20),
          color: Colors.white.withValues(alpha: .78),
          tooltip: volume == 0 ? '取消静音' : '静音',
          visualDensity: VisualDensity.compact,
        ),
        Expanded(
          child: SliderTheme(
            data: SliderTheme.of(context).copyWith(
              trackHeight: 2,
              padding: EdgeInsets.zero,
              activeTrackColor: Colors.white.withValues(alpha: .85),
              thumbColor: Colors.white,
              inactiveTrackColor: Colors.white.withValues(alpha: .2),
              thumbShape: const RoundSliderThumbShape(enabledThumbRadius: 5),
              overlayShape: SliderComponentShape.noOverlay,
              trackShape: const RoundedRectSliderTrackShape(),
            ),
            child: ContainedSlider(
              slider: Slider(
                value: volume,
                max: 100,
                semanticFormatterCallback: (value) => '音量 ${value.round()}%',
                onChanged: _session.setVolume,
              ),
            ),
          ),
        ),
        SizedBox(
          width: 34,
          child: Text(
            '${volume.round()}%',
            textAlign: TextAlign.right,
            style: TextStyle(
              color: Colors.white.withValues(alpha: .6),
              fontSize: 12,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
        ),
      ],
    );
  }

  Widget _transport({
    required bool wide,
    required bool hasSidebar,
    required bool sidebarOpen,
  }) {
    final session = _session;
    final busy = session.loading || session.ready && session.stalled;
    return Row(
      mainAxisAlignment: MainAxisAlignment.spaceBetween,
      children: [
        Builder(
          builder: (anchor) => SizedBox.square(
            dimension: 48,
            child: TextButton(
              onPressed: session.ready
                  ? () => unawaited(_openSpeed(anchor: wide ? anchor : null))
                  : null,
              style: TextButton.styleFrom(
                foregroundColor: Colors.white,
                disabledForegroundColor: Colors.white38,
                padding: EdgeInsets.zero,
                shape: const CircleBorder(),
                textStyle: const TextStyle(
                  fontSize: 15,
                  fontWeight: FontWeight.w700,
                ),
              ),
              child: Text('${session.speed}x'),
            ),
          ),
        ),
        IconButton(
          onPressed: session.hasPrevious
              ? () => unawaited(session.previous())
              : null,
          icon: const Icon(Icons.skip_previous_rounded, size: 40),
          color: Colors.white,
          disabledColor: Colors.white38,
          tooltip: '上一集',
        ),
        SizedBox.square(
          dimension: 72,
          child: busy
              ? const Padding(
                  padding: EdgeInsets.all(18),
                  child: CircularProgressIndicator(
                    strokeWidth: 3,
                    color: Colors.white,
                  ),
                )
              : IconButton(
                  onPressed: session.togglePlayback,
                  icon: Icon(
                    session.paused
                        ? Icons.play_arrow_rounded
                        : Icons.pause_rounded,
                    size: 60,
                  ),
                  color: Colors.white,
                  tooltip: session.paused ? '播放' : '暂停',
                ),
        ),
        IconButton(
          onPressed: session.hasNext ? () => unawaited(session.next()) : null,
          icon: const Icon(Icons.skip_next_rounded, size: 40),
          color: Colors.white,
          disabledColor: Colors.white38,
          tooltip: '下一集',
        ),
        IconButton(
          onPressed: hasSidebar
              ? () => setState(() => _showPlaylist = !_showPlaylist)
              : () => unawaited(_openEpisodes()),
          icon: const Icon(Icons.queue_music_rounded, size: 28),
          color: sidebarOpen
              ? Theme.of(context).colorScheme.primary
              : Colors.white,
          tooltip: hasSidebar ? (sidebarOpen ? '收起选集' : '展开选集') : '选集',
        ),
      ],
    );
  }

  Widget _actions({required bool wide}) {
    final session = _session;
    final sleeping = session.sleepAtEpisodeEnd || session.sleepAt != null;
    final idle = Colors.white.withValues(alpha: .75);
    return Row(
      mainAxisAlignment: MainAxisAlignment.spaceAround,
      children: [
        Builder(
          builder: (anchor) => IconButton(
            onPressed: () =>
                unawaited(_openSleep(anchor: wide ? anchor : null)),
            icon: Icon(
              sleeping ? Icons.bedtime_rounded : Icons.bedtime_outlined,
            ),
            color: sleeping ? Theme.of(context).colorScheme.primary : idle,
            tooltip: sleepTimerLabel(session),
          ),
        ),
        if (_canDownload)
          IconButton(
            onPressed: _openDownloads,
            icon: const Icon(Icons.download_rounded),
            color: idle,
            tooltip: '下载选集',
          ),
        if (castSupported)
          IconButton(
            onPressed: () => unawaited(startCast(context, session)),
            icon: const Icon(Icons.cast_rounded),
            color: idle,
            tooltip: '投屏',
          ),
        Builder(
          builder: (anchor) => IconButton(
            onPressed: () =>
                unawaited(_openSettings(anchor: wide ? anchor : null)),
            icon: const Icon(Icons.more_horiz_rounded),
            color: idle,
            tooltip: '播放设置',
          ),
        ),
      ],
    );
  }
}
