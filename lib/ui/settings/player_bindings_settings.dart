import 'dart:io';

import 'package:flutter/gestures.dart';
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

  Future<void> _choose(BuildContext context, PlayerInputAction action) async {
    final initial = store.playbackPreferences.desktopBindings;
    final choice = await showDialog<DesktopPlayerBindings>(
      context: context,
      builder: (_) => _InputDialog(action: action, bindings: initial),
    );
    if (choice == null || !context.mounted) return;
    final preferences = store.playbackPreferences;
    final current = preferences.desktopBindings;
    for (final key in choice.bindings(action)) {
      if (current.conflict(key, action) != null) return;
    }
    var next = choice.keys.containsKey(action)
        ? current.withKey(action, choice.keys[action])
        : current.resetKey(action);
    for (final up in [true, false]) {
      final before = up ? initial.wheelUp : initial.wheelDown;
      final after = up ? choice.wheelUp : choice.wheelDown;
      if (before == after) continue;
      if ((up ? current.wheelUp : current.wheelDown) != before) return;
      next = next.withWheel(up: up, action: after);
    }
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
              for (final action in PlayerInputAction.values.where(
                (value) => value != PlayerInputAction.none,
              ))
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
  String? _detected;
  String? _error;
  bool _changed = false;

  @override
  void dispose() {
    _focus.dispose();
    super.dispose();
  }

  void _recordKey(PlayerKeyBinding binding) {
    final conflict = _draft.conflict(binding, widget.action);
    setState(() {
      _detected = binding.label;
      _error = conflict == null
          ? null
          : '已用于“${conflict.label}”，请先移除该功能的绑定或选择其他输入';
      if (conflict == null) {
        _draft = _draft.recordKey(widget.action, binding);
        _changed = true;
      }
    });
  }

  KeyEventResult _capture(FocusNode _, KeyEvent event) {
    if (!_focus.hasPrimaryFocus) {
      return KeyEventResult.ignored;
    }
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
    if (binding.assignable) _recordKey(binding);
    return KeyEventResult.handled;
  }

  void _wheel(PointerSignalEvent event) {
    if (event is! PointerScrollEvent || event.scrollDelta.dy == 0) return;
    GestureBinding.instance.pointerSignalResolver.register(event, (_) {
      _focus.requestFocus();
      if (widget.action == PlayerInputAction.fullscreen) {
        setState(() {
          _error = '全屏仅支持按键，请录入按键';
        });
        return;
      }
      final up = event.scrollDelta.dy < 0;
      final conflict = _draft.wheelConflict(up, widget.action);
      setState(() {
        _detected = up ? '滚轮向上' : '滚轮向下';
        _error = conflict == null
            ? null
            : '已用于“${conflict.label}”，请先移除该功能的绑定或选择其他输入';
        if (conflict == null) {
          _draft = _draft.recordWheel(widget.action, up: up);
          _changed = true;
        }
      });
    });
  }

  void _remove({bool? wheelUp}) {
    setState(() {
      _draft = wheelUp == null
          ? _draft.withKey(widget.action, null)
          : _draft.withWheel(up: wheelUp, action: PlayerInputAction.none);
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
        _error = '默认按键已用于“${conflict.label}”，请先移除该功能的绑定';
      });
      return;
    }
    setState(() {
      _draft = _draft.resetAction(widget.action);
      if (widget.bindings.wheelUp == widget.action) {
        _draft = _draft.withWheel(up: true);
      }
      if (widget.bindings.wheelDown == widget.action) {
        _draft = _draft.withWheel(up: false);
      }
      _error = null;
      _detected = null;
      _changed = true;
    });
    _focus.requestFocus();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text('设置${widget.action.label}'),
    content: SizedBox(
      width: 480,
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('在下方录入区域按键或滚动鼠标，支持组合键。新输入会替换该功能原有绑定。'),
            const SizedBox(height: 12),
            Wrap(
              spacing: 8,
              runSpacing: 6,
              children: [
                if (_draft.bindings(widget.action).isNotEmpty)
                  InputChip(
                    label: Text(
                      _draft
                          .bindings(widget.action)
                          .map((key) => key.label)
                          .join('、'),
                    ),
                    onDeleted: () => _remove(),
                  ),
                if (_draft.wheelUp == widget.action)
                  InputChip(
                    label: const Text('滚轮向上'),
                    onDeleted: () => _remove(wheelUp: true),
                  ),
                if (_draft.wheelDown == widget.action)
                  InputChip(
                    label: const Text('滚轮向下'),
                    onDeleted: () => _remove(wheelUp: false),
                  ),
              ],
            ),
            const SizedBox(height: 12),
            KeyboardControls(
              child: Focus(
                focusNode: _focus,
                autofocus: true,
                onKeyEvent: _capture,
                child: Listener(
                  key: const ValueKey('shortcut-input-capture'),
                  behavior: HitTestBehavior.opaque,
                  onPointerSignal: _wheel,
                  child: GestureDetector(
                    behavior: HitTestBehavior.opaque,
                    onTap: _focus.requestFocus,
                    child: Container(
                      width: double.infinity,
                      padding: const EdgeInsets.all(16),
                      decoration: BoxDecoration(
                        border: Border.all(
                          color: Theme.of(context).colorScheme.outline,
                        ),
                        borderRadius: BorderRadius.circular(12),
                      ),
                      child: Text(
                        _detected == null ? '请按下按键或滚动鼠标…' : '已检测：$_detected',
                      ),
                    ),
                  ),
                ),
              ),
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
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      FilledButton(
        onPressed: !_changed || _error != null
            ? null
            : () => Navigator.pop(context, _draft),
        child: const Text('保存'),
      ),
    ],
  );
}
