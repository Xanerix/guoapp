import 'package:flutter_test/flutter_test.dart';
import 'package:duanju_app/source_modules.dart';

void main() {
  test('module index offers only newer versions', () {
    expect(isNewerModuleVersion('1.0.1', '1.0.0'), isTrue);
    expect(isNewerModuleVersion('1.10.0', '1.9.9'), isTrue);
    expect(isNewerModuleVersion('2.0.0', '1.99.99'), isTrue);
    expect(isNewerModuleVersion('1.0.0', '1.0.0'), isFalse);
    expect(isNewerModuleVersion('1.0.0', '1.0.1'), isFalse);
    expect(isNewerModuleVersion('1.0.0', 'dev'), isTrue);
    expect(isNewerModuleVersion('1.0.0', ''), isTrue);
    expect(isNewerModuleVersion('latest', '1.0.0'), isFalse);
  });
}
