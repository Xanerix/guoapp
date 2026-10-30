import 'dart:convert';
import 'dart:io';

import 'package:path/path.dart' as path;

class LocalFiles {
  LocalFiles(this.root);

  static const marker = 'users.json';

  final Directory root;

  Directory get _staging => Directory('${root.path}.new');
  Directory get _retired => Directory('${root.path}.old');

  File _file(Directory directory, String name) =>
      File(path.join(directory.path, name));

  Future<bool> _complete(Directory directory) async =>
      await _file(directory, marker).exists() ||
      await File('${_file(directory, marker).path}.bak').exists();

  Future<bool> exists() async {
    if (!await _complete(root)) {
      for (final candidate in [_staging, _retired]) {
        if (!await _complete(candidate)) continue;
        if (await root.exists()) await root.delete(recursive: true);
        await candidate.rename(root.path);
        break;
      }
    }
    for (final leftover in [_staging, _retired]) {
      if (await leftover.exists()) await leftover.delete(recursive: true);
    }
    return _complete(root);
  }

  Future<Object?> read(String name) async {
    final file = _file(root, name);
    final backup = File('${file.path}.bak');
    var found = false;
    for (final candidate in [file, backup]) {
      if (!await candidate.exists()) continue;
      found = true;
      try {
        return jsonDecode(await candidate.readAsString());
      } catch (_) {}
    }
    if (found) throw FormatException('$name 无法读取');
    return null;
  }

  Future<void> quarantine(String name) async {
    final file = _file(root, name);
    for (final candidate in [file, File('${file.path}.bak')]) {
      if (await candidate.exists()) {
        await candidate.rename('${candidate.path}.corrupt');
      }
    }
  }

  Future<void> persist(File file, String content) =>
      file.writeAsString(content, flush: true);

  Future<void> _write(Directory directory, String name, Object? value) async {
    final file = _file(directory, name);
    await file.parent.create(recursive: true);
    final temp = File('${file.path}.tmp');
    await persist(temp, jsonEncode(value));
    if (await file.exists()) await file.rename('${file.path}.bak');
    await temp.rename(file.path);
  }

  Future<void> write(String name, Object? value) => _write(root, name, value);

  Future<void> replaceAll(Map<String, Object?> files) async {
    if (await _staging.exists()) await _staging.delete(recursive: true);
    for (final entry in files.entries) {
      if (entry.key != marker) await _write(_staging, entry.key, entry.value);
    }
    await _write(_staging, marker, files[marker]);
    if (await _retired.exists()) await _retired.delete(recursive: true);
    if (await root.exists()) await root.rename(_retired.path);
    await _staging.rename(root.path);
    await _retired.delete(recursive: true).catchError((Object _) => _retired);
  }

  Future<void> remove(String directory) async {
    final target = Directory(path.join(root.path, directory));
    if (await target.exists()) await target.delete(recursive: true);
  }

  Future<Map<String, String>> dump() async {
    final files = <String, String>{};
    if (!await root.exists()) return files;
    await for (final entity in root.list(recursive: true)) {
      if (entity is! File) continue;
      final name = path.relative(entity.path, from: root.path);
      files[name.replaceAll(r'\', '/')] = await entity
          .readAsString()
          .catchError((Object _) => '');
    }
    return files;
  }
}
