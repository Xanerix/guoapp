import 'dart:convert';

import 'package:shared_preferences/shared_preferences.dart';

class LocalSnapshot {
  LocalSnapshot(this.preferences) {
    reloadValues();
  }

  static const storageKey = 'localSnapshot.v1';
  static const libraryKeys = {
    'history',
    'favorites',
    'followStates',
    'seriesCandidates',
    'mediaHistory',
    'source',
    'hideVip',
    'playback',
    'downloadPreferences',
    'catalogView',
    'recentSearches',
  };
  static const globalKeys = {
    'profiles',
    'activeProfile',
    'displayMode',
    'themeMode',
    'autoExport',
    'exportPosters',
    'sourceSwitches',
    'forceLogin',
  };
  static const _retiredKeys = {
    'sourceGateEnabled',
    'sourceGateOff',
    'sourceGateSalt',
    'sourceGateHash',
  };
  static final _retiredLibraryKey = RegExp(
    r'^(profile\.[a-zA-Z0-9_-]{1,64}\.)?(lanRecords|lanSettings|lanReceipts)$',
  );
  final SharedPreferences preferences;
  Map<String, Object> _values = {};

  static bool legacy(String key) =>
      key == storageKey ||
      owns(key) ||
      key.startsWith('seriesCandidates.') ||
      key.startsWith('followChecks.');

  static bool owns(String key) =>
      globalKeys.contains(key) ||
      libraryKeys.contains(key) ||
      RegExp(
        r'^profile\.[a-zA-Z0-9_-]{1,64}\.(history|favorites|followStates|seriesCandidates|mediaHistory|source|hideVip|playback|downloadPreferences|catalogView|recentSearches)$',
      ).hasMatch(key);

  Map<String, Object> get values => Map.of(_values);
  String? getString(String key) => _values[key] as String?;
  bool? getBool(String key) => _values[key] as bool?;

  void reloadValues() {
    final raw = preferences.getString(storageKey);
    if (raw == null) {
      _values = {
        for (final key in preferences.getKeys().where(owns))
          key: preferences.get(key)!,
      };
      _validate(_values);
      return;
    }
    final decoded = jsonDecode(raw) as Map;
    if (decoded['version'] != 1 || decoded['values'] is! Map) {
      throw const FormatException('本地配置快照无效');
    }
    final values = Map<String, Object>.from(decoded['values'] as Map)
      ..removeWhere(
        (key, _) =>
            _retiredKeys.contains(key) || _retiredLibraryKey.hasMatch(key),
      );
    if (!values.containsKey('profiles')) {
      throw const FormatException('本地配置快照无效');
    }
    _validate(values);
    _values = values;
  }

  void _validate(Map<String, Object> values) {
    for (final entry in values.entries) {
      final boolean = {
        'hideVip',
        'autoExport',
        'exportPosters',
        'forceLogin',
      }.contains(entry.key.split('.').last);
      if (!owns(entry.key) ||
          (boolean ? entry.value is! bool : entry.value is! String)) {
        throw const FormatException('本地配置字段无效');
      }
    }
  }
}
