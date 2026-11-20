import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';

Drama _drama(String cover) =>
    Drama(id: 'hongguo:book-1', source: 'hongguo', title: '书', cover: cover);

void main() {
  test('re-signed cover keeps the known address', () {
    const known = 'https://p3.example.com/static/a~620.heic?sig=1&exp=1';
    const resigned = 'https://p3.example.com/static/a~620.heic?sig=2&exp=2';
    expect(_drama(known).merge(_drama(resigned)).cover, known);
  });

  test('different cover replaces the old one', () {
    const old = 'https://p3.example.com/static/a.jpg';
    const fresh = 'https://p3.example.com/static/b.jpg';
    expect(_drama(old).merge(_drama(fresh)).cover, fresh);
    expect(_drama('').merge(_drama(fresh)).cover, fresh);
    expect(_drama(old).merge(_drama('')).cover, old);
  });
}
