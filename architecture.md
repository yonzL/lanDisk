---
id: lan-disk-arch
type: architecture-design
status: draft
title: lan disk 架构
references: [lan-disk-goal]
tags: [go, vue3, sqlite, embed]
---

# lan disk 架构

单进程、单二进制、零外部依赖：一个 Go 程序同时是 HTTP 服务器、Windows 服务，以及内嵌 SPA 的宿主。产品口径见 `lan-disk-goal`，这里只记拓扑与跨模块决策。

## 拓扑

```
                    ┌──────────────── lanDisk.exe (LocalSystem) ────────────────┐
浏览器 ── HTTP ──▶  │ 中间件链 ─▶ httpapi ─▶ files / upload / trash / admin  │
(匿名 / admin)      │              │              │                          │
                    │              └──▶ store (SQLite: landisk.db)              │
                    │  go:embed: web/dist + FileViewer 资源                  │
                    └───────────────────────────┬────────────────────────────┘
                                                │
                      共享目录（多个，可跨卷）─┴─▶ 各共享根内的 .land-trash / .landpart
```

一个进程内三件事：HTTP 服务、服务托管（Windows SCM）、SQLite 访问。没有后台守护进程、没有反向代理、没有外部队列；异步只有两处——上传分块落盘与日志批量写入。

## 运行期文件

**程序目录里运行期只有两个文件：`lanDisk.exe` 与 `landisk.db`。** 没有 `config.json`、没有口令文件、没有 temp 目录、没有日志副本。

| 位置 | 文件 | 说明 |
|---|---|---|
| exe 同级 | `lanDisk.exe` | 程序本体（内嵌前端与预览资源） |
| exe 同级 | `landisk.db` | **唯一真源**：配置、共享、规则、备注、账号、会话、上传会话、回收站元数据、日志、IP 规则、主机名映射，全部在这里 |
| 共享根内 | `<共享根>/.land-trash/` | 该共享的回收站（必须在同一卷才 `rename` 得动） |
| 共享根内 | `<目标名>.landpart-<uploadID>` · `<目标名>.landrestore-<id>` | 上传分块与跨卷恢复临时文件，写完即改名，列表与搜索中不可见 |

上传结束（或会话过期清理）后这两个共享根下的临时产物都会消失，程序目录始终只有 exe 与 db。日志 CSV 只在导出时由查询结果即时流给浏览器，不落盘。

部署约束：exe 必须放在服务账号（LocalSystem）可写的目录。放进 `Program Files` 会因 UAC 虚拟化/权限导致写库失败——这是便携形态的直接代价，写进部署文档。

## 启动顺序与引导

没有配置文件，于是启动顺序是固定的：

1. 定位 db（默认 exe 同级 `landisk.db`，`--db` 可覆盖，用于多实例调试）。
2. 打开 SQLite（WAL + `busy_timeout`），执行迁移，缺省 `settings` 行补齐。
3. 从 `settings.port` 读监听端口（默认 80）。

配置项全在 `settings` 表，有且只有两个写者：在线写 `PUT /api/admin/settings`，离线急救写 `lanDisk.exe config set`——两者走同一个 store 方法，不各写一套。**当配置把服务启动搞坏时（端口被占、IP 白名单把自己锁在外面），救援手段是本机命令行**，不靠改文件：

| 子命令 | 用途 |
|---|---|
| `lanDisk.exe`（无参） | 前台调试运行（不注册服务），日志直接打到控制台 |
| `lanDisk.exe install` / `uninstall` | 注册/注销 Windows 服务（LocalSystem，开机自启） |
| `lanDisk.exe reset-password` | 生成新管理员口令并打印到控制台、置 `must_change=1`；首启口令也这样拿 |
| `lanDisk.exe config get\|set <key> [value]` | 直接读写 `settings`，用于端口/白名单急救 |
| `lanDisk.exe version` | 版本与构建信息 |

首启没有 `users` 行时自动创建 admin + 随机口令 + `must_change=1`，控制台（服务模式下是事件日志）只提示"用 `lanDisk.exe reset-password` 获取口令"，不把口令写进任何文件。

## 组件与边界

