import 'package:duanju_app/local_store.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fixtures.dart';

void main() {
  Future<LocalStore> store() async {
    SharedPreferences.setMockInitialValues({});
    return LocalStore(await SharedPreferences.getInstance());
  }

  test('watch progress and favorites survive a new store', () async {
    final local = await store();
    await local.saveWatch(
      WatchEntry(
        drama: FixtureRepository.free,
        episode: 2,
        position: 18.5,
        duration: 90,
        updatedAt: DateTime.now(),
      ),
    );
    await local.toggleFavorite(FixtureRepository.free);
    final restored = LocalStore(local.preferences);
    expect(restored.watched(FixtureRepository.free.id)?.position, 18.5);
    expect(restored.watched(FixtureRepository.free.id)?.episode, 2);
    expect(restored.isFavorite(FixtureRepository.free.id), isTrue);
  });
}
