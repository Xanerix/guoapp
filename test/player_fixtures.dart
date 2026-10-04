import 'dart:async';

import 'package:duanju_app/models.dart';

import 'fixtures.dart';

class RouteRepository extends FixtureRepository {
  int primaryCalls = 0;
  int fallbackCalls = 0;
  final requestedQualities = <int>[];
  final requestedEpisodes = <int>[];
  bool broken = false;
  bool deferFallback = false;
  final active = <String>{};
  Completer<PlaybackPlan>? pending;

  PlaybackPlan plan(bool alternate) {
    final session = 'route-${primaryCalls + fallbackCalls}';
    active.add(session);
    return PlaybackPlan(
      url: 'https://media.test/${broken ? 'broken' : 'working'}-$session.mp4',
      session: session,
      routeIndex: alternate ? 1 : 0,
      routeCount: 2,
      quality: 1080,
      qualities: const [1080, 720],
    );
  }

  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    primaryCalls++;
    requestedQualities.add(quality);
    requestedEpisodes.add(episode.number);
    return plan(false);
  }

  @override
  Future<PlaybackPlan> fallback(PlaybackPlan current) async {
    fallbackCalls++;
    if (deferFallback) {
      pending = Completer<PlaybackPlan>();
      return pending!.future;
    }
    return plan(true);
  }

  @override
  Future<void> release(String session) async {
    active.remove(session);
  }
}
