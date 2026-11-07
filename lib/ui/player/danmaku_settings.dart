import 'package:flutter/material.dart';

import '../../catalog_sort.dart';
import '../../playback_preferences.dart';
import '../../playback_session.dart';
import '../widgets/focus_ring.dart';
import '../widgets/form_text_field.dart';
import '../widgets/slider_tile.dart';

const _colorLabels = ['白色', '黄色', '青色', '粉色', '绿色'];

class DanmakuSettings extends StatelessWidget {
  const DanmakuSettings({
    super.key,
    this.session,
    this.preferences,
    this.save,
    this.onFailure,
    this.dense = false,
  }) : assert(
         session != null || (preferences != null && save != null),
         '需要 session，或者 preferences 与 save',
       );

  final PlaybackSession? session;
  final PlaybackPreferences? preferences;
  final ValueChanged<PlaybackPreferences>? save;
  final VoidCallback? onFailure;

  final bool dense;

  PlaybackPreferences get _preferences =>
      preferences ?? session?.preferences ?? const PlaybackPreferences();

  Future<void> _apply(Future<void> Function() change) async {
    try {
      await change();
    } catch (_) {
      onFailure?.call();
    }
  }

  void _save(PlaybackPreferences next) => save?.call(next);

  double get _scale => _preferences.danmakuScale;
  double get _opacity => _preferences.danmakuOpacity;
  double get _stroke => _preferences.danmakuStroke;
  double get _shadow => _preferences.danmakuShadow;

  Widget _scaleTile() => SliderTile(
    title: '字号',
    subtitle: '相对默认字号的比例',
    value: _scale,
    min: danmakuScaleMin,
    max: danmakuScaleMax,
    divisions: 14,
    label: (value) => '${(value * 100).round()}%',
    onChanged: (value) => _apply(
      () => session?.setDanmakuScale(value) ?? _asyncSave(scale: value),
    ),
  );

  Widget _opacityTile() => SliderTile(
    title: '透明度',
    subtitle: '调低后弹幕更透，不挡画面',
    value: _opacity,
    min: .1,
    max: 1,
    divisions: 18,
    label: (value) => '${(value * 100).round()}%',
    onChanged: (value) => _apply(
      () => session?.setDanmakuOpacity(value) ?? _asyncSave(opacity: value),
    ),
  );

  Widget _strokeTile() => SliderTile(
    title: '描边',
    subtitle: _stroke == 0 ? '关闭' : '描边越粗字越突出',
    value: _stroke,
    min: danmakuStrokeMin,
    max: danmakuStrokeMax,
    divisions: 12,
    label: (value) => value == 0 ? '关闭' : value.toStringAsFixed(1),
    onChanged: (value) => _apply(
      () => session?.setDanmakuStroke(value) ?? _asyncSave(stroke: value),
    ),
  );

  Widget _shadowTile() => SliderTile(
    title: '阴影',
    subtitle: _shadow == 0 ? '关闭' : '阴影越浓字越清晰',
    value: _shadow,
    min: danmakuShadowMin,
    max: danmakuShadowMax,
    divisions: 12,
    label: (value) => value == 0 ? '关闭' : value.toStringAsFixed(1),
    onChanged: (value) => _apply(
      () => session?.setDanmakuShadow(value) ?? _asyncSave(shadow: value),
    ),
  );

  Future<void> _asyncSave({
    double? scale,
    int? color,
    double? opacity,
    double? stroke,
    double? shadow,
    bool? bold,
    DanmakuCoverage? coverage,
    List<String>? words,
  }) async {
    _save(
      _preferences.copyWith(
        danmakuScale: scale,
        danmakuColor: color,
        danmakuOpacity: opacity,
        danmakuStroke: stroke,
        danmakuShadow: shadow,
        danmakuBold: bold,
        danmakuCoverage: coverage,
        danmakuWords: words,
      ),
    );
  }

  Future<void> _editWords(BuildContext context) async {
    final words = await showDialog<List<String>>(
      context: context,
      builder: (_) => _WordsDialog(initial: _preferences.danmakuWords),
    );
    if (words == null) return;
    await _apply(
      () => session?.setDanmakuWords(words) ?? _asyncSave(words: words),
    );
  }

