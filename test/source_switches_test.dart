import 'dart:convert';

import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/local_profiles.dart';
import 'package:duanju_app/local_snapshot.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  Future<LocalStore> create([Map<String, Object> initial = const {}]) async {
    SharedPreferences.setMockInitialValues(Map.of(initial));
    final store = testStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    return store;
  }

  test('restricted sources start switched off', () async {
    final store = await create();
    expect(
      store.sources.map((site) => site.id),
      allSourcesEnabled
          ? ['hongguo', 'hanxiaoquan', 'guipian', 'sorani']
          : ['hongguo'],
    );
    expect(store.permittedSources.length, SourceSite.values.length);
    expect(store.sourceEnabled('hongguo'), isTrue);
    expect(store.sourceEnabled('huangdou'), isFalse);
    expect(store.allowsSource('huangdou'), isFalse);
  });

  test('switches persist across restarts', () async {
    final store = await create();
    await store.setSourceEnabled('hongguo', false);
    expect(store.allowsSource('hongguo'), isFalse);
    if (allSourcesEnabled) {
      await store.setSourceEnabled('huangdou', true);
      expect(store.allowsSource('huangdou'), isTrue);
    }
    final restarted = testStore(store.preferences);
    addTearDown(restarted.dispose);
    expect(restarted.sourceEnabled('hongguo'), isFalse);
    expect(restarted.allowsSource('huangdou'), allSourcesEnabled);
  });

  test('only the administrator can switch sources', () async {
    final store = await create({
      'sourceSwitches': jsonEncode({'huangdou': true}),
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
          name: '普通用户',
          sources: ['hongguo', 'huangdou'],
        ).toJson(),
      ]),
      'activeProfile': 'viewer',
    });
    expect(store.allowsSource('huangdou'), allSourcesEnabled);
    await expectLater(
      store.setSourceEnabled('hongguo', false),
      throwsStateError,
    );
    expect(store.sourceEnabled('hongguo'), isTrue);
  });

  test('retired password gate records are dropped on load', () async {
    final store = await create({
      LocalSnapshot.storageKey: jsonEncode({
        'version': 1,
        'values': {
          'profiles': jsonEncode([
            const LocalProfile(
              id: 'default',
              name: '管理员',
              admin: true,
            ).toJson(),
          ]),
          'sourceGateEnabled': false,
          'sourceGateOff': true,
          'sourceGateSalt': '',
          'sourceGateHash': '',
        },
      }),
    });
    expect(store.configurationError, isNull);
    expect(store.allowsSource('huangdou'), isFalse);
    await store.setSourceEnabled('hongguo', false);
    expect(
      store.preferences.getString(LocalSnapshot.storageKey),
      isNot(contains('sourceGate')),
    );
  });

  test('a damaged switch record falls back to defaults', () async {
    final store = await create({'sourceSwitches': 'not json'});
    expect(store.configurationError, isNull);
    expect(store.sourceEnabled('hongguo'), isTrue);
    expect(store.sourceEnabled('huangdou'), isFalse);
  });
}
