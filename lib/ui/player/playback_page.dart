import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:media_kit/media_kit.dart';

import '../../app_device.dart';
import '../../desktop_player_bindings.dart';
import '../../local_store.dart';
import '../../picture_in_picture.dart';
import '../../playback_session.dart';
import '../app_scope.dart';
import '../widgets/focus_ring.dart';
import 'audio_player_page.dart';
import 'landscape_player_page.dart';
import 'mini_player.dart';
import 'playback_handle.dart';
import 'player_page.dart';
import 'portrait_player_page.dart';
import 'television_player_page.dart';
import 'window_fullscreen.dart';
import 'player_input.dart';

class PlaybackPage extends StatefulWidget {
  const PlaybackPage({super.key, required this.store, required this.handle});
  final LocalStore store;
  final PlaybackHandle handle;

  @override
  State<PlaybackPage> createState() => _PlaybackPageState();
}

class _PlaybackPageState extends State<PlaybackPage> {
  final _padFullscreen = ValueNotifier(false);
  late final _inputs = PlayerInputController(
    session: () => _session,
    onFullscreen: () => _padFullscreen.value = true,
  );
  bool _windowFullscreen = false;
  final _pageScope = FocusScopeNode(debugLabel: 'playbackPage');
  final _pip = PictureInPicture.instance;
  late bool _wideVideo = _measure();
  late bool _audio = _session.audio;
  MediaQueryData? _media;
  bool _shown = false;
  bool _locked = false;
  Player? _bound;
  StreamSubscription<bool>? _playing;

  PlaybackSession get _session => widget.handle.session;

  bool _measure() =>
      _session.aspectKnown && _session.aspectRatio >= 1 && !_session.audio;

  bool get _television =>
      AppScope.formFactorOf(context) == FormFactor.television;

  @override
  void initState() {
    super.initState();
    _session.addListener(_changed);
    _padFullscreen.addListener(_padFullscreenChanged);
    _pip.addListener(_pipChanged);
    _pip.onAction = _action;
    _bind();
    WidgetsBinding.instance.addPostFrameCallback((_) => _sync());
  }

  @override
  void dispose() {
    _session.removeListener(_changed);
    _pip.removeListener(_pipChanged);
    if (_pip.onAction == _action) _pip.onAction = null;
    _pip.clear();
    if (_pip.window) unawaited(_restore());
    unawaited(_playing?.cancel());
    _pageScope.dispose();
    if (_padFullscreen.value) _applyPadFullscreen(false);
    _padFullscreen.dispose();
    unawaited(AppDevice.resetBrightness());
    widget.handle.dispose();
    super.dispose();
  }

  void _changed() {
    _bind();
    _sync();
    final wide = _measure();
    final audio = _session.audio;
    if (wide != _wideVideo || audio != _audio) {
      setState(() {
        _wideVideo = wide;
        _audio = audio;
      });
    }
    if (_session.locked != _locked) {
      _locked = _session.locked;
      _holdOrientation(_locked);
    }
  }

  void _holdOrientation(bool locked) {
    if (desktopPlatform) return;
    if (!locked) {
      unawaited(AppDevice.releaseHeldOrientation());
      return;
    }
    final landscape =
        (_media ?? MediaQuery.of(context)).orientation == Orientation.landscape;
    unawaited(AppDevice.holdOrientation(landscape: landscape));
  }

  void _bind() {
    final player = _session.player;
    if (identical(player, _bound)) return;
    _bound = player;
    unawaited(_playing?.cancel());
    _playing = player.stream.playing.listen((_) => _sync());
  }

  void _sync() {
    if (!mounted) return;
    final session = _session;
    final playing = session.player.state.playing;
    final iosVideo =
        Platform.isIOS &&
        !session.audio &&
        (session.ready && session.aspectKnown || _pip.active);
    _pip.update(
      aspectRatio: session.aspectRatio,
      playing: playing,
      hasPrevious: session.hasPrevious,
      hasNext: session.hasNext,
      iosPlayer: iosVideo ? session.player : null,
      autoEnter:
          playing &&
          !_television &&
          !_audio &&
          widget.store.playbackPreferences.autoPictureInPicture,
    );
    _pip.resize(session.aspectRatio);
  }

