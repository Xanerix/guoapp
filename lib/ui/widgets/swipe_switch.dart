import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../theme/motion.dart';

class SwipeSwitch extends StatefulWidget {
  const SwipeSwitch({
    super.key,
    this.onPrevious,
    this.onNext,
    required this.child,
  });
  final VoidCallback? onPrevious;
  final VoidCallback? onNext;
  final Widget child;

  @override
  State<SwipeSwitch> createState() => _SwipeSwitchState();
}

class _SwipeSwitchState extends State<SwipeSwitch>
    with SingleTickerProviderStateMixin {
  static const _distance = .22;
  static const _velocity = 700.0;
  static const _entry = 56.0;

  late final _offset = AnimationController.unbounded(vsync: this);
  double _drag = 0;

  @override
  void dispose() {
    _offset.dispose();
    super.dispose();
  }

  bool get _reduced => Motion.reduced(context);

  void _start(DragStartDetails _) {
    _offset.stop();
    _drag = _offset.value;
  }

  void _update(DragUpdateDetails details) {
    _drag += details.primaryDelta ?? 0;
    final open = _drag > 0 ? widget.onPrevious != null : widget.onNext != null;
    if (!_reduced) _offset.value = _drag * (open ? .4 : .12);
  }

  void _end(DragEndDetails details) {
    final width = context.size?.width ?? 1;
    final velocity = details.primaryVelocity ?? 0;
    final next =
        _drag < 0 && (_drag < -width * _distance || velocity < -_velocity);
    final previous =
        _drag > 0 && (_drag > width * _distance || velocity > _velocity);
    _drag = 0;
    final action = next
        ? widget.onNext
        : previous
        ? widget.onPrevious
        : null;
    if (action != null) {
      unawaited(HapticFeedback.selectionClick());
      action();
      if (!_reduced) _offset.value = next ? _entry : -_entry;
    }
    _settle();
  }

  void _settle() {
    _drag = 0;
    if (_offset.value == 0) return;
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
    final enabled = widget.onPrevious != null || widget.onNext != null;
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onHorizontalDragStart: enabled ? _start : null,
      onHorizontalDragUpdate: enabled ? _update : null,
      onHorizontalDragEnd: enabled ? _end : null,
      onHorizontalDragCancel: enabled ? _settle : null,
      child: AnimatedBuilder(
        animation: _offset,
        builder: (context, child) =>
            Transform.translate(offset: Offset(_offset.value, 0), child: child),
        child: widget.child,
      ),
    );
  }
}
