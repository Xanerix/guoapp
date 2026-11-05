import 'package:flutter/material.dart';

class ContainedSlider extends StatelessWidget {
  const ContainedSlider({super.key, required this.slider});

  final Slider slider;

  double _step(TargetPlatform platform) {
    final divisions = slider.divisions;
    if (divisions != null) return 1 / divisions;
    return switch (platform) {
      TargetPlatform.iOS || TargetPlatform.macOS => .1,
      _ => .05,
    };
  }

  double _fraction(double value) => slider.max > slider.min
      ? ((value - slider.min) / (slider.max - slider.min)).clamp(0.0, 1.0)
      : 0;

  double _lerp(double fraction) =>
      slider.min + (slider.max - slider.min) * fraction.clamp(0.0, 1.0);

  String _describe(double fraction) {
    final format = slider.semanticFormatterCallback;
    return format == null
        ? '${(fraction.clamp(0.0, 1.0) * 100).round()}%'
        : format(_lerp(fraction));
  }

  void _adjust(double fraction) {
    final target = _lerp(fraction);
    slider.onChangeStart?.call(slider.value);
    slider.onChanged?.call(target);
    slider.onChangeEnd?.call(target);
  }

  @override
  Widget build(BuildContext context) {
    final enabled = slider.onChanged != null;
    final current = _fraction(slider.value);
    final step = _step(Theme.of(context).platform);
    return Semantics(
      container: true,
      slider: true,
      enabled: enabled,
      label: slider.label,
      value: _describe(current),
      increasedValue: _describe(current + step),
      decreasedValue: _describe(current - step),
      onIncrease: enabled ? () => _adjust(current + step) : null,
      onDecrease: enabled ? () => _adjust(current - step) : null,
      child: ExcludeSemantics(
        child: Overlay.wrap(
          clipBehavior: Clip.none,
          alwaysSizeToContent: true,
          child: slider,
        ),
      ),
    );
  }
}
