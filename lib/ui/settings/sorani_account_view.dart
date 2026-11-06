import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../core_bridge.dart';
import '../../sorani_account.dart';
import '../app_scope.dart';
import '../widgets/focus_ring.dart';
import '../widgets/form_text_field.dart';
import '../widgets/group_card.dart';

const _soraniSite = 'https://www.sorani.net/';

Future<SoraniAccount?> showSoraniLogin(BuildContext context) =>
    showDialog<SoraniAccount>(
      context: context,
      barrierDismissible: false,
      builder: (_) =>
          _SoraniLoginDialog(repository: AppScope.of(context).repository),
    );

class SoraniAccountTile extends StatefulWidget {
  const SoraniAccountTile({super.key, required this.admin});
  final bool admin;

  @override
  State<SoraniAccountTile> createState() => _SoraniAccountTileState();
}

class _SoraniAccountTileState extends State<SoraniAccountTile> {
  SoraniAccount? _account;
  var _busy = false;

  AppRepository get _repository => AppScope.of(context).repository;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _load() async {
    final account = await _repository.soraniAccount().catchError(
      (_) => const SoraniAccount(),
    );
    if (mounted) setState(() => _account = account);
  }

  Future<void> _login() async {
    final account = await showSoraniLogin(context);
    if (account != null && mounted) setState(() => _account = account);
  }

  Future<void> _logout() async {
    setState(() => _busy = true);
    try {
      final account = await _repository.soraniLogout();
      if (mounted) setState(() => _account = account);
    } catch (failure) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('$failure')));
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _toggle() {
    final account = _account;
    if (account == null || _busy) return;
    account.loggedIn ? _logout() : _login();
  }

  @override
  Widget build(BuildContext context) {
    final account = _account;
    final loggedIn = account?.loggedIn ?? false;
    final enabled = widget.admin && account != null && !_busy;
    return SettingsTile(
      title: '青空账号',
      subtitle: account == null
          ? '正在读取登录状态'
          : loggedIn
          ? '已登录：${account.name}'
          : '未登录。青空目前需要登录后才能播放',
      enabled: widget.admin,
      onTap: widget.admin ? _toggle : null,
      trailing: ExcludeFocus(
        child: TextButton(
          onPressed: enabled ? (loggedIn ? _logout : _login) : null,
          child: Text(loggedIn ? '退出登录' : '登录'),
        ),
      ),
    );
  }
}

class _SoraniLoginDialog extends StatefulWidget {
  const _SoraniLoginDialog({required this.repository});
  final AppRepository repository;

  @override
  State<_SoraniLoginDialog> createState() => _SoraniLoginDialogState();
}

class _SoraniLoginDialogState extends State<_SoraniLoginDialog> {
  final _identifier = TextEditingController();
  final _password = TextEditingController();
  final _passwordFocus = FocusNode();
  var _verifying = false;
  var _busy = false;
  SoraniCaptcha? _captcha;
  String? _error;

  @override
  void dispose() {
    _identifier.dispose();
    _password.dispose();
    _passwordFocus.dispose();
    super.dispose();
  }

  void _next() {
    if (_identifier.text.trim().isEmpty || _password.text.isEmpty) {
      setState(() => _error = '请输入用户名或邮箱和密码');
      return;
    }
    setState(() {
      _verifying = true;
      _error = null;
    });
    _reload();
  }

  Future<void> _reload() async {
    setState(() => _captcha = null);
    try {
      final captcha = await widget.repository.soraniCaptcha();
      if (mounted) setState(() => _captcha = captcha);
    } catch (failure) {
      if (mounted) setState(() => _error = '$failure');
    }
  }

