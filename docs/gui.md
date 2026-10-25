# GUI 设计

视觉规范见 [DESIGN.md](../DESIGN.md)（Vercel Geist 风格 + Kubeyard 适配，主题色 Indigo）。布局参考截图：左侧 sidebar（顶部可收起，"集群"选择器在侧边栏顶部），右侧内容区带实时刷新，窗口无原生标题栏（macOS）。

## 1. 连接模型

- GUI 读取本地 `~/.kube/config` 及用户导入的 kubeconfig，按 context 列出集群
- 连接后自动识别角色，决定显示哪套导航：

| 探测结果 | 角色 | 导航 |
|---|---|---|
| 存在 `virtualclusters.kubeyard.wutz.dev` CRD，且有 cluster 级写权限 | 平台管理员（host） | 平台导航 |
| 存在 `workspaces.kubeyard.wutz.dev` CRD（vcluster 模式 operator 已装）或当前身份为 VC 的 cluster-admin | 虚拟集群管理员 | VC 导航 |
| 只有 namespace 级权限 | 用户 | 用户导航 |
| 未安装 Kubeyard | 未初始化 | 引导页：安装 Flux → 添加 catalog → 安装 kubeyard-operator |

- 所有操作都用当前 kubeconfig 的身份直接调用 API Server，GUI 不保存额外凭证，权限以 RBAC 为准；SelfSubjectAccessReview 用于按钮级别的置灰

## 2. 信息架构

侧边栏顶部选择器："集群"（context）。虚拟集群在导航中单独列出。

### 平台管理员（host 集群）

```
工作区
  概览          节点 / GPU 总量与空闲、虚拟集群数、运行中 VC、近 24h 抢占次数
虚拟集群
  虚拟集群      列表：优先级、是否可被抢占、节点数、本月 GPU 时长、状态
                详情：资源池声明、端点、导出管理员 kubeconfig、轮换凭证
资源
  资源池        NodePool 卡片（同截图"GPU 资源池"）：空闲 / 总数、保底占用
  节点          节点表格 + 分配视图：按 VC 分列，拖拽节点到 VC 即生成 pinned 变更
  计量          VC × 时间的节点时长 / GPU 时长、利用率、CSV 导出
管理
  组件          catalog 浏览、安装 / 升级 / 卸载、Flux 状态
  源            catalog 源管理（OCI 地址 + cosign 公钥）、签名校验状态
  设置          单价表、刷新频率、镜像覆盖
```

### 虚拟集群管理员（VC 内）

```
工作区
  概览          已分配节点 / GPU、配额使用、运行中 Pod、"即将被回收"提示
  工作负载      Pod / Job / Kueue Workload 列表
用户
  用户空间      Workspace 列表：成员、quota、网络隔离、签发 / 撤销 kubeconfig
资源
  节点          当前分配到的节点（只读）、规格
管理
  组件          VC profile（kueue、trainer、agent-sandbox…）
```

### 用户

只读的概览（配额使用、Pod、Job），不提供写操作，避免 GUI 变成又一个 kubectl 前端。

## 3. 关键交互

- **创建虚拟集群**：向导三步 —— 基本信息与优先级 → 选择资源池并设定 min / max / desired（实时显示池内剩余与是否会触发抢占）→ 暴露方式（LoadBalancer IP 或 Ingress 域名，端口 7443）。提交即创建 VirtualCluster，页面跟踪 Provisioning 进度
- **调整节点**：资源池页面拖拽节点或改数字；涉及抢占时先展示 dry-run 结果（哪些 VC、哪些节点、宽限期），二次确认
- **导出管理员 kubeconfig**：显示 server 地址，并可直接在 GUI 中"以该 VC 管理员身份打开"（便于平台管理员排障）。已实现导出：输入虚拟集群名称二次确认，通过原生保存对话框选择位置，凭证由 Go 直接写入文件（权限 0600），不进入页面、不缓存；写入文件前先写审计 Event，取消对话框则不写审计。"以该 VC 管理员身份打开"与凭证轮换按钮未实现，见 [roadmap](roadmap.md) M2
- **签发用户 kubeconfig**：选择成员与角色，生成后可下载；列表中显示签发时间。凭证不过期，页面明确提示这一点
- **撤销用户 kubeconfig**：二次确认，说明"撤销立即生效，且不可恢复，需重新签发"；撤销后该成员状态变为"已撤销"
- **安装组件**：依赖自动勾选（选 gpu-operator 自动带上 nfd），参数表单由 schema.json 渲染，提供"查看将要创建的 YAML"
- **危险操作**（删除虚拟集群、抢占、导出管理员凭证、撤销用户凭证）统一用红色确认框，要求输入名称

## 4. 刷新与状态

