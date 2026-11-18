import 'dart:io';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:path/path.dart' as path;
import 'package:path_provider/path_provider.dart';

import '../../core_bridge.dart';
import '../../local_store.dart';
import '../../models.dart';
import '../../source_modules.dart';
import '../app_scope.dart';
import '../settings/settings_rows.dart';
import 'module_install.dart';
import '../settings/sorani_account_view.dart';
import '../widgets/bar_button.dart';
import '../widgets/focus_ring.dart';
import '../widgets/form_text_field.dart';
import '../widgets/group_card.dart';

class ModulesPage extends StatefulWidget {
  const ModulesPage({super.key, required this.store});
  final LocalStore store;

  @override
  State<ModulesPage> createState() => _ModulesPageState();
}

ModuleIndexEntry? _updateFor(SourceModule module, ModuleIndexEntry? entry) =>
    entry != null &&
        module.error.isEmpty &&
        isNewerModuleVersion(entry.version, module.version)
    ? entry
    : null;

Map<String, String> _missingSources(
  LocalStore store,
  AppRepository repository,
) {
  final records = store.missingSourceRecords();
  final downloads = repository.missingSourceDownloads();
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
  return {for (final source in sources) source: describe(source)};
}

List<SourceModule> _arranged(List<SourceModule> modules, List<String> order) {
  final byId = {for (final module in modules) module.id: module};
  return [for (final id in order) ?byId.remove(id), ...byId.values];
}

const _tabLabels = ['已安装', '在线仓库', '内置', '手动'];

