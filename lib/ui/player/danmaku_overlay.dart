import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/scheduler.dart';

import '../../danmaku_controller.dart';
import '../../danmaku_models.dart';
import '../../danmaku_style.dart';

const danmakuMaxRows = 64;

class DanmakuFlight {
  const DanmakuFlight(this.item, this.text, this.lane, this.width);
  final DanmakuItem item;
  final String text;
  final int lane;
  final double width;
}

List<DanmakuFlight> planDanmaku(
  List<DanmakuItem> items, {
  required double width,
  required int rows,
  required double Function(String) measure,
}) {
  if (!width.isFinite || width <= 0 || rows < 1) return const [];
  final lanes = List<DanmakuFlight?>.filled(
    rows.clamp(1, danmakuMaxRows),
    null,
  );
  final sorted = items.toList()..sort((a, b) => a.timeMs.compareTo(b.timeMs));
  final seen = <String>{};
  final flights = <DanmakuFlight>[];
  for (final item in sorted) {
    if (item.id.isEmpty ||
        item.text.trim().isEmpty ||
        item.timeMs < 0 ||
        !seen.add(item.id)) {
      continue;
    }
    if (flights.reversed
            .takeWhile(
              (flight) => item.timeMs - flight.item.timeMs < danmakuLifetimeMs,
            )
            .length >=
        24) {
      continue;
    }
    final runes = item.text.runes;
    final text =
        String.fromCharCodes(runes.take(100)) + (runes.length > 100 ? '…' : '');
    final size = measure(text).ceilToDouble() + 8;
    final lane = lanes.indexWhere((previous) {
      if (previous == null) return true;
      final gap = item.timeMs - previous.item.timeMs;
      if (gap >= danmakuLifetimeMs) return true;
      return (width + previous.width) * gap / danmakuLifetimeMs -
                  previous.width >=
              24 &&
          width -
                  (width + size) *
                      (danmakuLifetimeMs - gap) /
                      danmakuLifetimeMs >=
              24;
    });
    if (lane < 0) continue;
    final flight = DanmakuFlight(item, text, lane, size);
    lanes[lane] = flight;
    flights.add(flight);
  }
  return flights;
}

TextStyle danmakuTextStyle({
  required double fontSize,
  required DanmakuStyle style,
  bool stroke = false,
}) {
  final weight = style.bold ? FontWeight.w700 : FontWeight.w500;
  final alpha = style.opacity;
  if (stroke) {
    return TextStyle(
      fontSize: fontSize,
      height: 1.2,
      fontWeight: weight,
      foreground: Paint()
        ..style = PaintingStyle.stroke
        ..strokeWidth = style.stroke
        ..strokeJoin = StrokeJoin.round
        ..color = Colors.black.withValues(alpha: alpha),
    );
  }
  return TextStyle(
    fontSize: fontSize,
    height: 1.2,
    fontWeight: weight,
    color: style.color.withValues(alpha: alpha),
    shadows: style.hasShadow
        ? [
            Shadow(
              color: Colors.black.withValues(alpha: .72 * alpha),
              blurRadius: style.shadow,
              offset: Offset(style.shadow * .3, style.shadow * .3),
            ),
          ]
        : null,
  );
}

class DanmakuOverlay extends StatelessWidget {
  const DanmakuOverlay({
    super.key,
    required this.controller,
    required this.aspectRatio,
    this.style = const DanmakuStyle(),
  });
  final DanmakuController controller;
  final double aspectRatio;
  final DanmakuStyle style;

  @override
  Widget build(BuildContext context) => IgnorePointer(
    child: ExcludeSemantics(
      child: LayoutBuilder(
        builder: (context, constraints) {
          if (!constraints.hasBoundedWidth ||
              !constraints.hasBoundedHeight ||
              constraints.maxWidth <= 0 ||
              constraints.maxHeight <= 0) {
            return const SizedBox.shrink();
          }
          final aspect = aspectRatio.isFinite && aspectRatio > 0
              ? aspectRatio
              : 16 / 9;
          final width = math.min(
            constraints.maxWidth,
            constraints.maxHeight * aspect,
          );
          final height = math.min(
            constraints.maxHeight,
            constraints.maxWidth / aspect,
          );
          final fontSize = (width < 600 ? 16.0 : 20.0) * style.scale;
          final scaler = MediaQuery.textScalerOf(context);
          final rowHeight = scaler.scale(fontSize) * 1.5;
          final rows = ((height * style.coverage.fraction - 8) / rowHeight)
              .floor()
              .clamp(0, danmakuMaxRows);
          return Center(
            child: SizedBox(
              width: width,
              height: height,
              child: ClipRect(
                child: _DanmakuLayer(
                  controller: controller,
                  width: width,
                  rows: rows,
                  rowHeight: rowHeight,
                  fontSize: fontSize,
                  style: style,
                  scaler: scaler,
                ),
              ),
            ),
          );
        },
      ),
    ),
  );
}

