import 'package:duanju_app/desktop_player_bindings.dart';
import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/playback_preferences.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'support/fakes.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const defaults = DesktopPlayerBindings();
  const nextKey = PlayerInputBinding.key(
    PlayerKeyBinding(LogicalKeyboardKey.keyN, control: true),
  );
  const wheelUp = PlayerInputBinding.pointer(PlayerPointerInput.wheelUp);
  const mouseBack = PlayerInputBinding.pointer(PlayerPointerInput.mouseBack);
  const mouseForward = PlayerInputBinding.pointer(
    PlayerPointerInput.mouseForward,
  );
  const mouseMiddle = PlayerInputBinding.pointer(
    PlayerPointerInput.mouseMiddle,
  );

  test('defaults match unified two-slot layout', () {
    final preferences = PlaybackPreferences.fromJson({});
    expect(preferences.desktopBindings.isDefault, isTrue);
    expect(preferences.toJson().containsKey('desktopBindings'), isFalse);

    expect(defaults.labels(PlayerInputAction.playPause), ['空格']);
    expect(defaults.labels(PlayerInputAction.nextEpisode), ['Page Down']);
    expect(defaults.labels(PlayerInputAction.previousEpisode), ['Page Up']);
    expect(defaults.labels(PlayerInputAction.seekForward), ['→']);
    expect(defaults.labels(PlayerInputAction.seekBackward), ['←']);
    expect(defaults.labels(PlayerInputAction.volumeUp), ['↑', '滚轮向上']);
    expect(defaults.labels(PlayerInputAction.volumeDown), ['↓', '滚轮向下']);
    expect(defaults.labels(PlayerInputAction.mute), ['M']);
    expect(defaults.labels(PlayerInputAction.fullscreen), ['F11', 'Enter']);

    expect(
      defaults.resolveWheel(true, PlayerInputScope.video),
      PlayerInputAction.volumeUp,
    );
    expect(
      defaults.resolveWheel(false, PlayerInputScope.portrait),
      PlayerInputAction.volumeDown,
    );
  });

  test('media keys map directly without showing in or overriding slots', () {
    expect(
      const PlayerKeyBinding(LogicalKeyboardKey.mediaPlayPause).assignable,
      isFalse,
    );
    expect(
      const PlayerKeyBinding(LogicalKeyboardKey.mediaTrackNext).assignable,
      isFalse,
    );
    expect(
      const PlayerKeyBinding(LogicalKeyboardKey.mediaTrackPrevious).assignable,
      isFalse,
    );
    final cleared = defaults
        .setSlot(PlayerInputAction.playPause, 0, null)
        .setSlot(PlayerInputAction.nextEpisode, 0, null)
        .setSlot(PlayerInputAction.previousEpisode, 0, null);
    expect(cleared.labels(PlayerInputAction.playPause), isEmpty);
    const playPause = KeyDownEvent(
      physicalKey: PhysicalKeyboardKey.mediaPlayPause,
      logicalKey: LogicalKeyboardKey.mediaPlayPause,
      timeStamp: Duration.zero,
    );
    const trackNext = KeyDownEvent(
      physicalKey: PhysicalKeyboardKey.mediaTrackNext,
      logicalKey: LogicalKeyboardKey.mediaTrackNext,
      timeStamp: Duration.zero,
    );
    const trackPrev = KeyDownEvent(
      physicalKey: PhysicalKeyboardKey.mediaTrackPrevious,
      logicalKey: LogicalKeyboardKey.mediaTrackPrevious,
      timeStamp: Duration.zero,
    );
    for (final scope in PlayerInputScope.values) {
      expect(cleared.resolveKey(playPause, scope), PlayerInputAction.playPause);
      expect(
        cleared.resolveKey(trackNext, scope),
        PlayerInputAction.nextEpisode,
      );
      expect(
        cleared.resolveKey(trackPrev, scope),
        PlayerInputAction.previousEpisode,
      );
    }
  });

  test('fullscreen forbids wheel but accepts keyboard and mouse side keys', () {
    final wheelBlocked = defaults.setSlot(
      PlayerInputAction.fullscreen,
      1,
      wheelUp,
    );
    expect(wheelBlocked.isDefault, isTrue);

    final withMouse = defaults.setSlot(
      PlayerInputAction.fullscreen,
      1,
      mouseForward,
    );
    expect(withMouse.labels(PlayerInputAction.fullscreen), ['F11', '鼠标前进键']);
    expect(
      withMouse.resolvePointer(
        PlayerPointerInput.mouseForward,
        PlayerInputScope.video,
      ),
      PlayerInputAction.fullscreen,
    );
    expect(
      withMouse.resolvePointer(
        PlayerPointerInput.mouseForward,
        PlayerInputScope.audio,
      ),
      isNull,
    );
    expect(
      withMouse.resolvePointer(
        PlayerPointerInput.mouseForward,
        PlayerInputScope.mini,
      ),
      isNull,
    );
  });

  test('mouse buttons map from pointer bitmask and persist across slots', () {
    expect(
      PlayerPointerInput.fromButtons(kMiddleMouseButton),
      PlayerPointerInput.mouseMiddle,
    );
    expect(
      PlayerPointerInput.fromButtons(kBackMouseButton),
      PlayerPointerInput.mouseBack,
    );
    expect(
      PlayerPointerInput.fromButtons(kForwardMouseButton),
      PlayerPointerInput.mouseForward,
    );
    expect(PlayerPointerInput.fromButtons(kPrimaryMouseButton), isNull);

    final bindings = defaults
        .setSlot(PlayerInputAction.nextEpisode, 1, mouseForward)
        .setSlot(PlayerInputAction.previousEpisode, 1, mouseBack)
        .setSlot(PlayerInputAction.playPause, 1, mouseMiddle);
    final restored = DesktopPlayerBindings.fromJson(bindings.toJson());
    expect(restored.labels(PlayerInputAction.nextEpisode), [
      'Page Down',
      '鼠标前进键',
    ]);
    expect(restored.labels(PlayerInputAction.previousEpisode), [
      'Page Up',
      '鼠标后退键',
    ]);
    expect(restored.labels(PlayerInputAction.playPause), ['空格', '鼠标中键']);
    expect(
      restored.resolvePointer(
        PlayerPointerInput.mouseBack,
        PlayerInputScope.video,
      ),
      PlayerInputAction.previousEpisode,
    );
  });

  test('every input can only bind one function and one slot', () {
    expect(
      defaults.conflict(wheelUp, PlayerInputAction.previousEpisode, slot: 1),
      PlayerInputAction.volumeUp,
    );
    expect(
      defaults.conflict(
        const PlayerInputBinding.key(PlayerKeyBinding(LogicalKeyboardKey.f11)),
        PlayerInputAction.fullscreen,
        slot: 1,
      ),
      PlayerInputAction.fullscreen,
    );
    expect(
      defaults.conflict(
        const PlayerInputBinding.key(PlayerKeyBinding(LogicalKeyboardKey.f11)),
        PlayerInputAction.fullscreen,
        slot: 0,
      ),
      isNull,
    );

    final moved = defaults
        .setSlot(PlayerInputAction.volumeUp, 1, null)
        .setSlot(PlayerInputAction.previousEpisode, 1, wheelUp);
    expect(
      moved.resolveWheel(true, PlayerInputScope.video),
      PlayerInputAction.previousEpisode,
    );
    expect(
      moved.resetConflict(PlayerInputAction.volumeUp),
      PlayerInputAction.previousEpisode,
    );
  });

  test('all arrow directions can be assigned to any action', () {
    final cleared = PlayerInputAction.values.fold(
      defaults,
      (current, action) =>
          current.setSlot(action, 0, null).setSlot(action, 1, null),
    );
    for (final key in [
      LogicalKeyboardKey.arrowUp,
      LogicalKeyboardKey.arrowDown,
      LogicalKeyboardKey.arrowLeft,
      LogicalKeyboardKey.arrowRight,
    ]) {
      final binding = PlayerInputBinding.key(PlayerKeyBinding(key));
      expect(binding.key!.assignable, isTrue);
      for (final action in PlayerInputAction.values) {
        final saved = DesktopPlayerBindings.fromJson(
          cleared.setSlot(action, 0, binding).toJson(),
        );
        expect(saved.slots(action)[0], binding);
      }
    }
  });

  test('setting slots back to defaults restores isDefault', () {
    final changed = defaults.setSlot(PlayerInputAction.nextEpisode, 0, nextKey);
    expect(changed.isDefault, isFalse);
    final restored = changed.setSlot(
      PlayerInputAction.nextEpisode,
      0,
      const PlayerInputBinding.key(
        PlayerKeyBinding(LogicalKeyboardKey.pageDown),
      ),
    );
    expect(restored.isDefault, isTrue);
    expect(
      changed.resetAction(PlayerInputAction.nextEpisode).isDefault,
      isTrue,
    );
  });

  test('legacy v1 config migrates cleanly to two-slot bindings', () {
    final migrated = DesktopPlayerBindings.fromJson({
      'keys': {'nextEpisode': nextKey.key!.toJson(), 'mute': null},
      'wheelUp': 'previousEpisode',
      'wheelDown': 'none',
    });
    expect(migrated.labels(PlayerInputAction.nextEpisode), ['Ctrl + N']);
    expect(migrated.labels(PlayerInputAction.mute), isEmpty);
    expect(migrated.labels(PlayerInputAction.previousEpisode), [
      'Page Up',
      '滚轮向上',
    ]);
    expect(migrated.labels(PlayerInputAction.volumeUp), ['↑']);
    expect(migrated.labels(PlayerInputAction.volumeDown), ['↓']);
    expect(migrated.resolveWheel(false, PlayerInputScope.video), isNull);
  });

  test(
    'invalid, unknown, and conflicting persisted bindings fall back safely',
    () {
      final bindings = DesktopPlayerBindings.fromJson({
        'v': 2,
        'actions': {
          'nextEpisode': [
            const PlayerInputBinding.key(
              PlayerKeyBinding(LogicalKeyboardKey.space),
            ).toJson(),
            null,
          ],
          'previousEpisode': [
            {'type': 'key', 'key': -1},
            null,
          ],
          'unknown': [nextKey.toJson(), null],
          'fullscreen': [wheelUp.toJson(), mouseForward.toJson()],
        },
      });
      expect(
        bindings.actions.containsKey(PlayerInputAction.nextEpisode),
        isFalse,
      );
      expect(bindings.labels(PlayerInputAction.fullscreen), ['鼠标前进键']);
      expect(DesktopPlayerBindings.fromJson('broken').isDefault, isTrue);
      expect(DesktopPlayerBindings.fromJson({'actions': 42}).isDefault, isTrue);
    },
  );

  test('duplicate custom bindings in persisted config both fall back', () {
    final bindings = DesktopPlayerBindings.fromJson({
      'v': 2,
      'actions': {
        'nextEpisode': [nextKey.toJson(), null],
        'previousEpisode': [nextKey.toJson(), null],
        'mute': [mouseBack.toJson(), null],
      },
    });
    expect(
      bindings.actions.containsKey(PlayerInputAction.nextEpisode),
      isFalse,
    );
    expect(
      bindings.actions.containsKey(PlayerInputAction.previousEpisode),
      isFalse,
    );
    expect(bindings.labels(PlayerInputAction.mute), ['鼠标后退键']);
  });

  test('swapped defaults survive persistence and detect reset conflict', () {
    const pageUp = PlayerInputBinding.key(
      PlayerKeyBinding(LogicalKeyboardKey.pageUp),
    );
    const pageDown = PlayerInputBinding.key(
      PlayerKeyBinding(LogicalKeyboardKey.pageDown),
    );
    final swapped = defaults
        .setSlot(PlayerInputAction.nextEpisode, 0, pageUp)
        .setSlot(PlayerInputAction.previousEpisode, 0, pageDown);
    final restored = DesktopPlayerBindings.fromJson(swapped.toJson());
    expect(restored.slots(PlayerInputAction.nextEpisode)[0], pageUp);
    expect(restored.slots(PlayerInputAction.previousEpisode)[0], pageDown);
    expect(
      restored.resetConflict(PlayerInputAction.nextEpisode),
      PlayerInputAction.previousEpisode,
    );
  });

  test('reserved navigation and window closing keys cannot be assigned', () {
    expect(
      const PlayerKeyBinding(LogicalKeyboardKey.escape).assignable,
      isFalse,
    );
    expect(const PlayerKeyBinding(LogicalKeyboardKey.tab).assignable, isFalse);
    expect(
      const PlayerKeyBinding(LogicalKeyboardKey.shiftLeft).assignable,
      isFalse,
    );
    expect(
      const PlayerKeyBinding(LogicalKeyboardKey.f4, alt: true).assignable,
      isFalse,
    );
    expect(
      const PlayerKeyBinding(LogicalKeyboardKey.f4, control: true).assignable,
      isTrue,
    );
  });

  test('per-user settings survive reopening the local store', () async {
    final files = MemoryFiles();
    final store = await openStore(const {}, null, files);
    await store.setPlaybackPreferences(
      store.playbackPreferences.copyWith(
        desktopBindings: defaults
            .setSlot(PlayerInputAction.nextEpisode, 0, nextKey)
            .setSlot(PlayerInputAction.nextEpisode, 1, mouseForward),
      ),
    );
    final reopened = await LocalStore.open(
      await SharedPreferences.getInstance(),
      files,
    );
    addTearDown(reopened.dispose);
    expect(
      reopened.playbackPreferences.desktopBindings.toJson(),
      store.playbackPreferences.desktopBindings.toJson(),
    );
    await reopened.setPlaybackPreferences(
      reopened.playbackPreferences.copyWith(desktopBindings: defaults),
    );
    final reset = await LocalStore.open(
      await SharedPreferences.getInstance(),
      files,
    );
    addTearDown(reset.dispose);
    expect(reset.playbackPreferences.desktopBindings.isDefault, isTrue);
  });
}