class _ModulesPageState extends State<ModulesPage>
    with SingleTickerProviderStateMixin {
  late final _tabs = TabController(length: _tabLabels.length, vsync: this)
    ..addListener(_tabChanged);
  List<SourceModule>? _modules;
  List<String>? _moving;
  String? _error;
  ModuleStore? _index;
  String? _indexError;
  bool _busy = false;
  final _groupsScope = FocusScopeNode(
    debugLabel: 'modules-groups',
    traversalEdgeBehavior: TraversalEdgeBehavior.parentScope,
  );
  final _detailScope = FocusScopeNode(
    debugLabel: 'modules-detail',
    traversalEdgeBehavior: TraversalEdgeBehavior.parentScope,
  );
  final _groupFocus = [for (final _ in _tabLabels) FocusNode()];
  var _detailFocused = false;

  LocalStore get _store => widget.store;

  AppRepository get _repository => AppScope.of(context).repository;

  bool get _canInstall => _store.profile.admin && !_store.locked && !_busy;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _refresh();
      _loadIndex();
    });
  }

  @override
  void dispose() {
    _tabs.dispose();
    _groupsScope.dispose();
    _detailScope.dispose();
    for (final node in _groupFocus) {
      node.dispose();
    }
    super.dispose();
  }

  void _tabChanged() {
    if (!_tabs.indexIsChanging) setState(() {});
  }

  Future<void> _refresh() async {
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

  Future<void> _loadIndex() async {
    setState(() {
      _index = null;
      _indexError = null;
    });
    try {
      final index = await _repository.moduleIndex();
      if (mounted) setState(() => _index = index);
    } catch (failure) {
      if (mounted) setState(() => _indexError = '$failure');
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
      await _refresh();
    }
  }

  Future<void> _installIndexed(ModuleIndexEntry entry) => _run(
    () async => _install(
      await _repository.prepareModule(
        indexed: entry.id,
        bundled: entry.bundled,
      ),
    ),
  );

  Future<void> _install(SourceModule module) async {
    if (!mounted) return;
    final accepted = await showDialog<bool>(
      context: context,
      builder: (_) => _ModuleDialog(module: module, install: true),
    );
    if (accepted != true) return;
    _show(await _applyInstall(module) ?? '${module.name} 已安装');
  }

  Future<String?> _applyInstall(SourceModule module) =>
      applyModuleInstall(_repository, _store, module);

  Future<void> _installAll(List<ModuleIndexEntry> entries) async {
    final accepted = await showDialog<bool>(
      context: context,
      builder: (_) => BatchInstallDialog(
        entries: entries,
        title: '安装 ${entries.length} 个站源？',
        message: '这些站源会逐个安装并启用。',
      ),
    );
    if (accepted != true) return;
    await _run(
      () async =>
          _show(await installIndexedModules(_repository, _store, entries)),
    );
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

  Future<void> _uninstall(SourceModule module) async {
    if (await _confirmUninstall(module)) await _remove(module);
  }

  Future<bool> _confirmUninstall(SourceModule module) async {
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
    return accepted == true;
  }

  Future<void> _remove(SourceModule module) => _run(() async {
    if (_repository.supportsDownloads && SourceSite.isAvailable(module.id)) {
      await _repository.controlDownloads('pauseSource', id: module.id);
    }
    await _repository.uninstallModule(module.id);
    _show('${module.name} 已卸载');
  });

  Future<void> _toggle(SourceModule module, bool value) {
    final repository = _repository;
    return saveSetting(context, () async {
      await _store.setSourceEnabled(module.id, value);
      if (!value && repository.supportsDownloads) {
        await repository.controlDownloads('pauseSource', id: module.id);
      }
    });
  }

  ValueChanged<bool>? _toggleFor(SourceModule module) =>
      _store.profile.admin &&
          module.error.isEmpty &&
          SourceSite.isAvailable(module.id)
      ? (value) => _toggle(module, value)
      : null;

  Future<void> _details(SourceModule module) => showDialog<void>(
    context: context,
    builder: (_) => _ModuleDialog(module: module, install: false),
  );

  Future<void> _menu(SourceModule module, ModuleIndexEntry? update) =>
      showDialog<void>(
        context: context,
        builder: (menu) => ListenableBuilder(
          listenable: _store,
          builder: (menu, _) {
            final enabled = _canInstall;
            return _ModuleMenu(
              module: module,
              update: update,
              enabled: _store.sourceEnabled(module.id),
              onToggle: _toggleFor(module),
              admin: _store.profile.admin,
              onUpdate: update != null && enabled
                  ? () {
                      Navigator.pop(menu);
                      _installIndexed(update);
                    }
                  : null,
              onUninstall: enabled
                  ? () async {
                      if (!await _confirmUninstall(module) || !menu.mounted) {
                        return;
                      }
                      Navigator.pop(menu);
                      await _remove(module);
                    }
                  : null,
            );
          },
        ),
      );

  Widget _list(List<Widget> children) => ListView.separated(
    padding: AppScope.of(context).television
        ? const EdgeInsets.fromLTRB(24, 8, televisionInset, 27)
        : const EdgeInsets.fromLTRB(16, 8, 16, 24),
    itemCount: children.length,
    itemBuilder: (_, index) => children[index],
    separatorBuilder: (_, _) => const SizedBox(height: 12),
  );

  Future<void> _reorder(List<SourceModule> modules, int from, int to) async {
    final ids = [for (final module in modules) module.id];
    ids.insert(to, ids.removeAt(from));
    setState(() => _moving = ids);
    await saveSetting(context, () => _store.setSourceOrder(ids));
    if (mounted) setState(() => _moving = null);
  }

  Widget _reorderable(
    List<SourceModule> modules,
    List<Widget> header,
    Widget Function(SourceModule) card,
  ) => ReorderableListView(
    padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
    buildDefaultDragHandles: false,
    header: header.isEmpty
        ? null
        : Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Column(spacing: 12, children: header),
          ),
    onReorderItem: (from, to) => _reorder(modules, from, to),
    proxyDecorator: (child, _, animation) => ScaleTransition(
      scale: Tween(begin: 1.0, end: 1.03).animate(animation),
      child: Material(type: MaterialType.transparency, child: child),
    ),
    children: [
      for (final (index, module) in modules.indexed)
        ReorderableDelayedDragStartListener(
          key: ValueKey(module.id),
          index: index,
          child: Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: card(module),
          ),
        ),
    ],
  );

  Widget _installedTab() {
    final modules = switch (_modules) {
      final loaded? => _arranged(
        loaded,
        _moving ?? [for (final site in SourceSite.allValues) site.id],
      ),
      null => null,
    };
    final enabled = _canInstall;
    final latest = <String, ModuleIndexEntry>{};
    for (final entry in _index?.entries ?? const <ModuleIndexEntry>[]) {
      final current = latest[entry.id];
      if (current == null ||
          isNewerModuleVersion(entry.version, current.version)) {
        latest[entry.id] = entry;
      }
    }
    Widget card(SourceModule module) => _InstalledCard(
      module: module,
      update: _updateFor(module, latest[module.id]),
      enabled: _store.sourceEnabled(module.id),
      onToggle: _toggleFor(module),
      admin: _store.profile.admin,
      onDetails: AppScope.of(context).television
          ? () => _menu(module, _updateFor(module, latest[module.id]))
          : () => _details(module),
      onUpdate: enabled ? () => _installIndexed(latest[module.id]!) : null,
      onUninstall: enabled ? () => _uninstall(module) : null,
    );
    final header = [if (_error case final error?) _Note(error)];
    if (modules != null &&
        modules.length > 1 &&
        _store.profile.admin &&
        !_store.locked &&
        !AppScope.of(context).television) {
      return _reorderable(modules, header, card);
    }
    return _list([
      ...header,
      if (modules == null && _error == null)
        const _Loading()
      else if (modules != null && modules.isEmpty)
        const _Note('还没有安装模块。站源以模块的形式提供，可以从在线仓库、内置或手动安装。')
      else if (modules != null)
        for (final module in modules) card(module),
    ]);
  }

  Widget _catalogTab({required bool bundled}) {
    final enabled = _canInstall;
    final installed = {for (final module in _modules ?? const []) module.id};
    final missing = _missingSources(_store, _repository);
    final warning = bundled ? '' : _index?.warning ?? '';
    final listed = [
      for (final entry in _index?.entries ?? const <ModuleIndexEntry>[])
        if (entry.bundled == bundled) entry,
    ];
    final entries =
        [
          for (final entry in listed)
            if (!installed.contains(entry.id)) entry,
        ]..sort(
          (a, b) => (missing.containsKey(a.id) ? 0 : 1).compareTo(
            missing.containsKey(b.id) ? 0 : 1,
          ),
        );
    return _list([
      if (warning.isNotEmpty) _Note(warning),
      if (!bundled && entries.isNotEmpty)
        SettingsCard(
          children: [
            SettingsTile(
              icon: Icons.download_rounded,
              title: '安装全部站源',
              subtitle: '在线仓库里还有 ${entries.length} 个未安装',
              enabled: enabled,
              onTap: enabled ? () => _installAll(entries) : null,
            ),
          ],
        ),
      if (_indexError ?? _error case final error?)
        _Note(error)
      else if (_index == null || _modules == null)
        const _Loading()
      else if (listed.isEmpty && warning.isEmpty)
        _Note(bundled ? '这个安装包没有内置模块。' : '仓库里还没有模块。')
      else if (entries.isEmpty && listed.isNotEmpty)
        _Note(bundled ? '内置模块都已安装。' : '仓库里的模块都已安装。')
      else
        for (final entry in entries)
          _IndexCard(
            entry: entry,
            records: missing[entry.id],
            onInstall: enabled ? () => _installIndexed(entry) : null,
          ),
    ]);
  }

  Widget _manualTab() {
    final enabled = _canInstall;
    return _list([
      GroupCard(
        children: [
          SettingsTile(
            title: '选择本地文件',
            enabled: enabled,
            onTap: enabled ? _installFile : null,
          ),
        ],
      ),
      GroupCard(
        children: [
          SettingsTile(
            title: '输入下载地址',
            enabled: enabled,
            onTap: enabled ? _installAddress : null,
          ),
        ],
      ),
    ]);
  }

  Widget? _refreshButton() => _tabs.index == 1
      ? BarButton(
          icon: Icons.refresh_rounded,
          tooltip: '刷新',
          onPressed: _index == null && _indexError == null ? null : _loadIndex,
        )
      : null;

  Widget _tabBar() => Padding(
    padding: const EdgeInsets.symmetric(horizontal: 4),
    child: TabBar(
      controller: _tabs,
      isScrollable: true,
      tabAlignment: TabAlignment.start,
      labelStyle: const TextStyle(fontSize: 15, fontWeight: FontWeight.w700),
      unselectedLabelStyle: const TextStyle(
        fontSize: 15,
        fontWeight: FontWeight.w500,
      ),
      tabs: [for (final label in _tabLabels) Tab(text: label)],
    ),
  );

  Widget _body() => Column(
    children: [
      if (_busy) const LinearProgressIndicator(),
      Expanded(
        child: TabBarView(
          controller: _tabs,
          children: [
            _installedTab(),
            _catalogTab(bundled: false),
            _catalogTab(bundled: true),
            _manualTab(),
          ],
        ),
      ),
    ],
  );

  Widget _content() => switch (_tabs.index) {
    0 => _installedTab(),
    1 => _catalogTab(bundled: false),
    2 => _catalogTab(bundled: true),
    _ => _manualTab(),
  };

  void _select(int index) => _tabs.index = index;

  FocusNode? _detailTarget({required bool resume}) {
    final last = _detailScope.focusedChild;
    if (resume && last != null && last.canRequestFocus) return last;
    final first = FocusTraversalGroup.of(
      context,
    ).findFirstFocus(_detailScope, ignoreCurrentFocus: true);
    return first == _detailScope ? null : first;
  }

  KeyEventResult _onGroupsKey(KeyEvent event) {
    if (event is KeyUpEvent ||
        event.logicalKey != LogicalKeyboardKey.arrowRight) {
      return KeyEventResult.ignored;
    }
    final target = _detailTarget(resume: true);
    if (target == null) return KeyEventResult.ignored;
    revealFocus(target);
    return KeyEventResult.handled;
  }

  KeyEventResult _onDetailKey(KeyEvent event) {
    if (event is KeyUpEvent ||
        event.logicalKey != LogicalKeyboardKey.arrowLeft) {
      return KeyEventResult.ignored;
    }
    final focus = FocusManager.instance.primaryFocus;
    if (focus != null && focus.focusInDirection(TraversalDirection.left)) {
      return KeyEventResult.handled;
    }
    revealFocus(_groupFocus[_tabs.index]);
    return KeyEventResult.handled;
  }

  Widget _groupList() => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      Padding(
        padding: const EdgeInsets.fromLTRB(televisionInset, 27, 12, 0),
        child: SizedBox(
          height: 48,
          child: Align(
            alignment: Alignment.centerLeft,
            child: Padding(
              padding: const EdgeInsets.only(left: 8),
              child: Text(
                '模块',
                style: Theme.of(context).textTheme.headlineSmall?.copyWith(
                  fontSize: 22,
                  fontWeight: FontWeight.w800,
                ),
              ),
            ),
          ),
        ),
      ),
      Expanded(
        child: ScrollConfiguration(
          behavior: ScrollConfiguration.of(context).copyWith(scrollbars: false),
          child: ListView(
            padding: const EdgeInsets.fromLTRB(televisionInset, 4, 16, 27),
            children: [
              for (final (index, label) in _tabLabels.indexed)
                Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: SettingsGroupItem(
                    label: label,
                    selected: index == _tabs.index,
                    focusNode: _groupFocus[index],
                    autofocus: index == _tabs.index,
                    onTap: () => _select(index),
                    onFocus: () => _select(index),
                  ),
                ),
            ],
          ),
        ),
      ),
    ],
  );

  Widget _detail() => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      Padding(
        padding: const EdgeInsets.fromLTRB(24, 27, televisionInset, 0),
        child: SizedBox(
          height: 48,
          child: Row(
            children: [
              Expanded(
                child: Semantics(
                  header: true,
                  child: Text(
                    _tabLabels[_tabs.index],
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.titleLarge?.copyWith(
                      fontSize: 22,
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                ),
              ),
              ?_refreshButton(),
            ],
          ),
        ),
      ),
      if (_busy) const LinearProgressIndicator(),
      Expanded(
        child: KeyedSubtree(key: ValueKey(_tabs.index), child: _content()),
      ),
    ],
  );

  Widget _television() => PopScope(
    canPop: !_detailFocused,
    onPopInvokedWithResult: (didPop, _) {
      if (!didPop) revealFocus(_groupFocus[_tabs.index]);
    },
    child: Scaffold(
      body: SafeArea(
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            SizedBox(
              width: 280,
              child: Focus(
                canRequestFocus: false,
                skipTraversal: true,
                onKeyEvent: (_, event) => _onGroupsKey(event),
                child: FocusScope(node: _groupsScope, child: _groupList()),
              ),
            ),
            Expanded(
              child: Focus(
                canRequestFocus: false,
                skipTraversal: true,
                onFocusChange: (focused) =>
                    setState(() => _detailFocused = focused),
                onKeyEvent: (_, event) => _onDetailKey(event),
                child: FocusScope(node: _detailScope, child: _detail()),
              ),
            ),
          ],
        ),
      ),
    ),
  );

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: _store,
    builder: (context, _) => AppScope.of(context).television
        ? _television()
        : Scaffold(
            appBar: AppBar(
              title: const Text('模块'),
              actions: [?_refreshButton(), const SizedBox(width: 8)],
              bottom: PreferredSize(
                preferredSize: const Size.fromHeight(48),
                child: _tabBar(),
              ),
            ),
            body: _body(),
          ),
  );
}

