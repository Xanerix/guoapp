import 'dart:math' as math;
import 'dart:ui' as ui;

import 'package:flutter/material.dart';

class LiquidGlassNavigation extends StatefulWidget {
  const LiquidGlassNavigation({super.key, required this.child});

  static const height = 64.0;
  final Widget child;

  @override
  State<LiquidGlassNavigation> createState() => _LiquidGlassNavigationState();
}

class _LiquidGlassNavigationState extends State<LiquidGlassNavigation> {
  static const _horizontalGap = 16.0;
  static const _bottomGap = 8.0;
  static const _glassShape = StadiumBorder();
  static final _blur = ui.ImageFilter.blur(sigmaX: 9, sigmaY: 9);
  ui.FragmentShader? _shader;

  @override
  void initState() {
    super.initState();
    if (ui.ImageFilter.isShaderFilterSupported) _loadShader();
  }

  Future<void> _loadShader() async {
    try {
      final program = await ui.FragmentProgram.fromAsset(
        'assets/shaders/navigation_glass.frag',
      );
      if (mounted) setState(() => _shader = program.fragmentShader());
    } catch (error) {
      debugPrint('Liquid glass shader could not be loaded: $error');
    }
  }

  @override
  void dispose() {
    _shader?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final screen = MediaQuery.sizeOf(context);
    final insets = MediaQuery.paddingOf(context);
    final scale = MediaQuery.devicePixelRatioOf(context);
    final width = (screen.width - insets.horizontal - 2 * _horizontalGap).clamp(
      0.0,
      440.0,
    );
    final bottom = insets.bottom + _bottomGap;
    const height = LiquidGlassNavigation.height;
    final center = Offset(
      (screen.width + insets.left - insets.right) / 2,
      screen.height - bottom - height / 2,
    );
    final contrast = MediaQuery.highContrastOf(context);
    final colors = Theme.of(context).colorScheme;
    final dark = colors.brightness == Brightness.dark;
    final tint = colors.surface.withValues(
      alpha: contrast ? 1 : (dark ? 0.76 : 0.67),
    );
    final surface = Container(
      width: width,
      height: height,
      decoration: ShapeDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [
            Color.alphaBlend(
              Colors.white.withValues(alpha: dark ? 0.07 : 0.22),
              tint,
            ),
            tint,
          ],
        ),
        shape: _glassShape,
      ),
      foregroundDecoration: ShapeDecoration(
        shape: _glassShape.copyWith(
          side: BorderSide(
            color: contrast
                ? colors.outline
                : (dark ? Colors.white : colors.outline).withValues(
                    alpha: dark ? 0.24 : 0.30,
                  ),
          ),
        ),
      ),
      child: MediaQuery.removePadding(
        context: context,
        removeBottom: true,
        child: widget.child,
      ),
    );
    final shader = _shader;
    if (shader != null && !contrast) {
      shader
        ..setFloat(2, center.dx * scale)
        ..setFloat(3, center.dy * scale)
        ..setFloat(4, math.max(0, (width - height) / 2) * scale)
        ..setFloat(5, height / 2 * scale)
        ..setFloat(6, scale);
    }
    return Padding(
      padding: EdgeInsets.fromLTRB(insets.left, 0, insets.right, bottom),
      child: Center(
        heightFactor: 1,
        child: DecoratedBox(
          decoration: ShapeDecoration(
            shape: _glassShape,
            shadows: [
              BoxShadow(
                color: Colors.black.withValues(alpha: dark ? 0.32 : 0.14),
                blurRadius: 18,
                offset: const Offset(0, 5),
              ),
            ],
          ),
          child: ClipPath(
            clipper: const ShapeBorderClipper(shape: _glassShape),
            child: contrast
                ? surface
                : BackdropFilter(
                    filter: shader == null
                        ? _blur
                        : ui.ImageFilter.shader(shader),
                    child: surface,
                  ),
          ),
        ),
      ),
    );
  }
}
