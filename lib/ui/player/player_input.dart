import 'dart:async';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

import '../../desktop_player_bindings.dart';
import '../../local_store.dart';
import '../../playback_session.dart';
import '../app_scope.dart';

class PlayerInputController {
  PlayerInputController({
    required this.session,
    this.onFullscreen,
    this.onHint,
    this.seekStep = 5,
  });
  final PlaybackSession Function() session;
  final VoidCallback? onFullscreen;
  final ValueChanged<String>? onHint;
  final int seekStep;
  DateTime? _lastWheelEpisode;
  PlayerInputAction? _lastWheelAction;

  DesktopPlayerBindings get bindings => desktopPlatform
      ? session().preferences.desktopBindings
      : const DesktopPlayerBindings();

  void invoke(PlayerInputAction action) {
    final playback = session();
    if (playback.locked) return;
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
        onHint?.call('音量 ${volume.round()}%');
      case PlayerInputAction.mute:
        final volume = playback.toggleMute();
        onHint?.call(volume == 0 ? '已静音' : '音量 ${volume.round()}%');
      case PlayerInputAction.fullscreen:
        onFullscreen?.call();
      case PlayerInputAction.none:
        break;
    }
  }

  bool wheel(PointerSignalEvent event, BuildContext context) {
    if (!desktopPlatform ||
        event is! PointerScrollEvent ||
        event.scrollDelta.dy == 0) {
      return false;
    }
    final action = event.scrollDelta.dy < 0
        ? bindings.wheelUp
        : bindings.wheelDown;
    if (action == null) return false;
    GestureBinding.instance.pointerSignalResolver.register(event, (_) {
      if (!context.mounted ||
          ModalRoute.of(context)?.isCurrent == false ||
          session().locked) {
        return;
      }
      if (action.episode) {
        final now = DateTime.now();
        final previous = _lastWheelEpisode;
        _lastWheelEpisode = now;
        final previousAction = _lastWheelAction;
        _lastWheelAction = action;
        if (previousAction == action &&
            previous != null &&
            now.difference(previous) < const Duration(milliseconds: 500)) {
          return;
        }
      }
      invoke(action);
    });
    return true;
  }
}

class DesktopPlayerShortcuts extends StatelessWidget {
  const DesktopPlayerShortcuts({
    super.key,
    required this.store,
    required this.controller,
    required this.fallback,
    required this.child,
  });
  final LocalStore store;
  final PlayerInputController controller;
  final Map<ShortcutActivator, VoidCallback> fallback;
  final Widget child;

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: Listenable.merge([store, FocusManager.instance]),
    builder: (context, _) => CallbackShortcuts(
      bindings:
          desktopPlatform &&
              controller.bindings.keys.isNotEmpty &&
              FocusManager.instance.primaryFocus?.context
                      ?.findAncestorWidgetOfExactType<EditableText>() !=
                  null
          ? const {}
          : desktopPlatform && controller.bindings.keys.isNotEmpty
          ? {
              for (final action in PlayerInputAction.values)
                if (action != PlayerInputAction.fullscreen)
                  for (final binding in controller.bindings.bindings(
                    action,
                    PlayerInputScope.audio,
                  ))
                    binding.activator(
                      repeats:
                          !controller.bindings.keys.containsKey(action) ||
                          action.repeats ||
                          action == PlayerInputAction.seekForward ||
                          action == PlayerInputAction.seekBackward,
                    ): () =>
                        controller.invoke(action),
            }
          : fallback,
      child: child,
    ),
  );
}

class PlayerWheelRegion extends StatelessWidget {
  const PlayerWheelRegion({
    super.key,
    required this.controller,
    required this.child,
  });
  final PlayerInputController controller;
  final Widget child;
  @override
  Widget build(BuildContext context) => Listener(
    behavior: HitTestBehavior.opaque,
    onPointerSignal: (event) => controller.wheel(event, context),
    child: child,
  );
}