- 列表页使用 watch（informer）而非轮询；概览页的聚合指标每 8 秒刷新一次，和截图一致
- Flux / operator 的 conditions 原样展示，同时提供"最近事件"抽屉
- 当前实现（M2）：概览页每 8 秒轮询，组件页、资源池页与虚拟集群页在切换上下文与点击刷新时加载（资源池页与虚拟集群页只读，进入页面时也会加载）；informer 与列表 watch 留作后续改进

## 5. 技术栈

窗口由 [Wails](https://wails.io) v2.17.0 提供，业务逻辑全部在 Go 侧。页面（`gui/frontend/`，原生 HTML / CSS / JS，无构建步骤）经 `//go:embed` 打进二进制，由 Wails 的资源服务提供；页面通过 Wails 绑定调用 `gui/api.go` 中 `App` 的方法，不经过 HTTP。

- macOS：WKWebView
- Windows：WebView2（需要运行时，Windows 10/11 通常已预装）
- Linux：WebKitGTK 4.0（Wails v2 默认链接 `webkit2gtk-4.0`）

API 直接用 `pkg/kube`、`pkg/catalog` 访问 API Server，与 operator 共用同一套业务代码，没有额外的后端服务。

编程模型：

- 页面状态保存在一个 JS 对象里，`render()` 按状态重建 DOM；DOM 只用 `el()` 构造，不使用 `innerHTML`
- 概览页每 8 秒刷新，只重绘内容区；安装 Flux、安装、更新参数、卸载需要二次点击确认；添加 catalog 源为单击。写操作成功后刷新概览与组件页
- 写操作在 Go 端串行执行（`App` 的互斥锁），每次执行前重新读取集群状态并校验 catalog 守卫（可安装、可更新、可卸载、托管），页面上的按钮状态只是提示
- 每个请求带超时：读取 15 秒，写入 30 秒

安全：

- 没有 HTTP 服务、没有监听端口：页面只能调用 `App` 上绑定的方法，同机其他进程与局域网都无法访问
- 页面只加载应用自带的脚本与样式，`index.html` 中的 CSP 限定为同源（`script-src 'self'`）
- 管理员 kubeconfig 不进入页面：导出由 Go 写入用户选择的文件（权限 0600），写入前记录审计 Event
- 写操作在 Go 端再次校验 catalog 守卫，页面的按钮状态只是提示

深色模式：CSS 变量跟随 `prefers-color-scheme`，侧边栏可手动切换"跟随系统 / 浅色 / 深色"，两套 token 对应 DESIGN.md。

调试：

- 直接运行 GUI 二进制必须带 `production` 标签：`go build -tags production -o kubeyard-gui ./gui`。不带标签时 Wails 会打印 "Wails applications will not build without the correct build tags" 并退出
- 开发者工具：`go build -tags production,devtools ./gui`

构建要求：Wails 使用 cgo，本地构建需要 C 编译器与系统 webview（macOS 为 Xcode Command Line Tools；Linux 为 `libgtk-3-dev libwebkit2gtk-4.0-dev`；Windows 需要 WebView2 运行时）。macOS 上直接 `go build` 时还要链接 UniformTypeIdentifiers：`CGO_LDFLAGS="-framework UniformTypeIdentifiers" go build -tags production ./gui`；`wails build` 会自动加上该参数。`gui/wails.json` 供 `wails build` 打包 `.app` / `.exe` 使用，前端没有 npm 步骤。CI（`.github/workflows/ci.yml`）在 Linux、macOS、Windows 上构建并测试全部包，包括 GUI。`pkg/` 不依赖 Wails，operator 仍可纯 Go 构建。

风险与对策：

| 风险 | 说明 | 对策 |
|---|---|---|
| cgo 构建 | 交叉编译与 CI 需要对应平台的 C 工具链 | GUI 与 operator 分开构建；`pkg/` 不依赖 cgo |
| 平台 webview 差异 | WKWebView、WebView2、WebKitGTK 的 CSS / JS 支持略有不同 | 页面只用 Geist 系统字体栈与稳定的 CSS 特性；每个平台发版前手动验证 |
| 页面被注入或同机进程滥用 | 页面能调用的 `App` 方法即攻击面 | 没有端口；CSP 限同源；管理员凭证只写文件不进页面；写操作服务端再校验 |
| 页面与 Go 类型不一致 | JSON 结构手写 | DTO 集中在 `gui/view.go`，有单元测试覆盖关键字段 |

关于参考对象：截图中"任务 / 沙箱 / 模板 / 存储卷 / 镜像"等导航来自 NeoGPU 控制台，Kube Studio 公开文档只覆盖集群创建、连接、扩容与监控。这里只借鉴截图的布局与视觉风格，功能范围以本文第 2 节为准。
