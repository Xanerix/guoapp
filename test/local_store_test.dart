import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/follow_state.dart';
import 'package:duanju_app/local_files.dart';
import 'package:duanju_app/local_profiles.dart';
import 'package:duanju_app/local_snapshot.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/playback_preferences.dart';
import 'package:duanju_app/theme_color.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:path/path.dart' as path;

import 'support/fakes.dart';

class _FailingFiles extends LocalFiles {
  _FailingFiles(super.root);
  String failure = '';
  int writes = 0;

  @override
  Future<void> persist(File file, String content) async {
    writes++;
    switch (failure) {
      case 'throw':
        throw const FileSystemException('写入失败');
      case 'after':
        await super.persist(file, content);
        throw const FileSystemException('写入后确认失败');
    }
    return super.persist(file, content);
  }
}

File _file(LocalStore store, String name) =>
    File(path.join(store.files.root.path, name));

Object? _read(LocalStore store, String name) =>
    jsonDecode(_file(store, name).readAsStringSync());

Future<LocalStore> _open([Map<String, Object> initial = const {}]) =>
    openStore(initial, null, temporaryFiles());

String _snapshot(Map<String, Object> values) => jsonEncode({
  'version': 1,
  'values': {
    'profiles': jsonEncode([
      const LocalProfile(id: 'default', name: '管理员', admin: true).toJson(),
    ]),
    ...values,
  },
});

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final first = drama('1', title: '旧资料');
  final second = drama('2', title: '新资料');

  group('migration', () {
    test('a v1 snapshot and standalone settings move into files', () async {
      final store = await _open({
        LocalSnapshot.storageKey: _snapshot({
          'favorites': jsonEncode([first.toJson()]),
          'history': jsonEncode([watching(first, episode: 3).toJson()]),
          'followStates': jsonEncode({
            first.id: {'knownEpisodes': 90, 'readEpisodes': 80},
          }),
          'playback': jsonEncode(const PlaybackPreferences(speed: 2).toJson()),
          'recentSearches': jsonEncode(['剧名']),
          'source': 'hongguo',
          'displayMode': 'pad',
          'themeMode': 'dark',
          'autoExport': true,
          'hideVip': false,
          'mediaHistory': '{}',
          'catalogView': '{}',
        }),
        'themeColor': 'teal',
        'followSystemColor': false,
        'autoExport': true,
        'checkUpdates': false,
        'sideBarExpanded': false,
        'followChecks.default': jsonEncode({
          first.id: [1000, 2],
        }),
        'seriesCandidates.default': '[]',
        'plugin_secret': 'not-ours',
      });
      expect(store.configurationError, isNull);
      expect(store.favorites.single.id, first.id);
      expect(store.watched(first.id)?.episode, 3);
      expect(store.following(first.id)?.newEpisodes, 10);
      expect(store.followChecks[first.id]?.failures, 2);
      expect(store.playbackPreferences.speed, 2);
      expect(store.recentSearches, ['剧名']);
      expect(store.source, 'hongguo');
      expect(store.displayMode, 'pad');
      expect(store.themeMode, 'dark');
      expect(store.themeColor, ThemeColor.teal);
      expect(store.followSystemColor, isFalse);
      expect(store.autoExport, isTrue);
      expect(store.checkUpdates, isFalse);
      expect(store.sideBarExpanded, isFalse);

      expect(store.preferences.getKeys(), {'sideBarExpanded', 'plugin_secret'});
      final settings = await store.files.read('users/default/settings.json');
      expect(settings, isNot(contains('hideVip')));
      expect(settings, isNot(contains('catalogView')));
      expect(
        _file(store, 'users/default/mediaHistory.json').existsSync(),
        false,
      );

      final restarted = await restartStore(store);
      expect(restarted.favorites.single.id, first.id);
      expect(restarted.watched(first.id)?.episode, 3);
      expect(restarted.themeColor, ThemeColor.teal);
      expect(restarted.autoExport, isTrue);
    });

    test('records saved before the snapshot move into files', () async {
      final store = await _open({
        'favorites': jsonEncode([first.toJson()]),
        'history': jsonEncode([watching(first, episode: 3).toJson()]),
        'themeMode': 'dark',
        'plugin_secret': 'not-ours',
      });
      expect(store.configurationError, isNull);
      expect(store.favorites.single.id, first.id);
      expect(store.watched(first.id)?.episode, 3);
      expect(store.themeMode, 'dark');
      expect(store.preferences.getKeys(), {'plugin_secret'});

      await store.toggleFavorite(second);
      final restarted = await restartStore(store);
      expect(restarted.favorites.map((item) => item.id), [second.id, first.id]);
      expect(restarted.watched(first.id)?.episode, 3);
    });

    test('values written by other versions fall back to defaults', () async {
      final store = await _open({
        LocalSnapshot.storageKey: _snapshot({
          'displayMode': 'standard',
          'themeMode': 'sepia',
          'sourceSwitches': 'not json',
          'sourceGateEnabled': false,
          'sourceGateHash': '',
        }),
        'themeColor': 'unknown',
        'miniWindow': '1,2',
      });
      expect(store.configurationError, isNull);
      expect(store.miniWindow, isNull);
      expect(store.playbackPreferences.autoPictureInPicture, isFalse);
      expect(store.displayMode, 'auto');
      expect(store.themeMode, 'system');
      expect(store.themeColor, ThemeColor.orange);
      expect(store.sourceEnabled('hongguo'), isTrue);
      expect(store.sourceEnabled('huangdou'), isFalse);
    });

    for (final (name, records) in [
      (
        'an unknown snapshot field',
        {
          LocalSnapshot.storageKey: _snapshot({
            'favorites': jsonEncode([first.toJson()]),
            'newerFeature': 'on',
          }),
        },
      ),
      (
        'unreadable profiles',
        {
          'profiles': 'broken-json',
          'favorites': jsonEncode([first.toJson()]),
        },
      ),
      (
        'an invalid profile',
        {
          'profiles': jsonEncode([
            {
              'id': 'default',
              'name': '管理员',
              'admin': true,
              'sources': [],
              'salt': 'bad',
            },
          ]),
          'favorites': jsonEncode([first.toJson()]),
        },
      ),
    ]) {
      test('$name locks access and keeps the raw records', () async {
        final store = await _open(records);
        expect(store.configurationError, isNotNull);
        expect(store.locked, isTrue);
        expect(store.canDownload, isFalse);
        expect(store.favorites, isEmpty);
        expect(store.sources, isEmpty);
        await expectLater(store.switchProfile('default'), throwsStateError);
        await store.toggleFavorite(second);

        for (final entry in records.entries) {
          expect(store.preferences.get(entry.key), entry.value);
        }
        expect(await store.exportRecoveryData(), contains('旧资料'));
        expect(await store.files.exists(), isFalse);
      });
    }
  });

  group('files', () {
    test('a broken record file is set aside and the rest loads', () async {
      final store = await _open();
      await store.toggleFavorite(first);
      await store.saveWatch(watching(first, position: 15));
      for (final name in ['history.json', 'history.json.bak']) {
        _file(store, 'users/default/$name').writeAsStringSync('{broken');
      }

      final restarted = await restartStore(store);
      expect(restarted.configurationError, isNull);
      expect(restarted.history, isEmpty);
      expect(restarted.favorites.single.id, first.id);
      expect(
        _file(restarted, 'users/default/history.json.corrupt').existsSync(),
        isTrue,
      );
    });

    test('a broken file falls back to its previous version', () async {
      final store = await _open();
      await store.saveWatch(watching(first, position: 15));
      await store.saveWatch(watching(first, position: 20));
      _file(store, 'users/default/history.json').writeAsStringSync('{broken');

      final restarted = await restartStore(store);
      expect(restarted.watched(first.id)?.position, 15);
    });

    test('a broken users file locks access', () async {
      final store = await _open();
      await store.toggleFavorite(first);
      for (final name in ['users.json', 'users.json.bak']) {
        _file(store, name).writeAsStringSync('[]');
      }

      final restarted = await restartStore(store);
      expect(restarted.configurationError, isNotNull);
      expect(restarted.favorites, isEmpty);
      expect(await restarted.exportRecoveryData(), contains('旧资料'));

      _file(store, 'users.json').writeAsStringSync(
        jsonEncode({
          'profiles': [
            const LocalProfile(
              id: 'default',
              name: '管理员',
              admin: true,
            ).toJson(),
          ],
        }),
      );
      await restarted.reload();
      expect(restarted.configurationError, isNull);
      expect(restarted.favorites.single.id, first.id);
    });

    test('an interrupted replacement keeps a complete store', () async {
      final store = await _open();
      await store.toggleFavorite(first);
      final root = store.files.root;

      Directory('${root.path}.new').createSync();
      File(
        path.join('${root.path}.new', 'users', 'default', 'history.json'),
      ).createSync(recursive: true);
      expect((await restartStore(store)).favorites.single.id, first.id);
      expect(Directory('${root.path}.new').existsSync(), isFalse);

      root.renameSync('${root.path}.old');
      expect((await restartStore(store)).favorites.single.id, first.id);
      expect(Directory('${root.path}.old').existsSync(), isFalse);
    });

    test('playback progress rewrites only the history file', () async {
      final store = await _open();
      await store.toggleFavorite(first);
      final favorites = _file(store, 'users/default/favorites.json');
      final states = _file(store, 'users/default/followStates.json');
      final before = (favorites.lastModifiedSync(), states.lastModifiedSync());

      await store.saveWatch(watching(first, position: 15));
      await store.saveWatch(watching(first, position: 20));
      expect((favorites.lastModifiedSync(), states.lastModifiedSync()), before);
      expect(store.watched(first.id)?.position, 20);
    });
  });

  group('failed writes', () {
    Future<LocalStore> launch(_FailingFiles files) =>
        openStore({}, null, files);

    for (final failure in ['throw', 'after']) {
      test('a "$failure" failure leaves memory and disk unchanged', () async {
        final files = _FailingFiles(temporaryFiles().root);
        final store = await launch(files);
        await store.toggleFavorite(first);
        await store.saveWatch(watching(first, position: 15));

        for (final change in <Future<void> Function()>[
          () => store.toggleFavorite(second),
          () => store.clearHistory(),
          () => store.setThemeMode('light'),
          () => store.setPlaybackPreferences(
            const PlaybackPreferences(speed: 2, autoAdvance: false),
          ),
        ]) {
          files.failure = failure;
          await expectLater(change(), throwsStateError);
          files.failure = '';
          for (final current in [store, await restartStore(store)]) {
            expect(current.favorites.map((item) => item.id), [first.id]);
            expect(current.watched(first.id)?.position, 15);
            expect(current.themeMode, 'system');
            expect(current.playbackPreferences.speed, 1);
          }
        }

        await store.toggleFavorite(second);
        expect((await restartStore(store)).favorites, hasLength(2));
      });
    }

    test('a failed backup import keeps every old record', () async {
      final files = _FailingFiles(temporaryFiles().root);
      final store = await launch(files);
      await store.toggleFavorite(first);
      final old = await store.exportBackup();
      final backup = jsonDecode(old) as Map<String, dynamic>;
      ((backup['libraries'] as Map)['default'] as Map)
        ..['favorites'] = [second.toJson()]
        ..['followStates'] = {};
      final content = jsonEncode(backup);

      for (final failure in ['throw', 'after']) {
        files.failure = failure;
        await expectLater(store.importBackup(content), throwsStateError);
        files.failure = '';
        expect(await store.exportBackup(), old);
        expect(await (await restartStore(store)).exportBackup(), old);
      }

      await store.importBackup(content);
      expect((await restartStore(store)).favorites.single.id, second.id);
    });
  });

  group('backup', () {
    test('a round trip restores records and settings', () async {
      final store = await _open({'plugin_secret': 'stays-local'});
      await store.toggleFavorite(first);
      await store.saveWatch(watching(first, episode: 2, position: 11));
      await store.setThemeMode('dark');
      await store.setFollowSystemColor(false);
      await store.setThemeColor(ThemeColor.violet);
      await store.setAutoExport(true);
      await store.setShareUsage(false);
      final backup = await store.exportBackup();
      expect(backup, isNot(contains('stays-local')));

      await store.toggleFavorite(first);
      await store.clearHistory();
      await store.setThemeMode('light');
      await store.setFollowSystemColor(true);
      await store.setThemeColor(ThemeColor.blue);
      await store.setAutoExport(false);
      await store.setShareUsage(true);
      await store.importBackup(backup);

      final restarted = await restartStore(store);
      expect(restarted.favorites.single.id, first.id);
      expect(restarted.watched(first.id)?.position, 11);
      expect(restarted.themeMode, 'dark');
      expect(restarted.followSystemColor, isFalse);
      expect(restarted.themeColor, ThemeColor.violet);
      expect(restarted.autoExport, isTrue);
      expect(restarted.shareUsage, isFalse);
      expect(restarted.preferences.getString('plugin_secret'), 'stays-local');
    });

    test('older backups without newer fields still import', () async {
      final store = await _open();
      await store.toggleFavorite(first);
      final backup = jsonDecode(await store.exportBackup()) as Map;
      for (final key in ['themeMode', 'followSystemColor', 'themeColor']) {
        backup.remove(key);
      }
      backup['autoExport'] = true;
      final library = (backup['libraries'] as Map)['default'] as Map;
      for (final key in ['playback', 'downloadPreferences', 'recentSearches']) {
        library.remove(key);
      }
      library
        ..['hideVip'] = true
        ..['mediaHistory'] = {}
        ..['catalogView'] = {}
        ..['seriesCandidates'] = [];

      await store.toggleFavorite(first);
      await store.setThemeMode('dark');
      await store.setThemeColor(ThemeColor.rose);
      await store.importBackup(jsonEncode(backup));
      expect(store.favorites.single.id, first.id);
      expect(store.themeMode, 'dark');
      expect(store.themeColor, ThemeColor.rose);
      expect(store.autoExport, isFalse);
      expect(
        store.playbackPreferences.toJson(),
        const PlaybackPreferences().toJson(),
      );
    });

    test('device link records from older versions are ignored', () async {
      final store = await _open();
      await store.toggleFavorite(first);
      final backup = jsonDecode(await store.exportBackup()) as Map;
      final library = (backup['libraries'] as Map)['default'] as Map;
      expect(library, isNot(contains('followSync')));
      library['followSync'] = [
        {'id': 'broken'},
      ];

      await store.toggleFavorite(first);
      await store.importBackup(jsonEncode(backup));
      expect(store.favorites.single.id, first.id);
      expect(
        jsonEncode(await store.files.dump()),
        isNot(contains('followSync')),
      );
    });

    for (final (name, change) in <(String, void Function(Map))>[
      ('another app', (data) => data['app'] = 'other'),
      ('an unknown theme mode', (data) => data['themeMode'] = 'sepia'),
      ('an unknown theme color', (data) => data['themeColor'] = 'gold'),
      ('a non-boolean setting', (data) => data['checkUpdates'] = 'yes'),
      (
        'a profile id with a path',
        (data) => (data['profiles'] as List).add({
          'id': '../escape',
          'name': '坏数据',
          'admin': false,
          'sources': [],
        }),
      ),
    ]) {
      test('a backup from $name is rejected before anything changes', () async {
        final store = await _open();
        await store.toggleFavorite(first);
        final old = await store.exportBackup();
        final data = jsonDecode(old) as Map;
        change(data);
        await expectLater(
          store.importBackup(jsonEncode(data)),
          throwsA(anything),
        );
        expect(await store.exportBackup(), old);
      });
    }
  });

  group('follows', () {
    test('three-state records from older versions still load', () async {
      final store = await _open({
        LocalSnapshot.storageKey: _snapshot({
          'favorites': jsonEncode([first.toJson(), second.toJson()]),
          'followStates': jsonEncode({
            first.id: {
              'status': 'watched',
              'manuallyWatched': true,
              'knownEpisodes': 90,
              'readEpisodes': 80,
            },
            second.id: {
              'status': 'planned',
              'manuallyWatched': false,
              'knownEpisodes': 80,
              'readEpisodes': 80,
            },
          }),
          'lanRecords': '{}',
          'profile.viewer.lanSettings': '{}',
        }),
      });
      expect(store.configurationError, isNull);
      expect(store.following(first.id)?.newEpisodes, 10);
      expect(store.following(second.id)?.hasUpdates, isFalse);

      await store.markUpdatesRead(first.id);
      final dump = jsonEncode(await store.files.dump());
      expect(dump, isNot(contains('lanRecords')));
      expect(dump, isNot(contains('lanSettings')));
      final states = _read(store, 'users/default/followStates.json') as Map;
      expect(states[first.id], {'knownEpisodes': 90, 'readEpisodes': 90});
      expect(
        (await restartStore(store)).following(first.id)?.hasUpdates,
        isFalse,
      );
    });

    test('new episodes stay marked until read, not until watched', () async {
      final store = await _open();
      await store.toggleFavorite(first);
      expect(store.following(first.id)?.hasUpdates, isFalse);

      await store.refreshDrama(drama('1', title: '旧资料', episodes: 86));
      expect(store.following(first.id)?.newEpisodes, 6);
      await store.saveWatch(watching(first, episode: 86));
      expect(store.following(first.id)?.newEpisodes, 6);

      await store.markUpdatesRead(first.id);
      expect(store.following(first.id)?.hasUpdates, isFalse);
      await store.refreshDrama(drama('1', title: '旧资料', episodes: 80));
      expect(store.following(first.id)?.hasUpdates, isFalse);
    });

    test('follow checks keep only followed dramas', () async {
      final store = await _open();
      await store.toggleFavorite(first);
      await store.saveFollowChecks({
        first.id: FollowCheck(DateTime(2026, 10, 30)),
        second.id: FollowCheck(DateTime(2026, 10, 30), failures: 1),
      }, epoch: store.profileEpoch);
      final restarted = await restartStore(store);
      expect(restarted.followChecks.keys, [first.id]);
    });

    test('unfollowing skips switched-off sources and keeps them', () async {
      final store = await _open();
      final third = drama('3', title: '第三部');
      for (final item in [first, second, third]) {
        await store.toggleFavorite(item);
      }
      await store.saveWatch(watching(first));
      await store.setSourceEnabled('hongguo', false);
      await store.removeFavorites({first.id});
      await store.setSourceEnabled('hongguo', true);
      expect(store.favorites, hasLength(3));

      await store.removeFavorites({first.id, third.id, 'hongguo:missing'});
      expect(store.favorites.map((item) => item.id), [second.id]);
      expect(store.following(first.id), isNull);
      expect(store.watched(first.id), isNotNull);
      expect((await restartStore(store)).favorites.single.id, second.id);
    });
  });

  group('history', () {
    test(
      'removing entries skips switched-off sources and keeps them',
      () async {
        final store = await _open();
        final third = drama('3', title: '第三部');
        for (final item in [first, second, third]) {
          await store.saveWatch(watching(item));
        }
        await store.setSourceEnabled('hongguo', false);
        await store.removeHistory({first.id});
        await store.setSourceEnabled('hongguo', true);
        expect(store.history, hasLength(3));

        await store.removeHistory({first.id, third.id, 'hongguo:missing'});
        expect(store.history.map((entry) => entry.drama.id), [second.id]);
        expect((await restartStore(store)).history.single.drama.id, second.id);
      },
    );
  });
}
