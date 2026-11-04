<p align="center">
  <img src="./docs/logo.svg" alt="lan disk" height="64">
</p>

<p align="center">
  <b>把一台 Windows 电脑变成局域网文件站：浏览器打开就能看、能下、能传</b>
</p>

<p align="center">
  <a href="#-快速开始">快速开始</a>
  &nbsp;·&nbsp;
  <a href="./architecture.md">架构文档</a>
  &nbsp;·&nbsp;
  <a href="./goal-and-requirements.md">需求与边界</a>
</p>

**lan disk** 是一个**单 exe、零安装、零外网依赖**的局域网 HTTP 文件服务器：Go 后端把本机目录通过浏览器共享给同网段的设备，Vue 3 前端提供文件管理，[FileViewer](https://doc.file-viewer.app/zh/) 负责在浏览器里直接预览 PDF / Word / Excel / PPT / 图片 / 音视频 / 压缩包 / CAD / 3D 模型——**解析和渲染全在浏览器里完成，服务端不做任何转码**。

功能面对标 [chfs](http://iscute.cn/chfs)（多共享目录、目录级写权限、上传、打包下载、访问日志、IP 过滤、限速），补上 chfs 最弱的一环：**文件点开就能看**，而不是只能下载到本地。

> **一个 69 MB 的 exe，就是全部。**
> 不用装数据库、不用装 .NET、不用装 LibreOffice、不用联外网——共享文件和 `landisk.db` 放在同一个文件夹里，拷走就能换机器。

![license](https://img.shields.io/badge/license-MIT-green)
![platform](https://img.shields.io/badge/platform-Windows%2010%2B-lightgrey)
![stack](https://img.shields.io/badge/Go%20%2B-Vue%203-blue)
![preview](https://img.shields.io/badge/preview-200%2B%20formats-orange)
![size](https://img.shields.io/badge/exe-69MB-brightgreen)

## 🎯 和 chfs / 网盘比

| 对比维度 | chfs / chfsgui | 网盘 | **lan disk** |
|---------|----------------|------|--------------|
| **部署** | 单 exe | 要联网、要上传 | **单 exe + Windows 服务，`install` 即用** |
| **在线预览** | 只有图片缩略图 | 要上传后才能看 | **PDF / Office / CAD / 3D / 音视频浏览器内直读** |
| **服务端转码** | — | 云端转码 | **零转码，Worker/WASM 全在同源静态资源里** |
| **离线可用** | 是 | 否 | **是，全部前端资源内嵌进 exe** |
| **权限模型** | 多账号 + 目录规则 | 账号体系 | **单管理员 + 匿名只读 / 可写目录，写操作全量可追溯** |
| **数据形态** | ini 配置 + 日志文件 | 云端 | **运行期只有 `lanDisk.exe` + `landisk.db`** |
| **回收站** | 有 | 有 | **有（按共享分仓，可恢复，保留期可配）** |
| **审计** | 有日志文件 | 有限 | **全量操作日志 + 按时间/IP/操作筛选 + CSV 导出 + IP→主机名映射** |

## ✨ 核心特性

### 📂 文件站该有的都有

- **多共享目录**：`D:\产品资料` ↔ `/产品资料`，`E:\图纸` ↔ `/图纸`，每个共享独立配权限
- **目录浏览**：文件图标、备注、大小、修改时间；表头点按排序；分页（默认每页 100，可选 50/200）
- **上传**：分片 + 断点续传（4 MiB 一块，服务重启也能续），支持**整个目录上传**（按相对路径自动建目录）
- **下载**：单文件支持 `Range`，目录一键打包 zip（流式，不落临时文件）
- **搜索**：当前目录递归按文件名，命中数/深度/超时三重上界，超限会如实告诉你"结果不完整"
- **备注**：管理员给任意文件/目录写一句话说明，全员可见；重命名、移动后备注跟着走

### 👀 预览才是重点

- **纯浏览器解析**：PDF、Word、Excel、PPT、OFD、图片、音视频、代码/文本、Markdown、压缩包、CAD（DWG/DXF）、3D 模型
- **零服务端成本**：没有 LibreOffice、没有临时文件、没有转换队列；Worker/WASM/字体全部同源托管，断网也能看
- **按需加载**：只在你点开对应类型时才拉取那一条预览链路
- **新标签页 + 全屏**：预览在自己的页面里铺满窗口，带「全屏」「下载」入口

### 🔐 权限与审计

- **单管理员**（登录名默认 `admin`，可改名）+ **匿名访问**：匿名只读全部共享，在管理员开了写权限的目录里可以上传/新建目录
- **删除、改名、移动、回收站、配置、备注**只有管理员能做；无权限目录对匿名访问者**完全不可见**（连直链返回 404）
- **回收站**：删除进共享内的 `.land-trash`，可恢复（同名冲突要求确认覆盖），保留期默认 90 天
- **全量日志**：浏览/下载/预览/上传/改名/移动/删除/登录/配置变更，带 IP 与反查主机名；可筛选、导出 CSV、保留 90 天
- **IP 白/黑名单**：deny 优先、allow 即白名单，被拒请求不写日志只计命中次数（后台可见），`127.0.0.1` 永远放行
- **限速与并发**：上下行 KB/s 与最大并发连接，后台改完即时生效

### 🪶 部署简单到不像话

- **Windows 服务**：`lanDisk.exe install` 注册 + 开机自启；`uninstall` 干净移除（不碰共享文件与数据库）
- **运行期只有两个文件**：`lanDisk.exe` 与 `landisk.db`（配置、账号、会话、备注、日志、回收站元数据全在里面）
- **免配置启动**：首启自动建管理员，口令用 `lanDisk.exe reset-password` 现取（不落任何口令文件）
- **登录长期有效**：关掉浏览器、隔天再来都不用重登，只有手动「退出登录」才失效

## 🚀 快速开始

### 环境要求

- Windows 10 / 11（服务模式需要管理员权限）
- 一个**可写目录**放 exe（例如 `D:\land\`，不要放 `Program Files`——`landisk.db` 就在它旁边）

### 部署三步

```powershell
# 1. 注册为 Windows 服务（管理员命令提示符）
lanDisk.exe install

# 2. 取管理员口令（不写任何文件，只打印到控制台）
lanDisk.exe reset-password

# 3. 浏览器打开 http://<本机局域网 IP>  →  登录  →  新建共享
```

默认端口 **80**（访问地址最短）。端口被占用时启动信息会直接报出占用进程名与 PID：

```powershell
lanDisk.exe config set port 8080     # 换端口，重启服务生效
```

### 常用命令

```powershell
lanDisk.exe                       # 前台调试运行（日志打到控制台，不注册服务）
lanDisk.exe install / uninstall   # 注册 / 移除 Windows 服务
lanDisk.exe reset-password        # 生成并打印新的管理员口令
lanDisk.exe config get|set KEY V  # 查看 / 修改配置（写进 landisk.db）
lanDisk.exe version               # 版本
```

### 数据与备份

| 路径 | 内容 |
|---|---|
| `landisk.db` | 配置、共享、权限规则、备注、账号、会话、上传会话、回收站元数据、日志、IP 规则、主机名 |
| `<共享根>/.land-trash/` | 该共享的回收站 |
| `<共享根>/<文件名>.landpart-<id>` | 上传中的分块（完成或过期自动消失） |

备份 = 停服务后拷贝 `landisk.db`（以及需要保留的共享目录）；升级 = 换 `lanDisk.exe`，数据库首次启动自动迁移。

## 🛠️ 从源码构建

```bash
# 1. 前端依赖与产物（bun 或 npm 均可）
cd web
bun install
bunx --no-install file-viewer-copy-assets ./public/file-viewer \
  --renderers office-word-openxml,spreadsheet-openxml,office-presentation,pdf,archive,cad,model
bun run build            # → web/dist（含 51 MB 自托管预览资源）

# 2. 后端（Go 1.25+）
cd ..
go test ./...
go build -trimpath -ldflags "-s -w -X main.version=1.0.0" -o lanDisk.exe ./cmd/land
```

> 网络受限时 Go 模块代理需要走镜像：`GOPROXY=https://goproxy.cn,direct go get <module>`
>
> 预览资源只复制实际用到的 renderer（51 MB）；全量是 191 MB。`web/public/file-viewer/` 不进仓库，按上面第二条命令生成。


### 发布（维护者）

发布由 GitHub Actions 在 **tag** 上自动完成（`.github/workflows/release.yml`）：

```bash
# 1. 先把本次发布说明放到 docs/release/vX.Y.Z.md（会作为 release body）
# 2. 打 tag 并推送，CI 会构建 Windows 二进制、复制预览资源、跑测试并发布
git tag -a v1.0.0 -m "lan disk 1.0.0"
git push origin v1.0.0
```

产物：`lanDisk-<version>-windows-amd64.zip`（含 exe + README + 第三方许可）与同名 `.sha256`。

## 🧱 技术栈

| 层 | 选择 | 说明 |
|---|---|---|
| 后端 | Go（stdlib `net/http`） | 单二进制，Windows 服务注册，流式文件 IO |
| 存储 | `modernc.org/sqlite`（CGO-free） | 单文件数据库，保住单命令交叉编译 |
| 前端 | Vue 3 + TypeScript + Vite + Element Plus | 构建产物 `go:embed` 进 exe |
| 图标 | Remix Icon | 随包自托管，无 CDN |
| 预览 | FileViewer（`@file-viewer/web` + standard/engineering preset） | 浏览器端渲染，Worker/WASM/字体同源 |
| 平台 | Windows 10+（其余平台可前台运行，不注册服务） | 服务以 LocalSystem 运行 |

## 📁 目录结构

```
cmd/land/            入口：子命令、服务托管
internal/app/        运行期装配：开库 → 读端口 → 起停 HTTP
internal/store/      SQLite：迁移与所有 SQL
internal/auth/       口令哈希、会话、登录节流
internal/perms/      权限判定（最长前缀、大小写折叠）
internal/files/      路径解析、列表、下载、zip、写操作
internal/upload/     分片上传与续传
internal/trash/      回收站
internal/logs/       异步审计日志
internal/httpapi/    HTTP 路由与中间件
internal/winsvc/     Windows 服务注册
web/                 Vue 3 前端（构建进 exe）
docs/                文档资源
```

## 📝 已知边界

- **挂载点登记未实现**：共享内指向其他卷的 Junction 会被安全策略拒绝（安全侧失败）。
- **IP 规则 CSV 导入/导出未实现**：目前只能逐条增删。
- **复杂线型（SHX）**：DWG 引用了未内嵌的 `ltypeshp.shx` 时，引擎按虚线/点线近似渲染，属图纸数据层面提示，不是预览失败。
- **Excel/PPT/CAD/3D** 已打通链路，但建议用自己的真实文件再验一遍。
- 完整清单见 [architecture.md#已知缺口](./architecture.md#已知缺口)。

## ❓ 常见问题

| 问题 | 处理 |
|---|---|
| 端口被占用 | 启动信息会给出占用进程名与 PID；或 `lanDisk.exe config set port 8080` |
| 忘记口令 | 到服务器本机执行 `lanDisk.exe reset-password`（没有远程找回，因为不落口令文件） |
| 服务起不来 | `eventvwr.msc` → Windows 日志 → 应用程序，来源 `LandDisk` |
| 把自己锁在门外 | 本机 `127.0.0.1` 永远放行；从本机登录后台改 IP 规则 / 并发上限 |
| 预览打不开 | 用 Chrome/Edge 近两个大版本；网络面板确认 `/file-viewer/` 下 Worker/WASM 为 200 |

## 🙏 致谢

本项目的功能面对标 [chfs / CuteHttpFileServer](http://iscute.cn/chfs)，预览能力来自 [FileViewer](https://github.com/flyfish-dev/file-viewer)。完整清单与许可见 **[THIRD-PARTY-NOTICES.md](./THIRD-PARTY-NOTICES.md)**：

| 类别 | 组件 |
|---|---|
| 后端 | Go 标准库、[modernc.org/sqlite](https://gitlab.com/cznic/sqlite)（CGO-free SQLite）、[golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto)（bcrypt）、[golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys)（Windows 服务） |
| 前端 | [Vue 3](https://vuejs.org/)、[Vue Router](https://router.vuejs.org/)、[Vite](https://vite.dev/)、[TypeScript](https://www.typescriptlang.org/)、[Element Plus](https://element-plus.org/)、[Remix Icon](https://remixicon.com/) |
| 预览 | [FileViewer](https://doc.file-viewer.app/)（Apache-2.0，含 PDF.js）、[libarchive](https://libarchive.org/) WASM（BSD-2-Clause）、[Comlink](https://github.com/GoogleChromeLabs/comlink) |
| CAD / DWF | [@flyfish-dev/cad-viewer](https://www.npmjs.com/package/@flyfish-dev/cad-viewer)（**AGPL-3.0**，内含 GPL-3.0 的 LibreDWG 与 AGPL-3.0 的 dwf-viewer）、[shx-parser](https://www.npmjs.com/package/@mlightcad/shx-parser)、[dxf-parser](https://www.npmjs.com/package/dxf-parser) |

> ⚠️ **许可证提示**：本项目自身代码是 **MIT**，但 CAD/DWF 预览链路依赖 **AGPL-3.0-only** 的 cad-viewer。**带 CAD 预览的构建产物在分发时受 AGPL-3.0 约束**（网络使用者有权获取对应源码）。若需要纯 MIT 分发，按上面的构建命令**去掉 `cad`、`model` 两个 renderer** 重新构建即可。


> **AGPL 对应源码**：带 CAD 预览的构建在提供网络服务时，使用者可向 <https://github.com/yonzL/lanDisk> 与 [THIRD-PARTY-NOTICES.md](./THIRD-PARTY-NOTICES.md#agpl-30-合规说明网络分发) 列出的上游仓库获取完整对应源码。

本项目代码以 [MIT License](./LICENSE) 发布 © 2026 yonzL；随包分发的第三方组件沿用各自许可，详见 [THIRD-PARTY-NOTICES.md](./THIRD-PARTY-NOTICES.md)。

