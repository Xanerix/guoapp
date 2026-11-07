import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:duanju_app/windows_data_migration.dart';
import 'package:path/path.dart' as path;

void main() {
  late Directory root;
  late Directory legacy;
  late Directory target;

  setUp(() async {
    root = await Directory.systemTemp.createTemp('migration');
    legacy = Directory(path.join(root.path, '真果鉴', '真果鉴'));
    target = Directory(path.join(root.path, 'com.example.app'));
    await File(
      path.join(legacy.path, 'store', 'library.json'),
    ).create(recursive: true);
    await File(
      path.join(legacy.path, 'shared_preferences.json'),
    ).writeAsString('{}');
  });

  tearDown(() => root.delete(recursive: true));

  test('moves legacy data into an empty target', () async {
    await target.create();

    expect(await moveLegacyData(legacy, target), isTrue);
    expect(await legacy.exists(), isFalse);
    expect(
      await File(path.join(target.path, 'store', 'library.json')).exists(),
      isTrue,
    );
    expect(
      await File(path.join(target.path, 'shared_preferences.json')).exists(),
      isTrue,
    );
  });

  test('moves legacy data when the target does not exist', () async {
    expect(await moveLegacyData(legacy, target), isTrue);
    expect(
      await File(path.join(target.path, 'shared_preferences.json')).exists(),
      isTrue,
    );
  });

  test('keeps both directories when the target already has data', () async {
    await File(
      path.join(target.path, 'store', 'own.json'),
    ).create(recursive: true);

    expect(await moveLegacyData(legacy, target), isFalse);
    expect(await legacy.exists(), isTrue);
    expect(
      await File(path.join(target.path, 'store', 'library.json')).exists(),
      isFalse,
    );
  });

  test('does nothing without legacy data', () async {
    await legacy.parent.delete(recursive: true);
    await target.create();

    expect(await moveLegacyData(legacy, target), isFalse);
    expect(await target.exists(), isTrue);
  });
}
