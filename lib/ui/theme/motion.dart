import 'dart:math' as math;

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

    if (defaultTargetPlatform != TargetPlatform.android || reduced) {
      return transition();
    }
    return _BackGesture(route: route, child: transition());
  }
}

class _SlidePageTransitionsBuilder extends PageTransitionsBuilder {
  const _SlidePageTransitionsBuilder();

  @override
  Duration get transitionDuration => const Duration(milliseconds: 400);

  @override
  Duration get reverseTransitionDuration => const Duration(milliseconds: 350);

  @override
  Widget buildTransitions<T>(
    PageRoute<T> route,
    BuildContext context,
    Animation<double> animation,
    Animation<double> secondaryAnimation,
    Widget child,
  ) {
    if (Motion.reduced(context)) return child;
    final slide = _SlideTransition(
      animation: animation,
      secondary: secondaryAnimation,
      child: child,
    );
    if (defaultTargetPlatform != TargetPlatform.android) return slide;
    return _BackGesture(route: route, child: slide);
  }
}

class _SlideTransition extends StatefulWidget {
  const _SlideTransition({
    required this.animation,
    required this.secondary,
    required this.child,
  });
  final Animation<double> animation;
  final Animation<double> secondary;
  final Widget child;

  @override
  State<_SlideTransition> createState() => _SlideTransitionState();
}

class _SlideTransitionState extends State<_SlideTransition> {
  static const _curve = Curves.fastEaseInToSlowEaseOut;
  static const _parallax = .3;
  static const _radius = 28.0;

  late CurvedAnimation _enter;
  late CurvedAnimation _cover;

  @override
  void initState() {
    super.initState();
    _bind();
  }

  @override
  void didUpdateWidget(_SlideTransition oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.animation != widget.animation ||
        oldWidget.secondary != widget.secondary) {
      _unbind();
      _bind();
    }
  }

  @override
  void dispose() {
    _unbind();
    super.dispose();
  }

  void _bind() {
    _enter = CurvedAnimation(
      parent: widget.animation,
      curve: _curve,
      reverseCurve: _curve.flipped,
    );
    _cover = CurvedAnimation(
      parent: widget.secondary,
      curve: _curve,
      reverseCurve: _curve.flipped,
    );
  }

  void _unbind() {
    _enter.dispose();
    _cover.dispose();
  }

  static double _corner(double t) => (math.min(t, 1 - t) * 8).clamp(0.0, 1.0);

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: Listenable.merge([_enter, _cover]),
    child: widget.child,
    builder: (context, child) {
      final enter = _enter.value;
      final cover = _cover.value;
      final width = MediaQuery.sizeOf(context).width;
      final radius = _radius * math.max(_corner(enter), _corner(cover));
      final shadow = enter < 1 ? (1 - enter) * enter * 4 : 0.0;
      return Transform.translate(
        offset: Offset(width * (1 - enter - _parallax * cover), 0),
        child: DecoratedBox(
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(radius),
            boxShadow: [
              if (shadow > 0)
                BoxShadow(
                  color: Colors.black.withValues(alpha: .16 * shadow),
                  blurRadius: 24,
                ),
            ],
          ),
          child: ClipRRect(
            borderRadius: BorderRadius.circular(radius),
            clipBehavior: radius > 0 ? Clip.antiAlias : Clip.none,
            child: DecoratedBox(
              position: DecorationPosition.foreground,
              decoration: BoxDecoration(
                color: Colors.black.withValues(alpha: .12 * cover),
              ),
              child: child,
            ),
          ),
        ),
      );
    },
  );
}

class _BackGesture extends StatefulWidget {
  const _BackGesture({required this.route, required this.child});
  final PageRoute<dynamic> route;
  final Widget child;

  @override
  State<_BackGesture> createState() => _BackGestureState();
}

