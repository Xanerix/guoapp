import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:duanju_app/danmaku_emoji.dart';

void main() {
  test('解析文字与表情片段', () {
    final segments = parseDanmakuSegments('道友们，你们爱的皇篇，又更新了[爽][爽][爽]');
    expect(segments.length, 4);
    expect(segments.first.text, '道友们，你们爱的皇篇，又更新了');
    expect(segments[1].isEmoji, isTrue);
    expect(segments[1].asset, 'assets/danmaku_emoji/1f60c.png');
    expect(danmakuSegmentsText(segments), '道友们，你们爱的皇篇，又更新了[爽][爽][爽]');
  });

  test('未知方括号保持原样文字', () {
    final segments = parseDanmakuSegments('这是[未知词]不是表情');
    expect(segments.length, 1);
    expect(segments.single.isEmoji, isFalse);
    expect(segments.single.text, '这是[未知词]不是表情');
  });

  test('纯文字返回单段', () {
    final segments = parseDanmakuSegments('普通弹幕');
    expect(segments.single.text, '普通弹幕');
    expect(hasDanmakuEmoji('普通弹幕'), isFalse);
    expect(hasDanmakuEmoji('好[赞]'), isTrue);
  });

  test('截断按字符计，表情算一个字符', () {
    final segments = parseDanmakuSegments('a[赞]b[赞]c');
    final truncated = truncateDanmakuSegments(segments, 3);
    expect(danmakuSegmentsText(truncated), 'a[赞]b…');
  });

  test('截断不超长时不加省略号', () {
    final segments = parseDanmakuSegments('abc');
    expect(danmakuSegmentsText(truncateDanmakuSegments(segments, 10)), 'abc');
  });

  test('空文本返回空片段', () {
    expect(parseDanmakuSegments(''), isEmpty);
  });

  testWidgets('富文本测量包含表情占位宽度', (tester) async {
    const style = TextStyle(fontSize: 16);
    final textOnly = measureDanmakuSegments(
      parseDanmakuSegments('一段普通弹幕'),
      style,
      fontSize: 16,
    );
    final withEmoji = measureDanmakuSegments(
      parseDanmakuSegments('一段[赞]普通弹幕'),
      style,
      fontSize: 16,
    );
    expect(textOnly, greaterThan(0));
    expect(withEmoji, greaterThan(textOnly));
    expect(danmakuEmojiExtent(fontSize: 16), closeTo(16 * 1.15 + 2, 1e-9));
  });

  testWidgets('表情资源都能被打包加载', (tester) async {
    for (final codepoint in danmakuEmojiCodepoints.values) {
      final data = await rootBundle.load('assets/danmaku_emoji/$codepoint.png');
      expect(data.lengthInBytes, greaterThan(0), reason: '缺少 $codepoint.png');
    }
  });

  test('映射表每个表情都有资源路径', () {
    for (final entry in danmakuEmojiCodepoints.entries) {
      final segment = parseDanmakuSegments('[${entry.key}]');
      expect(
        segment.single.asset,
        'assets/danmaku_emoji/${entry.value}.png',
        reason: '表情 ${entry.key} 未正确映射',
      );
    }
  });
}
