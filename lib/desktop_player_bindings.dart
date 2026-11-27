import 'dart:io';

import 'package:flutter/gestures.dart';
import 'package:flutter/services.dart';

enum PlayerInputAction {
  playPause('播放 / 暂停'),
  nextEpisode('下一集'),
  previousEpisode('上一集'),
  seekForward('快进'),
  seekBackward('快退'),
  volumeUp('增加音量'),
  volumeDown('降低音量'),
  mute('静音 / 恢复音量'),
  fullscreen('全屏（视频）');

  const PlayerInputAction(this.label);
  final String label;

  bool get repeats => this == volumeUp || this == volumeDown;
  bool get episode => this == nextEpisode || this == previousEpisode;
}

enum PlayerInputScope { video, audio, portrait, mini }

enum PlayerPointerInput {
  wheelUp('滚轮向上'),
  wheelDown('滚轮向下'),
  mouseMiddle('鼠标中键'),
  mouseBack('鼠标后退键'),
  mouseForward('鼠标前进键');

  const PlayerPointerInput(this.label);
  final String label;

  bool get wheel => this == wheelUp || this == wheelDown;

  static PlayerPointerInput? fromButtons(int buttons) => switch (buttons) {
    kMiddleMouseButton => mouseMiddle,
    kBackMouseButton => mouseBack,
    kForwardMouseButton => mouseForward,
    _ => null,
  };
}

class PlayerKeyBinding {
  const PlayerKeyBinding(
    this.key, {
    this.control = false,
    this.alt = false,
    this.shift = false,
    this.meta = false,
  });

  final LogicalKeyboardKey key;
  final bool control;
  final bool alt;
  final bool shift;
  final bool meta;

  static final _reserved = {
    LogicalKeyboardKey.escape,
    LogicalKeyboardKey.tab,
    LogicalKeyboardKey.shiftLeft,
    LogicalKeyboardKey.shiftRight,
    LogicalKeyboardKey.controlLeft,
    LogicalKeyboardKey.controlRight,
    LogicalKeyboardKey.altLeft,
    LogicalKeyboardKey.altRight,
    LogicalKeyboardKey.metaLeft,
    LogicalKeyboardKey.metaRight,
    LogicalKeyboardKey.mediaPlayPause,
    LogicalKeyboardKey.mediaPlay,
    LogicalKeyboardKey.mediaPause,
    LogicalKeyboardKey.mediaTrackNext,
    LogicalKeyboardKey.mediaTrackPrevious,
  };

  bool get assignable =>
      !_reserved.contains(key) && !(alt && key == LogicalKeyboardKey.f4);

  String get label {
    final keyText = switch (key) {
      LogicalKeyboardKey.space => '空格',
      LogicalKeyboardKey.enter => 'Enter',
      LogicalKeyboardKey.numpadEnter => '小键盘 Enter',
      LogicalKeyboardKey.arrowLeft => '←',
      LogicalKeyboardKey.arrowRight => '→',
      LogicalKeyboardKey.arrowUp => '↑',
      LogicalKeyboardKey.arrowDown => '↓',
      _ =>
        key.keyLabel.isNotEmpty
            ? key.keyLabel
            : (key.debugName ?? '按键 ${key.keyId}'),
    };
    return [
      if (control) 'Ctrl',
      if (alt) Platform.isMacOS ? 'Option' : 'Alt',
      if (shift) 'Shift',
      if (meta)
        Platform.isMacOS
            ? 'Cmd'
            : Platform.isLinux
            ? 'Super'
            : 'Win',
      keyText,
    ].join(' + ');
  }

  bool matches(KeyEvent event) {
    final keyboard = HardwareKeyboard.instance;
    return event.logicalKey == key &&
        control == keyboard.isControlPressed &&
        alt == keyboard.isAltPressed &&
        shift == keyboard.isShiftPressed &&
        meta == keyboard.isMetaPressed;
  }

  Map<String, Object> toJson() => {
    'key': key.keyId,
    'control': control,
    'alt': alt,
    'shift': shift,
    'meta': meta,
  };

  static PlayerKeyBinding? fromJson(Object? value) {
    if (value is! Map || value['key'] is! int) return null;
    final key = LogicalKeyboardKey.findKeyByKeyId(value['key'] as int);
    if (key == null) return null;
    final binding = PlayerKeyBinding(
      key,
      control: value['control'] == true,
      alt: value['alt'] == true,
      shift: value['shift'] == true,
      meta: value['meta'] == true,
    );
    return binding.assignable ? binding : null;
  }

