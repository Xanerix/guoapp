import 'dart:async';

import 'support/source_fixture.dart';

Future<void> testExecutable(FutureOr<void> Function() testMain) async {
  installSourceFixture();
  await testMain();
}
