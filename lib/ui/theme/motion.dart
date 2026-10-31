import 'package:flutter/cupertino.dart' show CupertinoPageTransitionsBuilder;
import 'package:flutter/material.dart';

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
    if (Motion.reduced(context)) return child;
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