  @override
  bool operator ==(Object other) =>
      other is PlayerKeyBinding &&
      key == other.key &&
      control == other.control &&
      alt == other.alt &&
      shift == other.shift &&
      meta == other.meta;

  @override
  int get hashCode => Object.hash(key, control, alt, shift, meta);
}

class PlayerInputBinding {
  const PlayerInputBinding.key(PlayerKeyBinding this.key) : pointer = null;
  const PlayerInputBinding.pointer(PlayerPointerInput this.pointer)
    : key = null;

  final PlayerKeyBinding? key;
  final PlayerPointerInput? pointer;

  bool get isWheel => pointer?.wheel == true;

  String get label => key?.label ?? pointer!.label;

  bool allowedFor(PlayerInputAction action) =>
      !(action == PlayerInputAction.fullscreen && isWheel);

  Map<String, Object> toJson() => key != null
      ? {'type': 'key', ...key!.toJson()}
      : {'type': 'pointer', 'pointer': pointer!.name};

  static PlayerInputBinding? fromJson(Object? value) {
    if (value is! Map) return null;
    if (value['type'] == 'pointer') {
      final pointer = PlayerPointerInput.values.asNameMap()[value['pointer']];
      return pointer == null ? null : PlayerInputBinding.pointer(pointer);
    }
    final key = PlayerKeyBinding.fromJson(value);
    return key == null ? null : PlayerInputBinding.key(key);
  }

  @override
  bool operator ==(Object other) =>
      other is PlayerInputBinding &&
      key == other.key &&
      pointer == other.pointer;

  @override
  int get hashCode => Object.hash(key, pointer);
}

const _defaults = <PlayerInputAction, List<PlayerInputBinding?>>{
  PlayerInputAction.playPause: [
    PlayerInputBinding.key(PlayerKeyBinding(LogicalKeyboardKey.space)),
    null,
  ],
  PlayerInputAction.nextEpisode: [
    PlayerInputBinding.key(PlayerKeyBinding(LogicalKeyboardKey.pageDown)),
    null,
  ],
  PlayerInputAction.previousEpisode: [
    PlayerInputBinding.key(PlayerKeyBinding(LogicalKeyboardKey.pageUp)),
    null,
  ],
  PlayerInputAction.seekForward: [
    PlayerInputBinding.key(PlayerKeyBinding(LogicalKeyboardKey.arrowRight)),
    null,
  ],
  PlayerInputAction.seekBackward: [
    PlayerInputBinding.key(PlayerKeyBinding(LogicalKeyboardKey.arrowLeft)),
    null,
  ],
  PlayerInputAction.volumeUp: [
    PlayerInputBinding.key(PlayerKeyBinding(LogicalKeyboardKey.arrowUp)),
    PlayerInputBinding.pointer(PlayerPointerInput.wheelUp),
  ],
  PlayerInputAction.volumeDown: [
    PlayerInputBinding.key(PlayerKeyBinding(LogicalKeyboardKey.arrowDown)),
    PlayerInputBinding.pointer(PlayerPointerInput.wheelDown),
  ],
  PlayerInputAction.mute: [
    PlayerInputBinding.key(PlayerKeyBinding(LogicalKeyboardKey.keyM)),
    null,
  ],
  PlayerInputAction.fullscreen: [
    PlayerInputBinding.key(PlayerKeyBinding(LogicalKeyboardKey.f11)),
    PlayerInputBinding.key(PlayerKeyBinding(LogicalKeyboardKey.enter)),
  ],
};

List<PlayerInputBinding?> _defaultSlots(PlayerInputAction action) =>
    List<PlayerInputBinding?>.unmodifiable(_defaults[action] ?? [null, null]);

bool _sameSlots(List<PlayerInputBinding?> a, List<PlayerInputBinding?> b) =>
    a[0] == b[0] && a[1] == b[1];

class DesktopPlayerBindings {
  const DesktopPlayerBindings({this.actions = const {}});

  final Map<PlayerInputAction, List<PlayerInputBinding?>> actions;

  bool get isDefault => actions.isEmpty;

  static PlayerInputAction? mediaAction(KeyEvent event) {
    final keyboard = HardwareKeyboard.instance;
    if (keyboard.isControlPressed ||
        keyboard.isAltPressed ||
        keyboard.isShiftPressed ||
        keyboard.isMetaPressed) {
      return null;
    }
    return switch (event.logicalKey) {
      LogicalKeyboardKey.mediaPlayPause ||
      LogicalKeyboardKey.mediaPlay ||
      LogicalKeyboardKey.mediaPause => PlayerInputAction.playPause,
      LogicalKeyboardKey.mediaTrackNext => PlayerInputAction.nextEpisode,
      LogicalKeyboardKey.mediaTrackPrevious =>
        PlayerInputAction.previousEpisode,
      _ => null,
    };
  }

