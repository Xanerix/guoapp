import 'package:flutter/material.dart';

import '../../theme/motion.dart';

Widget _switcherLayout(
  Widget? current,
  List<Widget> previous,
  AlignmentGeometry alignment,
) => Stack(
  alignment: alignment,
  children: [
    for (final child in previous) _presence(child, leaving: true),
    if (current != null) _presence(current, leaving: false),
  ],
);

Widget _presence(Widget child, {required bool leaving}) => KeyedSubtree(
  key: child.key,
  child: HeroMode(
    enabled: !leaving,
    child: ExcludeFocus(
      excluding: leaving,
      child: IgnorePointer(
        ignoring: leaving,
        child: ExcludeSemantics(excluding: leaving, child: child),
      ),
    ),
  ),
);

/// Cross-fades between children keyed by identity. Outgoing children are
/// excluded from focus, pointer input and semantics.
class FadeSwitcher extends StatelessWidget {
  const FadeSwitcher({
    super.key,
    required this.child,
    this.duration = Motion.medium,
    this.scale = false,
    this.alignment = Alignment.center,
  });

  final Widget child;
  final Duration duration;
  final bool scale;
  final AlignmentGeometry alignment;

  @override
  Widget build(BuildContext context) => AnimatedSwitcher(
    duration: Motion.of(context, duration),
    switchInCurve: Motion.enter,
    switchOutCurve: Motion.exit,
    layoutBuilder: (current, previous) =>
        _switcherLayout(current, previous, alignment),
    transitionBuilder: (child, animation) {
      final fade = FadeTransition(opacity: animation, child: child);
      if (!scale) return fade;
      return ScaleTransition(
        scale: Tween(begin: 0.96, end: 1.0).animate(animation),
        child: fade,
      );
    },
    child: child,
  );
}

/// Swaps an icon or small widget with a scale and fade. [child] must carry a
/// key that changes with its state.
class ToggleIconSwitcher extends StatelessWidget {
  const ToggleIconSwitcher({
    super.key,
    required this.child,
    this.duration = Motion.short,
  });

  final Widget child;
  final Duration duration;

  @override
  Widget build(BuildContext context) => AnimatedSwitcher(
    duration: Motion.of(context, duration),
    switchInCurve: Motion.enter,
    switchOutCurve: Motion.exit,
    layoutBuilder: (current, previous) =>
        _switcherLayout(current, previous, Alignment.center),
    transitionBuilder: (child, animation) => ScaleTransition(
      scale: Tween(begin: 0.7, end: 1.0).animate(animation),
      child: FadeTransition(opacity: animation, child: child),
    ),
    child: child,
  );
}

/// Image frame builder that fades in images decoded asynchronously and shows
/// cached images at once.
Widget fadeInFrameBuilder(
  BuildContext context,
  Widget child,
  int? frame,
  bool wasSynchronouslyLoaded,
) {
  if (wasSynchronouslyLoaded) return child;
  return AnimatedOpacity(
    opacity: frame == null ? 0 : 1,
    duration: Motion.of(context, Motion.medium),
    curve: Motion.standard,
    child: child,
  );
}

/// Plays a one-time fade and slight rise when first built. Items are
/// remembered by [id] in [played] so recycled or rebuilt items stay still.
class EnterOnce extends StatefulWidget {
  const EnterOnce({
    super.key,
    required this.id,
    required this.played,
    required this.child,
    this.index = 0,
  });

  static const maxStagger = 8;

  final Object id;
  final Set<Object> played;
  final int index;
  final Widget child;

  @override
  State<EnterOnce> createState() => _EnterOnceState();
}

class _EnterOnceState extends State<EnterOnce>
    with SingleTickerProviderStateMixin {
  AnimationController? _controller;
  Animation<double>? _curve;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_controller != null || widget.played.contains(widget.id)) return;
    widget.played.add(widget.id);
    final duration = Motion.of(context, Motion.medium);
    if (duration == Duration.zero) return;
    final step = widget.index.clamp(0, EnterOnce.maxStagger);
    final delay = Duration(milliseconds: 30 * step);
    final total = duration + delay;
    final controller = AnimationController(vsync: this, duration: total);
    _controller = controller;
    _curve = CurvedAnimation(
      parent: controller,
      curve: Interval(
        delay.inMicroseconds / total.inMicroseconds,
        1,
        curve: Motion.enter,
      ),
    );
    controller.forward();
  }

  @override
  void dispose() {
    _controller?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final curve = _curve;
    if (curve == null) return widget.child;
    return FadeTransition(
      opacity: curve,
      child: SlideTransition(
        position: Tween(
          begin: const Offset(0, 0.04),
          end: Offset.zero,
        ).animate(curve),
        child: widget.child,
      ),
    );
  }
}

/// Hero tag for a drama cover, scoped by section to avoid duplicates.
Object coverHeroTag(Object dramaId, String scope) => ('cover', scope, dramaId);