| 包 | 职责 | 边界 |
|---|---|---|
| `cmd/land` | 子命令：前台运行、install/uninstall、reset-password、config、version | 只做参数解析与装配 |
| `internal/app` | 运行期装配与生命周期：开库 → 读端口 → 起停 HTTP | 控制台与服务模式唯一的共用入口，两条路径不得各自搭一套启动/关闭 |
| `internal/store` | `landisk.db` 打开、迁移、类型化查询（shares/mounts/rules/notes/users/sessions/uploads/trash/logs/ip_rules/hosts/settings） | 唯一会碰 SQL 的包；对外只暴露结构体与仓储方法 |
| `internal/auth` | 口令哈希、会话签发与校验、登录失败节流 | 依赖 store，不依赖 httpapi |
| `internal/perms` | 规则解析与判定：共享可见性、路径可读/可写、`.land-trash`/`.landpart` 屏蔽 | 只回答"能不能"，不碰文件系统也不写 SQL；`files`/`upload`/`trash` 共用同一判定结果类型 |
| `internal/files` | 虚拟路径 ↔ 磁盘路径解析（含挂载点展开）、目录列表、备注、下载、zip、写操作 | 不写 SQL；权限判定一律调 `perms`，副作用（日志）经事件回调 |
| `internal/upload` | 分片会话、隐藏分块文件、续传位图、完成改名 | 目标目录内就地写入；跨卷复制复用与回收站同一套实现 |
| `internal/trash` | 分仓、移入（rename / 跨卷复制）、恢复、清理 | 只管 `.land-trash` 内的移动与元数据复原 |
| `internal/logs` | 异步队列、批量写入、条件查询、CSV 导出、IP→主机名（查 `hosts`，未命中才反查**并只回写 dns 行**） | 旁路：任何 handler 都不得因日志失败而失败 |
| `internal/httpapi` | 路由、中间件链（IP 过滤、限速、访问日志）、请求/响应编解码 | 只做编排，不含文件系统逻辑 |
| `internal/winsvc` | SCM 注册/注销、服务循环 | 只在 Windows 构建 |
| `web/` | Vue3 + TS + Vite 源码 | 构建产物 `web/dist` 被 embed |

依赖方向单向：`httpapi → files|upload|trash|perms|auth|logs → store`；`files`/`upload`/`trash` 也都依赖 `perms`（判定唯一入口），`store` 不反向依赖任何业务包。

## HTTP 接口面

| 路径 | 用途 |
|---|---|
| `GET /api/health` | 存活探针（版本、共享数） |
| `POST /api/login` · `POST /api/logout` · `GET /api/session` · `POST /api/password` | 管理员会话 |
| `GET /api/shares` | 当前访问者可见的共享列表（无读权限的共享/目录不出现） |
| `GET /api/shares/{id}/list` | 目录列表：`path`、分页、`sort`、`order` |
| `GET /api/shares/{id}/search` | 当前路径递归按文件名搜索 |
| `GET /api/shares/{id}/thumb` | 图片缩略图（96 px，JPEG/PNG，`ETag` 条件请求；内存 LRU 缓存，不落盘） |
| `GET /api/shares/{id}/raw` | 下载（支持 `Range`） |
| `GET /api/shares/{id}/zip` | 目录流式打包 |
| `POST /api/uploads` · `PUT /api/uploads/{id}/{n}` · `GET /api/uploads/{id}` · `POST /api/uploads/{id}/complete` · `DELETE /api/uploads/{id}` | 分片上传与续传；`DELETE` 放弃会话并删掉隐藏分块文件 |
| `POST /api/shares/{id}/{mkdir,rename,move,delete}` | 写操作（匿名仅 `mkdir`，且需该目录写权限） |

后台（全部要求 admin 会话）：

| 路径 | 用途 |
|---|---|
| `GET|POST|PUT|DELETE /api/admin/shares[/{id}]` | 共享目录增删改查 |
| `GET|POST|PUT|DELETE /api/admin/shares/{id}/mounts[/{mid}]` | 共享内挂载点（Junction 目标）登记 |
| `GET /api/admin/fs/list?path=` · `POST /api/admin/fs/mkdir` | 服务端目录选择器（仅 admin）：空 path 返回盘符列表，否则只列子目录；mkdir 在弹窗内新建文件夹 |
| `GET|POST /api/admin/shares/{id}/rules` · `DELETE /api/admin/shares/{id}/rules?rel_path=…` | 子目录权限规则（`none` / `r` / `rw`）：POST 为 upsert，DELETE 按相对路径删（根规则不可删）——三层权限模型的唯一配置入口 |
| `GET|POST|PUT /api/admin/notes` | 备注读、写 |
| `GET /api/admin/logs` · `GET /api/admin/logs/export` | 日志条件查询与 CSV 导出 |
| `GET /api/admin/trash` · `POST /api/admin/trash/{id}/restore` · `POST /api/admin/trash/clear` | 回收站列表、恢复（`?force=1` 才允许覆盖已存在目标，否则 409）、清空 |
| `GET|POST|DELETE /api/admin/ip-rules` · `GET /api/admin/ip-rules/export` · `POST /api/admin/ip-rules/import` | IP 白/黑名单与其命中计数、CSV 导入导出 |
| `GET|POST|DELETE /api/admin/hosts` · `GET /api/admin/hosts/export` · `POST /api/admin/hosts/import` | IP → 主机名映射与 CSV 导入导出 |
| `GET|PUT /api/admin/settings` | 站点与运行参数（端口、保留期、限速、并发、标题/公告） |
| `/preview-assets/*` | FileViewer 的 Worker/WASM/字体，同源自托管 |

