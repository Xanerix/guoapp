import 'package:duanju_app/follow_list.dart';
import 'package:duanju_app/follow_state.dart';
import 'package:duanju_app/follow_updater.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';
import 'support/source_fixture.dart';

void main() {
  setUpAll(installSourceFixture);
  test('sources list only those with follows, in site order', () {
    final follows = [drama('a'), drama('b'), drama('c', source: 'huangdou')];
    expect(followedSources(follows).map((site) => site.id), [
      'hongguo',
      if (SourceSite.isAvailable('huangdou')) 'huangdou',
    ]);
    expect(followedSources(const []), isEmpty);
  });

  test('titles match ignoring case, width and punctuation', () {
    final item = drama('a', title: '霸道总裁 · Love');
    expect(matchesFollowTitle(item, ''), isTrue);
    expect(matchesFollowTitle(item, '总裁love'), isTrue);
    expect(matchesFollowTitle(item, 'ＬＯＶＥ'), isTrue);
    expect(matchesFollowTitle(item, '王爷'), isFalse);
  });

  test('titles match pinyin initials', () {
    final item = drama('a', title: '霸道总裁 · Love');
    expect(matchesFollowTitle(item, 'BDZC'), isTrue);
    expect(matchesFollowTitle(item, 'zclove'), isTrue);
    expect(matchesFollowTitle(item, 'wy'), isFalse);
  });

  test('the update check covers every unfinished follow', () async {
    final store = await openStore();
    final repository = FakeRepository();
    final ongoing = drama('a');
    final finished = Drama(
      id: 'hongguo:b',
      source: 'hongguo',
      title: 'b',
      episodes: 80,
      releaseStatus: 'finished',
    );
    for (final item in [ongoing, finished]) {
      await store.toggleFavorite(item);
      repository.details[item.id] = dramaDetail(
        Drama(
          id: item.id,
          source: 'hongguo',
          title: item.title,
          episodes: 90,
          releaseStatus: item.releaseStatus,
        ),
      );
    }

    final updater = FollowUpdater(repository, store);
    addTearDown(updater.dispose);
    await updater.run();
    expect(repository.detailRequests, 1);
    expect(repository.seasonRequests, 2);
    expect(store.following(ongoing.id)?.newEpisodes, 10);
    expect(store.following(finished.id)?.hasUpdates, isFalse);
  });

  test('new released season notifies finished follow after baseline', () async {
    final store = await openStore();
    final repository = FakeRepository();
    var now = DateTime(2026, 11, 1);
    final season1 = Drama(
      id: 'hongguo:s1',
      source: 'hongguo',
      title: '系列剧 第一季',
      episodes: 60,
      releaseStatus: 'finished',
    );
    final season2 = Drama(
      id: 'hongguo:s2',
      source: 'hongguo',
      title: '系列剧 第二季',
      episodes: 0,
      onlineDate: '2099-01-01',
    );
    final season2Released = Drama(
      id: 'hongguo:s2',
      source: 'hongguo',
      title: '系列剧 第二季',
      episodes: 50,
      onlineDate: '2026-11-02',
    );
    final season3Released = Drama(
      id: 'hongguo:s3',
      source: 'hongguo',
      title: '系列剧 第三季',
      episodes: 40,
      onlineDate: '2026-11-05',
    );
    await store.toggleFavorite(season1);
    repository.seasonLists[season1.id] = [
      DramaSeason(number: 1, drama: season1),
      DramaSeason(number: 2, drama: season2),
    ];

    final updater = FollowUpdater(repository, store, clock: () => now);
    addTearDown(updater.dispose);

    await updater.run();
    expect(repository.detailRequests, 0);
    expect(repository.seasonRequests, 1);
    expect(store.following(season1.id)?.seasonsSeeded, isTrue);
    expect(store.following(season1.id)?.hasUpdates, isFalse);

    repository.seasonLists[season1.id] = [
      DramaSeason(number: 1, drama: season1),
      DramaSeason(number: 2, drama: season2Released),
    ];
    now = now.add(const Duration(hours: 12));
    await updater.run();
    expect(repository.seasonRequests, 1);

    now = now.add(const Duration(hours: 12));
    await updater.run();
    expect(repository.seasonRequests, 2);
    expect(store.following(season1.id)?.hasUpdates, isTrue);
    expect(store.following(season1.id)?.updateBadge, '第2季已上线');

    repository.seasonLists[season1.id] = [
      DramaSeason(number: 1, drama: season1),
      DramaSeason(number: 2, drama: season2Released),
      DramaSeason(number: 3, drama: season3Released),
    ];
    now = now.add(const Duration(hours: 24));
    await updater.run();
    expect(store.following(season1.id)?.updateBadge, '更新2季');

    await store.saveWatch(watching(season2Released, episode: 1));
    expect(store.following(season1.id)?.updateBadge, '第3季已上线');

    await store.toggleFavorite(season3Released);
    expect(store.following(season1.id)?.hasUpdates, isFalse);
  });

  test('already released seasons at first check stay silent as baseline', () async {
    final store = await openStore();
    final repository = FakeRepository();
    final season1 = drama('s1', title: '系列剧1');
    final season2 = drama('s2', title: '系列剧2', episodes: 60);
    await store.toggleFavorite(season1);
    repository.seasonLists[season1.id] = [
      DramaSeason(number: 1, drama: season1),
      DramaSeason(number: 2, drama: season2),
    ];

    final updater = FollowUpdater(repository, store);
    addTearDown(updater.dispose);
    await updater.run();

    expect(store.following(season1.id)?.seasonsSeeded, isTrue);
    expect(store.following(season1.id)?.hasUpdates, isFalse);
    expect(store.following(season1.id)?.seriesSeasons[season2.id]?.read, isTrue);
  });

  test('markUpdatesRead clears unread season badges and persists across restart', () async {
    final store = await openStore();
    final season1 = drama('s1', title: '系列剧1');
    final season2 = drama('s2', title: '系列剧2', episodes: 60);
    await store.toggleFavorite(season1);
    await store.observeFollowSeasons(season1.id, const []);
    await store.observeFollowSeasons(season1.id, [
      DramaSeason(number: 1, drama: season1),
      DramaSeason(number: 2, drama: season2),
    ]);
    expect(store.following(season1.id)?.updateBadge, '第2季已上线');

    final restarted = await restartStore(store);
    expect(restarted.following(season1.id)?.updateBadge, '第2季已上线');
    expect(restarted.following(season1.id)?.seasonsSeeded, isTrue);

    await restarted.markUpdatesRead(season1.id);
    expect(restarted.following(season1.id)?.hasUpdates, isFalse);
    expect(
      (await restartStore(restarted)).following(season1.id)?.hasUpdates,
      isFalse,
    );
  });

  test('non-hongguo season notice and 4-element followCheck round-trip', () {
    final state = FollowState.fromJson({
      'knownEpisodes': 10,
      'readEpisodes': 8,
      'seasonsSeeded': true,
      'seriesSeasons': {
        'sorani:2': {
          'id': 'sorani:2',
          'title': '第二季',
          'season': 2,
          'unit': '季',
          'read': false,
        },
      },
    });
    expect(state.updateBadge, '2 集更新 · 第2季已上线');
    expect(FollowState.fromJson(state.toJson()).updateBadge, '2 集更新 · 第2季已上线');

    final check = FollowCheck(
      DateTime.fromMillisecondsSinceEpoch(1000),
      failures: 1,
      seasonsCheckedAt: DateTime.fromMillisecondsSinceEpoch(2000),
      seasonFailures: 2,
    );
    final decoded = FollowCheck.fromJson(check.toJson());
    expect(decoded.checkedAt.millisecondsSinceEpoch, 1000);
    expect(decoded.failures, 1);
    expect(decoded.seasonsCheckedAt?.millisecondsSinceEpoch, 2000);
    expect(decoded.seasonFailures, 2);
  });
}
