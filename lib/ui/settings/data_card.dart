import 'dart:io';

import 'package:flutter/material.dart';

import '../widgets/material_chrome.dart';

import '../../local_store.dart';
import '../app_scope.dart';
import '../users/password_dialog.dart';
import '../widgets/focus_ring.dart';
import '../widgets/group_card.dart';
import 'settings_rows.dart';
import 'storage_view.dart';

class DataCard extends StatefulWidget {
  const DataCard({super.key, required this.store, required this.onCleared});
  final LocalStore store;
  final VoidCallback onCleared;

  @override
  State<DataCard> createState() => _DataCardState();
}

class _DataCardState extends State<DataCard> {
  int? _cacheBytes;
  bool _clearing = false;
  bool _loaded = false;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (_loaded) return;
    _loaded = true;
    _measure();
  }

  Future<void> _measure() async {
    final bytes = await AppScope.of(
      context,
    ).repository.cacheBytes().catchError((Object _) => -1);
    if (mounted) setState(() => _cacheBytes = bytes);
  }

  Future<void> _clearCache() async {
    setState(() => _clearing = true);
    final messenger = ScaffoldMessenger.of(context);
    await saveSetting(context, () async {
      await AppScope.of(context).repository.clearCache();
      messenger.showSnackBar(const SnackBar(content: Text('缓存已清理')));
    });
    if (!mounted) return;
    setState(() => _clearing = false);
    widget.onCleared();
    await _measure();
  }

  Future<void> _deleteAll() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => SurfaceAlertDialog(
        title: const Text('删除全部数据？'),
        content: const Text(
          '将删除所有用户、追剧、观看记录、设置、已安装模块、缓存和已下载的视频，无法恢复。已导出到相册的视频保留。完成后应用会退出。',
        ),
        actions: [
          FocusRing(
            radius: 20,
            child: TextButton(
              autofocus: true,
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
          ),
          FocusRing(
            radius: 20,
            child: FilledButton(
              style: FilledButton.styleFrom(
                backgroundColor: Theme.of(context).colorScheme.error,
                foregroundColor: Theme.of(context).colorScheme.onError,
              ),
              onPressed: () => Navigator.pop(context, true),
              child: const Text('删除'),
            ),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    final store = widget.store;
    if (store.adminProtected &&
        !await showPasswordDialog(
          context,
          title: '验证管理员',
          onSubmit: store.verifyAdmin,
        )) {
      return;
    }
    if (!mounted) return;
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
              Expanded(child: Text('正在删除，请保持应用运行…')),
            ],
          ),
        ),
      ),
    );
    List<String> leftover;
    try {
      leftover = await store.eraseAll(repository.deleteAllData);
    } catch (failure) {
      navigator.pop();
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('$failure')));
      }
      return;
    }
    navigator.pop();
    if (leftover.isEmpty && !Platform.isIOS) exit(0);
    if (!mounted) return;
    await showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (context) => PopScope(
        canPop: false,
        child: SurfaceAlertDialog(
          title: const Text('数据已删除'),
          content: SelectableText(
            [
              if (leftover.isNotEmpty)
                '以下文件没能删除，请退出后手动删除：\n${leftover.join('\n')}',
              if (Platform.isIOS) '请从后台划掉应用后重新打开。',
            ].join('\n\n'),
          ),
          actions: [
            if (!Platform.isIOS)
              FilledButton(
                autofocus: true,
                onPressed: () => exit(0),
                child: const Text('退出应用'),
              ),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final admin = widget.store.profile.admin;
    final bytes = _cacheBytes;
    return SettingsCard(
      children: [
        SettingsTile(
          title: '清理缓存',
          subtitle: [
            if (bytes != null) '已用 ${storageSize(bytes)}',
            '海报、目录和榜单会重新加载',
          ].join(' · '),
          enabled: !_clearing,
          onTap: _clearCache,
        ),
        SettingsTile(
          title: '删除全部数据',
          subtitle: admin ? '删除用户、记录、设置、模块、缓存和已下载的视频，完成后退出应用' : '需要管理员',
          enabled: admin,
          onTap: _deleteAll,
        ),
      ],
    );
  }
}