约定：错误统一 `{code, message}`；匿名与 admin 走同一套读取接口，差异只由权限表决定，不设两套端点。

## 中间件链

`IP 过滤 → 限速/并发 → 访问日志 → 会话识别 → 路由`

- **IP 过滤** 在最外层：规则来自 `ip_rules`，按 CIDR 匹配（单 IP 归一 `/32`），`deny` 优先于 `allow`，`allow` 非空即白名单模式，`127.0.0.1`/`::1` 始终放行。被拒请求返回 403 且**不入日志**（否则噪音淹没真实记录），但内存里累加该规则的 `hits`/`last_hit_at`，定时与优雅关闭时落库——后台因此能看到某条规则到底有没有生效。
- **限速** 按连接令牌桶 + 全局并发信号量；超限返回 429。
- **访问日志** 在响应后回收状态码与耗时，投递到 `logs` 的异步队列。
- **会话识别** 只解析 cookie 并查 sessions 表，不拦截——权限判定在 handler 内按操作类型做（读操作匿名即可）。

## 权限模型

三层判定，顺序不可交换：**共享可见性 → 路径可读性 → 路径可写性**。

- 规则按 `(share_id, rel_path)` 存，最长前缀匹配生效；子目录规则覆盖父级（chfs 的 rule 思路）。
- **“共享可见性”这一层的数据来源就是 `share_rules` 的根规则 `('', perm)`**：创建共享时自动写入根规则 `('', 'r')`（默认匿名可读、不可写），管理员要开写就把根规则改成 `rw` 或对子目录单独加规则。`shares` 表因此不需要额外的权限列；根规则缺失只可能来自手工改库，哨兵启动检查会补回。
- 匿名：可读规则 `r`/`rw` 的路径 + 该路径可写时的 `upload`/`mkdir`。
- admin：读全通；写操作（rename/move/delete/回收站/配置/备注）仅 admin。
- 无读权限的目录/共享对所有非 admin 完全不可见——列表、搜索、zip、单文件请求一律 404（不是 403，避免暴露存在性）。

## 路径安全不变量

所有磁盘访问必须经 `files.Resolve(share, relPath)`，它是唯一的路径出口：

1. 规范化 `relPath`（`..`、`.`、连续分隔符、Windows 短名、大小写差异）后再拼接共享根。
2. 静态结果必须仍在共享根内（按卷大小写不敏感比较）。
3. 再按真实路径校验：`EvalSymlinks` 后，要么仍在 `EvalSymlinks(share.root)` 之内，要么落在**该共享已登记的挂载点目标**（`share_mounts`）之内且 `relPath` 前缀与登记时一致。共享内的 Junction 因此是合法内容；指向共享之外、又没登记的目标一律 404。
4. 保留名（`CON`、`NUL`、`aux.txt` 等）与越界字符在创建类操作中直接拒绝。
5. `.land-trash` 是保留目录名；`.landpart-` 与 `.landrestore-` 是保留**文件名后缀**：任何路径段等于 `.land-trash`、或任意一段文件名包含子串 `.landpart-` / `.landrestore-`，对非 admin 一律 404——含 `raw`、`zip`、搜索命中与目录列表条目。判定在 `Resolve` 内（对单文件请求）与 `files` 的列表/搜索输出层（对枚举）同步做，两处都要，否则半成品块与半恢复文件能被匿名下载。
6. 用户提交的目标文件名（上传、新建、重命名）不得包含 `.landpart-` / `.landrestore-` / `.land-trash`，否则会伪造出“看不见的文件”或污染保留名空间，直接拒绝。

第 3 条同时是跨卷能力的前提：未登记的逃逸 junction 会被 `Resolve` 直接拒绝（安全的一侧失败）。后台的“发现未登记挂载点 → 一键登记”入口**本期没有实现**（见“已知缺口”），因此跨卷回收站/恢复分支在真实部署里目前不可达，只保留在 `internal/trash` 的复制回退里。

绕过 `Resolve` 直接拼路径视为缺陷，不靠 code review 兜底——这条规则写在包注释里，并作为 `files` 包的测试基线。

## 存储模型（landisk.db）

