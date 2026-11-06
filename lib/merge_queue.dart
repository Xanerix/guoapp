import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:path/path.dart' as path;
import 'package:path_provider/path_provider.dart';

import 'app_device.dart';
import 'background_downloads.dart';
import 'core_bridge.dart';
import 'diary_service.dart';
import 'download_merge.dart';
import 'media_pipeline.dart';
import 'models.dart';

enum MergeState { queued, running, done, failed, cancelled }

typedef MergeSave =
    Future<String> Function(DownloadMerge merge, MergeTask task);

class MergeTask {
  MergeTask._(this.id, List<DownloadJob> jobs, this.file)
    : jobs = List.unmodifiable(
        List.of(jobs)
          ..sort((a, b) => a.episode.number.compareTo(b.episode.number)),
      );

  final int id;
  final List<DownloadJob> jobs;
  final String? file;
  MergeState state = MergeState.queued;
  String status = '等待合并';
  double? progress;
  Duration? remaining;
  String message = '';

  Drama get drama => jobs.first.drama;
  String get fileName => mergeJobsFileName(jobs);
  bool get finished => state.index >= MergeState.done.index;
}

class MergeQueue extends ChangeNotifier {
  MergeQueue(
    this.repository, {
    MediaExecutor Function()? executor,
    MergeSave? save,
    DateTime Function()? now,
  }) : _executor = executor ?? FFmpegExecutor.new,
       _save = save ?? saveMerge,
       _now = now ?? DateTime.now;

  final AppRepository repository;
  final MediaExecutor Function() _executor;
  final MergeSave _save;
  final DateTime Function() _now;
  final tasks = <MergeTask>[];
  ValueChanged<MergeTask>? onFinished;
  DownloadMerge? _merge;
  int _nextId = 0;
  bool _running = false, _disposed = false;

  int get active => tasks.where((task) => !task.finished).length;

  void add(List<DownloadJob> jobs, {String? file}) {
    tasks.add(MergeTask._(++_nextId, jobs, file));
    _notify();
    unawaited(_pump());
  }

  void cancel(MergeTask task) {
    if (task.state == MergeState.running) {
      _merge?.cancel();
    } else if (task.state == MergeState.queued) {
      task
        ..state = MergeState.cancelled
        ..message = '已取消合并';
      _notify();
    }
  }

  void clearFinished() {
    tasks.removeWhere((task) => task.finished);
    _notify();
  }

  Future<void> _pump() async {
    if (_running || _disposed) return;
    _running = true;
    try {
      try {
        await BackgroundDownloads.ensureStarted();
      } catch (_) {}
      try {
        await repository.workLease('merge', 'start');
      } catch (error) {
        for (final task in tasks) {
          if (task.state != MergeState.queued) continue;
          task
            ..state = MergeState.failed
            ..message = '$error';
          onFinished?.call(task);
        }
        _notify();
        return;
      }
      try {
        while (!_disposed) {
          final task = tasks
              .where((task) => task.state == MergeState.queued)
              .firstOrNull;
          if (task == null) break;
          await _run(task);
        }
      } finally {
        try {
          await repository.workLease('merge', 'end');
        } catch (_) {}
      }
    } finally {
      _running = false;
    }
    if (tasks.any((task) => task.state == MergeState.queued)) {
      unawaited(_pump());
    }
  }

  Future<void> _run(MergeTask task) async {
    final merge = _merge = DownloadMerge(repository, executor: _executor());
    var phase = '';
    var phaseStart = _now();
    void sync() {
      if (merge.status != phase) {
        phase = merge.status;
        phaseStart = _now();
      }
      task
        ..status = merge.status
        ..progress = merge.progress
        ..remaining = mergeRemaining(
          merge.progress,
          _now().difference(phaseStart),
        );
      _notify();
    }

    task.state = MergeState.running;
    merge.addListener(sync);
    sync();
    try {
      await merge.prepare(task.jobs);
      task.message = await _save(merge, task);
      task.state = MergeState.done;
    } catch (error) {
      if (merge.cancelled) {
        task
          ..state = MergeState.cancelled
          ..message = '已取消合并';
      } else {
        task
          ..state = MergeState.failed
          ..message = '$error';
        DiaryService.add('[下载] 合并「${task.fileName}」失败：$error');
      }
    } finally {
      merge
        ..removeListener(sync)
        ..dispose();
      _merge = null;
      task
        ..progress = null
        ..remaining = null;
      _notify();
      if (!_disposed) onFinished?.call(task);
    }
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _merge?.cancel();
    super.dispose();
  }
}

String mergeJobsFileName(List<DownloadJob> jobs) {
  final numbers = [for (final job in jobs) job.episode.number]..sort();
  return mergeFileName(jobs.first.drama.title, numbers);
}

Duration? mergeRemaining(double? progress, Duration elapsed) {
  if (progress == null ||
      progress < .03 ||
      progress >= 1 ||
      elapsed < const Duration(seconds: 2)) {
    return null;
  }
  return elapsed * ((1 - progress) / progress);
}

Future<String> saveMerge(DownloadMerge merge, MergeTask task) async {
  if (task.file case final file?) {
    await merge.write(file, file: file);
    return '已保存到 $file';
  }
  if (Platform.isIOS) {
    final directory = Directory(
      path.join((await getApplicationDocumentsDirectory()).path, '合并视频'),
    );
    await directory.create(recursive: true);
    final file = unusedMergeFile(directory.path, merge.fileName);
    await merge.write(file, file: file);
    return '已保存到「文件」App 里本应用的「合并视频」文件夹';
  }
  if (!Platform.isAndroid) throw AppFailure('没有选择保存位置');
  final folder = safeFileName(task.drama.title);
  final temporary = await (await getTemporaryDirectory()).createTemp('merge');
  try {
    final file = path.join(temporary.path, merge.fileName);
    await merge.write(file, file: file);
    await AppDevice.exportVideo(file, folder, merge.fileName);
  } finally {
    try {
      await temporary.delete(recursive: true);
    } catch (_) {}
  }
  return '已保存到 Movies/$folder';
}

String unusedMergeFile(String directory, String name) {
  final stem = path.basenameWithoutExtension(name);
  var file = path.join(directory, name);
  for (var attempt = 2; File(file).existsSync(); attempt++) {
    file = path.join(directory, '$stem ($attempt).mp4');
  }
  return file;
}