  @override
  Widget build(BuildContext context) {
    final preferences = _preferences;
    final colors = Theme.of(context).colorScheme;
    final label = TextStyle(fontSize: 14, color: colors.onSurfaceVariant);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _section(context, '外观'),
        _scaleTile(),
        Padding(
          padding: const EdgeInsets.fromLTRB(0, 4, 0, 8),
          child: Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final (index, color) in danmakuColors.indexed)
                ChoiceChip(
                  avatar: CircleAvatar(backgroundColor: Color(color)),
                  label: Text(_colorLabels[index]),
                  selected: preferences.danmakuColor == color,
                  showCheckmark: false,
                  shape: const StadiumBorder(),
                  onSelected: (_) => _apply(
                    () =>
                        session?.setDanmakuColor(color) ??
                        _asyncSave(color: color),
                  ),
                ),
            ],
          ),
        ),
        _opacityTile(),
        SwitchListTile(
          contentPadding: EdgeInsets.zero,
          title: const Text('加粗'),
          subtitle: const Text('加粗的字在亮背景上更清楚'),
          value: preferences.danmakuBold,
          onChanged: (value) => _apply(
            () => session?.setDanmakuBold(value) ?? _asyncSave(bold: value),
          ),
        ),
        _strokeTile(),
        _shadowTile(),
        _section(context, '显示区域'),
        Padding(
          padding: const EdgeInsets.fromLTRB(0, 4, 0, 8),
          child: Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final coverage in DanmakuCoverage.values)
                ChoiceChip(
                  label: Text(coverage.label),
                  selected: preferences.danmakuCoverage == coverage,
                  showCheckmark: false,
                  shape: const StadiumBorder(),
                  onSelected: (_) => _apply(
                    () =>
                        session?.setDanmakuCoverage(coverage) ??
                        _asyncSave(coverage: coverage),
                  ),
                ),
            ],
          ),
        ),
        _section(context, '屏蔽词'),
        Text(
          preferences.danmakuWords.isEmpty
              ? '没有屏蔽词，含屏蔽词的弹幕会直接不显示。'
              : '已屏蔽 ${preferences.danmakuWords.length} 个词：${preferences.danmakuWords.join('、')}',
          style: TextStyle(fontSize: 13, height: 1.4, color: label.color),
        ),
        const SizedBox(height: 8),
        Align(
          alignment: Alignment.centerLeft,
          child: FocusRing(
            radius: 20,
            child: OutlinedButton.icon(
              onPressed: () => _editWords(context),
              icon: const Icon(Icons.filter_alt_outlined, size: 18),
              label: const Text('管理屏蔽词'),
            ),
          ),
        ),
      ],
    );
  }

  Widget _section(BuildContext context, String title) => Padding(
    padding: EdgeInsets.fromLTRB(0, dense ? 18 : 24, 0, 8),
    child: Text(
      title,
      style: Theme.of(
        context,
      ).textTheme.titleSmall?.copyWith(fontWeight: FontWeight.w700),
    ),
  );
}

class _WordsDialog extends StatefulWidget {
  const _WordsDialog({required this.initial});
  final List<String> initial;

  @override
  State<_WordsDialog> createState() => _WordsDialogState();
}

class _WordsDialogState extends State<_WordsDialog> {
  late final _controller = TextEditingController();
  late List<String> _words = [...widget.initial];
  String? _error;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _add() {
    final word = _controller.text.trim();
    if (word.isEmpty) return;
    if (word.length > danmakuWordLength) {
      setState(() => _error = '单个屏蔽词最多 $danmakuWordLength 个字');
      return;
    }
    final key = normalizedSearchText(word);
    if (key.isEmpty) {
      setState(() => _error = '只有标点或空格的词无法匹配弹幕');
      return;
    }
    if (_words.any((entry) => normalizedSearchText(entry) == key)) {
      setState(() => _error = '这个词已经在列表里了');
      return;
    }
    if (_words.length >= danmakuWordLimit) {
      setState(() => _error = '最多只能添加 $danmakuWordLimit 个屏蔽词');
      return;
    }
    setState(() {
      _words = [..._words, word];
      _error = null;
    });
    _controller.clear();
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return AlertDialog(
      title: const Text('弹幕屏蔽词'),
      content: SizedBox(
        width: 420,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              spacing: 8,
              children: [
                Expanded(
                  child: FormTextField(
                    controller: _controller,
                    maxLength: danmakuWordLength,
                    decoration: const InputDecoration(
                      hintText: '输入要屏蔽的词',
                      counterText: '',
                    ),
                    onSubmitted: (_) => _add(),
                  ),
                ),
                FilledButton(onPressed: _add, child: const Text('添加')),
              ],
            ),
            const SizedBox(height: 8),
            if (_error case final error?)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: Text(
                  error,
                  style: TextStyle(fontSize: 13, color: colors.error),
                ),
              ),
            if (_words.isEmpty)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 16),
                child: Text(
                  '还没有屏蔽词',
                  style: TextStyle(color: colors.onSurfaceVariant),
                ),
              )
            else
              ConstrainedBox(
                constraints: const BoxConstraints(maxHeight: 260),
                child: SingleChildScrollView(
                  child: Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      for (final word in _words)
                        InputChip(
                          label: Text(word),
                          onDeleted: () => setState(
                            () => _words = [
                              for (final entry in _words)
                                if (entry != word) entry,
                            ],
                          ),
                        ),
                    ],
                  ),
                ),
              ),
          ],
        ),
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
          child: FilledButton(
            onPressed: () => Navigator.pop(context, _words),
            child: const Text('保存'),
          ),
        ),
      ],
    );
  }
}
