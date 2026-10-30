import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'catalog_sort.dart';
import 'diary_service.dart';
import 'download_preferences.dart';
import 'follow_state.dart';
import 'hongguo_series.dart';
import 'local_files.dart';
import 'local_profiles.dart';
import 'local_snapshot.dart';
import 'models.dart';
import 'playback_preferences.dart';
import 'theme_color.dart';

const _usersFile = LocalFiles.marker;
const _settingsFile = 'settings.json';
const _displayModes = {'auto', 'television', 'phone', 'pad', 'window'};
const _themeModes = {'light', 'dark', 'system'};
const _movedFlags = {
  'followSystemColor': 'followSystemColor',
  'checkUpdates': 'checkUpdates',
  'shareUsage': 'shareUsage',
  'autoExport': 'exportAfterDownload',
  'deleteAfterAutoExport': 'deleteAfterAutoExport',
  'deleteAfterManualExport': 'deleteAfterManualExport',
  'televisionKeyboard': 'televisionKeyboard',
};
const _movedTexts = {'themeColor', 'ignoredUpdate'};
const _backupFlags = {
  'checkUpdates',
  'shareUsage',
  'exportAfterDownload',
  'deleteAfterAutoExport',
  'deleteAfterManualExport',
  'televisionKeyboard',
};
const _blocked = '本地用户配置损坏，已锁定访问。原始记录已保留，请重新读取或从备份恢复。';

String _userFile(String id, String name) => 'users/$id/$name.json';

List<Object?> _list(Object? value) => value is List ? value : const [];
Map<String, Object?> _map(Object? value) =>
    value is Map ? Map<String, Object?>.from(value) : {};
Iterable<Map<String, dynamic>> _rows(Object? value) =>
    _list(value).whereType<Map>().map(Map<String, dynamic>.from);

class LocalStore extends ChangeNotifier {
  LocalStore._(this.preferences, this.files, this._pinHasher);

  static Future<LocalStore> open(
    SharedPreferences preferences,
    LocalFiles files, {
    Future<String> Function(String, String)? pinHasher,
  }) async {
    final store = LocalStore._(preferences, files, pinHasher ?? hashProfilePin);
    await store._initialize();
    return store;
  }

  final SharedPreferences preferences;
  final LocalFiles files;
  final Future<String> Function(String pin, String salt) _pinHasher;
  final Map<String, WatchEntry> _history = {};
  final Map<String, Drama> _favorites = {};
  final Map<String, FollowState> _followStates = {};
  Map<String, FollowCheck> _followChecks = {};
  Map<String, Object?> _settings = {};
  Map<String, Object?> _userSettings = {};
  Future<void> _writes = Future<void>.value();
  List<LocalProfile> _profiles = [];
  String _current = 'default';
  bool? _forceLogin;
  bool _locked = false;
  bool _disposed = false;
  String? _configurationError;
  int _epoch = 0;
  int _failures = 0;
  DateTime _retryAfter = DateTime(2000);

  Future<void> _initialize() async {
    try {
      if (!await files.exists()) await _migrate();
    } catch (_) {
      _block(_blocked);
      return;
    }
    _settings = await _readMap(_settingsFile);
    try {
      final users = await files.read(_usersFile);
      if (users is! Map) throw const FormatException('用户配置无效');
      _profiles = _readProfiles(users['profiles']);
      _current = users['active'] is String
          ? users['active'] as String
          : 'default';
      _forceLogin = users['forceLogin'] is bool
          ? users['forceLogin'] as bool
          : null;
    } catch (_) {
      _block(_blocked);
      return;
    }
    if (!_profiles.any((profile) => profile.id == _current)) {
      _current = _profiles.firstWhere((profile) => profile.admin).id;
    }
    _configurationError = null;
    _locked = forceLogin && profile.protected;
    await _loadUser();
  }

