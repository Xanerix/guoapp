import 'package:duanju_app/app_device.dart';
import 'package:duanju_app/boss_key.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  final calls = <Object?>[];
  var registerResult = true;

  setUp(() {
    calls.clear();
    registerResult = true;
    bossKey.value = null;
    bossKeyEnabled.value = false;
    SharedPreferences.setMockInitialValues({});
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(AppDevice.channel, (call) async {
          if (call.method == 'setBossKey') {
            calls.add(call.arguments);
            return call.arguments == null ? true : registerResult;
          }
          return null;
        });
  });

  tearDown(() {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(AppDevice.channel, null);
  });

  test('legacy saved boss key defaults to enabled on restore', () async {
    SharedPreferences.setMockInitialValues({'bossKey': 'ctrl+B'});
    await restoreBossKey();
    expect(bossKey.value?.encoded, 'ctrl+B');
    expect(bossKeyEnabled.value, isTrue);
    expect(calls, [
      {'key': 'B', 'ctrl': true, 'alt': false, 'shift': false, 'meta': false},
    ]);
  });

  test(
    'disabled boss key is preserved on restore without registering',
    () async {
      SharedPreferences.setMockInitialValues({
        'bossKey': 'ctrl+B',
        'bossKeyEnabled': false,
      });
      await restoreBossKey();
      expect(bossKey.value?.encoded, 'ctrl+B');
      expect(bossKeyEnabled.value, isFalse);
      expect(calls, isEmpty);
    },
  );

  test(
    'toggling boss key registers and unregisters while keeping key',
    () async {
      const key = BossKey(key: 'B', ctrl: true);
      await setBossKey(key);
      expect(bossKey.value?.encoded, 'ctrl+B');
      expect(bossKeyEnabled.value, isTrue);

      calls.clear();
      await setBossKeyEnabled(false);
      expect(bossKey.value?.encoded, 'ctrl+B');
      expect(bossKeyEnabled.value, isFalse);
      expect(calls, [null]);

      calls.clear();
      await setBossKeyEnabled(true);
      expect(bossKeyEnabled.value, isTrue);
      expect(calls, [
        {'key': 'B', 'ctrl': true, 'alt': false, 'shift': false, 'meta': false},
      ]);
    },
  );

  test(
    'changing key while disabled updates shortcut without enabling',
    () async {
      await setBossKey(const BossKey(key: 'B', ctrl: true));
      await setBossKeyEnabled(false);
      calls.clear();

      await setBossKey(const BossKey(key: 'Q', alt: true));
      expect(bossKey.value?.encoded, 'alt+Q');
      expect(bossKeyEnabled.value, isFalse);
      expect(calls, [null]);
    },
  );

  test('failed re-enable throws and keeps disabled state', () async {
    await setBossKey(const BossKey(key: 'B', ctrl: true));
    await setBossKeyEnabled(false);
    registerResult = false;

    await expectLater(setBossKeyEnabled(true), throwsA(isA<AppFailure>()));
    expect(bossKeyEnabled.value, isFalse);
  });
}
