import 'package:duanju_app/download_export.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('export names pad episode numbers to the episode count', () {
    expect(exportFileName('剧甲', 1, 0), '剧甲 第01集.mp4');
    expect(exportFileName('剧甲', 7, 80), '剧甲 第07集.mp4');
    expect(exportFileName('剧甲', 7, 120), '剧甲 第007集.mp4');
    expect(exportFileName('剧/甲', 105, 90), '剧_甲 第105集.mp4');
  });
}
