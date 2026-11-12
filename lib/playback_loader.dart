import 'core_bridge.dart';
import 'models.dart';

class PlaybackLoader {
  PlaybackLoader(this.repository);

  final AppRepository repository;
  int _generation = 0;
  bool _closed = false;

  Future<PlaybackPlan?> load(
    Drama drama,
    Episode episode, {
    int quality = 0,
    bool localOnly = false,
    bool online = false,
    bool force = false,
    bool Function()? isCurrent,
  }) => _load(() async {
    if (online) {
      return repository.resolveOnline(drama, episode, quality: quality);
    }
    if (localOnly) {
      final plan = await repository.localPlayback(drama, episode);
      if (plan == null || !plan.local) {
        throw AppFailure('本地视频不可用，请重新下载或选择在线播放。', code: 'local_media');
      }
      return plan;
    }
    return repository.resolve(
      drama,
      episode,
      quality: quality,
      force: force,
    );
  }, isCurrent: isCurrent);

  Future<PlaybackPlan?> fallback(
    PlaybackPlan current, {
    bool Function()? isCurrent,
  }) => _load(() => repository.fallback(current), isCurrent: isCurrent);

  Future<PlaybackPlan?> use(
    PlaybackPlan prepared, {
    bool Function()? isCurrent,
  }) => _load(() async => prepared, isCurrent: isCurrent);

  /// 会话票据作废时调用：让仍在后台跑的解析任务在返回时自行释放 session，
  /// 而不是把过期结果写回新的播放状态。
  void invalidate() {
    _generation++;
  }

  Future<PlaybackPlan?> _load(
    Future<PlaybackPlan> Function() resolve, {
    bool Function()? isCurrent,
  }) async {
    if (_closed) {
      return null;
    }
    final generation = ++_generation;
    bool valid() =>
        !_closed && generation == _generation && (isCurrent?.call() ?? true);
    try {
      await repository.cancelPlayback();
      if (!valid()) {
        return null;
      }
      final plan = await resolve();
      if (!valid()) {
        // 旧任务晚返回：丢弃结果并释放它创建的原生会话，绝不交给上层。
        await repository.release(plan.session);
        return null;
      }
      return plan;
    } catch (_) {
      if (!valid()) {
        return null;
      }
      rethrow;
    }
  }

  Future<void> close() async {
    _closed = true;
    _generation++;
    await repository.cancelPlayback();
  }
}
