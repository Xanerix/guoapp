import 'dart:async';
import 'dart:math' as math;
import 'dart:ui' show lerpDouble;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../drama_details.dart';
import '../../local_store.dart';
import '../../models.dart';
import '../app_scope.dart';
import '../downloads/download_picker_page.dart';
import '../player/playback_handle.dart';
import '../player/playback_page.dart';
import '../shell/app_shell.dart';
import '../widgets/choice_panel.dart';
import '../widgets/cover_backdrop.dart';
import '../widgets/cover_image.dart';
import '../widgets/focus_ring.dart';
import '../widgets/follow_button.dart';
import '../theme/motion.dart';
import '../widgets/motion/motion_widgets.dart';
import '../widgets/pull_refresh.dart';
import '../widgets/status_view.dart';
import 'season_strip.dart';
import 'television_detail.dart';

class _Metrics {
  const _Metrics({
    required this.cover,
    required this.coverRadius,
    required this.title,
    required this.meta,
    required this.tag,
    required this.note,
    required this.body,
    required this.bodyHeight,
    required this.button,
    required this.buttonText,
    required this.buttonIcon,
    required this.section,
    required this.summary,
    required this.chip,
    required this.chipRadius,
    required this.chipText,
    required this.chipPadding,
    required this.cell,
    required this.cellRadius,
    required this.cellText,
    required this.cellGap,
    this.columns = 0,
    this.cellWidth = 0,
    this.follow = 40,
  });
  final Size cover;
  final double coverRadius;
  final double title;
  final double meta;
  final double tag;
  final double note;
  final double body;
  final double bodyHeight;
  final double button;
  final double buttonText;
  final double buttonIcon;
  final double section;
  final double summary;
  final double chip;
  final double chipRadius;
  final double chipText;
  final double chipPadding;
  final double cell;
  final double cellRadius;
  final double cellText;
  final double cellGap;
  final int columns;
  final double cellWidth;
  final double follow;

  int columnsFor(double width) => columns > 0
      ? columns
      : math.max(4, ((width + cellGap) / (cellWidth + cellGap)).floor());
}

const _phone = _Metrics(
  cover: Size(120, 180),
  coverRadius: 12,
  title: 22,
  meta: 13,
  tag: 12,
  note: 12,
  body: 14,
  bodyHeight: 1.6,
  button: 52,
  buttonText: 16,
  buttonIcon: 24,
  section: 17,
  summary: 13,
  chip: 32,
  chipRadius: 8,
  chipText: 13,
  chipPadding: 14,
  cell: 54,
  cellRadius: 12,
  cellText: 16,
  cellGap: 10,
  columns: 5,
);

const _desktop = _Metrics(
  cover: Size(160, 240),
  coverRadius: 14,
  title: 28,
  meta: 14,
  tag: 12,
  note: 13,
  body: 14,
  bodyHeight: 1.7,
  button: 48,
  buttonText: 15,
  buttonIcon: 22,
  section: 18,
  summary: 13,
  chip: 32,
  chipRadius: 8,
  chipText: 13,
  chipPadding: 14,
  cell: 52,
  cellRadius: 12,
  cellText: 16,
  cellGap: 10,
  cellWidth: 72,
);

const _pad = _Metrics(
  cover: Size(150, 225),
  coverRadius: 14,
  title: 24,
  meta: 14,
  tag: 13,
  note: 13,
  body: 15,
  bodyHeight: 1.6,
  button: 56,
  buttonText: 16,
  buttonIcon: 24,
  section: 18,
  summary: 13,
  chip: 40,
  chipRadius: 10,
  chipText: 14,
  chipPadding: 16,
  cell: 56,
  cellRadius: 12,
  cellText: 17,
  cellGap: 12,
  cellWidth: 76,
  follow: 44,
);

class DetailPage extends StatefulWidget {
  const DetailPage({
    super.key,
    required this.store,
    required this.drama,
    this.autoplay = false,
  });
  final LocalStore store;
  final Drama drama;
  final bool autoplay;

  @override
  State<DetailPage> createState() => _DetailPageState();
}

