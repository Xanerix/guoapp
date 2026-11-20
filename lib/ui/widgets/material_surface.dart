import 'dart:async';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/foundation.dart';

import '../app_scope.dart';

enum GlassSurfaceMode { cached, realtime, tint }

class MaterialSurface extends StatelessWidget {
  const MaterialSurface({
    super.key,
    this.color,
    this.borderRadius,
    this.shape,
    this.type = MaterialType.canvas,
    this.elevation = 0,
    this.shadowColor,
    this.surfaceTintColor,
    this.textStyle,
    this.clipBehavior,
    this.animationDuration = kThemeChangeDuration,
    this.borderOnForeground = true,
    this.mode = GlassSurfaceMode.cached,
    this.child,
  });

  final Color? color;
  final BorderRadius? borderRadius;
  final ShapeBorder? shape;
  final MaterialType type;
  final double elevation;
  final Color? shadowColor;
  final Color? surfaceTintColor;
  final TextStyle? textStyle;
  final Clip? clipBehavior;
  final Duration animationDuration;
  final bool borderOnForeground;
  final GlassSurfaceMode mode;
  final Widget? child;

  static bool glassEnabled(BuildContext context) =>
      (AppScope.maybeOf(context)?.liquidGlass ?? false) &&
      !MediaQuery.highContrastOf(context);

