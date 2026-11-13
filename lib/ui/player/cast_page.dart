import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';

import '../../core_bridge.dart';
import '../../models.dart';
import '../../playback_session.dart';
import 'playback_sheets.dart';
import 'player_surface.dart' show formatClock;

bool get castSupported => !Platform.isIOS;

Future<void> startCast(BuildContext context, PlaybackSession session) async {
  if (session.player.state.playing) session.togglePlayback();
  final navigator = Navigator.of(context);
  CastDevice? picked;
  await showPlayerSheet(
    context,
    (context) => _CastDevices(
      repository: session.repository,
      onPick: (device) {
        picked = device;
        Navigator.pop(context);
      },
    ),
  );
  final device = picked;
  if (device == null || !navigator.mounted) return;
  final result = await navigator.push<(int, double)>(
    MaterialPageRoute(
      builder: (_) => CastPage(session: session, device: device),
    ),
  );
  if (result == null) return;
  final (index, position) = result;
  if (index != session.index ||
      (position - session.currentPosition).abs() > 2) {
    unawaited(session.play(index, position: position, playWhenReady: false));
  }
}

class _CastDevices extends StatefulWidget {
  const _CastDevices({required this.repository, required this.onPick});
  final AppRepository repository;
  final ValueChanged<CastDevice> onPick;

  @override
  State<_CastDevices> createState() => _CastDevicesState();
}

class _CastDevicesState extends State<_CastDevices> {
  List<CastDevice> _devices = const [];
  bool _searching = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    unawaited(_search());
  }

  Future<void> _search() async {
    if (_searching) return;
    setState(() {
      _searching = true;
      _error = null;
    });
    try {
      final devices = await widget.repository.castDevices();
      if (mounted) setState(() => _devices = devices);
    } catch (error) {
      if (mounted) setState(() => _error = '$error');
    }
    if (mounted) setState(() => _searching = false);
  }

  Widget _message(String text) => Center(
    child: Padding(
      padding: const EdgeInsets.all(24),
      child: Text(text, textAlign: TextAlign.center),
    ),
  );

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      SheetHeader(
        children: [
          const Expanded(child: Text('选择投屏设备', style: sheetTitle)),
          IconButton(
            onPressed: _searching ? null : _search,
            icon: const Icon(Icons.refresh_rounded),
            tooltip: '重新搜索',
          ),
          IconButton(
            onPressed: () => Navigator.pop(context),
            icon: const Icon(Icons.close_rounded),
            tooltip: '关闭',
          ),
        ],
      ),
      Expanded(
        child: _searching
            ? const Center(child: CircularProgressIndicator())
            : _error != null
            ? _message(_error!)
            : _devices.isEmpty
            ? _message('没有找到可投屏的设备。\n请确认电视或盒子已开机，并且和本机连着同一个 Wi-Fi，然后重新搜索。')
            : ListView(
                children: [
                  for (final device in _devices)
                    ListTile(
                      leading: const Icon(Icons.tv_rounded),
                      title: Text(device.name),
                      onTap: () => widget.onPick(device),
                    ),
                ],
              ),
      ),
    ],
  );
}

class CastPage extends StatefulWidget {
  const CastPage({super.key, required this.session, required this.device});
  final PlaybackSession session;
  final CastDevice device;

  @override
  State<CastPage> createState() => _CastPageState();
}

class _CastPageState extends State<CastPage> {
  late int _index = widget.session.index;
  Timer? _timer;
  Future<void> _pending = Future.value();
  String _state = 'loading';
  String? _error;
  double? _drag;
  double _position = 0;
  double _duration = 0;
  int _ticks = 0;
  int _failures = 0;
  bool _busy = false;
  bool _started = false;
  bool _played = false;
  bool _polling = false;
  bool _leaving = false;

  PlaybackSession get _session => widget.session;
  List<Episode> get _episodes => _session.detail.episodes;

  @override
  void initState() {
    super.initState();
    _timer = Timer.periodic(const Duration(seconds: 1), (_) => _poll());
    _play(_index, position: _session.currentPosition);
  }

  @override
  void dispose() {
    _timer?.cancel();
    if (!_leaving) {
      unawaited(
        _session.repository.castControl('stop').catchError((Object _) {}),
      );
    }
    super.dispose();
  }

  void _play(int index, {double position = 0}) {
    if (_busy || _leaving) return;
    setState(() {
      _busy = true;
      _started = false;
      _played = false;
      _error = null;
      _index = index;
      _position = position;
      _duration = 0;
      _state = 'loading';
    });
    _pending = () async {
      try {
        await _session.repository.castPlay(
          widget.device,
          _session.detail.drama,
          _episodes[index],
          quality: _session.quality,
          position: position,
        );
        _started = true;
      } catch (error) {
        _error = '$error';
      }
      _busy = false;
      if (mounted) setState(() {});
    }();
  }

  Future<void> _poll() async {
    if (!_started || _busy || _polling || _leaving) return;
    _polling = true;
    try {
      final status = await _session.repository.castStatus();
      if (!mounted || _busy || _leaving) return;
      _failures = 0;
      var ended = false;
      if (status.state == 'playing' || status.state == 'paused') {
        _played = true;
        _position = status.position;
        if (status.duration > 0) _duration = status.duration;
        if (++_ticks % 5 == 0) _save();
      } else if (status.state == 'stopped' && _played) {
        _played = false;
        ended = _duration > 0 && _position >= _duration - 5;
        if (ended) _position = _duration;
        _save();
      }
      setState(() {
        _state = status.state;
        _error = null;
      });
      if (ended && _index + 1 < _episodes.length) _play(_index + 1);
    } catch (error) {
      if (mounted && !_busy && ++_failures >= 3) {
        setState(() => _error = '$error');
      }
    } finally {
      _polling = false;
    }
  }

