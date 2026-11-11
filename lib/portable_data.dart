import 'dart:io';

import 'package:path/path.dart' as path;
import 'package:path_provider_platform_interface/path_provider_platform_interface.dart';
import 'package:path_provider_windows/path_provider_windows.dart';
import 'package:shared_preferences_platform_interface/shared_preferences_platform_interface.dart';
import 'package:shared_preferences_windows/shared_preferences_windows.dart';

const portableDataFolder = 'userdata';

String? portableDataPath;

void enablePortableData() {
  if (!Platform.isWindows) return;
  final directory = path.join(
    path.dirname(Platform.resolvedExecutable),
    portableDataFolder,
  );
  if (!Directory(directory).existsSync()) return;
  portableDataPath = directory;
  final provider = _PortablePathProvider(directory);
  PathProviderPlatform.instance = provider;
  final preferences = SharedPreferencesStorePlatform.instance;
  if (preferences is SharedPreferencesWindows) {
    preferences.pathProvider = provider;
  }
}

class _PortablePathProvider extends PathProviderWindows {
  _PortablePathProvider(this.directory);
  final String directory;

  @override
  Future<String?> getApplicationSupportPath() async => directory;
}