class _DetailPageState extends State<DetailPage> {
  DramaDetails? _details;
  PlaybackHandle? _handle;
  bool _watching = false;
  var _currentEpisode = GlobalKey();
  String _keyedDrama = '';
  bool _expanded = false;
  final _played = <Object>{};
  final _groupKeys = <GlobalKey>[];
  PageController? _groupPager;
  String? _groupPagerDrama;
  int? _groupTarget;
  bool _groupDragging = false;
  final _groupScroll = ScrollController();
  final _televisionDetail = GlobalKey<TelevisionDetailState>();
  bool _entered = false;
  PlaybackHandle? _watched;
  late final bool _direct =
      !AppScope.of(context).television &&
      widget.store.playbackPreferences.directPlay;
  late bool _autoplayPending = widget.autoplay || _direct;
  late bool _curtain = widget.autoplay || _direct;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _details ??= DramaDetails(
      AppScope.of(context).repository,
      widget.store,
      widget.drama,
    )..addListener(_detailsChanged);
  }

  @override
  void dispose() {
    _handle?.dispose();
    _details?.dispose();
    _groupPager?.dispose();
    _groupScroll.dispose();
    super.dispose();
  }

  bool get _canDownload =>
      AppScope.of(context).repository.supportsDownloads &&
      widget.store.canDownload;

  bool get _ready => !_details!.loading && _details!.episodes.isNotEmpty;

  Future<void> _refresh() async {
    final details = _details!;
    await details.load();
    if (!mounted || details.error == null || details.episodes.isEmpty) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(details.error!)));
  }

  double _startAt(int index, {required bool resume}) {
    final watch = _details!.watch;
    return resume &&
            watch?.episode == _details!.episodes[index].number &&
            !watch!.finished
        ? watch.position
        : 0.0;
  }

  DramaDetail get _detail {
    final details = _details!;
    return DramaDetail(
      details.drama,
      details.episodes,
      warning: details.warning,
      seasons: details.seasons,
    );
  }

  PlaybackHandle _open(
    int index, {
    required double position,
    bool autoplay = true,
  }) => PlaybackHandle(
    repository: AppScope.of(context).repository,
    store: widget.store,
    detail: _detail,
    index: index,
    position: position,
    autoplay: autoplay,
    preopen: widget.store.playbackPreferences.preopens(
      television: AppScope.of(context).television,
    ),
  );

  void _detailsChanged() {
    final seasons = _details!.seasons;
    _handle?.session.syncSeasons(seasons);
    _watched?.session.syncSeasons(seasons);
    _preopen();
    unawaited(_autoplay());
  }

  Future<void> _autoplay() async {
    final details = _details!;
    if (!_autoplayPending || !mounted || details.loading) return;
    _autoplayPending = false;
    if (_ready && details.allowed) {
      if (_direct) await _awaitVideoSize();
      if (!mounted) return;
      final watched = await _play(details.resumeIndex, resume: true);
      if (!mounted || ModalRoute.of(context)?.isCurrent != true) return;
      if (_direct && watched) {
        Navigator.pop(context);
        return;
      }
    }
    if (_curtain) {
      setState(() => _curtain = false);
    } else if (AppScope.of(context).television &&
        details.currentIndex == null) {
      _televisionDetail.currentState?.focusPlay();
    }
  }

  Future<void> _awaitVideoSize() async {
    final session = _handle?.session;
    if (session == null) return;
    final known = Completer<void>();
    void check() {
      if ((session.aspectKnown || session.audio || session.error != null) &&
          !known.isCompleted) {
        known.complete();
      }
    }

    session.addListener(check);
    check();
    try {
      await known.future.timeout(const Duration(seconds: 3), onTimeout: () {});
    } finally {
      session.removeListener(check);
    }
  }

  void _liftCurtainAfter(Animation<double> entering) {
    void lift(AnimationStatus status) {
      if (status != AnimationStatus.completed) return;
      entering.removeStatusListener(lift);
      if (!mounted || !_curtain) return;
      _entered = true;
      setState(() => _curtain = false);
    }

    entering.addStatusListener(lift);
    lift(entering.status);
  }

  void _preopen() {
    final details = _details!;
    if (_handle != null ||
        _watching ||
        !mounted ||
        !_ready ||
        !details.allowed) {
      return;
    }
    final index = details.resumeIndex;
    _handle = _open(
      index,
      position: _startAt(index, resume: true),
      autoplay: false,
    );
  }

  Future<bool> _play(int index, {bool resume = false}) async {
    final details = _details!;
    final episode = details.episodes[index];
    if (episode.vip &&
        SourceSite.byId(details.drama.source).vipPrompt &&
        await showDialog<bool>(
              context: context,
              builder: (context) => AlertDialog(
                title: const Text('这是一集 VIP 内容'),
                content: const Text('站源可能只提供试看或限制播放。'),
                actions: [
                  TextButton(
                    onPressed: () => Navigator.pop(context, false),
                    child: const Text('取消'),
                  ),
                  FilledButton(
                    onPressed: () => Navigator.pop(context, true),
                    child: const Text('尝试播放'),
                  ),
                ],
              ),
            ) !=
            true) {
      return false;
    }
    if (!mounted || !details.allowed) return false;
    final position = _startAt(index, resume: resume);
    final handle = _handle ?? _open(index, position: position);
    _handle = null;
    handle.session.syncSeasons(details.seasons);
    handle.session.start(index, position: position);
    _watching = true;
    _watched = handle;
    final route = MaterialPageRoute<void>(
      builder: (_) => PlaybackPage(store: widget.store, handle: handle),
    );
    final closed = Navigator.of(context, rootNavigator: true).push(route);
    if (_curtain && !_direct) _liftCurtainAfter(route.animation!);
    await closed;
    _watching = false;
    _watched = null;
    if (!mounted || _curtain || ModalRoute.of(context)?.isCurrent != true) {
      return true;
    }
    final played = handle.session.detail.drama.id;
    if (played != details.drama.id) {
      final season = details.seasons
          .where((season) => season.drama.id == played)
          .firstOrNull;
      if (season != null) await details.selectSeason(season);
      if (!mounted) return true;
    }
    if (AppScope.of(context).television) _focusWatched(details);
    return true;
  }

  void _focusWatched(DramaDetails details) {
    final index = details.currentIndex;
    if (index == null) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _televisionDetail.currentState?.focusEpisode(index);
    });
  }

  void _download() => openPage<void>(
    context,
    (_) => DownloadPickerPage(store: widget.store, detail: _detail),
  );

  void _locate(int index) {
    final group = index ~/ episodeGroupSize;
    _details!.selectGroup(group);
    final pager = _groupPager;
    if (pager != null && pager.hasClients) {
      _groupTarget = null;
      pager.jumpToPage(group);
    }
    _revealGroupChip(group);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final target = _currentEpisode.currentContext;
      if (target == null || !target.mounted) return;
      final box = target.findRenderObject();
      final position = Scrollable.maybeOf(
        target,
        axis: Axis.vertical,
      )?.position;
      if (box == null || position == null) return;
      unawaited(
        position.ensureVisible(
          box,
          alignment: 0.3,
          duration: const Duration(milliseconds: 180),
          curve: Curves.easeOut,
        ),
      );
    });
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: _details!,
    builder: (context, _) {
      final details = _details!;
      _renewKeys(details.drama.id);
      if (!details.allowed) {
        return Scaffold(
          appBar: AppBar(),
          body: const StatusView(
            title: '当前用户无权访问',
            message: '请返回剧库后重新选择。',
            icon: Icons.lock_outline_rounded,
          ),
        );
      }
      if (_curtain) return _transition(widget.drama);
      final form = AppScope.formFactorOf(context);
      if (form == FormFactor.television) return _televisionPage(details);
      if (form == FormFactor.phone || MediaQuery.sizeOf(context).width < 840) {
        return _phonePage(details);
      }
      return _widePage(details, desktop: form == FormFactor.window);
    },
  );

  PreferredSizeWidget _appBar(DramaDetails details, double height) {
    final colors = Theme.of(context).colorScheme;
    return AppBar(
      toolbarHeight: height,
      backgroundColor: WidgetStateColor.resolveWith(
        (states) => states.contains(WidgetState.scrolledUnder)
            ? colors.surfaceContainer
            : Colors.transparent,
      ),
      actions: [
        IconButton(
          tooltip: '更新剧集信息',
          onPressed: details.loading ? null : _refresh,
          icon: const Icon(Icons.refresh_rounded),
        ),
        const SizedBox(width: 4),
      ],
    );
  }

  Widget _withBackdrop(Drama drama, double height, Widget child) {
    final theme = Theme.of(context);
    final surface = theme.colorScheme.surface;
    return Stack(
      children: [
        Positioned(
          left: 0,
          right: 0,
          top: 0,
          height: height,
          child: CoverBackdrop(
            drama: drama,
            sigma: 36,
            opacity: theme.brightness == Brightness.dark ? 0.55 : 0.45,
            overlay: LinearGradient(
              begin: Alignment.topCenter,
              end: Alignment.bottomCenter,
              stops: const [0, 0.92],
              colors: [surface.withValues(alpha: 0), surface],
            ),
          ),
        ),
        child,
      ],
    );
  }

  Widget _enter(int index, Widget child) => EnterOnce(
    id: 'section-$index',
    played: _played,
    index: index,
    child: child,
  );

  Widget _phonePage(DramaDetails details) {
    const m = _phone;
    final top = MediaQuery.paddingOf(context).top + kToolbarHeight;
    final drama = details.drama;
    return Scaffold(
      extendBodyBehindAppBar: true,
      appBar: _appBar(details, kToolbarHeight),
      body: PullRefresh(
        edgeOffset: top,
        onRefresh: _refresh,
        child: CustomScrollView(
          physics: const AlwaysScrollableScrollPhysics(),
          slivers: [
            SliverToBoxAdapter(
              child: _withBackdrop(
                drama,
                top + 250,
                Padding(
                  padding: EdgeInsets.fromLTRB(20, top + 4, 20, 0),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        spacing: 16,
                        children: [
                          _cover(drama, m),
                          Expanded(
                            child: Padding(
                              padding: const EdgeInsets.only(top: 6),
                              child: _enter(
                                0,
                                _followingHeadline(details, m, 6),
                              ),
                            ),
                          ),
                        ],
                      ),
                      if (_stats(drama, m) case final stats?)
                        Padding(
                          padding: const EdgeInsets.only(top: 14),
                          child: _enter(1, stats),
                        ),
                      if (_tags(drama, m) case final tags?)
                        Padding(
                          padding: const EdgeInsets.only(top: 10),
                          child: _enter(2, tags),
                        ),
                      if (drama.description.isNotEmpty)
                        Padding(
                          padding: const EdgeInsets.only(top: 12),
                          child: _enter(3, _description(drama, m)),
                        ),
                      if (details.warning.isNotEmpty)
                        Padding(
                          padding: const EdgeInsets.only(top: 4),
                          child: _warning(details.warning, m),
                        ),
                    ],
                  ),
                ),
              ),
            ),
            if (details.seasons.isNotEmpty)
              SliverToBoxAdapter(
                child: Padding(
                  padding: const EdgeInsets.fromLTRB(16, 14, 16, 4),
                  child: _seasonRow(details, m),
                ),
              ),
            if (details.episodes.isEmpty)
              SliverFillRemaining(
                hasScrollBody: false,
                child: _episodeStatus(details),
              )
            else ...[
              SliverToBoxAdapter(
                child: Padding(
                  padding: const EdgeInsets.fromLTRB(20, 8, 8, 0),
                  child: _episodeHeader(details, m),
                ),
              ),
              if (details.groupCount > 1)
                SliverToBoxAdapter(
                  child: SizedBox(
                    height: m.chip + 2,
                    child: ListView.separated(
                      scrollDirection: Axis.horizontal,
                      padding: const EdgeInsets.fromLTRB(20, 2, 20, 0),
                      itemCount: details.groupCount,
                      separatorBuilder: (_, _) => const SizedBox(width: 8),
                      itemBuilder: (context, group) =>
                          _groupChip(details, m, group),
                    ),
                  ),
                ),
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(0, 12, 0, 24),
                sliver: SliverToBoxAdapter(
                  child: _swipeGroups(details, m, gutter: 20),
                ),
              ),
            ],
          ],
        ),
      ),
      bottomNavigationBar: Material(
        color: Theme.of(context).colorScheme.surfaceContainerLow,
        child: SafeArea(
          top: false,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 10, 16, 16),
            child: Row(
              spacing: 12,
              children: [
                if (_canDownload)
                  FocusRing(
                    radius: m.button / 2,
                    child: IconButton.filledTonal(
                      tooltip: '下载选集',
                      onPressed: _ready ? _download : null,
                      style: IconButton.styleFrom(
                        minimumSize: Size.square(m.button),
                      ),
                      icon: const Icon(Icons.download_rounded),
                    ),
                  ),
                Expanded(child: _playButton(details, m)),
              ],
            ),
          ),
        ),
      ),
    );
  }

  Widget _widePage(DramaDetails details, {required bool desktop}) {
    final m = desktop ? _desktop : _pad;
    final top = MediaQuery.paddingOf(context).top + 64;
    final inset = desktop ? 32.0 : 24.0;
    final gap = desktop ? 40.0 : 32.0;
    final side = desktop ? 420.0 : 380.0;
    final drama = details.drama;
    final info = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          spacing: desktop ? 20 : 18,
          children: [
            _cover(drama, m),
            Expanded(
              child: Padding(
                padding: const EdgeInsets.only(top: 4),
                child: _enter(0, _followingHeadline(details, m, 4)),
              ),
            ),
          ],
        ),
        if (_stats(drama, m) case final stats?)
          Padding(
            padding: EdgeInsets.only(top: desktop ? 18 : 16),
            child: _enter(1, stats),
          ),
        if (_tags(drama, m) case final tags?)
          Padding(
            padding: const EdgeInsets.only(top: 10),
            child: _enter(2, tags),
          ),
        Padding(
          padding: EdgeInsets.only(top: desktop ? 22 : 20),
          child: Row(
            spacing: 12,
            children: [
              Expanded(child: _playButton(details, m)),
              if (_canDownload) _downloadButton(m),
            ],
          ),
        ),
        if (drama.description.isNotEmpty)
          Padding(
            padding: EdgeInsets.only(top: desktop ? 22 : 20),
            child: _enter(3, _description(drama, m)),
          ),
        if (details.warning.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: _warning(details.warning, m),
          ),
      ],
    );
    final episodeList = details.episodes.isEmpty
        ? SizedBox(
            key: const ValueKey('status'),
            height: 320,
            child: _episodeStatus(details),
          )
        : Column(
            key: const ValueKey('episodes'),
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              SizedBox(
                height: desktop ? 44 : 48,
                child: _episodeHeader(details, m),
              ),
              if (details.groupCount > 1)
                Padding(
                  padding: const EdgeInsets.only(top: 6),
                  child: _wideGroupBar(details, m),
                ),
              Padding(
                padding: const EdgeInsets.only(top: 14),
                child: desktop ? _grid(details, m) : _swipeGroups(details, m),
              ),
            ],
          );
    final episodes = Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (details.seasons.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: _seasonRow(details, m),
          ),
        FadeSwitcher(alignment: Alignment.topCenter, child: episodeList),
      ],
    );
    return Scaffold(
      extendBodyBehindAppBar: true,
      appBar: _appBar(details, 64),
      body: LayoutBuilder(
        builder: (context, constraints) {
          final split = constraints.maxWidth - inset * 2 >= side + gap + 480;
          if (!split) {
            return SingleChildScrollView(
              child: _withBackdrop(
                drama,
                top + 336,
                Padding(
                  padding: EdgeInsets.fromLTRB(inset, top + 4, inset, 40),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    spacing: gap,
                    children: [info, episodes],
                  ),
                ),
              ),
            );
          }
          return _withBackdrop(
            drama,
            top + 336,
            Row(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                SizedBox(
                  width: side + inset,
                  child: SingleChildScrollView(
                    padding: EdgeInsets.fromLTRB(inset, top + 4, 0, 40),
                    child: info,
                  ),
                ),
                SizedBox(width: gap),
                Expanded(
                  child: SingleChildScrollView(
                    padding: EdgeInsets.fromLTRB(0, top + 4, inset, 40),
                    child: episodes,
                  ),
                ),
              ],
            ),
          );
        },
      ),
    );
  }

  Widget _televisionPage(DramaDetails details) {
    if (_ready && !_entered) {
      _entered = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _televisionDetail.currentState?.focusPlay();
      });
    }
    return Scaffold(
      body: TelevisionDetail(
        key: _televisionDetail,
        details: details,
        store: widget.store,
        status: _episodeStatus(details, autofocus: true),
        onResume: _ready
            ? () => _play(details.resumeIndex, resume: true)
            : null,
        onEpisode: _play,
        onSeason: (season) => unawaited(_selectSeason(season)),
        onDescription: () => showDialog<void>(
          context: context,
          builder: (_) => _DescriptionDialog(drama: details.drama),
        ),
      ),
    );
  }

  Widget _transition(Drama drama) {
    final theme = Theme.of(context);
    final surface = theme.colorScheme.surface;
    return Scaffold(
      extendBodyBehindAppBar: true,
      appBar: AppBar(backgroundColor: Colors.transparent),
      body: Stack(
        fit: StackFit.expand,
        children: [
          CoverBackdrop(
            drama: drama,
            sigma: 36,
            opacity: theme.brightness == Brightness.dark ? 0.55 : 0.45,
            overlay: LinearGradient(
              begin: Alignment.topCenter,
              end: Alignment.bottomCenter,
              colors: [surface.withValues(alpha: .2), surface],
            ),
          ),
          Center(
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 32),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  _cover(drama, _phone),
                  const SizedBox(height: 20),
                  Text(
                    drama.title,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    textAlign: TextAlign.center,
                    style: const TextStyle(
                      fontSize: 18,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                  const SizedBox(height: 20),
                  const SizedBox.square(
                    dimension: 24,
                    child: CircularProgressIndicator(strokeWidth: 2.5),
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _cover(Drama drama, _Metrics m) => DecoratedBox(
    decoration: BoxDecoration(
      borderRadius: BorderRadius.circular(m.coverRadius),
      boxShadow: const [
        BoxShadow(
          color: Color(0x4D000000),
          blurRadius: 26,
          offset: Offset(0, 10),
        ),
      ],
    ),
    child: SizedBox.fromSize(
      size: m.cover,
      child: DramaCover(drama: drama, radius: m.coverRadius),
    ),
  );

  Widget _headline(
    DramaDetails details,
    _Metrics m, {
    double spacing = 8,
    List<Widget> trailing = const [],
  }) {
    final drama = details.drama;
    TextStyle sub(double size) => TextStyle(
      fontSize: size,
      height: 1.5,
      color: Theme.of(context).colorScheme.onSurfaceVariant,
    );
    final meta = [
      SourceSite.byId(drama.source).name,
      if (drama.episodes > 0) '共 ${drama.episodes} 集',
      if (drama.releaseStatus.isNotEmpty && drama.releaseStatus != 'unknown')
        drama.releaseLabel,
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      spacing: spacing,
      children: [
        Text(
          drama.title,
          maxLines: 3,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(
            fontSize: m.title,
            fontWeight: FontWeight.w800,
            height: 1.3,
          ),
        ),
        Text(meta.join(' · '), style: sub(m.meta)),
        if (drama.score.isNotEmpty)
          Text('评分 ${drama.score}', style: sub(m.meta)),
        if (drama.category.isNotEmpty) Text(drama.category, style: sub(m.meta)),
        if (drama.source == 'huangdou')
          Text(
            drama.vipStatus == null
                ? 'VIP 状态待补齐'
                : drama.vip
                ? 'VIP 内容'
                : '免费内容',
            style: sub(m.tag),
          ),
        ...trailing,
      ],
    );
  }

  Widget _followingHeadline(DramaDetails details, _Metrics m, double top) =>
      ConstrainedBox(
        constraints: BoxConstraints(minHeight: m.cover.height - top),
        child: IntrinsicHeight(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _headline(details, m),
              const Spacer(),
              Padding(
                padding: const EdgeInsets.only(top: 12),
                child: FollowPill(
                  store: widget.store,
                  drama: details.drama,
                  height: m.follow,
                ),
              ),
            ],
          ),
        ),
      );

  Widget? _stats(Drama drama, _Metrics m) {
    final stats = [
      if (drama.onlineDate.isNotEmpty) '${drama.onlineDate} 上线',
      if (drama.views.isNotEmpty && drama.views != '0') '播放 ${drama.views}',
    ];
    if (stats.isEmpty) return null;
    return Text(
      stats.join(' · '),
      style: TextStyle(
        fontSize: m.note,
        color: Theme.of(context).colorScheme.onSurfaceVariant,
      ),
    );
  }

  Widget? _tags(Drama drama, _Metrics m) {
    if (drama.tags.isEmpty) return null;
    final colors = Theme.of(context).colorScheme;
    return Wrap(
      spacing: 6,
      runSpacing: 6,
      children: [
        for (final tag in drama.tags.take(12))
          DecoratedBox(
            decoration: BoxDecoration(
              color: colors.surfaceContainerHighest,
              borderRadius: BorderRadius.circular(6),
            ),
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
              child: Text(tag, style: TextStyle(fontSize: m.tag)),
            ),
          ),
      ],
    );
  }

  Widget _description(Drama drama, _Metrics m) {
    final colors = Theme.of(context).colorScheme;
    final style = TextStyle(
      fontSize: m.body,
      height: m.bodyHeight,
      color: colors.onSurfaceVariant,
    );
    return LayoutBuilder(
      builder: (context, constraints) {
        final overflows = _exceeds(
          drama.description,
          style,
          3,
          constraints.maxWidth,
        );
        final label = Text(_expanded ? '收起简介' : '展开简介');
        void toggle() => setState(() => _expanded = !_expanded);
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            AnimatedSize(
              duration: Motion.of(context, Motion.long),
              curve: Motion.standard,
              alignment: Alignment.topCenter,
              child: Text(
                drama.description,
                maxLines: _expanded ? null : 3,
                overflow: _expanded ? null : TextOverflow.ellipsis,
                style: style,
              ),
            ),
            if (overflows)
              Align(
                alignment: Alignment.centerRight,
                child: TextButton(
                  onPressed: toggle,
                  child: FadeSwitcher(
                    duration: Motion.short,
                    child: KeyedSubtree(key: ValueKey(_expanded), child: label),
                  ),
                ),
              ),
          ],
        );
      },
    );
  }

  bool _exceeds(String text, TextStyle style, int maxLines, double width) {
    final painter = TextPainter(
      text: TextSpan(text: text, style: style),
      maxLines: maxLines,
      textDirection: Directionality.of(context),
      textScaler: MediaQuery.textScalerOf(context),
    )..layout(maxWidth: width);
    final exceeds = painter.didExceedMaxLines;
    painter.dispose();
    return exceeds;
  }

  Widget _warning(String text, _Metrics m) {
    final color = Theme.of(context).colorScheme.error;
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      spacing: 6,
      children: [
        Padding(
          padding: const EdgeInsets.only(top: 2),
          child: Icon(
            Icons.error_outline_rounded,
            size: m.note + 3,
            color: color,
          ),
        ),
        Expanded(
          child: Text(
            text,
            style: TextStyle(fontSize: m.note, height: 1.5, color: color),
          ),
        ),
      ],
    );
  }

  Widget _playButton(DramaDetails details, _Metrics m) {
    final label = details.watch != null && details.episodes.isNotEmpty
        ? '继续播放 · 第 ${details.episodes[details.resumeIndex].number} 集'
        : '立即播放';
    return FocusRing(
      radius: m.button / 2,
      child: FilledButton.icon(
        key: const ValueKey('detail-play'),
        onPressed: _ready
            ? () => _play(details.resumeIndex, resume: true)
            : null,
        style: FilledButton.styleFrom(
          minimumSize: Size(0, m.button),
          iconSize: m.buttonIcon,
          textStyle: TextStyle(
            fontSize: m.buttonText,
            fontWeight: FontWeight.w700,
          ),
        ),
        icon: const Icon(Icons.play_arrow_rounded),
        label: FadeSwitcher(
          child: Text(
            label,
            key: ValueKey(label),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
        ),
      ),
    );
  }

  Widget _downloadButton(_Metrics m) => FocusRing(
    radius: m.button / 2,
    child: FilledButton.tonalIcon(
      onPressed: _ready ? _download : null,
      style: FilledButton.styleFrom(
        minimumSize: Size(0, m.button),
        iconSize: m.buttonIcon - 2,
        textStyle: TextStyle(
          fontSize: m.buttonText - 1,
          fontWeight: FontWeight.w600,
        ),
      ),
      icon: const Icon(Icons.download_rounded),
      label: const Text('下载选集'),
    ),
  );

  Widget _episodeStatus(DramaDetails details, {bool autofocus = false}) =>
      FadeSwitcher(
        child: details.loading
            ? const StatusView.loading(key: ValueKey('loading'))
            : _episodeError(details, autofocus),
      );

  Widget _episodeError(DramaDetails details, bool autofocus) {
    return StatusView(
      key: const ValueKey('error'),
      title: details.error != null ? '剧集信息暂时不可用' : '暂时没有可播放的集数',
      message: details.error ?? '可以更新剧集信息后重试。',
      icon: Icons.error_outline_rounded,
      action: '重试',
      onAction: details.load,
      autofocus: autofocus,
    );
  }

  Widget _episodeTitle(DramaDetails details, _Metrics m) {
    final current = details.currentIndex;
    final summary = [
      '${details.episodes.length} 集',
      if (current != null) '当前第 ${details.episodes[current].number} 集',
    ].join(' · ');
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          '选集',
          style: TextStyle(fontSize: m.section, fontWeight: FontWeight.w700),
        ),
        const SizedBox(width: 8),
        Text(
          summary,
          style: TextStyle(
            fontSize: m.summary,
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
        ),
      ],
    );
  }

  Widget _episodeHeader(DramaDetails details, _Metrics m) => Row(
    children: [
      Expanded(
        child: Align(
          alignment: Alignment.centerLeft,
          child: _episodeTitle(details, m),
        ),
      ),
      if (details.currentIndex case final current?)
        TextButton(
          onPressed: () => _locate(current),
          child: const Text('定位当前'),
        ),
    ],
  );

  Widget _seasonRow(DramaDetails details, _Metrics m) => SeasonStrip(
    seasons: details.seasons,
    current: details.drama.id,
    store: widget.store,
    metaSize: m.meta,
    tagSize: m.tag,
    sectionSize: m.section,
    currentStatus: '正在浏览',
    collapsed: AppScope.formFactorOf(context) == FormFactor.window,
    onSelected: (season) => unawaited(_selectSeason(season)),
  );

  Future<void> _selectSeason(DramaSeason season) async {
    final details = _details!;
    if (season.drama.id == details.drama.id) return;
    _handle?.dispose();
    _handle = null;
    await details.selectSeason(season);
  }

  Widget _wideGroupBar(DramaDetails details, _Metrics m) {
    if (details.groupCount <= 8) {
      return Wrap(
        spacing: 8,
        runSpacing: 8,
        children: [
          for (var group = 0; group < details.groupCount; group++)
            _groupChip(details, m, group),
        ],
      );
    }
    return Row(
      spacing: 8,
      children: [
        Expanded(
          child: SizedBox(
            height: m.chip + 2,
            child: ListView.separated(
              controller: _groupScroll,
              scrollDirection: Axis.horizontal,
              itemCount: details.groupCount,
              separatorBuilder: (_, _) => const SizedBox(width: 8),
              itemBuilder: (context, group) => _groupChip(details, m, group),
            ),
          ),
        ),
        Builder(
          builder: (anchor) => FocusRing(
            radius: m.chipRadius,
            child: OutlinedButton.icon(
              onPressed: () => unawaited(_openGroupPicker(anchor, details)),
              style: OutlinedButton.styleFrom(
                shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(m.chipRadius),
                ),
                padding: const EdgeInsets.symmetric(horizontal: 10),
                minimumSize: Size(0, m.chip),
                foregroundColor: Theme.of(context).colorScheme.onSurfaceVariant,
                side: BorderSide(
                  color: Theme.of(context).colorScheme.outlineVariant,
                ),
                textStyle: TextStyle(fontSize: m.chipText),
              ),
              icon: const Icon(Icons.apps_rounded, size: 16),
              label: const Text('全部'),
            ),
          ),
        ),
      ],
    );
  }

  Future<void> _openGroupPicker(
    BuildContext anchor,
    DramaDetails details,
  ) async {
    final picked = await showChoicePanel<int>(
      context: anchor,
      title: '选集分段',
      selected: details.group,
      grid: ChoiceGrid.categories,
      options: [
        for (var group = 0; group < details.groupCount; group++)
          ChoiceOption(group, details.groupLabel(group)),
      ],
    );
    if (!mounted || picked == null) return;
    _swipeTo(details, picked);
  }

  Widget _groupChip(DramaDetails details, _Metrics m, int group) => _GroupChip(
    key: _groupKey(group),
    label: details.groupLabel(group),
    selected: group == details.group,
    metrics: m,
    onPressed: () => details.selectGroup(group),
  );

  PageController _groupPagerFor(DramaDetails details) {
    final drama = details.drama.id;
    final pager = _groupPager;
    if (pager != null && drama == _groupPagerDrama) return pager;
    if (pager != null) {
      WidgetsBinding.instance.addPostFrameCallback((_) => pager.dispose());
    }
    _groupPagerDrama = drama;
    _groupTarget = null;
    _groupDragging = false;
    return _groupPager = PageController(initialPage: details.group);
  }

  Widget _swipeGroups(DramaDetails details, _Metrics m, {double gutter = 0}) {
    final pager = _groupPagerFor(details);
    final group = details.group;
    WidgetsBinding.instance.addPostFrameCallback((_) => _followGroup(group));
    final extent = _cellExtent(details, m);
    return LayoutBuilder(
      builder: (context, constraints) {
        final columns = _columnsFor(
          details,
          m,
          constraints.maxWidth - gutter * 2,
        );
        double heightOf(int group) {
          final rows = (details.episodesIn(group).length / columns).ceil();
          return rows * extent + math.max(0, rows - 1) * m.cellGap;
        }

        return NotificationListener<ScrollNotification>(
          onNotification: (notification) =>
              _groupScrolled(notification, details),
          child: AnimatedBuilder(
            animation: pager,
            builder: (context, child) {
              final page =
                  pager.hasClients &&
                      pager.positions.length == 1 &&
                      pager.position.hasContentDimensions
                  ? pager.page ?? group.toDouble()
                  : group.toDouble();
              final last = details.groupCount - 1;
              final low = page.floor().clamp(0, last);
              final high = (low + 1).clamp(0, last);
              return SizedBox(
                height: lerpDouble(heightOf(low), heightOf(high), page - low),
                child: child,
              );
            },
            child: PageView.builder(
              key: ValueKey(details.drama.id),
              controller: pager,
              itemCount: details.groupCount,
              itemBuilder: (context, group) => OverflowBox(
                alignment: Alignment.topCenter,
                maxHeight: double.infinity,
                child: Padding(
                  padding: EdgeInsets.symmetric(horizontal: gutter),
                  child: _groupRows(details, m, group, columns, extent),
                ),
              ),
            ),
          ),
        );
      },
    );
  }

  bool _groupScrolled(ScrollNotification notification, DramaDetails details) {
    if (notification.depth != 0 ||
        notification.metrics.axis != Axis.horizontal) {
      return false;
    }
    if (notification is ScrollStartNotification &&
        notification.dragDetails != null) {
      _groupDragging = true;
      _groupTarget = null;
    } else if (notification is ScrollEndNotification && _groupDragging) {
      _groupDragging = false;
      final page = _groupPager?.page?.round();
      if (page != null && page != details.group) _swipeTo(details, page);
    }
    return false;
  }

  void _followGroup(int group) {
    final pager = _groupPager;
    if (!mounted || pager == null || !pager.hasClients || _groupDragging) {
      return;
    }
    final page = pager.page;
    if (page == null || _groupTarget == group) return;
    if ((page - group).abs() < .01) return;
    final from = page.round();
    if ((group - from).abs() > 1) {
      pager.jumpToPage(group > from ? group - 1 : group + 1);
    }
    _groupTarget = group;
    unawaited(
      pager
          .animateToPage(
            group,
            duration: Motion.of(context, Motion.long),
            curve: Motion.standard,
          )
          .whenComplete(() {
            if (_groupTarget == group) _groupTarget = null;
          }),
    );
  }

  void _renewKeys(String drama) {
    if (drama == _keyedDrama) return;
    _keyedDrama = drama;
    _currentEpisode = GlobalKey();
    _groupKeys.clear();
  }

  GlobalKey _groupKey(int group) {
    while (_groupKeys.length <= group) {
      _groupKeys.add(GlobalKey());
    }
    return _groupKeys[group];
  }

  void _swipeTo(DramaDetails details, int group) {
    details.selectGroup(group);
    _revealGroupChip(group);
  }

  void _revealGroupChip(int group) {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final chip = _groupKey(group).currentContext;
      if (chip == null || !chip.mounted) return;
      final position = Scrollable.maybeOf(
        chip,
        axis: Axis.horizontal,
      )?.position;
      final box = chip.findRenderObject();
      if (position == null || box == null) return;
      unawaited(
        position.ensureVisible(
          box,
          alignment: .5,
          duration: Motion.of(chip, Motion.medium),
          curve: Motion.standard,
        ),
      );
    });
  }

  int _columnsFor(DramaDetails details, _Metrics m, double width) {
    if (!episodesNamed(details.episodes)) return m.columnsFor(width);
    return math.max(1, ((width + m.cellGap) / (280 + m.cellGap)).floor());
  }

  double _cellExtent(DramaDetails details, _Metrics m) {
    final named = episodesNamed(details.episodes);
    final base = named && m.columns == 0 ? 44.0 : m.cell;
    final text = named && m.columns == 0 ? 14.0 : m.cellText;
    return math.max(base, MediaQuery.textScalerOf(context).scale(text) + 24);
  }

  Widget _grid(DramaDetails details, _Metrics m) {
    final extent = _cellExtent(details, m);
    return LayoutBuilder(
      builder: (context, constraints) {
        final fade = Motion.of(context, Motion.medium);
        return TweenAnimationBuilder<double>(
          tween: Tween(begin: fade == Duration.zero ? 1 : 0, end: 1),
          duration: fade,
          curve: Motion.standard,
          builder: (context, value, child) =>
              Opacity(opacity: value, child: child),
          child: _groupRows(
            details,
            m,
            details.group,
            _columnsFor(details, m, constraints.maxWidth),
            extent,
          ),
        );
      },
    );
  }

  Widget _groupRows(
    DramaDetails details,
    _Metrics m,
    int group,
    int columns,
    double extent,
  ) {
    final current = details.currentIndex;
    final start = group * episodeGroupSize;
    final episodes = details.episodesIn(group);
    final named = episodesNamed(details.episodes);
    Widget cell(int offset) {
      final index = start + offset;
      final episode = episodes[offset];
      return _EpisodeCell(
        key: index == current
            ? _currentEpisode
            : ValueKey('episode-${episode.number}'),
        episode: episode,
        named: named,
        metrics: m,
        current: index == current,
        watched: current != null && index < current,
        onPressed: () => _play(index),
      );
    }

    return Column(
      spacing: m.cellGap,
      children: [
        for (var row = 0; row < episodes.length; row += columns)
          SizedBox(
            height: extent,
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              spacing: m.cellGap,
              children: [
                for (var offset = row; offset < row + columns; offset++)
                  Expanded(
                    child: offset < episodes.length
                        ? cell(offset)
                        : const SizedBox.shrink(),
                  ),
              ],
            ),
          ),
      ],
    );
  }
}

