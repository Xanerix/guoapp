import 'package:flutter/material.dart';

import '../../core_bridge.dart';
import '../../local_store.dart';
import '../../models.dart';
import '../../source_modules.dart';
import '../app_scope.dart';
import '../widgets/focus_ring.dart';

Future<String?> applyModuleInstall(
  AppRepository repository,
  LocalStore store,
  SourceModule module, {
  bool enable = true,
}) async {
  final id = await repository.installModule(module.token);
  if (enable && !module.installed) {
    await store.setSourceEnabled(id, true);
  }
  final site = SourceSite.byId(id);
  if (site.devices && !await repository.sourceDevices(id)) {
    try {
      if (!await repository.sourceDevices(id, register: true)) {
        throw AppFailure('设备注册未完成');
      }
    } catch (failure) {
      return '${module.name} 已安装，但$failure；下次打开应用时会再次注册';
    }
  }
  return null;
}

Future<({int succeeded, int failed, String message})> installIndexedModules(
  AppRepository repository,
  LocalStore store,
  List<ModuleIndexEntry> entries, {
  bool enable = true,
  String verb = '安装',
  bool Function()? canContinue,
}) async {
  final failures = <String>[];
  final notices = <String>[];
  var succeeded = 0;
  for (final entry in entries) {
    if (canContinue != null && !canContinue()) break;
    try {
      final warning = await applyModuleInstall(
        repository,
        store,
        await repository.prepareModule(
          indexed: entry.id,
          bundled: entry.bundled,
        ),
        enable: enable,
      );
      succeeded++;
      if (warning != null) notices.add(warning);
    } catch (failure) {
      failures.add('${entry.name}：$failure');
    }
  }
  final message = succeeded == 0 && failures.isNotEmpty
      ? '站源$verb失败：${failures.join('；')}'
      : [
          '已$verb $succeeded 个站源',
          if (failures.isNotEmpty)
            '${failures.length} 个失败：${failures.join('；')}',
          ...notices,
        ].join('，');
  return (succeeded: succeeded, failed: failures.length, message: message);
}

Future<void> offerRestoredModules(
  BuildContext context,
  LocalStore store,
) async {
  final wanted = store.takeRestoredModules();
  if (wanted.isEmpty || !store.profile.admin || store.locked) return;
  final epoch = store.profileEpoch;
  final repository = AppScope.of(context).repository;
  final messenger = ScaffoldMessenger.of(context);
  final List<ModuleIndexEntry> entries;
  final List<String> unknown;
  try {
    final installed = {
      for (final module in await repository.modules()) module.id,
    };
    final latest = <String, ModuleIndexEntry>{};
    for (final entry in (await repository.moduleIndex()).entries) {
      final current = latest[entry.id];
      if (current == null ||
          isNewerModuleVersion(entry.version, current.version)) {
        latest[entry.id] = entry;
      }
    }
    final missing = [
      for (final id in wanted)
        if (!installed.contains(id)) id,
    ];
    entries = [for (final id in missing) ?latest[id]];
    unknown = [
      for (final id in missing)
        if (!latest.containsKey(id)) id,
    ];
  } catch (failure) {
    messenger.showSnackBar(
      SnackBar(content: Text('无法读取模块仓库，备份里的模块未安装：$failure')),
    );
    return;
  }
  if (entries.isEmpty ||
      !context.mounted ||
      !store.profile.admin ||
      store.locked ||
      store.profileEpoch != epoch) {
    return;
  }
  final accepted = await showDialog<bool>(
    context: context,
    builder: (_) => BatchInstallDialog(
      entries: entries,
      title: '安装备份里的模块？',
      message: [
        '备份里有 ${entries.length} 个模块本机还没有安装，安装后按备份里的站源开关启用。',
        if (unknown.isNotEmpty) '另有 ${unknown.length} 个模块不在仓库里，需要手动安装。',
      ].join(''),
    ),
  );
  if (accepted != true ||
      !context.mounted ||
      !store.profile.admin ||
      store.locked ||
      store.profileEpoch != epoch) {
    return;
  }
  messenger.showSnackBar(SnackBar(content: Text('正在安装 ${entries.length} 个模块')));
  final result = await installIndexedModules(
    repository,
    store,
    entries,
    enable: false,
    canContinue: () =>
        store.profile.admin && !store.locked && store.profileEpoch == epoch,
  );
  if (context.mounted && store.profileEpoch == epoch) {
    messenger.showSnackBar(SnackBar(content: Text(result.message)));
  }
}

class BatchInstallDialog extends StatelessWidget {
  const BatchInstallDialog({
    super.key,
    required this.entries,
    required this.title,
    required this.message,
  });
  final List<ModuleIndexEntry> entries;
  final String title;
  final String message;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final adult = entries.where((entry) => entry.adult).length;
    return AlertDialog(
      title: Text(title),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          spacing: 8,
          children: [
            Text(message),
            const Text('各站源可访问的域名和功能，安装后可在「已安装」里查看。'),
            for (final entry in entries)
              Row(
                spacing: 8,
                children: [
                  Expanded(child: Text('${entry.name} · ${entry.version}')),
                  if (entry.adult)
                    Text('限制级', style: TextStyle(color: colors.error)),
                ],
              ),
            if (adult > 0)
              Text('包含 $adult 个限制级站源。', style: TextStyle(color: colors.error)),
            const Text('安装失败的会跳过，完成后提示结果。'),
          ],
        ),
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
            onPressed: () => Navigator.pop(context, true),
            child: const Text('全部安装'),
          ),
        ),
      ],
    );
  }
}
