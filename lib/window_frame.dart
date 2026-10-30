import 'dart:async';

import 'package:shared_preferences/shared_preferences.dart';
import 'package:window_manager/window_manager.dart';

import 'app_device.dart';
import 'picture_in_picture.dart';

const _key = 'mainWindow';

Future<void> rememberWindowFrame() async {
  final preferences = await SharedPreferences.getInstance();
  final values = preferences
      .getString(_key)
      ?.split(',')
      .map(int.tryParse)
      .toList();
  if (values != null && values.length == 4 && !values.contains(null)) {
    try {
      await AppDevice.channel.invokeMethod<void>('setWindowFrame', {
        'left': values[0],
        'top': values[1],
        'width': values[2],
        'height': values[3],
      });
    } catch (_) {}
  }
  windowManager.addListener(_WindowFrameKeeper(preferences));
}

class _WindowFrameKeeper with WindowListener {
  _WindowFrameKeeper(this.preferences);
  final SharedPreferences preferences;

  Future<void> _save() async {
    if (PictureInPicture.instance.window) return;
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
          [left, top, width, height].map((value) => value.round()).join(','),
        );
      }
    } catch (_) {}
  }

  @override
  void onWindowMoved() => unawaited(_save());

  @override
  void onWindowResized() => unawaited(_save());

  @override
  void onWindowUnmaximize() => unawaited(_save());
}
