import 'package:flutter/material.dart';

import 'material_surface.dart';

Widget _surfaceDialog(Dialog dialog, BuildContext context) {
  if (!MaterialSurface.glassEnabled(context)) return dialog.build(context);
  final theme = Theme.of(context);
  final shape =
      dialog.shape ??
      theme.dialogTheme.shape ??
      RoundedRectangleBorder(borderRadius: BorderRadius.circular(28));
  return Dialog(
    backgroundColor: Colors.transparent,
    elevation: 0,
    shadowColor: Colors.transparent,
    surfaceTintColor: Colors.transparent,
    insetAnimationDuration: dialog.insetAnimationDuration,
    insetAnimationCurve: dialog.insetAnimationCurve,
    insetPadding: dialog.insetPadding,
    clipBehavior: dialog.clipBehavior,
    shape: shape,
    alignment: dialog.alignment,
    semanticsRole: dialog.semanticsRole,
    constraints: dialog.constraints,
    child: MaterialSurface(
      mode: GlassSurfaceMode.realtime,
      color: dialog.backgroundColor ?? theme.colorScheme.surfaceContainerHigh,
      shape: shape,
      elevation: dialog.elevation ?? 6,
      child: dialog.child,
    ),
  );
}

class SurfaceDialog extends Dialog {
  const SurfaceDialog({
    super.key,
    super.backgroundColor,
    super.elevation,
    super.shadowColor,
    super.surfaceTintColor,
    super.insetAnimationDuration,
    super.insetAnimationCurve,
    super.insetPadding,
    super.clipBehavior,
    super.shape,
    super.alignment,
    super.child,
    super.semanticsRole,
    super.constraints,
  });
  @override
  Widget build(BuildContext context) => MaterialSurface.glassEnabled(context)
      ? _surfaceDialog(this, context)
      : super.build(context);
}

class SurfaceAlertDialog extends AlertDialog {
  const SurfaceAlertDialog({
    super.key,
    super.icon,
    super.iconPadding,
    super.iconColor,
    super.title,
    super.titlePadding,
    super.titleTextStyle,
    super.content,
    super.contentPadding,
    super.contentTextStyle,
    super.actions,
    super.actionsPadding,
    super.actionsAlignment,
    super.actionsOverflowAlignment,
    super.actionsOverflowDirection,
    super.actionsOverflowButtonSpacing,
    super.buttonPadding,
    super.backgroundColor,
    super.elevation,
    super.shadowColor,
    super.surfaceTintColor,
    super.semanticLabel,
    super.insetPadding,
    super.clipBehavior,
    super.shape,
    super.alignment,
    super.constraints,
    super.scrollable,
  });
  @override
  Widget build(BuildContext context) {
    final dialog = super.build(context);
    return dialog is Dialog && MaterialSurface.glassEnabled(context)
        ? _surfaceDialog(dialog, context)
        : dialog;
  }
}

class SurfaceSimpleDialog extends SimpleDialog {
  const SurfaceSimpleDialog({
    super.key,
    super.title,
    super.titlePadding,
    super.titleTextStyle,
    super.children,
    super.contentPadding,
    super.contentTextStyle,
    super.backgroundColor,
    super.elevation,
    super.shadowColor,
    super.surfaceTintColor,
    super.semanticLabel,
    super.insetPadding,
    super.clipBehavior,
    super.shape,
    super.alignment,
    super.constraints,
  });
  @override
  Widget build(BuildContext context) {
    final dialog = super.build(context);
    return dialog is Dialog && MaterialSurface.glassEnabled(context)
        ? _surfaceDialog(dialog, context)
        : dialog;
  }
}

class SurfaceAppBar extends AppBar {
  SurfaceAppBar({
    super.key,
    super.leading,
    super.automaticallyImplyLeading,
    super.title,
    super.actions,
    super.automaticallyImplyActions,
    super.flexibleSpace,
    super.bottom,
    super.elevation,
    super.scrolledUnderElevation,
    super.notificationPredicate,
    super.shadowColor,
    super.surfaceTintColor,
    super.shape,
    super.backgroundColor,
    super.foregroundColor,
    super.iconTheme,
    super.actionsIconTheme,
    super.primary,
    super.centerTitle,
    super.excludeHeaderSemantics,
    super.titleSpacing,
    super.toolbarOpacity,
    super.bottomOpacity,
    super.toolbarHeight,
    super.leadingWidth,
    super.toolbarTextStyle,
    super.titleTextStyle,
    super.systemOverlayStyle,
    super.forceMaterialTransparency,
    super.useDefaultSemanticsOrder,
    super.clipBehavior,
    super.actionsPadding,
    super.animateColor,
    this.glassMode = GlassSurfaceMode.realtime,
  });
  final GlassSurfaceMode glassMode;
  @override
  State<AppBar> createState() => _SurfaceAppBarState();
}

