import 'dart:async';
import 'dart:math';

import 'core_bridge.dart';
import 'follow_state.dart';
import 'local_store.dart';
import 'models.dart';

class FollowUpdater {
  FollowUpdater(
    this.repository,
    this.store, {
    this.startDelay = const Duration(seconds: 30),
    this.interval = const Duration(minutes: 30),
    this.recheck = const Duration(hours: 6),
    this.seasonRecheck = const Duration(hours: 24),
    this.limit = 10,
    DateTime Function()? clock,
  }) : _epoch = store.profileEpoch,
       _clock = clock ?? DateTime.now;

  static const _maxBackoffShift = 3;
  static const _never = 0;

  final AppRepository repository;
  final LocalStore store;
  final Duration startDelay;
  final Duration interval;
  final Duration recheck;
  final Duration seasonRecheck;
  final int limit;
  final DateTime Function() _clock;
  final int _epoch;
  Timer? _timer;
  bool _running = false;
  bool _disposed = false;

  bool get _valid =>
      !_disposed && !store.locked && _epoch == store.profileEpoch;

  void start() {
    if (_timer != null || !repository.supportsSourceManagement || !_valid) {
      return;
    }
    _timer = Timer(startDelay, () {
      _timer = Timer.periodic(interval, (_) => _tick());
      _tick();
    });
  }

  void _tick() => unawaited(run().catchError((Object _) {}));

  bool _needsDetail(Drama drama, FollowCheck? check, DateTime now) {
    if (const {'finished', 'completed'}.contains(drama.releaseStatus)) {
      return false;
    }
    if (check == null || check.checkedAt.millisecondsSinceEpoch == _never) {
      return true;
    }
    return !now.isBefore(
      check.checkedAt.add(
        recheck * (1 << min(check.failures, _maxBackoffShift)),
      ),
    );
  }

  bool _needsSeasons(Drama drama, FollowCheck? check, DateTime now) {
    if (!SourceSite.byId(drama.source).seasons) return false;
    final checkedAt = check?.seasonsCheckedAt;
    if (checkedAt == null) return true;
    return !now.isBefore(
      checkedAt.add(
        seasonRecheck * (1 << min(check!.seasonFailures, _maxBackoffShift)),
      ),
    );
  }

  List<({Drama drama, bool detail, bool seasons})> _due(
    Map<String, FollowCheck> checks,
  ) {
    final now = _clock();
    final never = DateTime.fromMillisecondsSinceEpoch(_never);
    DateTime oldest(Drama drama, bool detail, bool seasons) {
      final check = checks[drama.id];
      final detailAt = detail ? (check?.checkedAt ?? never) : null;
      final seasonAt = seasons ? (check?.seasonsCheckedAt ?? never) : null;
      if (detailAt == null) return seasonAt ?? never;
      if (seasonAt == null) return detailAt;
      return detailAt.isBefore(seasonAt) ? detailAt : seasonAt;
    }

    final items = <({Drama drama, bool detail, bool seasons, DateTime at})>[];
    for (final drama in store.favorites) {
      final check = checks[drama.id];
      final detail = _needsDetail(drama, check, now);
      final seasons = _needsSeasons(drama, check, now);
      if (!detail && !seasons) continue;
      items.add((
        drama: drama,
        detail: detail,
        seasons: seasons,
        at: oldest(drama, detail, seasons),
      ));
    }
    items.sort((a, b) => a.at.compareTo(b.at));
    return [
      for (final item in items.take(limit))
        (drama: item.drama, detail: item.detail, seasons: item.seasons),
    ];
  }

  Future<void> run() async {
    if (_running || !_valid) return;
    _running = true;
    try {
      final checks = store.followChecks;
      final updates = <Drama>[];
      final seasons = <String, List<DramaSeason>>{};
      final never = DateTime.fromMillisecondsSinceEpoch(_never);
      for (final task in _due(checks)) {
        if (!_valid) return;
        final drama = task.drama;
        var check = checks[drama.id] ?? FollowCheck(never);
        if (task.detail) {
          try {
            updates.add(await repository.followCheck(drama));
            check = check.withDetail(_clock());
          } catch (_) {
            check = check.withDetail(_clock(), failures: check.failures + 1);
          }
        }
        if (!_valid) return;
        if (task.seasons) {
          try {
            seasons[drama.id] = await repository.followSeasons(drama);
            check = check.withSeasons(_clock());
          } catch (_) {
            check = check.withSeasons(
              _clock(),
              failures: check.seasonFailures + 1,
            );
          }
        }
        checks[drama.id] = check;
      }
      if (!_valid) return;
      if (updates.isNotEmpty) await store.refreshDramas(updates);
      if (seasons.isNotEmpty) await store.observeFollowSeasonsBatch(seasons);
      await store.saveFollowChecks(checks, epoch: _epoch);
    } finally {
      _running = false;
    }
  }

  void dispose() {
    _disposed = true;
    _timer?.cancel();
  }
}
