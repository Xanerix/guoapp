import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';
import 'dart:typed_data';

import 'package:path/path.dart' as path;

sealed class LanModulePayload {
  const LanModulePayload();
}

final class LanModuleUrl extends LanModulePayload {
  const LanModuleUrl(this.url);
  final String url;
}

final class LanModuleFile extends LanModulePayload {
  const LanModuleFile(this.path);
  final String path;
}

const _maxUploadBytes = 8 * 1024 * 1024;
const _maxUrlLength = 2048;

bool isPrivateIpv4(String address) {
  final parts = address.split('.');
  if (parts.length != 4) return false;
  final octets = [for (final part in parts) int.tryParse(part)];
  if (octets.any((value) => value == null || value < 0 || value > 255)) {
    return false;
  }
  final a = octets[0]!;
  final b = octets[1]!;
  if (a == 10) return true;
  if (a == 172 && b >= 16 && b <= 31) return true;
  if (a == 192 && b == 168) return true;
  return false;
}

Future<List<String>> localLanAddresses() async {
  final interfaces = await NetworkInterface.list(
    type: InternetAddressType.IPv4,
    includeLoopback: false,
    includeLinkLocal: false,
  );
  final preferred = <String>[];
  final fallback = <String>[];
  for (final interface in interfaces) {
    for (final address in interface.addresses) {
      final host = address.address;
      if (host.startsWith('169.254.') || host.startsWith('127.')) continue;
      if (isPrivateIpv4(host)) {
        if (!preferred.contains(host)) preferred.add(host);
      } else {
        if (!fallback.contains(host)) fallback.add(host);
      }
    }
  }
  return [...preferred, ...fallback];
}

class LanModuleServer {
  LanModuleServer._(
    this._server,
    this.token,
    this.addresses,
    this._directory,
  ) {
    _server.listen(_handle, onError: (_, _) {});
  }

  final HttpServer _server;
  final String token;
  final List<String> addresses;
  final Directory _directory;
  final _received = Completer<LanModulePayload>();
  var _closed = false;

  int get port => _server.port;

  Future<LanModulePayload> get received => _received.future;

  List<String> get urls => [
    for (final host in addresses) 'http://$host:$port/$token/',
  ];

  static Future<LanModuleServer> start({
    required Directory cacheDirectory,
    Future<List<String>> Function()? resolveAddresses,
    InternetAddress? bindAddress,
  }) async {
    final addresses = await (resolveAddresses ?? localLanAddresses)();
    final server = await HttpServer.bind(
      bindAddress ?? InternetAddress.anyIPv4,
      0,
    );
    final token = _randomToken();
    return LanModuleServer._(server, token, addresses, cacheDirectory);
  }

  static String _randomToken() {
    final random = Random.secure();
    return List.generate(
      12,
      (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
    ).join();
  }

  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    await _server.close(force: true);
  }

  Future<void> _handle(HttpRequest request) async {
    try {
      final segments = request.uri.pathSegments.where((s) => s.isNotEmpty).toList();
      if (segments.isEmpty || segments.first != token) {
        await _respond(request, HttpStatus.notFound, '页面不存在');
        return;
      }
      final route = segments.length == 1 ? '' : segments.sublist(1).join('/');
      if (request.method == 'GET' && route.isEmpty) {
        await _respond(
          request,
          HttpStatus.ok,
          _pageHtml(token),
          contentType: ContentType.html,
        );
        return;
      }
      if (request.method == 'POST' && route == 'url') {
        await _handleUrl(request);
        return;
      }
      if (request.method == 'POST' && route == 'upload') {
        await _handleUpload(request);
        return;
      }
      await _respond(request, HttpStatus.methodNotAllowed, '不支持的请求');
    } catch (_) {
      try {
        await _respond(request, HttpStatus.internalServerError, '处理失败，请重试');
      } catch (_) {}
    }
  }

  Future<void> _handleUrl(HttpRequest request) async {
    if (_received.isCompleted) {
      await _respond(request, HttpStatus.conflict, '已收到安装包，请在目标设备确认');
      return;
    }
    final bytes = await _readLimited(request, _maxUrlLength * 4);
    if (bytes == null) {
      await _respond(request, HttpStatus.requestEntityTooLarge, '地址过长');
      return;
    }
    final contentType = request.headers.contentType?.mimeType ?? '';
    String raw;
    if (contentType == 'application/x-www-form-urlencoded') {
      raw = Uri.splitQueryString(utf8.decode(bytes, allowMalformed: true))['url'] ?? '';
    } else {
      raw = utf8.decode(bytes, allowMalformed: true);
    }
    final trimmed = raw.trim();
    final parsed = Uri.tryParse(trimmed);
    if (trimmed.isEmpty ||
        trimmed.length > _maxUrlLength ||
        parsed == null ||
        (parsed.scheme != 'http' && parsed.scheme != 'https') ||
        parsed.host.isEmpty) {
      await _respond(
        request,
        HttpStatus.badRequest,
        '请填写以 http:// 或 https:// 开头的下载地址',
      );
      return;
    }
    if (!_received.isCompleted) {
      _received.complete(LanModuleUrl(trimmed));
    }
    await _respond(request, HttpStatus.ok, '已发送下载地址，请在设备屏幕上确认安装');
  }

