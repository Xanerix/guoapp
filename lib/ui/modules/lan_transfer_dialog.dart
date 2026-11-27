import 'dart:async';

import 'package:flutter/material.dart';
import 'package:path_provider/path_provider.dart';
import 'package:qr/qr.dart';

import '../../lan_module_server.dart';
import '../app_scope.dart';
import '../widgets/focus_ring.dart';

Future<LanModulePayload?> showLanTransferDialog(BuildContext context) =>
    showDialog<LanModulePayload>(
      context: context,
      builder: (_) => const _LanTransferDialog(),
    );

class _LanTransferDialog extends StatefulWidget {
  const _LanTransferDialog();

  @override
  State<_LanTransferDialog> createState() => _LanTransferDialogState();
}

class _LanTransferDialogState extends State<_LanTransferDialog> {
  LanModuleServer? _server;
  String? _error;
  var _addressIndex = 0;

  @override
  void initState() {
    super.initState();
    _start();
  }

  Future<void> _start() async {
    try {
      final cache = await getApplicationCacheDirectory();
      final server = await LanModuleServer.start(cacheDirectory: cache);
      if (!mounted) {
        await server.close();
        return;
      }
      setState(() => _server = server);
      unawaited(
        server.received.then((payload) {
          if (mounted) Navigator.pop(context, payload);
        }),
      );
    } catch (failure) {
      if (mounted) setState(() => _error = '无法启动局域网接收服务：$failure');
    }
  }

  @override
  void dispose() {
    unawaited(_server?.close());
    super.dispose();
  }

  void _nextAddress() {
    final urls = _server?.urls;
    if (urls == null || urls.length < 2) return;
    setState(() => _addressIndex = (_addressIndex + 1) % urls.length);
  }

  @override
  Widget build(BuildContext context) {
    final television = AppScope.of(context).television;
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    final server = _server;
    final urls = server?.urls ?? const <String>[];
    final currentUrl = urls.isEmpty ? null : urls[_addressIndex % urls.length];

    Widget body;
    if (_error != null) {
      body = Text(_error!, style: TextStyle(color: colors.error));
    } else if (server == null) {
      body = const Padding(
        padding: EdgeInsets.all(24),
        child: Center(child: CircularProgressIndicator()),
      );
    } else if (currentUrl == null) {
      body = Text(
        '未检测到局域网地址，请确认设备已连接 Wi-Fi 或有线网络后重试。',
        style: TextStyle(color: colors.onSurfaceVariant),
      );
    } else {
      final qr = Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: Colors.white,
          borderRadius: BorderRadius.circular(12),
        ),
        child: _QrCodeView(data: currentUrl, size: television ? 200 : 180),
      );
      final guide = Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        spacing: 10,
        children: [
          const Text('用同一局域网下的手机或电脑扫码，或在浏览器打开下方地址，即可发送模块下载地址或上传本地 .zip 安装包。'),
          SelectableText(
            currentUrl,
            style: theme.textTheme.bodyMedium?.copyWith(
              fontWeight: FontWeight.w600,
              color: colors.primary,
            ),
          ),
          Text(
            '传输完成后会在此界面弹出确认，关闭窗口将停止接收。',
            style: theme.textTheme.bodySmall?.copyWith(
              color: colors.onSurfaceVariant,
            ),
          ),
        ],
      );
      body = television
          ? SizedBox(
              width: 580,
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.center,
                spacing: 24,
                children: [qr, Expanded(child: guide)],
              ),
            )
          : SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                spacing: 16,
                children: [qr, guide],
              ),
            );
    }

    return AlertDialog(
      title: const Text('局域网传输安装'),
      content: body,
      actions: [
        if (urls.length > 1)
          FocusRing(
            radius: 20,
            child: TextButton(
              onPressed: _nextAddress,
              child: Text('切换地址 (${_addressIndex + 1}/${urls.length})'),
            ),
          ),
        FocusRing(
          radius: 20,
          child: TextButton(
            autofocus: true,
            onPressed: () => Navigator.pop(context),
            child: const Text('取消'),
          ),
        ),
      ],
    );
  }
}

class _QrCodeView extends StatelessWidget {
  const _QrCodeView({required this.data, required this.size});
  final String data;
  final double size;

  @override
  Widget build(BuildContext context) {
    final code = QrCode(
      payload: QrPayload.fromString(data),
      errorCorrectLevel: QrErrorCorrectLevel.medium,
    );
    final image = QrImage(code);
    return SizedBox.square(
      dimension: size,
      child: CustomPaint(painter: _QrCodePainter(image)),
    );
  }
}

class _QrCodePainter extends CustomPainter {
  const _QrCodePainter(this.image);
  final QrImage image;

  @override
  void paint(Canvas canvas, Size size) {
    final count = image.moduleCount;
    final cell = size.width / count;
    final paint = Paint()
      ..color = Colors.black
      ..style = PaintingStyle.fill;
    for (var y = 0; y < count; y++) {
      for (var x = 0; x < count; x++) {
        if (image.isDark(y, x)) {
          canvas.drawRect(
            Rect.fromLTWH(x * cell, y * cell, cell + 0.3, cell + 0.3),
            paint,
          );
        }
      }
    }
  }

  @override
  bool shouldRepaint(_QrCodePainter oldDelegate) => oldDelegate.image != image;
}
