import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

enum PlayerInputAction {
  playPause('播放 / 暂停'),
  nextEpisode('下一集'),
  previousEpisode('上一集'),
  seekForward('快进'),
  seekBackward('快退'),
  volumeUp('增加音量'),
  volumeDown('降低音量'),
  mute('静音 / 恢复音量'),
  fullscreen('全屏（视频）'),
  none('不操作');

  const PlayerInputAction(this.label);
  final String label;

  bool get repeats => this == volumeUp || this == volumeDown;
  bool get episode => this == nextEpisode || this == previousEpisode;
}

enum PlayerInputScope { video, audio, portrait, mini }

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

  bool get assignable =>
      !{
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
      }.contains(key) &&
      !(alt && key == LogicalKeyboardKey.f4);

  String get label {
    final keyText = switch (key) {
      LogicalKeyboardKey.space => '空格',
      LogicalKeyboardKey.enter => 'Enter',
      LogicalKeyboardKey.numpadEnter => '小键盘 Enter',
      LogicalKeyboardKey.arrowLeft => '←',
      LogicalKeyboardKey.arrowRight => '→',
      LogicalKeyboardKey.arrowUp => '↑',
      LogicalKeyboardKey.arrowDown => '↓',
      LogicalKeyboardKey.mediaPlayPause => '媒体播放 / 暂停',
      LogicalKeyboardKey.mediaTrackNext => '媒体下一曲',
      LogicalKeyboardKey.mediaTrackPrevious => '媒体上一曲',
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

  SingleActivator activator({bool repeats = false}) => SingleActivator(
    key,
    control: control,
    alt: alt,
    shift: shift,
    meta: meta,
    includeRepeats: repeats,
  );

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

const _videoDefaults = <PlayerInputAction, List<PlayerKeyBinding>>{
  PlayerInputAction.playPause: [
    PlayerKeyBinding(LogicalKeyboardKey.space),
    PlayerKeyBinding(LogicalKeyboardKey.mediaPlayPause),
  ],
  PlayerInputAction.nextEpisode: [
    PlayerKeyBinding(LogicalKeyboardKey.pageDown),
    PlayerKeyBinding(LogicalKeyboardKey.mediaTrackNext),
  ],
  PlayerInputAction.previousEpisode: [
    PlayerKeyBinding(LogicalKeyboardKey.pageUp),
    PlayerKeyBinding(LogicalKeyboardKey.mediaTrackPrevious),
  ],
  PlayerInputAction.seekForward: [
    PlayerKeyBinding(LogicalKeyboardKey.arrowRight),
  ],
  PlayerInputAction.seekBackward: [
    PlayerKeyBinding(LogicalKeyboardKey.arrowLeft),
  ],
  PlayerInputAction.volumeUp: [PlayerKeyBinding(LogicalKeyboardKey.arrowUp)],
  PlayerInputAction.volumeDown: [
    PlayerKeyBinding(LogicalKeyboardKey.arrowDown),
  ],
  PlayerInputAction.mute: [PlayerKeyBinding(LogicalKeyboardKey.keyM)],
  PlayerInputAction.fullscreen: [
    PlayerKeyBinding(LogicalKeyboardKey.f11),
    PlayerKeyBinding(LogicalKeyboardKey.enter),
    PlayerKeyBinding(LogicalKeyboardKey.numpadEnter),
  ],
};

class DesktopPlayerBindings {
  const DesktopPlayerBindings({
    this.keys = const {},
    this.wheelUp,
    this.wheelDown,
  });
  final Map<PlayerInputAction, PlayerKeyBinding?> keys;
  final PlayerInputAction? wheelUp;
  final PlayerInputAction? wheelDown;
  bool get isDefault => keys.isEmpty && wheelUp == null && wheelDown == null;

  List<PlayerKeyBinding> bindings(
    PlayerInputAction action, [
    PlayerInputScope scope = PlayerInputScope.video,
  ]) {
    if (keys.containsKey(action)) {
      return [?keys[action]];
    }
    final defaults = _videoDefaults[action] ?? const [];
    return switch (scope) {
      PlayerInputScope.video => defaults,
      PlayerInputScope.portrait =>
        action == PlayerInputAction.fullscreen ? defaults : const [],
      PlayerInputScope.mini =>
        action == PlayerInputAction.playPause ? defaults : const [],
      PlayerInputScope.audio =>
        action == PlayerInputAction.fullscreen
            ? const []
            : defaults
                  .where(
                    (value) =>
                        value.key != LogicalKeyboardKey.mediaPlayPause &&
                        value.key != LogicalKeyboardKey.mediaTrackNext &&
                        value.key != LogicalKeyboardKey.mediaTrackPrevious,
                  )
                  .toList(),
    };
  }

  PlayerInputAction? resolve(KeyEvent event, PlayerInputScope scope) {
    for (final action in PlayerInputAction.values) {
      if (bindings(action, scope).any((binding) => binding.matches(event))) {
        return action;
      }
    }
    return null;
  }

  PlayerInputAction? conflict(PlayerKeyBinding key, PlayerInputAction action) {
    for (final other in PlayerInputAction.values) {
      if (other != action && bindings(other).contains(key)) return other;
    }
    return null;
  }

  PlayerInputAction? resetConflict(PlayerInputAction action) {
    for (final key in _videoDefaults[action] ?? const <PlayerKeyBinding>[]) {
      final other = conflict(key, action);
      if (other != null) return other;
    }
    return null;
  }

  PlayerInputAction? wheelConflict(bool up, PlayerInputAction action) {
    final owner = up ? wheelUp : wheelDown;
    return owner == null || owner == PlayerInputAction.none || owner == action
        ? null
        : owner;
  }

  List<String> labels(PlayerInputAction action) => [
    ...bindings(action).map((key) => key.label),
    if (wheelUp == action) '滚轮向上',
    if (wheelDown == action) '滚轮向下',
  ];

  DesktopPlayerBindings resetAction(PlayerInputAction action) {
    var result = resetKey(action);
    if (wheelUp == action) result = result.withWheel(up: true);
    if (wheelDown == action) result = result.withWheel(up: false);
    return result;
  }

  DesktopPlayerBindings recordKey(
    PlayerInputAction action,
    PlayerKeyBinding key,
  ) {
    var result = withKey(action, key);
    if (wheelUp == action) {
      result = result.withWheel(up: true, action: PlayerInputAction.none);
    }
    if (wheelDown == action) {
      result = result.withWheel(up: false, action: PlayerInputAction.none);
    }
    return result;
  }

  DesktopPlayerBindings recordWheel(
    PlayerInputAction action, {
    required bool up,
  }) {
    if (action == PlayerInputAction.fullscreen) return this;
    var result = withKey(action, null);
    if (wheelUp == action && !up) {
      result = result.withWheel(up: true, action: PlayerInputAction.none);
    }
    if (wheelDown == action && up) {
      result = result.withWheel(up: false, action: PlayerInputAction.none);
    }
    return result.withWheel(up: up, action: action);
  }

  DesktopPlayerBindings withKey(
    PlayerInputAction action,
    PlayerKeyBinding? key,
  ) => DesktopPlayerBindings(
    keys: Map.unmodifiable({...keys, action: key}),
    wheelUp: wheelUp,
    wheelDown: wheelDown,
  );
  DesktopPlayerBindings resetKey(PlayerInputAction action) =>
      DesktopPlayerBindings(
        keys: Map.unmodifiable({...keys}..remove(action)),
        wheelUp: wheelUp,
        wheelDown: wheelDown,
      );
  DesktopPlayerBindings withWheel({
    required bool up,
    PlayerInputAction? action,
  }) => DesktopPlayerBindings(
    keys: keys,
    wheelUp: up ? action : wheelUp,
    wheelDown: up ? wheelDown : action,
  );

  Map<String, Object?> toJson() => {
    'keys': {
      for (final entry in keys.entries) entry.key.name: entry.value?.toJson(),
    },
    if (wheelUp != null) 'wheelUp': wheelUp!.name,
    if (wheelDown != null) 'wheelDown': wheelDown!.name,
  };

  factory DesktopPlayerBindings.fromJson(Object? value) {
    if (value is! Map) return const DesktopPlayerBindings();
    final overrides = <PlayerInputAction, PlayerKeyBinding?>{};
    if (value['keys'] case final Map rows) {
      for (final entry in rows.entries) {
        final action = PlayerInputAction.values.asNameMap()[entry.key];
        if (action == null || action == PlayerInputAction.none) continue;
        if (entry.value == null) {
          overrides[action] = null;
          continue;
        }
        final binding = PlayerKeyBinding.fromJson(entry.value);
        if (binding != null) overrides[action] = binding;
      }
    }
    final clean = <PlayerInputAction, PlayerKeyBinding?>{
      for (final entry in overrides.entries)
        if (entry.value == null) entry.key: null,
    };
    final used = <PlayerKeyBinding>{
      for (final action in PlayerInputAction.values)
        if (!overrides.containsKey(action)) ...?_videoDefaults[action],
    };
    final validOverrides = <PlayerInputAction, PlayerKeyBinding>{};
    for (final entry in overrides.entries) {
      final binding = entry.value;
      if (binding != null && !used.contains(binding)) {
        validOverrides[entry.key] = binding;
      }
    }
    final counts = <PlayerKeyBinding, int>{};
    for (final binding in validOverrides.values) {
      counts[binding] = (counts[binding] ?? 0) + 1;
    }
    for (final entry in validOverrides.entries) {
      if (counts[entry.value] == 1) clean[entry.key] = entry.value;
    }
    var changed = true;
    while (changed) {
      changed = false;
      for (final action in PlayerInputAction.values) {
        if (clean.containsKey(action)) continue;
        final defaults = _videoDefaults[action] ?? const <PlayerKeyBinding>[];
        for (final other in clean.keys.toList()) {
          final binding = clean[other];
          if (binding != null && defaults.contains(binding)) {
            clean.remove(other);
            changed = true;
          }
        }
      }
    }
    PlayerInputAction? parseWheel(Object? raw) {
      final action = PlayerInputAction.values.asNameMap()[raw];
      return action == PlayerInputAction.fullscreen ? null : action;
    }

    return DesktopPlayerBindings(
      keys: Map.unmodifiable(clean),
      wheelUp: parseWheel(value['wheelUp']),
      wheelDown: parseWheel(value['wheelDown']),
    );
  }
}
