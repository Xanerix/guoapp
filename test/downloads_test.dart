import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/models.dart';
import 'package:duanju_app/playback_loader.dart';
import 'package:flutter_test/flutter_test.dart';

import 'fixtures.dart';

class DownloadRepository extends FixtureRepository {
  int localCalls = 0;
  int onlineCalls = 0;
  bool missing = false;

  Episode episode(int number) => Episode({
    'id': '$number',
    'currentEpisode': number,
    'vip': number == 3,
  }, number);

  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) async {
    localCalls++;
    return missing
        ? null
        : const PlaybackPlan(url: '/synthetic/local.mp4', local: true);
  }

  @override
  Future<PlaybackPlan> resolve(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    final plan = await localPlayback(drama, episode);
    if (plan == null) throw AppFailure('本地文件缺失', code: 'local_media');
    return plan;
  }

  @override
  Future<PlaybackPlan> resolveOnline(
    Drama drama,
    Episode episode, {
    int quality = 0,
  }) async {
    onlineCalls++;
    return const PlaybackPlan(url: 'https://synthetic.test/online.mp4');
  }

  @override
  Future<PlaybackPlan> fallback(PlaybackPlan current) async {
    throw AppFailure('本地视频不应该自动切线');
  }
}

void main() {
  test(
    'local-only loader never falls through to online; explicit online bypasses it',
    () async {
      final repository = DownloadRepository()..missing = true;
      final loader = PlaybackLoader(repository);
      await expectLater(
        loader.load(
          FixtureRepository.free,
          repository.episode(1),
          localOnly: true,
        ),
        throwsA(
          isA<AppFailure>().having(
            (error) => error.code,
            'code',
            'local_media',
          ),
        ),
      );
      expect(repository.onlineCalls, 0);
      final plan = await loader.load(
        FixtureRepository.free,
        repository.episode(1),
        localOnly: true,
        online: true,
      );
      expect(plan!.local, isFalse);
      expect(repository.localCalls, 1);
      expect(repository.onlineCalls, 1);
      await loader.close();
    },
  );
}
