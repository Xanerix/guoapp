import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:window_manager/window_manager.dart';

const _key = 'windowOnTop';

final windowOnTop = ValueNotifier(false);

Future<void> restoreWindowOnTop() async {
  final preferences = await SharedPreferences.getInstance();
  windowOnTop.value = preferences.getBool(_key) ?? false;
  await applyWindowOnTop();
}

Future<void> applyWindowOnTop() async {
  if (!windowOnTop.value) return;
  try {
    await windowManager.setAlwaysOnTop(true);
  } catch (_) {}
}

Future<void> setWindowOnTop(bool enabled) async {
  await windowManager.setAlwaysOnTop(enabled);
  final preferences = await SharedPreferences.getInstance();
  await preferences.setBool(_key, enabled);
  windowOnTop.value = enabled;
}
