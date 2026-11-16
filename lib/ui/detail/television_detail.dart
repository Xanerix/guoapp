import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../drama_details.dart';
import '../../local_store.dart';
import '../../models.dart';
import '../widgets/cover_image.dart';
import 'poster_glow.dart';

const _background = Color(0xFF101015);
const _accent = Color(0xFF8C7BFF);
const _focusText = Color(0xFF121216);
const _scoreColor = Color(0xFFF5C451);
const _inset = 48.0;
const _columns = 10;
const _cellHeight = 48.0;
const _rowGap = 10.0;
const _columnGap = 8.0;
const _rowExtent = _cellHeight + _rowGap;
const _focusDuration = Duration(milliseconds: 150);
const _focusShadow = [
  BoxShadow(color: Color(0x80000000), blurRadius: 22, offset: Offset(0, 8)),
];
final _arrows = {
  LogicalKeyboardKey.arrowUp,
  LogicalKeyboardKey.arrowDown,
  LogicalKeyboardKey.arrowLeft,
  LogicalKeyboardKey.arrowRight,
};

TextStyle _text(
  double size,
  double line,
  Color color, [
  FontWeight weight = FontWeight.w400,
]) => TextStyle(
  fontSize: size,
  height: line / size,
  color: color,
  fontWeight: weight,
);

(List<double>, List<Color>) _eased(Color color, List<(double, double)> keys) {
  const steps = 8;
  final stops = <double>[];
  final colors = <Color>[];
  for (var i = 0; i + 1 < keys.length; i++) {
    final (from, fromAlpha) = keys[i];
    final (to, toAlpha) = keys[i + 1];
    for (var step = 0; step < steps; step++) {
      final t = step / steps;
      stops.add(from + (to - from) * t);
      colors.add(
        color.withValues(
          alpha: fromAlpha + (toAlpha - fromAlpha) * t * t * (3 - 2 * t),
        ),
      );
    }
  }
  final (last, lastAlpha) = keys.last;
  stops.add(last);
  colors.add(color.withValues(alpha: lastAlpha));
  return (stops, colors);
}

enum _Row { actions, seasons, ranges, grid }

class TelevisionDetail extends StatefulWidget {
  const TelevisionDetail({
    super.key,
    required this.details,
    required this.store,
    required this.status,
    required this.onResume,
    required this.onEpisode,
    required this.onSeason,
    required this.onDescription,
  });
  final DramaDetails details;
  final LocalStore store;
  final Widget status;
  final VoidCallback? onResume;
  final ValueChanged<int> onEpisode;
  final ValueChanged<DramaSeason> onSeason;
  final VoidCallback onDescription;

  @override
  State<TelevisionDetail> createState() => TelevisionDetailState();
}

class TelevisionDetailState extends State<TelevisionDetail> {
  final _focus = FocusNode();
  final _statusFocus = FocusNode(canRequestFocus: false, skipTraversal: true);
  final _seasonKeys = <GlobalKey>[];
  final _rangeKeys = <GlobalKey>[];
  _Row _row = _Row.actions;
  int _action = 0;
  int? _season;
  int _range = 0;
  int _top = 0;
  int? _cell;
  Object? _located;
  Timer? _repeat;
  LogicalKeyboardKey? _held;

  DramaDetails get _details => widget.details;
  List<Episode> get _episodes => _details.episodes;
  bool get _seasoned => _details.seasons.length > 1;
  bool get _ranged => _episodes.length > 40;

  int get _rangeSize {
    final count = _episodes.length;
    if (count <= 40) return math.max(count, 1);
    if (count <= 200) return 20;
    return math.max(100, (count / 1000).ceil() * 100);
  }

  int get _rangeCount => (_episodes.length / _rangeSize).ceil();
  int get _start => _range * _rangeSize;
  int get _end => math.min(_start + _rangeSize, _episodes.length);
  int get _rows => ((_end - _start) / _columns).ceil();
  int get _maxTop => math.max(0, _rows - 2);
  int get _actionCount => _details.drama.description.isEmpty ? 2 : 3;

