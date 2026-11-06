import 'dart:io';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import '../../app_device.dart';
import '../../core_bridge.dart';
import '../../merge_queue.dart';
import '../../models.dart';

Future<bool> enqueueMerges(
  BuildContext context,
  MergeQueue merges,
  List<List<DownloadJob>> groups,
) async {
  final messenger = ScaffoldMessenger.of(context);
  void say(String text) =>
      messenger.showSnackBar(SnackBar(content: Text(text)));
  final ready = [
    for (final jobs in groups)
      if (jobs.length >= 2) jobs,
  ];
  final skipped = groups.length - ready.length;
  if (ready.isEmpty) {
    say('至少要有 2 集才能合并');
    return false;
  }
  final List<String?>? files;
  try {
    files = await _destinations(ready);
  } catch (error) {
    say('$error');
    return false;
  }
  if (files == null) return false;
  for (final (index, jobs) in ready.indexed) {
    merges.add(jobs, file: files[index]);
  }
  say(
    '已加入合并队列${skipped > 0 ? '，$skipped 部不足 2 集已跳过' : ''}'
    '，可在下载 › 合并查看进度',
  );
  return true;
}

Future<List<String?>?> _destinations(List<List<DownloadJob>> groups) async {
  if (Platform.isAndroid) {
    if (!await AppDevice.requestVideoExport()) {
      throw AppFailure('未获得存储权限，无法保存合并视频');
    }
    return [for (final _ in groups) null];
  }
  if (Platform.isIOS) return [for (final _ in groups) null];
  if (groups.length == 1) {
    final saved = await FilePicker.saveFile(
      fileName: mergeJobsFileName(groups.single),
      bytes: Uint8List(0),
      mimeType: 'video/mp4',
      dialogTitle: '保存合并视频',
    );
    return saved == null ? null : [saved.toFilePath()];
  }
  final directory = await FilePicker.getDirectoryPath(
    dialogTitle: '选择合并视频的保存文件夹',
  );
  if (directory == null) return null;
  return [
    for (final jobs in groups)
      unusedMergeFile(directory, mergeJobsFileName(jobs)),
  ];
}
