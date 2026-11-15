import 'package:flutter/widgets.dart';

import '../models.dart';

enum CoverBlur { off, adult, all }

class CoverBlurScope extends InheritedWidget {
  const CoverBlurScope({
    super.key,
    required this.mode,
    required this.enabled,
    required this.everywhere,
    required this.onToggle,
    required super.child,
  });
  final CoverBlur mode;
  final bool enabled;
  final bool everywhere;
  final VoidCallback onToggle;

  static CoverBlurScope? of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<CoverBlurScope>();

  bool blurs(Drama drama, {required bool explore}) =>
      enabled &&
      (explore || everywhere) &&
      switch (mode) {
        CoverBlur.off => false,
        CoverBlur.adult => SourceSite.isAdult(drama.source),
        CoverBlur.all => true,
      };

  @override
  bool updateShouldNotify(CoverBlurScope oldWidget) =>
      mode != oldWidget.mode ||
      enabled != oldWidget.enabled ||
      everywhere != oldWidget.everywhere;
}
