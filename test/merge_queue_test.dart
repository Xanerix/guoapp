import 'dart:async';

import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/download_merge.dart';
import 'package:duanju_app/media_pipeline.dart';
import 'package:duanju_app/merge_queue.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';

class _LocalRepository extends FakeRepository {
  final leases = <String>[];
  Object? leaseError;

  @override
  Future<PlaybackPlan?> localPlayback(Drama drama, Episode episode) async =>
      PlaybackPlan(
        url: '/videos/${drama.id}/${episode.number}.mp4',
        local: true,
      );

  @override
  Future<int> workLease(String id, String command) async {
    if (command == 'start' && leaseError != null) throw leaseError!;
    leases.add('$id:$command');
    return 0;
  }
}

class _Executor implements MediaExecutor {
  final runs = <Completer<void>>[];
  final outputs = <String>[];

  @override
  Future<MediaProbe> probe(String file) async => MediaProbe({
    'format': {'duration': '12'},
    'streams': [
      {'codec_type': 'video', 'codec_name': 'h264', 'height': 1080},
    ],
  });

  @override
  Future<void> run(
    List<String> arguments, {
    double duration = 0,
    void Function(double)? progress,
  }) {
    outputs.add(arguments.last);
    progress?.call(.5);
    final run = Completer<void>();
    runs.add(run);
    return run.future;
  }

  @override
  Future<void> cancel() async {
    for (final run in runs) {
      if (!run.isCompleted) run.completeError(AppFailure('ffmpeg 已取消'));
    }
  }
}

Future<void> _settle() => Future<void>.delayed(const Duration(milliseconds: 5));

void main() {
  final first = drama('a', title: '剧甲');
  final second = drama('b', title: '剧乙');

  late _LocalRepository repository;
  late _Executor executor;
  late MergeQueue queue;
  late List<MergeTask> finished;

  setUp(() {
    repository = _LocalRepository();
    executor = _Executor();
    finished = [];
    queue = MergeQueue(
      repository,
      executor: () => executor,
      save: (merge, task) async {
        await merge.write('/out/${task.id}.mp4');
        return '已保存';
      },
    )..onFinished = finished.add;
  });

  tearDown(() => queue.dispose());

  Future<void> waitForRuns(int count) async {
    for (var i = 0; i < 200 && executor.runs.length < count; i++) {
      await _settle();
    }
    expect(executor.runs, hasLength(count));
  }

  test('tasks run one after another inside a single lease', () async {
    queue.add([downloadJob(first, 2), downloadJob(first, 1)]);
    queue.add([downloadJob(second, 1), downloadJob(second, 2)]);
    await waitForRuns(1);
    expect(queue.tasks.map((task) => task.state), [
      MergeState.running,
      MergeState.queued,
    ]);
    expect(queue.tasks.first.progress, .5);

    executor.runs.first.complete();
    await waitForRuns(2);
    expect(queue.tasks.first.state, MergeState.done);
    expect(queue.tasks.first.message, '已保存');

    executor.runs.last.complete();
    for (var i = 0; i < 200 && repository.leases.length < 2; i++) {
      await _settle();
    }
    expect(executor.outputs, ['/out/1.mp4', '/out/2.mp4']);
    expect(repository.leases, ['merge:start', 'merge:end']);
    expect(finished.map((task) => task.fileName), [
      '剧甲 第1-2集.mp4',
      '剧乙 第1-2集.mp4',
    ]);
    expect(queue.active, 0);
  });

  test('a waiting task is dropped and a running task is stopped', () async {
    queue.add([downloadJob(first, 1), downloadJob(first, 2)]);
    queue.add([downloadJob(second, 1), downloadJob(second, 2)]);
    await waitForRuns(1);

    queue.cancel(queue.tasks.last);
    expect(queue.tasks.last.state, MergeState.cancelled);

    queue.cancel(queue.tasks.first);
    for (var i = 0; i < 200 && queue.active > 0; i++) {
      await _settle();
    }
    expect(queue.tasks.first.state, MergeState.cancelled);
    expect(executor.runs, hasLength(1));

    queue.clearFinished();
    expect(queue.tasks, isEmpty);
  });

  test('a refused lease fails the waiting tasks with its reason', () async {
    repository.leaseError = AppFailure('正在迁移下载目录，请稍后再合并');
    queue.add([downloadJob(first, 1), downloadJob(first, 2)]);
    for (var i = 0; i < 200 && queue.active > 0; i++) {
      await _settle();
    }
    expect(queue.tasks.single.state, MergeState.failed);
    expect(queue.tasks.single.message, contains('正在迁移下载目录'));
    expect(executor.runs, isEmpty);
  });

  test('remaining time is estimated only once progress is measurable', () {
    expect(mergeRemaining(null, const Duration(seconds: 10)), isNull);
    expect(mergeRemaining(.01, const Duration(seconds: 10)), isNull);
    expect(mergeRemaining(.5, const Duration(seconds: 1)), isNull);
    expect(
      mergeRemaining(.25, const Duration(seconds: 10)),
      const Duration(seconds: 30),
    );
  });

  test('file names follow the sorted episode numbers', () {
    expect(
      mergeJobsFileName([downloadJob(first, 3), downloadJob(first, 1)]),
      mergeFileName('剧甲', [1, 3]),
    );
  });
}
