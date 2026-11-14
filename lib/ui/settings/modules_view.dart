import 'dart:io';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:path/path.dart' as path;
import 'package:path_provider/path_provider.dart';

import '../../core_bridge.dart';
import '../../local_store.dart';
import '../../models.dart';
import '../../source_modules.dart';
import '../app_scope.dart';
import '../widgets/focus_ring.dart';
import '../widgets/form_text_field.dart';
import '../widgets/group_card.dart';
import 'settings_rows.dart';

class ModulesView extends StatefulWidget {
  const ModulesView({super.key, required this.store});
  final LocalStore store;

  @override
  State<ModulesView> createState() => _ModulesViewState();
}

class _ModulesViewState extends State<ModulesView> {
  List<SourceModule>? _modules;
  String? _error;
  bool _busy = false;

  AppRepository get _repository => AppScope.of(context).repository;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _reload());
  }

  Future<void> _reload() async {
    try {
      final modules = await _repository.modules();
      if (mounted) {
        setState(() {
          _modules = modules;
          _error = null;
        });
      }
    } catch (failure) {
      if (mounted) setState(() => _error = '$failure');
    }
  }

  void _show(String message) {
    if (mounted) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text(message)));
    }
  }

  Future<void> _run(Future<void> Function() work) async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      await work();
    } catch (failure) {
      _show('$failure');
    } finally {
      if (mounted) setState(() => _busy = false);
      await _reload();
    }
  }

  Future<void> _installFile() => _run(() async {
    final file = await FilePicker.pickFile(
      type: FileType.custom,
      allowedExtensions: ['zip'],
    );
    if (file == null) return;
    var location = file.path;
    if (location == null) {
      final size = await file.length();
      if (size == null || size > 8 * 1024 * 1024) {
        throw AppFailure('安装包过大或无法读取');
      }
      location = path.join(
        (await getApplicationCacheDirectory()).path,
        'module-picked.zip',
      );
      await File(location).writeAsBytes(await file.readAsBytes());
    }
    await _install(await _repository.prepareModule(file: location));
  });

  Future<void> _installAddress() async {
    final address = await showDialog<String>(
      context: context,
      builder: (_) => const _AddressDialog(),
    );
    if (address == null || address.isEmpty) return;
    await _run(
      () async => _install(await _repository.prepareModule(url: address)),
    );
  }

  Future<void> _install(SourceModule module) async {
    if (!mounted) return;
    final accepted = await showDialog<bool>(
      context: context,
      builder: (_) => _InstallDialog(module: module),
    );
    if (accepted != true) return;
    final id = await _repository.installModule(module.token);
    if (!module.installed) {
      await widget.store.setSourceEnabled(id, true);
    }
    final site = SourceSite.byId(id);
    if (site.devices && !await _repository.sourceDevices(id)) {
      try {
        if (!await _repository.sourceDevices(id, register: true)) {
          throw AppFailure('设备注册未完成');
        }
      } catch (failure) {
        _show('${module.name} 已安装，但$failure；下次打开应用时会再次注册');
        return;
      }
    }
    _show('${module.name} 已安装');
  }

  Future<void> _uninstall(SourceModule module) async {
    final accepted = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('卸载 ${module.name}？'),
        content: const Text('该站源的追剧、观看记录和已下载视频会保留，但在重新安装前不再显示，正在下载的集会暂停。'),
        actions: [
          FocusRing(
            radius: 20,
            child: TextButton(
              autofocus: true,
              onPressed: () => Navigator.pop(context, false),
              child: const Text('取消'),
            ),
          ),
          FocusRing(
            radius: 20,
            child: FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('卸载'),
            ),
          ),
        ],
      ),
    );
    if (accepted != true) return;
    await _run(() async {
      if (_repository.supportsDownloads && SourceSite.isAvailable(module.id)) {
        await _repository.controlDownloads('pauseSource', id: module.id);
      }
      await _repository.uninstallModule(module.id);
      _show('${module.name} 已卸载');
    });
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: widget.store,
    builder: (context, _) {
      final admin = widget.store.profile.admin && !widget.store.locked;
      final modules = _modules;
      final missing = _missingSources();
      return ListView(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 24),
        children: [
          if (_error case final error?) ...[
            _Note(error),
            const SizedBox(height: 12),
          ],
          if (modules == null && _error == null)
            const Padding(
              padding: EdgeInsets.all(24),
              child: Center(child: CircularProgressIndicator()),
            )
          else if (modules != null && modules.isEmpty)
            const _Note('还没有安装模块。站源以模块的形式提供，安装后才能在发现、搜索中使用。')
          else if (modules != null)
            SettingsCard(
              children: [
                for (final module in modules)
                  _ModuleTile(
                    module: module,
                    onUninstall: admin && !_busy
                        ? () => _uninstall(module)
                        : null,
                  ),
              ],
            ),
          const SizedBox(height: 12),
          SettingsCard(
            children: [
              SettingsTile(
                title: '从文件安装',
                subtitle: '选择模块的 zip 安装包',
                enabled: admin && !_busy,
                onTap: admin && !_busy ? _installFile : null,
              ),
              SettingsTile(
                title: '从地址安装',
                subtitle: '填写 zip 安装包的下载地址',
                enabled: admin && !_busy,
                onTap: admin && !_busy ? _installAddress : null,
              ),
            ],
          ),
          if (_busy) ...[
            const SizedBox(height: 12),
            const LinearProgressIndicator(),
          ],
          const SizedBox(height: 12),
          const _Note('安装同一标识的模块会覆盖旧版本。只安装来源可信的模块：模块能访问它声明的域名，并在本机保存自己的数据。'),
          if (missing.isNotEmpty) ...[
            const SizedBox(height: 12),
            _Note('以下站源未安装，相关记录已隐藏，安装对应模块后恢复显示：\n$missing'),
          ],
        ],
      );
    },
  );

  String _missingSources() {
    final records = widget.store.missingSourceRecords();
    final downloads = _repository.missingSourceDownloads();
    final sources = {
      ...records.follows.keys,
      ...records.history.keys,
      ...downloads.keys,
    }.toList()..sort();
    String describe(String source) => [
      if (records.follows[source] case final count?) '追剧 $count 部',
      if (records.history[source] case final count?) '观看记录 $count 条',
      if (downloads[source] case final count?) '下载 $count 集',
    ].join('，');
    return [
      for (final source in sources) '$source：${describe(source)}',
    ].join('\n');
  }
}