```sql
settings(key TEXT PRIMARY KEY, value TEXT)
shares(id INTEGER PK, name TEXT, virtual_path TEXT UNIQUE, root TEXT, sort INT)
share_mounts(id INTEGER PK, share_id INT, rel_path TEXT, target TEXT, added_at INT)   -- 共享内 Junction 目标
share_rules(share_id INT, rel_path TEXT, perm TEXT /*none|r|rw*/, PRIMARY KEY(share_id, rel_path))
notes(share_id INT, rel_path TEXT, note TEXT, updated_at INT, PRIMARY KEY(share_id, rel_path))
users(id INTEGER PK, username TEXT UNIQUE, pass_hash TEXT, must_change INT)
sessions(token TEXT PK, user_id INT, created_at INT, last_seen INT)
uploads(id TEXT PK, share_id INT, rel_path TEXT, size INT, chunk INT, bitmap BLOB, part_path TEXT, created_at INT)
trash(id INTEGER PK, share_id INT, orig_rel_path TEXT, trash_rel_path TEXT, deleted_at INT, ip TEXT)
ip_rules(id INTEGER PK, cidr TEXT UNIQUE, kind TEXT /*allow|deny*/, note TEXT, hits INT, last_hit_at INT, created_at INT)
hosts(ip TEXT PK, hostname TEXT, source TEXT /*manual|dns*/, note TEXT, updated_at INT)
logs(id INTEGER PK, ts INT, ip TEXT, host TEXT, action TEXT, share_id INT, path TEXT, result TEXT, detail TEXT)
```

- `settings` 持有全部可运营参数（`port`、`log_retention_days`、`trash_retention_days`、限速值、并发上限、站点标题/公告）。**每个参数只有这一个住所**；回收站保留期与日志保留期是两个独立键，互不影响。
- `hosts.source` 区分人工与反查：反查只 upsert `source='dns'` 的行，永不覆盖 `manual`——管理员写的"192.168.1.31 = 张三的笔记本"不会被后台解析任务改掉。
- `logs.host` 是写入时刻的快照，**查询与 CSV 导出都按 `ip` join `hosts` 取当前映射**，因此今天补的映射立刻对昨天的记录生效；`logs.host` 只在 `hosts` 无该 IP 时兜底。
- 索引：`logs(ts)`、`logs(ip, ts)`、`logs(action, ts)`。日志按天分批删除：`DELETE FROM logs WHERE ts < now - settings.log_retention_days * 86400`，保留天数只从 `settings` 读，语句里不出现硬编码天数。
- `sessions`、`uploads` 各自带过期清理；`uploads` 过期时连同其 `.landpart-*` 文件一起删。文件已经不存在时只删行（与回收站同一套“先动文件、再动行、缺失即回收”的自愈顺序，见“实现时定的细节”）。

驱动选 `modernc.org/sqlite`（CGO-free）：保持 `go build` 单命令可交叉编译，代价是比 cgo 版略慢，对 20 台设备的写入量完全够用。

## 关键流程

**下载与 zip**：`http.ServeContent` 提供 Range/If-Modified-Since；zip 用 `archive/zip` 直接写响应流，边遍历边写，不落临时文件，单文件读取有并发上限。

**分片上传**：`POST /api/uploads` 建会话，在目标目录创建隐藏分块文件 `<目标名>.landpart-<uploadID>`（预分配到最终大小）并返回 `id` 与已收块位图 → 客户端按块 `PUT`，服务端按块偏移写入（不额外占空间、天然可续传）→ `complete` 时校验位图完整、`Fsync` 后 `rename` 为最终文件名（同名覆盖语义），删 DB 会话行；最终文件名含 `.landpart-`/`.land-trash` 的直接拒绝。位图与会话行持久化（`last_seen` 记最近一次写入，过期按它而非创建时间判断），服务重启后 `GET` 即可续传；`DELETE` 放弃会话并删分块文件，过期会话由清理任务同样处理，删除共享时也会先删它的分块文件。缺块完成返回 400 与缺块数量；`Fsync`/`rename` 失败只记日志并返回通用 500，不把服务器路径回给客户端。

**备注跟随**：`rename`/`move` 在同一事务里把 `notes` 的 `(share_id, rel_path)` 一并迁移（目录移动则迁移其前缀子树）。程序外的改名不做探测——这是记录在 `lan-disk-goal` 已知边界里的取舍。

**回收站**：删除按卷归属分两条路径。同卷——`rename` 到 `<共享根>/.land-trash/<id>_<原名>`；跨卷（已登记的挂载点）——`rename` 以 `ERROR_NOT_SAME_DEVICE` 失败，改走流式复制、校验大小一致后才删源，中途失败保留源文件并返回明确错误，绝不半删。两条路径都写 `trash` 行并在日志里区分 `trash_move` / `trash_copy`。

