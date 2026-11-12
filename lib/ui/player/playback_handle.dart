import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';

import '../../core_bridge.dart';
import '../../diary_service.dart';
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
      onRetired: _retireVideo,
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

  /// media_kit_video 2.0.1 的 VideoController 没有公开 dispose，
  /// 播放器释放后只需移除映射，避免每次重建都残留一份引用。
  void _retireVideo(Player player) => _videos.remove(player);

  /// Player ↔ VideoController 的映射由本类唯一持有。
  /// 回前台 respawn / retire 时序交错时映射可能短暂缺失：就地补建并留痕，
  /// 不再用 `!` 强解——空指针会把整棵播放页打挂，比一次纹理重建严重得多。
  VideoController video(Player player) => _videos.putIfAbsent(player, () {
    DiaryService.add('[Play] VideoController 映射缺失，已就地补建');
    return VideoController(player);
  });

  void dispose() {
    session.dispose();
    _videos.clear();
  }
}
