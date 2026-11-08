# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 仓库注意事项

- 更新版本前，检查项目 Github 该版本是否已经发布 Release，若未发布则继续在当前版本写。
- 更新版本要同时递增 `pubspec.yaml` 的版本号与构建号。
- 更新内容写在 `CHANGELOG.md` 。标题为完整版本号（如 `## 0.2.72+79`），每个版本的更新日志不得超过3500字。
- 未验证的改动标为开发快照，提醒用户在对应平台进行实机测试，验证通过后，移除标记。
- 不新增解释性代码注释，必要的编译或工具指令除外。采用清晰命名和模块边界。
- 不为UI类内容编写单元测试，直接叫用户打开实际界面查看。
- 合并外部 PR 时，先用 `git merge --no-ff` 把 PR 原提交原样合进本地 `main`，合并提交只解决冲突（如 `CHANGELOG.md` 同版本标题去重），不改 PR 内容；需要的修订另起提交。不用 cherry-pick、squash 或 amend PR 提交，保证推送后 GitHub 将 PR 显示为 Merged。
- 已知老问题：Windows 上跑 `native/core` 整套 Go 测试，红果、下载、目录缓存等启动引擎或后台写盘的测试会随机报 `TempDir RemoveAll cleanup: ... The directory is not empty`，与改动无关。判断是否由改动引起时，单独重跑失败的测试，或撤掉改动对照。

## 站源开发约定

允许直接访问的站源为：`红果` `韩小圈` `鬼片` `青空`，其他站点（包括新增）遵循以下规定：

- 不得通过任何形式访问站源，直接连接到站源的命令应由用户在会话外运行，并由用户审查后返回到对话中。
- 不得编写或运行可以输出内容的测试，验证方法同上。
- 给用户的连通性测试脚本写在 scratchpad，回复中只给一行执行命令，不在回复里贴多行脚本，脚本形式不限，可以是 `.ps1(UTF-8 with BOM)`、`.py` 等。
- 脚本输出只打印到用户控制台，不写入任何文件，也不通过 `!` 前缀等方式回到会话；请求体等中间数据走 stdin，不落临时文件。
- 脚本在用户控制台的输出没有限制，提醒用户检查内容后再贴回。
- 编写探测脚本时使用项目 skill `masked-probe`（`.claude/skills/masked-probe/`），用其中的遮罩脚本给输出打码，减少用户审查的工作量。
