# GUI 设计

视觉规范见 [DESIGN.md](../DESIGN.md)（Vercel Geist 风格 + Kubeyard 适配，主题色 Indigo）。布局参考 Kube Studio：左侧 sidebar，顶部"集群 / 项目"选择器，右侧内容区带实时刷新。

## 1. 连接模型

- GUI 读取本地 `~/.kube/config` 及用户导入的 kubeconfig，按 context 列出集群
- 连接后自动识别集群角色，决定显示哪套导航：

| 探测结果 | 角色 | 导航 |
|---|---|---|
| 存在 `tenants.kubeyard.dev` CRD 且有 cluster 级写权限 | 平台管理员（host） | 平台导航 |
| 集群是 vcluster（`kubeyard.dev/vcluster` 节点标签或 vcluster 特征 Service）且有 cluster-admin | 租户管理员 | 租户导航 |
| 只有 namespace 级权限 | 普通用户 | 用户导航 |
| 未安装 Kubeyard | 未初始化 | 引导页：安装 Flux → 添加 catalog → 安装 kubeyard-operator |

- 所有操作都用当前 kubeconfig 的身份直接调用 API Server，GUI 不保存额外凭证，权限以 RBAC 为准；SelfSubjectAccessReview 用于按钮级别的置灰

## 2. 信息架构

顶部选择器："集群"（context）+ "租户 / 项目"（host 下选租户，虚拟集群下选 Workspace）。

### 平台管理员（host 集群）

```
工作区
  概览          节点 / GPU 总量与空闲、租户数、运行中虚拟集群、近 24h 抢占次数
租户
  租户          列表、优先级、是否可被抢占、节点数、本月 GPU 时长
  虚拟集群      状态、版本、端点、导出 kubeconfig、轮换凭证
资源
  资源池        NodePool 卡片（同截图"GPU 资源池"）：空闲 / 总数、保底占用
  节点          节点表格 + 分配视图：按租户分列，拖拽节点到租户即生成 pinned 变更
  计量          租户 × 时间的节点时长 / GPU 时长、利用率、CSV 导出
管理
  组件          catalog 浏览、安装 / 升级 / 卸载、Flux 状态
  源            catalog 源管理（OCI / Git）、签名校验状态
  设置          单价表、刷新频率、registry 镜像
```

### 租户管理员（虚拟集群）

```
工作区
  概览          已分配节点 / GPU、配额使用、运行中 Pod、"即将被回收"提示
  工作负载      Pod / Job / Kueue Workload 列表
用户
  用户空间      Workspace 列表：成员、quota、网络隔离、签发 kubeconfig
资源
  节点          当前分配到的节点（只读）、规格
管理
  组件          tenant profile（kueue、trainer、agent-sandbox…）
```

### 普通用户

只读的概览（配额使用、Pod、Job），不提供写操作，避免 GUI 变成又一个 kubectl 前端。

## 3. 关键交互

- **开通租户**：向导三步 —— 基本信息与优先级 → 选择资源池并设定 min / max / desired（实时显示池内剩余与是否触发抢占）→ 暴露方式（LoadBalancer IP 或 Ingress 域名，端口 7443）。提交即创建 Tenant + VirtualCluster，页面跟踪 Provisioning 进度
- **调整节点**：资源池页面拖拽节点或改数字；涉及抢占时先展示 dry-run 结果（哪些租户、哪些节点、宽限期），二次确认
- **导出 kubeconfig**：显示 server 地址与过期策略，支持复制、保存文件、直接在 GUI 中"以该租户身份打开"（便于平台管理员排障）
- **安装组件**：依赖自动勾选（选 gpu-operator 自动带上 nfd），参数表单由 schema.json 渲染，提供"查看将要创建的 YAML"
- **危险操作**（删除租户、抢占、导出 cluster-admin 凭证）统一用红色确认框，要求输入名称

## 4. 刷新与状态

- 列表页使用 watch（informer）而非轮询；概览页的聚合指标每 8 秒刷新一次，和截图一致
- Flux / operator 的 conditions 原样展示，同时提供"最近事件"抽屉

## 5. 技术栈

选型：[mygo](https://github.com/egoist/mygo) native UI（MIT）。纯 Go，不使用 HTML / JS / webview，UI 由 mygo 自绘：macOS 12+ 用 Metal，Windows 10/11 用 Direct3D 11，Linux 用 GTK 3。不依赖 cgo，可以交叉编译。

选它的理由：

- GUI 与 operator 同为 Go，直接复用 `client-go`、`controller-runtime` 的 typed client、CRD 类型（`pkg/`）与 Flux API 包，不需要 IPC 或生成 TS 类型
- 组件覆盖本项目所需：sidebar、table（虚拟化行）、list、tabs、dialog / alert-dialog、form、select / combobox、progress / meter、chart、toast、code-editor（YAML 预览）、diff-viewer（升级前后 values 对比）、drag-and-drop（节点拖拽分配）
- 自带打包与分发：macOS app + DMG（签名、公证），Windows 安装包，deb，签名的自动更新（支持增量）
- 深色模式：`mygo.Theme` 跟随系统，对应 DESIGN.md 的浅 / 深两套 token

编程模型与 k8s 集成：

- view 是"状态 → UI"的 Go 函数，事件处理直接改状态（即时模式风格），mygo 在下一帧前重建变化的部分
- informer / watch 跑在后台 goroutine，结果通过 `Window.Update(func(){...})` 回到主线程写入状态；耗时调用（安装、导出、dry-run）一律放后台，界面只显示进度
- 每个 context 一个 `ClusterSession`：持有 rest.Config、dynamic client、按需启动的 informer（只 watch 当前页面需要的资源，切页面停掉），断线自动重连
- headless view 测试用于页面级单元测试

风险与对策：

| 风险 | 说明 | 对策 |
|---|---|---|
| 框架年轻 | 2026-10 时最新为 v0.6.3，尚未 1.0，迭代很快（约每两天一个版本） | `go.mod` 固定版本，升级单独提 PR；UI 层与业务层（`ClusterSession`、catalog、导出逻辑）严格分离 |
| 工具链要求高 | `go.mod` 声明 `go 1.27.1` | 项目统一 Go 1.27+，CI 同版本；operator 若需更早 Go 版本，`pkg/` 保持不依赖 mygo |
| 组件细节不满足 | 例如大表格排序、拖拽交互 | 优先用 `custom-widgets` 扩展；评审时用 M1 的概览页 + 节点表格做技术验证 |
| 备选 | — | 若 M1 验证失败，退回 mygo 的 web frontend 模式（同一框架、`Bind` + 生成 TS client），业务层无需改动 |

关于参考对象：截图中"任务 / 沙箱 / 模板 / 存储卷 / 镜像"等导航来自 NeoGPU 控制台，Kube Studio 公开文档只覆盖集群创建、连接、扩容与监控。这里只借鉴截图的布局与视觉风格，功能范围以本文第 2 节为准。
