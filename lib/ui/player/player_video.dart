import 'dart:async';
import 'dart:io';
import 'dart:math' as math;
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';

import 'playback_handle.dart';

class PlayerVideo extends StatefulWidget {
  const PlayerVideo({
    super.key,
    required this.handle,
    required this.player,
    this.fit = BoxFit.contain,
    this.covered = false,
  });
  final PlaybackHandle handle;
  final Player player;

  /// 竖屏视频用 [BoxFit.cover] 铺满屏幕，横屏视频用 [BoxFit.contain] 保留完整画面。
  final BoxFit fit;

  /// 恢复瞬间遮住重建纹理的花屏帧。
  final bool covered;

  @override
  State<PlayerVideo> createState() => _PlayerVideoState();
}

class _PlayerVideoState extends State<PlayerVideo> with WidgetsBindingObserver {
  final _boundary = GlobalKey();
  ui.Image? _lastFrame;
  double _pixelRatio = 1;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _lastFrame?.dispose();
    super.dispose();
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _pixelRatio = MediaQuery.devicePixelRatioOf(context);
  }

  /// 切出/锁屏瞬间（inactive，进程尚未挂起、画面仍在渲染）抓取最后一帧，
  /// 回前台重建纹理期间用它替代纯黑幕——用户看到的是画面静止而不是黑屏。
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.inactive) {
      // 只抓正在出声的那一路；待机播放器是暂停态，抓了也用不上。
      if (widget.player.state.playing) unawaited(_capture());
    }
  }

  Future<void> _capture() async {
    final boundary =
        _boundary.currentContext?.findRenderObject() as RenderRepaintBoundary?;
    if (boundary == null || boundary.size.isEmpty) return;
    try {
      final image = await boundary.toImage(
        pixelRatio: math.min(_pixelRatio, 3.0),
      );
      if (!mounted) {
        image.dispose();
        return;
      }
      final old = _lastFrame;
      setState(() => _lastFrame = image);
      old?.dispose();
    } catch (_) {
      // 抓帧失败（例如系统已在挂起边缘）——黑幕兜底，行为与旧版一致。
    }
  }

  @override
  Widget build(BuildContext context) {
    final controller = widget.handle.video(widget.player);
    final video = Video(
      controller: controller,
      controls: NoVideoControls,
      fill: Colors.black,
      fit: widget.fit,
      pauseUponEnteringBackgroundMode: false,
    );
    final surface = Platform.isWindows
        ? LayoutBuilder(
            builder: (context, constraints) {
              final ratio = MediaQuery.devicePixelRatioOf(context);
              final box = constraints.biggest * ratio;
              if (!box.isFinite || box.isEmpty) return video;
              return _OutputSize(
                controller: controller,
                box: box,
                child: video,
              );
            },
          )
        : video;
    // 黑幕始终在同一位置，只改变透明度：来回切换 widget 结构会让 Video
    // 重新挂载纹理，反而制造新的花屏帧。
    // 盖幕期间优先显示切出前抓到的最后一帧（红果体验），
    // 没抓到（换集、首次起播）则退回纯黑。
    return Stack(
      fit: StackFit.expand,
      children: [
        RepaintBoundary(key: _boundary, child: surface),
        IgnorePointer(
          child: AnimatedOpacity(
            opacity: widget.covered ? 1 : 0,
            duration: const Duration(milliseconds: 120),
            child: _cover(),
          ),
        ),
      ],
    );
  }

  Widget _cover() {
    final frame = _lastFrame;
    if (frame == null) return const ColoredBox(color: Colors.black);
    return ColoredBox(
      color: Colors.black,
      child: RawImage(
        image: frame,
        fit: widget.fit,
        alignment: Alignment.center,
      ),
    );
  }
}

class _OutputSize extends StatefulWidget {
  const _OutputSize({
    required this.controller,
    required this.box,
    required this.child,
  });
  final VideoController controller;
  final Size box;
  final Widget child;

  @override
  State<_OutputSize> createState() => _OutputSizeState();
}

class _OutputSizeState extends State<_OutputSize> {
  @override
  void initState() {
    super.initState();
    _VideoOutput.request(widget.controller, this, widget.box);
  }

  @override
  void didUpdateWidget(_OutputSize oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.controller != widget.controller) {
      _VideoOutput.release(oldWidget.controller, this);
    }
    if (oldWidget.controller != widget.controller ||
        oldWidget.box != widget.box) {
      _VideoOutput.request(widget.controller, this, widget.box);
    }
  }

  @override
  void dispose() {
    _VideoOutput.release(widget.controller, this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => widget.child;
}

class _VideoOutput {
  _VideoOutput(this.controller) {
    _subscription = controller.player.stream.videoParams.listen(
      (_) => _schedule(),
    );
  }

  static final _outputs = <VideoController, _VideoOutput>{};

  static void request(VideoController controller, Object owner, Size box) {
    final output = _outputs[controller] ??= _VideoOutput(controller);
    output._boxes[owner] = box;
    output._schedule();
  }

  static void release(VideoController controller, Object owner) {
    final output = _outputs[controller];
    if (output == null) return;
    output._boxes.remove(owner);
    if (output._boxes.isNotEmpty) return;
    output._timer?.cancel();
    unawaited(output._subscription.cancel());
    _outputs.remove(controller);
  }

  final VideoController controller;
  final _boxes = <Object, Size>{};
  late final StreamSubscription<VideoParams> _subscription;
  Timer? _timer;

  void _schedule() {
    _timer?.cancel();
    _timer = Timer(const Duration(milliseconds: 300), _apply);
  }

  void _apply() {
    final params = controller.player.state.videoParams;
    final width = params.dw ?? params.w ?? 0;
    final height = params.dh ?? params.h ?? 0;
    if (width <= 0 || height <= 0 || _boxes.isEmpty) return;
    final scale = math.min(
      1.0,
      _boxes.values
          .map((box) => math.min(box.width / width, box.height / height))
          .reduce(math.max),
    );
    unawaited(
      controller
          .setSize(
            width: math.max(1, (width * scale).round()),
            height: math.max(1, (height * scale).round()),
          )
          .catchError((Object _) {}),
    );
  }
}
