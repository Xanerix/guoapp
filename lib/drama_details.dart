import 'dart:async';

import 'package:flutter/foundation.dart';

import 'core_bridge.dart';
import 'follow_state.dart';
import 'local_store.dart';
import 'models.dart';

const episodeGroupSize = 30;

class DramaDetails extends ChangeNotifier {
  DramaDetails(this.repository, this.store, Drama drama)
    : _drama = repository.catalogUpdates.current(drama),
      _epoch = store.profileEpoch {
    store.addListener(_notify);
    load();
  }
  final AppRepository repository;
  final LocalStore store;
  final int _epoch;
  Drama _drama;
  List<Episode> _episodes = const [];
  List<DramaSeason> _seasons = const [];
  String _warning = '';
  String? _error;
  bool _loading = false;
  bool _seasonsLoading = false;

  /// 切到另一季后需要按新的当前季重新求一次季列表（补齐可能缺失的季）。
  bool _seasonsDirty = false;
  int _group = 0;
  int _generation = 0;
  bool _disposed = false;

  Drama get drama => _drama;
  List<Episode> get episodes => _episodes;

  /// 同站源内的季列表；长度 ≤ 1 时 UI 不展示季切换。
  List<DramaSeason> get seasons => _seasons;
  bool get seasonsLoading => _seasonsLoading;
  String get warning => _warning;
  String? get error => _error;
  bool get loading => _loading;
  bool get allowed =>
      store.profileEpoch == _epoch && store.allowsSource(_drama.source);
  WatchEntry? get watch => allowed ? store.watched(_drama.id) : null;
  int get resumeIndex => resumeEpisodeIndex(_episodes, watch);
  int? get currentIndex {
    final number = watch?.episode;
    final index = _episodes.indexWhere((episode) => episode.number == number);
    return index < 0 ? null : index;
  }

  int get groupCount => (_episodes.length / episodeGroupSize).ceil();
  int get group => _group;
  List<Episode> get groupEpisodes =>
      _episodes.skip(_group * episodeGroupSize).take(episodeGroupSize).toList();

  String groupLabel(int group) {
    final start = group * episodeGroupSize;
    final end = (start + episodeGroupSize).clamp(0, _episodes.length) - 1;
    return '${_episodes[start].number}-${_episodes[end].number}';
  }

  void selectGroup(int group) {
    if (group == _group || group < 0 || group >= groupCount) return;
    _group = group;
    _notify();
  }

  Future<void> load() async {
    if (!allowed) return;
    final generation = ++_generation;
    _loading = true;
    _error = null;
    _notify();
    try {
      final detail = await repository.detail(_drama);
      if (!_current(generation)) return;
      _drama = repository.catalogUpdates.current(_drama).merge(detail.drama);
      _episodes = detail.episodes;
      _warning = detail.warning;
      _group = groupCount == 0 ? 0 : resumeIndex ~/ episodeGroupSize;
      _loading = false;
      _notify();
      repository.catalogUpdates.publish(_drama, retryCover: true);
      unawaited(store.refreshDrama(_drama).catchError((Object _) {}));
      unawaited(_loadSeasons(generation));
      unawaited(_supplement(generation));
    } catch (error) {
      if (!_current(generation)) return;
      _error = error.toString();
      _loading = false;
      _notify();
      repository.catalogUpdates.publish(_drama, retryCover: true);
    }
  }

  /// 季列表只是增强信息：异步、带超时、失败保持单季体验。
  ///
  /// 切季会重新请求一次（[_seasonsDirty]），并把新结果按季号并进已有列表：
  /// 从后面的季进入时可能只拿到部分季，换个季再查一次往往能补齐前面几季。
  Future<void> _loadSeasons(int generation) async {
    if (_seasonsLoading) {
      _seasonsDirty = true;
      return;
    }
    if (_seasons.isNotEmpty && !_seasonsDirty) return;
    _seasonsDirty = false;
    _seasonsLoading = true;
    try {
      final seasons = await repository
          .seasons(_drama)
          .timeout(const Duration(seconds: 20));
      if (!_current(generation)) return;
      if (seasons.length <= 1) return;
      _seasons = _mergeSeasons(_seasons, seasons);
      _notify();
    } catch (_) {
    } finally {
      _seasonsLoading = false;
      if (_seasonsDirty && !_disposed) {
        unawaited(_loadSeasons(_generation));
      }
    }
  }

  /// 按季号合并；已有的季保留原对象，只补新拿到的季。
  List<DramaSeason> _mergeSeasons(
    List<DramaSeason> previous,
    List<DramaSeason> fresh,
  ) {
    if (previous.isEmpty) return fresh;
    final byNumber = {for (final season in previous) season.number: season};
    for (final season in fresh) {
      byNumber.putIfAbsent(season.number, () => season);
    }
    final numbers = byNumber.keys.toList()..sort();
    return [for (final number in numbers) byNumber[number]!];
  }

  /// 切换到另一季：换掉当前 [Drama] 后重新加载剧集，进度按季独立。
  Future<void> selectSeason(DramaSeason season) async {
    if (!allowed || season.drama.id == _drama.id) return;
    _drama = repository.catalogUpdates.current(season.drama);
    _episodes = const [];
    _warning = '';
    _group = 0;
    _error = null;
    _seasonsDirty = true;
    _notify();
    await load();
  }

  Future<void> _supplement(int generation) async {
    try {
      final fresh = await repository.supplementMetadata(_drama);
      if (fresh == null || !_current(generation)) return;
      _drama = _drama.merge(fresh);
      _notify();
      repository.catalogUpdates.publish(_drama);
      await store.refreshDrama(_drama);
    } catch (_) {}
  }

  bool _current(int generation) =>
      !_disposed && generation == _generation && allowed;

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    store.removeListener(_notify);
    super.dispose();
  }
}