恢复同样两条路径：跨卷时先复制到目标路径同目录的隐藏文件 `<目标名>.landrestore-<id>`、校验大小、再 `rename` 到最终名；该隐藏文件与 `.landpart-` 同享保留名屏蔽（不变量 5），失败或进程中断后由下次恢复/清理任务删除。任何一步失败保留 `trash` 行与回收站内的原文件、返回错误，绝不在目标路径留下截断文件。

恢复的覆盖确认是**服务端契约**：`POST /api/admin/trash/{id}/restore` 默认在目标已存在时返回 409（`{code:"target_exists"}`，附冲突路径），前端弹确认后带 `?force=1` 重试；不带 `force` 的重试或脚本调用永远不会静默覆盖。

清理按 `settings.trash_retention_days`（默认 90，与日志保留期独立）扫描：`.land-trash` 由 `Resolve` 对非 admin 硬屏蔽。

**日志**：内存通道（容量满则丢弃并计数，绝不阻塞请求）→ 单写入协程批量 `INSERT`。IP→主机名在写入侧查 `hosts`，未命中才异步反查并只回写 `dns` 行，失败留空。

## 前端结构

单 SPA，两个面：**浏览面**（匿名可用）与**管理面**（需 admin）。

- 路由：`/` 虚拟根（把共享目录当文件夹列出来，没有独立首页）、`/s/:share/*path` 共享内浏览、`/s/:share/preview/*path` 预览（懒加载预览链路）、`/login`、`/admin/*`。
- 面包屑从虚拟根开始（根目录 / 共享名 / 子目录…），共享名本身可点回到该共享根，根目录可点回到 `/`。
- 技术：Vue 3 + TypeScript + Vite + Element Plus（表单/表格/弹窗直接拿现成件）；文件列表列序固定（文件名 / 备注 / 操作 / 大小 / 修改时间），列宽用 `min-width` 比例自适应；列拖拽重排已移除（此前实现不可靠），每页条数仍存 `localStorage`。
- 浏览面视觉对齐 chfs 参考：工具栏（上传文件 / 上传目录 / 新建目录 / 页内搜索 / 打包下载）+ 灰色面包屑条 + 行末图标按钮（预览/下载/重命名/移动/删除）+ 页脚版本行；表头点按排序保留。
- 目录选择：`components/DirPickerDialog.vue` 一套组件两种模式——`machine`（调 `/api/admin/fs/*`，整机盘符 → 目录，仅 admin）用于新建共享；`share`（复用 `/api/shares/{id}/list`）用于移动目标，范围锁在当前共享。
- 目录上传：隐藏的 `webkitdirectory` 输入，按 `webkitRelativePath` 用 mkdir（409 视为已存在）逐层建目录后再分片上传。
- 预览：FileViewer 按需组合的包（`preset-standard` + `preset-engineering`），只在进入 `/preview` 路由时动态 import；Worker/WASM/字体走本地 `/file-viewer/*`，禁一切外网请求。
  - **CAD 与压缩包的 Worker 必须显式指定**：`options.cad = { workerUrl, wasmPath, dwfWasmUrl }`、`options.archive = { workerUrl }`（libarchive 的 bundle 用 emscripten `locateFile`，传 `wasmUrl` 反而会因补丁不匹配而破坏初始化）。
  - 预览页在**新标签页**打开（`window.open` + 清空 opener，被拦截时同页回退），走 `land-main--full` 全宽布局，页面内提供「全屏」（Fullscreen API）与下载。
  - 图标统一用 Remix Icon（随包自托管，无 CDN）。
- 状态：轻量 composable + `fetch` 封装，不引入重状态库。
- 状态与刷新：登录/退出后 `session` 是响应式的（页头、管理员菜单、按钮即时变化），浏览页 `watch(session.authenticated)` 会重新拉取目录——因为可写目录、备注入口、隐藏共享都是**服务端按访问者算的**，只靠前端状态会让页面停留在上一个身份的数据上。

## 构建与打包

```
bun install && bun run build   → web/dist
bun run build:preview          → preview-assets/（FileViewer 自托管资源）
go build -trimpath -ldflags "-s -w -X main.version=..."  → lanDisk.exe
```

`web/dist` 与 `preview-assets` 通过 `go:embed` 内嵌；embed 目录需在构建前存在（放一个占位文件保证没跑前端构建时 `go build` 也不炸）。`-trimpath` + 版本号注入让产物可复现。目标平台 `windows/amd64`（`arm64` 顺带可编译），其余平台允许前台模式运行但不注册服务。

## 实现时定的细节

以下三点不在文档阶段定稿，由实现与测试确定（不变更本文件的边界与拓扑）：

