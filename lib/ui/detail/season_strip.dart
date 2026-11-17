import 'dart:async';

import 'package:flutter/material.dart';

import '../../local_store.dart';
import '../../models.dart';
import '../theme/motion.dart';
import '../widgets/choice_panel.dart';
import '../widgets/focus_ring.dart';

class SeasonStrip extends StatefulWidget {
  const SeasonStrip({
    super.key,
    required this.seasons,
    required this.current,
    required this.store,
    required this.metaSize,
    required this.tagSize,
    required this.sectionSize,
    required this.currentStatus,
    required this.collapsed,
    required this.onSelected,
  });
  final bool collapsed;
  final List<DramaSeason> seasons;
  final String current;
  final LocalStore store;
  final double metaSize, tagSize, sectionSize;
  final String currentStatus;
  final ValueChanged<DramaSeason> onSelected;

  @override
  State<SeasonStrip> createState() => _SeasonStripState();
}

class _SeasonStripState extends State<SeasonStrip> {
  static const _cardWidth = 116.0;
  static const _gap = 8.0;
  final _scroll = ScrollController();

  @override
  void initState() {
    super.initState();
    _reveal(animate: false);
  }

  @override
  void didUpdateWidget(SeasonStrip oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.current != widget.current) _reveal(animate: true);
  }

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  void _reveal({required bool animate}) {
    final index = widget.seasons.indexWhere(
      (season) => season.drama.id == widget.current,
    );
    if (index <= 0) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !_scroll.hasClients) return;
      final offset = (index * (_cardWidth + _gap) - _cardWidth / 2).clamp(
        0.0,
        _scroll.position.maxScrollExtent,
      );
      if (animate) {
        _scroll.animateTo(
          offset,
          duration: Motion.medium,
          curve: Curves.easeOut,
        );
      } else {
        _scroll.jumpTo(offset);
      }
    });
  }

  String _status(DramaSeason season, bool selected) {
    if (selected) return widget.currentStatus;
    final watched = widget.store.watched(season.drama.id);
    if (watched != null && !watched.finished) return '看至第${watched.episode}集';
    if (season.upcoming) return '待上线';
    return season.drama.episodes > 0 ? '共 ${season.drama.episodes} 集' : '';
  }

  void _openMenu() {
    unawaited(
      showAnchoredPanel<void>(
        context: context,
        width: context.size!.width,
        builder: (_) => GridView.builder(
          shrinkWrap: true,
          padding: const EdgeInsets.all(12),
          itemCount: widget.seasons.length,
          gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
            maxCrossAxisExtent: 150,
            mainAxisExtent: 82,
            mainAxisSpacing: _gap,
            crossAxisSpacing: _gap,
          ),
          itemBuilder: (menu, index) =>
              _card(menu, widget.seasons[index], (season) {
                Navigator.pop(menu);
                widget.onSelected(season);
              }),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    final background = colors.surfaceContainerHighest.withValues(alpha: .55);
    final currentLabel = widget.seasons
        .where((season) => season.drama.id == widget.current)
        .firstOrNull
        ?.label;
    final content = Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      spacing: 10,
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12),
          child: Row(
            children: [
              Icon(Icons.layers_rounded, size: 16, color: colors.primary),
              const SizedBox(width: 6),
              Text(
                '系列多季',
                style: TextStyle(
                  fontSize: widget.sectionSize,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(width: 8),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                decoration: BoxDecoration(
                  color: colors.primary.withValues(alpha: .16),
                  borderRadius: BorderRadius.circular(999),
                ),
                child: Text(
                  '共 ${widget.seasons.length} 季',
                  style: TextStyle(
                    fontSize: widget.tagSize,
                    color: colors.primary,
                  ),
                ),
              ),
              const Spacer(),
              if (widget.collapsed) ...[
                if (currentLabel != null)
                  Text(
                    currentLabel,
                    style: TextStyle(
                      fontSize: widget.tagSize,
                      color: colors.onSurfaceVariant,
                    ),
                  ),
                Icon(
                  Icons.expand_more_rounded,
                  size: 20,
                  color: colors.onSurfaceVariant,
                ),
              ] else
                Text(
                  '左右滑动切换',
                  style: TextStyle(
                    fontSize: widget.tagSize,
                    color: colors.onSurfaceVariant,
                  ),
                ),
            ],
          ),
        ),
        if (!widget.collapsed)
          SizedBox(
            height: 90,
            child: ListView.separated(
              controller: _scroll,
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
              itemCount: widget.seasons.length,
              separatorBuilder: (_, _) => const SizedBox(width: _gap),
              itemBuilder: (context, index) =>
                  _card(context, widget.seasons[index], widget.onSelected),
            ),
          ),
      ],
    );
    if (widget.collapsed) {
      return Material(
        color: background,
        borderRadius: BorderRadius.circular(16),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: _openMenu,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(4, 10, 4, 10),
            child: content,
          ),
        ),
      );
    }
    return Container(
      padding: const EdgeInsets.fromLTRB(4, 10, 4, 12),
      decoration: BoxDecoration(
        color: background,
        borderRadius: BorderRadius.circular(16),
      ),
      child: content,
    );
  }

  Widget _card(
    BuildContext context,
    DramaSeason season,
    ValueChanged<DramaSeason> onSelected,
  ) {
    final colors = Theme.of(context).colorScheme;
    final selected = season.drama.id == widget.current;
    final foreground = selected ? colors.onPrimaryContainer : colors.onSurface;
    final muted = selected
        ? colors.onPrimaryContainer.withValues(alpha: .8)
        : colors.onSurfaceVariant;
    final status = _status(season, selected);
    final online = season.onlineLabel;
    return FocusRing(
      radius: 12,
      child: Material(
        color: selected ? colors.primaryContainer : colors.surfaceContainer,
        borderRadius: BorderRadius.circular(12),
        child: InkWell(
          borderRadius: BorderRadius.circular(12),
          onTap: selected ? null : () => onSelected(season),
          child: Container(
            width: _cardWidth,
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisAlignment: MainAxisAlignment.center,
              spacing: 3,
              children: [
                Row(
                  spacing: 4,
                  children: [
                    Flexible(
                      child: Text(
                        season.label,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(
                          fontSize: widget.metaSize,
                          fontWeight: FontWeight.w700,
                          color: foreground,
                        ),
                      ),
                    ),
                    if (selected)
                      Container(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 5,
                          vertical: 1,
                        ),
                        decoration: BoxDecoration(
                          color: colors.onPrimaryContainer.withValues(
                            alpha: .14,
                          ),
                          borderRadius: BorderRadius.circular(999),
                        ),
                        child: Text(
                          '当前',
                          style: TextStyle(
                            fontSize: widget.tagSize * .85,
                            color: colors.onPrimaryContainer,
                          ),
                        ),
                      ),
                  ],
                ),
                if (status.isNotEmpty)
                  Text(
                    status,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(fontSize: widget.tagSize, color: muted),
                  ),
                if (online != null)
                  Text(
                    online,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                      fontSize: widget.tagSize * .85,
                      color: muted.withValues(alpha: .7),
                    ),
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
