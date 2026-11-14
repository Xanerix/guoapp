import 'package:flutter/widgets.dart';

const Map<String, String> danmakuEmojiCodepoints = {
  '爽': '1f60c',
  '偷笑': '1f60f',
  '没看够': '1f97a',
  '笑': '1f604',
  '笑哭': '1f602',
  '尬笑': '1f605',
  '奸笑': '1f63c',
  '捂脸': '1f926',
  '爱慕': '1f60d',
  '飞吻': '1f618',
  '鼓掌': '1f44f',
  '赞': '1f44d',
  '送花': '1f490',
  '震惊': '1f631',
  '思考': '1f914',
  '抓狂': '1f62b',
  '怒': '1f620',
  '害羞': '1f60a',
  '哭': '1f622',
  '快哭了': '1f979',
  '求爆更': '1f64f',
  'OK': '1f44c',
  '什么': '2753',
  '流泪': '1f62d',
  '大笑': '1f606',
  '微笑': '1f642',
  '坏笑': '1f608',
  '白眼': '1f644',
  '疑问': '2754',
  '闭嘴': '1f910',
  '色': '1f61b',
  '调皮': '1f61c',
  '难过': '1f614',
  '馋': '1f924',
  '晕': '1f635',
  '流汗': '1f613',
  '生病': '1f912',
  '睡着': '1f634',
  '狗头': '1f436',
  '比心': '1f91f',
  '火': '1f525',
  '加油': '1f4aa',
  '抱抱': '1f917',
  '吃瓜': '1f349',
  '无语': '1f611',
  '666': '1f44c',
  '爱心': '2764-fe0f',
  '拳头': '1f44a',
  '狂笑': '1f923',
  '惊喜': '1f929',
};

final RegExp danmakuEmojiPattern = RegExp(r'\[([^\[\]\s]{1,8})\]');

class DanmakuSegment {
  const DanmakuSegment.text(this.text) : asset = null;
  const DanmakuSegment.emoji(this.asset) : text = null;

  final String? text;
  final String? asset;

  bool get isEmoji => asset != null;

  @override
  bool operator ==(Object other) =>
      other is DanmakuSegment && other.text == text && other.asset == asset;

  @override
  int get hashCode => Object.hash(text, asset);

  @override
  String toString() => isEmoji ? 'emoji($asset)' : 'text($text)';
}

String? danmakuEmojiAsset(String name) {
  final codepoint = danmakuEmojiCodepoints[name];
  if (codepoint == null) return null;
  return 'assets/danmaku_emoji/$codepoint.png';
}

bool hasDanmakuEmoji(String text) {
  for (final match in danmakuEmojiPattern.allMatches(text)) {
    if (danmakuEmojiCodepoints.containsKey(match.group(1))) return true;
  }
  return false;
}

List<DanmakuSegment> parseDanmakuSegments(String text) {
  if (text.isEmpty) return const [];
  final segments = <DanmakuSegment>[];
  var cursor = 0;
  for (final match in danmakuEmojiPattern.allMatches(text)) {
    final asset = danmakuEmojiAsset(match.group(1)!);
    if (asset == null) continue;
    if (match.start > cursor) {
      segments.add(DanmakuSegment.text(text.substring(cursor, match.start)));
    }
    segments.add(DanmakuSegment.emoji(asset));
    cursor = match.end;
  }
  if (cursor < text.length) {
    segments.add(DanmakuSegment.text(text.substring(cursor)));
  }
  return segments.isEmpty ? [DanmakuSegment.text(text)] : segments;
}

List<DanmakuSegment> truncateDanmakuSegments(
  List<DanmakuSegment> segments,
  int maxRunes,
) {
  if (maxRunes <= 0) return const [];
  final kept = <DanmakuSegment>[];
  var used = 0;
  var truncated = false;
  for (final segment in segments) {
    if (used >= maxRunes) {
      truncated = true;
      break;
    }
    final text = segment.text;
    if (text == null) {
      kept.add(segment);
      used++;
      continue;
    }
    final runes = text.runes.toList();
    final room = maxRunes - used;
    if (runes.length <= room) {
      kept.add(segment);
      used += runes.length;
      continue;
    }
    kept.add(DanmakuSegment.text(String.fromCharCodes(runes.take(room))));
    used = maxRunes;
    truncated = true;
    break;
  }
  if (truncated) {
    final last = kept.isEmpty ? null : kept.last;
    if (last != null && last.text != null) {
      kept[kept.length - 1] = DanmakuSegment.text('${last.text}…');
    } else {
      kept.add(const DanmakuSegment.text('…'));
    }
  }
  return kept;
}

double danmakuEmojiExtent({
  required double fontSize,
  TextScaler textScaler = TextScaler.noScaling,
}) => textScaler.scale(fontSize) * 1.15 + 2;

double measureDanmakuSegments(
  List<DanmakuSegment> segments,
  TextStyle style, {
  TextScaler textScaler = TextScaler.noScaling,
  required double fontSize,
}) {
  final extent = danmakuEmojiExtent(fontSize: fontSize, textScaler: textScaler);
  final spans = <InlineSpan>[];
  var placeholders = 0;
  for (final segment in segments) {
    final text = segment.text;
    if (text != null) {
      if (text.isEmpty) continue;
      spans.add(TextSpan(text: text, style: style));
      continue;
    }
    spans.add(
      WidgetSpan(
        alignment: PlaceholderAlignment.middle,
        child: SizedBox(width: extent, height: extent),
      ),
    );
    placeholders++;
  }
  if (spans.isEmpty) return 0;
  final painter = TextPainter(
    text: TextSpan(style: style, children: spans),
    textDirection: TextDirection.ltr,
    textScaler: textScaler,
    maxLines: 1,
  );
  if (placeholders > 0) {
    painter.setPlaceholderDimensions(
      List<PlaceholderDimensions>.generate(
        placeholders,
        (_) => PlaceholderDimensions(
          size: Size(extent, extent),
          alignment: PlaceholderAlignment.middle,
        ),
      ),
    );
  }
  painter.layout();
  final width = painter.width;
  painter.dispose();
  return width;
}

String danmakuSegmentsText(List<DanmakuSegment> segments) {
  final buffer = StringBuffer();
  for (final segment in segments) {
    final text = segment.text;
    if (text != null) {
      buffer.write(text);
      continue;
    }
    final asset = segment.asset!;
    final stem = asset.split('/').last.replaceAll('.png', '');
    var name = stem;
    for (final entry in danmakuEmojiCodepoints.entries) {
      if (entry.value == stem) {
        name = entry.key;
        break;
      }
    }
    buffer.write('[$name]');
  }
  return buffer.toString();
}
