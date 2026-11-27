import 'dart:math';

import 'models.dart';

class FollowState {
  const FollowState({
    this.knownEpisodes = 0,
    this.readEpisodes,
    this.seriesSeasons = const {},
    this.seasonsSeeded = false,
  });

  final int knownEpisodes;
  final int? readEpisodes;
  final Map<String, SeriesSeasonNotice> seriesSeasons;
  final bool seasonsSeeded;

  int get newEpisodes =>
      readEpisodes == null ? 0 : max(0, knownEpisodes - readEpisodes!);
  List<SeriesSeasonNotice> get unreadSeasons {
    final list = [
      for (final notice in seriesSeasons.values)
        if (!notice.read) notice,
    ]..sort((a, b) => a.season.compareTo(b.season));
    return list;
  }

  bool get hasUpdates => newEpisodes > 0 || unreadSeasons.isNotEmpty;

  String? get updateBadge {
    final parts = [
      if (newEpisodes > 0) '$newEpisodes 集更新',
      if (unreadSeasons.isNotEmpty)
        unreadSeasons.length == 1
            ? '第${unreadSeasons.first.season}${unreadSeasons.first.unit}已上线'
            : '更新${unreadSeasons.length}季',
    ];
    return parts.isEmpty ? null : parts.join(' · ');
  }

  FollowState copyWith({
    int? knownEpisodes,
    int? readEpisodes,
    Map<String, SeriesSeasonNotice>? seriesSeasons,
    bool? seasonsSeeded,
  }) => FollowState(
    knownEpisodes: knownEpisodes ?? this.knownEpisodes,
    readEpisodes: readEpisodes ?? this.readEpisodes,
    seriesSeasons: seriesSeasons ?? this.seriesSeasons,
    seasonsSeeded: seasonsSeeded ?? this.seasonsSeeded,
  );

  factory FollowState.initial(Drama drama) {
    final count = max(0, drama.episodes);
    return FollowState(
      knownEpisodes: count,
      readEpisodes: count > 0 ? count : null,
    );
  }

  FollowState observe(Drama drama) {
    final count = max(knownEpisodes, drama.episodes);
    return FollowState(
      knownEpisodes: count,
      readEpisodes: readEpisodes ?? (count > 0 ? count : null),
      seriesSeasons: seriesSeasons,
      seasonsSeeded: seasonsSeeded,
    );
  }

  FollowState observeSeasons({
    required String anchorId,
    required List<DramaSeason> seasons,
    required Set<String> favoriteIds,
    required Set<String> watchedIds,
    bool? baseline,
  }) {
    final anchor = seasons
        .where((season) => season.drama.id == anchorId)
        .firstOrNull;
    var updated = seriesSeasons;
    if (anchor == null) {
      return seasonsSeeded ? this : copyWith(seasonsSeeded: true);
    }
    final seed = baseline ?? !seasonsSeeded;
    for (final id in updated.keys.toList()) {
      if (id == anchorId || favoriteIds.contains(id)) {
        updated = Map.of(updated)..remove(id);
      } else if (watchedIds.contains(id) && !updated[id]!.read) {
        updated = Map.of(updated)..[id] = updated[id]!.markRead();
      }
    }
    for (final season in seasons) {
      final drama = season.drama;
      if (drama.id == anchorId ||
          season.number <= anchor.number ||
          season.upcoming ||
          favoriteIds.contains(drama.id)) {
        continue;
      }
      final previous = updated[drama.id];
      final title = drama.title.trim().isEmpty ? season.label : drama.title;
      final notice = SeriesSeasonNotice(
        id: drama.id,
        title: title,
        season: season.number,
        unit: '季',
        read: previous?.read ?? (seed || watchedIds.contains(drama.id)),
      );
      if (previous?.title == notice.title &&
          previous?.season == notice.season &&
          previous?.unit == notice.unit &&
          previous?.read == notice.read) {
        continue;
      }
      updated = Map.of(updated)..[notice.id] = notice;
    }
    if (seasonsSeeded && identical(updated, seriesSeasons)) return this;
    return copyWith(seriesSeasons: updated, seasonsSeeded: true);
  }

  FollowState markRead() => FollowState(
    knownEpisodes: knownEpisodes,
    readEpisodes: knownEpisodes > 0 ? knownEpisodes : null,
    seriesSeasons: {
      for (final entry in seriesSeasons.entries)
        entry.key: entry.value.markRead(),
    },
    seasonsSeeded: seasonsSeeded,
  );

  FollowState markSeriesSeasonRead(String id) =>
      seriesSeasons[id]?.read != false
      ? this
      : copyWith(
          seriesSeasons: Map.of(seriesSeasons)
            ..[id] = seriesSeasons[id]!.markRead(),
        );

  FollowState removeSeriesSeason(String id) => !seriesSeasons.containsKey(id)
      ? this
      : copyWith(seriesSeasons: Map.of(seriesSeasons)..remove(id));

  Map<String, dynamic> toJson() {
    final value = <String, dynamic>{
      'knownEpisodes': knownEpisodes,
      'readEpisodes': readEpisodes,
    };
    if (seasonsSeeded) value['seasonsSeeded'] = true;
    if (seriesSeasons.isNotEmpty) {
      value['seriesSeasons'] = {
        for (final entry in seriesSeasons.entries)
          entry.key: entry.value.toJson(),
      };
    }
    return value;
  }