class _ModuleCard extends StatelessWidget {
  const _ModuleCard({
    required this.name,
    required this.adult,
    required this.version,
    required this.id,
    required this.body,
    this.trailing,
    this.leading = const [],
    this.actions = const [],
    this.onTap,
  });
  final String name;
  final bool adult;
  final String version;
  final String id;
  final List<Widget> body;
  final Widget? trailing;
  final List<Widget> leading;
  final List<Widget> actions;
  final VoidCallback? onTap;

  void _reveal(BuildContext context) {
    final box = context.findRenderObject();
    if (box is RenderBox && box.hasSize) {
      box.showOnScreen(rect: (Offset.zero & box.size).inflate(16));
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final television = AppScope.of(context).television;
    final meta = theme.textTheme.bodySmall?.copyWith(
      color: theme.colorScheme.onSurfaceVariant,
      height: 1.35,
    );
    final footer = leading.isNotEmpty || actions.isNotEmpty;
    final bottom = footer ? 10.0 : 14.0;
    final details = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        DefaultTextStyle.merge(
          style: theme.textTheme.titleMedium?.copyWith(fontSize: 18),
          child: _ModuleTitle(name, adult: adult),
        ),
        const SizedBox(height: 2),
        Text('版本：${version.isEmpty ? '未标注' : version}', style: meta),
        Text('标识：$id', style: meta),
        if (body.isNotEmpty) ...[const SizedBox(height: 6), ...body],
      ],
    );
    final header = television
        ? Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(child: _televisionDetails(details, bottom)),
              if (trailing case final trailing?)
                Padding(
                  padding: const EdgeInsets.fromLTRB(0, 14, 12, 0),
                  child: trailing,
                ),
            ],
          )
        : Padding(
            padding: EdgeInsets.fromLTRB(18, 14, 12, bottom),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(child: details),
                ?trailing,
              ],
            ),
          );
    return Focus(
      canRequestFocus: false,
      skipTraversal: true,
      onFocusChange: (focused) {
        if (focused && television) _reveal(context);
      },
      child: GroupCard(
        children: [
          InkWell(
            onTap: television ? null : onTap,
            child: Column(
              children: [
                header,
                if (footer) ...[
                  Divider(
                    height: 1,
                    indent: 18,
                    endIndent: 18,
                    color: theme.colorScheme.outlineVariant.withValues(
                      alpha: 0.6,
                    ),
                  ),
                  Padding(
                    padding: const EdgeInsets.fromLTRB(12, 8, 12, 10),
                    child: Row(
                      spacing: 8,
                      children: [...leading, const Spacer(), ...actions],
                    ),
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _televisionDetails(Widget details, double bottom) {
    final padded = Padding(
      padding: EdgeInsets.fromLTRB(18, 14, 12, bottom),
      child: details,
    );
    final tap = onTap;
    if (tap == null) return padded;
    return TileFocus(
      builder: (focusNode) =>
          InkWell(focusNode: focusNode, onTap: tap, child: padded),
    );
  }
}

ButtonStyle _cardButton(ColorScheme colors, {bool primary = false}) =>
    FilledButton.styleFrom(
      visualDensity: VisualDensity.compact,
      padding: const EdgeInsets.symmetric(horizontal: 14),
      backgroundColor: primary ? colors.primaryContainer : null,
      foregroundColor: primary ? colors.onPrimaryContainer : null,
    );

class _InstalledCard extends StatelessWidget {
  const _InstalledCard({
    required this.module,
    required this.update,
    required this.enabled,
    required this.onToggle,
    required this.admin,
    required this.onDetails,
    required this.onUpdate,
    required this.onUninstall,
  });
  final SourceModule module;
  final ModuleIndexEntry? update;
  final bool enabled;
  final ValueChanged<bool>? onToggle;
  final bool admin;
  final VoidCallback onDetails;
  final VoidCallback? onUpdate;
  final VoidCallback? onUninstall;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final television = AppScope.of(context).television;
    final card = _ModuleCard(
      name: module.name,
      adult: module.adult,
      version: module.version,
      id: module.id,
      body: [
        if (module.error.isNotEmpty)
          Text(module.error, style: TextStyle(color: colors.error))
        else ...[
          if (module.description.isNotEmpty) Text(module.description),
          if (update case final update?)
            Text(
              '可更新到 ${update.version}',
              style: TextStyle(color: colors.primary),
            ),
        ],
      ],
      trailing: television
          ? null
          : FocusRing(
              radius: 20,
              child: Switch(value: enabled, onChanged: onToggle),
            ),
      onTap: onDetails,
      leading: [
        if (!television && module.id == 'sorani' && soraniAccountAvailable())
          SoraniAccountButton(admin: admin, style: _cardButton(colors)),
      ],
      actions: television
          ? const []
          : [
              if (update != null) ...[
                FocusRing(
                  radius: 20,
                  child: FilledButton.tonalIcon(
                    style: _cardButton(colors, primary: true),
                    onPressed: onUpdate,
                    icon: const Icon(Icons.cloud_upload_outlined),
                    label: const Text('更新'),
                  ),
                ),
                FocusRing(
                  radius: 20,
                  child: IconButton.filledTonal(
                    visualDensity: VisualDensity.compact,
                    tooltip: '卸载',
                    onPressed: onUninstall,
                    icon: const Icon(Icons.delete_outline_rounded),
                  ),
                ),
              ] else
                FocusRing(
                  radius: 20,
                  child: FilledButton.tonalIcon(
                    style: _cardButton(colors),
                    onPressed: onUninstall,
                    icon: const Icon(Icons.delete_outline_rounded),
                    label: const Text('卸载'),
                  ),
                ),
            ],
    );
    return television && !enabled ? Opacity(opacity: 0.4, child: card) : card;
  }
}

class _IndexCard extends StatelessWidget {
  const _IndexCard({
    required this.entry,
    required this.records,
    required this.onInstall,
  });
  final ModuleIndexEntry entry;
  final String? records;
  final VoidCallback? onInstall;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return _ModuleCard(
      name: entry.name,
      adult: entry.adult,
      version: entry.version,
      id: entry.id,
      body: [
        if (entry.description.isNotEmpty) Text(entry.description),
        if (records != null)
          Text('待恢复：$records', style: TextStyle(color: colors.primary)),
      ],
      trailing: FocusRing(
        radius: 20,
        child: FilledButton.tonalIcon(
          style: _cardButton(colors, primary: true),
          onPressed: onInstall,
          icon: const Icon(Icons.download_rounded),
          label: const Text('安装'),
        ),
      ),
    );
  }
}

class _ModuleTitle extends StatelessWidget {
  const _ModuleTitle(this.text, {required this.adult});
  final String text;
  final bool adult;

  @override
  Widget build(BuildContext context) {
    if (!adult) return Text(text);
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    return Row(
      spacing: 8,
      children: [
        Flexible(child: Text(text)),
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
          decoration: BoxDecoration(
            color: colors.errorContainer,
            borderRadius: BorderRadius.circular(6),
          ),
          child: Text(
            '限制级',
            style: theme.textTheme.labelSmall?.copyWith(
              color: colors.onErrorContainer,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
      ],
    );
  }
}

List<(String, String, int)> _moduleFacts(
  SourceModule module, {
  required bool install,
}) {
  final version = module.version.isEmpty ? '未标注' : module.version;
  return [
    (
      '版本',
      install && module.installed
          ? '${module.previous.isEmpty ? '未标注' : module.previous} → $version'
          : version,
      1,
    ),
    ('标识', module.id, 1),
    ('内容分级', module.adult ? '限制级' : '非限制级', 1),
    ('可访问的域名', module.hosts.isEmpty ? '无' : module.hosts.join('\n'), 1),
    (
      '提供的功能',
      [
        '目录、详情和播放',
        for (final name in module.capabilities)
          moduleCapabilityNames[name] ?? name,
      ].join('、'),
      2,
    ),
  ];
}

class _ModuleFact extends StatelessWidget {
  const _ModuleFact(this.title, this.body);
  final String title;
  final String body;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(top: 12),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      spacing: 4,
      children: [
        Text(title, style: Theme.of(context).textTheme.labelLarge),
        Text(body),
      ],
    ),
  );
}

class _ModuleDetails extends StatelessWidget {
  const _ModuleDetails({
    required this.module,
    required this.install,
    this.notes = const [],
  });
  final SourceModule module;
  final bool install;
  final List<Widget> notes;

  @override
  Widget build(BuildContext context) {
    final television = AppScope.of(context).television;
    final label = Theme.of(context).textTheme.labelLarge;
    final facts = _moduleFacts(module, install: install);
    final content = SingleChildScrollView(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        spacing: television ? 4 : 0,
        children: [
          ...notes,
          if (module.description.isNotEmpty)
            television
                ? _FoldedText(module.description, maxLines: 3)
                : Text(module.description),
          if (television)
            Padding(
              padding: const EdgeInsets.only(top: 12),
              child: Table(
                columnWidths: const {0: IntrinsicColumnWidth()},
                defaultVerticalAlignment: TableCellVerticalAlignment.baseline,
                textBaseline: TextBaseline.alphabetic,
                children: [
                  for (final (title, body, lines) in facts)
                    TableRow(
                      children: [
                        Padding(
                          padding: const EdgeInsets.only(right: 24, bottom: 12),
                          child: Text(title, style: label),
                        ),
                        Padding(
                          padding: const EdgeInsets.only(bottom: 12),
                          child: _FoldedText(
                            body,
                            maxLines: lines,
                            list: body.contains('\n'),
                          ),
                        ),
                      ],
                    ),
                ],
              ),
            )
          else
            for (final (title, body, _) in facts) _ModuleFact(title, body),
        ],
      ),
    );
    return television ? SizedBox(width: 600, child: content) : content;
  }
}

class _FoldedText extends StatelessWidget {
  const _FoldedText(this.text, {required this.maxLines, this.list = false});
  final String text;
  final int maxLines;
  final bool list;

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final items = list ? text.split('\n') : [text];
      final shown = items.first;
      final suffix = items.length > 1 ? ' +${items.length - 1}' : null;
      final painter = TextPainter(
        text: TextSpan(text: shown, style: DefaultTextStyle.of(context).style),
        maxLines: maxLines,
        textDirection: Directionality.of(context),
        textScaler: MediaQuery.textScalerOf(context),
      )..layout(maxWidth: constraints.maxWidth);
      final folded = suffix != null || painter.didExceedMaxLines;
      painter.dispose();
      final label = Text(
        shown,
        maxLines: maxLines,
        overflow: TextOverflow.ellipsis,
      );
      final content = suffix == null
          ? label
          : Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Flexible(child: label),
                Text(suffix),
              ],
            );
      if (!folded) return content;
      return FocusRing(
        radius: 8,
        scale: 1,
        child: InkWell(
          focusColor: Colors.transparent,
          borderRadius: BorderRadius.circular(8),
          onTap: () => showDialog<void>(
            context: context,
            builder: (_) => _FullTextDialog(text),
          ),
          child: content,
        ),
      );
    },
  );
}