  void _save() {
    if (_position < .1) return;
    unawaited(
      _session.store
          .saveWatch(
            WatchEntry(
              drama: _session.repository.catalogUpdates.current(
                _session.detail.drama,
              ),
              episode: _episodes[_index].number,
              position: _position,
              duration: _duration,
              updatedAt: DateTime.now(),
            ),
            quiet: true,
          )
          .catchError((Object _) {}),
    );
  }

  Future<void> _control(String command, {double position = 0}) async {
    try {
      await _session.repository.castControl(command, position: position);
    } catch (error) {
      if (mounted) setState(() => _error = '$error');
    }
  }

  Future<void> _exit() async {
    if (_leaving) return;
    setState(() => _leaving = true);
    _timer?.cancel();
    _save();
    final navigator = Navigator.of(context);
    await _pending;
    try {
      await _session.repository.castControl('stop');
    } catch (_) {}
    if (mounted) navigator.pop((_index, _position));
  }

  String get _label {
    if (_leaving) return '正在结束投屏…';
    if (_busy) return '正在连接「${widget.device.name}」…';
    if (!_started) return '投屏没有成功';
    return switch (_state) {
      'playing' => '正在播放',
      'paused' => '已暂停',
      'loading' => '电视正在加载…',
      _ => '已停止',
    };
  }

  @override
  Widget build(BuildContext context) => Theme(
    data: playerTheme(context),
    child: Builder(
      builder: (context) {
        final colors = Theme.of(context).colorScheme;
        final active = _started && !_busy && !_leaving;
        final seekable = active && _duration > 0;
        final max = _duration > 0 ? _duration : 1.0;
        final shown = (_drag ?? _position).clamp(0.0, max);
        final muted = TextStyle(fontSize: 13, color: colors.onSurfaceVariant);
        return PopScope(
          canPop: false,
          onPopInvokedWithResult: (didPop, _) {
            if (!didPop) unawaited(_exit());
          },
          child: Scaffold(
            appBar: AppBar(
              leading: BackButton(onPressed: _exit),
              title: const Text('投屏'),
            ),
            body: SafeArea(
              child: Center(
                child: SingleChildScrollView(
                  padding: const EdgeInsets.all(24),
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 480),
                    child: Column(
                      spacing: 12,
                      children: [
                        Icon(
                          Icons.cast_connected_rounded,
                          size: 56,
                          color: colors.primary,
                        ),
                        Text(
                          widget.device.name,
                          textAlign: TextAlign.center,
                          style: const TextStyle(
                            fontSize: 20,
                            fontWeight: FontWeight.w700,
                          ),
                        ),
                        Text(
                          '${_session.detail.drama.title} · ${_episodes[_index].title}',
                          textAlign: TextAlign.center,
                        ),
                        Text(_label, style: muted),
                        if (_error case final error?)
                          Text(
                            error,
                            textAlign: TextAlign.center,
                            style: TextStyle(color: colors.error),
                          ),
                        Slider(
                          value: shown,
                          max: max,
                          onChanged: seekable
                              ? (value) => setState(() => _drag = value)
                              : null,
                          onChangeEnd: (value) {
                            setState(() {
                              _drag = null;
                              _position = value;
                            });
                            unawaited(_control('seek', position: value));
                          },
                        ),
                        Text(
                          '${formatClock(Duration(milliseconds: (shown * 1000).round()))} / ${formatClock(Duration(milliseconds: (_duration * 1000).round()))}',
                          style: muted.copyWith(
                            fontFeatures: const [FontFeature.tabularFigures()],
                          ),
                        ),
                        Row(
                          mainAxisAlignment: MainAxisAlignment.center,
                          spacing: 16,
                          children: [
                            IconButton(
                              onPressed: !_busy && !_leaving && _index > 0
                                  ? () => _play(_index - 1)
                                  : null,
                              icon: const Icon(Icons.skip_previous_rounded),
                              iconSize: 32,
                              tooltip: '上一集',
                            ),
                            if (_started || _busy)
                              IconButton.filled(
                                onPressed: active
                                    ? () => unawaited(
                                        _control(
                                          _state == 'playing'
                                              ? 'pause'
                                              : 'resume',
                                        ),
                                      )
                                    : null,
                                icon: Icon(
                                  _state == 'playing'
                                      ? Icons.pause_rounded
                                      : Icons.play_arrow_rounded,
                                ),
                                iconSize: 40,
                                tooltip: _state == 'playing' ? '暂停' : '播放',
                              )
                            else
                              IconButton.filled(
                                onPressed: _leaving
                                    ? null
                                    : () => _play(_index, position: _position),
                                icon: const Icon(Icons.refresh_rounded),
                                iconSize: 40,
                                tooltip: '重试',
                              ),
                            IconButton(
                              onPressed:
                                  !_busy &&
                                      !_leaving &&
                                      _index + 1 < _episodes.length
                                  ? () => _play(_index + 1)
                                  : null,
                              icon: const Icon(Icons.skip_next_rounded),
                              iconSize: 32,
                              tooltip: '下一集',
                            ),
                          ],
                        ),
                        FilledButton.tonal(
                          onPressed: _leaving ? null : _exit,
                          child: const Text('结束投屏'),
                        ),
                        Text(
                          Platform.isAndroid
                              ? '投屏期间可以切到后台或熄屏，但不要从最近任务里划掉应用：视频要经本机转给电视，自动下一集和观看进度也靠应用运行。'
                              : '投屏期间请保持应用运行：视频要经本机转给电视，自动下一集和观看进度也靠应用运行。',
                          textAlign: TextAlign.center,
                          style: muted,
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
          ),
        );
      },
    ),
  );
}
