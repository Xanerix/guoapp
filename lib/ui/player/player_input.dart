import 'dart:async';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../desktop_player_bindings.dart';
import '../../local_store.dart';
import '../../playback_session.dart';
import '../app_scope.dart';
import '../widgets/focus_ring.dart';

class PlayerPointerClaim {
  static PointerDownEvent? _handled;

  static void claim(PointerDownEvent event) => _handled = event;

  static bool isClaimed(PointerDownEvent event) => identical(_handled, event);
}

class PlayerInputController {
  PlayerInputController({
    required this.session,
    required this.scope,
    this.onFullscreen,
    this.onHint,
    this.seekStep = 5,
  });
  final PlaybackSession Function() session;
  final PlayerInputScope scope;
  final VoidCallback? onFullscreen;
  final ValueChanged<String>? onHint;
  final int seekStep;
  final _wheelClock = Stopwatch()..start();
  Duration? _lastWheelEpisode;

  bool _acceptEpisodeWheel() {
    final now = _wheelClock.elapsed;
    final previous = _lastWheelEpisode;
    _lastWheelEpisode = now;
    return !session().loading &&
        (previous == null ||
            now - previous >= const Duration(milliseconds: 700));
  }

  DesktopPlayerBindings get bindings => desktopPlatform
      ? session().preferences.desktopBindings
      : const DesktopPlayerBindings();

  void invoke(PlayerInputAction action) {
    final playback = session();
    if (playback.locked) {
      if (action == PlayerInputAction.fullscreen) onFullscreen?.call();
      return;
    }
    switch (action) {
      case PlayerInputAction.playPause:
        playback.togglePlayback();
      case PlayerInputAction.nextEpisode || PlayerInputAction.previousEpisode:
        final direction = action == PlayerInputAction.nextEpisode ? 1 : -1;
        final index = playback.index + direction;
        if (index < 0 || index >= playback.detail.episodes.length) {
          onHint?.call(index < 0 ? '已经是第一集' : '已经是最后一集');
          return;
        }
        unawaited(playback.play(index));
        onHint?.call('第 ${playback.detail.episodes[index].number} 集');
      case PlayerInputAction.seekForward || PlayerInputAction.seekBackward:
        final seconds = action == PlayerInputAction.seekForward
            ? seekStep
            : -seekStep;
        if (playback.seekBy(seconds) != null) {
          onHint?.call('${seconds > 0 ? '快进' : '快退'} ${seconds.abs()} 秒');
        }
      case PlayerInputAction.volumeUp || PlayerInputAction.volumeDown:
        final volume = playback.changeVolume(
          action == PlayerInputAction.volumeUp ? 5 : -5,
        );
        onHint?.call(volume == 0 ? '已静音' : '音量 ${volume.round()}%');
      case PlayerInputAction.mute:
        final volume = playback.toggleMute();
        onHint?.call(volume == 0 ? '已静音' : '音量 ${volume.round()}%');
      case PlayerInputAction.fullscreen:
        onFullscreen?.call();
    }
  }

  bool wheel(PointerSignalEvent event, BuildContext context) {
    if (!desktopPlatform ||
        event is! PointerScrollEvent ||
        event.scrollDelta.dy == 0) {
      return false;
    }
    final action = bindings.resolveWheel(event.scrollDelta.dy < 0, scope);
    if (action == null) return false;
    GestureBinding.instance.pointerSignalResolver.register(event, (_) {
      if (!context.mounted ||
          ModalRoute.of(context)?.isCurrent == false ||
          session().locked) {
        return;
      }
      if (action.episode && !_acceptEpisodeWheel()) return;
      invoke(action);
    });
    return true;
  }

  bool pointerDown(PointerDownEvent event, BuildContext context) {
    if (!desktopPlatform || PlayerPointerClaim.isClaimed(event)) return false;
    final input = PlayerPointerInput.fromButtons(event.buttons);
    if (input == null) return false;
    final action = bindings.resolvePointer(input, scope);
    if (action == null) return false;
    if (!context.mounted || ModalRoute.of(context)?.isCurrent == false) {
      return false;
    }
    PlayerPointerClaim.claim(event);
    invoke(action);
    return true;
  }
}

class DesktopPlayerShortcuts extends StatelessWidget {
  const DesktopPlayerShortcuts({
    super.key,
    required this.store,
    required this.controller,
    this.repeatSeek = true,
    required this.child,
  });
  final LocalStore store;
  final PlayerInputController controller;
  final bool repeatSeek;
  final Widget child;

  KeyEventResult _onKey(BuildContext context, KeyEvent event) {
    if (!desktopPlatform ||
        ModalRoute.of(context)?.isCurrent == false ||
        focusedTextField() != null) {
      return KeyEventResult.ignored;
    }
    final action = controller.bindings.resolveKey(event, controller.scope);
    if (action == null) return KeyEventResult.ignored;
    final down = event is KeyDownEvent;
    final repeat =
        action.repeats ||
        repeatSeek &&
            (action == PlayerInputAction.seekForward ||
                action == PlayerInputAction.seekBackward);
    if (down || repeat && event is KeyRepeatEvent) {
      controller.invoke(action);
    }
    return KeyEventResult.handled;
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: store,
    builder: (context, _) => KeyboardControls(
      handles: (event) =>
          desktopPlatform &&
          ModalRoute.of(context)?.isCurrent != false &&
          focusedTextField() == null &&
          controller.bindings.resolveKey(event, controller.scope) != null,
      child: Focus(
        autofocus: true,
        onKeyEvent: (_, event) => _onKey(context, event),
        child: child,
      ),
    ),
  );
}

class PlayerPointerRegion extends StatelessWidget {
  const PlayerPointerRegion({
    super.key,
    required this.controller,
    this.wheel = true,
    this.enabled = true,
    required this.child,
  });
  final PlayerInputController controller;
  final bool wheel;
  final bool enabled;
  final Widget child;

  @override
  Widget build(BuildContext context) => Listener(
    behavior: HitTestBehavior.translucent,
    onPointerSignal: enabled && wheel
        ? (event) => controller.wheel(event, context)
        : null,
    onPointerDown: enabled
        ? (event) => controller.pointerDown(event, context)
        : null,
    child: child,
  );
}
