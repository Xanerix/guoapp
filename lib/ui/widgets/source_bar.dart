import 'package:flutter/material.dart';

import 'material_surface.dart';

import '../app_scope.dart';
import 'bar_button.dart';
import 'page_header.dart';
import 'search_field.dart';

class SourceBar extends StatelessWidget {
  const SourceBar({
    super.key,
    required this.source,
    required this.onSwitchSource,
    required this.onOpenSearch,
    required this.onSearchSubmitted,
    this.searchController,
    this.recentSearches = const [],
    this.onForgetSearch,
    this.televisionLeading,
    this.leading = const [],
    this.actions = const [],
  });
  final List<Widget> leading;
  final String source;
  final void Function(BuildContext anchor) onSwitchSource;
  final VoidCallback onOpenSearch;
  final ValueChanged<String> onSearchSubmitted;
  final TextEditingController? searchController;
  final List<String> recentSearches;
  final ValueChanged<String>? onForgetSearch;
  final Widget? televisionLeading;
  final List<Widget> actions;

  @override
  Widget build(BuildContext context) {
    final form = AppScope.formFactorOf(context);
    final hint = '在 $source 中搜索';
    final switcher = Builder(
      builder: (anchor) => BarButton(
        icon: Icons.public_rounded,
        tooltip: '切换站源',
        onPressed: () => onSwitchSource(anchor),
      ),
    );
    final search = BarButton(
      icon: Icons.search_rounded,
      tooltip: hint,
      onPressed: onOpenSearch,
    );
    return switch (form) {
      FormFactor.phone => PageHeader(
        title: source,
        actions: [...leading, switcher, search, ...actions],
      ),
      FormFactor.pad || FormFactor.window => PageHeader(
        title: source,
        actions: [
          ...leading,
          switcher,
          const SizedBox(width: 4),
          _RecentSearchField(
            width: MediaQuery.sizeOf(context).width >= 900 ? 320 : 240,
            hint: hint,
            controller: searchController,
            recent: recentSearches,
            onSubmitted: onSearchSubmitted,
            onForget: onForgetSearch,
          ),
          const SizedBox(width: 4),
          ...actions,
        ],
      ),
      FormFactor.television => SizedBox(
        height: 48,
        child: Row(
          spacing: 10,
          children: [
            ?televisionLeading,
            const Spacer(),
            Text(
              source,
              style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w700),
            ),
            switcher,
            search,
            ...actions,
          ],
        ),
      ),
    };
  }
}

class _RecentSearchField extends StatefulWidget {
  const _RecentSearchField({
    required this.width,
    required this.hint,
    required this.controller,
    required this.recent,
    required this.onSubmitted,
    required this.onForget,
  });
  final double width;
  final String hint;
  final TextEditingController? controller;
  final List<String> recent;
  final ValueChanged<String> onSubmitted;
  final ValueChanged<String>? onForget;

  @override
  State<_RecentSearchField> createState() => _RecentSearchFieldState();
}

class _RecentSearchFieldState extends State<_RecentSearchField> {
  final _ownController = TextEditingController();
  final _focus = FocusNode();
  final _overlay = OverlayPortalController()..show();
  final _link = LayerLink();
  var _active = false;

  TextEditingController get _controller => widget.controller ?? _ownController;

  @override
  void dispose() {
    _ownController.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _pick(String query) {
    _focus.unfocus();
    widget.onSubmitted(query);
  }

  Widget _options(BuildContext context) => ListenableBuilder(
    listenable: _controller,
    builder: (context, _) {
      if (!_active || _controller.text.isNotEmpty || widget.recent.isEmpty) {
        return const SizedBox.shrink();
      }
      return CompositedTransformFollower(
        link: _link,
        showWhenUnlinked: false,
        targetAnchor: Alignment.bottomLeft,
        offset: const Offset(0, 4),
        child: Align(
          alignment: Alignment.topLeft,
          child: TextFieldTapRegion(
            child: SizedBox(
              width: widget.width,
              child: MaterialSurface(
                elevation: 3,
                borderRadius: BorderRadius.circular(12),
                clipBehavior: Clip.antiAlias,
                child: ConstrainedBox(
                  constraints: const BoxConstraints(maxHeight: 360),
                  child: ListView(
                    shrinkWrap: true,
                    padding: const EdgeInsets.symmetric(vertical: 6),
                    children: [
                      for (final query in widget.recent)
                        GestureDetector(
                          onSecondaryTap: () => widget.onForget?.call(query),
                          child: ListTile(
                            dense: true,
                            leading: const Icon(
                              Icons.history_rounded,
                              size: 18,
                            ),
                            title: Text(
                              query,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                            ),
                            onTap: () => _pick(query),
                            onLongPress: () => widget.onForget?.call(query),
                          ),
                        ),
                    ],
                  ),
                ),
              ),
            ),
          ),
        ),
      );
    },
  );

  @override
  Widget build(BuildContext context) => Focus(
    canRequestFocus: false,
    skipTraversal: true,
    onFocusChange: (value) => setState(() => _active = value),
    child: CompositedTransformTarget(
      link: _link,
      child: OverlayPortal(
        controller: _overlay,
        overlayChildBuilder: _options,
        child: SizedBox(
          width: widget.width,
          child: SearchField(
            hint: widget.hint,
            controller: _controller,
            focusNode: _focus,
            onSubmitted: widget.onSubmitted,
          ),
        ),
      ),
    ),
  );
}
