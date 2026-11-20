import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'app_device.dart';

const _preferenceKey = 'miniEdgeSnap';

final miniEdgeSnap = ValueNotifier(false);

Future<void> _apply(bool enabled) async {
  if (!Platform.isWindows) return;
  try {
    await AppDevice.channel.invokeMethod<void>('setMiniEdgeSnap', {
      'enabled': enabled,
    });
  } catch (_) {}
}

Future<void> restoreMiniEdgeSnap() async {
  final preferences = await SharedPreferences.getInstance();
  miniEdgeSnap.value = preferences.getBool(_preferenceKey) ?? false;
  await _apply(miniEdgeSnap.value);
}

Future<void> setMiniEdgeSnap(bool enabled) async {
  miniEdgeSnap.value = enabled;
  final preferences = await SharedPreferences.getInstance();
  await preferences.setBool(_preferenceKey, enabled);
  await _apply(enabled);
}
