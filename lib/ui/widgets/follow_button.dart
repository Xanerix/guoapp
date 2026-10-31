import 'package:flutter/material.dart';

import '../../local_store.dart';
import '../../models.dart';
import '../theme/motion.dart';
import 'focus_ring.dart';
import 'motion/motion_widgets.dart';

Future<void> _toggle(BuildContext context, LocalStore store, Drama drama) =>
    store.toggleFavorite(drama).catchError((Object failure) {
      if (context.mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('$failure')));
      }
    });

class FollowPill extends StatelessWidget {
  const FollowPill({
    super.key,
    required this.store,
    required this.drama,
    this.height = 40,
    this.radius,
    this.onToggled,
    this.focusNode,
  });
  final LocalStore store;
  final Drama drama;
  final double height;
  final double? radius;
  final VoidCallback? onToggled;
  final FocusNode? focusNode;

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: store,
    builder: (context, _) {
      final followed = store.isFavorite(drama.id);
      final corner = radius ?? height / 2;
      return FocusRing(
        radius: corner,
        child: FilledButton.tonalIcon(
          key: const ValueKey('follow-toggle'),
          focusNode: focusNode,
          onPressed: () {
            _toggle(context, store, drama);
            onToggled?.call();
          },
          style: FilledButton.styleFrom(
            minimumSize: Size(0, height),
            padding: EdgeInsets.fromLTRB(height / 4, 0, height * 0.35, 0),
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(corner),
            ),
            iconSize: height * 0.45,
            textStyle: TextStyle(
              fontSize: height >= 44 ? 15 : 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          icon: ToggleIconSwitcher(
            child: Icon(
              followed ? Icons.bookmark_rounded : Icons.bookmark_border_rounded,
              key: ValueKey(followed),
            ),
          ),
          label: Stack(
            alignment: Alignment.center,
            children: [
              const Visibility(
                visible: false,
                maintainSize: true,
                maintainAnimation: true,
                maintainState: true,
                child: Text('已追剧'),
              ),
              FadeSwitcher(
                duration: Motion.short,
                child: Text(followed ? '已追剧' : '追剧', key: ValueKey(followed)),
              ),
            ],
          ),
        ),
      );
    },
  );
}

class FollowIcon extends StatelessWidget {
  const FollowIcon({
    super.key,
    required this.store,
    required this.drama,
    required this.color,
    required this.activeColor,
    this.size = 24,
    this.extent = 48,
  });
  final LocalStore store;
  final Drama drama;
  final Color color;
  final Color activeColor;
  final double size;
  final double extent;

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: store,
    builder: (context, _) {
      final followed = store.isFavorite(drama.id);
      return Semantics(
        label: followed ? '已追剧，轻点取消' : '追剧',
        button: true,
        excludeSemantics: true,
        child: IconButton(
          key: const ValueKey('follow-toggle'),
          onPressed: () => _toggle(context, store, drama),
          style: IconButton.styleFrom(fixedSize: Size.square(extent)),
          icon: ToggleIconSwitcher(
            child: Icon(
              followed ? Icons.bookmark_rounded : Icons.bookmark_add_outlined,
              key: ValueKey(followed),
              size: size,
              color: followed ? activeColor : color,
            ),
          ),
        ),
      );
    },
  );
}