- **`settings` 的双写者**：后台接口与 `lanDisk.exe config set` 都写 `settings`（CLI 是离线急救通道），实现时两者走同一个 store 方法；服务在线时 CLI 写入后提示“重启生效”，不做热加载——避免文档里“唯一写入方”与急救命令互相矛盾。
- **挂载点发现与主机名刷新**：`share_mounts` 的“未登记候选”扫描（遍历共享内 reparse point）与 `POST /api/admin/hosts/refresh`（批量反查 DNS）属于便利功能，接口命名与触发时机在写该模块时定。
- **回收站与上传清理的失败顺序**：NTFS 删除与 SQLite 事务无法真正同事务，顺序定为“先动文件、再动行”，并在下次扫描/恢复时遇到缺失文件则回收该行并报告跳过；真实事务边界由集成测试钉死。

## 平台约束

- Windows 服务以 LocalSystem 运行：可读本机任意共享目录，也可写 db 所在目录；`install` 时若 exe 所在目录不可写直接报错并给出提示。
- 端口来自 `settings.port`（默认 80）：可能被 IIS / 其他服务占用，启动失败必须输出"端口被占用 + 当前占用进程名"并退出，不允许静默；急救用 `lanDisk.exe config set port 8080`。
- 无外网依赖：所有前端资源（含字体、WASM）随 exe 分发。

## 实现状态

首版已落地的能力（与上面的设计一致，差异见"已知缺口"）：

| 领域 | 状态 |
|---|---|
| 部署形态 | 单 exe、`install`/`uninstall` 注册 Windows 服务（未提权时明确报错）、`reset-password`、`config get/set` |
| 账号与会话 | 首启建 admin（随机口令不落盘）、登录名“用户名 + 口令”（用户名可在改口令页一并改名，默认 admin）、登录节流、**登录长期有效直到手动退出**（持久 cookie，服务端不按空闲时间清会话）、**首次登录设置新口令时免输当前口令**（会话刚由该口令建立）、改凭据即踢所有会话、`must_change` 服务端强制 |
| 共享与权限 | 多共享 + 虚拟路径、`share_rules` 前缀覆盖、deny-by-default、大小写折叠、匿名仅读 + 可写目录内上传/建目录 |
| 浏览 | 固定列序（名称/备注/操作/大小/修改时间）、按比例自适应列宽、单列排序、分页、隐藏保留名；工具栏 + 面包屑 + 行末图标按钮 + 页脚（对齐 chfs 参考图） |
| 目录选择 | 弹窗浏览服务端整机（盘符 → 目录，仅目录，可内建文件夹）用于新建共享；同一组件用共享内模式选移动目标 |
| 备注 | admin 编辑、全员可见、随重命名/移动迁移（同事务） |
| 下载 | 单文件（Range）+ 目录 zip 流式打包 |
| 上传 | 分片 + 断点续传（分块行记录、`last_seen` 过期、放弃接口）；整目录上传按相对路径自动建子目录 |
| 搜索 | 当前目录递归按文件名，命中数/深度/超时三重上界且如实上报 |
| 删除 | 进回收站（按共享分仓）、恢复（409/`force`）、保留期清理、清空 |
| 预览 | FileViewer `@file-viewer/web` + `preset-standard` + `preset-engineering`，资源同源自托管；PDF/Word/文本/图片/压缩包/CAD（DWG）已在真实浏览器验证；新标签页 + 全屏 + 全宽布局 |
| 治理 | 全量审计日志（路由模式自动归类）、按时间/IP/操作筛选、CSV 导出、主机名手工映射 + 异步 DNS 回写、IP 白/黑名单（含命中计数）、并发上限、下载/上传限速、站点标题/公告 |

## 已知缺口

- **挂载点（`share_mounts`）没有入口**：表与 `Resolve` 的规则已存在，但后台既没有"登记 junction"的接口也没有候选扫描；因此共享内指向其他卷的 junction 目前会被安全不变量拒绝（安全的一侧失败），跨卷回收站分支因而不可达。
- **IP 规则的 CSV 导入/导出未实现**（架构里列了接口）：目前只能逐条增删；主机名的导入/导出已实现。
- **日志动作含 `upload_chunk`**：每个 4 MiB 分块一条记录。真实配置里如需降噪，可在 `actionFor` 里合并，但这会削弱"全量记录"的可核对性，暂按全量保留。
- **复杂线型（SHX）**：DWG 引用了未内嵌的外部形状字体时（如 `ltypeshp.shx`），CAD 引擎的警告是**图纸数据层面的**，不是预览失败——引擎按虚线/点线近似渲染。要完全还原需要该 SHX 字体并接入 `shxGlyphResolver`，本期不做。
- **Windows 服务未在提权环境实机验收**：`install`/`uninstall` 的 SCM 路径只做了代码与未提权报错验证。

