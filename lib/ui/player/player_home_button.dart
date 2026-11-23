import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import '../app_scope.dart';

class PlayerHomeButton extends StatelessWidget {
  const PlayerHomeButton({super.key, this.size = 48});
  final double size;

  static bool available(BuildContext context) =>
      defaultTargetPlatform == TargetPlatform.android &&
      AppScope.formFactorOf(context) == FormFactor.phone;

  @override
  Widget build(BuildContext context) => IconButton(
    onPressed: () => Navigator.of(
      context,
      rootNavigator: true,
    ).popUntil((route) => route.isFirst),
    icon: const Icon(Icons.home_rounded),
    color: Colors.white,
    tooltip: '返回主页',
    style: IconButton.styleFrom(
      fixedSize: Size.square(size),
      padding: EdgeInsets.zero,
    ),
  );
}
