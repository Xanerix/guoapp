import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';

import '../theme/motion.dart';

/// Fades [child] in when non-null and fades the last shown child out when it
/// becomes null. Hidden content ignores pointer and focus.
class FadePresence extends StatefulWidget {
  const FadePresence({
    super.key,
    required this.child,
    this.duration = Motion.short,
    this.scaleFrom = 1,
    this.alignment = Alignment.center,
    this.slideFrom = Offset.zero,
  });

  final Widget? child;
  final Duration duration;
  final double scaleFrom;
  final Alignment alignment;
  final Offset slideFrom;

  @override
  State<FadePresence> createState() => _FadePresenceState();
}

class _FadePresenceState extends State<FadePresence>
    with SingleTickerProviderStateMixin {
  late final _controller = AnimationController(vsync: this)
    ..addStatusListener((status) {
      if (status != AnimationStatus.dismissed || widget.child != null) return;
      if (SchedulerBinding.instance.schedulerPhase ==
          SchedulerPhase.persistentCallbacks) {
        _last = null;
      } else if (mounted) {
        setState(() => _last = null);
      }
    });
  late final _curve = CurvedAnimation(
    parent: _controller,
    curve: Motion.enter,
    reverseCurve: Motion.exit,
  );
  Widget? _last;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _sync();
  }

  @override
  void didUpdateWidget(FadePresence oldWidget) {
    super.didUpdateWidget(oldWidget);
    _sync();
  }

  void _sync() {
    final d = Motion.of(context, widget.duration);
    _controller
      ..duration = d
      ..reverseDuration = d;
    if (widget.child != null) {
      _last = widget.child;
      if (_controller.status != AnimationStatus.completed &&
          _controller.status != AnimationStatus.forward) {
        _controller.forward();
      }
    } else if (_controller.status == AnimationStatus.completed ||
        _controller.status == AnimationStatus.forward) {
      _controller.reverse();
    }
  }

  @override
  void dispose() {
    _curve.dispose();
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final last = _last;
    if (last == null) return const SizedBox.shrink();
    final visible = widget.child != null;
    Widget body = FadeTransition(opacity: _curve, child: last);
    if (widget.scaleFrom != 1) {
      body = ScaleTransition(
        scale: Tween(begin: widget.scaleFrom, end: 1.0).animate(_curve),
        alignment: widget.alignment,
        child: body,
      );
    }
    if (widget.slideFrom != Offset.zero) {
      body = SlideTransition(
        position: Tween(
          begin: widget.slideFrom,
          end: Offset.zero,
        ).animate(_curve),
        child: body,
      );
    }
    return IgnorePointer(
      ignoring: !visible,
      child: ExcludeFocus(
        excluding: !visible,
        child: ExcludeSemantics(excluding: !visible, child: body),
      ),
    );
  }
}
