import 'dart:convert';

import 'package:duanju_app/local_store.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  Future<LocalStore> localStore() async {
    SharedPreferences.setMockInitialValues({});
    return LocalStore(await SharedPreferences.getInstance());
  }

  test(
    'theme survives restart and backup, with compatible old backups',
    () async {
      final store = await localStore();
      expect(store.themeMode, 'system');
      await store.setThemeMode('light');
      final restored = LocalStore(store.preferences);
      expect(restored.themeMode, 'light');
      final backup = await store.exportBackup();
      await store.setThemeMode('system');
      await store.importBackup(backup);
      expect(store.themeMode, 'light');
      final old = jsonDecode(backup) as Map<String, dynamic>;
      old.remove('themeMode');
      await store.setThemeMode('system');
      await store.importBackup(jsonEncode(old));
      expect(store.themeMode, 'system');
      old['themeMode'] = 'invalid';
      await expectLater(
        store.importBackup(jsonEncode(old)),
        throwsFormatException,
      );
      expect(store.themeMode, 'system');
      store.dispose();
      restored.dispose();
    },
  );
}
