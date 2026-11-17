import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'app_device.dart';
import 'core_bridge.dart';
import 'diary_service.dart';
import 'media_controls.dart';

const _preferenceKey = 'bossKey';
final _keyPattern = RegExp(r'^([A-Z0-9]|F([1-9]|1[0-2]))$');

@immutable
class BossKey {
  const BossKey({
    required this.key,
    this.ctrl = false,
    this.alt = false,
    this.shift = false,
    this.meta = false,
  });

  final String key;
  final bool ctrl;
  final bool alt;
  final bool shift;
  final bool meta;

  static BossKey? parse(String? text) {
    if (text == null) return null;
    final parts = text.split('+');
    final value = BossKey(
      key: parts.last,
      ctrl: parts.contains('ctrl'),
      alt: parts.contains('alt'),
      shift: parts.contains('shift'),
      meta: parts.contains('meta'),
    );
    return value._valid ? value : null;
  }

  static BossKey? fromEvent(KeyEvent event) {
    if (event is! KeyDownEvent) return null;
    final keyboard = HardwareKeyboard.instance;
    final value = BossKey(
      key: event.logicalKey.keyLabel.toUpperCase(),
      ctrl: keyboard.isControlPressed,
      alt: keyboard.isAltPressed,
      shift: keyboard.isShiftPressed,
      meta: keyboard.isMetaPressed,
    );
    return value._valid ? value : null;
  }

  bool get _valid => _keyPattern.hasMatch(key);

  String get encoded => [
    if (ctrl) 'ctrl',
    if (alt) 'alt',
    if (shift) 'shift',
    if (meta) 'meta',
    key,
  ].join('+');

  String get label => Platform.isMacOS
      ? [if (ctrl) '⌃', if (alt) '⌥', if (shift) '⇧', if (meta) '⌘', key].join()
      : [
          if (ctrl) 'Ctrl',
          if (alt) 'Alt',
          if (shift) 'Shift',
          if (meta) 'Win',
          key,
        ].join('+');

  Map<String, Object> get _arguments => {
    'key': key,
    'ctrl': ctrl,
    'alt': alt,
    'shift': shift,
    'meta': meta,
  };
}

final bossKey = ValueNotifier<BossKey?>(null);

Future<bool> _register(BossKey? value) async {
  try {
    return await AppDevice.channel.invokeMethod<bool>(
          'setBossKey',
          value?._arguments,
        ) ??
        false;
  } catch (_) {
    return false;
  }
}

Future<void> _handle(MethodCall call) async {
  if (call.method == 'bossKeyHidden') MediaControls.instance.pause();
}

Future<void> restoreBossKey() async {
  AppDevice.channel.setMethodCallHandler(_handle);
  final preferences = await SharedPreferences.getInstance();
  final value = BossKey.parse(preferences.getString(_preferenceKey));
  bossKey.value = value;
  if (value != null && !await _register(value)) {
    DiaryService.add('[App] 老板键 ${value.label} 注册失败，可能已被其他程序占用');
  }
}

Future<void> suspendBossKey() => _register(null);

Future<void> resumeBossKey() => _register(bossKey.value);

Future<void> setBossKey(BossKey? value) async {
  if (value != null && !await _register(value)) {
    await _register(bossKey.value);
    throw AppFailure('${value.label} 已被其他程序占用');
  }
  if (value == null) await _register(null);
  final preferences = await SharedPreferences.getInstance();
  if (value == null) {
    await preferences.remove(_preferenceKey);
  } else {
    await preferences.setString(_preferenceKey, value.encoded);
  }
  bossKey.value = value;
}