class _SurfaceAppBarState extends State<SurfaceAppBar> {
  @override
  Widget build(BuildContext context) {
    final glass = MaterialSurface.glassEnabled(context);
    final theme = Theme.of(context);
    final background = widget.backgroundColor == Colors.transparent
        ? theme.colorScheme.surfaceContainer
        : widget.backgroundColor ??
              theme.appBarTheme.backgroundColor ??
              theme.colorScheme.surface;
    return AppBar(
      leading: widget.leading,
      automaticallyImplyLeading: widget.automaticallyImplyLeading,
      title: widget.title,
      actions: widget.actions,
      automaticallyImplyActions: widget.automaticallyImplyActions,
      flexibleSpace: glass
          ? Stack(
              fit: StackFit.expand,
              children: [
                MaterialSurface(
                  mode: widget.glassMode,
                  color: background,
                  child: const SizedBox.expand(),
                ),
                if (widget.flexibleSpace != null) widget.flexibleSpace!,
              ],
            )
          : widget.flexibleSpace,
      bottom: widget.bottom,
      elevation: glass ? 0 : widget.elevation,
      scrolledUnderElevation: glass ? 0 : widget.scrolledUnderElevation,
      notificationPredicate: widget.notificationPredicate,
      shadowColor: glass ? Colors.transparent : widget.shadowColor,
      surfaceTintColor: glass ? Colors.transparent : widget.surfaceTintColor,
      shape: widget.shape,
      backgroundColor: glass ? Colors.transparent : widget.backgroundColor,
      foregroundColor: widget.foregroundColor,
      iconTheme: widget.iconTheme,
      actionsIconTheme: widget.actionsIconTheme,
      primary: widget.primary,
      centerTitle: widget.centerTitle,
      excludeHeaderSemantics: widget.excludeHeaderSemantics,
      titleSpacing: widget.titleSpacing,
      toolbarOpacity: widget.toolbarOpacity,
      bottomOpacity: widget.bottomOpacity,
      toolbarHeight: widget.toolbarHeight,
      leadingWidth: widget.leadingWidth,
      toolbarTextStyle: widget.toolbarTextStyle,
      titleTextStyle: widget.titleTextStyle,
      systemOverlayStyle: widget.systemOverlayStyle,
      forceMaterialTransparency: widget.forceMaterialTransparency,
      useDefaultSemanticsOrder: widget.useDefaultSemanticsOrder,
      clipBehavior: widget.clipBehavior,
      actionsPadding: widget.actionsPadding,
      animateColor: widget.animateColor,
    );
  }
}

Future<T?> showSurfaceModalBottomSheet<T>({
  required BuildContext context,
  required WidgetBuilder builder,
  Color? backgroundColor,
  String? barrierLabel,
  double? elevation,
  ShapeBorder? shape,
  Clip? clipBehavior,
  BoxConstraints? constraints,
  Color? barrierColor,
  bool isScrollControlled = false,
  double scrollControlDisabledMaxHeightRatio = 9 / 16,
  bool useRootNavigator = false,
  bool isDismissible = true,
  bool enableDrag = true,
  bool? showDragHandle,
  bool useSafeArea = false,
  RouteSettings? routeSettings,
  AnimationController? transitionAnimationController,
  Offset? anchorPoint,
  AnimationStyle? sheetAnimationStyle,
  bool? requestFocus,
  GlassSurfaceMode glassMode = GlassSurfaceMode.realtime,
}) {
  final glass = MaterialSurface.glassEnabled(context);
  final theme = Theme.of(context);
  final sheetShape =
      shape ??
      theme.bottomSheetTheme.shape ??
      const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(28)),
      );
  final handle =
      showDragHandle ?? theme.bottomSheetTheme.showDragHandle ?? false;
  return showModalBottomSheet<T>(
    context: context,
    backgroundColor: glass ? Colors.transparent : backgroundColor,
    barrierLabel: barrierLabel,
    elevation: glass ? 0 : elevation,
    shape: shape,
    clipBehavior: clipBehavior,
    constraints: constraints,
    barrierColor: barrierColor,
    isScrollControlled: isScrollControlled,
    scrollControlDisabledMaxHeightRatio: scrollControlDisabledMaxHeightRatio,
    useRootNavigator: useRootNavigator,
    isDismissible: isDismissible,
    enableDrag: enableDrag,
    showDragHandle: glass ? false : showDragHandle,
    useSafeArea: useSafeArea,
    routeSettings: routeSettings,
    transitionAnimationController: transitionAnimationController,
    anchorPoint: anchorPoint,
    sheetAnimationStyle: sheetAnimationStyle,
    requestFocus: requestFocus,
    builder: (sheetContext) => glass
        ? MaterialSurface(
            mode: glassMode,
            color: backgroundColor ?? theme.colorScheme.surfaceContainerLow,
            shape: sheetShape,
            child: handle
                ? Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Center(
                        child: Container(
                          width:
                              theme.bottomSheetTheme.dragHandleSize?.width ??
                              32,
                          height:
                              theme.bottomSheetTheme.dragHandleSize?.height ??
                              4,
                          margin: const EdgeInsets.symmetric(vertical: 12),
                          decoration: BoxDecoration(
                            color:
                                theme.bottomSheetTheme.dragHandleColor ??
                                theme.colorScheme.onSurfaceVariant,
                            borderRadius: BorderRadius.circular(2),
                          ),
                        ),
                      ),
                      Flexible(child: builder(sheetContext)),
                    ],
                  )
                : builder(sheetContext),
          )
        : builder(sheetContext),
  );
}