  List<PlayerInputBinding?> slots(PlayerInputAction action) =>
      actions[action] ?? _defaultSlots(action);

  List<String> labels(PlayerInputAction action) => [
    for (final item in slots(action))
      if (item != null) item.label,
  ];

  bool _allowed(PlayerInputAction action, PlayerInputScope scope) =>
      action != PlayerInputAction.fullscreen ||
      scope == PlayerInputScope.video ||
      scope == PlayerInputScope.portrait;

  PlayerInputAction? resolveKey(KeyEvent event, PlayerInputScope scope) {
    if (mediaAction(event) case final media? when _allowed(media, scope)) {
      return media;
    }
    for (final action in PlayerInputAction.values) {
      if (!_allowed(action, scope)) continue;
      for (final item in slots(action)) {
        if (item?.key?.matches(event) == true) return action;
      }
    }
    return null;
  }

  PlayerInputAction? resolvePointer(
    PlayerPointerInput input,
    PlayerInputScope scope,
  ) {
    for (final action in PlayerInputAction.values) {
      if (!_allowed(action, scope)) continue;
      for (final item in slots(action)) {
        if (item?.pointer == input) return action;
      }
    }
    return null;
  }

  PlayerInputAction? resolveWheel(bool up, PlayerInputScope scope) =>
      resolvePointer(
        up ? PlayerPointerInput.wheelUp : PlayerPointerInput.wheelDown,
        scope,
      );

  PlayerInputAction? conflict(
    PlayerInputBinding input,
    PlayerInputAction action, {
    int? slot,
  }) {
    for (final other in PlayerInputAction.values) {
      final list = slots(other);
      for (var i = 0; i < 2; i++) {
        if (other == action && i == slot) continue;
        if (list[i] == input) return other;
      }
    }
    return null;
  }

  PlayerInputAction? resetConflict(PlayerInputAction action) {
    for (final item in _defaultSlots(action)) {
      if (item == null) continue;
      final other = conflict(item, action);
      if (other != null && other != action) return other;
    }
    return null;
  }

  DesktopPlayerBindings setSlot(
    PlayerInputAction action,
    int slot,
    PlayerInputBinding? input,
  ) {
    assert(slot == 0 || slot == 1);
    if (input != null && !input.allowedFor(action)) return this;
    final next = List<PlayerInputBinding?>.of(slots(action));
    next[slot] = input;
    return _withSlots(action, next);
  }

  DesktopPlayerBindings resetAction(PlayerInputAction action) {
    if (!actions.containsKey(action)) return this;
    final copy = Map<PlayerInputAction, List<PlayerInputBinding?>>.of(actions)
      ..remove(action);
    return DesktopPlayerBindings(actions: Map.unmodifiable(copy));
  }

  DesktopPlayerBindings _withSlots(
    PlayerInputAction action,
    List<PlayerInputBinding?> next,
  ) {
    final copy = Map<PlayerInputAction, List<PlayerInputBinding?>>.of(actions);
    if (_sameSlots(next, _defaultSlots(action))) {
      copy.remove(action);
    } else {
      copy[action] = List<PlayerInputBinding?>.unmodifiable(next);
    }
    return DesktopPlayerBindings(actions: Map.unmodifiable(copy));
  }

  Map<String, Object?> toJson() => {
    'v': 2,
    'actions': {
      for (final entry in actions.entries)
        entry.key.name: [entry.value[0]?.toJson(), entry.value[1]?.toJson()],
    },
  };

  factory DesktopPlayerBindings.fromJson(Object? value) {
    if (value is! Map) return const DesktopPlayerBindings();
    final raw = value['actions'] is Map
        ? _parseV2(value['actions'] as Map)
        : _parseLegacy(value);
    return _sanitize(raw);
  }