  void _pipChanged() {
    if (!mounted) return;
    final shown = _pip.shown;
    if (shown && !_shown) _closeRoutesAbove();
    if (!shown && _shown) _pageScope.requestFocus();
    _shown = shown;
    if (!_television) {
      _session.setForeground(
        playerVisible(WidgetsBinding.instance.lifecycleState),
      );
    }
    _sync();
    setState(() {});
  }

  void _closeRoutesAbove() {
    final route = ModalRoute.of(context);
    if (route == null || route.isCurrent) return;
    Navigator.of(context).popUntil((other) => other == route);
  }

  void _action(String action) {
    switch (action) {
      case 'play':
        if (!_session.player.state.playing) _session.togglePlayback();
      case 'pause':
        if (_session.player.state.playing) _session.togglePlayback();
      case 'toggle':
        _session.togglePlayback();
      case 'previous':
        unawaited(_session.previous());
      case 'next':
        unawaited(_session.next());
    }
  }

  Future<void> _enterMini() async {
    _closeRoutesAbove();
    _session.setLocked(false);
    await _pip.enter(_session.aspectRatio, frame: widget.store.miniWindow);
  }

  Future<void> _restore() async {
    final frame = await _pip.exit();
    if (frame != null) {
      unawaited(widget.store.setMiniWindow(frame).catchError((Object _) {}));
    }
  }

  Future<void> _close() async {
    await _restore();
    if (mounted) Navigator.pop(context);
  }

  KeyEventResult _key(FocusNode _, KeyEvent event) {
    final size = MediaQuery.sizeOf(context);
    final form = AppScope.formFactorOf(context);
    if (!desktopPlatform ||
        _audio ||
        _pip.shown ||
        ModalRoute.of(context)?.isCurrent == false ||
        _padFullscreen.value ||
        _windowFullscreen ||
        size.width > size.height) {
      return KeyEventResult.ignored;
    }
    final focusContext = FocusManager.instance.primaryFocus?.context;
    if (focusContext?.findAncestorWidgetOfExactType<EditableText>() != null) {
      return KeyEventResult.ignored;
    }
    final action = _inputs.bindings.resolve(event, PlayerInputScope.portrait);
    if (action == null) return KeyEventResult.ignored;
    final canPadFullscreen =
        form == FormFactor.window || form == FormFactor.pad;
    if (action == PlayerInputAction.fullscreen && !canPadFullscreen) {
      return KeyEventResult.ignored;
    }
    if (!canPadFullscreen && !_inputs.bindings.keys.containsKey(action)) {
      return KeyEventResult.ignored;
    }
    if (event is KeyDownEvent || action.repeats && event is KeyRepeatEvent) {
      _inputs.invoke(action);
    }
    return KeyEventResult.handled;
  }

  void _padFullscreenChanged() {
    _applyPadFullscreen(_padFullscreen.value);
    setState(() {});
  }

  void _applyPadFullscreen(bool full) {
    if (desktopPlatform) {
      unawaited(setWindowFullScreen(full));
      return;
    }
    unawaited(
      AppDevice.setPlayerOrientation(
        full ? PlayerOrientation.lockedLandscape : PlayerOrientation.auto,
      ),
    );
    unawaited(
      SystemChrome.setEnabledSystemUIMode(
        full ? SystemUiMode.immersiveSticky : SystemUiMode.edgeToEdge,
      ).catchError((Object _) {}),
    );
  }

