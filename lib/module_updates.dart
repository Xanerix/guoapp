import 'dart:convert';

import 'package:shared_preferences/shared_preferences.dart';

import 'source_modules.dart';

class ModuleUpdate {
  const ModuleUpdate(this.module, this.latest);
  final SourceModule module;
  final ModuleIndexEntry latest;
}

List<ModuleUpdate> availableModuleUpdates(
  List<SourceModule> installed,
  List<ModuleIndexEntry> entries,
  Map<String, String> ignored,
) {
  final latest = <String, ModuleIndexEntry>{};
  for (final entry in entries) {
    final previous = latest[entry.id];
    if (previous == null ||
        isNewerModuleVersion(entry.version, previous.version) ||
        entry.version == previous.version &&
            previous.bundled &&
            !entry.bundled) {
      latest[entry.id] = entry;
    }
  }
  return [
    for (final module in installed)
      if (latest[module.id] case final entry?)
        if (module.error.isEmpty &&
            isNewerModuleVersion(entry.version, module.version) &&
            ignored[module.id] != entry.version)
          ModuleUpdate(module, entry),
  ];
}

class ModuleUpdatePreferences {
  ModuleUpdatePreferences(this.preferences);
  static const storageKey = 'moduleUpdates.ignored.v1';
  final SharedPreferences preferences;

  Map<String, String> get ignored {
    try {
      final raw = preferences.getString(storageKey);
      if (raw == null) return const {};
      final value = jsonDecode(raw);
      if (value is! Map) return const {};
      return {
        for (final entry in value.entries)
          if (entry.key is String && entry.value is String)
            entry.key as String: entry.value as String,
      };
    } catch (_) {
      return const {};
    }
  }

  Future<void> ignore(List<ModuleUpdate> updates) async {
    final next = {
      ...ignored,
      for (final update in updates) update.module.id: update.latest.version,
    };
    if (!await preferences.setString(storageKey, jsonEncode(next))) {
      throw StateError('站源更新提醒偏好未能保存');
    }
  }
}
