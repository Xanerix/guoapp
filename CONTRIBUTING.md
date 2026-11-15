# 参与开发

本仓库是应用的界面与 Go 运行时，不含任何站源。环境、构建和架构见 [DEVELOPMENT.md](DEVELOPMENT.md)。

## 界面与运行时

按 DEVELOPMENT.md 编译 Go 核心，再把示例模块 `native/module_example/jsexample` 放进数据目录的 `modules` 下，目录、分类、搜索和详情都能走通，播放会提示不可用。

提交 PR 时：

- 用户能感知的改动在 `CHANGELOG.md` 当前版本一节补一条。
- 界面改动不写单元测试，在 PR 里写清楚在哪个平台、哪个页面验证过。
- 不新增解释性代码注释，靠命名和模块边界表达意图。
- 不要提交 `android/app/src/main/jniLibs` 里的核心库。

本仓库不接受具体站点的代码，站源请做成独立的站源模块。

## 站源模块

站源是运行时加载的 JS 模块，写法见 DEVELOPMENT.md 的[站源模块](DEVELOPMENT.md#站源模块)，模板是 [guoapp-modules/template](https://github.com/guoapp-modules/template)。模块接口为第 1 版，不兼容的改动会升级 `api` 版本号。

官方发布包不带任何站源，不收录、不审核、不分发社区模块，社区模块与本项目无关，使用和分发的责任由其作者和使用者承担。

自行出包时请设置环境变量 `PACKAGE_NAME` 为自己的包名，避免覆盖安装官方版本。

## 模块接口的改动

需要宿主提供新的接口或能力时，欢迎直接提 PR 到本仓库，并在 `CHANGELOG.md` 写明。