  Future<void> _migrate() async {
    final snapshot = LocalSnapshot(preferences);
    String? text(String key) => snapshot.getString(key);
    T decoded<T>(String? raw, T fallback) {
      try {
        final value = raw == null ? null : jsonDecode(raw);
        return value is T ? value : fallback;
      } catch (_) {
        return fallback;
      }
    }

    String key(String name, String id) =>
        id == 'default' ? name : 'profile.$id.$name';
    final rawProfiles = text('profiles');
    if (rawProfiles == null &&
        (text('activeProfile') != null ||
            snapshot.values.keys.any((key) => key.startsWith('profile.')))) {
      throw const FormatException('已有用户配置缺失');
    }
    final profiles = rawProfiles == null
        ? [const LocalProfile(id: 'default', name: '管理员', admin: true)]
        : _readProfiles(jsonDecode(rawProfiles));
    final contents = <String, Object?>{
      _settingsFile: {
        'displayMode': ?text('displayMode'),
        'themeMode': ?text('themeMode'),
        'sourceSwitches': decoded<Map>(text('sourceSwitches'), {}),
        for (final entry in _movedFlags.entries)
          if (preferences.get(entry.key) case final bool value)
            entry.value: value,
        for (final name in _movedTexts)
          if (preferences.get(name) case final String value) name: value,
      },
      _usersFile: {
        'profiles': [for (final profile in profiles) profile.toJson()],
        'active': text('activeProfile') ?? 'default',
        'forceLogin': ?snapshot.getBool('forceLogin'),
      },
    };
    for (final profile in profiles) {
      final id = profile.id;
      final checks = preferences.get('followChecks.$id');
      contents.addAll({
        _userFile(id, 'history'): readJsonList(text(key('history', id))),
        _userFile(id, 'favorites'): readJsonList(text(key('favorites', id))),
        _userFile(id, 'followStates'): decoded<Map>(
          text(key('followStates', id)),
          {},
        ),
        _userFile(id, 'followChecks'): decoded<Map>(
          checks is String ? checks : null,
          {},
        ),
        _userFile(id, 'settings'): {
          'source': ?text(key('source', id)),
          'playback': decoded<Map>(text(key('playback', id)), {}),
          'downloadPreferences': decoded<Map>(
            text(key('downloadPreferences', id)),
            {},
          ),
          'recentSearches': decoded<List>(text(key('recentSearches', id)), []),
        },
      });
    }
    await files.replaceAll(contents);
    await _removeLegacy();
  }

  Future<void> _removeLegacy() async {
    for (final key in preferences.getKeys().toList()) {
      if (LocalSnapshot.legacy(key) ||
          _movedFlags.containsKey(key) ||
          _movedTexts.contains(key)) {
        await preferences.remove(key);
      }
    }
  }

  Future<Object?> _readFile(String name) async {
    try {
      return await files.read(name);
    } on FormatException {
      DiaryService.add('[存储] $name 无法读取，已改名保留，按空记录继续');
      await files.quarantine(name).catchError((Object _) {});
      return null;
    }
  }

  Future<Map<String, Object?>> _readMap(String name) async =>
      _map(await _readFile(name));

  Future<void> _loadUser() async {
    _history.clear();
    _favorites.clear();
    _followStates.clear();
    _followChecks = {};
    _userSettings = {};
    if (_configurationError != null) return;
    final id = _current;
    _userSettings = await _readMap(_userFile(id, 'settings'));
    for (final row in _rows(await _readFile(_userFile(id, 'history')))) {
      try {
        final entry = WatchEntry.fromJson(row);
        _history[entry.drama.id] = entry;
      } catch (_) {}
    }
    for (final row in _rows(await _readFile(_userFile(id, 'favorites')))) {
      try {
        final drama = Drama.fromJson(row);
        _favorites[drama.id] = drama;
      } catch (_) {}
    }
    final states = _map(await _readFile(_userFile(id, 'followStates')));
    for (final drama in _favorites.values) {
      try {
        _followStates[drama.id] = FollowState.fromJson(
          Map<String, dynamic>.from(states[drama.id] as Map),
        );
      } catch (_) {
        _followStates[drama.id] = FollowState.initial(drama);
      }
    }
    final checks = _map(await _readFile(_userFile(id, 'followChecks')));
    for (final entry in checks.entries) {
      try {
        _followChecks[entry.key] = FollowCheck.fromJson(entry.value);
      } catch (_) {}
    }
  }

  static List<LocalProfile> _readProfiles(dynamic records) {
    final profiles = (records as List)
        .map(
          (row) => LocalProfile.fromJson(Map<String, dynamic>.from(row as Map)),
        )
        .toList();
    if (profiles.isEmpty ||
        profiles.length > 20 ||
        profiles.map((profile) => profile.id).toSet().length !=
            profiles.length ||
        profiles.where((profile) => profile.admin).length != 1 ||
        (profiles.length > 1 &&
            !profiles.firstWhere((profile) => profile.admin).protected)) {
      throw const FormatException('用户配置无效');
    }
    return profiles;
  }

