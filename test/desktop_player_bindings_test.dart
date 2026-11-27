import 'package:duanju_app/desktop_player_bindings.dart';
import 'package:duanju_app/playback_preferences.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/fakes.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const defaults = DesktopPlayerBindings();
  const nextKey = PlayerKeyBinding(LogicalKeyboardKey.keyN, control: true);

  test('wheel recording keeps the upstream fullscreen restriction', () {
    final bindings = defaults.withKey(PlayerInputAction.fullscreen, nextKey);
    final recorded = bindings.recordWheel(
      PlayerInputAction.fullscreen,
      up: true,
    );
    expect(recorded.toJson(), bindings.toJson());
    expect(recorded.bindings(PlayerInputAction.fullscreen), [nextKey]);
  });

  test(
    'all arrow directions can be assigned and persist as action mappings',
    () {
      for (final key in [
        LogicalKeyboardKey.arrowUp,
        LogicalKeyboardKey.arrowDown,
        LogicalKeyboardKey.arrowLeft,
        LogicalKeyboardKey.arrowRight,
      ]) {
        final binding = PlayerKeyBinding(key);
        expect(binding.assignable, isTrue);
        expect(PlayerKeyBinding.fromJson(binding.toJson()), binding);
        final initial = DesktopPlayerBindings(
          keys: {for (final action in PlayerInputAction.values) action: null},
        );
        for (final action in PlayerInputAction.values.where(
          (value) => value != PlayerInputAction.none,
        )) {
          final saved = DesktopPlayerBindings.fromJson(
            initial.recordKey(action, binding).toJson(),
          );
          expect(saved.bindings(action), [binding]);
        }
      }
    },
  );

  test('recording a wheel replaces all keyboard aliases for that action', () {
    final recorded = defaults.recordWheel(
      PlayerInputAction.nextEpisode,
      up: false,
    );
    expect(recorded.bindings(PlayerInputAction.nextEpisode), isEmpty);
    expect(recorded.labels(PlayerInputAction.nextEpisode), ['滚轮向下']);
    expect(
      recorded.bindings(PlayerInputAction.playPause),
      defaults.bindings(PlayerInputAction.playPause),
    );
  });

  test('recording a keyboard input disables the old wheel for the action', () {
    final recorded = defaults
        .recordWheel(PlayerInputAction.nextEpisode, up: false)
        .recordKey(PlayerInputAction.nextEpisode, nextKey);
    expect(recorded.labels(PlayerInputAction.nextEpisode), ['Ctrl + N']);
    expect(recorded.wheelDown, PlayerInputAction.none);
  });

  test(
    'recording the other wheel direction replaces the previous direction',
    () {
      final recorded = defaults
          .recordWheel(PlayerInputAction.nextEpisode, up: false)
          .recordWheel(PlayerInputAction.nextEpisode, up: true);
      expect(recorded.labels(PlayerInputAction.nextEpisode), ['滚轮向上']);
      expect(recorded.wheelDown, PlayerInputAction.none);
      expect(
        DesktopPlayerBindings.fromJson(
          recorded.toJson(),
        ).labels(PlayerInputAction.nextEpisode),
        ['滚轮向上'],
      );
    },
  );

  test(
    'wheel capture preserves keyboard defaults and shows both on the action',
    () {
      final bindings = defaults.withWheel(
        up: false,
        action: PlayerInputAction.nextEpisode,
      );
      expect(bindings.keys, isEmpty);
      expect(
        bindings.bindings(PlayerInputAction.nextEpisode),
        defaults.bindings(PlayerInputAction.nextEpisode),
      );
      expect(bindings.labels(PlayerInputAction.nextEpisode), contains('滚轮向下'));
      expect(
        bindings.labels(PlayerInputAction.volumeDown),
        isNot(contains('滚轮向下')),
      );
    },
  );

  test(
    'explicit wheel ownership conflicts across functions but not within the owner',
    () {
      final bindings = defaults
          .withWheel(up: false, action: PlayerInputAction.nextEpisode)
          .withWheel(up: true, action: PlayerInputAction.previousEpisode);
      expect(
        bindings.wheelConflict(false, PlayerInputAction.playPause),
        PlayerInputAction.nextEpisode,
      );
      expect(
        bindings.wheelConflict(true, PlayerInputAction.seekForward),
        PlayerInputAction.previousEpisode,
      );
      expect(
        bindings.wheelConflict(false, PlayerInputAction.nextEpisode),
        isNull,
      );
      expect(
        defaults.wheelConflict(false, PlayerInputAction.nextEpisode),
        isNull,
      );
    },
  );

  test(
    'resetting one function releases its wheel while preserving other choices',
    () {
      final bindings = defaults
          .withKey(PlayerInputAction.nextEpisode, nextKey)
          .withWheel(up: false, action: PlayerInputAction.nextEpisode)
          .withWheel(up: true, action: PlayerInputAction.previousEpisode);
      final reset = bindings.resetAction(PlayerInputAction.nextEpisode);
      expect(reset.keys.containsKey(PlayerInputAction.nextEpisode), isFalse);
      expect(reset.wheelDown, isNull);
      expect(reset.wheelUp, PlayerInputAction.previousEpisode);
    },
  );

  test(
    'old separate wheel configurations remain compatible with unified labels',
    () {
      final restored = DesktopPlayerBindings.fromJson({
        'keys': {},
        'wheelDown': 'nextEpisode',
        'wheelUp': 'previousEpisode',
      });
      expect(restored.labels(PlayerInputAction.nextEpisode), contains('滚轮向下'));
      expect(
        restored.labels(PlayerInputAction.previousEpisode),
        contains('滚轮向上'),
      );
      expect(
        DesktopPlayerBindings.fromJson(restored.toJson()).toJson(),
        restored.toJson(),
      );
      expect(
        restored
            .withWheel(up: false, action: PlayerInputAction.none)
            .wheelConflict(false, PlayerInputAction.playPause),
        isNull,
      );
    },
  );

  test('existing profiles retain default keys and wheel behavior', () {
    final preferences = PlaybackPreferences.fromJson({});
    expect(preferences.desktopBindings.isDefault, isTrue);
    expect(preferences.toJson().containsKey('desktopBindings'), isFalse);
    expect(defaults.wheelUp, isNull);
    expect(defaults.wheelDown, isNull);
    expect(
      defaults.bindings(PlayerInputAction.nextEpisode),
      contains(const PlayerKeyBinding(LogicalKeyboardKey.pageDown)),
    );
    expect(
      defaults.bindings(PlayerInputAction.previousEpisode),
      contains(const PlayerKeyBinding(LogicalKeyboardKey.pageUp)),
    );
    expect(defaults.bindings(PlayerInputAction.seekForward), [
      const PlayerKeyBinding(LogicalKeyboardKey.arrowRight),
    ]);
  });

  test('defaults preserve the individual player scopes', () {
    expect(
      defaults.bindings(PlayerInputAction.nextEpisode, PlayerInputScope.mini),
      isEmpty,
    );
    expect(
      defaults.bindings(
        PlayerInputAction.nextEpisode,
        PlayerInputScope.portrait,
      ),
      isEmpty,
    );
    expect(
      defaults.bindings(PlayerInputAction.playPause, PlayerInputScope.audio),
      [const PlayerKeyBinding(LogicalKeyboardKey.space)],
    );
    expect(
      defaults.bindings(PlayerInputAction.fullscreen, PlayerInputScope.audio),
      isEmpty,
    );
    expect(
      defaults
          .bindings(PlayerInputAction.playPause, PlayerInputScope.mini)
          .length,
      2,
    );
  });

  test('custom keyboard input replaces the action aliases in every scope', () {
    final bindings = defaults.withKey(PlayerInputAction.nextEpisode, nextKey);
    for (final scope in PlayerInputScope.values) {
      expect(bindings.bindings(PlayerInputAction.nextEpisode, scope), [
        nextKey,
      ]);
    }
    expect(
      bindings.bindings(PlayerInputAction.playPause),
      defaults.bindings(PlayerInputAction.playPause),
    );
    expect(
      bindings.conflict(nextKey, PlayerInputAction.previousEpisode),
      PlayerInputAction.nextEpisode,
    );
    expect(
      bindings.conflict(
        const PlayerKeyBinding(LogicalKeyboardKey.space),
        PlayerInputAction.nextEpisode,
      ),
      PlayerInputAction.playPause,
    );
  });

  test('unbinding and resetting an action preserve other custom choices', () {
    final bindings = defaults
        .withKey(PlayerInputAction.nextEpisode, nextKey)
        .withKey(PlayerInputAction.playPause, null)
        .withWheel(up: false, action: PlayerInputAction.nextEpisode);
    expect(bindings.bindings(PlayerInputAction.playPause), isEmpty);
    final restored = bindings.resetKey(PlayerInputAction.playPause);
    expect(
      restored.bindings(PlayerInputAction.playPause),
      defaults.bindings(PlayerInputAction.playPause),
    );
    expect(restored.bindings(PlayerInputAction.nextEpisode), [nextKey]);
    expect(restored.wheelDown, PlayerInputAction.nextEpisode);
  });

  test('wheel directions are independent and can return to default', () {
    final bindings = defaults
        .withWheel(up: true, action: PlayerInputAction.previousEpisode)
        .withWheel(up: false, action: PlayerInputAction.nextEpisode);
    final restored = bindings.withWheel(up: false);
    expect(restored.wheelDown, isNull);
    expect(restored.wheelUp, PlayerInputAction.previousEpisode);
    expect(
      defaults.withWheel(up: true, action: PlayerInputAction.none).wheelUp,
      PlayerInputAction.none,
    );
  });

  test('custom configuration survives playback preference serialization', () {
    final preferences = const PlaybackPreferences(speed: 1.5).copyWith(
      desktopBindings: defaults
          .withKey(PlayerInputAction.nextEpisode, nextKey)
          .withWheel(up: false, action: PlayerInputAction.nextEpisode),
    );
    final restored = PlaybackPreferences.fromJson(preferences.toJson());
    expect(restored.speed, 1.5);
    expect(restored.desktopBindings.bindings(PlayerInputAction.nextEpisode), [
      nextKey,
    ]);
    expect(restored.desktopBindings.wheelDown, PlayerInputAction.nextEpisode);
    expect(
      restored.desktopBindings.toJson(),
      preferences.desktopBindings.toJson(),
    );
  });

  test(
    'invalid, unknown and conflicting persisted bindings fall back safely',
    () {
      final bindings = DesktopPlayerBindings.fromJson({
        'keys': {
          'nextEpisode': {'key': LogicalKeyboardKey.space.keyId},
          'previousEpisode': {'key': -1},
          'unknown': nextKey.toJson(),
          'mute': {'key': LogicalKeyboardKey.escape.keyId},
        },
        'wheelUp': 'fullscreen',
        'wheelDown': 'nextEpisode',
      });
      expect(bindings.keys, isEmpty);
      expect(bindings.wheelUp, isNull);
      expect(bindings.wheelDown, PlayerInputAction.nextEpisode);
      expect(DesktopPlayerBindings.fromJson('broken').isDefault, isTrue);
      expect(DesktopPlayerBindings.fromJson({'keys': 42}).isDefault, isTrue);
    },
  );

  test(
    'duplicate custom keys in persisted config both fall back deterministically',
    () {
      final bindings = DesktopPlayerBindings.fromJson({
        'keys': {
          'nextEpisode': nextKey.toJson(),
          'previousEpisode': nextKey.toJson(),
          'mute': const PlayerKeyBinding(LogicalKeyboardKey.keyK).toJson(),
        },
      });
      expect(bindings.keys.containsKey(PlayerInputAction.nextEpisode), isFalse);
      expect(
        bindings.keys.containsKey(PlayerInputAction.previousEpisode),
        isFalse,
      );
      expect(bindings.bindings(PlayerInputAction.mute), [
        const PlayerKeyBinding(LogicalKeyboardKey.keyK),
      ]);
    },
  );

  test(
    'swapped default keys survive persistence and detect reset conflict',
    () {
      final swapped = defaults
          .withKey(
            PlayerInputAction.nextEpisode,
            const PlayerKeyBinding(LogicalKeyboardKey.pageUp),
          )
          .withKey(
            PlayerInputAction.previousEpisode,
            const PlayerKeyBinding(LogicalKeyboardKey.pageDown),
          );
      final restored = DesktopPlayerBindings.fromJson(swapped.toJson());
      expect(restored.bindings(PlayerInputAction.nextEpisode), [
        const PlayerKeyBinding(LogicalKeyboardKey.pageUp),
      ]);
      expect(restored.bindings(PlayerInputAction.previousEpisode), [
        const PlayerKeyBinding(LogicalKeyboardKey.pageDown),
      ]);
      expect(
        restored.resetConflict(PlayerInputAction.nextEpisode),
        PlayerInputAction.previousEpisode,
      );
    },
  );

  test(
    'resetConflict detects default key occupied by another custom action',
    () {
      final bindings = defaults
          .withKey(PlayerInputAction.playPause, null)
          .withKey(
            PlayerInputAction.nextEpisode,
            const PlayerKeyBinding(LogicalKeyboardKey.space),
          );
      expect(
        bindings.resetConflict(PlayerInputAction.playPause),
        PlayerInputAction.nextEpisode,
      );
      expect(bindings.resetConflict(PlayerInputAction.nextEpisode), isNull);
    },
  );

  test('reserved navigation and window closing keys cannot be assigned', () {
    expect(
      const PlayerKeyBinding(LogicalKeyboardKey.escape).assignable,
      isFalse,
    );
    expect(const PlayerKeyBinding(LogicalKeyboardKey.tab).assignable, isFalse);
    expect(
      const PlayerKeyBinding(LogicalKeyboardKey.controlLeft).assignable,
      isFalse,
    );
    expect(
      const PlayerKeyBinding(LogicalKeyboardKey.f4, alt: true).assignable,
      isFalse,
    );
    expect(nextKey.assignable, isTrue);
  });

  test('per-user settings survive reopening the local store', () async {
    final store = await openStore();
    await store.setPlaybackPreferences(
      store.playbackPreferences.copyWith(
        desktopBindings: defaults
            .withKey(PlayerInputAction.nextEpisode, nextKey)
            .withWheel(up: false, action: PlayerInputAction.nextEpisode),
      ),
    );
    final restarted = await restartStore(store);
    expect(
      restarted.playbackPreferences.desktopBindings.toJson(),
      store.playbackPreferences.desktopBindings.toJson(),
    );
    await restarted.setPlaybackPreferences(
      restarted.playbackPreferences.copyWith(desktopBindings: defaults),
    );
    expect(
      (await restartStore(
        restarted,
      )).playbackPreferences.desktopBindings.isDefault,
      isTrue,
    );
  });
}
