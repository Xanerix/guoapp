# 开发指南

## 环境

Flutter `3.47.9`、Dart `3.12+`、Go `1.24.1+`、Python `3.10+`。

- Android：JDK 17、SDK 36、NDK `28.2.13676358`
- Windows：Visual Studio C++ 桌面组件、MinGW-w64 x64、Inno Setup 6.5+（`winget install JRSoftware.InnoSetup`）
- iOS：macOS、完整 Xcode、CocoaPods
- macOS：完整 Xcode、CocoaPods，打 dmg 需 `pip install dmgbuild Pillow`；`xcode-select` 指向 Command Line Tools 时 `build_macos.py` 会自动改用 `/Applications/Xcode*.app`，也可以在系统层面执行 `sudo xcode-select -s`
- Linux：Ubuntu 22.04（或兼容 glibc 2.35 环境）、`clang`、`cmake`、`ninja-build`、`pkg-config`、`libgtk-3-dev`、`libmpv-dev`、`libepoxy-dev`、`libjson-glib-dev`、`libx265-dev`、`libvidstab-dev`、`patchelf` 及 `appimagetool`

构建脚本对子进程默认设置 `GOPROXY=https://goproxy.cn,direct`、`GOSUMDB=off`，同名环境变量可覆盖。

## 调试运行

