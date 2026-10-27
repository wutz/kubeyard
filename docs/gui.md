# GUI 设计

视觉规范见 [DESIGN.md](../DESIGN.md)（Vercel Geist 风格 + Kubeyard 适配，主题色 Indigo）。布局：左侧 sidebar（顶部可收起，"集群"选择器在侧边栏顶部），右侧内容区，窗口无原生标题栏（macOS）。

## 1. 连接模型

- GUI 读取本地 `~/.kube/config`（或 `KUBECONFIG` 指定的文件），按 context 列出集群，默认选中 current-context
- 所有操作都用当前 context 的身份直接调用 API Server，GUI 不保存额外凭证，权限以 RBAC 为准
- 集群没有 Flux 时，组件页只显示"安装 Flux"；Flux 已就绪但没有 catalog 源时，显示"添加 catalog 源"

## 2. 信息架构

侧边栏：

```
集群        [ context 选择器 ]
管理
  组件      当前页
主题        跟随系统 / 浅色 / 深色
```

顶部栏只放刷新按钮；侧边栏收起后，顶部栏左侧出现展开按钮。

### 组件页

```
组件                                   （页头：目录与集群中的组件 · <context>）
┌ 源面板 ──────────────────────────────────────────────────────────┐
│ 源 catalog-default/… · sha256:… · cosign 已校验                   │
│ [安装 Flux]  或  [添加 catalog 源]                                 │
└──────────────────────────────────────────────────────────────────┘
安装与卸载只作用于 Kubeyard 管理的组件；已存在的组件不会被重复安装。
┌ 组件表格 ─────────────────────────────────────────────────────────┐
│ 组件 │ 分类 │ 版本 │ 适用范围 │ 依赖 │ 状态 │ 说明                  │
└──────────────────────────────────────────────────────────────────┘
┌ 详情（选中的组件）──────────────────────────────────────────────┐
│ 名称、描述、命名空间、健康检查                                    │
│ Flux 状态：conditions 与最近事件（仅 Kubeyard 管理的组件）          │
│ 参数表单                                                         │
│ [安装] [更新参数] [卸载]  [取消]                                  │
└──────────────────────────────────────────────────────────────────┘
```

组件状态：

| 状态 | 文案 | 含义 |
|---|---|---|
| installed | 已安装 | 健康检查通过，且由 Kubeyard 安装（或 Kubeyard 管理） |
| existing | 已存在（非 Helm） | 同名工作负载或 CRD 存在，但不是 Kubeyard 安装的 |
| absent | 未安装 | 健康检查对象都不存在 |
| unknown | 无法判断 | 健康检查对象无法用 Helm release、工作负载或 CRD 查询，拒绝安装 |

## 3. 关键交互

- **安装 Flux**：仅在没有 Flux 控制器、没有 `flux-operator` release、没有 FluxInstance 时可用。二次点击确认，后台等待 Flux 就绪，提示"控制器就绪后组件会出现"
- **添加 catalog 源**：Flux 已安装且没有 `kubeyard-system/catalog-default` 时可用。单击即添加；源已存在时不覆盖，只显示其状态
- **安装组件**：依赖未就绪、已存在、无法判断的组件时按钮禁用，原因显示在按钮下方。二次点击确认
- **更新参数**：只对 Kubeyard 管理的组件开放。表单以集群中存储的值为初值，不会把已安装的值重置为默认值
- **卸载**：只对 Kubeyard 管理的组件开放，被其他已安装组件依赖时禁用。二次点击后确认框列出卸载后会残留的 namespace 与保留策略的 CRD，确认才执行
- **参数表单**：每个参数一行，必填项带 `*`，留空的文本项显示 schema 默认值作为占位；布尔参数是复选框
- **写操作的反馈**：成功或失败的消息显示在触发它的区域（源面板或组件详情），写入结束后重新读取 catalog

## 4. 刷新与状态

- 切换集群、点击刷新、写入结束时读取一次 catalog。没有轮询，也没有 watch（informer），列表不会自动变化
- 组件写入（安装、更新、卸载）后，页面每 3 秒重新读取 catalog，最多 2 分钟，直到该组件的状态达到预期（安装后为"已安装"，卸载后为非"已安装"）。Flux 协调是异步的，这样不需要手动刷新就能看到结果
- 每次读取有 15 秒超时，每次写入有 30 秒超时；超时与错误直接显示在页面上
- Flux 的 conditions 与最近事件原样展示，不做翻译

