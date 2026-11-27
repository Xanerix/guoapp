import 'dart:async';

import 'package:flutter/foundation.dart';

import 'core_bridge.dart';
import 'drama_feed.dart';
import 'local_store.dart';
import 'models.dart';
import 'ranking_models.dart';
import 'source_catalog.dart';

const recommendTab = CatalogCategory('app:recommend', '推荐');

CatalogCategory rankingTab(RankingBoard board) =>
    CatalogCategory('app:rank:${board.id}', board.name);

class ExploreFeed extends ChangeNotifier {
  ExploreFeed(this.repository, this.store) {
    store.addListener(_storeChanged);
    repository.catalogUpdates.addListener(_metadataChanged);
    _open();
  }
  final AppRepository repository;
  final LocalStore store;
  SourceCatalog? _catalog;
  CatalogCategory _tab = CatalogCategory.all;
  DramaFeed? _feed;
  final _feeds = <String, DramaFeed>{};
  final _rankings = <String, Map<String, int>>{};
  bool _disposed = false;

  SourceSite? get source => _catalog?.source;
  DramaFeed? get feed => _feed;
  CatalogCategory get tab => _tab;
  bool get recommending => _tab.id == recommendTab.id;

  List<CatalogCategory> get tabs {
    final catalog = _catalog;
    if (catalog == null) return const [];
    final [all, ...genres] = catalog.categories;
    return [
      if (catalog.recommends) recommendTab,
      all,
      for (final board in catalog.boards) rankingTab(board),
      ...genres,
    ];
  }

  int? rankOf(Drama drama) => rankIn(_tab, drama);
  WatchEntry? get resume => resumeIn(_tab);
  Drama? get featured => featuredIn(_tab);
  List<Drama> get items => itemsIn(_tab);

  DramaFeed feedOf(CatalogCategory tab) => _feeds[tab.id] ?? _create(tab);

  int? rankIn(CatalogCategory tab, Drama drama) => _rankings[tab.id]?[drama.id];

  WatchEntry? resumeIn(CatalogCategory tab) {
    if (!store.exploreBanner) return null;
    final source = this.source;
    if (source == null || tab.id != tabs.first.id) return null;
    return store.history
        .where((entry) => entry.drama.source == source.id)
        .firstOrNull;
  }

  Drama? featuredIn(CatalogCategory tab) =>
      store.exploreBanner && tab.id == recommendTab.id && resumeIn(tab) == null
      ? _feeds[tab.id]?.items.firstOrNull
      : null;

  List<Drama> itemsIn(CatalogCategory tab) {
    final items = _feeds[tab.id]?.items ?? const <Drama>[];
    return featuredIn(tab) == null ? items : items.skip(1).toList();
  }

  void select(String id) {
    if (id == _tab.id) return;
    final tab = tabs.where((tab) => tab.id == id).firstOrNull;
    if (tab != null) _select(tab);
  }

  Future<void> refresh() async {
    final catalog = _catalog;
    if (catalog == null) return;
    if (catalog.categories.length == 1) {
      unawaited(_loadCategories(catalog, force: true));
    }
    await _feed?.reload(force: true);
  }

  void _open() {
    final id = store.source;
    if (id == _catalog?.source.id) return;
    unawaited(repository.cancelCatalog());
    unawaited(repository.cancelRecommendations());
    _dropFeeds();
    if (id.isEmpty) {
      _catalog = null;
      return;
    }
    final catalog = _catalog = SourceCatalog(repository, SourceSite.byId(id));
    _select(catalog.recommends ? recommendTab : CatalogCategory.all);
    unawaited(_loadCategories(catalog));
  }

  Future<void> _loadCategories(
    SourceCatalog catalog, {
    bool force = false,
  }) async {
    try {
      await catalog.loadCategories(force: force);
    } catch (_) {}
    if (_disposed || catalog != _catalog) return;
    _prefetch();
    _notify();
  }

  void _select(CatalogCategory tab) {
    if (recommending) unawaited(repository.cancelRecommendations());
    _tab = tab;
    final kept = _feeds[tab.id];
    final feed = _feed = kept ?? _create(tab);
    if (feed.items.isEmpty && !feed.loading) unawaited(feed.reload());
    _prefetch();
    _notify();
  }

  DramaFeed _create(CatalogCategory tab) {
    final catalog = _catalog!;
    final board = catalog.boards
        .where((board) => rankingTab(board).id == tab.id)
        .firstOrNull;
    final ranks = _rankings[tab.id] = <String, int>{};
    final feed = _feeds[tab.id] = tab.id == recommendTab.id
        ? DramaFeed(
            cumulative: true,
            load: (page, force) =>
                _saved(catalog.recommendations(more: page > 1, force: force)),
          )
        : board != null
        ? DramaFeed(
            load: (page, force) =>
                _saved(_ranking(catalog, board, ranks, page, force)),
          )
        : DramaFeed(
            cached: () => catalog.cached(tab),
            load: (page, force) => _saved(
              catalog.fetch(tab, page: page, lane: _lane(tab), force: force),
            ),
          );
    unawaited(feed.reload());
    feed.addListener(_feedChanged);
    return feed;
  }

  String _lane(CatalogCategory tab) {
    if (tab.id == _tab.id) return '';
    final tabs = this.tabs;
    final index = tabs.indexWhere((item) => item.id == tab.id);
    final current = tabs.indexWhere((item) => item.id == _tab.id);
    return index < current ? 'behind' : 'ahead';
  }

  void _feedChanged() {
    _prefetch();
    _notify();
  }

  void _prefetch() {
    final feed = _feed;
    if (feed == null ||
        feed.loading ||
        feed.loadingMore ||
        feed.items.isEmpty ||
        feed.error.isNotEmpty) {
      return;
    }
    final tabs = this.tabs;
    final index = tabs.indexWhere((tab) => tab.id == _tab.id);
    if (index < 0) return;
    for (final neighbor in [index - 1, index + 1]) {
      if (neighbor < 0 || neighbor >= tabs.length) continue;
      final tab = tabs[neighbor];
      if (_feeds.containsKey(tab.id)) continue;
      _create(tab);
    }
  }

  Future<CatalogPage> _ranking(
    SourceCatalog catalog,
    RankingBoard board,
    Map<String, int> ranks,
    int page,
    bool force,
  ) async {
    final result = await catalog.ranking(board, page: page, force: force);
    for (final item in result.items) {
      ranks[item.drama.id] = item.rank;
    }
    return CatalogPage(
      [for (final item in result.items) item.drama],
      hasMore: result.hasMore,
      warning: result.warning,
      page: result.page,
    );
  }

  Future<CatalogPage> _saved(Future<CatalogPage> request) async {
    final page = await request;
    unawaited(store.refreshDramas(page.items).catchError((Object _) {}));
    return page;
  }

  void _storeChanged() {
    _open();
    _notify();
  }

  void _metadataChanged() {
    final drama = repository.catalogUpdates.latest;
    if (drama == null || drama.source != source?.id) return;
    _catalog?.remember([drama]);
    for (final feed in _feeds.values) {
      feed.update(drama);
    }
    unawaited(store.refreshDrama(drama).catchError((Object _) {}));
  }

  void _dropFeeds() {
    for (final feed in _feeds.values) {
      feed.dispose();
    }
    _feeds.clear();
    _rankings.clear();
    _feed = null;
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    store.removeListener(_storeChanged);
    repository.catalogUpdates.removeListener(_metadataChanged);
    _dropFeeds();
    unawaited(repository.cancelCatalog());
    unawaited(repository.cancelRecommendations());
    super.dispose();
  }
}
