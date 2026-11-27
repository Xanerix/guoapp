import 'dart:convert';
import 'dart:io';

import 'package:duanju_app/lan_module_server.dart';
import 'package:flutter_test/flutter_test.dart';

Future<({int status, String body})> _send(
  HttpClient client,
  String method,
  Uri uri, {
  String? contentType,
  List<int>? bytes,
}) async {
  final request = await client.openUrl(method, uri);
  if (contentType != null) {
    request.headers.set(HttpHeaders.contentTypeHeader, contentType);
  }
  if (bytes != null) {
    request.add(bytes);
  }
  final response = await request.close();
  final body = await utf8.decodeStream(response);
  return (status: response.statusCode, body: body);
}

void main() {
  test('isPrivateIpv4 identifies LAN ranges', () {
    expect(isPrivateIpv4('192.168.1.20'), isTrue);
    expect(isPrivateIpv4('10.0.0.5'), isTrue);
    expect(isPrivateIpv4('172.16.0.1'), isTrue);
    expect(isPrivateIpv4('172.31.255.254'), isTrue);
    expect(isPrivateIpv4('172.32.0.1'), isFalse);
    expect(isPrivateIpv4('8.8.8.8'), isFalse);
    expect(isPrivateIpv4('169.254.1.1'), isFalse);
  });

  test('serves upload page, rejects wrong token and invalid url', () async {
    final temp = await Directory.systemTemp.createTemp('lan_module_test_');
    final server = await LanModuleServer.start(
      cacheDirectory: temp,
      resolveAddresses: () async => ['127.0.0.1'],
      bindAddress: InternetAddress.loopbackIPv4,
    );
    final client = HttpClient();
    addTearDown(() async {
      client.close(force: true);
      await server.close();
      if (await temp.exists()) await temp.delete(recursive: true);
    });

    final base = Uri.parse(server.urls.single);
    final missing = await _send(
      client,
      'GET',
      base.replace(path: '/wrong-token/'),
    );
    expect(missing.status, HttpStatus.notFound);

    final page = await _send(client, 'GET', base);
    expect(page.status, HttpStatus.ok);
    expect(page.body, contains('局域网安装模块'));

    final badUrl = await _send(
      client,
      'POST',
      base.resolve('url'),
      contentType: 'text/plain; charset=utf-8',
      bytes: utf8.encode('ftp://example.com/mod.zip'),
    );
    expect(badUrl.status, HttpStatus.badRequest);

    final okUrl = await _send(
      client,
      'POST',
      base.resolve('url'),
      contentType: 'text/plain; charset=utf-8',
      bytes: utf8.encode('https://example.com/mod.zip'),
    );
    expect(okUrl.status, HttpStatus.ok);
    final payload = await server.received;
    expect(payload, isA<LanModuleUrl>());
    expect((payload as LanModuleUrl).url, 'https://example.com/mod.zip');

    final second = await _send(
      client,
      'POST',
      base.resolve('url'),
      contentType: 'text/plain; charset=utf-8',
      bytes: utf8.encode('https://example.com/other.zip'),
    );
    expect(second.status, HttpStatus.conflict);
  });

  test('validates and writes uploaded zip file', () async {
    final temp = await Directory.systemTemp.createTemp('lan_module_zip_');
    final server = await LanModuleServer.start(
      cacheDirectory: temp,
      resolveAddresses: () async => ['127.0.0.1'],
      bindAddress: InternetAddress.loopbackIPv4,
    );
    final client = HttpClient();
    addTearDown(() async {
      client.close(force: true);
      await server.close();
      if (await temp.exists()) await temp.delete(recursive: true);
    });

    final base = Uri.parse(server.urls.single);
    final invalid = await _send(
      client,
      'POST',
      base.resolve('upload'),
      contentType: 'application/octet-stream',
      bytes: utf8.encode('not-a-zip'),
    );
    expect(invalid.status, HttpStatus.badRequest);

    final zipBytes = [0x50, 0x4B, 0x03, 0x04, 1, 2, 3, 4];
    final uploaded = await _send(
      client,
      'POST',
      base.resolve('upload'),
      contentType: 'application/octet-stream',
      bytes: zipBytes,
    );
    expect(uploaded.status, HttpStatus.ok);
    final payload = await server.received;
    expect(payload, isA<LanModuleFile>());
    final saved = File((payload as LanModuleFile).path);
    expect(await saved.exists(), isTrue);
    expect(await saved.readAsBytes(), zipBytes);
  });
}
