import 'package:flutter/material.dart';

import '../../boss_key.dart';
import '../widgets/focus_ring.dart';
import '../widgets/group_card.dart';
import 'settings_rows.dart';

class BossKeyTile extends StatelessWidget {
  const BossKeyTile({super.key});

  Future<void> _choose(BuildContext context) async {
    await suspendBossKey();
    if (!context.mounted) return resumeBossKey();
    final choice = await showDialog<({BossKey? key})>(
      context: context,
      builder: (context) => const _BossKeyDialog(),
    );
    if (choice == null || !context.mounted) return resumeBossKey();
    await saveSetting(context, () => setBossKey(choice.key));
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: Listenable.merge([bossKey, bossKeyEnabled]),
    builder: (context, _) {
      final value = bossKey.value;
      return SettingsTile(
        title: '老板键',
        subtitle: value != null && !bossKeyEnabled.value
            ? '已在摸鱼设置中关闭，可在摸鱼里重新开启'
            : '在任何程序里按下快捷键，隐藏或恢复窗口，隐藏时暂停播放',
        value: value?.label ?? '未设置',
        onTap: () => _choose(context),
      );
    },
  );
}

class BossKeySwitchTile extends StatelessWidget {
  const BossKeySwitchTile({super.key});

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: Listenable.merge([bossKey, bossKeyEnabled]),
    builder: (context, _) {
      final value = bossKey.value;
      return SwitchTile(
        title: '老板键',
        subtitle: value == null
            ? '请先在「控制」中设置快捷键'
            : '全局快捷键：${value.label}，按下后隐藏或恢复窗口并暂停播放',
        value: value != null && bossKeyEnabled.value,
        onChanged: value == null
            ? null
            : (enabled) =>
                  saveSetting(context, () => setBossKeyEnabled(enabled)),
      );
    },
  );
}

class _BossKeyDialog extends StatefulWidget {
  const _BossKeyDialog();

  @override
  State<_BossKeyDialog> createState() => _BossKeyDialogState();
}

class _BossKeyDialogState extends State<_BossKeyDialog> {
  KeyEventResult _onKey(FocusNode _, KeyEvent event) {
    final value = BossKey.fromEvent(event);
    if (value == null) return KeyEventResult.ignored;
    Navigator.pop(context, (key: value));
    return KeyEventResult.handled;
  }

  @override
  Widget build(BuildContext context) {
    final current = bossKey.value;
    return Focus(
      autofocus: true,
      onKeyEvent: _onKey,
      child: AlertDialog(
        title: const Text('老板键'),
        content: Text(
          '按下新的快捷键：字母、数字或 F1~F12，可搭配修饰键。'
          '${current == null ? '' : '\n当前：${current.label}'}',
        ),
        actions: [
          if (current != null)
            FocusRing(
              radius: 20,
              child: TextButton(
                onPressed: () => Navigator.pop(context, (key: null)),
                child: const Text('清除'),
              ),
            ),
          FocusRing(
            radius: 20,
            child: TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('取消'),
            ),
          ),
        ],
      ),
    );
  }
}
