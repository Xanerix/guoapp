import 'dart:io';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';

import '../widgets/material_chrome.dart';
import 'package:flutter/services.dart';
import 'package:path_provider/path_provider.dart';

import '../../local_store.dart';
import '../app_scope.dart';
import '../widgets/focus_ring.dart';
import '../widgets/group_card.dart';
import '../widgets/motion/motion_widgets.dart';
import '../widgets/status_view.dart';
import 'data_card.dart';
import 'settings_rows.dart';

String storageSize(int bytes) {
  if (bytes < 0) return '暂不可用';
  if (bytes < 1 << 20) return '${(bytes / 1024).toStringAsFixed(1)} KB';
  if (bytes < 1 << 30) return '${(bytes / (1 << 20)).toStringAsFixed(1)} MB';
  return '${(bytes / (1 << 30)).toStringAsFixed(2)} GB';
}

class StorageView extends StatefulWidget {
  const StorageView({super.key, required this.store});
  final LocalStore store;

  @override
  State<StorageView> createState() => _StorageViewState();
}

class _StorageViewState extends State<StorageView> {
  Map<String, dynamic>? _info;
  String? _error;
  bool _loaded = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_loaded) return;
    _loaded = true;
    _refresh();
  }

  Future<void> _refresh() async {
    if (!widget.store.canDownload) return;
    try {
      final info = await AppScope.of(context).repository.storage();
      if (mounted) {
        setState(() {
          _info = info;
          _error = null;
        });
      }
    } catch (failure) {
      if (mounted) setState(() => _error = '$failure');
    }
  }

  Future<String?> _pickParent() async {
    if (!Platform.isAndroid && !Platform.isIOS) {
      return FilePicker.getDirectoryPath(dialogTitle: '选择下载保存位置');
    }
    final support = await getApplicationSupportDirectory();
    final directories = <String, String>{support.path: '应用内部存储'};
    if (Platform.isAndroid) {
      final external = await getExternalStorageDirectories() ?? [];
      for (final (index, directory) in external.indexed) {
        directories[directory.path] = index == 0
            ? '设备共享存储（应用目录）'
            : 'SD 卡 ${index + 1}（应用目录）';
      }
    } else {
      final documents = await getApplicationDocumentsDirectory();
      directories[documents.path] = '文件 App 可见目录';
    }
    if (!mounted) return null;
    return showOptionDialog(
      context,
      title: '选择下载位置',
      options: directories,
      selected: null,
    );
  }

  Future<void> _move() async {
    final parent = await _pickParent();
    if (parent == null || !mounted) return;
    await _migrate(parent, parent);
  }

  Future<void> _restore() => _migrate('', '应用默认目录');

  Future<void> _migrate(String parent, String destination) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => SurfaceAlertDialog(
        title: const Text('迁移已下载内容？'),
        content: Text(
          '将下载目录里的全部内容迁移到：\n$destination\n\n下载会先暂停，复制成功后清理旧目录。请保证目标有足够空间，并在完成后继续下载。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            autofocus: true,
            onPressed: () => Navigator.pop(context, true),
            child: const Text('迁移'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    final repository = AppScope.of(context).repository;
    final navigator = Navigator.of(context, rootNavigator: true);
    showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (context) => const PopScope(
        canPop: false,
        child: SurfaceAlertDialog(
          content: Row(
            spacing: 20,
            children: [
              CircularProgressIndicator(),
              Expanded(child: Text('正在迁移，请保持应用运行…')),
            ],
          ),
        ),
      ),
    );
    var leftover = (path: '', reason: '');
    String? failed;
    try {
      leftover = await repository.moveDownloads(parent);
    } catch (failure) {
      failed = '$failure';
    } finally {
      navigator.pop();
    }
    await _refresh();
    if (failed != null && mounted) setState(() => _error = failed);
    if (leftover.path.isEmpty || !mounted) return;
    await showDialog<void>(
      context: context,
      builder: (context) => SurfaceAlertDialog(
        title: const Text('旧下载目录没有删干净'),
        content: SelectableText(
          '下载内容已迁移到新位置，但旧目录没能删除，仍然占用空间：\n${leftover.path}\n\n系统报错：${leftover.reason}\n\n常见原因是文件被其他程序占用、只读或没有权限，处理后请手动删除该文件夹。',
        ),
        actions: [
          FilledButton(
            autofocus: true,
            onPressed: () => Navigator.pop(context),
            child: const Text('知道了'),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    spacing: 12,
    children: [
      if (widget.store.canDownload)
        FadeSwitcher(
          alignment: Alignment.topCenter,
          child: KeyedSubtree(
            key: ValueKey(_info != null ? 'content' : _error ?? 'loading'),
            child: _body(context),
          ),
        ),
      DataCard(store: widget.store, onCleared: _refresh),
    ],
  );

  Widget _body(BuildContext context) {
    final info = _info;
    if (info == null) {
      return _error == null
          ? const StatusView.loading()
          : StatusView(
              title: '无法读取下载目录',
              message: _error,
              icon: Icons.folder_off_outlined,
              action: '重试',
              onAction: _refresh,
            );
    }
    final text = Theme.of(context).textTheme;
    final colors = Theme.of(context).colorScheme;
    final directory = info['directory'] as String? ?? '';
    final admin = widget.store.profile.admin;
    final isDefault = info['default'] == true;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      spacing: 12,
      children: [
        GroupCard(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 16, 16, 14),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                spacing: 4,
                children: [
                  Text(
                    '已使用 ${storageSize((info['bytes'] as num?)?.toInt() ?? 0)}',
                    style: text.headlineSmall,
                  ),
                  Text(
                    '剩余 ${storageSize((info['free'] as num?)?.toInt() ?? -1)} · ${info['files'] ?? 0} 个下载文件',
                    style: text.bodyMedium?.copyWith(
                      color: colors.onSurfaceVariant,
                    ),
                  ),
                ],
              ),
            ),
            const Divider(indent: 16, endIndent: 16),
            SettingsTile(
              title: '当前下载目录',
              subtitle: directory,
              trailing: FocusRing(
                radius: 999,
                child: IconButton(
                  tooltip: '复制路径',
                  onPressed: () =>
                      Clipboard.setData(ClipboardData(text: directory)),
                  icon: const Icon(Icons.copy_rounded),
                ),
              ),
            ),
            const Divider(indent: 16, endIndent: 16),
            SettingsTile(
              title: '更改并迁移目录',
              subtitle: admin ? '下载会先暂停，复制成功后清理旧目录' : '需要管理员',
              enabled: admin,
              onTap: _move,
            ),
            const Divider(indent: 16, endIndent: 16),
            SettingsTile(
              title: '恢复默认下载目录',
              subtitle: !admin
                  ? '需要管理员'
                  : isDefault
                  ? '正在使用默认目录'
                  : '迁移回应用默认目录',
              enabled: admin && !isDefault,
              onTap: _restore,
            ),
            const Divider(indent: 16, endIndent: 16),
            SettingsTile(title: '刷新用量', onTap: _refresh),
          ],
        ),
        if (_error case final error?)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Text(
              error,
              style: text.bodyMedium?.copyWith(color: colors.error),
            ),
          ),
      ],
    );
  }
}
