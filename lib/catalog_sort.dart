import 'package:lpinyin/lpinyin.dart';

String normalizedSearchText(String text) {
  final value = String.fromCharCodes(
    text.runes.map(
      (rune) => rune >= 0xff01 && rune <= 0xff5e ? rune - 0xfee0 : rune,
    ),
  );
  return value.toLowerCase().replaceAll(
    RegExp(r'[\s\u3000，。！？、：；·「」『』（）【】《》“”‘’.,!?:;\-_/()\[\]{}]+'),
    '',
  );
}

bool matchesTitle(String title, String query) {
  final term = normalizedSearchText(query);
  if (term.isEmpty) return true;
  final text = normalizedSearchText(title);
  if (text.contains(term)) return true;
  try {
    return PinyinHelper.getShortPinyin(text).toLowerCase().contains(term);
  } on PinyinException {
    return false;
  }
}

int _seasonNumber(String label) {
  final numeric = int.tryParse(label);
  if (numeric != null) return numeric;
  const digits = {
    '零': 0,
    '〇': 0,
    '一': 1,
    '二': 2,
    '两': 2,
    '兩': 2,
    '三': 3,
    '四': 4,
    '五': 5,
    '六': 6,
    '七': 7,
    '八': 8,
    '九': 9,
  };
  var number = 0;
  var digit = 0;
  for (final rune in label.runes) {
    final character = String.fromCharCode(rune);
    if (digits.containsKey(character)) {
      digit = digit * 10 + digits[character]!;
    } else {
      final multiplier = character == '百' ? 100 : 10;
      number += (digit == 0 ? 1 : digit) * multiplier;
      digit = 0;
    }
  }
  return number + digit;
}

String _naturalTitle(String title) =>
    normalizedSearchText(title).replaceAllMapped(
      RegExp(r'第([0-9零〇一二两兩三四五六七八九十百]+)([季部])'),
      (match) => '第${_seasonNumber(match[1]!)}${match[2]}',
    );

int naturalTitleCompare(String left, String right) {
  final pattern = RegExp(r'\d+|\D+');
  final a = pattern
      .allMatches(_naturalTitle(left))
      .map((match) => match[0]!)
      .toList();
  final b = pattern
      .allMatches(_naturalTitle(right))
      .map((match) => match[0]!)
      .toList();
  for (var index = 0; index < a.length && index < b.length; index++) {
    final numberA = int.tryParse(a[index]);
    final numberB = int.tryParse(b[index]);
    final comparison = numberA != null && numberB != null
        ? numberA.compareTo(numberB)
        : a[index].compareTo(b[index]);
    if (comparison != 0) return comparison;
  }
  return a.length.compareTo(b.length);
}
