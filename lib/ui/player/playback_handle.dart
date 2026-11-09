import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';

import '../../core_bridge.dart';
import '../../local_store.dart';
import '../../models.dart';
import '../../playback_session.dart';

Player _player() => Player(
  configuration: const PlayerConfiguration(
    bufferSize: 32 * 1024 * 1024,
    logLevel: MPVLogLevel.error,
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
    final main = _respawnPlayer();
    final standby = preopen ? _player() : null;
    if (standby != null) _videos[standby] = VideoController(standby);
    session = PlaybackSession(
      repository: repository,
      store: store,
      detail: detail,
      player: main,
      standby: standby,
      respawn: _respawnPlayer,
      index: index,
      position: position,
      autoplay: autoplay,
    );
  }

  late final PlaybackSession session;
  final _videos = <Player, VideoController>{};

  /// iOS 回前台需要连同渲染上下文一起重建播放器。
  Player _respawnPlayer() {
    final player = _player();
    _videos[player] = VideoController(player);
    return player;
  }

  VideoController video(Player player) => _videos[player]!;

  void dispose() => session.dispose();
}