  Future<void> _submit(SoraniCaptchaTrack track) async {
    final captcha = _captcha;
    if (captcha == null || _busy) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final account = await widget.repository.soraniLogin(
        identifier: _identifier.text.trim(),
        password: _password.text,
        captchaId: captcha.id,
        track: track,
      );
      if (mounted) Navigator.pop(context, account);
      return;
    } catch (failure) {
      if (!mounted) return;
      final message = '$failure';
      setState(() {
        _busy = false;
        _error = message;
        if (!message.contains('安全验证')) _verifying = false;
      });
      if (_verifying) await _reload();
    }
  }

  Future<void> _register() async {
    final opened = await launchUrl(
      Uri.parse(_soraniSite),
      mode: LaunchMode.externalApplication,
    ).catchError((_) => false);
    if (opened) return;
    await Clipboard.setData(const ClipboardData(text: _soraniSite));
    if (mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('无法打开浏览器，链接已复制')));
    }
  }

  Widget _form() => Column(
    mainAxisSize: MainAxisSize.min,
    crossAxisAlignment: CrossAxisAlignment.stretch,
    spacing: 12,
    children: [
      FormTextField(
        controller: _identifier,
        autofocus: true,
        maxLength: 128,
        onSubmitted: (_) => _passwordFocus.requestFocus(),
        decoration: const InputDecoration(labelText: '用户名或邮箱'),
      ),
      FormTextField(
        controller: _password,
        focusNode: _passwordFocus,
        obscure: true,
        maxLength: 128,
        onSubmitted: (_) => _next(),
        decoration: InputDecoration(labelText: '密码', errorText: _error),
      ),
      Align(
        alignment: Alignment.centerLeft,
        child: FocusRing(
          radius: 20,
          child: TextButton(
            onPressed: _register,
            child: const Text('没有账号？在浏览器中注册'),
          ),
        ),
      ),
    ],
  );

  Widget _verification() {
    final captcha = _captcha;
    final error = _error;
    final television = AppScope.of(context).television;
    return Column(
      mainAxisSize: MainAxisSize.min,
      spacing: 12,
      children: [
        Text(television ? '按左右方向键移动拼图块，对齐后按确定键' : '拖动下方滑块，让拼图块与缺口重合'),
        if (captcha == null && error == null)
          const SizedBox(
            height: 180,
            child: Center(child: CircularProgressIndicator()),
          )
        else if (captcha != null)
          _SliderCaptcha(
            key: ValueKey(captcha.id),
            captcha: captcha,
            enabled: !_busy,
            onTrack: _submit,
          ),
        if (error != null)
          Text(
            error,
            style: TextStyle(color: Theme.of(context).colorScheme.error),
          ),
      ],
    );
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text(_verifying ? '安全验证' : '登录青空'),
    content: SizedBox(
      width: 352,
      child: SingleChildScrollView(
        child: _verifying ? _verification() : _form(),
      ),
    ),
    actions: [
      FocusRing(
        radius: 20,
        child: TextButton(
          onPressed: _busy
              ? null
              : _verifying
              ? () => setState(() {
                  _verifying = false;
                  _error = null;
                })
              : () => Navigator.pop(context),
          child: Text(_verifying ? '返回' : '取消'),
        ),
      ),
      if (_verifying)
        FocusRing(
          radius: 20,
          child: TextButton(
            onPressed: _busy ? null : _reload,
            child: const Text('换一张'),
          ),
        )
      else
        FocusRing(
          radius: 20,
          child: FilledButton(onPressed: _next, child: const Text('下一步')),
        ),
    ],
  );
}

class _SliderCaptcha extends StatefulWidget {
  const _SliderCaptcha({
    super.key,
    required this.captcha,
    required this.enabled,
    required this.onTrack,
  });
  final SoraniCaptcha captcha;
  final bool enabled;
  final ValueChanged<SoraniCaptchaTrack> onTrack;

  @override
  State<_SliderCaptcha> createState() => _SliderCaptchaState();
}