class _FullTextDialog extends StatefulWidget {
  const _FullTextDialog(this.text);
  final String text;

  @override
  State<_FullTextDialog> createState() => _FullTextDialogState();
}

class _FullTextDialogState extends State<_FullTextDialog> {
  final _scroll = ScrollController();

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  KeyEventResult _onKey(KeyEvent event) {
    final step = switch (event.logicalKey) {
      LogicalKeyboardKey.arrowUp => -120.0,
      LogicalKeyboardKey.arrowDown => 120.0,
      _ => null,
    };
    if (event is KeyUpEvent || step == null || !_scroll.hasClients) {
      return KeyEventResult.ignored;
    }
    final position = _scroll.position;
    _scroll.animateTo(
      (position.pixels + step).clamp(0, position.maxScrollExtent),
      duration: const Duration(milliseconds: 150),
      curve: Curves.easeOut,
    );
    return KeyEventResult.handled;
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    content: SizedBox(
      width: 600,
      child: Focus(
        autofocus: true,
        onKeyEvent: (_, event) => _onKey(event),
        child: SingleChildScrollView(
          controller: _scroll,
          child: Text(widget.text),
        ),
      ),
    ),
  );
}

class _ModuleDialog extends StatelessWidget {
  const _ModuleDialog({required this.module, required this.install});
  final SourceModule module;
  final bool install;

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text(
      !install
          ? module.name
          : module.installed
          ? '更新 ${module.name}？'
          : '安装 ${module.name}？',
    ),
    content: _ModuleDetails(module: module, install: install),
    actions: [
      if (install) ...[
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
      ] else
        FocusRing(
          radius: 20,
          child: TextButton(
            autofocus: true,
            onPressed: () => Navigator.pop(context),
            child: const Text('关闭'),
          ),
        ),
    ],
  );
}

