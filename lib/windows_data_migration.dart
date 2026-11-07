import 'dart:io';

import 'package:path/path.dart' as path;
import 'package:path_provider/path_provider.dart';

import 'diary_service.dart';

const legacyDataFolder = '真果鉴';

Future<void> migrateWindowsData() async {
  final target = await getApplicationSupportDirectory();
  final legacyParent = Directory(
    path.join(target.parent.path, legacyDataFolder),
  );
  final legacy = Directory(path.join(legacyParent.path, legacyDataFolder));
  try {
    if (!await moveLegacyData(legacy, target)) return;
    DiaryService.add('[App] 已迁移旧数据目录: ${legacy.path} -> ${target.path}');
  } catch (failure) {
    DiaryService.add('[App] 旧数据目录迁移失败: $failure');
    return;
  }
  try {
    await legacyParent.delete();
  } catch (_) {}
}

Future<bool> moveLegacyData(Directory legacy, Directory target) async {
  if (!await legacy.exists()) return false;
  if (await target.exists()) {
    if (!await target.list().isEmpty) return false;
    await target.delete();
  }
  try {
    await legacy.rename(target.path);
  } catch (_) {
    await target.create(recursive: true);
    rethrow;
  }
  return true;
}