  @override
  Widget build(BuildContext context) {
    final live = MediaQuery.of(context);
    final ios = Platform.isIOS;
    final shown = ios
        ? _pip.systemWindowActive
        : _pip.shown || _pip.leaving && live.size != _media?.size;
    if (!shown || _media == null) _media = live;
    return Stack(
      fit: StackFit.expand,
      children: [
        FocusScope(
          node: _pageScope,
          autofocus: true,
          descendantsAreFocusable: !ios || !shown,
          onKeyEvent: _key,
          child: Offstage(
            offstage: shown,
            child: KeyboardControls(
              handles: (event) {
                if (!desktopPlatform || focusedTextField() != null) {
                  return false;
                }
                final action = _inputs.bindings.resolve(
                  event,
                  PlayerInputScope.portrait,
                );
                return action != null &&
                    _inputs.bindings.keys.containsKey(action);
              },
              child: MediaQuery(
                data: _media!,
                child: Builder(builder: _page),
              ),
            ),
          ),
        ),
        if (ios && shown)
          Scaffold(
            body: Center(
              child: OutlinedButton(
                onPressed: () => unawaited(_enterMini()),
                child: const Text('返回播放器'),
              ),
            ),
          )
        else if (shown)
          _pip.window
              ? MiniWindowView(
                  handle: widget.handle,
                  onRestore: () => unawaited(_restore()),
                  onClose: () => unawaited(_close()),
                )
              : PictureInPictureView(handle: widget.handle),
      ],
    );
  }

  Widget _page(BuildContext context) {
    final form = AppScope.formFactorOf(context);
    final mini = _pip.available && (!Platform.isIOS || !_audio)
        ? () => unawaited(_enterMini())
        : null;
    if (_audio && form != FormFactor.television) {
      return AudioPlayerPage(
        store: widget.store,
        handle: widget.handle,
        onMiniWindow: mini,
      );
    }
    if (desktopPlatform) {
      final size = MediaQuery.sizeOf(context);
      return switch (form) {
        FormFactor.phone
            when _wideVideo &&
                !widget.store.playbackPreferences.portraitStyleForWide =>
          LandscapePlayerPage(
            store: widget.store,
            handle: widget.handle,
            onMiniWindow: mini,
          ),
        FormFactor.phone => PortraitPlayerPage(
          store: widget.store,
          handle: widget.handle,
          onMiniWindow: mini,
        ),
        FormFactor.pad => _padPage(context, mini),
        FormFactor.window
            when !_windowFullscreen &&
                (_padFullscreen.value || size.width <= size.height) =>
          _padPage(context, mini),
        FormFactor.window => PlayerPage(
          store: widget.store,
          handle: widget.handle,
          onWindowFullscreen: (value) => _windowFullscreen = value,
          onMiniWindow: mini,
        ),
        FormFactor.television => TelevisionPlayerPage(handle: widget.handle),
      };
    }
    final portraitStyle = widget.store.playbackPreferences.portraitStyleForWide;
    return switch (form) {
      FormFactor.pad || FormFactor.window => _padPage(context, mini),
      FormFactor.phone
          when _wideVideo &&
              !(portraitStyle &&
                  MediaQuery.orientationOf(context) == Orientation.portrait) =>
        LandscapePlayerPage(
          store: widget.store,
          handle: widget.handle,
          followSensor: !portraitStyle,
          onMiniWindow: mini,
        ),
      FormFactor.phone => PortraitPlayerPage(
        store: widget.store,
        handle: widget.handle,
        onFullscreen: _wideVideo
            ? () => unawaited(
                AppDevice.setPlayerOrientation(PlayerOrientation.landscape),
              )
            : null,
        onMiniWindow: mini,
      ),
      FormFactor.television => TelevisionPlayerPage(handle: widget.handle),
    };
  }

  Widget _padPage(BuildContext context, VoidCallback? mini) {
    final size = MediaQuery.sizeOf(context);
    if (_padFullscreen.value || size.width > size.height) {
      return PlayerPage(
        store: widget.store,
        handle: widget.handle,
        padFullscreen: _padFullscreen,
        onMiniWindow: mini,
      );
    }
    final portraitStyle = widget.store.playbackPreferences.portraitStyleForWide;
    return _wideVideo && !portraitStyle
        ? LandscapePlayerPage(
            store: widget.store,
            handle: widget.handle,
            onFullscreen: () => _padFullscreen.value = true,
            onMiniWindow: mini,
          )
        : PortraitPlayerPage(
            store: widget.store,
            handle: widget.handle,
            onFullscreen: _wideVideo ? () => _padFullscreen.value = true : null,
            onMiniWindow: mini,
          );
  }
}