class _ModuleMenu extends StatelessWidget {
  const _ModuleMenu({
    required this.module,
    required this.update,
    required this.enabled,
    required this.onToggle,
    required this.admin,
    required this.onUpdate,
    required this.onUninstall,
  });
  final SourceModule module;
  final ModuleIndexEntry? update;
  final bool enabled;
  final ValueChanged<bool>? onToggle;
  final bool admin;
  final VoidCallback? onUpdate;
  final VoidCallback? onUninstall;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final toggle = onToggle;
    return AlertDialog(
      title: Text(module.name),
      content: _ModuleDetails(
        module: module,
        install: false,
        notes: [
          if (module.error.isNotEmpty)
            Text(module.error, style: TextStyle(color: colors.error)),
          if (update case final update?)
            Text(
              '可更新到 ${update.version}',
              style: TextStyle(color: colors.primary),
            ),
        ],
      ),
      actions: [
        if (module.id == 'sorani' && soraniAccountAvailable())
          SoraniAccountButton(admin: admin),
        if (update != null)
          FocusRing(
            radius: 20,
            child: FilledButton.tonalIcon(
              onPressed: onUpdate,
              icon: const Icon(Icons.cloud_upload_outlined),
              label: const Text('更新'),
            ),
          ),
        FocusRing(
          radius: 20,
          child: FilledButton.tonalIcon(
            autofocus: toggle == null,
            onPressed: onUninstall,
            icon: const Icon(Icons.delete_outline_rounded),
            label: const Text('卸载'),
          ),
        ),
        FocusRing(
          radius: 20,
          child: FilledButton.tonalIcon(
            autofocus: true,
            onPressed: toggle == null ? null : () => toggle(!enabled),
            icon: Icon(
              enabled
                  ? Icons.block_rounded
                  : Icons.check_circle_outline_rounded,
            ),
            label: Text(enabled ? '禁用' : '启用'),
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

class _Loading extends StatelessWidget {
  const _Loading();

  @override
  Widget build(BuildContext context) => const Padding(
    padding: EdgeInsets.all(24),
    child: Center(child: CircularProgressIndicator()),
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
