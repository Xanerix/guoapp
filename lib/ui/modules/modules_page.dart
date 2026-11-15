import 'package:flutter/material.dart';

import '../../local_store.dart';
import '../app_scope.dart';
import '../settings/modules_view.dart';
import '../widgets/page_header.dart';

class ModulesPage extends StatelessWidget {
  const ModulesPage({super.key, required this.store});
  final LocalStore store;

  @override
  Widget build(BuildContext context) => Scaffold(
    body: SafeArea(
      bottom: false,
      child: Column(
        children: [
          if (!AppScope.of(context).television) const PageHeader(title: '模块'),
          Expanded(child: ModulesView(store: store)),
        ],
      ),
    ),
  );
}