class _ModuleTile extends StatelessWidget {
  const _ModuleTile({required this.module, required this.onUninstall});
  final SourceModule module;
  final VoidCallback? onUninstall;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final version = module.version.isEmpty ? '' : ' · ${module.version}';
    return MergeSemantics(
      child: ListTile(
        minTileHeight: 68,
        contentPadding: const EdgeInsets.fromLTRB(20, 4, 8, 4),
        title: Text('${module.name}$version'),
        subtitle: Text(
          module.error.isNotEmpty
              ? module.error
              : module.description.isEmpty
              ? module.id
              : module.description,
          style: module.error.isEmpty ? null : TextStyle(color: colors.error),
        ),
        trailing: FocusRing(
          radius: 20,
          child: TextButton(onPressed: onUninstall, child: const Text('卸载')),
        ),
      ),
    );
  }
}

class _InstallDialog extends StatelessWidget {
  const _InstallDialog({required this.module});
  final SourceModule module;

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    final version = module.version.isEmpty ? '未标注' : module.version;
    final capabilities = [
      for (final name in module.capabilities)
        moduleCapabilityNames[name] ?? name,
    ];
    Widget section(String title, String body) => Padding(
      padding: const EdgeInsets.only(top: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        spacing: 4,
        children: [
          Text(title, style: text.labelLarge),
          Text(body),
        ],
      ),
    );
    return AlertDialog(
      title: Text(
        module.installed ? '更新 ${module.name}？' : '安装 ${module.name}？',
      ),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (module.description.isNotEmpty) Text(module.description),
            section(
              '版本',
              module.installed
                  ? '${module.previous.isEmpty ? '未标注' : module.previous} → $version'
                  : version,
            ),
            section('标识', module.id),
            section(
              '可访问的域名',
              module.hosts.isEmpty ? '无' : module.hosts.join('\n'),
            ),
            section('提供的功能', ['目录、详情和播放', ...capabilities].join('、')),
          ],
        ),
      ),
      actions: [
        FocusRing(
          radius: 20,
          child: TextButton(
            autofocus: true,
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
        ),
        FocusRing(
          radius: 20,
          child: FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: Text(module.installed ? '更新' : '安装'),
          ),
        ),
      ],
    );
  }
}

class _AddressDialog extends StatefulWidget {
  const _AddressDialog();

  @override
  State<_AddressDialog> createState() => _AddressDialogState();
}

class _AddressDialogState extends State<_AddressDialog> {
  final _controller = TextEditingController();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _submit() => Navigator.pop(context, _controller.text.trim());

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('从地址安装'),
    content: FormTextField(
      controller: _controller,
      autofocus: true,
      url: true,
      decoration: const InputDecoration(hintText: 'https://…/module.zip'),
      onSubmitted: (_) => _submit(),
    ),
    actions: [
      FocusRing(
        radius: 20,
        child: TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
      ),
      FocusRing(
        radius: 20,
        child: FilledButton(onPressed: _submit, child: const Text('下一步')),
      ),
    ],
  );
}

class _Note extends StatelessWidget {
  const _Note(this.text);
  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(horizontal: 16),
    child: Text(
      text,
      style: Theme.of(context).textTheme.bodyMedium?.copyWith(
        color: Theme.of(context).colorScheme.onSurfaceVariant,
        height: 1.6,
      ),
    ),
  );
}
