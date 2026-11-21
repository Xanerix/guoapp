import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';

import '../core_bridge.dart';
import '../interface_material.dart';

enum FormFactor { phone, pad, window, television }

const televisionInset = 58.0;

bool get desktopPlatform => switch (defaultTargetPlatform) {
  TargetPlatform.windows ||
  TargetPlatform.macOS ||
  TargetPlatform.linux => true,
  _ => false,
};

bool get supportsInterfaceMaterial =>
    !kIsWeb && defaultTargetPlatform == TargetPlatform.android;

class AppScope extends InheritedWidget {
  const AppScope({
    super.key,
    required this.repository,
    this.formFactor,
    this.systemColorAvailable = false,
    this.interfaceMaterial = InterfaceMaterial.standard,
    this.version = '',
    required super.child,
  });
  final AppRepository repository;
  final FormFactor? formFactor;
  final bool systemColorAvailable;
  final InterfaceMaterial interfaceMaterial;
  final String version;

  bool get television => formFactor == FormFactor.television;

  bool get liquidGlass =>
      supportsInterfaceMaterial &&
      !television &&
      interfaceMaterial == InterfaceMaterial.liquidGlass;

  static AppScope? maybeOf(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<AppScope>();

  static AppScope of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<AppScope>()!;

  static FormFactor formFactorOf(BuildContext context) {
    if (of(context).formFactor case final form?) return form;
    final size = MediaQuery.sizeOf(context);
    if (desktopPlatform) {
      return size.width >= 600 ? FormFactor.window : FormFactor.phone;
    }
    return size.shortestSide >= 600 ? FormFactor.pad : FormFactor.phone;
  }

  @override
  bool updateShouldNotify(AppScope oldWidget) =>
      repository != oldWidget.repository ||
      formFactor != oldWidget.formFactor ||
      systemColorAvailable != oldWidget.systemColorAvailable ||
      interfaceMaterial != oldWidget.interfaceMaterial ||
      version != oldWidget.version;
}
