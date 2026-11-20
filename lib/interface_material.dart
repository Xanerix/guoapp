enum InterfaceMaterial {
  standard('默认'),
  liquidGlass('液态玻璃');

  const InterfaceMaterial(this.label);
  final String label;

  static InterfaceMaterial parse(String? value) => switch (value) {
    'liquidGlass' => liquidGlass,
    _ => standard,
  };
}
