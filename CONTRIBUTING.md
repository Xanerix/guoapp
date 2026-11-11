# 参与开发

本仓库是应用的界面与 Go 运行时；官方站源放在单独的私有仓库，不在这里。环境、构建和架构见 [DEVELOPMENT.md](DEVELOPMENT.md)。

## 界面与运行时

不需要站源仓库。直接按 DEVELOPMENT.md 编译 Go 核心，脚本会自动接入只返回假数据的示例站源 `native/sources_example`，目录、分类、搜索和详情都能走通，播放会提示不可用。

提交 PR 时：

- 用户能感知的改动在 `CHANGELOG.md` 当前版本一节补一条。
- 界面改动不写单元测试，在 PR 里写清楚在哪个平台、哪个页面验证过。
- 不新增解释性代码注释，靠命名和模块边界表达意图。
- 不要提交 `native/go.work`、`native/bridge/sources_gen.go` 和 `android/app/src/main/jniLibs` 里的核心库。

本仓库不接受具体站点的代码，站源相关的 PR 请改成社区站源模块。

## 社区站源

站源是编译期接入的 Go 模块，社区可以各自维护站源仓库并自行出包：

1. 把 `native/sources_example` 复制到自己的仓库，改掉 `go.mod` 的模块名和站源 ID。
2. 按 DEVELOPMENT.md 的[站源开发约定](DEVELOPMENT.md#站源开发约定)实现 `core.Provider`，在 `flavor_*.go` 里把站源加进版本名单，并保留 `var _ core.SourceAPI1` 这类接口版本声明。
3. 用 `--sources <站源仓库路径>` 接入后出包；可以多次指定，同时接入多个站源模块。

官方发布包只包含官方站源，不收录、不审核、不分发社区站源，社区站源与本项目无关，使用和分发的责任由其作者和使用者承担。

自行出包建议用全站源版（`--all-sources`），并设置环境变量 `ZHENGUOJIAN_PACKAGE_NAME`、`ZHENGUOJIAN_DISPLAY_NAME`、`ZHENGUOJIAN_LATIN_NAME` 为自己的包名和名称，避免覆盖安装或被误认为官方版本。

## 站源接口的改动

社区站源依赖 `native/core` 的导出接口。需要新增接口或能力时，欢迎直接提 PR 到本仓库；不兼容的改动要按 DEVELOPMENT.md 的[接口版本](DEVELOPMENT.md#接口版本)规则升级版本号，并在 `CHANGELOG.md` 写明迁移办法。
