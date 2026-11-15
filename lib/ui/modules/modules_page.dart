import 'package:flutter/material.dart';

import '../../local_store.dart';
import '../settings/modules_view.dart';

class ModulesPage extends StatelessWidget {
  const ModulesPage({super.key, required this.store});
  final LocalStore store;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('模块')),
    body: ModulesView(store: store),
  );
}
