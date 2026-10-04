import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/source_gate_taps.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  // 输入框光标闪烁会让动画不收敛，固定光标保证 pump 步进可预期。
  EditableText.debugDeterministicCursor = true;

  Future<LocalStore> create([Map<String, Object> initial = const {}]) async {
    SharedPreferences.setMockInitialValues(Map.of(initial));
    final store = testStore(await SharedPreferences.getInstance());
    addTearDown(store.dispose);
    return store;
  }

  test('default visibility hides restricted sources', () async {
    final store = await create();
    expect(store.sourceGateEnabled, isFalse);
    expect(store.sourcesUnlocked, isFalse);
    expect(
      store.sources.map((site) => site.id),
      allSourcesEnabled
          ? ['hongguo', 'hanxiaoquan', 'guipian', 'sorani']
          : ['hongguo'],
    );
    expect(store.allowsSource('huangdou'), isFalse);
    expect(store.allowsSource('hongguo'), isTrue);
    // 未启用密码锁时无处输入密码，需要先在弹窗里启用。
    await expectLater(store.unlockSources('666'), throwsStateError);
    expect(store.sourcesUnlocked, isFalse);
  });

  test('enabling the gate hides restricted sources and unlocks once', () async {
    final store = await create();
    await store.enableSourceGate('666');
    expect(store.sourceGateEnabled, isTrue);
    expect(store.sourcesUnlocked, isTrue);
    expect(store.sources.length, allSourcesEnabled ? 11 : 1);

    store.lockSources();
    expect(store.sourcesUnlocked, isFalse);
    expect(store.sources.length, allSourcesEnabled ? 4 : 1);
    expect(store.allowsSource('huangdou'), isFalse);
    expect(store.allowsSource('hongguo'), isTrue);
    if (allSourcesEnabled) expect(store.allowsSource('sorani'), isTrue);

    await expectLater(store.unlockSources('777'), throwsStateError);
    expect(store.sourcesUnlocked, isFalse);
    await store.unlockSources('666');
    expect(store.sourcesUnlocked, isTrue);
    expect(store.sources.length, allSourcesEnabled ? 11 : 1);
  });

  test('a saved gate hides sources again after restart', () async {
    final store = await create();
    await store.enableSourceGate('666');
    store.lockSources();
    final restarted = testStore(store.preferences);
    addTearDown(restarted.dispose);
    expect(restarted.sourceGateEnabled, isTrue);
    expect(restarted.sourcesUnlocked, isFalse);
    expect(restarted.sources.length, allSourcesEnabled ? 4 : 1);
    await restarted.unlockSources('666');
    expect(restarted.sourcesUnlocked, isTrue);
  });

  test('disabling the gate restores every compiled source', () async {
    final store = await create();
    await store.enableSourceGate('666');
    store.lockSources();
    await store.disableSourceGate();
    expect(store.sourceGateEnabled, isFalse);
    expect(store.sourcesUnlocked, isTrue);
    expect(store.sources.length, allSourcesEnabled ? 11 : 1);
  });

  test('an invalid pin must be 3 to 12 digits', () async {
    final store = await create();
    await expectLater(store.enableSourceGate('12'), throwsStateError);
    await expectLater(store.enableSourceGate('abcdef'), throwsStateError);
    await expectLater(
      store.enableSourceGate('1234567890123'),
      throwsStateError,
    );
    expect(store.sourceGateEnabled, isFalse);
  });

  test('repeated taps on one entry only fire after six in a row', () {
    final gate = RepeatTapGate();
    for (var i = 0; i < 5; i++) {
      expect(gate.register(2), isFalse);
    }
    expect(gate.register(2), isTrue);
    expect(gate.register(2), isFalse);
    gate.reset();
    for (var i = 0; i < 5; i++) {
      expect(gate.register(2), isFalse);
    }
    // 切换入口会清零计数，避免误触发。
    expect(gate.register(0), isFalse);
    for (var i = 0; i < 5; i++) {
      expect(gate.register(2), isFalse);
    }
    expect(gate.register(2), isTrue);
  });

  test('a damaged gate record keeps hiding restricted sources', () async {
    final store = await create({
      'sourceGateEnabled': true,
      'sourceGateSalt': 'bad',
      'sourceGateHash': 'bad',
    });
    expect(store.configurationError, isNull);
    expect(store.sourceGateEnabled, isFalse);
    expect(store.sourcesUnlocked, isFalse);
    expect(store.sources.length, allSourcesEnabled ? 4 : 1);
  });

  test('a user-disabled gate keeps every compiled source visible', () async {
    final store = await create({
      'sourceGateEnabled': false,
      'sourceGateOff': true,
    });
    expect(store.sourceGateEnabled, isFalse);
    expect(store.sourcesUnlocked, isTrue);
    expect(store.sources.length, allSourcesEnabled ? 11 : 1);
  });
}
