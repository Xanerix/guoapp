import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'app_device.dart';
import 'media_controls.dart';

const _preferenceKey = 'miniEdgeSnap';
const _pausePreferenceKey = 'miniEdgePause';

final miniEdgeSnap = ValueNotifier(false);
final miniEdgePause = ValueNotifier(false);
bool _pausedByEdge = false;

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
  miniEdgePause.value = preferences.getBool(_pausePreferenceKey) ?? false;
  await _apply(miniEdgeSnap.value);
}

Future<void> setMiniEdgeSnap(bool enabled) async {
  miniEdgeSnap.value = enabled;
  final preferences = await SharedPreferences.getInstance();
  await preferences.setBool(_preferenceKey, enabled);
  await _apply(enabled);
}

Future<void> setMiniEdgePause(bool enabled) async {
  miniEdgePause.value = enabled;
  final preferences = await SharedPreferences.getInstance();
  await preferences.setBool(_pausePreferenceKey, enabled);
}

void handleMiniEdgeCall(MethodCall call) {
  if (call.method != 'miniEdgeHidden') return;
  final media = MediaControls.instance;
  if (call.arguments == true) {
    if (!miniEdgePause.value || !media.playing) return;
    _pausedByEdge = true;
    media.pause();
  } else if (_pausedByEdge) {
    _pausedByEdge = false;
    media.play();
  }
}
