import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';

import '../../core_bridge.dart';
import '../../local_store.dart';
import '../../models.dart';
import '../../playback_preferences.dart';
import '../../playback_session.dart';

Player _player() => Player(
  configuration: PlayerConfiguration(
    bufferSize: 32 * 1024 * 1024,
    logLevel: Platform.isIOS && !kReleaseMode
        ? MPVLogLevel.v
        : MPVLogLevel.error,
  ),
);

class PlaybackHandle {
  PlaybackHandle({
    required AppRepository repository,
    required LocalStore store,
    required DramaDetail detail,
    required int index,
    double position = 0,
    bool autoplay = true,
    bool preopen = true,
  }) {
    final decoder = store.playbackPreferences.televisionDecoder;
    final main = _player();
    final standby = preopen ? _player() : null;
    final configuration =
        Platform.isAndroid && decoder == TelevisionDecoder.hardwareDirect
        ? const VideoControllerConfiguration(
            vo: 'mediacodec_embed',
            hwdec: 'mediacodec',
          )
        : VideoControllerConfiguration(
            enableHardwareAcceleration:
                !Platform.isIOS ||
                decoder == TelevisionDecoder.optimized ||
                decoder == TelevisionDecoder.hardwareDirect,
          );
    _videos[main] = VideoController(main, configuration: configuration);
    if (standby != null) {
      _videos[standby] = VideoController(standby, configuration: configuration);
    }
    session = PlaybackSession(
      repository: repository,
      store: store,
      detail: detail,
      player: main,
      standby: standby,
      index: index,
      position: position,
      autoplay: autoplay,
    );
  }

  late final PlaybackSession session;
  final _videos = <Player, VideoController>{};

  VideoController video(Player player) => _videos[player]!;

  void dispose() => session.dispose();
}