  factory FollowState.fromJson(Map<String, dynamic> json) {
    final known = json['knownEpisodes'];
    final read = json['readEpisodes'];
    final seeded = json['seasonsSeeded'];
    final rawSeasons = json['seriesSeasons'];
    final seasons = <String, SeriesSeasonNotice>{};
    if (rawSeasons != null) {
      if (rawSeasons is! Map || rawSeasons.length > 200) {
        throw const FormatException('系列剧提醒无效');
      }
      for (final entry in rawSeasons.entries) {
        if (entry.key is! String) throw const FormatException('系列剧提醒无效');
        if (entry.value is! Map) throw const FormatException('系列剧提醒无效');
        final notice = SeriesSeasonNotice.fromJson(
          Map<String, dynamic>.from(entry.value as Map),
        );
        if (entry.key != notice.id || seasons.containsKey(notice.id)) {
          throw const FormatException('系列剧提醒无效');
        }
        seasons[notice.id] = notice;
      }
    }
    if (known is! int ||
        known < 0 ||
        known > 1000000 ||
        read != null && (read is! int || read < 0 || read > known) ||
        seeded != null && seeded is! bool) {
      throw const FormatException('追剧状态无效');
    }
    return FollowState(
      knownEpisodes: known,
      readEpisodes: read as int?,
      seriesSeasons: seasons,
      seasonsSeeded: seeded == true,
    );
  }
}

class SeriesSeasonNotice {
  const SeriesSeasonNotice({
    required this.id,
    required this.title,
    required this.season,
    required this.unit,
    this.read = false,
  });

  final String id;
  final String title;
  final int season;
  final String unit;
  final bool read;

  SeriesSeasonNotice markRead() => read
      ? this
      : SeriesSeasonNotice(
          id: id,
          title: title,
          season: season,
          unit: unit,
          read: true,
        );

  Map<String, dynamic> toJson() => {
    'id': id,
    'title': title,
    'season': season,
    'unit': unit,
    'read': read,
  };

  factory SeriesSeasonNotice.fromJson(Map<String, dynamic> json) {
    final id = json['id'], title = json['title'], season = json['season'];
    final unit = json['unit'], read = json['read'];
    final colon = id is String ? id.indexOf(':') : -1;
    if (id is! String ||
        colon <= 0 ||
        colon == id.length - 1 ||
        !validSourceId(id.substring(0, colon)) ||
        id.length > 512 ||
        title is! String ||
        title.trim().isEmpty ||
        title.length > 300 ||
        season is! int ||
        season < 1 ||
        season > 200 ||
        unit is! String ||
        !{'季', '部'}.contains(unit) ||
        read is! bool) {
      throw const FormatException('系列剧提醒无效');
    }
    return SeriesSeasonNotice(
      id: id,
      title: title,
      season: season,
      unit: unit,
      read: read,
    );
  }
}

class FollowCheck {
  const FollowCheck(
    this.checkedAt, {
    this.failures = 0,
    this.seasonsCheckedAt,
    this.seasonFailures = 0,
  });

  final DateTime checkedAt;
  final int failures;
  final DateTime? seasonsCheckedAt;
  final int seasonFailures;

  FollowCheck withDetail(DateTime at, {int failures = 0}) => FollowCheck(
    at,
    failures: failures,
    seasonsCheckedAt: seasonsCheckedAt,
    seasonFailures: seasonFailures,
  );

  FollowCheck withSeasons(DateTime at, {int failures = 0}) => FollowCheck(
    checkedAt,
    failures: this.failures,
    seasonsCheckedAt: at,
    seasonFailures: failures,
  );

  List<int> toJson() => [
    checkedAt.millisecondsSinceEpoch,
    failures,
    if (seasonsCheckedAt != null || seasonFailures > 0) ...[
      seasonsCheckedAt?.millisecondsSinceEpoch ?? 0,
      seasonFailures,
    ],
  ];

  factory FollowCheck.fromJson(Object? json) {
    if (json is! List ||
        (json.length != 2 && json.length != 4) ||
        json[0] is! int ||
        json[1] is! int ||
        (json[1] as int) < 0) {
      throw const FormatException('追剧检查记录无效');
    }
    DateTime? seasonsCheckedAt;
    var seasonFailures = 0;
    if (json.length == 4) {
      if (json[2] is! int || json[3] is! int || (json[3] as int) < 0) {
        throw const FormatException('追剧检查记录无效');
      }
      final timestamp = json[2] as int;
      if (timestamp > 0) {
        seasonsCheckedAt = DateTime.fromMillisecondsSinceEpoch(timestamp);
      }
      seasonFailures = json[3] as int;
    }
    return FollowCheck(
      DateTime.fromMillisecondsSinceEpoch(json[0] as int),
      failures: json[1] as int,
      seasonsCheckedAt: seasonsCheckedAt,
      seasonFailures: seasonFailures,
    );
  }
}

int resumeEpisodeIndex(List<Episode> episodes, WatchEntry? watch) {
  if (episodes.isEmpty || watch == null) return 0;
  final current = episodes.indexWhere((item) => item.number == watch.episode);
  if (current >= 0 && !watch.finished) return current;
  final next = episodes.indexWhere((item) => item.number > watch.episode);
  if (next >= 0) return next;
  return current >= 0 ? current : episodes.length - 1;
}
