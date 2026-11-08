---
name: masked-probe
description: 给受限站源（红果、韩小圈、鬼片、青空以外的站点，包括新增站点）编写由用户在会话外运行的连通性或接口探测脚本，并用遮罩层把输出里的中日韩文字打码。需要探测站点结构、域名、接口、加密或封面格式，或要用户运行脚本并贴回输出时使用。
---

# 受限站源探测脚本

CLAUDE.md「站源开发约定」是硬性规定：不得自己访问受限站源，也不得编写或运行会输出站点内容的测试。需要站点的真实数据时，写脚本交给用户在会话外运行，用户审查输出后再贴回对话。

本目录有两个遮罩脚本：

- `mask_cjk.py`：统一的打码层，把汉字、假名、韩文、全角标点等中日韩字符逐字替换为 `*`；ASCII 的 ID、URL、JSON 结构和代码原样保留。
  - 管道过滤：`<命令> | python .claude/skills/masked-probe/mask_cjk.py`，逐行输出。
  - 在 Python 脚本内使用：`from mask_cjk import install` 后调用 `install()`，给自身的标准输出和标准错误打码。
- `run_masked.py`：包装运行 Python 脚本，标准输出和标准错误逐行打码后实时输出，并原样返回子脚本的退出码。子进程强制使用 UTF-8、关闭输出缓冲，`PYTHONPATH` 里加入本目录，所以被包装的脚本可以直接 `import mask_cjk`。

## 流程

1. 脚本写到会话 scratchpad（系统提示里的 scratchpad 绝对路径），不要写进仓库。
2. 交付前只做本地语法检查：`python -m py_compile <脚本>`，不要自己运行。
3. 回复里只给一行执行命令，不贴多行脚本，在仓库根目录执行：
   `python .claude/skills/masked-probe/run_masked.py <scratchpad 绝对路径>\probe_xxx.py`
4. 提醒用户：中文已经打码，其他内容仍要检查后再贴回。
5. 根据贴回的输出改代码；需要再探测就迭代脚本，不要自己发请求验证。

## 脚本要求

- 输出只打印到控制台，不写文件，也不通过 `!` 前缀回到会话。请求体等中间数据走 stdin，不落临时文件。
- 只用标准库（`urllib`、`json`、`re`、`gzip`、`zlib`、`base64`），避免用户还要安装依赖；确实需要第三方库时（如 AES 解密用 `pycryptodome`），在回复里说明。
- 每个请求设置超时（8 到 20 秒），异常要捕获并打印类型和信息，不要让一个失败中断整个脚本。
- 循环探测时限制数量（域名、分页、详情 ID），避免对站点发出大量请求。
- 不用 `.py` 时也可以写 `.ps1`（UTF-8 with BOM），输出通过管道交给 `mask_cjk.py`。

## 踩过的坑

- **压缩**：有的站点即使请求里没有 `Accept-Encoding` 也返回 gzip，`urllib` 不会自动解压，读到的是二进制，看起来像"页面里什么都没有"。请求时带上 `Accept-Encoding: gzip, deflate`，按 `Content-Encoding` 或 `1f8b` 文件头解压。Go 侧 `http.Transport` 会自动处理，只要不手动设置 `Accept-Encoding`。
- **重定向**：发布页、导航页常用 301 跳到会更换的中转域名。用自定义 `HTTPRedirectHandler` 记录完整的跳转链，并打印最终 URL。
- **长字符串**：base64、密文、token 打印前截断（例如超过 120 个字符替换成 `<N chars>`），避免刷屏，也减少用户审查的工作量。
- **结构先于内容**：优先打印状态码、Content-Type、关键响应头、变量名、JSON 键名和数组长度，确认结构后再按需打印少量样本值。
- **判断接口是否可用**：以能否解析出接口自己的 JSON 外壳（如 `{"code":...}`）为准，不要只看 HTTP 状态码；停放页、拦截页常返回 200 的 HTML。
