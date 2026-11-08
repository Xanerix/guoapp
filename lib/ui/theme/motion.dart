import 'package:flutter/cupertino.dart' show CupertinoPageTransitionsBuilder;
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../app_scope.dart';

abstract final class Motion {
  static const short = Duration(milliseconds: 150);
  static const medium = Duration(milliseconds: 250);
  static const long = Duration(milliseconds: 300);

  static const enter = Easing.emphasizedDecelerate;
  static const exit = Easing.emphasizedAccelerate;
  static const standard = Easing.standard;

  static bool reduced(BuildContext context) =>
      MediaQuery.maybeDisableAnimationsOf(context) ?? false;

  static Duration of(BuildContext context, Duration duration) {
    if (reduced(context)) return Duration.zero;
    final scope = context.getInheritedWidgetOfExactType<AppScope>();
    if (scope?.television ?? false) {
      return duration > short ? short : duration;
    }
    return duration;
  }
}

class _FadeScalePageTransitionsBuilder extends PageTransitionsBuilder {
  const _FadeScalePageTransitionsBuilder();

  @override
  Duration get transitionDuration => Motion.long;

  @override
  Duration get reverseTransitionDuration => Motion.medium;

  @override
  Widget buildTransitions<T>(
    PageRoute<T> route,
    BuildContext context,
    Animation<double> animation,
    Animation<double> secondaryAnimation,
    Widget child,
  ) {
    final reduced = Motion.reduced(context);
    Widget transition() {
      if (reduced) return child;
      final enter = CurvedAnimation(
        parent: animation,
        curve: Motion.enter,
        reverseCurve: Motion.exit,
      );
      return FadeTransition(
        opacity: enter,
        child: ScaleTransition(
          scale: Tween(begin: 0.92, end: 1.0).animate(enter),
          child: child,
        ),
      );
    }

    if (defaultTargetPlatform != TargetPlatform.android) return transition();
    return _BackGesture(
      route: route,
      builder: (context, edge, committedAt) =>
          route.popGestureInProgress && !reduced
          ? _BackGestureTransition(
              animation: animation,
              edge: edge,
              committedAt: committedAt,
              child: child,
            )
          : transition(),
    );
  }
}

class _BackGesture extends StatefulWidget {
  const _BackGesture({required this.route, required this.builder});
  final PageRoute<dynamic> route;
  final Widget Function(
    BuildContext context,
    SwipeEdge edge,
    double? committedAt,
  )
  builder;

  @override
  State<_BackGesture> createState() => _BackGestureState();
}

class _BackGestureState extends State<_BackGesture>
    with WidgetsBindingObserver {
  var _edge = SwipeEdge.left;
  double? _committedAt;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  bool handleStartBackGesture(PredictiveBackEvent backEvent) {
    final route = widget.route;
    if (backEvent.isButtonEvent ||
        !route.isCurrent ||
        !route.popGestureEnabled) {
      return false;
    }
    route.handleStartBackGesture(progress: 1 - backEvent.progress);
    setState(() {
      _edge = backEvent.swipeEdge;
      _committedAt = null;
    });
    return true;
  }

  @override
  void handleUpdateBackGestureProgress(PredictiveBackEvent backEvent) => widget
      .route
      .handleUpdateBackGestureProgress(progress: 1 - backEvent.progress);

  @override
  void handleCancelBackGesture() => widget.route.handleCancelBackGesture();

  @override
  void handleCommitBackGesture() {
    setState(() => _committedAt = widget.route.animation?.value);
    widget.route.handleCommitBackGesture();
  }

  @override
  Widget build(BuildContext context) =>
      widget.builder(context, _edge, _committedAt);
}

class _BackGestureTransition extends AnimatedWidget {
  const _BackGestureTransition({
    required Animation<double> animation,
    required this.edge,
    required this.committedAt,
    required this.child,
  }) : super(listenable: animation);
  final SwipeEdge edge;
  final double? committedAt;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final shown = (listenable as Animation<double>).value;
    final progress = 1 - shown;
    final width = MediaQuery.sizeOf(context).width;
    final shift = (width / 20 - 8) * progress;
    return Opacity(
      opacity: switch (committedAt) {
        final from? when from > 0 => (shown / from).clamp(0.0, 1.0),
        _ => 1.0,
      },
      child: Transform.translate(
        offset: Offset(edge == SwipeEdge.left ? shift : -shift, 0),
        child: Transform.scale(
          scale: 1 - 0.1 * progress,
          child: ClipRRect(
            borderRadius: BorderRadius.circular(32 * progress.clamp(0, 1)),
            child: child,
          ),
        ),
      ),
    );
  }
}

class _FadePageTransitionsBuilder extends PageTransitionsBuilder {
  const _FadePageTransitionsBuilder();

  @override
  Duration get transitionDuration => Motion.short;

  @override
  Duration get reverseTransitionDuration => Motion.short;

  @override
  Widget buildTransitions<T>(
    PageRoute<T> route,
    BuildContext context,
    Animation<double> animation,
    Animation<double> secondaryAnimation,
    Widget child,
  ) {
    if (Motion.reduced(context)) return child;
    return FadeTransition(
      opacity: CurvedAnimation(parent: animation, curve: Motion.standard),
      child: child,
    );
  }
}

PageTransitionsTheme pageTransitions({required bool television}) {
  if (television) {
    const fade = _FadePageTransitionsBuilder();
    return const PageTransitionsTheme(
      builders: {
        TargetPlatform.android: fade,
        TargetPlatform.iOS: fade,
        TargetPlatform.macOS: fade,
        TargetPlatform.windows: fade,
        TargetPlatform.linux: fade,
        TargetPlatform.fuchsia: fade,
      },
    );
  }
  const scale = _FadeScalePageTransitionsBuilder();
  return const PageTransitionsTheme(
    builders: {
      TargetPlatform.android: scale,
      TargetPlatform.iOS: CupertinoPageTransitionsBuilder(),
      TargetPlatform.macOS: CupertinoPageTransitionsBuilder(),
      TargetPlatform.windows: scale,
      TargetPlatform.linux: scale,
      TargetPlatform.fuchsia: scale,
    },
  );
}

Route<T> fadeRoute<T>(WidgetBuilder builder, {RouteSettings? settings}) =>
    PageRouteBuilder<T>(
      settings: settings,
      transitionDuration: Motion.medium,
      reverseTransitionDuration: Motion.short,
      pageBuilder: (context, _, _) => builder(context),
      transitionsBuilder: (context, animation, _, child) =>
          Motion.reduced(context)
          ? child
          : FadeTransition(
              opacity: CurvedAnimation(
                parent: animation,
                curve: Motion.standard,
              ),
              child: child,
            ),
    );