本仓库不含任何站源代码。站源是运行时加载的 JS 模块，调试时把模块文件夹放进数据目录的 `modules` 下（见[站源模块](#站源模块)）；仓库里的 `native/module_example/jsexample` 是只返回假数据的示例模块，不访问网络、不能播放，用来调试界面。

Flutter 运行前必须先编译对应平台 / 架构的 Go 核心：

~~~sh
python3 scripts/build_native.py --platform android --abi arm64-v8a
flutter pub get --enforce-lockfile
flutter run
~~~

## 出包

~~~sh
python3 scripts/build_android.py
python3 scripts/build_android.py --abi arm64-v8a
python scripts/build_windows.py
python3 scripts/build_ios.py
python3 scripts/build_macos.py
python3 scripts/build_linux.py
python3 scripts/build_ios.py --core-only [--simulator]
python3 scripts/build_ios.py --export-options /path/to/ExportOptions.plist
~~~

产物在 `dist/android`、`dist/windows`、`dist/ios`、`dist/macos`，以 `guoapp-` 开头。Windows 同时出便携 zip 和 Inno Setup 安装包（`windows/installer/setup.iss`，简体中文语言文件取自 Inno Setup 官方仓库），安装包以包名为 AppId，默认装到 `%LOCALAPPDATA%\Programs\<包名>`，安装时可提权改为所有用户，卸载窗口先显示「卸载选项」页（插在卸载进度窗口里），勾选「同时清除本地数据」才删除 `%APPDATA%\<包名>`（含默认下载目录），默认保留；静默卸载不删。缓存目录 `%LOCALAPPDATA%\<包名>` 卸载时总是删除。macOS 出 arm64 + x86_64 通用 dmg；dmg 的安装窗口（背景图、图标位置、隐藏工具栏）由 `dmgbuild` 写入 `.DS_Store`，打包机需要 `pip install dmgbuild Pillow`，没装时会退回普通镜像并提示。默认 ad-hoc 签名，从网络下载的 dmg 在其他 Mac 上首次打开会被 Gatekeeper 拦一次（右键「打开」，或执行 `xattr -dr com.apple.quarantine /Applications/小果影视.app`）。有 Developer ID 时用 `package_release.py --platform macos --identity "Developer ID Application: ..."` 重新打包并公证，才能免掉这一步。本机 Android 签名读 `android/key.properties`（不入库）。

其他脚本：`generate_brand_assets.py` 从 `brand_icon.svg` 生成各平台图标；`clear_app_data.py`、`clear_catalog_cache.py` 清理 Windows 上的应用数据和剧库缓存。

## 检查与测试

~~~sh
dart format --output=none --set-exit-if-changed lib test
dart analyze --fatal-infos lib test
flutter test --dart-define=DISABLE_REMOTE_IMAGES=true
flutter test --dart-define=DISABLE_REMOTE_IMAGES=true --dart-define=ONLY_HONGGUO=true   # 模拟只装了红果
cd native
go test -race ./...
~~~

Flutter 测试一律带 `DISABLE_REMOTE_IMAGES=true`，避免加载站源图片。测试只覆盖数据兼容、权限和逻辑层，界面改动不写测试；公共假仓库在 `test/support/`。

## 架构

Flutter 界面 + Go 原生核心。站源请求、解析、签名、缓存、下载调度和播放代理都在 Go 里完成，Dart 只负责界面、播放器和本地用户数据。

- **FFI**：`native/bridge` 以 `-buildmode=c-shared` 只导出 `DuanjuRequest(json) -> json` 与 `DuanjuFree`。`lib/core_bridge.dart` 的 `NativeRepository` 把每次调用编码成 `{"action": ...}`，在 `Isolate.run` 中调用；Go 侧在 `native/core/app_runtime.go` 的 `NativeRequest` 里按 action 分派。新增能力两端都要改。Android 与 Windows 动态加载（`libduanju_core.so` / `duanju_core.dll`）；iOS 与 macOS 改成 `-buildmode=c-archive` 静态库（`ios/DuanjuCore`、`macos/DuanjuCore`）链进主程序，Dart 侧用 `DynamicLibrary.process()` 查符号，避免 dylib 的签名与库校验问题。
- **Go 核心**：`native/core` 是运行时。站源模块由 `module_loader.go` 读取清单并注册，`module_script.go` 用 [goja](https://github.com/dop251/goja) 执行并适配成内部的 `Provider` 接口。`app_*.go` 运行时与业务（目录缓存、下载、弹幕、存储、设置），`network_*.go` HTTP 客户端、DNS、CDN、限流。`app_stream.go` 在本机起播放代理，重写 HLS playlist、透传 Range，并处理带密钥的媒体。
- **Flutter**：界面在 `lib/ui/`（按页面分子目录，入口 `lib/main.dart` → `lib/ui/app.dart`），逻辑层平铺在 `lib/`，状态管理只用 ChangeNotifier。
- **播放与下载**：播放器用 [media_kit](https://github.com/media-kit/media-kit) / libmpv；每集下载完由 [FFmpegKit min-gpl](https://github.com/sk3llo/ffmpeg_kit_flutter)（含 GPL 媒体组件）转成单个 mp4，只换封装、不重新编码，不参与正常播放。
- **本地数据**：存储格式须兼容旧版。本地快照遇到未知键会整体报错，新增设置若要兼容旧版须存为快照外的独立键。

## 目录结构

| 目录 | 内容 |
| --- | --- |
| `lib/ui` | 界面：主题、导航壳、各页面、播放器、公共组件 |
| `lib` | 逻辑层：FFI、站源目录、播放、下载、本地存储与用户 |
| `native/core`、`native/bridge` | Go 运行时（缓存、下载、播放代理）及 C ABI |
| `native/module_example` | 示例站源模块（假数据，不访问网络） |
| `android`、`windows`、`ios`、`macos` | 平台工程与资源 |
| `packages/media_kit_libs_windows_video` | 本地覆盖的 Windows 媒体依赖插件 |
| `scripts` | 构建、图标生成与数据清理脚本 |
| `test` | 逻辑层测试 |

## 站源模块

站源是运行时加载的 JS 模块，模块接口为第 1 版（清单里的 `api` 填 `1`）。

- 位置：数据目录下的 `modules/<模块标识>/`，应用启动时加载。Windows 的数据目录是 `%APPDATA%\<包名>`，便携版是程序旁的 `userdata`。
- 文件：`manifest.json` 与 `main.js`，写法参照 `native/module_example/jsexample`。
- 安装包：zip，`manifest.json` 和 `main.js` 放在根目录或唯一的一层文件夹里，其余文件忽略，整包不超过 8 MB。在「模块」页里从文件或地址安装，同一标识覆盖旧版本，标识或别名与其他站源重复时拒绝安装；安装和卸载不用重启。
- 模块商店：读取 GitHub 组织 guoapp-modules 的 `index` 仓库里的 `index.json`（先 raw.githubusercontent.com，失败依次切换 jsDelivr 与 GitHub 代理镜像），按其中的仓库和标签下载 `manifest.json` 与 `main.js` 并核对 sha256，再走同样的安装确认。模块的发版和上架方式见该组织的 `template` 与 `index` 仓库。
- 内置模块：`build_native.py` / `build_ios.py` 把 `--bundle <模块目录>`（可多次指定）和环境变量 `BUNDLED_MODULES`（逗号分隔）里的模块复制到 `native/core/bundled/`（相对路径以仓库根目录为准），由 Go 以 `go:embed` 打进核心；该目录除 `.gitkeep` 外不入库，每次出包前清空重建。应用启动时，未安装或内置版本更高的内置模块写入数据目录的 `modules/`，用户卸载过的不再自动安装（记在 `modules/.bundled-removed.json`），模块商店在「内置」里照常列出，从安装包内的副本安装，索引读不到时也能装；索引里也有同一模块时，「在线仓库」另列一项，从索引下载，已安装模块按两者中较高的版本提示更新。官方包内置红果，来自私有仓库 guoapp-modules/hongguo，CI 用 Secret `HONGGUO_DEPLOY_KEY`（只读部署密钥）检出到 `bundled-modules/hongguo`。
- 内置模块云更新：应用启动后在后台静默检查内置模块的云更新（`native/core/module_cloud_update.go`，不受「启动时检查更新」开关影响，用户主动卸载的不装回）。地址在编译期由环境变量 `MODULE_UPDATE_URL`（逗号分隔，未设时回退 `UPDATE_SERVER_URL`，即先走日本 VPS 反代再切 Cloudflare Worker）和签名公钥 `MODULE_UPDATE_PUBLIC_KEY`（Base64 编码的 32 字节 Ed25519 公钥）经 `scripts/app_build.py` 注入 Go。客户端请求 `GET /v1/modules/<id>/latest.json`（带请求头 `X-App-Client: guoapp`）匹配当前 `api` 槽位，远端版本更高时下载 zip 并核对 SHA-256 与 Ed25519 签名，校验通过后原子热替换并保留模块状态。Worker（`worker/`）提供同构的 `GET` 分发与带 `Authorization: Bearer <MODULE_PUBLISH_TOKEN>` 的 `PUT` 上传接口，包体存于 D1 表 `module_packages`。
- 清单：`id`（小写字母、数字、`_`、`-`，最长 32 个字符，须与文件夹同名）、`name`、`version`、`description`、`runtime`（只支持 `js`）、`api`（当前为 `1`）、`adult`（必填，是否限制级内容，`true` 或 `false`）、`hosts`（允许请求的域名，`*.example.com` 匹配子域名）、`capabilities`（可选能力，见下表）、`rankings`（榜单列表，每项 `id`、`name`、`description`）。`mediaAuthPaths`（声明 `mediaAuth` 时必填，见下表）。其余可选字段对应站源描述：`allowEmptyCatalog`、`group`、`groupName`、`entryName`、`skipVipPrompt`、`aliases`、`hostSuffixes`、`baseUrl`、`noHostRotation`、`keepDetailEpisodes`、`continuousFeed`、`browserFingerprint`。
- 入口：`main.js` 是 CommonJS，用 `module.exports` 导出 `categories(force)`、`catalogPage(page, category)`、`detail(sourceId)`、`resolveMedia(task)`，以及可选的 `validCategory(category)`。`categories` 只取到部分分类时返回 `{ items, incomplete: true }`，应用稍后会重取；`catalogPage` 和 `search` 返回 `{ items, hasMore, warning }`，有剧集又有 `warning` 时作为提示显示。函数必须同步返回，不能是异步函数。需要用户登录时抛出带 `loginRequired = true` 属性的 `Error`。
- 分集：`detail` 返回的分集可带 `mediaType: "audio"` 标明是音频，播放时显示封面并隐藏画面比例；未声明时，文件只有音轨也按音频处理。多数分集的 `title` 不是「第 N 集」这类序号时，选集改为单列显示标题。
- 能力：声明后才会调用对应的函数。

  | 能力 | 导出函数 |
  | --- | --- |
  | `search` | `search(page, query)` |
  | `account` | `account(command, payload)` |
  | `suggestions` | `suggestions(query)`，返回字符串数组 |
  | `recommendations` | `validRecommendation(genre, cursor)`（参数无效时抛错）、`recommendations(genre, cursor, seen)`，返回 `{ items, cursor, hasMore }` |
  | `danmaku` | `danmakuIds(task)`，返回 `{ series, video }` 或 `null`；`danmaku(series, video, startMs, durationMs)` |
  | `devices` | `devices(register)`，返回是否就绪 |
  | `rankings` | `rankingPage(boardId, page)`，返回 `{ items: [{ rank, drama, metric }], hasMore, totalPages, updatedText }` |
  | `seasons` | `seasons(sourceId)`，返回 `[{ number, drama }]` |
  | `cover` | `coverAddress(drama, sourceId)`，返回封面地址 |
  | `metadata` | `needsExtraMetadata(drama)`、`extraMetadata(drama, sourceId)`，返回要合并的剧集字段（`id` 须与原剧相同）或 `null` |
  | `catalogState` | `catalogState()`、`restoreCatalogState(state)`，状态由应用随目录缓存保存 |
  | `mediaAuth` | `mediaAuthorization(address, referer)`，返回 `{ header, value }`，附加到清单域名下、路径以 `mediaAuthPaths` 某项开头的媒体请求上；`rejectMediaAuthorization(address, value)` 在该凭证被站点拒绝时调用，随后重取一次 |

- 宿主：全局对象 `host` 提供：
  - 标识：`source`、`dramaId(sourceId)`、`chapterId(sourceId, key)`。
  - 网络：`fetch(request)`、`fetchText(request)`、`fetchPlaylist(request)`、`reserve({ url, lane, background })`（先占限流名额，返回的 `ticket` 传给 `fetch`，用于签名里带时间戳的请求）、`retries()`、`maxPages()`、`sleep(ms)`。`fetch` 的 `acceptStatus` 列出按正常响应返回的错误状态码（响应体须是 JSON）。`fetch` 和 `reserve` 抛出的错误带 `backoff`（站点要求暂缓）和 `oversized`（响应过大）属性，`fetch` 的错误还带 `status`、`body`、`blocked`（站点返回的状态码、响应体和是否被拦截）；报错信息里的站源名取清单的 `name`。`fetch` 的 `body` 可以是字符串或字节，`binary: true` 时响应的 `body` 为 `Uint8Array`（错误上的 `body` 仍是文本）；`fetch` 的 `direct` 改用不带浏览器指纹的普通连接；`fetchText` 的 `mirrors` 列出主地址失败后依次尝试的候选地址；`preferredSite()` 返回最近一次请求成功的域名（`scheme://host`）。请求和候选地址都只能发往清单声明的域名。
  - 媒体：`prepareWebMedia(media, sourceName)`、`finishMedia(media)`。`resolveMedia` 返回的 `cencKey`、`hlsKey` 用 Base64 字符串。
  - 目录：`catalogContext()` 返回 `{ more, background }`，`isKnown(id)` 判断剧集是否已在列表里，`reportProgress(items)` 边取边上报。
  - 数据：`loadState(name)`、`saveState(name, value)`（按模块隔离、存到磁盘），`cacheGet(key)`、`cacheSet(key, value, ttlMs)`（同一模块各实例共享的内存缓存，`ttlMs` 为 0 表示不过期，`value` 为 `null` 删除），`exclusive(fn)`，`log(message)`。
  - 解析：`parseUrl(raw)`、`resolveUrl(base, ref)`（按 `base` 解析相对地址）、`parseHtml(text)`（返回 `{ tag, attrs, text, children }` 节点树）、`preserveIntegers(text)`（把 JSON 里超出安全范围的整数改成字符串，再交给 `JSON.parse`）。
  - 字节与加密：`utf8Encode`、`utf8Decode`、`hexEncode`、`hexDecode`、`base64Encode`、`base64Decode`、`md5`、`sha256`、`sha512`、`randomBytes(n)`、`aesCbcEncrypt(key, iv, data)`、`aesCbcDecrypt(key, iv, data)`（都不处理填充）、`aesGcmSeal(key, nonce, plain, aad)`、`aesGcmOpen(key, nonce, sealed, aad)`（校验失败抛错）、`ecdhP256KeyPair()`（返回 `{ privateKey, publicKey }`）、`ecdhP256(privateKey, publicKey)`、`hkdfSha256(secret, salt, info, length)`、`gzip(data)`、`gunzip(data)`（解压后上限 20MB）。字节用 `Uint8Array` 表示，输入也接受字符串（按 UTF-8）和数字数组。站点私有的签名算法写在模块里，JS 的 `BigInt` 足够快。
- 并发：同一个模块会有多个互不共享变量的实例同时执行，跨调用的数据放进状态或缓存；读改写的过程用 `host.exclusive` 包起来，它不可重入，嵌套调用会抛错。
- 调试：在 `native` 下执行 `go run ./cmd/moduletool <模块目录> <请求 JSON>`，在临时数据目录里加载该模块并依次执行请求，例如 `{"action":"catalog","source":"jsexample","page":1}`。
- 运行环境不是浏览器也不是 Node：没有 `fetch`、`URL`、`Buffer`、`atob`、`TextEncoder` 这些全局对象。
- 剧和分集的 ID 会写进用户的追剧、记录和下载，发布后不能改格式。

## 发布

工作流 `.github/workflows/build.yml` 只能在 `main` 上手动触发，推送和 PR 不会自动运行。

1. 同时提升 `pubspec.yaml` 的版本号与构建号。
2. 在 `CHANGELOG.md` 新增同名一节（标题如 `## 1.0.1+101`），工作流会把它写入 Release 说明；未验证的改动标为开发快照。
3. 推送到 `main`，在 Actions 中手动运行 **Build app packages**。

工作流先检查版本：tag `app-v{version}`（`+` 替换为 `-`）或同名 Release 已存在时直接失败。随后四个平台各构建一份：

| 产物 | 内容 |
| --- | --- |
| `android` | 三种架构 APK，使用发布证书签名并通过指纹校验 |
| `windows` | Windows x64 安装包和便携 ZIP |
| `ios-unsigned` | arm64 iPhone 未签名 IPA |
| `macos` | arm64 + x86_64 通用 dmg，ad-hoc 签名、未公证 |

格式、分析、测试不阻塞出包；Android、Windows、iOS、macOS 全部成功后才创建 tag 和 Release，任一平台失败就不发布，已有 Release 不会被覆盖。

Android 签名只用仓库 Secrets：`SIGNING_KEYSTORE_BASE64`、`SIGNING_KEYSTORE_PASSWORD`、`SIGNING_KEY_ALIAS`、`SIGNING_KEY_PASSWORD`，另需仓库变量 `SIGNING_CERT_SHA256`（证书 SHA-256，冒号和大小写不限），缺任意一项都会失败。CI 通过环境变量把签名传给 Gradle；本机读 `android/key.properties`（不入库）。

包名取环境变量 `PACKAGE_NAME`（CI 取同名仓库变量），没设或为空时用 `com.guoapp.app`，由 Android `build.gradle.kts`、Windows `app_branding.cmake`、iOS / macOS `scripts/app_build.py` 读取。应用名固定为「小果影视」，英文名 `GuoApp`；安装包前缀、Windows 可执行文件和使用统计的构建标识为 `guoapp`。

Windows 数据目录为 `%APPDATA%\<包名>`：`Runner.rc` 不设 CompanyName，ProductName 为包名，`path_provider` 据此拼出应用支持目录。删除不影响使用的缓存（`covers-v1/`、`catalogs.json`、`rankings.json`）放在 `getApplicationCacheDirectory()` 给出的目录，Windows 为 `%LOCALAPPDATA%\<包名>`，便携版为 `userdata\cache`；初始化时由 `moveLegacyNativeCache` 把数据目录里的旧缓存搬过去，新增缓存文件要写进 `cacheDirectory` 并加到这份清单。站源的 `CatalogState`（红果设备、辅助设备和列表游标）不算缓存，存在数据目录的 `provider-states.json`，旧版写在 `catalogs.json` 里的会在首次启动时迁出。旧版数据在 `%APPDATA%\真果鉴\真果鉴`，`lib/windows_data_migration.dart` 在启动最早阶段检查：新目录为空且旧目录存在时整个改名移入；新目录已有内容或移动失败都不处理，失败写入日志。Android TV 横幅 `tv_banner.png` 只有图标不带文字，改名不用重新生成。

关于页的重建版 GitHub、Telegram 交流群组和更新频道链接分别取环境变量 `PROJECT_URL`、`TELEGRAM_GROUP_URL`、`TELEGRAM_CHANNEL_URL`（CI 取同名仓库变量），由 `scripts/app_build.py` 作为 `--dart-define` 传给 Flutter；没设的链接在关于页不显示，直接 `flutter run` 时需自行加 `--dart-define`。`PROJECT_URL` 同时注入 Go 的 `nativeProjectURL`，检查更新只接受 `{PROJECT_URL}/releases/` 开头的发布页，没设时启动不提示更新，手动检查更新会提示没有配置。

## 检查更新与使用统计

接收端是 Cloudflare Worker `guoapp-update`（`worker/`，D1 数据库 `guoapp-stats`），地址在编译期由环境变量 `UPDATE_SERVER_URL` 经 `scripts/app_build.py` 注入 Go（CI 取仓库变量 `UPDATE_SERVER_URL`），没设时检查更新和使用统计都不发请求，fork 和本地构建默认不会连到这里。变量可填逗号分隔的多个地址，Go 按顺序尝试，前一个连不上或返回错误时换下一个；CI 里先填国内可直连的中转服务器，再填 Worker。Go 核心（`native/core/app_update.go`）的 `checkUpdate` 请求 `GET /v1/latest`，只读不写；`reportUsage` 请求 `POST /v1/ping`，按北京时间日期和随机编号在 `pings` 表里每天记一行。两者分别由设置里的「启动时检查更新」和「匿名使用统计」控制。各平台点「更新」都只打开发布页，应用内不下载安装包。Worker 每小时从 GitHub 读取最新 Release 写入 `meta` 表。改 Worker 后在 `worker/` 下运行 `npx wrangler deploy`。

在 `worker/` 下查询（`--remote` 查线上库）：

~~~sh
npx wrangler d1 execute guoapp-stats --remote --command "SELECT day, COUNT(*) AS users FROM pings GROUP BY day ORDER BY day DESC LIMIT 30"   # 日活
npx wrangler d1 execute guoapp-stats --remote --command "SELECT COUNT(DISTINCT id) FROM pings WHERE day >= date('now', '-30 days')"          # 近 30 天活跃
npx wrangler d1 execute guoapp-stats --remote --command "SELECT COUNT(DISTINCT id) FROM pings"                                               # 累计装机
npx wrangler d1 execute guoapp-stats --remote --command "SELECT platform, build, version, COUNT(*) FROM pings WHERE day = date('now', '+8 hours') GROUP BY 1, 2, 3 ORDER BY 4 DESC"  # 今日分布
~~~

应用显示名：Android 在 `android/app/build.gradle.kts`，Windows 在 `windows/runner/app_branding.cmake`，iOS 由 Xcode「Thin Binary」构建阶段写入，macOS 由 `scripts/package_release.py` 打包时改 `CFBundleDisplayName` / `CFBundleName` / `CFBundleIdentifier` 后重新签名。iOS 的 `CFBundleDisplayName` 用英文名 `GuoApp` 以通过侧载工具的名称校验，中文名写在 `zh-Hans.lproj/InfoPlist.strings`。macOS 关闭了 App Sandbox，以便本地播放代理和自选下载目录正常工作；系统代理由 `MainFlutterWindow.swift` 的 `duanju/device` 通道读取。
