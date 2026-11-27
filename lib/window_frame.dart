import 'dart:async';

import 'package:shared_preferences/shared_preferences.dart';
import 'package:window_manager/window_manager.dart';

import 'app_device.dart';
import 'picture_in_picture.dart';

const _key = 'mainWindow';

Future<void> rememberWindowFrame({bool rememberMaximized = false}) async {
  final preferences = await SharedPreferences.getInstance();
  final values = preferences
      .getString(_key)
      ?.split(',')
      .map(int.tryParse)
      .toList();
  if (values != null &&
      (values.length == 4 || (rememberMaximized && values.length == 5)) &&
      !values.contains(null)) {
    try {
      await AppDevice.channel.invokeMethod<void>('setWindowFrame', {
        'left': values[0],
        'top': values[1],
        'width': values[2],
        'height': values[3],
        if (rememberMaximized)
          'maximized': values.length == 5 && values[4] == 1,
      });
    } catch (_) {}
  }
  windowManager.addListener(_WindowFrameKeeper(preferences, rememberMaximized));
  if (rememberMaximized) await windowManager.setPreventClose(true);
}

class _WindowFrameKeeper with WindowListener {
  _WindowFrameKeeper(this.preferences, this.rememberMaximized);
  final SharedPreferences preferences;
  final bool rememberMaximized;
  Future<void> _pending = Future.value();
  bool _closing = false;

  Future<void> _queueSave() {
    if (!rememberMaximized) return _save();
    return _pending = _pending.then((_) => _save());
  }

  Future<void> _save() async {
    if (!rememberMaximized && PictureInPicture.instance.window) return;
    try {
      final frame = await AppDevice.channel.invokeMapMethod<String, num>(
        'windowFrame',
      );
      if (frame case {
        'left': final left,
        'top': final top,
        'width': final width,
        'height': final height,
      }) {
        await preferences.setString(
          _key,
          [
            left,
            top,
            width,
            height,
            if (rememberMaximized) frame['maximized'] ?? 0,
          ].map((value) => value.round()).join(','),
        );
      }
    } catch (_) {}
  }

  @override
  void onWindowMoved() => unawaited(_queueSave());

  @override
  void onWindowResized() => unawaited(_queueSave());

  @override
  void onWindowUnmaximize() => unawaited(_queueSave());

  @override
  void onWindowMaximize() {
    if (rememberMaximized) unawaited(_queueSave());
  }

  @override
  void onWindowMinimize() {
    if (rememberMaximized) unawaited(_queueSave());
  }

  @override
  void onWindowClose() async {
    if (!rememberMaximized || _closing) return;
    _closing = true;
    await _queueSave();
    windowManager.removeListener(this);
    await windowManager.setPreventClose(false);
    await windowManager.close();
  }
}