class _GroupChip extends StatelessWidget {
  const _GroupChip({
    super.key,
    required this.label,
    required this.selected,
    required this.metrics,
    required this.onPressed,
  });
  final String label;
  final bool selected;
  final _Metrics metrics;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final m = metrics;
    final shape = RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(m.chipRadius),
    );
    final padding = EdgeInsets.symmetric(horizontal: m.chipPadding);
    final size = Size(0, m.chip);
    return FocusRing(
      radius: m.chipRadius,
      child: selected
          ? FilledButton.tonal(
              onPressed: onPressed,
              style: FilledButton.styleFrom(
                shape: shape,
                padding: padding,
                minimumSize: size,
                textStyle: TextStyle(
                  fontSize: m.chipText,
                  fontWeight: FontWeight.w600,
                ),
              ),
              child: Text(label),
            )
          : OutlinedButton(
              onPressed: onPressed,
              style: OutlinedButton.styleFrom(
                shape: shape,
                padding: padding,
                minimumSize: size,
                foregroundColor: colors.onSurfaceVariant,
                side: BorderSide(color: colors.outlineVariant),
                textStyle: TextStyle(fontSize: m.chipText),
              ),
              child: Text(label),
            ),
    );
  }
}

class _EpisodeCell extends StatelessWidget {
  const _EpisodeCell({
    super.key,
    required this.episode,
    required this.named,
    required this.metrics,
    required this.current,
    required this.watched,
    required this.onPressed,
  });
  final Episode episode;
  final bool named;
  final _Metrics metrics;
  final bool current;
  final bool watched;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    final dark = theme.brightness == Brightness.dark;
    final radius = BorderRadius.circular(metrics.cellRadius);
    final style = TextStyle(
      fontSize: named && metrics.columns == 0 ? 14 : metrics.cellText,
      fontWeight: current
          ? FontWeight.w800
          : watched
          ? FontWeight.w400
          : FontWeight.w600,
      color: current
          ? colors.onPrimaryContainer
          : watched
          ? colors.onSurfaceVariant
          : colors.onSurface,
    );
    return FocusRing(
      radius: metrics.cellRadius,
      child: Semantics(
        button: true,
        label:
            '${named ? episode.label : '第 ${episode.number} 集'}'
            '${episode.vip ? '，VIP' : ''}',
        excludeSemantics: true,
        child: Material(
          color: current ? colors.primaryContainer : colors.surfaceContainer,
          borderRadius: radius,
          clipBehavior: Clip.antiAlias,
          child: InkWell(
            onTap: onPressed,
            child: Stack(
              fit: StackFit.expand,
              children: [
                if (named)
                  Padding(
                    padding: EdgeInsets.fromLTRB(
                      14,
                      0,
                      episode.vip ? 36 : 14,
                      0,
                    ),
                    child: Align(
                      alignment: Alignment.centerLeft,
                      child: Text(
                        episode.label,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: style,
                      ),
                    ),
                  )
                else
                  Center(child: Text('${episode.number}', style: style)),
                if (episode.vip)
                  Positioned(
                    right: 5,
                    top: 4,
                    child: Text(
                      'VIP',
                      style: TextStyle(
                        fontSize: metrics.cellText - 7,
                        fontWeight: FontWeight.w800,
                        color: dark ? vipGold : const Color(0xFFB08200),
                      ),
                    ),
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _DescriptionDialog extends StatefulWidget {
  const _DescriptionDialog({required this.drama});
  final Drama drama;

  @override
  State<_DescriptionDialog> createState() => _DescriptionDialogState();
}

class _DescriptionDialogState extends State<_DescriptionDialog> {
  final _scroll = ScrollController();

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  KeyEventResult _onKey(KeyEvent event) {
    final step = switch (event.logicalKey) {
      LogicalKeyboardKey.arrowUp => -120.0,
      LogicalKeyboardKey.arrowDown => 120.0,
      _ => null,
    };
    if (event is KeyUpEvent || step == null) return KeyEventResult.ignored;
    if (_scroll.hasClients) {
      final position = _scroll.position;
      _scroll.animateTo(
        (position.pixels + step).clamp(0, position.maxScrollExtent),
        duration: const Duration(milliseconds: 150),
        curve: Curves.easeOut,
      );
    }
    return KeyEventResult.handled;
  }

  @override
  Widget build(BuildContext context) => Focus(
    canRequestFocus: false,
    skipTraversal: true,
    onKeyEvent: (_, event) => _onKey(event),
    child: AlertDialog(
      title: Text(widget.drama.title),
      content: SizedBox(
        width: 560,
        child: SingleChildScrollView(
          controller: _scroll,
          child: Text(
            widget.drama.description,
            style: const TextStyle(fontSize: 16, height: 1.7),
          ),
        ),
      ),
      actions: [
        FocusRing(
          radius: 20,
          child: TextButton(
            autofocus: true,
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('关闭'),
          ),
        ),
      ],
    ),
  );
}
