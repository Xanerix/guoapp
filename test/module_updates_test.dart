import 'package:duanju_app/module_updates.dart';
import 'package:duanju_app/source_modules.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

const installed = SourceModule(id: 'example', name: '示例', version: '1.0.0');
ModuleIndexEntry entry(
  String version, {
  String id = 'example',
  bool bundled = false,
}) => ModuleIndexEntry(id: id, name: '示例', version: version, bundled: bundled);

void main() {
  test('only installed healthy modules with newer versions are offered', () {
    expect(availableModuleUpdates([installed], [entry('1.0.0')], {}), isEmpty);
    expect(availableModuleUpdates([installed], [entry('0.9.9')], {}), isEmpty);
    expect(
      availableModuleUpdates([installed], [entry('1.0.1', id: 'other')], {}),
      isEmpty,
    );
    expect(
      availableModuleUpdates(
        [
          const SourceModule(
            id: 'example',
            name: '示例',
            version: '1.0.0',
            error: '损坏',
          ),
        ],
        [entry('1.0.1')],
        {},
      ),
      isEmpty,
    );
    expect(
      availableModuleUpdates(
        [installed],
        [entry('1.0.1')],
        {},
      ).single.latest.version,
      '1.0.1',
    );
  });

  test('the highest version is chosen and the online copy wins a tie', () {
    final online = entry('1.1.0');
    final updates = availableModuleUpdates(
      [installed],
      [entry('1.1.0', bundled: true), entry('1.0.1'), online],
      {},
    );
    expect(updates.single.latest, same(online));
  });

  test('ignoring one version does not suppress a later version', () {
    expect(
      availableModuleUpdates([installed], [entry('1.0.1')], {
        'example': '1.0.1',
      }),
      isEmpty,
    );
    expect(
      availableModuleUpdates([installed], [entry('1.0.2')], {
        'example': '1.0.1',
      }).single.latest.version,
      '1.0.2',
    );
  });

  test('ignored versions persist independently of other preferences', () async {
    SharedPreferences.setMockInitialValues({'existing': 'preserved'});
    final preferences = await SharedPreferences.getInstance();
    await ModuleUpdatePreferences(
      preferences,
    ).ignore([ModuleUpdate(installed, entry('1.0.1'))]);
    expect(ModuleUpdatePreferences(preferences).ignored, {'example': '1.0.1'});
    expect(preferences.getString('existing'), 'preserved');
    await ModuleUpdatePreferences(preferences).ignore([
      ModuleUpdate(
        const SourceModule(id: 'second', name: '第二个'),
        entry('2.0.0', id: 'second'),
      ),
    ]);
    expect(ModuleUpdatePreferences(preferences).ignored, {
      'example': '1.0.1',
      'second': '2.0.0',
    });
  });

  test('missing or malformed reminder data is treated as empty', () async {
    for (final value in [null, 3, '{broken', '[]']) {
      SharedPreferences.setMockInitialValues({
        ModuleUpdatePreferences.storageKey: ?value,
      });
      expect(
        ModuleUpdatePreferences(await SharedPreferences.getInstance()).ignored,
        isEmpty,
      );
    }
  });
}
