import 'dart:async';
import 'dart:io';
import 'dart:math' as math;
import 'dart:ui' as ui;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import '../../models.dart';
import '../app_scope.dart';
import '../widgets/cover_image.dart';

const _boxWidth = 640.0;
const _boxHeight = 460.0;
const _width = 320;
const _height = 230;
const _sigma = 36.0;
const _opacity = .45;
const _sampleWidth = 32;

class PosterGlow extends StatefulWidget {
  const PosterGlow({super.key, required this.drama, required this.background});
  final Drama drama;
  final Color background;

  @override
  State<PosterGlow> createState() => _PosterGlowState();
}

class _PosterGlowState extends State<PosterGlow> {
  ui.Image? _image;
  String _source = '';
  int _generation = 0;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    unawaited(_load());
  }

  @override
  void didUpdateWidget(PosterGlow oldWidget) {
    super.didUpdateWidget(oldWidget);
    unawaited(_load());
  }

  @override
  void dispose() {
    _generation++;
    _image?.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final drama = widget.drama;
    final source = '${drama.id}\u0000${drama.cover}';
    if (source == _source) return;
    _source = source;
    final generation = ++_generation;
    if (DramaCover.imagesDisabled || drama.id.isEmpty) return;
    final repository = AppScope.of(context).repository;
    final background = widget.background.toARGB32();
    try {
      final bytes = await File(await repository.cover(drama)).readAsBytes();
      final codec = await ui.instantiateImageCodec(
        bytes,
        targetWidth: _sampleWidth,
      );
      final frame = await codec.getNextFrame();
      codec.dispose();
      final sample = frame.image;
      final width = sample.width;
      final height = sample.height;
      final data = await sample.toByteData(
        format: ui.ImageByteFormat.rawStraightRgba,
      );
      sample.dispose();
      if (data == null || generation != _generation) return;
      final pixels = await compute(_render, (
        data.buffer.asUint8List(),
        width,
        height,
        background,
      ));
      if (generation != _generation) return;
      final completer = Completer<ui.Image>();
      ui.decodeImageFromPixels(
        pixels,
        _width,
        _height,
        ui.PixelFormat.rgba8888,
        completer.complete,
      );
      final image = await completer.future;
      if (!mounted || generation != _generation) {
        image.dispose();
        return;
      }
      final old = _image;
      setState(() => _image = image);
      old?.dispose();
    } catch (_) {}
  }

  @override
  Widget build(BuildContext context) => switch (_image) {
    final image? => RawImage(
      image: image,
      fit: BoxFit.fill,
      filterQuality: FilterQuality.medium,
    ),
    null => const SizedBox.expand(),
  };
}

Uint8List _render((Uint8List, int, int, int) job) {
  final (source, sourceWidth, sourceHeight, background) = job;
  final scale = math.max(_width / sourceWidth, _height / sourceHeight);
  final left = (sourceWidth - _width / scale) / 2;
  final top = (sourceHeight - _height / scale) / 2;
  final sigma = _sigma * _width / _boxWidth / scale;
  final channels = [
    for (var channel = 0; channel < 3; channel++)
      _blur(
        Float64List.fromList([
          for (var i = 0; i < sourceWidth * sourceHeight; i++)
            source[i * 4 + channel].toDouble(),
        ]),
        sourceWidth,
        sourceHeight,
        sigma,
      ),
  ];
  final base = [
    (background >> 16) & 0xFF,
    (background >> 8) & 0xFF,
    background & 0xFF,
  ];
  final noise = math.Random(1);
  final out = Uint8List(_width * _height * 4);
  final color = Float64List(3);
  for (var y = 0; y < _height; y++) {
    final sy = top + (y + .5) / scale - .5;
    for (var x = 0; x < _width; x++) {
      final sx = left + (x + .5) / scale - .5;
      for (var channel = 0; channel < 3; channel++) {
        color[channel] = _sample(
          channels[channel],
          sourceWidth,
          sourceHeight,
          sx,
          sy,
        );
      }
      final (r, g, b) = (color[0], color[1], color[2]);
      final saturated = [
        1.1574 * r - .143 * g - .0144 * b,
        -.0426 * r + 1.057 * g - .0144 * b,
        -.0426 * r - .143 * g + 1.1856 * b,
      ];
      final alpha = _opacity * _fade(x, y);
      final offset = (y * _width + x) * 4;
      for (var channel = 0; channel < 3; channel++) {
        final value = saturated[channel].clamp(0.0, 255.0);
        final mixed =
            base[channel] +
            (value - base[channel]) * alpha +
            noise.nextDouble() +
            noise.nextDouble() -
            1;
        out[offset + channel] = mixed.round().clamp(0, 255);
      }
      out[offset + 3] = 255;
    }
  }
  return out;
}

double _fade(int x, int y) {
  final dx = _boxWidth - (x + .5) * _boxWidth / _width;
  final dy = (y + .5) * _boxHeight / _height;
  final distance =
      math.sqrt(dx * dx + dy * dy) / (1.4 * math.min(_boxWidth, _boxHeight));
  if (distance <= .45) return 1;
  if (distance >= 1) return 0;
  final t = (distance - .45) / .55;
  return 1 - t * t * (3 - 2 * t);
}

Float64List _blur(Float64List values, int width, int height, double sigma) {
  final radius = (sigma * 3).ceil();
  final kernel = Float64List(radius * 2 + 1);
  var sum = 0.0;
  for (var i = -radius; i <= radius; i++) {
    sum += kernel[i + radius] = math.exp(-i * i / (2 * sigma * sigma));
  }
  for (var i = 0; i < kernel.length; i++) {
    kernel[i] /= sum;
  }
  final rows = Float64List(values.length);
  for (var y = 0; y < height; y++) {
    for (var x = 0; x < width; x++) {
      var total = 0.0;
      for (var k = -radius; k <= radius; k++) {
        total +=
            values[y * width + (x + k).clamp(0, width - 1)] *
            kernel[k + radius];
      }
      rows[y * width + x] = total;
    }
  }
  final out = Float64List(values.length);
  for (var y = 0; y < height; y++) {
    for (var x = 0; x < width; x++) {
      var total = 0.0;
      for (var k = -radius; k <= radius; k++) {
        total +=
            rows[(y + k).clamp(0, height - 1) * width + x] * kernel[k + radius];
      }
      out[y * width + x] = total;
    }
  }
  return out;
}

double _sample(Float64List values, int width, int height, double x, double y) {
  final x0 = x.floor();
  final y0 = y.floor();
  final wx = _weights(x - x0);
  final wy = _weights(y - y0);
  var total = 0.0;
  for (var j = 0; j < 4; j++) {
    final row = (y0 - 1 + j).clamp(0, height - 1) * width;
    for (var i = 0; i < 4; i++) {
      total += values[row + (x0 - 1 + i).clamp(0, width - 1)] * wx[i] * wy[j];
    }
  }
  return total;
}

List<double> _weights(double t) {
  final t2 = t * t;
  final t3 = t2 * t;
  return [
    (1 - t) * (1 - t) * (1 - t) / 6,
    (3 * t3 - 6 * t2 + 4) / 6,
    (-3 * t3 + 3 * t2 + 3 * t + 1) / 6,
    t3 / 6,
  ];
}
