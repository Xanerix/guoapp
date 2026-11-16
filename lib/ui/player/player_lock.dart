import 'dart:async';

import 'package:flutter/material.dart';

import '../widgets/motion/motion_widgets.dart';

class PlayerLockButton extends StatelessWidget {
  const PlayerLockButton({
    super.key,
    required this.locked,
    required this.onPressed,
    this.size = 44,
  });
  final bool locked;
  final VoidCallback onPressed;
  final double size;

  @override
  Widget build(BuildContext context) {
    final label = locked ? '解锁' : '锁定';
    return Semantics(
      label: label,
      button: true,
      excludeSemantics: true,
      child: IconButton(
        onPressed: onPressed,
        icon: ToggleIconSwitcher(
          child: Icon(
            locked ? Icons.lock_rounded : Icons.lock_open_rounded,
            key: ValueKey(locked),
            shadows: const [Shadow(color: Colors.black54, blurRadius: 8)],
          ),
        ),
        iconSize: 22,
        color: Colors.white,
        tooltip: label,
        style: IconButton.styleFrom(
          fixedSize: Size.square(size),
          padding: EdgeInsets.zero,
          shape: const CircleBorder(),
        ),
      ),
    );
  }
}

class PlayerLockOverlay extends StatefulWidget {
  const PlayerLockOverlay({
    super.key,
    required this.locked,
    required this.onUnlock,
    required this.onBlocked,
    required this.alignment,
    required this.padding,
  });
  final bool locked;
  final VoidCallback onUnlock;
  final VoidCallback onBlocked;
  final Alignment alignment;
  final EdgeInsets padding;

  @override
  State<PlayerLockOverlay> createState() => _PlayerLockOverlayState();
}

class _PlayerLockOverlayState extends State<PlayerLockOverlay> {
  static const _visibleFor = Duration(seconds: 3);
  Timer? _hideTimer;
  bool _shown = true;

  @override
  void initState() {
    super.initState();
    _scheduleHide();
  }

  @override
  void didUpdateWidget(PlayerLockOverlay oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.locked == oldWidget.locked) return;
    if (!widget.locked) {
      _hideTimer?.cancel();
      _hideTimer = null;
      return;
    }
    _shown = true;
    _scheduleHide();
  }

  @override
  void dispose() {
    _hideTimer?.cancel();
    super.dispose();
  }

  void _scheduleHide() {
    _hideTimer?.cancel();
    _hideTimer = Timer(_visibleFor, () {
      if (!mounted || !widget.locked) return;
      setState(() => _shown = false);
    });
  }

  void _tap() {
    if (!_shown) {
      setState(() => _shown = true);
    } else {
      widget.onBlocked();
    }
    _scheduleHide();
  }

  @override
  Widget build(BuildContext context) {
    if (!widget.locked) return const SizedBox.shrink();
    return Stack(
      fit: StackFit.expand,
      children: [
        GestureDetector(behavior: HitTestBehavior.opaque, onTap: _tap),
        Padding(
          padding: widget.padding,
          child: Align(
            alignment: widget.alignment,
            child: IgnorePointer(
              ignoring: !_shown,
              child: AnimatedOpacity(
                opacity: _shown ? 1 : 0,
                duration: const Duration(milliseconds: 220),
                curve: Curves.easeOut,
                child: PlayerLockButton(
                  locked: true,
                  onPressed: widget.onUnlock,
                ),
              ),
            ),
          ),
        ),
      ],
    );
  }
}
