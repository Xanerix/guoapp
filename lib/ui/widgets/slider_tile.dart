import 'package:flutter/material.dart';

import 'contained_slider.dart';

class SliderTile extends StatefulWidget {
  const SliderTile({
    super.key,
    required this.title,
    required this.value,
    required this.min,
    required this.max,
    required this.onChanged,
    this.subtitle,
    this.divisions,
    this.label,
    this.padding = const EdgeInsets.fromLTRB(0, 10, 0, 6),
  });

  final String title;
  final String? subtitle;
  final double value;
  final double min;
  final double max;
  final int? divisions;
  final String Function(double value)? label;
  final ValueChanged<double> onChanged;
  final EdgeInsetsGeometry padding;

  @override
  State<SliderTile> createState() => _SliderTileState();
}

class _SliderTileState extends State<SliderTile> {
  double? _draft;
  bool _dragging = false;

  @override
  void didUpdateWidget(SliderTile oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!_dragging) _draft = null;
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colors = theme.colorScheme;
    final value = _draft ?? widget.value;
    final label = Text(
      widget.label?.call(value) ?? '${(value * 100).round()}%',
      style: theme.textTheme.bodyMedium?.copyWith(
        color: colors.onSurfaceVariant,
        fontFeatures: const [FontFeature.tabularFigures()],
      ),
    );
    return Padding(
      padding: widget.padding,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            spacing: 12,
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  spacing: 2,
                  children: [
                    Text(widget.title, style: theme.textTheme.bodyLarge),
                    if (widget.subtitle case final subtitle?)
                      Text(
                        subtitle,
                        style: theme.textTheme.bodySmall?.copyWith(
                          color: colors.onSurfaceVariant,
                        ),
                      ),
                  ],
                ),
              ),
              label,
            ],
          ),
          SizedBox(
            height: 40,
            child: SliderTheme(
              data: SliderTheme.of(context).copyWith(
                trackHeight: 4,
                thumbShape: const RoundSliderThumbShape(enabledThumbRadius: 8),
                overlayShape: const RoundSliderOverlayShape(overlayRadius: 16),
              ),
              child: MediaQuery(
                data: MediaQuery.of(
                  context,
                ).copyWith(navigationMode: NavigationMode.directional),
                child: ContainedSlider(
                  slider: Slider(
                    value: value.clamp(widget.min, widget.max),
                    min: widget.min,
                    max: widget.max,
                    divisions: widget.divisions,
                    label: widget.label?.call(value),
                    semanticFormatterCallback: widget.label,
                    onChangeStart: (_) => _dragging = true,
                    onChanged: (next) => setState(() => _draft = next),
                    onChangeEnd: (next) {
                      _dragging = false;
                      if (next == widget.value) {
                        setState(() => _draft = null);
                      } else {
                        widget.onChanged(next);
                      }
                    },
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