## 预览资源路径（1.0.3 起）

预览的运行时资源**全部显式指派绝对路径**，不再依赖“全局资产基地址 + 文档地址”的隐式拼装：

| 链路 | 显式选项 |
|---|---|
| PDF | `pdf.assetBaseUrl` / `workerUrl` / `cMapUrl` / `wasmUrl` / `standardFontDataUrl` |
| Word | `docx.workerUrl` / `workerJsZipUrl` |
| PPT / Excel | `presentation.workerUrl` / `spreadsheet.workerUrl` |
| 压缩包 | `archive.workerUrl`（wasm 由 bundle 自行按自身位置解析） |
| CAD | `cad.workerUrl` / `wasmPath` / `dwfWasmUrl` |
| 3D 模型 | `model.workerUrl` / `runtimeUrl` / `wasmUrl` |

背景：现场部署里 PDF worker 曾被解析成站根 `/vendor/pdf/pdf.worker.mjs`，而该路径被 SPA 回落接住返回了 `index.html`，浏览器把 HTML 当 ES module 导入，报 `Setting up fake worker failed` / `Failed to fetch dynamically imported module`。两点收口：

1. 路径由代码写死（`/file-viewer/...`），与文档地址、反代前缀无关；
2. `webui.isStaticSurface` 把 `/vendor/`、`/wasm/` 也当作静态面——丢前缀的请求返回 404，不再被 SPA 的 HTML 掩盖。

## 预览文件名中的 `#` / `?`（1.1.2 起）

预览器把 `name` 当 URL 解析：`normalizeFilename()` 会按 `#` / `?` 截断，截断后为空则回落到 `preview.bin`，于是 `#知识分享官#…-第2讲.mp4` 这类文件名被当成 `.bin`，界面上会显示"未装配 Binary Inspector renderer"。

处理方式：`PreviewView` 在把文件名交给预览器之前，把 `#`、`?` 百分号编码（`escapeViewerName`）。预览器随后 `decodeURIComponent` 还原显示名，扩展名也从编码后的字符串里正确取到。回归检查脚本：`web/scripts/check-viewer-name.mjs`（直接对 `@file-viewer/core` 的 `normalizeFilename` / `getExtension` 断言）。


## 缩略图（1.2.0 起，1.2.1 改为磁盘缓存）

图片在列表里显示 36×36 缩略图，服务端按需生成：

- **端点** `GET /api/shares/{id}/thumb?path=`：先过 `perms` 判读权限、`Resolve` 校验路径，再按扩展名白名单（jpg/jpeg/png/gif/bmp/webp）解码；非图片返回 404（前端回退图标）。
- **渲染**：`golang.org/x/image` 解码（webp/bmp 走 x/image，其余走标准库），`CatmullRom` 缩放到 96 px 长边（不放大）；含透明通道输出 PNG，否则 JPEG q82。
- **缓存（1.2.1）**：**只落磁盘**，位于系统临时目录下的 `lanDisk-thumbs/`（`%TEMP%`，服务运行时是服务账号的 temp），文件名 = `sha256(绝对路径|mtime|size|px)`；写入用临时文件 + rename 保证原子。**进程内不再持有缩略图数据**，避免堆内存随图片目录增长。
  - 自清理：启动时与每 64 次写入扫一次，删除超过 7 天的条目；目录超过 512 MB 时按最旧优先删到限额以下。系统清理临时目录也只会让缓存失效一次。
  - 可用 `LAND_THUMB_CACHE` 指定目录（测试用）。
- **并发**：解码经 4 个信号量槽限流；同一键的并发请求共享一次渲染。
- **HTTP**：`ETag` + `Cache-Control: private, max-age=3600`，`If-None-Match` 命中返回 304。

## 已知缺口

- **挂载点（`share_mounts`）没有入口**：表与 `Resolve` 的规则已存在，但后台既没有"登记 junction"的接口也没有候选扫描；因此共享内指向其他卷的 junction 目前会被安全不变量拒绝（安全的一侧失败），跨卷回收站分支因而不可达。
- **IP 规则的 CSV 导入/导出未实现**（架构里列了接口）：目前只能逐条增删；主机名的导入/导出已实现。
- **日志动作含 `upload_chunk`**：每个 4 MiB 分块一条记录。真实配置里如需降噪，可在 `actionFor` 里合并，但这会削弱"全量记录"的可核对性，暂按全量保留。
- **复杂线型（SHX）**：DWG 引用了未内嵌的外部形状字体时（如 `ltypeshp.shx`），CAD 引擎的警告是**图纸数据层面的**，不是预览失败——引擎按虚线/点线近似渲染。要完全还原需要该 SHX 字体并接入 `shxGlyphResolver`，本期不做。
- **Windows 服务未在提权环境实机验收**：`install`/`uninstall` 的 SCM 路径只做了代码与未提权报错验证。

