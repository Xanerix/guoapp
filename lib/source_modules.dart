class SourceModule {
  const SourceModule({
    required this.id,
    required this.name,
    this.version = '',
    this.description = '',
    this.hosts = const [],
    this.capabilities = const [],
    this.installed = false,
    this.previous = '',
    this.token = '',
    this.error = '',
  });
  factory SourceModule.fromJson(Map<String, dynamic> json) => SourceModule(
    id: json['id'] as String? ?? '',
    name: json['name'] as String? ?? '',
    version: json['version'] as String? ?? '',
    description: json['description'] as String? ?? '',
    hosts: (json['hosts'] as List? ?? const []).whereType<String>().toList(),
    capabilities: (json['capabilities'] as List? ?? const [])
        .whereType<String>()
        .toList(),
    installed: json['installed'] == true,
    previous: json['previous'] as String? ?? '',
    token: json['token'] as String? ?? '',
    error: json['error'] as String? ?? '',
  );
  final String id;
  final String name;
  final String version;
  final String description;
  final List<String> hosts;
  final List<String> capabilities;
  final bool installed;
  final String previous;
  final String token;
  final String error;
}

class ModuleIndexEntry {
  const ModuleIndexEntry({
    required this.id,
    required this.name,
    required this.version,
    this.description = '',
    this.repo = '',
    this.bundled = false,
  });
  factory ModuleIndexEntry.fromJson(Map<String, dynamic> json) =>
      ModuleIndexEntry(
        id: json['id'] as String? ?? '',
        name: json['name'] as String? ?? '',
        version: json['version'] as String? ?? '',
        description: json['description'] as String? ?? '',
        repo: json['repo'] as String? ?? '',
        bundled: json['bundled'] == true,
      );
  final String id;
  final String name;
  final String version;
  final String description;
  final String repo;
  final bool bundled;
}

class ModuleStore {
  const ModuleStore(this.entries, {this.warning = ''});
  final List<ModuleIndexEntry> entries;
  final String warning;
}

bool isNewerModuleVersion(String latest, String installed) {
  List<int>? parse(String version) {
    final parts = version.split('.').map(int.tryParse).toList();
    return parts.length == 3 && !parts.contains(null)
        ? parts.cast<int>()
        : null;
  }

  final next = parse(latest);
  final current = parse(installed);
  if (next == null) return false;
  if (current == null) return latest != installed;
  for (var index = 0; index < 3; index++) {
    if (next[index] != current[index]) return next[index] > current[index];
  }
  return false;
}

const moduleCapabilityNames = {
  'search': '搜索',
  'account': '账号登录',
  'suggestions': '搜索联想',
  'recommendations': '推荐',
  'danmaku': '弹幕',
  'devices': '设备注册',
  'rankings': '榜单',
  'seasons': '分季',
  'cover': '封面',
  'metadata': '补充信息',
  'catalogState': '目录状态',
};

String? missingSourceHint(Map<String, int> counts, String unit) {
  final total = counts.values.fold(0, (sum, count) => sum + count);
  return total == 0 ? null : '有 $total $unit来自未安装的站源，在模块管理中安装后恢复显示';
}
