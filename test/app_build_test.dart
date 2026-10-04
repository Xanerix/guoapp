import 'dart:convert';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/local_profiles.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('edition sources include DSD only in the all-source build', () async {
    SharedPreferences.setMockInitialValues({'source': 'huangdou'});
    final store = testStore(await SharedPreferences.getInstance());
    expect(appSlug, allSourcesEnabled ? 'zhenguojian' : 'hongguojian');
    // 默认只显示红果、韩小圈、鬼片网与青空次元，其余站源需要密码解锁。
    expect(store.sources.length, allSourcesEnabled ? 4 : 1);
    expect(
      SourceSite.values.any((source) => source.id == 'dsd'),
      allSourcesEnabled,
    );
    expect(SourceSite.isAvailable('dsd'), allSourcesEnabled);
    expect(SourceSite.isKnown('dsd'), isTrue);
    expect(SourceSite.byId('dsd').name, '帝果');
    expect(store.allowsSource('dsd'), isFalse);
    expect(store.source, 'hongguo');
    await store.enableSourceGate('666666');
    expect(store.sourcesUnlocked, isTrue);
    expect(store.sources.length, allSourcesEnabled ? 11 : 1);
    expect(store.allowsSource('dsd'), allSourcesEnabled);
    store.dispose();
  });

  test(
    'a restored foreign-source profile keeps its identity and permissions',
    () async {
      SharedPreferences.setMockInitialValues({
        ...await gatePreferences(),
        'profiles': jsonEncode([
          LocalProfile(
            id: 'default',
            name: '管理员',
            admin: true,
            salt: '0' * 32,
            pinHash: '1' * 64,
          ).toJson(),
          const LocalProfile(
            id: 'viewer',
            name: '已有用户',
            sources: ['huangdou'],
            download: false,
          ).toJson(),
        ]),
        'activeProfile': 'viewer',
        'profile.viewer.source': 'huangdou',
      });
      final store = testStore(await SharedPreferences.getInstance());
      await unlockGate(store);
      expect(store.profile.id, 'viewer');
      expect(store.profile.admin, isFalse);
      expect(store.profile.sources, ['huangdou']);
      expect(store.canDownload, isFalse);
      expect(store.source, allSourcesEnabled ? 'huangdou' : '');
      expect(store.allowsSource('hongguo'), isFalse);
      expect(store.allowsSource('huangdou'), allSourcesEnabled);
      store.dispose();
    },
  );

  test('restored DSD profile data follows edition availability', () async {
    SharedPreferences.setMockInitialValues({
      ...await gatePreferences(),
      'profiles': jsonEncode([
        LocalProfile(
          id: 'default',
          name: '管理员',
          admin: true,
          salt: '0' * 32,
          pinHash: '1' * 64,
        ).toJson(),
        const LocalProfile(
          id: 'viewer',
          name: '帝果旧用户',
          sources: ['dsd'],
          download: false,
        ).toJson(),
      ]),
      'activeProfile': 'viewer',
      'profile.viewer.source': 'dsd',
    });
    final store = testStore(await SharedPreferences.getInstance());
    await unlockGate(store);
    expect(store.configurationError, isNull);
    expect(store.profile.sources, ['dsd']);
    expect(
      store.sources.map((source) => source.id),
      allSourcesEnabled ? ['dsd'] : [],
    );
    expect(store.source, allSourcesEnabled ? 'dsd' : '');
    expect(store.allowsSource('dsd'), allSourcesEnabled);
    store.dispose();
  });

  test(
    'background requests reject unavailable sources before native I/O',
    () async {
      final repository = NativeRepository(background: true);
      final denied = [
        ...SourceSite.allValues
            .where((source) => !SourceSite.isAvailable(source.id))
            .map((source) => source.id),
        'unknown',
      ];
      for (final source in denied) {
        final drama = Drama(id: '$source:123', source: source, title: '合成数据');
        final episode = Episode({'id': '1'}, 1);
        for (final request in [
          () => repository.catalog(source),
          () => repository.cached(source),
          () => repository.sourceStatus(source),
          () => repository.startSourceJob(source, 'update'),
          () => repository.cancelSourceJob(source),
          () => repository.detail(drama),
          () => repository.cover(drama),
          () => repository.resolve(drama, episode),
          () => repository.resolveOnline(drama, episode),
          () => repository.enqueueDownloads(DramaDetail(drama, [episode]), [
            episode,
          ]),
          () => repository.localPlayback(drama, episode),
        ]) {
          await expectLater(
            request(),
            throwsA(
              isA<AppFailure>().having(
                (error) => error.message,
                'message',
                '当前版本不包含此站源',
              ),
            ),
          );
        }
      }
    },
  );
}