  Future<void> _handleUpload(HttpRequest request) async {
    if (_received.isCompleted) {
      await _respond(request, HttpStatus.conflict, '已收到安装包，请在目标设备确认');
      return;
    }
    final declared = request.contentLength;
    if (declared > _maxUploadBytes) {
      await _respond(request, HttpStatus.requestEntityTooLarge, '文件超过 8 MB');
      return;
    }
    final bytes = await _readLimited(request, _maxUploadBytes);
    if (bytes == null) {
      await _respond(request, HttpStatus.requestEntityTooLarge, '文件超过 8 MB');
      return;
    }
    if (bytes.length < 4 || bytes[0] != 0x50 || bytes[1] != 0x4B) {
      await _respond(request, HttpStatus.badRequest, '请选择有效的 .zip 模块安装包');
      return;
    }
    if (!await _directory.exists()) {
      await _directory.create(recursive: true);
    }
    final file = File(path.join(_directory.path, 'module-lan-$token.zip'));
    await file.writeAsBytes(bytes, flush: true);
    if (!_received.isCompleted) {
      _received.complete(LanModuleFile(file.path));
    }
    await _respond(request, HttpStatus.ok, '已上传安装包，请在设备屏幕上确认安装');
  }

  Future<List<int>?> _readLimited(HttpRequest request, int limit) async {
    final buffer = BytesBuilder(copy: false);
    await for (final chunk in request) {
      if (buffer.length + chunk.length > limit) return null;
      buffer.add(chunk);
    }
    return buffer.takeBytes();
  }

  Future<void> _respond(
    HttpRequest request,
    int status,
    String body, {
    ContentType? contentType,
  }) async {
    request.response
      ..statusCode = status
      ..headers.contentType = contentType ?? ContentType.text
      ..headers.set(HttpHeaders.cacheControlHeader, 'no-store')
      ..write(body);
    await request.response.close();
  }
}

String _pageHtml(String token) => '''<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>局域网安装模块</title>
<style>
*{box-sizing:border-box}
body{margin:0;padding:24px 16px;font:16px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#f5f5f7;color:#1d1d1f}
main{max-width:460px;margin:0 auto;display:grid;gap:16px}
h1{margin:0;font-size:22px}
.card{background:#fff;border-radius:16px;padding:18px;box-shadow:0 1px 4px rgba(0,0,0,.06);display:grid;gap:12px}
h2{margin:0;font-size:16px}
input[type=url],input[type=file]{width:100%;padding:12px;border:1px solid #d2d2d7;border-radius:10px;font-size:15px;background:#fff}
button{width:100%;padding:12px;border:0;border-radius:10px;background:#0071e3;color:#fff;font-size:15px;font-weight:600;cursor:pointer}
button:disabled{opacity:.5;cursor:default}
#msg{min-height:24px;font-size:14px;color:#0071e3}
#msg.err{color:#d70015}
.hint{margin:0;font-size:13px;color:#6e6e73}
</style>
</head>
<body>
<main>
<h1>局域网安装模块</h1>
<p class="hint">提交模块下载地址或本地 .zip 安装包，提交后请在目标设备确认安装。</p>
<section class="card">
<h2>发送下载地址</h2>
<input id="url" type="url" placeholder="https://…/module.zip" autocomplete="off">
<button id="sendUrl" type="button">发送地址</button>
</section>
<section class="card">
<h2>上传本地安装包</h2>
<input id="file" type="file" accept=".zip,application/zip">
<button id="sendFile" type="button">上传 .zip 文件</button>
</section>
<div id="msg" role="status"></div>
</main>
<script>
const base = '/$token/';
const msg = document.getElementById('msg');
const urlBtn = document.getElementById('sendUrl');
const fileBtn = document.getElementById('sendFile');
function setMsg(text, err) {
  msg.textContent = text;
  msg.className = err ? 'err' : '';
}
function lock(on) {
  urlBtn.disabled = on;
  fileBtn.disabled = on;
}
urlBtn.onclick = async () => {
  const value = document.getElementById('url').value.trim();
  if (!value) return setMsg('请先输入下载地址', true);
  lock(true);
  setMsg('正在发送…', false);
  try {
    const res = await fetch(base + 'url', { method: 'POST', headers: { 'Content-Type': 'text/plain; charset=utf-8' }, body: value });
    const text = await res.text();
    setMsg(text, !res.ok);
    if (!res.ok) lock(false);
  } catch (e) {
    setMsg('发送失败，请确认与目标设备在同一局域网', true);
    lock(false);
  }
};
fileBtn.onclick = async () => {
  const file = document.getElementById('file').files[0];
  if (!file) return setMsg('请先选择 .zip 文件', true);
  if (file.size > 8 * 1024 * 1024) return setMsg('文件超过 8 MB', true);
  lock(true);
  setMsg('正在上传…', false);
  try {
    const res = await fetch(base + 'upload', { method: 'POST', headers: { 'Content-Type': 'application/octet-stream' }, body: file });
    const text = await res.text();
    setMsg(text, !res.ok);
    if (!res.ok) lock(false);
  } catch (e) {
    setMsg('上传失败，请确认与目标设备在同一局域网', true);
    lock(false);
  }
};
</script>
</body>
</html>
''';