  void _block(String message) {
    _configurationError = message;
    _locked = true;
    _profiles = [
      const LocalProfile(id: 'default', name: '配置待恢复', download: false),
    ];
    _current = 'default';
    _forceLogin = null;
    _history.clear();
    _favorites.clear();
    _followStates.clear();
    _followChecks = {};
    _userSettings = {};
    _epoch++;
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  Future<void> _write(String name, Object? value) async {
    try {
      await files.write(name, value);
    } catch (_) {
      throw StateError('未能保存，原有记录已保留；请检查存储空间和权限后重试');
    }
  }

  String? get configurationError => _configurationError;
  List<LocalProfile> get profiles => List.unmodifiable(_profiles);
  LocalProfile get profile =>
      _profiles.firstWhere((profile) => profile.id == _current);
  bool get locked => _locked || _configurationError != null;
  int get profileEpoch => _epoch;
  bool get forceLogin {
    if (_configurationError != null) return true;
    final admin = _profiles.firstWhere((profile) => profile.admin);
    return _forceLogin ?? admin.protected;
  }

  bool get canDownload => !locked && (profile.admin || profile.download);
  bool allowsSource(String source) =>
      !locked &&
      SourceSite.isAvailable(source) &&
      profile.allows(source) &&
      sourceEnabled(source);
  List<SourceSite> get sources =>
      SourceSite.values.where((site) => allowsSource(site.id)).toList();
  List<SourceSite> get permittedSources => [
    for (final site in SourceSite.values)
      if (!locked && profile.allows(site.id)) site,
  ];

  bool sourceEnabled(String source) => switch (_settings['sourceSwitches']) {
    final Map switches when switches[source] is bool =>
      switches[source] as bool,
    _ => !SourceSite.isRestricted(source),
  };

  Future<void> setSourceEnabled(String source, bool enabled) {
    if (!SourceSite.isAvailable(source)) {
      return Future.error(StateError('站源无效'));
    }
    final epoch = _epoch;
    return _queue(() async {
      _requireAdmin();
      if (epoch != _epoch) throw StateError('当前用户已变更，请重试');
      final switches = {..._map(_settings['sourceSwitches']), source: enabled};
      await _saveSettings({..._settings, 'sourceSwitches': switches});
    });
  }

  Map<String, FollowCheck> get followChecks => Map.of(_followChecks);

  Future<void> saveFollowChecks(
    Map<String, FollowCheck> checks, {
    required int epoch,
  }) => _queue(() async {
    if (locked || epoch != _epoch) return;
    final kept = {
      for (final entry in checks.entries)
        if (_favorites.containsKey(entry.key)) entry.key: entry.value,
    };
    await _write(_userFile(_current, 'followChecks'), {
      for (final entry in kept.entries) entry.key: entry.value.toJson(),
    });
    _followChecks = kept;
  });

  List<WatchEntry> get history =>
      _history.values
          .where((entry) => allowsSource(entry.drama.source))
          .toList()
        ..sort((a, b) => b.updatedAt.compareTo(a.updatedAt));
  List<Drama> get favorites => _favorites.values
      .where((drama) => allowsSource(drama.source))
      .toList()
      .reversed
      .toList();
  WatchEntry? watched(String id) {
    final entry = _history[id];
    return entry != null && allowsSource(entry.drama.source) ? entry : null;
  }

  bool isFavorite(String id) =>
      _favorites[id] != null && allowsSource(_favorites[id]!.source);
  FollowState? following(String id) =>
      isFavorite(id) ? _followStates[id] : null;

  bool _flag(String key, bool fallback) => switch (_settings[key]) {
    final bool value => value,
    _ => fallback,
  };

  String get displayMode {
    final value = _settings['displayMode'];
    return _displayModes.contains(value) ? value as String : 'auto';
  }

  String get themeMode {
    final value = _settings['themeMode'];
    return _themeModes.contains(value) ? value as String : 'system';
  }

  bool get followSystemColor => _flag('followSystemColor', true);
  ThemeColor get themeColor =>
      ThemeColor.byName(_settings['themeColor']) ?? ThemeColor.orange;
  bool get sideBarExpanded => preferences.getBool('sideBarExpanded') ?? true;
  bool get playerWide => preferences.getBool('playerWide') ?? false;
  ({double left, double top, double extent})? get miniWindow {
    final values = preferences
        .getString('miniWindow')
        ?.split(',')
        .map(double.tryParse)
        .toList();
    if (values == null || values.length != 3 || values.contains(null)) {
      return null;
    }
    return (left: values[0]!, top: values[1]!, extent: values[2]!);
  }

  bool get televisionKeyboard => _flag('televisionKeyboard', false);
  bool get checkUpdates => _flag('checkUpdates', true);
  bool get autoExport => _flag('exportAfterDownload', false);
  bool get deleteAfterAutoExport => _flag('deleteAfterAutoExport', false);
  bool get deleteAfterManualExport => _flag('deleteAfterManualExport', false);
  bool get shareUsage => _flag('shareUsage', true);
  String get ignoredUpdate => switch (_settings['ignoredUpdate']) {
    final String version => version,
    _ => '',
  };

  String get source {
    if (locked) return '';
    final value = _userSettings['source'];
    return sources.any((site) => site.id == value)
        ? value as String
        : sources.firstOrNull?.id ?? '';
  }

  DownloadPreferences get downloadPreferences {
    if (locked) return const DownloadPreferences();
    try {
      return DownloadPreferences.fromJson(
        Map<String, dynamic>.from(_userSettings['downloadPreferences'] as Map),
      );
    } catch (_) {
      return const DownloadPreferences();
    }
  }

  PlaybackPreferences get playbackPreferences {
    if (locked) return const PlaybackPreferences();
    try {
      return PlaybackPreferences.fromJson(
        Map<String, dynamic>.from(_userSettings['playback'] as Map),
      );
    } catch (_) {
      return const PlaybackPreferences();
    }
  }

  List<String> get recentSearches {
    if (locked) return const [];
    return _list(
      _userSettings['recentSearches'],
    ).whereType<String>().take(20).toList();
  }

  Future<void> _saveSettings(Map<String, Object?> settings) async {
    await _write(_settingsFile, settings);
    _settings = settings;
    _notify();
  }

  Future<void> _setting(String key, Object value) =>
      _queue(() => _saveSettings({..._settings, key: value}));

  Future<void> _userSetting(String key, Object value) {
    final epoch = _epoch;
    return _queue(() async {
      if (locked || epoch != _epoch) throw StateError('当前用户已变更，请重试');
      final settings = {..._userSettings, key: value};
      await _write(_userFile(_current, 'settings'), settings);
      _userSettings = settings;
      _notify();
    });
  }

  Future<void> rememberSearch(String text) {
    final query = text.trim();
    if (query.isEmpty || query.runes.length > 80) return Future.value();
    final epoch = _epoch;
    return _queue(() async {
      if (locked || epoch != _epoch) return;
      final entries = [
        query,
        ...recentSearches.where(
          (entry) => normalizedSearchText(entry) != normalizedSearchText(query),
        ),
      ].take(20).toList();
      final settings = {..._userSettings, 'recentSearches': entries};
      await _write(_userFile(_current, 'settings'), settings);
      _userSettings = settings;
      _notify();
    });
  }

  Future<void> clearRecentSearches() =>
      _userSetting('recentSearches', const <String>[]);

  Future<void> setForceLogin(bool value) => _queue(() async {
    _requireAdmin();
    await _saveUsers(_profiles, _current, value);
    _forceLogin = value;
    _notify();
  });

  Future<void> setSource(String value) {
    if (!allowsSource(value)) return Future.error(StateError('当前用户没有此站源权限'));
    return _userSetting('source', value);
  }

  Future<void> setDisplayMode(String value) => _displayModes.contains(value)
      ? _setting('displayMode', value)
      : Future.value();
  Future<void> setThemeMode(String value) => _themeModes.contains(value)
      ? _setting('themeMode', value)
      : Future.value();
  Future<void> setFollowSystemColor(bool value) =>
      _setting('followSystemColor', value);
  Future<void> setThemeColor(ThemeColor value) =>
      _setting('themeColor', value.name);
  Future<void> setSideBarExpanded(bool value) => _queue(() async {
    await preferences.setBool('sideBarExpanded', value);
    _notify();
  });
  Future<void> setPlayerWide(bool value) => _queue(() async {
    await preferences.setBool('playerWide', value);
    _notify();
  });
  Future<void> setMiniWindow(
    ({double left, double top, double extent}) value,
  ) => _queue(
    () => preferences.setString(
      'miniWindow',
      '${value.left},${value.top},${value.extent}',
    ),
  );
  Future<void> setTelevisionKeyboard(bool value) =>
      _setting('televisionKeyboard', value);
  Future<void> setCheckUpdates(bool value) => _setting('checkUpdates', value);
  Future<void> setAutoExport(bool value) =>
      _setting('exportAfterDownload', value);
  Future<void> setDeleteAfterAutoExport(bool value) =>
      _setting('deleteAfterAutoExport', value);
  Future<void> setDeleteAfterManualExport(bool value) =>
      _setting('deleteAfterManualExport', value);
  Future<void> setShareUsage(bool value) => _setting('shareUsage', value);
  Future<void> setIgnoredUpdate(String version) =>
      _setting('ignoredUpdate', version);
  Future<void> setPlaybackPreferences(PlaybackPreferences value) {
    PlaybackPreferences.fromJson(value.toJson());
    return _userSetting('playback', value.toJson());
  }

  Future<void> setDownloadPreferences(DownloadPreferences value) {
    DownloadPreferences.fromJson(value.toJson());
    return _userSetting('downloadPreferences', value.toJson());
  }

  Future<void> _saveStates(Map<String, FollowState> states) async {
    await _write(_userFile(_current, 'followStates'), {
      for (final entry in states.entries) entry.key: entry.value.toJson(),
    });
    _followStates
      ..clear()
      ..addAll(states);
  }

  Future<void> _saveFavorites(Map<String, Drama> favorites) async {
    await _write(_userFile(_current, 'favorites'), [
      for (final drama in favorites.values) drama.toJson(),
    ]);
    _favorites
      ..clear()
      ..addAll(favorites);
  }

  Future<void> _saveHistory(Iterable<WatchEntry> entries) async {
    final rows = entries.toList();
    await _write(_userFile(_current, 'history'), [
      for (final entry in rows) entry.toJson(),
    ]);
    _history
      ..clear()
      ..addEntries(rows.map((entry) => MapEntry(entry.drama.id, entry)));
  }

  Future<void> toggleFavorite(Drama drama) {
    final epoch = _epoch;
    return _queue(() async {
      if (!allowsSource(drama.source) || epoch != _epoch) return;
      final entries = Map.of(_favorites);
      final states = Map.of(_followStates);
      if (entries.containsKey(drama.id)) {
        entries.remove(drama.id);
        states.remove(drama.id);
      } else {
        if (entries.length >= 20000) throw StateError('追剧数量已达上限，请先整理追剧');
        entries[drama.id] = drama;
        states[drama.id] = FollowState.initial(drama);
      }
      await _saveStates(states);
      await _saveFavorites(entries);
      _notify();
    });
  }

  Future<void> removeFavorites(Set<String> ids) {
    final epoch = _epoch;
    return _queue(() async {
      if (epoch != _epoch) return;
      final removed = {
        for (final id in ids)
          if (_favorites[id] case final drama? when allowsSource(drama.source))
            id,
      };
      if (removed.isEmpty) return;
      await _saveStates(
        Map.of(_followStates)..removeWhere((id, _) => removed.contains(id)),
      );
      await _saveFavorites(
        Map.of(_favorites)..removeWhere((id, _) => removed.contains(id)),
      );
      _notify();
    });
  }

  Future<void> markUpdatesRead(String id) {
    final epoch = _epoch;
    return _queue(() async {
      if (!isFavorite(id) || epoch != _epoch) return;
      await _saveStates(
        Map.of(_followStates)..[id] = _followStates[id]!.markRead(),
      );
      _notify();
    });
  }

  Future<void> markSeriesSeasonRead(String id, String seasonId) {
    final epoch = _epoch;
    return _queue(() async {
      if (!isFavorite(id) || epoch != _epoch) return;
      final current = _followStates[id]!;
      final updated = current.markSeriesSeasonRead(seasonId);
      if (identical(current, updated)) return;
      await _saveStates(Map.of(_followStates)..[id] = updated);
      _notify();
    });
  }

  Future<void> saveWatch(WatchEntry entry) {
    final epoch = _epoch;
    return _queue(() async {
      if (!allowsSource(entry.drama.source) || epoch != _epoch) return;
      final id = entry.drama.id;
      final previous = _history[id]?.drama;
      final current = previous == null
          ? entry
          : WatchEntry(
              drama: previous.merge(entry.drama),
              episode: entry.episode,
              position: entry.position,
              duration: entry.duration,
              updatedAt: entry.updatedAt,
            );
      final sorted = [
        for (final other in _history.values)
          if (other.drama.id != id) other,
        current,
      ]..sort((a, b) => b.updatedAt.compareTo(a.updatedAt));
      await _saveHistory(sorted.take(300));
      if (_followStates[id] case final following?) {
        final state = following.observe(current.drama);
        if (jsonEncode(state) != jsonEncode(following)) {
          await _saveStates(Map.of(_followStates)..[id] = state);
        }
        final favorite = _favorites[id]!;
        final merged = favorite.merge(current.drama);
        if (jsonEncode(merged) != jsonEncode(favorite)) {
          await _saveFavorites(Map.of(_favorites)..[id] = merged);
        }
      }
      _notify();
    });
  }

  Future<void> clearHistory() {
    final epoch = _epoch;
    return _queue(() async {
      if (locked || epoch != _epoch) return;
      await _saveHistory(const []);
      _notify();
    });
  }

  Future<void> removeHistory(Set<String> ids) {
    final epoch = _epoch;
    return _queue(() async {
      if (epoch != _epoch) return;
      final removed = {
        for (final id in ids)
          if (_history[id] case final entry?
              when allowsSource(entry.drama.source))
            id,
      };
      if (removed.isEmpty) return;
      await _saveHistory(
        _history.values.where((entry) => !removed.contains(entry.drama.id)),
      );
      _notify();
    });
  }

  Future<void> refreshDrama(Drama drama) => refreshDramas([drama]);

  Future<void> refreshDramas(Iterable<Drama> dramas) {
    final epoch = _epoch;
    final updates = dramas.toList();
    return _queue(() async {
      if (locked || epoch != _epoch) return;
      final favorites = Map.of(_favorites);
      final history = Map.of(_history);
      final states = Map.of(_followStates);
      var favoritesChanged = false, historyChanged = false;
      for (final drama in updates) {
        if (!allowsSource(drama.source)) continue;
        if (favorites[drama.id] case final favorite?) {
          final merged = favorite.merge(drama);
          if (jsonEncode(merged) != jsonEncode(favorite)) {
            favorites[drama.id] = merged;
            favoritesChanged = true;
          }
          states[drama.id] = states[drama.id]!.observe(merged);
        }
        if (history[drama.id] case final watched?) {
          final merged = watched.drama.merge(drama);
          if (jsonEncode(merged) != jsonEncode(watched.drama)) {
            history[drama.id] = WatchEntry(
              drama: merged,
              episode: watched.episode,
              position: watched.position,
              duration: watched.duration,
              updatedAt: watched.updatedAt,
            );
            historyChanged = true;
          }
        }
      }
      final candidates = <String, Drama>{
        for (final entry in history.values) entry.drama.id: entry.drama,
        ...favorites,
        for (final drama in updates)
          if (allowsSource(drama.source)) drama.id: drama,
      };
      for (final state in states.values) {
        for (final notice in state.seriesSeasons.values) {
          candidates.putIfAbsent(
            notice.id,
            () => Drama(
              id: notice.id,
              source: SourceSite.hongguo.id,
              sourceId: notice.id.substring(SourceSite.hongguo.id.length + 1),
              title: notice.title,
            ),
          );
        }
      }
      final favoriteIds = favorites.keys.toSet();
      for (final favorite in favorites.values) {
        if (favorite.source != SourceSite.hongguo.id ||
            states[favorite.id] == null) {
          continue;
        }
        states[favorite.id] = observeHongguoSeriesSeasons(
          anchor: favorite,
          state: states[favorite.id]!,
          candidates: candidates.values,
          favoriteIds: favoriteIds,
        );
      }
      final statesChanged = jsonEncode(states) != jsonEncode(_followStates);
      if (!favoritesChanged && !historyChanged && !statesChanged) return;
      if (statesChanged) await _saveStates(states);
      if (favoritesChanged) await _saveFavorites(favorites);
      if (historyChanged) await _saveHistory(history.values);
      _notify();
    });
  }

  void _requireAdmin() {
    if (locked || !profile.admin) throw StateError('仅管理员可以修改此设置');
  }

  Future<void> _checkPin(LocalProfile target, String pin) async {
    if (DateTime.now().isBefore(_retryAfter)) {
      throw StateError('密码输入过于频繁，请稍后再试');
    }
    if (!await checkProfilePin(target, pin)) {
      _failures++;
      if (_failures >= 3) {
        _retryAfter = DateTime.now().add(
          Duration(seconds: (_failures * 2).clamp(0, 30)),
        );
      }
      throw StateError('密码不正确');
    }
    _failures = 0;
  }

  Future<void> _saveUsers(
    List<LocalProfile> profiles,
    String active,
    bool? forceLogin,
  ) => _write(_usersFile, {
    'profiles': [for (final profile in profiles) profile.toJson()],
    'active': active,
    'forceLogin': ?forceLogin,
  });

  Future<void> switchProfile(String id, {String pin = ''}) => _queue(() async {
    if (_configurationError != null) throw StateError('本地用户配置损坏，请先恢复');
    final target = _profiles.firstWhere((profile) => profile.id == id);
    await _checkPin(target, pin);
    await _saveUsers(_profiles, id, _forceLogin);
    _current = id;
    _locked = false;
    await _loadUser();
    _epoch++;
    _notify();
  });
  void lock() {
    if (!profile.protected) return;
    _locked = true;
    _epoch++;
    _notify();
  }

  Future<void> saveProfile({
    String? id,
    required String name,
    required List<String> sources,
    required bool download,
    String? pin,
  }) => _queue(() async {
    _requireAdmin();
    final old = _profiles.where((profile) => profile.id == id).firstOrNull;
    final targetId = old?.id ?? randomProfileToken();
    final cleanName = name.trim();
    if (cleanName.isEmpty || cleanName.length > 40) {
      throw StateError('用户名需要 1 至 40 个字符');
    }
    if (sources.any((source) => !SourceSite.isKnown(source))) {
      throw StateError('站源无效');
    }
    if (targetId != 'default' &&
        !_profiles.firstWhere((profile) => profile.admin).protected) {
      throw StateError('请先为管理员设置密码，再创建或修改其他用户');
    }
    if (old == null && _profiles.length >= 20) {
      throw StateError('最多支持 20 个本地用户');
    }
    var salt = old?.salt ?? '', hash = old?.pinHash ?? '';
    if (pin != null) {
      if (pin.isEmpty) {
        if (targetId == 'default' && _profiles.length > 1) {
          throw StateError('存在其他用户时不能取消管理员密码');
        }
        salt = '';
        hash = '';
      } else {
        if (pin.length < 6 || pin.length > 128) {
          throw StateError('密码需要 6 至 128 个字符');
        }
        salt = randomProfileToken();
        hash = await _pinHasher(pin, salt);
      }
    }
    _requireAdmin();
    final updated = LocalProfile(
      id: targetId,
      name: cleanName,
      admin: targetId == 'default',
      sources: sources.toSet().toList(),
      download: download,
      salt: salt,
      pinHash: hash,
    );
    final profiles =
        [
          for (final profile in _profiles)
            if (profile.id != targetId) profile,
          updated,
        ]..sort(
          (a, b) => a.admin
              ? -1
              : b.admin
              ? 1
              : a.name.compareTo(b.name),
        );
    await _saveUsers(profiles, _current, _forceLogin);
    _profiles = profiles;
    _notify();
  });
  Future<void> deleteProfile(String id) => _queue(() async {
    _requireAdmin();
    if (id == 'default') throw StateError('不能删除管理员');
    final profiles = _profiles.where((profile) => profile.id != id).toList();
    await _saveUsers(profiles, _current, _forceLogin);
    _profiles = profiles;
    await files.remove('users/$id').catchError((Object _) {});
    _notify();
  });

  Future<String> exportBackup() async {
    await _writes.catchError((Object _) {});
    _requireAdmin();
    final libraries = <String, Object?>{};
    for (final profile in _profiles) {
      Future<Object?> read(String name) =>
          _readFile(_userFile(profile.id, name));
      final settings = _map(await read('settings'));
      final favorites = _list(await read('favorites'));
      final ids = {
        for (final row in favorites)
          if (row is Map) row['id'],
      };
      libraries[profile.id] = {
        'history': _list(await read('history')),
        'favorites': favorites,
        'followStates': _map(await read('followStates'))
          ..removeWhere((id, _) => !ids.contains(id)),
        'source': settings['source'] is String ? settings['source'] : '',
        'playback': _map(settings['playback']),
        'downloadPreferences': _map(settings['downloadPreferences']),
        'recentSearches': _list(settings['recentSearches']),
      };
    }
    final content = jsonEncode({
      'schema': 1,
      'app': 'zhenguojian',
      'profiles': _profiles.map((profile) => profile.toJson()).toList(),
      'displayMode': displayMode,
      'themeMode': themeMode,
      'followSystemColor': followSystemColor,
      'themeColor': themeColor.name,
      'forceLogin': forceLogin,
      'checkUpdates': checkUpdates,
      'shareUsage': shareUsage,
      'exportAfterDownload': autoExport,
      'deleteAfterAutoExport': deleteAfterAutoExport,
      'deleteAfterManualExport': deleteAfterManualExport,
      'televisionKeyboard': televisionKeyboard,
      'libraries': libraries,
    });
    if (utf8.encode(content).length > 8 * 1024 * 1024) {
      throw StateError('备份超过 8 MiB 保存上限，请先整理记录；尚未写出备份文件');
    }
    validateBackup(content);
    return content;
  }

  Map<String, dynamic> validateBackup(String content) {
    if (utf8.encode(content).length > 8 * 1024 * 1024) {
      throw const FormatException('备份文件过大');
    }
    final data = jsonDecode(content) as Map<String, dynamic>;
    if (data['schema'] != 1 || data['app'] != 'zhenguojian') {
      throw const FormatException('不支持的备份格式');
    }
    final profiles = _readProfiles(data['profiles']);
    if (data.containsKey('themeMode') &&
        !_themeModes.contains(data['themeMode'])) {
      throw const FormatException('备份主题设置无效');
    }
    if (data.containsKey('followSystemColor') &&
            data['followSystemColor'] is! bool ||
        data.containsKey('themeColor') &&
            ThemeColor.byName(data['themeColor']) == null) {
      throw const FormatException('备份主题色设置无效');
    }
    if (data.containsKey('forceLogin') && data['forceLogin'] is! bool) {
      throw const FormatException('备份登录设置无效');
    }
    if (_backupFlags.any(
      (key) => data.containsKey(key) && data[key] is! bool,
    )) {
      throw const FormatException('备份设置无效');
    }
    final libraries = data['libraries'] as Map;
    for (final profile in profiles) {
      final library = libraries[profile.id] as Map;
      final history = library['history'] as List,
          favorites = library['favorites'] as List;
      if (history.length > 300 || favorites.length > 20000) {
        throw const FormatException('备份记录过多');
      }
      for (final row in history) {
        WatchEntry.fromJson(Map<String, dynamic>.from(row as Map));
      }
      for (final row in favorites) {
        Drama.fromJson(Map<String, dynamic>.from(row as Map));
      }
      final states = library['followStates'] as Map? ?? {};
      final favoriteIds = favorites.map((row) => (row as Map)['id']).toSet();
      if (states.length > 20000 ||
          states.keys.any((id) => id is! String || !favoriteIds.contains(id))) {
        throw const FormatException('备份追剧状态无效');
      }
      for (final row in states.values) {
        FollowState.fromJson(Map<String, dynamic>.from(row as Map));
      }
      if (library['source'] is! String) {
        throw const FormatException('备份设置无效');
      }
      PlaybackPreferences.fromJson(
        Map<String, dynamic>.from(library['playback'] as Map? ?? {}),
      );
      DownloadPreferences.fromJson(
        Map<String, dynamic>.from(library['downloadPreferences'] as Map? ?? {}),
      );
      final searches = library['recentSearches'] as List? ?? [];
      if (searches.length > 20 ||
          searches.any(
            (value) => value is! String || value.runes.length > 80,
          )) {
        throw const FormatException('备份搜索记录无效');
      }
    }
    return data;
  }

  Future<void> _installBackup(Map<String, dynamic> data) async {
    final profiles = _readProfiles(data['profiles']);
    final libraries = data['libraries'] as Map;
    final contents = <String, Object?>{
      _settingsFile: {
        ..._settings,
        'displayMode': _displayModes.contains(data['displayMode'])
            ? data['displayMode']
            : 'auto',
        if (data['themeMode'] case final String mode) 'themeMode': mode,
        if (data['followSystemColor'] case final bool follow)
          'followSystemColor': follow,
        if (ThemeColor.byName(data['themeColor']) case final color?)
          'themeColor': color.name,
        for (final key in _backupFlags)
          if (data[key] case final bool value) key: value,
      },
      _usersFile: {
        'profiles': [for (final profile in profiles) profile.toJson()],
        'active': profiles.firstWhere((profile) => profile.admin).id,
        if (data['forceLogin'] case final bool force) 'forceLogin': force,
      },
    };
    for (final profile in profiles) {
      final library = libraries[profile.id] as Map;
      contents.addAll({
        _userFile(profile.id, 'history'): library['history'],
        _userFile(profile.id, 'favorites'): library['favorites'],
        _userFile(profile.id, 'followStates'): library['followStates'] ?? {},
        _userFile(profile.id, 'followChecks'): const {},
        _userFile(profile.id, 'settings'): {
          'source': library['source'],
          'playback': library['playback'] ?? {},
          'downloadPreferences': library['downloadPreferences'] ?? {},
          'recentSearches': library['recentSearches'] ?? [],
        },
      });
    }
    try {
      await files.replaceAll(contents);
    } catch (_) {
      throw StateError('未能保存，原有记录已保留；请检查存储空间和权限后重试');
    }
    await _removeLegacy();
    await _initialize();
    _epoch++;
    _notify();
  }

  Future<void> importBackup(String content) => _queue(() async {
    _requireAdmin();
    await _installBackup(validateBackup(content));
  });

  Future<LocalProfile?> _originalAdmin() async {
    Object? records;
    try {
      final users = await files.read(_usersFile);
      if (users is Map) records = users['profiles'];
    } catch (_) {}
    if (records == null) {
      try {
        final raw = preferences.getString(LocalSnapshot.storageKey);
        final values = raw == null
            ? null
            : (jsonDecode(raw) as Map)['values'] as Map;
        records = jsonDecode(
          (values?['profiles'] as String?) ??
              preferences.getString('profiles') ??
              '[]',
        );
      } catch (_) {}
    }
    for (final row in _list(records)) {
      if (row is Map && row['admin'] == true) {
        try {
          return LocalProfile.fromJson(Map<String, dynamic>.from(row));
        } catch (_) {}
      }
    }
    return null;
  }

  Future<void> recoverBackup(String content, {required String pin}) =>
      _queue(() async {
        if (_configurationError == null) throw StateError('请从设置中恢复备份');
        final data = validateBackup(content);
        final originalAdmin = await _originalAdmin();
        final backupAdmin = _readProfiles(
          data['profiles'],
        ).firstWhere((profile) => profile.admin);
        final verifier = originalAdmin?.protected == true
            ? originalAdmin!
            : backupAdmin;
        if (!verifier.protected) throw StateError('无法验证管理员身份，请使用包含管理员密码的备份恢复');
        await _checkPin(verifier, pin);
        await _installBackup(data);
      });

  Future<String> exportRecoveryData() async {
    if (_configurationError == null) throw StateError('请从设置中导出备份');
    return jsonEncode({
      'app': 'zhenguojian',
      'recovery': true,
      'values': {
        for (final key in preferences.getKeys())
          if (LocalSnapshot.legacy(key)) key: preferences.get(key),
      },
      'files': await files.dump(),
    });
  }

  Future<void> reload() => _queue(() async {
    final previousProfiles = _configurationError == null
        ? jsonEncode(_profiles)
        : null;
    final previousId = _current;
    final previousLocked = locked;
    await preferences.reload();
    await _initialize();
    if (_configurationError == null &&
        previousProfiles == jsonEncode(_profiles) &&
        previousId == _current) {
      _locked = previousLocked;
    } else {
      _epoch++;
    }
    _notify();
  });

  Future<void> _queue(Future<void> Function() action) {
    _writes = _writes.catchError((Object _) {}).then((_) => action());
    return _writes;
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }
}
