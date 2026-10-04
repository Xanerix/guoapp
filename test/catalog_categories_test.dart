import 'package:duanju_app/catalog_browser.dart';
import 'package:duanju_app/core_bridge.dart';
import 'package:duanju_app/models.dart';
import 'package:flutter_test/flutter_test.dart';

import 'fixtures.dart';

class CategoryRepository extends FixtureRepository {
  final categoryRequests = <String>[];
  bool failLegacy = false;
  bool paginate = false;

  @override
  Future<CatalogPage> cached(String source, {String category = ''}) async =>
      category.isEmpty
      ? cachedPages[source] ?? CatalogPage([])
      : CatalogPage([]);

  @override
  Future<List<CatalogCategory>> categories(
    String source, {
    bool force = false,
  }) async => switch (source) {
    'hongguo' => const [
      CatalogCategory.all,
      CatalogCategory('short_play', '真人剧'),
      CatalogCategory('comic_series', '漫剧'),
      CatalogCategory('ai_series', 'AI 剧'),
    ],
    'huangguo-video' => const [CatalogCategory.all, CatalogCategory('2', '短片')],
    'huangguoai' => const [
      CatalogCategory.all,
      CatalogCategory('ai-duanju', 'AI 短剧'),
      CatalogCategory('ai-manju', 'AI 漫剧'),
    ],
    'cloudfront' => const [
      CatalogCategory.all,
      CatalogCategory('old-short', 'AI成人短剧'),
    ],
    _ => const [CatalogCategory.all],
  };

  @override
  Future<CatalogPage> catalog(
    String source, {
    int page = 1,
    String query = '',
    String category = '',
    bool force = false,
  }) async {
    categoryRequests.add('$source|$category|$page');
    if (source == 'cloudfront' && failLegacy) throw AppFailure('合成入口失败');
    return CatalogPage(
      [
        Drama(
          id: '$source:$category:$page',
          source: source,
          title: '$source · ${category.isEmpty ? '全部' : category}',
          category: source == 'hongguo' ? '异能' : '',
        ),
      ],
      page: page,
      hasMore: paginate && source == 'huangguo-video' && page == 1,
    );
  }
}

void main() {
  test(
    'remote content types never hide fine categories from cached pages',
    () async {
      final repository = CategoryRepository();
      repository.cachedPages['hongguo'] = CatalogPage(const [
        Drama(
          id: 'hongguo:old1',
          source: 'hongguo',
          title: '缓存剧甲',
          category: '都市',
        ),
        Drama(
          id: 'hongguo:old2',
          source: 'hongguo',
          title: '缓存剧乙',
          category: '成长',
        ),
      ], page: 7);
      final browser = CatalogBrowser(repository);
      const group = SourceGroup('hongguo', '红果', [SourceSite.hongguo]);
      await browser.loadCategories(group);
      await browser.load(group, category: 'category:漫剧');
      expect(
        browser.categories(group).map((category) => category.name),
        containsAll(['真人剧', '漫剧', 'AI 剧', '都市', '成长', '异能']),
      );
      final local = await browser.load(group, category: 'local:都市');
      expect(local.items.map((drama) => drama.id), contains('hongguo:old1'));
    },
  );

  test(
    'group pagination retries failed member without skipping or reloading exhausted members',
    () async {
      final repository = CategoryRepository()
        ..failLegacy = true
        ..paginate = true;
      final browser = CatalogBrowser(repository);
      final group = SourceGroup.fromSources(
        SourceSite.knownValues,
      ).firstWhere((group) => group.id == 'huangguo');
      final first = await browser.load(group);
      expect(first.items.length, 2);
      expect(first.warning, '合成入口失败');
      repository.failLegacy = false;
      final next = await browser.load(group, more: true);
      expect(
        repository.categoryRequests
            .where((request) => request == 'cloudfront||1')
            .length,
        2,
      );
      expect(repository.categoryRequests, contains('huangguo-video||2'));
      expect(repository.categoryRequests, isNot(contains('huangguoai||2')));
      expect(next.items.length, 4);
      expect(next.warning, isEmpty);
    },
  );
}