  int get _seasonIndex =>
      _season ??
      math.max(
        0,
        _details.seasons.indexWhere(
          (season) => season.drama.id == _details.drama.id,
        ),
      );

  List<_Row> get _available => [
    _Row.actions,
    if (_seasoned) _Row.seasons,
    if (_ranged) _Row.ranges,
    if (_episodes.isNotEmpty) _Row.grid,
  ];

  @override
  void dispose() {
    _stopRepeat();
    _focus.dispose();
    _statusFocus.dispose();
    super.dispose();
  }

  void focusPlay() => _move(() {
    _row = _Row.actions;
    _action = 0;
  });

  void focusEpisode(int index) {
    _locate();
    if (index < 0 || index >= _episodes.length) return;
    _move(() {
      _row = _Row.grid;
      _show(index);
      _cell = index;
    });
  }

  void _locate() {
    final key = (_details.drama.id, _episodes.length);
    if (key == _located) return;
    _located = key;
    _cell = null;
    _season = null;
    _show(_details.currentIndex ?? 0);
  }

  void _show(int index) {
    _range = index ~/ _rangeSize;
    _keep(index);
  }

  void _keep(int index) {
    final row = (index - _start) ~/ _columns;
    _top =
        (row < _top
                ? row
                : row > _top + 1
                ? row - 1
                : _top)
            .clamp(0, _maxTop);
  }

  bool _visible(int index) {
    if (index < _start || index >= _end) return false;
    final row = (index - _start) ~/ _columns;
    return row == _top || row == _top + 1;
  }

  void _move(VoidCallback change) {
    setState(change);
    _focus.requestFocus();
    _reveal();
  }

  void _stopRepeat() {
    _repeat?.cancel();
    _repeat = null;
    _held = null;
  }

  KeyEventResult _onKey(KeyEvent event) {
    final key = event.logicalKey;
    if (!_arrows.contains(key) || !_focus.hasPrimaryFocus) {
      return KeyEventResult.ignored;
    }
    if (event is KeyUpEvent) {
      if (key == _held) _stopRepeat();
      return KeyEventResult.ignored;
    }
    if (event is KeyRepeatEvent) {
      return key == _held ? KeyEventResult.handled : KeyEventResult.ignored;
    }
    _stopRepeat();
    if (!_step(key)) return KeyEventResult.ignored;
    _held = key;
    _repeat = Timer(
      const Duration(milliseconds: 400),
      () => _accelerate(key, 0),
    );
    return KeyEventResult.handled;
  }

  void _accelerate(LogicalKeyboardKey key, int count) {
    if (!mounted || !_focus.hasPrimaryFocus || !_step(key)) {
      _stopRepeat();
      return;
    }
    _repeat = Timer(
      Duration(milliseconds: math.max(50, 150 - count * 10)),
      () => _accelerate(key, count + 1),
    );
  }

  bool _step(LogicalKeyboardKey key) {
    if (key == LogicalKeyboardKey.arrowUp ||
        key == LogicalKeyboardKey.arrowDown) {
      final down = key == LogicalKeyboardKey.arrowDown;
      if (_row != _Row.grid || !_gridStep(down ? _columns : -_columns)) {
        final rows = _available;
        final next = rows.indexOf(_row) + (down ? 1 : -1);
        if (next >= rows.length) {
          if (_episodes.isEmpty) {
            _statusFocus.descendants
                .where((node) => node.canRequestFocus && !node.skipTraversal)
                .firstOrNull
                ?.requestFocus();
          }
          return true;
        }
        if (next < 0) return true;
        _enter(rows[next]);
      }
    } else {
      _sideStep(key == LogicalKeyboardKey.arrowRight ? 1 : -1);
    }
    _move(() {});
    return true;
  }

