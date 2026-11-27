import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../desktop_player_bindings.dart';
import '../../local_store.dart';
import '../widgets/focus_ring.dart';
import '../widgets/group_card.dart';
import 'boss_key_tile.dart';
import 'settings_rows.dart';

class PlayerBindingsSettings extends StatelessWidget {
  const PlayerBindingsSettings({super.key, required this.store});
  final LocalStore store;

  Future<void> _key(BuildContext context, PlayerInputAction action) async {
    final bindings = store.playbackPreferences.desktopBindings;
    final choice = await showDialog<({bool reset, PlayerKeyBinding? key})>(
      context: context,
      builder: (_) => _KeyDialog(action: action, bindings: bindings),
    );
    if (choice == null || !context.mounted) return;
    final preferences = store.playbackPreferences;
    final current = preferences.desktopBindings;
    if (choice.reset) {
      if (current.resetConflict(action) != null) return;
    } else if (choice.key != null &&
        current.conflict(choice.key!, action) != null) {
      return;
    }
    await saveSetting(
      context,
      () => store.setPlaybackPreferences(
        preferences.copyWith(
          desktopBindings: choice.reset
              ? current.resetKey(action)
              : current.withKey(action, choice.key),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: store,
    builder: (context, _) {
      final preferences = store.playbackPreferences;
      final bindings = preferences.desktopBindings;
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        spacing: 12,
        children: [
          if (Platform.isWindows || Platform.isMacOS)
            const SettingsCard(children: [BossKeyTile()]),
          const Padding(
            padding: EdgeInsets.fromLTRB(4, 4, 4, 0),
            child: Text(
              '以下按键与滚轮操作在电脑播放器中生效。键盘可使用组合键，Esc 和 Tab 保留用于返回和焦点导航。未自定义的操作保持当前布局的默认设置。',
            ),
          ),
          SettingsCard(
            children: [
              for (final action in PlayerInputAction.values.where(
                (action) => action != PlayerInputAction.none,
              ))
                SettingsTile(
                  title: action.label,
                  value: bindings.bindings(action).isEmpty
                      ? '不绑定'
                      : bindings
                            .bindings(action)
                            .map((key) => key.label)
                            .join('、'),
                  onTap: () => _key(context, action),
                ),
            ],
          ),
          SettingsCard(
            children: [
              for (final up in [true, false])
                ValueTile<String>(
                  title: up ? '滚轮向上' : '滚轮向下',
                  subtitle: '在播放画面上滚动；切集时一次连续滚动只切换一集',
                  options: {
                    'default': '保持默认',
                    for (final action in PlayerInputAction.values.where(
                      (action) => action != PlayerInputAction.fullscreen,
                    ))
                      action.name: action.label,
                  },
                  value:
                      (up ? bindings.wheelUp : bindings.wheelDown)?.name ??
                      'default',
                  onChanged: (action) => saveSetting(
                    context,
                    () => store.setPlaybackPreferences(
                      store.playbackPreferences.copyWith(
                        desktopBindings: store
                            .playbackPreferences
                            .desktopBindings
                            .withWheel(
                              up: up,
                              action: PlayerInputAction.values
                                  .asNameMap()[action],
                            ),
                      ),
                    ),
                  ),
                ),
            ],
          ),
          Align(
            alignment: Alignment.centerLeft,
            child: FocusRing(
              radius: 20,
              child: OutlinedButton.icon(
                onPressed: bindings.isDefault
                    ? null
                    : () => saveSetting(
                        context,
                        () => store.setPlaybackPreferences(
                          store.playbackPreferences.copyWith(
                            desktopBindings: const DesktopPlayerBindings(),
                          ),
                        ),
                      ),
                icon: const Icon(Icons.restore_rounded),
                label: const Text('恢复播放器默认操作'),
              ),
            ),
          ),
        ],
      );
    },
  );
}

class _KeyDialog extends StatefulWidget {
  const _KeyDialog({required this.action, required this.bindings});
  final PlayerInputAction action;
  final DesktopPlayerBindings bindings;
  @override
  State<_KeyDialog> createState() => _KeyDialogState();
}

class _KeyDialogState extends State<_KeyDialog> {
  final _focus = FocusNode(debugLabel: 'playerBinding');
  PlayerKeyBinding? _key;
  String? _error;
  @override
  void dispose() {
    _focus.dispose();
    super.dispose();
  }

  void _reset() {
    final conflict = widget.bindings.resetConflict(widget.action);
    if (conflict != null) {
      setState(() {
        _error = '默认按键已用于“${conflict.label}”，请先调整该操作或恢复全部默认';
      });
      return;
    }
    Navigator.pop(context, (reset: true, key: null));
  }

  KeyEventResult _capture(FocusNode _, KeyEvent event) {
    if (!_focus.hasPrimaryFocus || event is! KeyDownEvent) {
      return KeyEventResult.ignored;
    }
    final key = event.logicalKey;
    if (key == LogicalKeyboardKey.escape || key == LogicalKeyboardKey.tab) {
      return KeyEventResult.ignored;
    }
    final keyboard = HardwareKeyboard.instance;
    final binding = PlayerKeyBinding(
      key,
      control: keyboard.isControlPressed,
      alt: keyboard.isAltPressed,
      shift: keyboard.isShiftPressed,
      meta: keyboard.isMetaPressed,
    );
    if (!binding.assignable) return KeyEventResult.handled;
    final conflict = widget.bindings.conflict(binding, widget.action);
    setState(() {
      _key = binding;
      _error = conflict == null
          ? null
          : '已用于“${conflict.label}”，请先调整该操作或选择其他按键';
    });
    return KeyEventResult.handled;
  }

  @override
  Widget build(BuildContext context) {
    final modifiers = Platform.isMacOS
        ? 'Ctrl、Option、Shift 或 Cmd'
        : 'Ctrl、Alt、Shift 或 Win';
    return AlertDialog(
      title: Text('自定义${widget.action.label}'),
      content: Focus(
        focusNode: _focus,
        autofocus: true,
        onKeyEvent: _capture,
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: _focus.requestFocus,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('按下要使用的按键，可同时按住 $modifiers。'),
              const SizedBox(height: 16),
              Text(
                _key?.label ?? '等待按键…',
                style: Theme.of(context).textTheme.titleMedium,
              ),
              if (_error != null) ...[
                const SizedBox(height: 12),
                Text(
                  _error!,
                  style: TextStyle(color: Theme.of(context).colorScheme.error),
                ),
              ],
            ],
          ),
        ),
      ),
      actions: [
        TextButton(onPressed: _reset, child: const Text('恢复默认')),
        TextButton(
          onPressed: () => Navigator.pop(context, (reset: false, key: null)),
          child: const Text('不绑定'),
        ),
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: _key == null || _error != null
              ? null
              : () => Navigator.pop(context, (reset: false, key: _key)),
          child: const Text('保存'),
        ),
      ],
    );
  }
}
