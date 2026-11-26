import 'package:flutter/material.dart';

import '../../local_store.dart';
import '../../module_updates.dart';
import '../app_scope.dart';
import '../widgets/focus_ring.dart';
import 'module_install.dart';

var _checked = false;
var _checking = false;

enum _ModuleUpdateChoice { later, ignore, update }

Future<void> checkModuleUpdatesAtStartup(
  BuildContext context,
  LocalStore store,
) async {
  bool allowed() =>
      context.mounted &&
      store.profile.admin &&
      !store.locked &&
      store.checkUpdates;
  if (_checked || _checking || !allowed()) return;
  _checking = true;
  final epoch = store.profileEpoch;
  final repository = AppScope.of(context).repository;
  final preferences = ModuleUpdatePreferences(store.preferences);
  final List<ModuleUpdate> updates;
  try {
    final installed = await repository.modules();
    if (installed.isEmpty) {
      _checked = true;
      return;
    }
    updates = availableModuleUpdates(
      installed,
      (await repository.moduleIndex()).entries,
      preferences.ignored,
    );
    _checked = true;
  } catch (_) {
    return;
  } finally {
    _checking = false;
  }
  if (!context.mounted ||
      !allowed() ||
      store.profileEpoch != epoch ||
      updates.isEmpty) {
    return;
  }
  final choice = await showDialog<_ModuleUpdateChoice>(
    context: context,
    builder: (dialog) => AlertDialog(
      title: const Text('站源有新版本'),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          spacing: 12,
          children: [
            Text('${updates.length} 个已安装站源可以更新。'),
            const Text('站源更新可能修复播放、搜索等问题，建议及时更新。'),
            for (final update in updates)
              Text(
                update.module.version.isEmpty
                    ? '${update.module.name}  → ${update.latest.version}'
                    : '${update.module.name}  ${update.module.version} → ${update.latest.version}',
              ),
          ],
        ),
      ),
      actions: [
        for (final (choice, label) in [
          (
            _ModuleUpdateChoice.ignore,
            updates.length == 1 ? '此版本不再提醒' : '这些版本不再提醒',
          ),
          (_ModuleUpdateChoice.later, '稍后'),
        ])
          FocusRing(
            radius: 20,
            child: TextButton(
              onPressed: () => Navigator.pop(dialog, choice),
              child: Text(label),
            ),
          ),
        FocusRing(
          radius: 20,
          child: FilledButton(
            autofocus: true,
            onPressed: () => Navigator.pop(dialog, _ModuleUpdateChoice.update),
            child: Text(updates.length == 1 ? '更新' : '全部更新'),
          ),
        ),
      ],
    ),
  );
  if (!context.mounted || !allowed() || store.profileEpoch != epoch) return;
  final messenger = ScaffoldMessenger.of(context);
  try {
    if (choice == _ModuleUpdateChoice.ignore) {
      await preferences.ignore(updates);
    } else if (choice == _ModuleUpdateChoice.update) {
      messenger.showSnackBar(
        SnackBar(content: Text('正在更新 ${updates.length} 个站源')),
      );
      final result = await installIndexedModules(
        repository,
        store,
        [for (final update in updates) update.latest],
        enable: false,
        verb: '更新',
        canContinue: () =>
            store.profile.admin && !store.locked && store.profileEpoch == epoch,
      );
      if (allowed() && store.profileEpoch == epoch) {
        final text = result.succeeded == 0 && result.failed > 0
            ? result.message
            : '更新完成：${result.message}';
        messenger.showSnackBar(SnackBar(content: Text(text)));
      }
    }
  } catch (failure) {
    if (allowed() && store.profileEpoch == epoch) {
      messenger.showSnackBar(SnackBar(content: Text('站源更新失败：$failure')));
    }
  }
}
