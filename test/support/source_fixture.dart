import 'package:duanju_app/app_build.dart';
import 'package:duanju_app/models.dart';

const allSourcesEnabled = appFlavor == 'zhenguojian';

SourceSite _site(
  String id,
  String name, {
  String group = '',
  String entry = '',
  bool defaultOff = false,
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
  defaultOff: defaultOff,
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
  _site('huangdou', '黄豆', defaultOff: true, search: false),
  _site('huangju', '剧果', defaultOff: true),
  _site('yeguo', '野果', defaultOff: true),
  _site('dsd', '帝果', defaultOff: true, vipPrompt: false),
  _site('jinguoduanju', '禁果短剧', defaultOff: true),
  _site('jinguo', '禁果', defaultOff: true),
  _site('heitao', '黑桃', defaultOff: true),
  _site('beabox', '笔盒', defaultOff: true),
  _site(
    'huangguo-video',
    '黄果视频',
    group: 'huangguo',
    entry: '视频',
    defaultOff: true,
    search: false,
  ),
  _site(
    'huangguoai',
    '黄果 AI',
    group: 'huangguo',
    entry: 'AI',
    defaultOff: true,
  ),
  _site(
    'cloudfront',
    '黄果旧版',
    group: 'huangguo',
    entry: '旧版',
    defaultOff: true,
    search: false,
  ),
]);
