import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:path/path.dart' as path;
import 'package:path_provider/path_provider.dart';
import 'package:url_launcher/url_launcher.dart';

import '../app_device.dart';
import '../app_update.dart';
import '../core_bridge.dart';
import '../local_store.dart';
import 'app_scope.dart';
import 'widgets/focus_ring.dart';

var _checked = false;
const _manualCooldown = Duration(seconds: 30);
DateTime? _lastManualCheck;
AppUpdate? _lastManualUpdate;

enum _UpdateChoice { mute, skip, dismiss, download }

Future<void> runStartupChecks(BuildContext context, LocalStore store) async {
  if (_checked) return;
  _checked = true;
  final repository = AppScope.of(context).repository;
  final String current;
  final AppUpdate update;
  try {
    current = await currentAppVersion();
    if (store.shareUsage) {
      unawaited(repository.reportUsage(current).catchError((_) {}));
    }
    if (!store.checkUpdates) return;
    final latest = await repository.checkUpdate(current);
    if (latest == null) return;
    update = latest;
  } catch (_) {
    return;
  }
  if (update.version == store.ignoredUpdate || !context.mounted) return;
  final choice = await _askUpdate(context, current, update, manual: false);
  switch (choice) {
    case _UpdateChoice.mute:
      await store.setCheckUpdates(false);
    case _UpdateChoice.skip:
      await store.setIgnoredUpdate(update.version);
    case _UpdateChoice.download:
      if (context.mounted) await _update(context, update);
    case _UpdateChoice.dismiss || null:
      return;
  }
}

Future<void> checkUpdateManually(BuildContext context) async {
  final repository = AppScope.of(context).repository;
  final messenger = ScaffoldMessenger.of(context);
  final String current;
  final AppUpdate? update;
  try {
    current = await currentAppVersion();
    final last = _lastManualCheck;
    if (last != null && DateTime.now().difference(last) < _manualCooldown) {
      update = _lastManualUpdate;
    } else {
      update = await repository.checkUpdate(current);
      _lastManualCheck = DateTime.now();
      _lastManualUpdate = update;
    }
  } catch (_) {
    messenger.showSnackBar(const SnackBar(content: Text('检查更新失败，请稍后再试')));
    return;
  }
  if (update == null) {
    messenger.showSnackBar(const SnackBar(content: Text('已是最新版本')));
    return;
  }
  if (!context.mounted) return;
  final choice = await _askUpdate(context, current, update, manual: true);
  if (choice == _UpdateChoice.download && context.mounted) {
    await _update(context, update);
  }
}

Future<void> _update(BuildContext context, AppUpdate update) async {
  if (defaultTargetPlatform == TargetPlatform.android) {
    final repository = AppScope.of(context).repository;
    final file = await showDialog<String>(
      context: context,
      barrierDismissible: false,
      builder: (_) => _UpdateDownloadDialog(
        repository: repository,
        version: update.version,
      ),
    );
    if (file == '') return;
    if (file != null && await AppDevice.installPackage(file)) return;
    if (!context.mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('应用内更新失败，已打开发布页')));
  }
  if (context.mounted) await _openRelease(context, update.url);
}

class _UpdateDownloadDialog extends StatefulWidget {
  const _UpdateDownloadDialog({
    required this.repository,
    required this.version,
  });

  final AppRepository repository;
  final String version;

  @override
  State<_UpdateDownloadDialog> createState() => _UpdateDownloadDialogState();
}

class _UpdateDownloadDialogState extends State<_UpdateDownloadDialog> {
  var _progress = const UpdateDownload();
  var _cancelled = false;

  @override
  void initState() {
    super.initState();
    unawaited(_download());
  }

  Future<void> _download() async {
    try {
      final cache = await getTemporaryDirectory();
      await widget.repository.startUpdateDownload(
        widget.version,
        path.join(cache.path, 'update'),
      );
      while (true) {
        await Future<void>.delayed(const Duration(milliseconds: 300));
        if (!mounted || _cancelled) return;
        final progress = await widget.repository.updateDownloadStatus();
        if (!mounted || _cancelled) return;
        if (progress.error.isNotEmpty) break;
        if (progress.path.isNotEmpty) {
          Navigator.pop(context, progress.path);
          return;
        }
        setState(() => _progress = progress);
      }
    } catch (_) {}
    if (mounted && !_cancelled) Navigator.pop(context);
  }

  void _cancel() {
    if (_cancelled) return;
    _cancelled = true;
    unawaited(widget.repository.cancelUpdateDownload().catchError((_) {}));
    Navigator.pop(context, '');
  }

  @override
  Widget build(BuildContext context) {
    String megabytes(int bytes) => (bytes / 1048576).toStringAsFixed(1);
    final total = _progress.total;
    return PopScope(
      canPop: false,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop) _cancel();
      },
      child: AlertDialog(
        title: Text('正在下载 ${widget.version}'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            LinearProgressIndicator(
              value: total > 0 ? _progress.received / total : null,
            ),
            const SizedBox(height: 12),
            Text(
              total > 0
                  ? '${megabytes(_progress.received)} / ${megabytes(total)} MB'
                  : '正在连接…',
            ),
          ],
        ),
        actions: [
          FocusRing(
            radius: 20,
            child: TextButton(
              autofocus: true,
              onPressed: _cancel,
              child: const Text('取消'),
            ),
          ),
        ],
      ),
    );
  }
}

Future<_UpdateChoice?> _askUpdate(
  BuildContext context,
  String current,
  AppUpdate update, {
  required bool manual,
}) {
  return showDialog<_UpdateChoice>(
    context: context,
    builder: (dialogContext) => AlertDialog(
      title: Text('发现新版本 ${update.version}'),
      content: Text(
        manual ? '当前版本 $current。' : '当前版本 $current。选「不再提醒」后可在设置 › 关于中重新打开检查更新。',
      ),
      actions: [
        for (final (choice, label) in [
          if (!manual) (_UpdateChoice.mute, '不再提醒'),
          if (!manual) (_UpdateChoice.skip, '忽略此版本'),
          (_UpdateChoice.dismiss, '忽略'),
        ])
          FocusRing(
            radius: 20,
            child: TextButton(
              onPressed: () => Navigator.pop(dialogContext, choice),
              child: Text(label),
            ),
          ),
        FocusRing(
          radius: 20,
          child: FilledButton(
            autofocus: true,
            onPressed: () =>
                Navigator.pop(dialogContext, _UpdateChoice.download),
            child: const Text('更新'),
          ),
        ),
      ],
    ),
  );
}

Future<void> _openRelease(BuildContext context, String url) async {
  final opened = await launchUrl(
    Uri.parse(url),
    mode: LaunchMode.externalApplication,
  ).catchError((_) => false);
  if (opened) return;
  await Clipboard.setData(ClipboardData(text: url));
  if (context.mounted) {
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(const SnackBar(content: Text('无法打开浏览器，链接已复制')));
  }
}
