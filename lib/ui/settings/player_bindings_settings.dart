import 'dart:io';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../desktop_player_bindings.dart';
import '../../local_store.dart';
import '../player/player_input.dart';
import '../widgets/focus_ring.dart';
import '../widgets/group_card.dart';
import 'boss_key_tile.dart';
import 'settings_rows.dart';

class PlayerBindingsSettings extends StatelessWidget {
  const PlayerBindingsSettings({super.key, required this.store});
  final LocalStore store;

  Future<void> _choose(BuildContext context, PlayerInputAction action) async {
    final initial = store.playbackPreferences.desktopBindings;
    final choice = await showDialog<List<PlayerInputBinding?>>(
      context: context,
      builder: (_) => _InputDialog(action: action, bindings: initial),
    );
    if (choice == null || !context.mounted) return;
    final preferences = store.playbackPreferences;
    final current = preferences.desktopBindings;
    for (var i = 0; i < 2; i++) {
      final item = choice[i];
      if (item == null) continue;
      if (!item.allowedFor(action)) return;
      final owner = current.conflict(item, action);
      if (owner != null && owner != action) return;
    }
    var next = current.setSlot(action, 0, choice[0]);
    next = next.setSlot(action, 1, choice[1]);
    await saveSetting(
      context,
      () => store.setPlaybackPreferences(
        preferences.copyWith(desktopBindings: next),
      ),
    );
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: store,
    builder: (context, _) {
      final bindings = store.playbackPreferences.desktopBindings;
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        spacing: 12,
        children: [
          if (Platform.isWindows || Platform.isMacOS)
            const SettingsCard(children: [BossKeyTile()]),
          SettingsCard(
            children: [
              for (final action in PlayerInputAction.values)
                SettingsTile(
                  title: action.label,
                  value: bindings.labels(action).isEmpty
                      ? '未绑定'
                      : bindings.labels(action).join('、'),
                  onTap: () => _choose(context, action),
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

class _InputDialog extends StatefulWidget {
  const _InputDialog({required this.action, required this.bindings});
  final PlayerInputAction action;
  final DesktopPlayerBindings bindings;

  @override
  State<_InputDialog> createState() => _InputDialogState();
}

class _InputDialogState extends State<_InputDialog> {
  final _focus = FocusNode(debugLabel: 'shortcutCapture');
  late DesktopPlayerBindings _draft = widget.bindings;
  late int _active = _initialSlot();
  String? _detected;
  String? _error;
  bool _changed = false;

  int _initialSlot() {
    final slots = widget.bindings.slots(widget.action);
    return slots[0] == null
        ? 0
        : slots[1] == null
        ? 1
        : 0;
  }

  @override
  void dispose() {
    _focus.dispose();
    super.dispose();
  }

  void _record(PlayerInputBinding input) {
    if (!input.allowedFor(widget.action)) {
      setState(() {
        _detected = input.label;
        _error = '全屏仅支持按键或鼠标侧键，请重新录入';
      });
      return;
    }
    final conflict = _draft.conflict(input, widget.action, slot: _active);
    setState(() {
      _detected = input.label;
      _error = conflict == null
          ? null
          : conflict == widget.action
          ? '该功能另一键位已使用此输入'
          : '已用于“${conflict.label}”，请先移除该功能的绑定或选择其他输入';
      if (conflict == null) {
        _draft = _draft.setSlot(widget.action, _active, input);
        _changed = true;
      }
    });
  }

  KeyEventResult _captureKey(FocusNode _, KeyEvent event) {
    if (!_focus.hasPrimaryFocus) return KeyEventResult.ignored;
    if (event is KeyRepeatEvent) return KeyEventResult.handled;
    if (event is! KeyDownEvent) return KeyEventResult.ignored;
    if (event.logicalKey == LogicalKeyboardKey.escape ||
        event.logicalKey == LogicalKeyboardKey.tab) {
      return KeyEventResult.ignored;
    }
    final keyboard = HardwareKeyboard.instance;
    final binding = PlayerKeyBinding(
      event.logicalKey,
      control: keyboard.isControlPressed,
      alt: keyboard.isAltPressed,
      shift: keyboard.isShiftPressed,
      meta: keyboard.isMetaPressed,
    );
    if (binding.assignable) _record(PlayerInputBinding.key(binding));
    return KeyEventResult.handled;
  }

  void _captureWheel(PointerSignalEvent event) {
    if (event is! PointerScrollEvent || event.scrollDelta.dy == 0) return;
    GestureBinding.instance.pointerSignalResolver.register(event, (_) {
      _focus.requestFocus();
      _record(
        PlayerInputBinding.pointer(
          event.scrollDelta.dy < 0
              ? PlayerPointerInput.wheelUp
              : PlayerPointerInput.wheelDown,
        ),
      );
    });
  }

  void _capturePointerDown(PointerDownEvent event) {
    final input = PlayerPointerInput.fromButtons(event.buttons);
    if (input == null) return;
    PlayerPointerClaim.claim(event);
    _focus.requestFocus();
    _record(PlayerInputBinding.pointer(input));
  }

  void _selectSlot(int index) {
    setState(() {
      _active = index;
      _error = null;
      _detected = null;
    });
    _focus.requestFocus();
  }

  void _clearSlot(int index) {
    setState(() {
      _draft = _draft.setSlot(widget.action, index, null);
      _active = index;
      _error = null;
      _detected = null;
      _changed = true;
    });
    _focus.requestFocus();
  }

  void _reset() {
    final conflict = _draft.resetConflict(widget.action);
    if (conflict != null) {
      setState(() {
        _error = '默认输入已用于“${conflict.label}”，请先移除该功能的绑定';
      });
      return;
    }
    setState(() {
      _draft = _draft.resetAction(widget.action);
      _error = null;
      _detected = null;
      _changed = true;
    });
    _focus.requestFocus();
  }

  @override
  Widget build(BuildContext context) {
    final slots = _draft.slots(widget.action);
    final colors = Theme.of(context).colorScheme;
    return AlertDialog(
      title: Text('设置${widget.action.label}'),
      content: SizedBox(
        width: 480,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('选择键位后，在下方录入区域按键、滚动鼠标或按鼠标中键/侧键，支持组合键。'),
              const SizedBox(height: 12),
              Row(
                spacing: 8,
                children: [
                  for (var i = 0; i < 2; i++)
                    Expanded(
                      child: _SlotCard(
                        title: '键位 ${i + 1}',
                        label: slots[i]?.label ?? '未绑定',
                        selected: _active == i,
                        onSelect: () => _selectSlot(i),
                        onClear: slots[i] == null ? null : () => _clearSlot(i),
                      ),
                    ),
                ],
              ),
              const SizedBox(height: 12),
              KeyboardControls(
                child: Focus(
                  focusNode: _focus,
                  autofocus: true,
                  onKeyEvent: _captureKey,
                  child: Listener(
                    key: const ValueKey('shortcut-input-capture'),
                    behavior: HitTestBehavior.opaque,
                    onPointerSignal: _captureWheel,
                    onPointerDown: _capturePointerDown,
                    child: GestureDetector(
                      behavior: HitTestBehavior.opaque,
                      onTap: _focus.requestFocus,
                      child: Container(
                        width: double.infinity,
                        padding: const EdgeInsets.all(16),
                        decoration: BoxDecoration(
                          border: Border.all(color: colors.outline),
                          borderRadius: BorderRadius.circular(12),
                        ),
                        child: Text(
                          _detected == null
                              ? '正在录入键位 ${_active + 1}：请按下按键、滚动鼠标或按鼠标侧键…'
                              : '已检测（键位 ${_active + 1}）：$_detected',
                        ),
                      ),
                    ),
                  ),
                ),
              ),
              if (_error != null) ...[
                const SizedBox(height: 12),
                Text(_error!, style: TextStyle(color: colors.error)),
              ],
            ],
          ),
        ),
      ),
      actions: [
        TextButton(onPressed: _reset, child: const Text('恢复默认')),
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: !_changed || _error != null
              ? null
              : () => Navigator.pop(context, _draft.slots(widget.action)),
          child: const Text('保存'),
        ),
      ],
    );
  }
}

class _SlotCard extends StatelessWidget {
  const _SlotCard({
    required this.title,
    required this.label,
    required this.selected,
    required this.onSelect,
    this.onClear,
  });
  final String title;
  final String label;
  final bool selected;
  final VoidCallback onSelect;
  final VoidCallback? onClear;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    return Material(
      color: selected ? colors.secondaryContainer : colors.surfaceContainerHigh,
      borderRadius: BorderRadius.circular(12),
      child: InkWell(
        onTap: onSelect,
        borderRadius: BorderRadius.circular(12),
        child: Container(
          padding: const EdgeInsets.fromLTRB(12, 10, 6, 10),
          decoration: BoxDecoration(
            border: Border.all(
              color: selected ? colors.primary : colors.outlineVariant,
            ),
            borderRadius: BorderRadius.circular(12),
          ),
          child: Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  spacing: 2,
                  children: [
                    Text(
                      title,
                      style: theme.textTheme.labelSmall?.copyWith(
                        color: colors.onSurfaceVariant,
                      ),
                    ),
                    Text(
                      label,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: theme.textTheme.bodyMedium,
                    ),
                  ],
                ),
              ),
              if (onClear != null)
                IconButton(
                  icon: const Icon(Icons.close_rounded, size: 18),
                  tooltip: '清除',
                  visualDensity: VisualDensity.compact,
                  onPressed: onClear,
                ),
            ],
          ),
        ),
      ),
    );
  }
}
