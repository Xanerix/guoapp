import 'package:flutter/material.dart';

import '../core_bridge.dart';
import '../local_store.dart';
import 'app_scope.dart';
import 'settings/settings_page.dart';
import 'update_prompt.dart';
import 'widgets/focus_ring.dart';

var _devicesReady = false;

class DeviceRegistrationGate extends StatefulWidget {
  const DeviceRegistrationGate({
    super.key,
    required this.store,
    required this.child,
  });
  final LocalStore store;
  final Widget child;

  @override
  State<DeviceRegistrationGate> createState() => _DeviceRegistrationGateState();
}

class _DeviceRegistrationGateState extends State<DeviceRegistrationGate> {
  @override
  void initState() {
    super.initState();
    if (!_devicesReady) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _open());
    }
  }

  Future<void> _open() async {
    if (!mounted) return;
    final repository = AppScope.of(context).repository;
    var ready = await repository.hongguoDevices().catchError((_) => false);
    if (!ready && mounted) {
      await runStartupChecks(context, widget.store);
      if (!mounted) return;
      ready =
          await showDialog<bool>(
            context: context,
            barrierDismissible: false,
            builder: (_) => _RegistrationDialog(
              repository: repository,
              store: widget.store,
            ),
          ) ??
          false;
    }
    if (ready && mounted) setState(() => _devicesReady = true);
  }

  @override
  Widget build(BuildContext context) => _devicesReady
      ? widget.child
      : const Scaffold(body: Center(child: CircularProgressIndicator()));
}

class _RegistrationDialog extends StatefulWidget {
  const _RegistrationDialog({required this.repository, required this.store});
  final AppRepository repository;
  final LocalStore store;

  @override
  State<_RegistrationDialog> createState() => _RegistrationDialogState();
}

class _RegistrationDialogState extends State<_RegistrationDialog> {
  String? _error;

  @override
  void initState() {
    super.initState();
    _register();
  }

  Future<void> _register() async {
    if (_error != null) setState(() => _error = null);
    String error;
    try {
      if (await widget.repository.hongguoDevices(register: true)) {
        if (mounted) Navigator.pop(context, true);
        return;
      }
      error = '设备注册未完成，请重试';
    } catch (failure) {
      error = '$failure';
    }
    if (mounted) setState(() => _error = error);
  }

  Future<void> _openProxy() async {
    await openProxySettings(context, widget.store);
    if (mounted) await _register();
  }

  @override
  Widget build(BuildContext context) {
    final error = _error;
    return PopScope(
      canPop: false,
      child: AlertDialog(
        title: Text(error == null ? '正在注册设备...' : '设备注册失败'),
        content: error == null
            ? const Row(
                spacing: 16,
                children: [
                  SizedBox.square(
                    dimension: 24,
                    child: CircularProgressIndicator(strokeWidth: 3),
                  ),
                  Flexible(child: Text('正在注册红果设备和辅助设备，完成后自动进入')),
                ],
              )
            : Text('$error\n\n请检查网络，或在网络设置中修改代理后重试。'),
        actions: error == null
            ? null
            : [
                FocusRing(
                  radius: 20,
                  child: TextButton(
                    onPressed: _openProxy,
                    child: const Text('网络设置'),
                  ),
                ),
                FocusRing(
                  radius: 20,
                  child: FilledButton(
                    autofocus: true,
                    onPressed: _register,
                    child: const Text('重试'),
                  ),
                ),
              ],
      ),
    );
  }
}
