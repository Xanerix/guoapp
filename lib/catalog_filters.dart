import 'dart:math';

import 'package:flutter/material.dart';

import 'app_layout.dart';
import 'models.dart';

class CatalogFilters extends StatefulWidget {
  const CatalogFilters({
    super.key,
    required this.categories,
    required this.category,
    required this.onCategory,
    required this.onRetry,
    this.error,
    this.trailing,
  });

  final List<CatalogCategory> categories;
  final String category;
  final String? error;
  final ValueChanged<String> onCategory;
  final VoidCallback onRetry;
  final Widget? trailing;

  @override
  State<CatalogFilters> createState() => _CatalogFiltersState();
}

class _CatalogFiltersState extends State<CatalogFilters> {
  final _anchors = <String, GlobalKey>{};

  @override
  void didUpdateWidget(covariant CatalogFilters oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.category != widget.category) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final anchor = _anchors[widget.category]?.currentContext;
        if (mounted && anchor != null) {
          Scrollable.ensureVisible(
            anchor,
            alignment: .4,
            duration: const Duration(milliseconds: 180),
          );
        }
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final television = AppLayout.isTelevision(context);
    return SizedBox(
      height: television
          ? 64
          : max(52, MediaQuery.textScalerOf(context).scale(14) + 28),
      child: Row(
        children: [
          Expanded(
            child: SingleChildScrollView(
              key: const ValueKey('catalog-categories'),
              scrollDirection: Axis.horizontal,
              padding: const EdgeInsets.symmetric(horizontal: 12),
              child: Row(
                children: [
                  for (final entry in widget.categories)
                    Padding(
                      key: _anchors.putIfAbsent(entry.id, GlobalKey.new),
                      padding: const EdgeInsets.only(right: 6),
                      child: ChoiceChip(
                        key: ValueKey('category-${entry.id}'),
                        label: Text(entry.name),
                        selected: entry.id == widget.category,
                        showCheckmark: false,
                        side: television
                            ? WidgetStateBorderSide.resolveWith(
                                (states) => states.contains(WidgetState.focused)
                                    ? BorderSide(
                                        color: Theme.of(
                                          context,
                                        ).colorScheme.primary,
                                        width: 3,
                                      )
                                    : null,
                              )
                            : null,
                        onSelected: (_) => widget.onCategory(entry.id),
                      ),
                    ),
                ],
              ),
            ),
          ),
          if (widget.error != null)
            IconButton(
              tooltip: widget.error,
              onPressed: widget.onRetry,
              icon: Icon(
                Icons.refresh_rounded,
                color: Theme.of(context).colorScheme.error,
              ),
            ),
          if (widget.trailing != null) widget.trailing!,
        ],
      ),
    );
  }
}
