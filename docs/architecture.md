# 架构总览

## 1. 目标与非目标

目标：

- 平台管理员用一个桌面客户端完成 GPU 集群初始化（Flux + 基础组件）、虚拟集群开通、节点资源池管理与计量
- 每个虚拟集群是独立的 k8s 控制面（v1：vcluster，独占节点），交付给使用方一份 cluster-admin kubeconfig
- 虚拟集群管理员用同一客户端在虚拟集群内安装 AI 组件，并为普通用户开通可撤销的 namespace 级 kubeconfig
- 组件分发走 Flux + 签名 OCI catalog（GHCR）

非目标（v1）：

- 不管理"租户"实体。组织、客户、账单主体都在项目之外，本项目的最小单位是虚拟集群
- 不做 Web 控制台，不做常驻后端服务；GUI 直连 API Server
- 不做共享节点的软隔离多 VC
- 不内置计费结算，只出用量与单价换算
- 不替代 kubectl / k9s

### 术语

| 术语 | 含义 |
|---|---|
| 虚拟集群（VirtualCluster, VC） | 本项目的核心对象：独立控制面 + 独占节点集合 + 优先级 + 计量 + 凭证 |
| 平台管理员 | 管理底层（host）集群的人，操作 GUI 的 host 视图 |
| 虚拟集群管理员 | 持有某个 VC 的 cluster-admin kubeconfig 的人，操作 GUI 的 VC 视图 |
| 用户 | 通过 Workspace 拿到受限 kubeconfig 的人，只读或按角色读写自己的 namespace |

## 2. 组件与部署拓扑

```
┌──────────────── 平台管理员桌面 ────────────────┐    ┌──── 虚拟集群管理员桌面 ─────┐
│ Kubeyard GUI  ── host kubeconfig               │    │ Kubeyard GUI ── VC kubeconfig│
└───────────────┬────────────────────────────────┘    └──────────────┬───────────────┘
                │ HTTPS (k8s API)                                     │ HTTPS (LB IP / Ingress:7443)
┌───────────────▼──────────────── Host 集群 ───────────────────────────┼─────────────────┐
│ kubeyard-system                                                      │                 │
│   flux (flux-operator + FluxInstance)                                │                 │
│   OCIRepository catalog-default ◄── oci://ghcr.io/wutz/kubeyard-catalog                │
│   Kustomization addon-*  → nfd, spegel, metallb, cert-manager,       │                 │
│                            istio, gpu-operator, …                    │                 │
│   kubeyard-operator (--mode=host)                                    │                 │
│     VirtualCluster / NodePool / NodeLease / UsageReport              │                 │
│                                                                      │                 │
│ vc-acme-prod  ───────────────────────────────────────────────────┐   │                 │
│   HelmRelease vcluster (operator 渲染)                            │   │                 │
│   vcluster 控制面 Pod (api-server / syncer / etcd) ◄──────────────┼───┘                 │
│   用户工作负载 Pod（syncer 同步下来，只调度到本 VC 的节点）         │                     │
│                                    ┌─────────── 虚拟集群 acme-prod ─┴────────────┐     │
│                                    │ flux（VC 自己的）                             │     │
│                                    │ kueue / trainer / agent-sandbox              │     │
│                                    │ kubeyard-operator (--mode=vcluster): Workspace│    │
│                                    └──────────────────────────────────────────────┘     │
│ 节点: gpu-node-07 [kubeyard.wutz.dev/vcluster=acme-prod:NoSchedule] …                    │
└──────────────────────────────────────────────────────────────────────────────────────────┘
```

| 组件 | 形态 | 仓库 |
|---|---|---|
| Kubeyard GUI | 桌面应用（macOS / Windows / Linux） | `wutz/kubeyard`（`gui/`） |
| kubeyard-operator | Go，controller-runtime，单二进制两种模式 | `wutz/kubeyard`（`operator/`） |
| kubeyard-catalog | Flux 清单，CI 发布为 OCI artifact 到 GHCR | `wutz/kubeyard-catalog`（独立仓库，便于他人 fork 发布） |
| 共享 Go 包 | CRD 类型、kubeconfig 处理、catalog 解析 | `wutz/kubeyard`（`pkg/`），GUI 与 operator 共用 |

## 3. 关键设计决策

| # | 决策 | 理由 | 代价 |
|---|---|---|---|
| D1 | GUI 无后端，直连 API Server | 部署成本为零；权限天然等于 kubeconfig 的 RBAC；内网可用 | 跨集群聚合、长期任务必须交给 operator |
| D2 | 持续逻辑全部放进 operator，GUI 只写 CR | GUI 关掉不影响抢占与计量；CLI、GitOps 可复用 | 需要维护 CRD 版本演进 |
| D3 | catalog 只以 OCI 形式发布到 GHCR，cosign 签名；GUI 只接受 OCI 源 | digest 不可变，签名可校验；内网集群可按需镜像 | 发布必须经过 CI；不支持直接引用 Git 仓库作为源 |
| D4 | 用 Flux 安装一切（包括 vcluster 与 operator 本身） | 统一的状态、依赖、回滚模型；GUI 只需理解 Flux 对象 | 需先装 Flux；调试多一层 |
| D5 | 节点归属以 NodeLease CR 为唯一事实，label / taint 为投影 | 抢占、计量、审计有据可查；可重建 | 多一个 CRD |
| D6 | 抢占只作用于 min 以上部分，sum(min) ≤ 容量由 webhook 保证 | 保底资源可预期，避免级联抢占 | 管理员需规划 min |
| D7 | 计量以"分配时长"为主 | 独占节点场景下占用即成本；不依赖 Prometheus 也能出账 | 不反映实际利用率（作为辅助维度补充） |
| D8 | 普通用户 kubeconfig 使用长期 ServiceAccount token，不过期，可撤销 | 满足"不过期、可撤销"；k8s 证书无法吊销，因此不用客户端证书 | token 在撤销前一直有效，泄露风险依赖撤销及时性；撤销即时性需 M5 验证 |
| D9 | provider 抽象（vcluster / k3k） | k3k 已在路线图 | v1 只实现一个，抽象可能需调整 |
| D10 | 跨 VC 抢占由 operator 按节点做；Kueue 只在 VC 内部做作业排队 | Kueue 看不到"节点归哪个 VC"，在 host 跑 Kueue 会与 VC 内的 Kueue 重复排队 | 两层调度，需要在 GUI 中讲清楚 |
| D11 | 只用 vcluster OSS 能力（dedicated host nodes），不用 private nodes | private nodes 需要 vCluster Platform | 节点隔离靠污点与准入策略补强，属于节点级而非内核级隔离 |
| D12 | 虚拟集群是唯一的业务单位，不引入 Tenant CRD | 配额、优先级、节点、计量、凭证都挂在 VC 上即可；组织与账单模型在项目之外，不在这里建模 | 同一组织开多个 VC 时，跨 VC 的汇总由调用方在 UsageReport 之上完成 |

