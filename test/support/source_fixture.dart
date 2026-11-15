import 'package:duanju_app/models.dart';

const allSourcesEnabled = !bool.fromEnvironment('ONLY_HONGGUO');

SourceSite _site(
  String id,
  String name, {
  String group = '',
  String entry = '',
  bool search = true,
  bool hongguo = false,
  bool account = false,
  bool vipPrompt = true,
}) => SourceSite(
  id,
  name,
  '',
  group: group,
  groupLabel: group.isEmpty ? '' : '黄果',
  entryLabel: entry,
  available: allSourcesEnabled || hongguo,
  pagedSearch: search,
  searchSuggestions: hongguo,
  recommends: hongguo,
  danmaku: hongguo,
  devices: hongguo,
  account: account,
  vipPrompt: vipPrompt,
);

void installSourceFixture() => SourceSite.install([
  _site('hongguo', '红果', hongguo: true),
  _site('hanxiaoquan', '韩小圈'),
  _site('guipian', '鬼片'),
  _site('sorani', '青空', account: true),
  _site('huangdou', '黄豆', search: false),
  _site('huangju', '剧果'),
  _site('yeguo', '野果'),
  _site('dsd', '帝果', vipPrompt: false),
  _site('jinguoduanju', '禁果短剧'),
  _site('jinguo', '禁果'),
  _site('heitao', '黑桃'),
  _site('beabox', '笔盒'),
  _site(
    'huangguo-video',
    '黄果视频',
    group: 'huangguo',
    entry: '视频',
    search: false,
  ),
  _site('huangguoai', '黄果 AI', group: 'huangguo', entry: 'AI'),
  _site('cloudfront', '黄果旧版', group: 'huangguo', entry: '旧版', search: false),
]);