  @override
  Widget build(BuildContext context) {
    if (!glassEnabled(context) ||
        type == MaterialType.transparency ||
        color == Colors.transparent ||
        color == Colors.black) {
      return Material(
        color: color,
        borderRadius: borderRadius,
        shape: shape,
        type: type,
        elevation: elevation,
        shadowColor: shadowColor,
        surfaceTintColor: surfaceTintColor,
        textStyle: textStyle,
        clipBehavior: clipBehavior ?? Clip.none,
        animationDuration: animationDuration,
        borderOnForeground: borderOnForeground,
        child: child,
      );
    }
    final colors = Theme.of(context).colorScheme;
    final effectiveColor = color ?? colors.surfaceContainer;
    final effectiveShape =
        shape ??
        (type == MaterialType.circle
            ? const CircleBorder()
            : RoundedRectangleBorder(
                borderRadius: borderRadius ?? BorderRadius.zero,
              ));
    final dark = Theme.of(context).brightness == Brightness.dark;
    final tint = ShapeDecoration(
      shape: effectiveShape,
      gradient: LinearGradient(
        begin: Alignment.topLeft,
        end: Alignment.bottomRight,
        colors: [
          Color.alphaBlend(
            Colors.white.withValues(alpha: dark ? .08 : .24),
            effectiveColor.withValues(alpha: dark ? .76 : .64),
          ),
          effectiveColor.withValues(alpha: dark ? .68 : .52),
        ],
      ),
    );
    final outline = ShapeDecoration(
      shape: effectiveShape,
      gradient: LinearGradient(
        colors: [
          Colors.white.withValues(alpha: dark ? .22 : .58),
          Colors.white.withValues(alpha: .04),
        ],
        begin: Alignment.topLeft,
        end: Alignment.bottomRight,
      ),
    );
    final background = switch (mode) {
      GlassSurfaceMode.cached => _CachedGlassPaint(shape: effectiveShape),
      GlassSurfaceMode.realtime => _LiveGlassPaint(shape: effectiveShape),
      GlassSurfaceMode.tint => const SizedBox.expand(),
    };
    return DecoratedBox(
      decoration: ShapeDecoration(
        shape: effectiveShape,
        shadows: mode == GlassSurfaceMode.realtime && elevation > 0
            ? [
                BoxShadow(
                  color: Colors.black.withValues(alpha: .12),
                  blurRadius: 8,
                  offset: const Offset(0, 3),
                ),
              ]
            : null,
      ),
      child: ClipPath(
        clipper: ShapeBorderClipper(
          shape: effectiveShape,
          textDirection: Directionality.of(context),
        ),
        clipBehavior: clipBehavior == null || clipBehavior == Clip.none
            ? Clip.antiAlias
            : clipBehavior!,
        child: Stack(
          fit: StackFit.passthrough,
          children: [
            Positioned.fill(child: IgnorePointer(child: background)),
            Positioned.fill(
              child: IgnorePointer(child: DecoratedBox(decoration: tint)),
            ),
            Material(
              type: MaterialType.transparency,
              textStyle: textStyle,
              child: child,
            ),
            Positioned.fill(
              child: IgnorePointer(
                child: CustomPaint(
                  painter: _GlassRim(
                    effectiveShape,
                    outline,
                    Directionality.of(context),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

double _radius(ShapeBorder shape, Size size, TextDirection direction) =>
    switch (shape) {
      RoundedRectangleBorder(:final borderRadius) =>
        borderRadius.resolve(direction).topLeft.x,
      CircleBorder() || StadiumBorder() => size.shortestSide / 2,
      _ => 0,
    };

class _GlassRim extends CustomPainter {
  const _GlassRim(this.shape, this.decoration, this.direction);
  final ShapeBorder shape;
  final ShapeDecoration decoration;
  final TextDirection direction;
  @override
  void paint(Canvas canvas, Size size) {
    final rect = Offset.zero & size;
    final paint = Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = .8
      ..shader = decoration.gradient!.createShader(rect);
    canvas.drawPath(shape.getOuterPath(rect, textDirection: direction), paint);
  }

  @override
  bool shouldRepaint(_GlassRim old) =>
      shape != old.shape ||
      decoration != old.decoration ||
      direction != old.direction;
}

class _LiveGlassPaint extends StatefulWidget {
  const _LiveGlassPaint({required this.shape});
  final ShapeBorder shape;
  @override
  State<_LiveGlassPaint> createState() => _LiveGlassPaintState();
}

class _LiveGlassPaintState extends State<_LiveGlassPaint> {
  static Future<ui.FragmentProgram?>? _program;
  ui.FragmentShader? _shader;
  ui.ImageFilter? _filter;
  (double, double)? _geometry;
  static final _blur = ui.ImageFilter.blur(sigmaX: 4, sigmaY: 4);
  @override
  void initState() {
    super.initState();
    if (ui.ImageFilter.isShaderFilterSupported) {
      (_program ??= _loadGlassProgram('shaders/liquid_glass.frag')).then((
        program,
      ) {
        if (!mounted || program == null) return;
        setState(() => _shader = program.fragmentShader());
      });
    }
  }

  @override
  void dispose() {
    _shader?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final ratio = MediaQuery.devicePixelRatioOf(context);
      final radius = _radius(
        widget.shape,
        constraints.biggest,
        Directionality.of(context),
      );
      final shader = _shader;
      if (shader != null && _geometry != (radius, ratio)) {
        _geometry = (radius, ratio);
        shader
          ..setFloat(2, radius * ratio)
          ..setFloat(3, 8 * ratio)
          ..setFloat(4, .5 * ratio);
        _filter = ui.ImageFilter.compose(
          outer: ui.ImageFilter.shader(shader),
          inner: _blur,
        );
      }
      return BackdropFilter(
        filter: _filter ?? _blur,
        child: const SizedBox.expand(),
      );
    },
  );
}

Future<ui.FragmentProgram?> _loadGlassProgram(String asset) async {
  try {
    return await ui.FragmentProgram.fromAsset(asset);
  } catch (error, stack) {
    FlutterError.reportError(
      FlutterErrorDetails(
        exception: error,
        stack: stack,
        library: 'glass material',
      ),
    );
    return null;
  }
}

class _MaterialBackdrop extends InheritedWidget {
  const _MaterialBackdrop({
    required this.image,
    required this.program,
    required this.size,
    required super.child,
  });
  final ui.Image? image;
  final ui.FragmentProgram? program;
  final Size size;
  static _MaterialBackdrop? of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<_MaterialBackdrop>();
  @override
  bool updateShouldNotify(_MaterialBackdrop old) =>
      image != old.image || program != old.program || size != old.size;
}

class _CachedGlassPaint extends LeafRenderObjectWidget {
  const _CachedGlassPaint({required this.shape});
  final ShapeBorder shape;
  @override
  RenderObject createRenderObject(BuildContext context) => _RenderCachedGlass(
    _MaterialBackdrop.of(context),
    shape,
    Directionality.of(context),
  );
  @override
  void updateRenderObject(BuildContext context, _RenderCachedGlass object) =>
      object.update(
        _MaterialBackdrop.of(context),
        shape,
        Directionality.of(context),
      );
}

class _RenderCachedGlass extends RenderBox {
  _RenderCachedGlass(this._backdrop, this._shape, this._direction) {
    _updateShader();
  }
  _MaterialBackdrop? _backdrop;
  ShapeBorder _shape;
  TextDirection _direction;
  ui.FragmentShader? _shader;
  ui.FragmentProgram? _program;
  void _updateShader() {
    if (_program == _backdrop?.program) return;
    _shader?.dispose();
    _program = _backdrop?.program;
    _shader = _program?.fragmentShader();
  }

  void update(
    _MaterialBackdrop? backdrop,
    ShapeBorder shape,
    TextDirection direction,
  ) {
    _backdrop = backdrop;
    _shape = shape;
    _direction = direction;
    _updateShader();
    markNeedsPaint();
  }

  @override
  bool get sizedByParent => true;
  @override
  void performResize() {
    size = constraints.biggest;
  }

  @override
  void paint(PaintingContext context, Offset offset) {
    final image = _backdrop?.image;
    final shader = _shader;
    if (image == null || shader == null || size.isEmpty) return;
    final scene = _backdrop!.size;
    final origin = localToGlobal(Offset.zero);
    shader
      ..setFloat(0, size.width)
      ..setFloat(1, size.height)
      ..setFloat(2, scene.width)
      ..setFloat(3, scene.height)
      ..setFloat(4, origin.dx)
      ..setFloat(5, origin.dy)
      ..setFloat(6, _radius(_shape, size, _direction))
      ..setFloat(7, 8)
      ..setFloat(8, .5)
      ..setImageSampler(0, image);
    context.canvas.save();
    context.canvas.translate(offset.dx, offset.dy);
    context.canvas.drawRect(Offset.zero & size, Paint()..shader = shader);
    context.canvas.restore();
  }

  @override
  void dispose() {
    _shader?.dispose();
    super.dispose();
  }
}

class MaterialBackground extends StatefulWidget {
  const MaterialBackground({super.key, required this.child});
  final Widget child;
  @override
  State<MaterialBackground> createState() => _MaterialBackgroundState();
}

class _MaterialBackgroundState extends State<MaterialBackground> {
  static Future<ui.FragmentProgram?>? _programFuture;
  ui.FragmentProgram? _program;
  ui.Image? _image;
  Timer? _pending;
  (List<Color>, Size)? _background;
  int _epoch = 0;
  bool _requestedProgram = false;
  void _ensureProgram() {
    if (_requestedProgram) return;
    _requestedProgram = true;
    (_programFuture ??= _loadGlassProgram('shaders/glass_material.frag')).then((
      program,
    ) {
      if (mounted) setState(() => _program = program);
    });
  }

  void _cache(LinearGradient gradient, Size size) {
    final previous = _background;
    if (previous != null &&
        previous.$2 == size &&
        listEquals(previous.$1, gradient.colors)) {
      return;
    }
    _background = (gradient.colors, size);
    _pending?.cancel();
    final epoch = ++_epoch;
    _pending = Timer(const Duration(milliseconds: 100), () async {
      final pixels = Size(
        96,
        (96 * size.height / size.width).clamp(32, 256).toDouble(),
      );
      final recorder = ui.PictureRecorder();
      final canvas = Canvas(recorder);
      canvas.drawRect(
        Offset.zero & pixels,
        Paint()..shader = gradient.createShader(Offset.zero & pixels),
      );
      final picture = recorder.endRecording();
      ui.Image image;
      try {
        image = await picture.toImage(
          pixels.width.ceil(),
          pixels.height.ceil(),
        );
      } catch (error, stack) {
        FlutterError.reportError(
          FlutterErrorDetails(
            exception: error,
            stack: stack,
            library: 'glass background cache',
          ),
        );
        return;
      } finally {
        picture.dispose();
      }
      if (!mounted || epoch != _epoch) {
        image.dispose();
        return;
      }
      final old = _image;
      setState(() => _image = image);
      WidgetsBinding.instance.addPostFrameCallback((_) => old?.dispose());
    });
  }

  @override
  void dispose() {
    _pending?.cancel();
    _image?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (!MaterialSurface.glassEnabled(context)) return widget.child;
    _ensureProgram();
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    final pureBlack = colors.surface == Colors.black;
    final size = MediaQuery.sizeOf(context);
    if (size.isEmpty) return widget.child;
    final gradient = LinearGradient(
      begin: Alignment.topLeft,
      end: Alignment.bottomRight,
      colors: [
        colors.surface,
        pureBlack
            ? Colors.black
            : Color.lerp(colors.surface, colors.primaryContainer, .35)!,
        pureBlack
            ? Colors.black
            : Color.lerp(colors.surface, colors.tertiaryContainer, .2)!,
        colors.surface,
      ],
      stops: const [0, .3, .7, 1],
    );
    _cache(gradient, size);
    return _MaterialBackdrop(
      image: _image,
      program: _program,
      size: size,
      child: Theme(
        data: glassTheme(theme),
        child: DecoratedBox(
          decoration: BoxDecoration(gradient: gradient),
          child: widget.child,
        ),
      ),
    );
  }
}

ThemeData glassTheme(ThemeData theme) {
  final colors = theme.colorScheme;
  final shape = RoundedRectangleBorder(
    borderRadius: BorderRadius.circular(16),
    side: BorderSide(color: colors.outlineVariant.withValues(alpha: .55)),
  );
  final fill = colors.surfaceContainerHigh.withValues(alpha: .7);
  return theme.copyWith(
    scaffoldBackgroundColor: Colors.transparent,
    appBarTheme: theme.appBarTheme.copyWith(
      backgroundColor: colors.surface.withValues(alpha: .82),
    ),
    cardTheme: theme.cardTheme.copyWith(
      color: fill,
      shape: shape,
      elevation: 0,
    ),
    inputDecorationTheme: theme.inputDecorationTheme.copyWith(
      filled: true,
      fillColor: fill,
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(16),
        borderSide: BorderSide(color: colors.outlineVariant),
      ),
    ),
    popupMenuTheme: theme.popupMenuTheme.copyWith(
      color: fill,
      surfaceTintColor: Colors.transparent,
      shape: shape,
    ),
    menuTheme: MenuThemeData(
      style: MenuStyle(
        backgroundColor: WidgetStatePropertyAll(fill),
        surfaceTintColor: const WidgetStatePropertyAll(Colors.transparent),
        shape: WidgetStatePropertyAll(shape),
      ),
    ),
    snackBarTheme: theme.snackBarTheme.copyWith(
      backgroundColor: colors.inverseSurface.withValues(alpha: .88),
      behavior: SnackBarBehavior.floating,
      shape: shape,
    ),
    bottomSheetTheme: theme.bottomSheetTheme.copyWith(
      backgroundColor: fill,
      surfaceTintColor: Colors.transparent,
    ),
    filledButtonTheme: FilledButtonThemeData(
      style:
          theme.filledButtonTheme.style?.copyWith(
            shape: WidgetStatePropertyAll(shape),
            backgroundColor: WidgetStateProperty.resolveWith(
              (states) => states.contains(WidgetState.disabled)
                  ? colors.onSurface.withValues(alpha: .12)
                  : colors.primary.withValues(alpha: .92),
            ),
          ) ??
          FilledButton.styleFrom(
            shape: shape,
            backgroundColor: colors.primary.withValues(alpha: .92),
          ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style:
          theme.outlinedButtonTheme.style?.copyWith(
            shape: WidgetStatePropertyAll(shape),
          ) ??
          OutlinedButton.styleFrom(shape: shape),
    ),
    segmentedButtonTheme: SegmentedButtonThemeData(
      style: (theme.segmentedButtonTheme.style ?? const ButtonStyle()).copyWith(
        backgroundColor: WidgetStateProperty.resolveWith(
          (states) => states.contains(WidgetState.selected)
              ? colors.secondaryContainer.withValues(alpha: .85)
              : fill,
        ),
      ),
    ),
  );
}