  bool _gridStep(int delta) {
    final cell = _cell!;
    final row = (cell - _start) ~/ _columns;
    if (delta < 0) {
      if (row == 0) return false;
      _cell = cell + delta;
    } else if (row + 1 < _rows) {
      _cell = math.min(cell + delta, _end - 1);
    } else if (_range + 1 < _rangeCount) {
      final column = (cell - _start) % _columns;
      _range++;
      _top = 0;
      _cell = math.min(_start + column, _end - 1);
    }
    _keep(_cell!);
    return true;
  }

  void _sideStep(int delta) {
    switch (_row) {
      case _Row.actions:
        _action = (_action + delta).clamp(0, _actionCount - 1);
      case _Row.seasons:
        _season = (_seasonIndex + delta).clamp(0, _details.seasons.length - 1);
      case _Row.ranges:
        final range = (_range + delta).clamp(0, _rangeCount - 1);
        if (range != _range) {
          _range = range;
          _top = 0;
        }
      case _Row.grid:
        final cell = (_cell! + delta).clamp(0, _episodes.length - 1);
        final range = cell ~/ _rangeSize;
        if (range != _range) {
          _range = range;
          _top = delta > 0 ? 0 : _maxTop;
        }
        _cell = cell;
        _keep(cell);
    }
  }

  void _enter(_Row row) {
    _row = row;
    if (row != _Row.grid) return;
    final current = _details.currentIndex;
    _cell = [_cell, current].firstWhere(
      (cell) => cell != null && _visible(cell),
      orElse: () => _start + _top * _columns,
    );
  }

  void _activate() {
    switch (_row) {
      case _Row.actions:
        switch (_action) {
          case 0:
            widget.onResume?.call();
          case 1:
            _toggleFollow();
          default:
            widget.onDescription();
        }
      case _Row.seasons:
        widget.onSeason(_details.seasons[_seasonIndex]);
      case _Row.ranges:
        _move(() => _enter(_Row.grid));
      case _Row.grid:
        widget.onEpisode(_cell!);
    }
  }

  void _tap(_Row row, int index) {
    _move(() {
      _row = row;
      switch (row) {
        case _Row.actions:
          _action = index;
        case _Row.seasons:
          _season = index;
        case _Row.ranges:
          _range = index;
          _top = 0;
        case _Row.grid:
          _cell = index;
      }
    });
    _activate();
  }

