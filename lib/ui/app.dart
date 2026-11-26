import 'dart:async';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_localizations/flutter_localizations.dart';

import '../app_build.dart';
import '../app_device.dart';
import '../core_bridge.dart';
import '../local_store.dart';
import '../theme_color.dart';
import 'app_scope.dart';
import 'cover_blur.dart';
import 'device_registration.dart';
import 'shell/app_shell.dart';
import 'terms_page.dart';
import 'theme/app_theme.dart';
import 'theme/motion.dart';
import 'theme/system_color.dart';
import 'users/unlock_page.dart';
import 'widgets/focus_ring.dart';

class DuanjuApp extends StatefulWidget {
  const DuanjuApp({
    super.key,
    required this.repository,
    this.store,
    this.bootstrapError,
    this.onRetry,
    this.television = false,
    this.version = '',
  });
  final AppRepository repository;
  final LocalStore? store;
  final String? bootstrapError;
  final VoidCallback? onRetry;
  final bool television;
  final String version;

  @override
  State<DuanjuApp> createState() => _DuanjuAppState();
}

class _DuanjuAppState extends State<DuanjuApp> with WidgetsBindingObserver {
  final _navigator = GlobalKey<NavigatorState>();
  GlobalKey<AppShellState>? _shell;
  int? _shellEpoch;
  Color? _systemColor;
  int? _profileEpoch;
  var _television = false;
  var _phone = false;
  (Color, bool, bool)? _themeKey;
  CoverBlur? _coverBlur;
  late (ThemeData, ThemeData) _themes;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    FocusManager.instance
      ..addEarlyKeyEventHandler(_blockKeyboardFocus)
      ..addEarlyKeyEventHandler(_leaveTextField)
      ..addEarlyKeyEventHandler(_scrollPage)
      ..addLateKeyEventHandler(_escape);
    _loadSystemColor();
  }

  @override
  void dispose() {
    FocusManager.instance
      ..removeEarlyKeyEventHandler(_blockKeyboardFocus)
      ..removeEarlyKeyEventHandler(_leaveTextField)
      ..removeEarlyKeyEventHandler(_scrollPage)
      ..removeLateKeyEventHandler(_escape);
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _loadSystemColor();
  }

  Future<void> _loadSystemColor() async {
    final color = await readSystemColor();
    if (mounted && color != _systemColor) {
      setState(() => _systemColor = color);
    }
  }

  static final _focusKeys = {
    LogicalKeyboardKey.arrowUp,
    LogicalKeyboardKey.arrowDown,
    LogicalKeyboardKey.arrowLeft,
    LogicalKeyboardKey.arrowRight,
    LogicalKeyboardKey.enter,
    LogicalKeyboardKey.numpadEnter,
    LogicalKeyboardKey.space,
    LogicalKeyboardKey.select,
    LogicalKeyboardKey.gameButtonA,
  };

  KeyEventResult _blockKeyboardFocus(KeyEvent event) {
    if (_television) return KeyEventResult.ignored;
    final key = event.logicalKey;
    return key == LogicalKeyboardKey.tab ||
            focusedTextField() == null &&
                !keyboardControlsFocused() &&
                _focusKeys.contains(key)
        ? KeyEventResult.handled
        : KeyEventResult.ignored;
  }

  KeyEventResult _leaveTextField(KeyEvent event) {
    if (!_television || event is KeyUpEvent) return KeyEventResult.ignored;
    final focus = FocusManager.instance.primaryFocus;
    if (focus == null ||
        focusedTextField() == null ||
        textFieldKeepsKey(event.logicalKey)) {
      return KeyEventResult.ignored;
    }
    final direction = switch (event.logicalKey) {
      LogicalKeyboardKey.arrowUp => TraversalDirection.up,
      LogicalKeyboardKey.arrowDown => TraversalDirection.down,
      LogicalKeyboardKey.arrowLeft => TraversalDirection.left,
      LogicalKeyboardKey.arrowRight => TraversalDirection.right,
      _ => null,
    };
    return direction != null && focus.focusInDirection(direction)
        ? KeyEventResult.handled
        : KeyEventResult.ignored;
  }

  KeyEventResult _scrollPage(KeyEvent event) {
    final key = event.logicalKey;
    final keyboard = HardwareKeyboard.instance;
    if (_television ||
        event is KeyUpEvent ||
        (key != LogicalKeyboardKey.pageUp &&
            key != LogicalKeyboardKey.pageDown) ||
        keyboard.isShiftPressed ||
        keyboard.isControlPressed ||
        keyboard.isAltPressed ||
        keyboard.isMetaPressed ||
        keyboardControlsFocused() ||
        (focusedTextField()?.widget.maxLines ?? 1) != 1) {
      return KeyEventResult.ignored;
    }
    final position = _centerScrollPosition();
    if (position == null) return KeyEventResult.ignored;
    final forward =
        (key == LogicalKeyboardKey.pageDown) !=
        (position.axisDirection == AxisDirection.up);
    final step = 0.8 * position.viewportDimension * (forward ? 1 : -1);
    position.moveTo(
      (position.pixels + step).clamp(
        position.minScrollExtent,
        position.maxScrollExtent,
      ),
      duration: Motion.short,
      curve: Motion.standard,
    );
    return KeyEventResult.handled;
  }

  static ScrollPosition? _centerScrollPosition() {
    final view = WidgetsBinding.instance.platformDispatcher.implicitView;
    if (view == null) return null;
    final result = HitTestResult();
    WidgetsBinding.instance.hitTestInView(
      result,
      view.physicalSize.center(Offset.zero) / view.devicePixelRatio,
      view.viewId,
    );
    for (final entry in result.path) {
      if (entry.target
          case RenderViewportBase(offset: final ScrollPosition position)
          when position.axis == Axis.vertical &&
              position.hasContentDimensions &&
              position.maxScrollExtent > position.minScrollExtent) {
        return position;
      }
    }
    return null;
  }

  KeyEventResult _escape(KeyEvent event) {
    if (event is! KeyDownEvent ||
        event.logicalKey != LogicalKeyboardKey.escape) {
      return KeyEventResult.ignored;
    }
    final keyboard = HardwareKeyboard.instance;
    if (keyboard.isAltPressed ||
        keyboard.isControlPressed ||
        keyboard.isMetaPressed ||
        keyboard.isShiftPressed) {
      return KeyEventResult.ignored;
    }
    return _navigateBack() ? KeyEventResult.handled : KeyEventResult.ignored;
  }

  void _pointerDown(PointerDownEvent event) {
    if (!desktopPlatform || event.buttons != kBackMouseButton) return;
    _navigateBack();
  }

  bool _navigateBack() {
    final context =
        focusedTextField()?.context ??
        FocusManager.instance.primaryFocus?.context;
    if (context != null) {
      const intent = DismissIntent();
      final action = Actions.maybeFind<DismissIntent>(context, intent: intent);
      if (action != null &&
          Actions.of(
            context,
          ).invokeActionIfEnabled(action, intent, context).$1) {
        return true;
      }
      final navigator = Navigator.maybeOf(context);
      if (navigator != null && navigator.canPop()) {
        unawaited(navigator.maybePop());
        return true;
      }
    }
    final root = _navigator.currentState;
    if (root != null && root.canPop()) {
      unawaited(root.maybePop());
      return true;
    }
    final shell = _shell?.currentState;
    if (shell != null && shell.canPopCurrentTab) {
      unawaited(shell.popCurrentTab());
      return true;
    }
    return false;
  }

  @override
  Widget build(BuildContext context) {
    final store = widget.store;
    return ListenableBuilder(
      listenable: Listenable.merge([?store]),
      builder: (_, _) => _application(store),
    );
  }

  GlobalKey<AppShellState> _shellKey(int epoch) {
    if (_shell == null || _shellEpoch != epoch) {
      _shellEpoch = epoch;
      _shell = GlobalKey<AppShellState>();
    }
    return _shell!;
  }

  void _resetRoutes(LocalStore? store) {
    final epoch = store?.profileEpoch;
    if (epoch == _profileEpoch) return;
    final previous = _profileEpoch;
    _profileEpoch = epoch;
    if (previous == null) return;
    WidgetsBinding.instance.addPostFrameCallback(
      (_) => _navigator.currentState?.popUntil((route) => route.isFirst),
    );
  }

  Widget _application(LocalStore? store) {
    _resetRoutes(store);
    final formFactor = switch (store?.displayMode) {
      'television' => FormFactor.television,
      'phone' => FormFactor.phone,
      'pad' => FormFactor.pad,
      'window' => desktopPlatform ? FormFactor.window : FormFactor.pad,
      _ => widget.television ? FormFactor.television : null,
    };
    final television = formFactor == FormFactor.television;
    if (television != _television) {
      unawaited(AppDevice.setTelevisionMode(television));
    }
    _television = television;
    if (store != null) {
      _coverBlur ??= store.blurCoversButton
          ? CoverBlur.values.byName(store.blurCoversLaunch)
          : CoverBlur.off;
    }
    final coverBlur = _coverBlur ?? CoverBlur.off;
    final systemColor = (store?.followSystemColor ?? true)
        ? _systemColor
        : null;
    final seed =
        systemColor ?? Color((store?.themeColor ?? ThemeColor.orange).seed);
    final pureBlack = store?.pureBlack ?? false;
    if ((seed, television, pureBlack) != _themeKey) {
      _themeKey = (seed, television, pureBlack);
      _themes = (
        AppTheme.build(seed, Brightness.light, television: television),
        AppTheme.build(
          seed,
          Brightness.dark,
          television: television,
          pureBlack: pureBlack,
        ),
      );
    }
    WidgetsBinding.instance.addPostFrameCallback(
      (_) => FocusManager.instance.highlightStrategy = television
          ? FocusHighlightStrategy.alwaysTraditional
          : FocusHighlightStrategy.alwaysTouch,
    );
    return MaterialApp(
      navigatorKey: _navigator,
      title: appName,
      debugShowCheckedModeBanner: false,
      scrollBehavior: const FocusOutlineScrollBehavior(),
      locale: const Locale('zh', 'CN'),
      supportedLocales: const [Locale('zh', 'CN')],
      localizationsDelegates: GlobalMaterialLocalizations.delegates,
      theme: _themes.$1,
      darkTheme: _themes.$2,
      themeMode: AppTheme.mode(store?.themeMode ?? 'system'),
      builder: (context, child) => AnnotatedRegion<SystemUiOverlayStyle>(
        value: AppTheme.systemBars(Theme.of(context).brightness),
        child: Listener(
          behavior: HitTestBehavior.translucent,
          onPointerDown: _pointerDown,
          child: Shortcuts(
            shortcuts: const {
              SingleActivator(LogicalKeyboardKey.select, includeRepeats: false):
                  ActivateIntent(),
              SingleActivator(
                LogicalKeyboardKey.gameButtonA,
                includeRepeats: false,
              ): ActivateIntent(),
            },
            child: AppScope(
              repository: widget.repository,
              formFactor: formFactor,
              systemColorAvailable: _systemColor != null,
              version: widget.version,
              child: CoverBlurScope(
                mode: coverBlur,
                enabled: !television && (store?.blurCoversButton ?? false),
                everywhere: store?.blurAllCovers ?? false,
                onToggle: () => setState(
                  () => _coverBlur = coverBlur == CoverBlur.off
                      ? CoverBlur.all
                      : CoverBlur.off,
                ),
                child: Builder(
                  builder: (context) {
                    final phone =
                        AppScope.formFactorOf(context) == FormFactor.phone;
                    if (phone != _phone) {
                      _phone = phone;
                      unawaited(AppDevice.setPhoneMode(phone));
                    }
                    return FocusOutlineMotion(
                      child: FocusOutlineLayer(
                        child: FocusTraversalGroup(
                          policy: ReadingOrderTraversalPolicy(
                            requestFocusCallback: revealFocus,
                          ),
                          child: child!,
                        ),
                      ),
                    );
                  },
                ),
              ),
            ),
          ),
        ),
      ),
      themeAnimationDuration: Motion.long,
      themeAnimationCurve: Motion.standard,
      home: switch (store) {
        null => _BootScreen(
          error: widget.bootstrapError,
          onRetry: widget.onRetry,
        ),
        LocalStore(acceptedTerms: < termsVersion) => TermsPage(store: store),
        LocalStore(locked: true) => UnlockPage(store: store),
        _ => DeviceRegistrationGate(
          store: store,
          child: AppShell(key: _shellKey(store.profileEpoch), store: store),
        ),
      },
    );
  }
}

class _BootScreen extends StatelessWidget {
  const _BootScreen({this.error, this.onRetry});
  final String? error;
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) => Scaffold(
    body: Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          spacing: 24,
          children: [
            if (error == null) const CircularProgressIndicator(),
            Text(
              error ?? '正在打开$appName',
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.titleMedium,
            ),
            if (error != null && onRetry != null)
              FilledButton(onPressed: onRetry, child: const Text('重新打开')),
          ],
        ),
      ),
    ),
  );
}