class _DanmakuLayer extends StatefulWidget {
  const _DanmakuLayer({
    required this.controller,
    required this.width,
    required this.rows,
    required this.rowHeight,
    required this.fontSize,
    required this.style,
    required this.scaler,
  });
  final DanmakuController controller;
  final double width;
  final int rows;
  final double rowHeight;
  final double fontSize;
  final DanmakuStyle style;
  final TextScaler scaler;

  @override
  State<_DanmakuLayer> createState() => _DanmakuLayerState();
}

class _DanmakuLayerState extends State<_DanmakuLayer>
    with SingleTickerProviderStateMixin {
  late final Ticker _ticker = createTicker(_tick);
  final _clock = ValueNotifier<double>(0);
  final _items = <String, Widget>{};
  Object? _layout;
  List<DanmakuFlight> _schedule = const [];
  int _revision = -1;
  int _anchorMs = 0;
  double _rate = 1;
  bool _visible = false;
  int _dataRevision = -1;
  (int, int) _window = (0, 0);

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_changed);
    _sync();
  }

  @override
  void didUpdateWidget(_DanmakuLayer oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.controller != widget.controller) {
      oldWidget.controller.removeListener(_changed);
      widget.controller.addListener(_changed);
      _revision = -1;
    }
    _sync();
  }

  @override
  void dispose() {
    widget.controller.removeListener(_changed);
    _ticker.dispose();
    _clock.dispose();
    super.dispose();
  }

  void _changed() {
    if (_sync()) setState(() {});
  }

  bool _sync() {
    final controller = widget.controller;
    var changed = false;
    if (controller.motionRevision != _revision) {
      _revision = controller.motionRevision;
      _anchorMs = controller.positionMs;
      _rate = controller.rate;
      _ticker.stop();
      _clock.value = _anchorMs.toDouble();
      changed = _moveWindow();
    }
    final run = controller.moving && widget.rows > 0;
    if (run && !_ticker.isActive) _ticker.start();
    if (!run && _ticker.isActive) _ticker.stop();
    if (_visible != controller.visible ||
        _dataRevision != controller.dataRevision) {
      _visible = controller.visible;
      _dataRevision = controller.dataRevision;
      changed = true;
    }
    return changed;
  }

  void _tick(Duration elapsed) {
    _clock.value = _anchorMs + elapsed.inMicroseconds / 1000 * _rate;
    if (_moveWindow()) setState(() {});
  }

  (int, int) _windowAt(double now) {
    var first = 0;
    while (first < _schedule.length &&
        now >= _schedule[first].item.timeMs + danmakuLifetimeMs) {
      first++;
    }
    var last = first;
    while (last < _schedule.length && _schedule[last].item.timeMs <= now) {
      last++;
    }
    return (first, last);
  }

  bool _moveWindow() {
    final window = _windowAt(_clock.value);
    if (window == _window) return false;
    _window = window;
    return true;
  }

  @override
  Widget build(BuildContext context) {
    final controller = widget.controller;
    if (!controller.visible || widget.rows == 0) {
      return const SizedBox.shrink();
    }
    final style = widget.style;
    final fill = danmakuTextStyle(fontSize: widget.fontSize, style: style);
    final layout = (
      controller,
      controller.dataRevision,
      widget.width,
      widget.rows,
      widget.rowHeight,
      widget.fontSize,
      widget.scaler,
      style,
    );
    if (_layout != layout) {
      _layout = layout;
      _items.clear();
      _schedule = planDanmaku(
        controller.items,
        width: widget.width,
        rows: widget.rows,
        measure: (text) {
          final painter = TextPainter(
            text: TextSpan(text: text, style: fill),
            textDirection: TextDirection.ltr,
            textScaler: widget.scaler,
            maxLines: 1,
          )..layout();
          final measured = painter.width;
          painter.dispose();
          return measured;
        },
      );
      _window = _windowAt(_clock.value);
    }
    final outline = style.hasStroke
        ? danmakuTextStyle(
            fontSize: widget.fontSize,
            style: style,
            stroke: true,
          )
        : null;
    final (first, last) = _window;
    final shown = _schedule.sublist(first, last);
    _items.removeWhere((id, _) => !shown.any((flight) => flight.item.id == id));
    return RepaintBoundary(
      child: Stack(
        clipBehavior: Clip.none,
        children: [
          for (final flight in shown)
            _items[flight.item.id] ??= Positioned(
              key: ValueKey('danmaku-item-${flight.item.id}'),
              left: 0,
              top: 8 + flight.lane * widget.rowHeight,
              width: flight.width,
              height: widget.rowHeight,
              child: _DanmakuSlide(
                clock: _clock,
                startMs: flight.item.timeMs,
                travel: widget.width + flight.width,
                origin: widget.width,
                child: RepaintBoundary(
                  child: _DanmakuText(
                    text: flight.text,
                    style: fill,
                    outline: outline,
                    scaler: widget.scaler,
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class _DanmakuSlide extends SingleChildRenderObjectWidget {
  const _DanmakuSlide({
    required this.clock,
    required this.startMs,
    required this.travel,
    required this.origin,
    required super.child,
  });
  final ValueListenable<double> clock;
  final int startMs;
  final double travel;
  final double origin;

  @override
  _RenderDanmakuSlide createRenderObject(BuildContext context) =>
      _RenderDanmakuSlide(clock, startMs, travel, origin);

  @override
  void updateRenderObject(
    BuildContext context,
    _RenderDanmakuSlide renderObject,
  ) {
    renderObject
      ..clock = clock
      ..startMs = startMs
      ..travel = travel
      ..origin = origin;
  }
}

class _RenderDanmakuSlide extends RenderProxyBox {
  _RenderDanmakuSlide(this._clock, this._startMs, this._travel, this._origin);

  ValueListenable<double> _clock;
  set clock(ValueListenable<double> value) {
    if (identical(value, _clock)) return;
    if (attached) _clock.removeListener(markNeedsPaint);
    _clock = value;
    if (attached) _clock.addListener(markNeedsPaint);
    markNeedsPaint();
  }

  int _startMs;
  set startMs(int value) {
    if (value == _startMs) return;
    _startMs = value;
    markNeedsPaint();
  }

  double _travel;
  set travel(double value) {
    if (value == _travel) return;
    _travel = value;
    markNeedsPaint();
  }

  double _origin;
  set origin(double value) {
    if (value == _origin) return;
    _origin = value;
    markNeedsPaint();
  }

  @override
  void attach(PipelineOwner owner) {
    super.attach(owner);
    _clock.addListener(markNeedsPaint);
  }

  @override
  void detach() {
    _clock.removeListener(markNeedsPaint);
    super.detach();
  }

  @override
  void paint(PaintingContext context, Offset offset) {
    final child = this.child;
    if (child == null) return;
    final progress = ((_clock.value - _startMs) / danmakuLifetimeMs).clamp(
      0.0,
      1.0,
    );
    context.paintChild(child, offset + Offset(_origin - _travel * progress, 0));
  }
}

class _DanmakuText extends StatelessWidget {
  const _DanmakuText({
    required this.text,
    required this.style,
    required this.outline,
    required this.scaler,
  });
  final String text;
  final TextStyle style;
  final TextStyle? outline;
  final TextScaler scaler;

  @override
  Widget build(BuildContext context) {
    final label = Text(
      text,
      style: style,
      textScaler: scaler,
      textDirection: TextDirection.ltr,
      maxLines: 1,
      softWrap: false,
    );
    final outline = this.outline;
    if (outline == null) return label;
    return Stack(
      clipBehavior: Clip.none,
      children: [
        Text(
          text,
          style: outline,
          textScaler: scaler,
          textDirection: TextDirection.ltr,
          maxLines: 1,
          softWrap: false,
        ),
        label,
      ],
    );
  }
}