class _SliderCaptchaState extends State<_SliderCaptcha> {
  static const _maxPoints = 4000;
  static const _keyStep = 4.0;
  final _points = <SoraniCaptchaPoint>[];
  final _focus = FocusNode();
  Offset? _origin;
  DateTime? _started;
  double _offset = 0;
  bool _keyboard = false;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && AppScope.of(context).television) _focus.requestFocus();
    });
  }

  @override
  void dispose() {
    _focus.dispose();
    super.dispose();
  }

  void _keyMove(double delta, double limit) {
    if (_origin == null) {
      _keyboard = true;
      _points.clear();
      _origin = Offset.zero;
      _started = DateTime.now();
      _record(Offset.zero, 'down', limit);
    } else if (!_keyboard) {
      return;
    }
    final x = (_offset + delta).clamp(0.0, limit);
    setState(() => _record(Offset(x, 0), 'move', limit));
  }

  KeyEventResult _keyEvent(
    KeyEvent event,
    Size background,
    Size template,
    double limit,
  ) {
    if (!widget.enabled || event is KeyUpEvent) {
      return KeyEventResult.ignored;
    }
    final key = event.logicalKey;
    if (key == LogicalKeyboardKey.arrowLeft ||
        key == LogicalKeyboardKey.arrowRight) {
      _keyMove(
        key == LogicalKeyboardKey.arrowRight ? _keyStep : -_keyStep,
        limit,
      );
      return KeyEventResult.handled;
    }
    if (key == LogicalKeyboardKey.enter ||
        key == LogicalKeyboardKey.numpadEnter ||
        key == LogicalKeyboardKey.select ||
        key == LogicalKeyboardKey.space ||
        key == LogicalKeyboardKey.gameButtonA) {
      if (_origin != null && _keyboard) {
        _up(Offset(_offset, 0), background, template, limit);
      }
      return KeyEventResult.handled;
    }
    return KeyEventResult.ignored;
  }

  void _record(Offset position, String type, double limit) {
    final origin = _origin!;
    final delta = position - origin;
    final x = delta.dx.clamp(0.0, limit);
    _offset = x;
    if (_points.length < _maxPoints || type == 'up') {
      _points.add(
        SoraniCaptchaPoint(
          x.round(),
          delta.dy.round(),
          type,
          DateTime.now().difference(_started!).inMilliseconds,
        ),
      );
    }
  }

  void _down(PointerDownEvent event, double limit) {
    if (!widget.enabled || _origin != null) return;
    _keyboard = false;
    _points.clear();
    _origin = event.position;
    _started = DateTime.now();
    setState(() => _record(event.position, 'down', limit));
  }

  void _move(PointerMoveEvent event, double limit) {
    if (_origin == null || _keyboard) return;
    setState(() => _record(event.position, 'move', limit));
  }

  void _pointerUp(
    PointerUpEvent event,
    Size background,
    Size template,
    double limit,
  ) {
    if (_keyboard) return;
    _up(event.position, background, template, limit);
  }

  void _pointerCancel() {
    if (_origin == null || _keyboard) return;
    setState(() {
      _origin = null;
      _offset = 0;
    });
  }

  void _up(Offset position, Size background, Size template, double limit) {
    if (_origin == null) return;
    _record(position, 'up', limit);
    final started = _started!;
    _origin = null;
    if (_offset < 2 || _points.length < 2) {
      setState(() => _offset = 0);
      return;
    }
    setState(() {});
    widget.onTrack(
      SoraniCaptchaTrack(
        backgroundWidth: background.width.round(),
        backgroundHeight: background.height.round(),
        templateWidth: template.width.round(),
        templateHeight: template.height.round(),
        startTime: started.millisecondsSinceEpoch,
        stopTime: DateTime.now().millisecondsSinceEpoch,
        points: List.of(_points),
      ),
    );
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final captcha = widget.captcha;
      final width = math.min(300.0, constraints.maxWidth).floorToDouble();
      final scale = width / captcha.backgroundWidth;
      final background = Size(width, captcha.backgroundHeight * scale);
      final template = Size(
        captcha.templateWidth * scale,
        captcha.templateHeight * scale,
      );
      final limit = width - template.width;
      final colors = Theme.of(context).colorScheme;
      return FocusRing(
        radius: 12,
        scale: 1,
        child: Focus(
          focusNode: _focus,
          onKeyEvent: (_, event) =>
              _keyEvent(event, background, template, limit),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            spacing: 12,
            children: [
              ClipRRect(
                borderRadius: BorderRadius.circular(8),
                child: SizedBox.fromSize(
                  size: background,
                  child: Stack(
                    children: [
                      Positioned.fill(
                        child: Image.memory(
                          captcha.background,
                          fit: BoxFit.fill,
                          gaplessPlayback: true,
                        ),
                      ),
                      Positioned(
                        left: _offset,
                        top: 0,
                        width: template.width,
                        height: template.height,
                        child: Image.memory(
                          captcha.template,
                          fit: BoxFit.fill,
                          gaplessPlayback: true,
                        ),
                      ),
                      if (!widget.enabled)
                        const Positioned.fill(
                          child: ColoredBox(
                            color: Color(0x66000000),
                            child: Center(child: CircularProgressIndicator()),
                          ),
                        ),
                    ],
                  ),
                ),
              ),
              SizedBox(
                width: width,
                height: 44,
                child: Stack(
                  children: [
                    Positioned.fill(
                      child: DecoratedBox(
                        decoration: BoxDecoration(
                          color: colors.surfaceContainerHighest,
                          borderRadius: BorderRadius.circular(22),
                        ),
                      ),
                    ),
                    Positioned(
                      left: 0,
                      top: 0,
                      bottom: 0,
                      width: _offset + template.width,
                      child: DecoratedBox(
                        decoration: BoxDecoration(
                          color: colors.primaryContainer,
                          borderRadius: BorderRadius.circular(22),
                        ),
                      ),
                    ),
                    Positioned(
                      left: _offset,
                      top: 0,
                      bottom: 0,
                      width: template.width,
                      child: Listener(
                        behavior: HitTestBehavior.opaque,
                        onPointerDown: (event) => _down(event, limit),
                        onPointerMove: (event) => _move(event, limit),
                        onPointerUp: (event) =>
                            _pointerUp(event, background, template, limit),
                        onPointerCancel: (_) => _pointerCancel(),
                        child: DecoratedBox(
                          decoration: BoxDecoration(
                            color: widget.enabled
                                ? colors.primary
                                : colors.onSurface.withValues(alpha: 0.38),
                            borderRadius: BorderRadius.circular(22),
                          ),
                          child: Icon(
                            Icons.arrow_forward_rounded,
                            color: colors.onPrimary,
                          ),
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      );
    },
  );
}