class _BackGestureState extends State<_BackGesture>
    with WidgetsBindingObserver, SingleTickerProviderStateMixin {
  static const _held = .9999;

  late final _progress = AnimationController(vsync: this);
  var _edge = SwipeEdge.left;
  var _active = false;
  var _committed = false;

  PageRoute<dynamic> get _route => widget.route;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _route.animation?.removeStatusListener(_opened);
    _progress.dispose();
    super.dispose();
  }

  bool get _canPop {
    final route = _route;
    final navigator = route.navigator;
    return navigator != null &&
        !navigator.userGestureInProgress &&
        route.isCurrent &&
        !route.isFirst &&
        !route.fullscreenDialog &&
        !route.willHandlePopInternally &&
        route.popDisposition != RoutePopDisposition.doNotPop;
  }

  void _opened(AnimationStatus status) {
    if (!status.isCompleted) return;
    _route.animation?.removeStatusListener(_opened);
    if (_active && !_committed) {
      _route.handleUpdateBackGestureProgress(progress: _held);
    }
  }

  @override
  bool handleStartBackGesture(PredictiveBackEvent backEvent) {
    if (backEvent.isButtonEvent) return false;
    if (_active && !_committed) {
      _progress.value = backEvent.progress;
      return true;
    }
    if (!_canPop) return false;
    _route.navigator!.didStartUserGesture();
    final animation = _route.animation!;
    if (animation.isCompleted) {
      _route.handleUpdateBackGestureProgress(progress: _held);
    } else {
      animation.addStatusListener(_opened);
    }
    _active = true;
    _committed = false;
    setState(() => _edge = backEvent.swipeEdge);
    _progress.value = backEvent.progress;
    return true;
  }

  @override
  void handleUpdateBackGestureProgress(PredictiveBackEvent backEvent) {
    if (_active && !_committed) _progress.value = backEvent.progress;
  }

  @override
  void handleCancelBackGesture() {
    if (!_active || _committed) return;
    _progress
        .animateTo(
          0,
          duration: Motion.of(context, Motion.medium),
          curve: Motion.standard,
        )
        .whenCompleteOrCancel(() {
          if (!mounted || !_active || _committed || _progress.value > 0) {
            return;
          }
          _route.animation?.removeStatusListener(_opened);
          _active = false;
          _route.handleCancelBackGesture();
        });
  }

  @override
  void handleCommitBackGesture() {
    if (!_active || _committed) return;
    _route.animation?.removeStatusListener(_opened);
    _committed = true;
    final navigator = _route.navigator!;
    final animation = _route.animation!;
    navigator.pop();
    if (!animation.isAnimating) return navigator.didStopUserGesture();
    late final AnimationStatusListener closed;
    closed = (status) {
      if (status.isAnimating) return;
      animation.removeStatusListener(closed);
      navigator.didStopUserGesture();
    };
    animation.addStatusListener(closed);
  }

  @override
  Widget build(BuildContext context) => _BackGestureTransition(
    progress: _progress,
    edge: _edge,
    child: widget.child,
  );
}

class _BackGestureTransition extends AnimatedWidget {
  const _BackGestureTransition({
    required Animation<double> progress,
    required this.edge,
    required this.child,
  }) : super(listenable: progress);
  final SwipeEdge edge;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final progress = (listenable as Animation<double>).value;
    final width = MediaQuery.sizeOf(context).width;
    final shift = (width / 20 - 8) * progress;
    return Transform.translate(
      offset: Offset(edge == SwipeEdge.left ? shift : -shift, 0),
      child: Transform.scale(
        scale: 1 - 0.1 * progress,
        child: ClipRRect(
          borderRadius: BorderRadius.circular(32 * progress),
          clipBehavior: progress > 0 ? Clip.antiAlias : Clip.none,
          child: child,
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

PageTransitionsTheme pageTransitions({
  required bool television,
  required bool slide,
}) {
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
  return PageTransitionsTheme(
    builders: {
      TargetPlatform.android: slide
          ? const _SlidePageTransitionsBuilder()
          : scale,
      TargetPlatform.iOS: const CupertinoPageTransitionsBuilder(),
      TargetPlatform.macOS: const CupertinoPageTransitionsBuilder(),
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
