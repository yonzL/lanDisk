# 第三方组件与许可（Third-Party Notices）

lan disk 自身代码以 [MIT](./LICENSE) 发布。本文件列出随项目一起分发的第三方组件及其许可，供再分发、二次开发与合规审查使用。

> **重要**：CAD/DWF 预览链路依赖 `@flyfish-dev/cad-viewer`，该包是 **AGPL-3.0-only**（内部包含 GPL-3.0 的 LibreDWG 与 AGPL-3.0 的 DWF 渲染器）。因此**包含 CAD 预览资源的构建产物在分发时受 AGPL-3.0 约束**：通过网络使用该服务的用户有权获得相应源代码。如果你只需要纯 MIT 的分发，请按 README「从源码构建」的说明，去掉 `cad`、`model` 两个 renderer 后重新构建。

## 后端（Go）

| 组件 | 版本 | 许可 | 用途 |
|---|---|---|---|
| [Go 标准库](https://go.dev/) | 1.25+ | BSD-3-Clause | HTTP 服务、文件 IO、archive/zip |
| [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) | v1.6x | BSD-3-Clause | CGO-free 的 SQLite 驱动 |
| [modernc.org/libc](https://gitlab.com/cznic/libc) | v1.7x | BSD-3-Clause | 上述驱动的运行时 |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto) | v0.5x | BSD-3-Clause | bcrypt 口令哈希 |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | v0.4x | BSD-3-Clause | Windows SCM / 事件日志 |

## 前端（Vue 3）

| 组件 | 版本 | 许可 | 用途 |
|---|---|---|---|
| [Vue 3](https://vuejs.org/) | 3.5 | MIT | 响应式 UI |
| [Vue Router](https://router.vuejs.org/) | 4.x | MIT | 前端路由 |
| [Vite](https://vite.dev/) | 6.x | MIT | 构建与开发服务器 |
| [TypeScript](https://www.typescriptlang.org/) | 5.x | Apache-2.0 | 类型检查 |
| [Element Plus](https://element-plus.org/) | 2.14 | MIT | 表格、表单、弹窗等基础组件 |
| [Remix Icon](https://remixicon.com/) | 4.9 | Apache-2.0 | 文件类型与操作图标（自托管） |
| [Bun](https://bun.sh/) | 1.4 | MIT | 前端包管理与构建（仅开发期） |

## 在线预览（FileViewer）

| 组件 | 版本 | 许可 | 用途 |
|---|---|---|---|
| [@file-viewer/web](https://doc.file-viewer.app/) | 3.3.3 | Apache-2.0 | 预览器组件（Vanilla Web） |
| [@file-viewer/core](https://doc.file-viewer.app/) | 3.3.3 | Apache-2.0 | 加载、缩放、搜索、导出等核心能力 |
| [@file-viewer/preset-standard](https://doc.file-viewer.app/) | 3.3.3 | Apache-2.0 | PDF / Office / 图片 / 音视频 / 文本 / 压缩包 / 邮件链路 |
| [@file-viewer/preset-engineering](https://doc.file-viewer.app/) | 3.3.3 | Apache-2.0 | CAD / 3D / 绘图 / 思维导图 / 地理链路 |
| [PDF.js](https://mozilla.github.io/pdf.js/) | 随 FileViewer | Apache-2.0 | PDF 解析与渲染 |
| [libarchive](https://libarchive.org/)（WASM） | 随 FileViewer | BSD-2-Clause | 压缩包目录读取 |
| [Comlink](https://github.com/GoogleChromeLabs/comlink) | 随 libarchive bundle | Apache-2.0 | Worker RPC |
| **[@flyfish-dev/cad-viewer](https://www.npmjs.com/package/@flyfish-dev/cad-viewer)** | 0.8.3 | **AGPL-3.0-only** | DWG/DWF 解析与渲染 |
| ├ [@mlightcad/libredwg-web](https://www.npmjs.com/package/@mlightcad/libredwg-web)（LibreDWG WASM） | 随 cad-viewer | **GPL-3.0-only** | DWG 解析 |
| ├ [@mlightcad/shx-parser](https://www.npmjs.com/package/@mlightcad/shx-parser) | 随 cad-viewer | MIT | SHX 字形解析 |
| ├ [dxf-parser](https://www.npmjs.com/package/dxf-parser) | 随 cad-viewer | MIT | DXF 解析 |
| └ [dwf-viewer](https://www.npmjs.com/package/dwf-viewer) | 0.6.x | **AGPL-3.0-only** | DWF/DWFx/XPS 渲染 |

`@flyfish-dev/cad-viewer` 的原始 NOTICE 随资源分发在
`web/public/file-viewer/wasm/cad/0.8.3/NOTICE` 与 `LICENSE`，再分发时请一并保留。

## 致谢

- 感谢 [chfs / CuteHttpFileServer](http://iscute.cn/chfs)：本项目在功能面上把它当作对标基线（多共享目录、目录级权限、打包下载、访问日志、IP 过滤、限速）。
- 感谢 [FileViewer](https://github.com/flyfish-dev/file-viewer) 把「浏览器里直接读 Office/CAD」这件事做成可自托管的组件，否则这个项目的预览能力只能靠服务端转码。
- 感谢 [Vue](https://vuejs.org/)、[Element Plus](https://element-plus.org/)、[Remix Icon](https://remixicon.com/) 与 [modernc.org/sqlite](https://gitlab.com/cznic/sqlite)，它们让「一个 exe + 一个 db 文件」这种部署形态成立。

## AGPL-3.0 合规说明（网络分发）

lan disk 是通过网络提供服务的程序，因此按 AGPL-3.0 第 13 条，**使用带 CAD/DWF 预览的服务的人有权获得对应源代码**。对应源码的获取方式：

| 组成部分 | 获取位置 |
|---|---|
| lan disk 自身（含嵌入 CAD 资源的构建脚本与资源清单） | 本仓库 <https://github.com/yonzL/lanDisk> |
| `@flyfish-dev/cad-viewer`（0.8.3） | <https://github.com/flyfish-dev/file-viewer>（及其 npm 发布包内源码） |
| `@mlightcad/libredwg-web` / LibreDWG | <https://github.com/mlightcad/libredwg-web>、<https://github.com/LibreDWG/libredwg> |
| `dwf-viewer` | <https://www.npmjs.com/package/dwf-viewer>（发布包内源码） |
| `@file-viewer/*`（Apache-2.0） | <https://github.com/flyfish-dev/file-viewer> |

如果你的部署只供内部使用、或你已按上文改为不带 CAD 的构建，则不受此条约束。
