import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../theme/motion.dart';

class SwipeSwitch extends StatefulWidget {
  const SwipeSwitch({
    super.key,
    required this.index,
    required this.count,
    required this.onSwitch,
    this.enabled = true,
    this.ready = true,
    this.onEnter,
    required this.child,
  });
  final int index;
  final int count;
  final ValueChanged<int> onSwitch;
  final bool enabled;
  final bool ready;
  final VoidCallback? onEnter;
  final Widget child;

  @override
  State<SwipeSwitch> createState() => _SwipeSwitchState();
}

class _SwipeSwitchState extends State<SwipeSwitch>
    with TickerProviderStateMixin {
  static const _distance = .22;
  static const _velocity = 700.0;
  static const _travel = .18;

  late final _offset = AnimationController.unbounded(vsync: this);
  late final _opacity = AnimationController(vsync: this, value: 1);
  double _drag = 0;
  Widget? _leaving;
  int _run = 0;
  int _direction = 1;
  Timer? _wait;

  @override
  void didUpdateWidget(SwipeSwitch oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.index != widget.index) {
      _leaving ??= oldWidget.child;
      _direction = widget.index > oldWidget.index ? 1 : -1;
      widget.ready ? _proceed() : _hold();
    } else if (_wait != null && widget.ready) {
      _proceed();
    }
  }

  @override
  void dispose() {
    _wait?.cancel();
    _offset.dispose();
    _opacity.dispose();
    super.dispose();
  }

  double get _width => context.size?.width ?? 1;

  bool get _switching => _leaving != null;

  void _hold() {
    _run++;
    _wait?.cancel();
    _wait = Timer(const Duration(milliseconds: 1500), _proceed);
    final duration = Motion.of(context, Motion.medium);
    unawaited(_offset.animateTo(0, duration: duration, curve: Motion.enter));
    unawaited(_opacity.animateTo(.5, duration: duration, curve: Motion.enter));
  }

  void _proceed() {
    _wait?.cancel();
    _wait = null;
    if (mounted) unawaited(_play(_direction));
  }

  Future<void> _play(int direction) async {
    final run = ++_run;
    final travel = _width * _travel * direction;
    final exit = Motion.of(context, Motion.short);
    try {
      await Future.wait([
        _offset.animateTo(-travel, duration: exit, curve: Motion.exit).orCancel,
        _opacity.animateTo(0, duration: exit, curve: Motion.exit).orCancel,
      ]);
    } on TickerCanceled {
      return;
    }
    if (!mounted || run != _run) return;
    widget.onEnter?.call();
    setState(() => _leaving = null);
    _offset.value = travel;
    final enter = Motion.of(context, Motion.medium);
    unawaited(_offset.animateTo(0, duration: enter, curve: Motion.enter));
    unawaited(_opacity.animateTo(1, duration: enter, curve: Motion.enter));
  }

  void _start(DragStartDetails _) {
    if (_switching) return;
    _offset.stop();
    _drag = _offset.value / .4;
  }

  void _update(DragUpdateDetails details) {
    if (_switching) return;
    _drag += details.primaryDelta ?? 0;
    final open = _drag > 0 ? widget.index > 0 : widget.index < widget.count - 1;
    if (!Motion.reduced(context)) _offset.value = _drag * (open ? .4 : .12);
  }

  void _end(DragEndDetails details) {
    if (_switching) return;
    final velocity = details.primaryVelocity ?? 0;
    final next =
        _drag < 0 && (_drag < -_width * _distance || velocity < -_velocity);
    final previous =
        _drag > 0 && (_drag > _width * _distance || velocity > _velocity);
    _drag = 0;
    final target = widget.index + (next ? 1 : (previous ? -1 : 0));
    if (target != widget.index && target >= 0 && target < widget.count) {
      unawaited(HapticFeedback.selectionClick());
      widget.onSwitch(target);
      return;
    }
    _settle();
  }

  void _settle() {
    _drag = 0;
    if (_switching || _offset.value == 0) return;
    unawaited(
      _offset.animateTo(
        0,
        duration: Motion.of(context, Motion.long),
        curve: Motion.enter,
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final enabled = widget.enabled && widget.count > 1;
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onHorizontalDragStart: enabled ? _start : null,
      onHorizontalDragUpdate: enabled ? _update : null,
      onHorizontalDragEnd: enabled ? _end : null,
      onHorizontalDragCancel: enabled ? _settle : null,
      child: AnimatedBuilder(
        animation: Listenable.merge([_offset, _opacity]),
        builder: (context, child) => Opacity(
          opacity: _opacity.value,
          child: Transform.translate(
            offset: Offset(_offset.value, 0),
            child: child,
          ),
        ),
        child: IgnorePointer(
          ignoring: _switching,
          child: _leaving ?? widget.child,
        ),
      ),
    );
  }
}