## 4. 主要流程

### 4.1 初始化底层集群

1. GUI 连接 host kubeconfig，检测未初始化 → 引导页
2. 安装 Flux：GUI 安装 flux-operator 的 chart，创建 FluxInstance
3. 添加默认 catalog 源（OCIRepository，GHCR，cosign 校验）
4. 勾选 host profile 组件 → 生成 Kustomization，按 dependsOn 顺序就绪
5. 安装 kubeyard-operator（host 模式），CRD 就绪后 GUI 切换到平台导航

### 4.2 创建虚拟集群

1. 平台管理员创建或选择 NodePool（选标签，预览匹配节点）
2. 向导创建 VirtualCluster：优先级、各 pool 的 min / max / desired、暴露方式
3. operator：创建 `vc-<name>` 命名空间 → 分配节点（NodeLease → label / taint）→ 渲染 vcluster HelmRelease → 等待就绪 → 回写 endpoint 与 kubeconfig Secret
4. GUI 导出管理员 kubeconfig，交付给虚拟集群管理员

### 4.3 抢占

1. 高优先级 VC 的 want 增加（手动或 Pending Pod 驱动）
2. pool 无空闲 → operator 选出受害租约 → Draining → 宽限期 → 驱逐 → Released
3. 节点重新绑定给申请者；全过程写 Event，UsageReport 记录分段时长

### 4.4 虚拟集群管理员

1. GUI 连接 VC kubeconfig，检测为虚拟集群且未初始化 → 引导页
2. 安装 VC 内的 Flux → 添加 catalog → 安装 VC profile（kueue、trainer、agent-sandbox、kubeyard-operator vcluster 模式）
3. 创建 Workspace → operator 生成 namespace、quota、netpol、RBAC 与 kubeconfig → 导出给用户

### 4.5 撤销用户访问

将 Workspace 成员标记为 revoked → operator 删除该成员的 token Secret 与 kubeconfig Secret → 凭证立即失效。GUI 显示"已撤销"，不删除 Workspace 本身。

## 5. 安全

- 凭证：GUI 不上传、不缓存额外凭证；导出的 kubeconfig 只写到用户选择的本地路径
- VC 管理员 kubeconfig 等同该 VC 的 cluster-admin，泄露即该 VC 全权失守。导出需二次确认并写审计 Event，支持轮换
- 用户 kubeconfig 不过期，因此撤销必须可靠：撤销后 operator 同时清理 Secret 与 RoleBinding，并在 Workspace status 中可见
- VC 之间的隔离：独占节点（taint + nodeSelector 强制）+ 每个 VC 独立控制面 + host 侧 NetworkPolicy（vcluster chart 自带并由 operator 收紧）+ PodSecurity（默认 baseline，可按 VC 放宽到 privileged，例如 GPU 训练需要）
- 节点复用风险：节点从一个 VC 转给另一个时，本地盘、hostPath、GPU 显存残留需要清理；v1 只保证 Pod 清空，清理钩子列入路线图并在 GUI 中提示
- catalog 供应链：OCIRepository 默认开启 cosign 校验；GUI 添加第三方源时需要显式确认
- Ingress 暴露虚拟集群 API 需 TLS passthrough（端口 7443），由 istio / ingress 组件配置

## 6. 代码结构（规划）

```
kubeyard/
├── gui/              # 桌面客户端
├── operator/         # kubeyard-operator（api/、controllers/、webhooks/）
├── pkg/              # 共享：CRD 类型、catalog、kubeconfig
├── config/           # CRD / RBAC 生成物，供 catalog 引用
└── docs/
```

## 7. 已确认的决定

| 问题 | 决定 |
|---|---|
| API Group | `kubeyard.wutz.dev` |
| catalog 发布位置 | 只发布到 GHCR（`ghcr.io/wutz/kubeyard-catalog`），不额外同步到内网镜像站 |
| mygo 1.0 前的风险 | 接受 |
| 虚拟集群数量 | 本项目不设租户，设计以虚拟集群为单位；一个组织需要多个 VC 时各自开通 |
| 普通用户 kubeconfig | 不过期，可撤销 |

实现阶段待验证（不阻塞设计）：

- 目标 vcluster 版本中 `enforceTolerations` 与 nodeSelector 的合并行为
- legacy ServiceAccount token 撤销的即时性（M5）
- 节点清理钩子的范围（路线图）