  void _toggleFollow() => unawaited(
    widget.store.toggleFavorite(_details.drama).catchError((Object failure) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('$failure')));
      }
    }),
  );

  void _reveal() => WidgetsBinding.instance.addPostFrameCallback((_) {
    if (!mounted) return;
    for (final key in [
      if (_seasoned) _key(_seasonKeys, _seasonIndex),
      if (_ranged) _key(_rangeKeys, _range),
    ]) {
      final target = key.currentContext;
      final box = target?.findRenderObject();
      if (target == null || box == null) continue;
      unawaited(
        Scrollable.maybeOf(
          target,
          axis: Axis.horizontal,
        )?.position.ensureVisible(box, alignment: .5, duration: _focusDuration),
      );
    }
  });

  GlobalKey _key(List<GlobalKey> keys, int index) {
    while (keys.length <= index) {
      keys.add(GlobalKey());
    }
    return keys[index];
  }

  bool _focused(_Row row) => _focus.hasPrimaryFocus && _row == row;

  @override
  Widget build(BuildContext context) {
    _locate();
    if (!_available.contains(_row)) _row = _Row.actions;
    return PopScope(
      canPop: widget.onResume == null || (_row == _Row.actions && _action == 0),
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) focusPlay();
      },
      child: Actions(
        actions: {
          ActivateIntent: CallbackAction<ActivateIntent>(
            onInvoke: (_) {
              _activate();
              return null;
            },
          ),
        },
        child: Focus(
          focusNode: _focus,
          onFocusChange: (focused) {
            if (!focused) _stopRepeat();
            setState(() {});
          },
          onKeyEvent: (_, event) => _onKey(event),
          child: ColoredBox(
            color: _background,
            child: Stack(
              children: [
                ..._backdrop(),
                Positioned.fill(
                  child: SingleChildScrollView(
                    physics: const NeverScrollableScrollPhysics(),
                    padding: const EdgeInsets.only(top: 36),
                    child: _content(),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  List<Widget> _backdrop() {
    final (sideStops, sideColors) = _eased(_background, const [
      (0, 1),
      (.3, 1),
      (.62, .72),
      (1, .5),
    ]);
    final (bottomStops, bottomColors) = _eased(_background, const [
      (0, 1),
      (.55, 1),
      (1, 0),
    ]);
    return [
      Positioned(
        right: -40,
        top: -40,
        width: 640,
        height: 460,
        child: PosterGlow(drama: _details.drama, background: _background),
      ),
      Positioned.fill(
        child: IgnorePointer(
          child: DecoratedBox(
            decoration: BoxDecoration(
              gradient: LinearGradient(stops: sideStops, colors: sideColors),
            ),
          ),
        ),
      ),
      Positioned(
        left: 0,
        right: 0,
        bottom: 0,
        height: 300,
        child: IgnorePointer(
          child: DecoratedBox(
            decoration: BoxDecoration(
              gradient: LinearGradient(
                begin: Alignment.bottomCenter,
                end: Alignment.topCenter,
                stops: bottomStops,
                colors: bottomColors,
              ),
            ),
          ),
        ),
      ),
    ];
  }

  Widget _content() => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      Padding(
        padding: const EdgeInsets.symmetric(horizontal: _inset),
        child: SizedBox(height: 225, child: _header()),
      ),
      if (_seasoned)
        Padding(padding: const EdgeInsets.only(top: 26), child: _seasonTabs()),
      if (_episodes.isEmpty)
        Padding(
          padding: const EdgeInsets.fromLTRB(_inset, 24, _inset, 0),
          child: SizedBox(
            height: 220,
            child: Theme(
              data: Theme.of(context).copyWith(
                colorScheme: ColorScheme.fromSeed(
                  seedColor: Theme.of(context).colorScheme.primary,
                  brightness: Brightness.dark,
                ),
              ),
              child: Focus(
                focusNode: _statusFocus,
                onKeyEvent: (_, event) {
                  if (event is! KeyDownEvent ||
                      event.logicalKey != LogicalKeyboardKey.arrowUp) {
                    return KeyEventResult.ignored;
                  }
                  _move(() => _row = _available.last);
                  return KeyEventResult.handled;
                },
                child: widget.status,
              ),
            ),
          ),
        )
      else ...[
        if (_ranged)
          Padding(padding: const EdgeInsets.only(top: 12), child: _rangeTabs()),
        Padding(
          padding: const EdgeInsets.fromLTRB(_inset, 12, _inset, 0),
          child: _grid(),
        ),
      ],
    ],
  );

  Widget _header() {
    final drama = _details.drama;
    final count = _episodes.isNotEmpty ? _episodes.length : drama.episodes;
    final year = RegExp(r'^\d{4}').stringMatch(drama.onlineDate);
    final status = [
      if (drama.releaseStatus.isNotEmpty && drama.releaseStatus != 'unknown')
        drama.releaseLabel,
      if (count > 0) '共 $count 集',
    ].join(' · ');
    final meta = [
      if (drama.score.isNotEmpty)
        TextSpan(
          text: '${drama.score} 分',
          style: const TextStyle(
            color: _scoreColor,
            fontWeight: FontWeight.w700,
          ),
        ),
      if (year != null) TextSpan(text: year),
      if (status.isNotEmpty) TextSpan(text: status),
      TextSpan(text: SourceSite.byId(drama.source).name),
    ];
    return Row(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      spacing: 24,
      children: [
        DecoratedBox(
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(10),
            boxShadow: const [
              BoxShadow(
                color: Color(0x73000000),
                blurRadius: 30,
                offset: Offset(0, 10),
              ),
            ],
          ),
          child: SizedBox(
            width: 150,
            child: DramaCover(
              drama: drama,
              radius: 10,
              details: false,
              heroScope: 'drama',
            ),
          ),
        ),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                drama.title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: _text(
                  32,
                  40,
                  Colors.white,
                  FontWeight.w700,
                ).copyWith(letterSpacing: 1),
              ),
              const SizedBox(height: 6),
              Text.rich(
                TextSpan(
                  children: [
                    for (final (index, span) in meta.indexed) ...[
                      if (index > 0)
                        const WidgetSpan(child: SizedBox(width: 10)),
                      span,
                    ],
                  ],
                ),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: _text(14, 20, const Color(0xFFA9A9B6)),
              ),
              if (drama.tags.isNotEmpty) ...[
                const SizedBox(height: 10),
                SizedBox(
                  height: 24,
                  child: Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    clipBehavior: Clip.hardEdge,
                    children: [
                      for (final tag in drama.tags.take(12))
                        DecoratedBox(
                          decoration: BoxDecoration(
                            color: Colors.white.withValues(alpha: .08),
                            borderRadius: BorderRadius.circular(4),
                          ),
                          child: Padding(
                            padding: const EdgeInsets.symmetric(
                              horizontal: 10,
                              vertical: 2,
                            ),
                            child: Text(
                              tag,
                              style: _text(12, 20, const Color(0xFFC9C9D3)),
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
              ],
              if (drama.description.isNotEmpty) ...[
                const SizedBox(height: 12),
                ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 600),
                  child: Text(
                    drama.description,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: _text(14, 22, const Color(0xFFB9B9C4)),
                  ),
                ),
              ],
              const Spacer(),
              _actions(),
            ],
          ),
        ),
      ],
    );
  }

  Widget _actions() {
    final watch = _details.watch;
    final episode = _episodes.isEmpty
        ? null
        : _episodes[_details.resumeIndex].number;
    final play = episode == null
        ? '播放'
        : watch != null
        ? '继续播放 第 $episode 集'
        : '播放 第 $episode 集';
    final followed = widget.store.isFavorite(_details.drama.id);
    final buttons = [
      (Icons.play_arrow_outlined, play, FontWeight.w700, 22.0),
      (
        followed ? Icons.bookmark_rounded : Icons.bookmark_border_rounded,
        followed ? '已追剧' : '追剧',
        FontWeight.w500,
        20.0,
      ),
      (Icons.notes_rounded, '简介', FontWeight.w500, 20.0),
    ].take(_actionCount);
    return Row(
      spacing: 12,
      children: [
        for (final (index, (icon, label, weight, padding)) in buttons.indexed)
          Builder(
            builder: (context) {
              final focused = _focused(_Row.actions) && _action == index;
              final color = focused ? _focusText : const Color(0xFFF2F2F5);
              return _Surface(
                focused: focused,
                scale: 1.06,
                color: Colors.white.withValues(alpha: .12),
                radius: 20,
                height: 40,
                padding: padding,
                onTap: () => _tap(_Row.actions, index),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  spacing: 8,
                  children: [
                    Icon(icon, size: 16, color: color),
                    Text(label, style: _text(15, 20, color, weight)),
                  ],
                ),
              );
            },
          ),
      ],
    );
  }

  Widget _tabStrip({
    required double height,
    required double lead,
    required double spacing,
    required List<Widget> children,
    double trail = _inset,
  }) => ClipRect(
    clipper: const _BleedClip(),
    child: SizedBox(
      height: height,
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        clipBehavior: Clip.none,
        padding: EdgeInsets.only(left: lead, right: trail),
        child: Row(spacing: spacing, children: children),
      ),
    ),
  );

  Widget _seasonTabs() {
    final focusedRow = _focused(_Row.seasons);
    return ShaderMask(
      blendMode: BlendMode.dstIn,
      shaderCallback: (bounds) => const LinearGradient(
        stops: [0, .86, 1],
        colors: [Colors.white, Colors.white, Colors.transparent],
      ).createShader(bounds),
      child: _tabStrip(
        height: 36,
        lead: _inset - 12,
        spacing: 6,
        children: [
          for (final (index, season) in _details.seasons.indexed)
            Builder(
              key: _key(_seasonKeys, index),
              builder: (context) {
                final focused = focusedRow && _seasonIndex == index;
                final selected = season.drama.id == _details.drama.id;
                return _Surface(
                  focused: focused,
                  scale: 1.06,
                  color: Colors.transparent,
                  radius: 17,
                  height: 34,
                  padding: 14,
                  onTap: () => _tap(_Row.seasons, index),
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    spacing: 3,
                    children: [
                      Text(
                        season.label,
                        style: _text(
                          16,
                          20,
                          focused
                              ? _focusText
                              : selected
                              ? Colors.white
                              : Colors.white.withValues(alpha: .6),
                          selected ? FontWeight.w700 : FontWeight.w400,
                        ),
                      ),
                      Container(
                        width: 16,
                        height: 3,
                        decoration: BoxDecoration(
                          color: selected ? _accent : Colors.transparent,
                          borderRadius: BorderRadius.circular(2),
                        ),
                      ),
                    ],
                  ),
                );
              },
            ),
        ],
      ),
    );
  }

  Widget _rangeTabs() {
    final focusedRow = _focused(_Row.ranges);
    final wide = _episodes.length > 200;
    final first = _start + _top * _columns;
    final last = math.min(_start + (_top + 2) * _columns, _end) - 1;
    final hint = _text(12, 16, const Color(0xFF9A9AAA));
    Widget arrow(String glyph, bool active) => Opacity(
      opacity: active ? 1 : .25,
      child: Text(glyph, style: hint.copyWith(fontSize: 10)),
    );
    return Row(
      children: [
        Expanded(
          child: _tabStrip(
            height: 28,
            lead: _inset - 10,
            spacing: 4,
            trail: 12,
            children: [
              for (var range = 0; range < _rangeCount; range++)
                Builder(
                  key: _key(_rangeKeys, range),
                  builder: (context) {
                    final focused = focusedRow && _range == range;
                    final selected = _range == range;
                    final start = range * _rangeSize;
                    final end =
                        math.min(start + _rangeSize, _episodes.length) - 1;
                    return _Surface(
                      focused: focused,
                      scale: 1.06,
                      color: selected
                          ? Colors.white.withValues(alpha: .14)
                          : Colors.transparent,
                      radius: 14,
                      height: 28,
                      padding: wide ? 9 : 12,
                      onTap: () => _tap(_Row.ranges, range),
                      child: Text(
                        '${_episodes[start].number}-${_episodes[end].number}',
                        style: _text(
                          13,
                          16,
                          focused
                              ? _focusText
                              : selected
                              ? Colors.white
                              : Colors.white.withValues(alpha: .6),
                          selected ? FontWeight.w700 : FontWeight.w400,
                        ),
                      ),
                    );
                  },
                ),
            ],
          ),
        ),
        Padding(
          padding: const EdgeInsets.only(right: _inset),
          child: Row(
            spacing: 4,
            children: [
              arrow('▲', _top > 0),
              Text(
                '${_episodes[first].number}-${_episodes[last].number}',
                style: hint,
              ),
              arrow('▼', _top < _maxTop),
            ],
          ),
        ),
      ],
    );
  }

  double _rowOpacity(double position) {
    if (position < 0) return (1 + position).clamp(0, 1);
    if (position <= 1) return 1;
    if (position <= 2) return 1 - .7 * (position - 1);
    return (.3 * (3 - position)).clamp(0, 1);
  }

  Widget _grid() {
    final current = _details.currentIndex;
    final focusedRow = _focused(_Row.grid);
    return SizedBox(
      height: _cellHeight * 3 + _rowGap * 2,
      child: TweenAnimationBuilder<double>(
        key: ValueKey((_details.drama.id, _range)),
        tween: Tween(end: _top.toDouble()),
        duration: _focusDuration,
        curve: Curves.easeOut,
        builder: (context, offset, _) => Stack(
          clipBehavior: Clip.none,
          children: [
            for (
              var row = math.max(0, offset.floor() - 1);
              row < math.min(_rows, offset.ceil() + 4);
              row++
            )
              Positioned(
                left: 0,
                right: 0,
                top: (row - offset) * _rowExtent,
                height: _cellHeight,
                child: Opacity(
                  opacity: _rowOpacity(row - offset),
                  child: Row(
                    spacing: _columnGap,
                    children: [
                      for (var column = 0; column < _columns; column++)
                        Expanded(
                          child: switch (_start + row * _columns + column) {
                            final index when index < _end => _EpisodeTile(
                              episode: _episodes[index],
                              focused: focusedRow && _cell == index,
                              current: index == current,
                              watched: current != null && index < current,
                              onTap: () => _tap(_Row.grid, index),
                            ),
                            _ => const SizedBox.shrink(),
                          },
                        ),
                    ],
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

class _BleedClip extends CustomClipper<Rect> {
  const _BleedClip();

  @override
  Rect getClip(Size size) =>
      Rect.fromLTRB(0, -40, size.width, size.height + 40);

  @override
  bool shouldReclip(_BleedClip oldClipper) => false;
}

class _Surface extends StatelessWidget {
  const _Surface({
    required this.focused,
    required this.scale,
    required this.color,
    required this.radius,
    required this.height,
    required this.onTap,
    required this.child,
    this.padding = 0,
  });
  final bool focused;
  final double scale;
  final Color color;
  final double radius;
  final double height;
  final double padding;
  final VoidCallback onTap;
  final Widget child;

  @override
  Widget build(BuildContext context) => GestureDetector(
    onTap: onTap,
    child: AnimatedScale(
      scale: focused ? scale : 1,
      duration: _focusDuration,
      child: DecoratedBox(
        decoration: BoxDecoration(
          color: focused ? Colors.white : color,
          borderRadius: BorderRadius.circular(radius),
          boxShadow: focused ? _focusShadow : null,
        ),
        child: SizedBox(
          height: height,
          child: Padding(
            padding: EdgeInsets.symmetric(horizontal: padding),
            child: Center(widthFactor: 1, child: child),
          ),
        ),
      ),
    ),
  );
}

class _EpisodeTile extends StatelessWidget {
  const _EpisodeTile({
    required this.episode,
    required this.focused,
    required this.current,
    required this.watched,
    required this.onTap,
  });
  final Episode episode;
  final bool focused;
  final bool current;
  final bool watched;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => _Surface(
    focused: focused,
    scale: 1.1,
    color: Colors.white.withValues(alpha: .07),
    radius: 8,
    height: _cellHeight,
    onTap: onTap,
    child: Stack(
      fit: StackFit.expand,
      children: [
        Center(
          child: Text(
            '${episode.number}',
            style: _text(
              17,
              22,
              focused
                  ? _focusText
                  : current
                  ? Colors.white
                  : watched
                  ? const Color(0xFF85859A)
                  : const Color(0xFFD2D2DC),
              current ? FontWeight.w700 : FontWeight.w500,
            ),
          ),
        ),
        if (current)
          const Positioned(
            top: 6,
            right: 6,
            child: DecoratedBox(
              decoration: BoxDecoration(color: _accent, shape: BoxShape.circle),
              child: SizedBox.square(dimension: 6),
            ),
          ),
        if (episode.vip)
          Positioned(
            top: 4,
            left: 6,
            child: Text(
              'VIP',
              style: _text(9, 12, _scoreColor, FontWeight.w800),
            ),
          ),
      ],
    ),
  );
}
