# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 仓库注意事项

- 更新版本前，检查项目 Github 该版本是否已经发布 Release，若未发布则继续在当前版本写。
- 更新版本要同时递增 `pubspec.yaml` 的版本号与构建号。
- 更新内容写在 `CHANGELOG.md` 。标题为完整版本号（如 `## 0.2.72+79`），每个版本的更新日志不得超过3500字。
- 每条更新日志只需简单描述，不需要具体的说明。
- 未验证的改动标为开发快照，提醒用户在对应平台进行实机测试，验证通过后，移除标记。
- 不新增解释性代码注释，必要的编译或工具指令除外。采用清晰命名和模块边界。
- 不为UI类内容编写单元测试，直接叫用户打开实际界面查看。
- 合并外部 PR 时，先用 `git merge --no-ff` 把 PR 原提交原样合进本地 `main`，合并提交只解决冲突（如 `CHANGELOG.md` 同版本标题去重），不改 PR 内容；需要的修订另起提交。不用 cherry-pick、squash 或 amend PR 提交，保证推送后 GitHub 将 PR 显示为 Merged。

## 站源

本体不内置站源，站源是运行时加载的 JS 模块（`native/core/module_*.go`，示例 `native/module_example`）。官方站源正从私有仓库 `guoapp-sources`（本机 `../guoapp-sources`）里的 Go 代码改写成 JS 模块，改写后的模块放在同级文件夹 `../guoapp-modules/<标识>/`（那里有自己的 `CLAUDE.md`）；`guoapp-sources` 只读、只作对照，不要改动，也不再编译。探测脚本 skill `masked-probe` 在 `guoapp-sources` 的 `.claude/skills/` 里。其中的硬性规定在此重申：允许直接访问的站源只有 `红果` `韩小圈` `鬼片` `青空`，其他站点（包括新增）不得以任何形式直接访问，也不得编写或运行会输出站点内容的测试，需要真实数据时写脚本交给用户在会话外运行。