  static Map<PlayerInputAction, List<PlayerInputBinding?>> _parseV2(Map rows) {
    final parsed = <PlayerInputAction, List<PlayerInputBinding?>>{};
    for (final entry in rows.entries) {
      final action = PlayerInputAction.values.asNameMap()[entry.key];
      if (action == null || entry.value is! List) continue;
      final list = entry.value as List;
      final slot0 = list.isNotEmpty
          ? PlayerInputBinding.fromJson(list[0])
          : null;
      final slot1 = list.length > 1
          ? PlayerInputBinding.fromJson(list[1])
          : null;
      final valid0 = slot0 != null && slot0.allowedFor(action) ? slot0 : null;
      final valid1 =
          slot1 != null && slot1.allowedFor(action) && slot1 != valid0
          ? slot1
          : null;
      parsed[action] = [valid0, valid1];
    }
    return parsed;
  }

  static Map<PlayerInputAction, List<PlayerInputBinding?>> _parseLegacy(
    Map value,
  ) {
    final parsed = <PlayerInputAction, List<PlayerInputBinding?>>{};
    final keys = value['keys'];
    if (keys is Map) {
      for (final entry in keys.entries) {
        final action = PlayerInputAction.values.asNameMap()[entry.key];
        if (action == null) continue;
        if (entry.value == null) {
          parsed[action] = [null, null];
          continue;
        }
        final key = PlayerKeyBinding.fromJson(entry.value);
        if (key != null) {
          parsed[action] = [PlayerInputBinding.key(key), null];
        }
      }
    }
    void applyWheel(Object? raw, PlayerPointerInput pointer) {
      if (raw == 'none') {
        final owner = pointer == PlayerPointerInput.wheelUp
            ? PlayerInputAction.volumeUp
            : PlayerInputAction.volumeDown;
        final slots = List<PlayerInputBinding?>.of(
          parsed[owner] ?? _defaultSlots(owner),
        );
        for (var i = 0; i < 2; i++) {
          if (slots[i]?.pointer == pointer) slots[i] = null;
        }
        parsed[owner] = slots;
        return;
      }
      final action = PlayerInputAction.values.asNameMap()[raw];
      if (action == null || action == PlayerInputAction.fullscreen) return;
      final defaultOwner = pointer == PlayerPointerInput.wheelUp
          ? PlayerInputAction.volumeUp
          : PlayerInputAction.volumeDown;
      if (action != defaultOwner) {
        final ownerSlots = List<PlayerInputBinding?>.of(
          parsed[defaultOwner] ?? _defaultSlots(defaultOwner),
        );
        for (var i = 0; i < 2; i++) {
          if (ownerSlots[i]?.pointer == pointer) ownerSlots[i] = null;
        }
        parsed[defaultOwner] = ownerSlots;
      }
      final target = List<PlayerInputBinding?>.of(
        parsed[action] ?? _defaultSlots(action),
      );
      final binding = PlayerInputBinding.pointer(pointer);
      if (target[0] == binding || target[1] == binding) {
        parsed[action] = target;
        return;
      }
      if (target[0] == null) {
        target[0] = binding;
      } else {
        target[1] = binding;
      }
      parsed[action] = target;
    }

    applyWheel(value['wheelUp'], PlayerPointerInput.wheelUp);
    applyWheel(value['wheelDown'], PlayerPointerInput.wheelDown);
    return parsed;
  }

  static DesktopPlayerBindings _sanitize(
    Map<PlayerInputAction, List<PlayerInputBinding?>> overrides,
  ) {
    final clean = <PlayerInputAction, List<PlayerInputBinding?>>{
      for (final entry in overrides.entries)
        entry.key: List<PlayerInputBinding?>.of(entry.value),
    };
    final counts = <PlayerInputBinding, int>{};
    for (final entry in clean.entries) {
      for (final item in entry.value) {
        if (item != null) counts[item] = (counts[item] ?? 0) + 1;
      }
    }
    for (final action in clean.keys.toList()) {
      final list = clean[action]!;
      if (list.any((item) => item != null && (counts[item] ?? 0) > 1)) {
        clean.remove(action);
      }
    }
    var changed = true;
    while (changed) {
      changed = false;
      for (final action in PlayerInputAction.values) {
        if (clean.containsKey(action)) continue;
        final defaults = _defaultSlots(action);
        for (final other in clean.keys.toList()) {
          final list = clean[other]!;
          if (list.any((item) => item != null && defaults.contains(item))) {
            clean.remove(other);
            changed = true;
          }
        }
      }
    }
    clean.removeWhere(
      (action, list) => _sameSlots(list, _defaultSlots(action)),
    );
    return DesktopPlayerBindings(
      actions: Map.unmodifiable({
        for (final entry in clean.entries)
          entry.key: List<PlayerInputBinding?>.unmodifiable(entry.value),
      }),
    );
  }
}