## 预览资源路径（1.0.3 起）

预览的运行时资源**全部显式指派绝对路径**，不再依赖“全局资产基地址 + 文档地址”的隐式拼装：

| 链路 | 显式选项 |
|---|---|
| PDF | `pdf.assetBaseUrl` / `workerUrl` / `cMapUrl` / `wasmUrl` / `standardFontDataUrl` |
| Word | `docx.workerUrl` / `workerJsZipUrl` |
| PPT / Excel | `presentation.workerUrl` / `spreadsheet.workerUrl` |
| 压缩包 | `archive.workerUrl`（wasm 由 bundle 自行按自身位置解析） |
| CAD | `cad.workerUrl` / `wasmPath` / `dwfWasmUrl` |
| 3D 模型 | `model.workerUrl` / `runtimeUrl` / `wasmUrl` |

背景：现场部署里 PDF worker 曾被解析成站根 `/vendor/pdf/pdf.worker.mjs`，而该路径被 SPA 回落接住返回了 `index.html`，浏览器把 HTML 当 ES module 导入，报 `Setting up fake worker failed` / `Failed to fetch dynamically imported module`。两点收口：

1. 路径由代码写死（`/file-viewer/...`），与文档地址、反代前缀无关；
2. `webui.isStaticSurface` 把 `/vendor/`、`/wasm/` 也当作静态面——丢前缀的请求返回 404，不再被 SPA 的 HTML 掩盖。

## 预览文件名中的 `#` / `?`（1.1.2 起）

预览器把 `name` 当 URL 解析：`normalizeFilename()` 会按 `#` / `?` 截断，截断后为空则回落到 `preview.bin`，于是 `#知识分享官#…-第2讲.mp4` 这类文件名被当成 `.bin`，界面上会显示"未装配 Binary Inspector renderer"。

处理方式：`PreviewView` 在把文件名交给预览器之前，把 `#`、`?` 百分号编码（`escapeViewerName`）。预览器随后 `decodeURIComponent` 还原显示名，扩展名也从编码后的字符串里正确取到。回归检查脚本：`web/scripts/check-viewer-name.mjs`（直接对 `@file-viewer/core` 的 `normalizeFilename` / `getExtension` 断言）。

## 缩略图（1.2.0 起）

图片在列表里显示 36×36 缩略图，服务端按需生成：

- **端点** `GET /api/shares/{id}/thumb?path=`：先过 `perms` 判读权限、`Resolve` 校验路径，再按扩展名白名单（jpg/jpeg/png/gif/bmp/webp）解码；非图片返回 404（前端回退图标）。
- **渲染**：`golang.org/x/image` 解码（webp/bmp 走 x/image，其余走标准库），`CatmullRom` 缩放到 96 px 长边（不放大）；含透明通道输出 PNG，否则 JPEG q82。
- **缓存**：进程内 LRU（最多 512 条 / 64 MB），键为 `(绝对路径, mtime, size, px)`——文件一改就是新键，旧条目自然淘汰；不落盘，保持"运行期只有 exe + db"。
- **并发**：解码经 4 个信号量槽限流，滚动大图目录不会打满 CPU；同一键的并发请求共享一次渲染。
- **HTTP**：`ETag` + `Cache-Control: private, max-age=3600`，`If-None-Match` 命中返回 304。

## 媒体播放（1.2.4 起）

- **视频/音频不走 FileViewer**：media renderer 的接口是 `render(buffer, …)`——文件会先被整包读进内存，2 GB 视频等于先下载完再播。所以 `PreviewView` 对 mp4/m4v/webm/ogv/mov/mkv 与 mp3/wav/m4a/aac/flac/ogg/opus 直接交给原生 `<video>`/`<audio>`，`src` 指向流式端点。
- **流式端点** `GET /api/shares/{id}/content?path=`：`Content-Type` 按扩展名（video/mp4、video/webm…），`Content-Disposition: inline`、`X-Content-Type-Options: nosniff`，由 `http.ServeContent` 处理 Range/If-Range（`206` + `Content-Range` + `Accept-Ranges`）。`/raw` 保持 `attachment`，只用于下载按钮。
- **不可解码格式**：WMV/WMA、ASF、AVI、RMVB、FLV、MPEG/VOB/TS、3GP、DivX 等浏览器内核根本不支持，页面直接显示「浏览器无法播放该格式」+ 下载入口；能播放的容器里编码不被支持（H.265 等）时由 `<video @error>` 触发提示。
