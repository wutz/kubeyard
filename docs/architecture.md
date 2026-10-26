# 架构总览

## 1. 目标与非目标

目标：

- 用一个桌面客户端安装、更新、卸载集群中的组件，并查看每个组件的 Flux 状态与事件
- 没有 Flux 的集群可以从 GUI 一键安装 Flux，再添加组件 catalog
- 组件分发走 Flux + 签名 OCI catalog（GHCR），内容由 cosign 校验

非目标：

- 不做 Web 控制台，不做常驻后端服务；GUI 直连 API Server，不开端口
- 不管理虚拟集群、节点资源池、抢占与计量。这些功能已从本项目移除（见 [路线图](roadmap.md) 的"已移除的范围"）
- 不替代 kubectl、k9s 或 Flux 本身。GUI 只管理 Kubeyard 安装的组件，其他方式安装的组件只读

### 术语

| 术语 | 含义 |
|---|---|
| catalog | 组件目录：每个组件的元数据、参数 schema 与 Flux 清单，以 OCI artifact 发布 |
| 源（catalog 源） | 集群中的 OCIRepository `kubeyard-system/catalog-default`，Flux 从 GHCR 拉取并校验 catalog |
| 组件（addon） | catalog 中的一个条目，安装后对应一个 Kustomization `addon-<name>` 与一个参数 ConfigMap |
| 托管（managed） | 组件由 Kubeyard 安装，即存在 `addon-<name>` Kustomization。只有托管组件可以更新参数与卸载 |

## 2. 组件与部署拓扑

```
┌──────────── 管理员桌面 ─────────────┐
│ Kubeyard GUI                        │
│   页面 ── Wails 绑定 ── App（Go）    │
│                 │                    │
│   pkg/kube、pkg/catalog              │
└─────────────────┬───────────────────┘
                  │ HTTPS（kubeconfig 的身份）
┌─────────────────▼──────── 集群 ──────────────────────────────┐
│ flux-system                                                    │
│   flux-operator + FluxInstance（Flux 控制器）                    │
│                                                                 │
│ kubeyard-system                                                 │
│   OCIRepository catalog-default ◄── oci://ghcr.io/wutz/kubeyard-catalog
│   Kustomization addon-<name>   → 组件的 Flux 清单（HelmRelease 等） │
│   ConfigMap addon-<name>-params → 表单参数（valuesFrom / substitute）│
└─────────────────────────────────────────────────────────────────┘
```

| 组件 | 形态 | 仓库 |
|---|---|---|
| Kubeyard GUI | 桌面应用（macOS / Windows / Linux） | `wutz/kubeyard`（`gui/`） |
| pkg/kube、pkg/catalog | Go 包，GUI 直接调用，不依赖 Wails | `wutz/kubeyard`（`pkg/`） |
| kubeyard-catalog | Flux 清单与 schema，CI 发布为 OCI artifact 到 GHCR | `wutz/kubeyard-catalog`（独立仓库，便于他人 fork 发布） |

## 3. 关键设计决策

| # | 决策 | 理由 | 代价 |
|---|---|---|---|
| D1 | GUI 无后端，直连 API Server | 部署成本为零；权限天然等于 kubeconfig 的 RBAC；内网可用 | 没有常驻进程，长期任务与跨集群聚合不在范围内 |
| D2 | catalog 只以 OCI 形式发布到 GHCR，cosign 签名；GUI 只接受 OCI 源 | digest 不可变，签名可校验；内网集群可按需镜像 | 发布必须经过 CI；不支持直接引用 Git 仓库作为源 |
| D3 | 用 Flux 安装组件 | 统一的状态、依赖、清理模型；GUI 只需理解 Flux 对象 | 需先装 Flux；调试多一层 |
| D4 | 安装前后都做守卫，守卫以集群的最新状态为准 | 不覆盖已有组件，不删除非 Kubeyard 安装的东西；页面按钮状态只是提示 | 每次写入多一次读取 |
| D5 | 组件参数以 ConfigMap 存放，Kustomization 只引用 | 参数变更可以用 `reconcile.fluxcd.io/watch` 立即协调，不等待间隔 | 参数分散在两处（values 与 substitute），由 catalog 的 `x-kubeyard-target` 区分 |

## 4. 主要流程

### 4.1 初始化集群

1. GUI 读取 kubeconfig，列出 context，选择一个
2. 没有 Flux：GUI 安装 flux-operator 的 chart（Helm SDK，等待就绪），再创建 FluxInstance
3. Flux 就绪后，GUI 添加默认 catalog 源（OCIRepository，cosign 校验），源校验通过后组件列表出现

### 4.2 安装组件

1. GUI 读取 catalog 与集群中的 Kustomization、ConfigMap、工作负载，判断每个组件的状态
2. 用户填写参数，二次点击确认安装
3. 写入前复查守卫：未安装、依赖已就绪、没有同名的非 Helm 组件
4. 写入参数 ConfigMap 与 Kustomization，Flux 协调 HelmRelease

### 4.3 更新参数

只对托管组件开放。表单以集群中存储的参数为初值，保存时只重写参数 ConfigMap 与 Kustomization 的 substitute 变量，不把已安装的值重置为默认值。Flux 随 ConfigMap 变更立即协调。

### 4.4 卸载组件

1. 写入前复查守卫：只允许托管组件；被其他已安装组件依赖时拒绝
2. 确认框列出卸载后会残留的内容（Helm 创建的 namespace、keep 策略的 CRD）
3. 删除 Kustomization（`prune: true` 清理其托管对象），再删除参数 ConfigMap

## 5. 安全

- 凭证：GUI 不上传、不缓存额外凭证；所有请求用当前 kubeconfig 的身份，权限以 RBAC 为准
- catalog 供应链：OCIRepository 开启 cosign 校验，只有 `SourceVerified=True` 的源才会被读取
- 页面的攻击面：页面只能调用 `App` 上绑定的方法；没有端口，同机其他进程与局域网都无法访问
- 删除前提醒：卸载不删除 namespace，因为里面可能有用户数据；确认框会写出残留对象

## 6. 代码结构

```
kubeyard/
├── gui/              # 桌面客户端：main.go（Wails 启动）、app.go（绑定的 App）、api.go（读写方法）
│   ├── frontend/     # 页面：index.html、static/app.js、static/app.css（无构建步骤）
│   └── build/        # 应用图标
├── pkg/
│   ├── kube/         # kubeconfig、Flux 安装、catalog 源、组件安装与状态
│   └── catalog/      # catalog 解析、安装与卸载守卫、参数渲染
├── .github/          # CI：gofmt、vet、build、test，GUI 窗口启动
└── docs/
```

## 7. 已确认的决定

| 问题 | 决定 |
|---|---|
| catalog 发布位置 | 只发布到 GHCR（`ghcr.io/wutz/kubeyard-catalog`），不额外同步到内网镜像站 |
| 卸载后的 namespace | 保留，并在确认框中列出，不自动删除 |
| 版本升级 | 不由 GUI 单独触发：OCIRepository 跟随 `stable` tag，由 Flux 拉取 |

实现阶段待验证：

- catalog 中 Helm 组件的 `createNamespace` 残留范围是否需要随 catalog 更新同步调整