## 5. 技术栈

窗口由 [Wails](https://wails.io) v2.17.0 提供，业务逻辑全部在 Go 侧。页面（`gui/frontend/`，原生 HTML / CSS / JS，无构建步骤）经 `//go:embed` 打进二进制，由 Wails 的资源服务提供；页面通过 Wails 绑定调用 `gui/app.go` 的 `App` 方法，不经过 HTTP。

绑定的方法（`gui/api.go`）：

| 方法 | 作用 |
|---|---|
| `Contexts()` | 列出 kubeconfig 的 context |
| `Catalog(context)` | 读取 catalog 源、组件状态与 Flux 状态 |
| `InstallFlux(context)` | 安装 Flux |
| `AddCatalogSource(context)` | 添加 catalog 源 |
| `InstallAddon(context, name, fields)` | 安装组件 |
| `UpdateAddon(context, name, fields)` | 更新托管组件的参数 |
| `UninstallAddon(context, name)` | 卸载托管组件 |

平台支持：

- macOS：WKWebView
- Windows：WebView2（需要运行时，Windows 10/11 通常已预装）
- Linux：WebKitGTK 4.0（Wails v2 默认链接 `webkit2gtk-4.0`）

编程模型：

- 页面状态保存在一个 JS 对象里，`render()` 与 `renderContent()` 按状态重建 DOM；DOM 只用 `el()` 构造，不使用 `innerHTML`，文本一律作为文本节点插入
- 写操作在 Go 端串行执行（`App` 的互斥锁），每次执行前重新读取集群状态并校验 catalog 守卫，页面上的按钮状态只是提示

安全：

- 没有 HTTP 服务、没有监听端口：页面只能调用 `App` 上绑定的方法，同机其他进程与局域网都无法访问
- 页面只加载应用自带的脚本与样式，`index.html` 中的 CSP 限定为同源（`script-src 'self'`）
- 写操作在 Go 端再次校验 catalog 守卫，页面的按钮状态只是提示

深色模式：CSS 变量跟随 `prefers-color-scheme`，侧边栏可手动切换"跟随系统 / 浅色 / 深色"，两套 token 对应 DESIGN.md。

调试：

- 直接运行 GUI 二进制必须带 `production` 标签：`go build -tags production -o kubeyard-gui ./gui`。不带标签时 Wails 会打印 "Wails applications will not build without the correct build tags" 并退出
- 开发者工具：`go build -tags production,devtools ./gui`

构建要求：Wails 使用 cgo，本地构建需要 C 编译器与系统 webview（macOS 为 Xcode Command Line Tools；Linux 为 `libgtk-3-dev libwebkit2gtk-4.0-dev`；Windows 需要 WebView2 运行时）。macOS 上直接 `go build` 时还要链接 UniformTypeIdentifiers：`CGO_LDFLAGS="-framework UniformTypeIdentifiers" go build -tags production ./gui`；`wails build` 会自动加上该参数。`gui/wails.json` 供 `wails build` 打包 `.app` / `.exe` 使用，前端没有 npm 步骤。CI（`.github/workflows/ci.yml`）在 Linux、macOS、Windows 上构建并测试全部包，并启动 GUI 窗口截屏；Linux 上另起 kind 集群运行 `TestLiveCatalog`。`pkg/` 不依赖 Wails。

风险与对策：

| 风险 | 说明 | 对策 |
|---|---|---|
| cgo 构建 | 交叉编译与 CI 需要对应平台的 C 工具链 | GUI 与 `pkg/` 分开构建；`pkg/` 不依赖 cgo |
| 平台 webview 差异 | WKWebView、WebView2、WebKitGTK 的 CSS / JS 支持略有不同 | 页面只用 Geist 系统字体栈与稳定的 CSS 特性；每个平台发版前手动验证 |
| 页面被注入或同机进程滥用 | 页面能调用的 `App` 方法即攻击面 | 没有端口；CSP 限同源；写操作服务端再校验守卫 |
| 页面与 Go 类型不一致 | JSON 结构手写 | DTO 集中在 `gui/view.go` 与 `gui/api.go`，`view_test.go` 覆盖守卫与状态文案 |
| 更新后残留的参数 | 早于某个 catalog 修复安装的组件，参数 ConfigMap 缺少键时会回落到 chart 默认值 | 在组件详情中重新保存参数即可写入 schema 默认值（见 [catalog.md](catalog.md) §3） |
