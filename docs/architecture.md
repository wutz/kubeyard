# 架构总览

## 1. 目标与非目标

目标：

- 平台管理员用一个桌面客户端完成 GPU 集群的初始化（Flux + 基础组件）、租户开通、节点资源池管理、计量
- 每个租户拿到一个独立的虚拟 k8s 集群（v1：vcluster 独占节点模式），有完整的 cluster-admin 体验
- 租户管理员用同一个客户端在虚拟集群里装 AI 组件、给普通用户开通受限空间
- 组件分发走 Flux + 签名 OCI catalog，任何组织都可以发布自己的源

非目标（v1）：

- 不做 Web 控制台和常驻后端服务；GUI 直连 API Server
- 不做共享节点的多租户（软隔离），只做独占节点
- 不内置计费结算，只出用量与单价换算
- 不替代 kubectl / k9s 做通用资源浏览

## 2. 组件与部署拓扑

```
┌──────────────── 平台管理员桌面 ────────────────┐    ┌──────── 租户管理员桌面 ────────┐
│ Kubeyard GUI  ── host kubeconfig               │    │ Kubeyard GUI ── vc kubeconfig │
└───────────────┬────────────────────────────────┘    └──────────────┬────────────────┘
                │ HTTPS (k8s API)                                     │ HTTPS (LB IP / Ingress:7443)
┌───────────────▼──────────────── Host 集群 ───────────────────────────┼──────────────────┐
│ kubeyard-system                                                      │                  │
│   flux (flux-operator + FluxInstance)                                │                  │
│   OCIRepository catalog-default ◄── oci://…/kubeyard-catalog         │                  │
│   Kustomization addon-*  → nfd, spegel, metallb, cert-manager,       │                  │
│                            istio, gpu-operator, …                    │                  │
│   kubeyard-operator (--mode=host)                                    │                  │
│     Tenant / VirtualCluster / NodePool / NodeLease / UsageReport     │                  │
│                                                                      │                  │
│ tenant-acme  ─────────────────────────────────────────────────────┐  │                  │
│   HelmRelease vcluster (operator 渲染)                             │  │                  │
│   vcluster 控制面 Pod (api-server / syncer / etcd) ◄───────────────┼──┘                  │
│   租户工作负载 Pod（syncer 同步下来，只调度到 acme 的节点）          │                     │
│                                    ┌─────────── 虚拟集群 acme ──────┴─────────────┐      │
│                                    │ flux（租户自己的）                            │      │
│                                    │ kueue / trainer / agent-sandbox              │      │
│                                    │ kubeyard-operator (--mode=tenant): Workspace │      │
│                                    └──────────────────────────────────────────────┘      │
│ 节点: gpu-node-07 [kubeyard.dev/tenant=acme:NoSchedule] …                                │
└──────────────────────────────────────────────────────────────────────────────────────────┘
```

| 组件 | 形态 | 仓库 |
|---|---|---|
| Kubeyard GUI | 桌面应用（macOS / Windows / Linux） | `wutz/kubeyard`（`gui/`） |
| kubeyard-operator | Go，controller-runtime，单二进制两种模式 | `wutz/kubeyard`（`operator/`） |
| kubeyard-catalog | Flux 清单，CI 发布为 OCI artifact | `wutz/kubeyard-catalog`（独立仓库，便于他人 fork 发布） |
| 共享 Go 包 | CRD 类型、kubeconfig 处理、catalog 解析 | `wutz/kubeyard`（`pkg/`），GUI 与 operator 共用 |

## 3. 关键设计决策

| # | 决策 | 理由 | 代价 |
|---|---|---|---|
| D1 | GUI 无后端，直连 API Server | 部署成本为零；权限天然等于 kubeconfig 的 RBAC；离线内网可用 | 跨集群聚合、长期任务必须交给 operator |
| D2 | 持续逻辑全部放进 operator，GUI 只写 CR | GUI 关掉不影响抢占/计量；后续 CLI、GitOps 可复用 | 需要维护 CRD 版本演进 |
| D3 | catalog 用 OCI 发布，Git 作为兼容 | 内网 registry 现成；digest 不可变；cosign 签名 | 发布需 CI |
| D4 | 用 Flux 安装一切（包括 vcluster 与 operator 本身） | 统一的状态、依赖、回滚模型；GUI 只需理解 Flux 对象 | 需先装 Flux；调试多一层 |
| D5 | 节点归属以 NodeLease CR 为唯一事实，label/taint 为投影 | 抢占/计量/审计有据可查；可重建 | 多一个 CRD |
| D6 | 抢占只作用于 min 以上部分，sum(min) ≤ 容量由 webhook 保证 | 保底资源可预期，避免级联抢占 | 管理员需规划 min |
| D7 | 计量以"分配时长"为主 | 独占节点场景下占用即成本；不依赖 Prometheus 也能出账 | 不反映实际利用率（作为辅助维度补充） |
| D8 | 普通用户身份 v1 用 ServiceAccount + TokenRequest | 无需 IdP；token 有期限 | 无单点登录，见路线图 |
| D9 | provider 抽象（vcluster / k3k） | k3k 已在路线图 | v1 只实现一个，抽象可能需调整 |
| D10 | 跨租户抢占由 operator 按"节点"做；Kueue 只在虚拟集群内部做作业排队 | Kueue 管的是单集群内的 quota 与 Workload 准入，看不到"节点归哪个虚拟集群"。host 侧只能看到同步下来的 Pod，在 host 跑 Kueue 会和租户自己的 Kueue 重复排队 | 两层调度：租户内由 Kueue 管，租户间由 operator 管，需要在 GUI 里讲清楚 |
| D11 | 只用 vcluster OSS 能力（dedicated host nodes），不用 private nodes | private nodes 需要 vCluster Platform | 节点隔离靠污点 + 准入策略补强，属于"节点级"而非"网络/内核级"硬隔离 |

## 4. 主要流程

### 4.1 初始化底层集群

1. GUI 连接 host kubeconfig，检测未初始化 → 引导页
2. 安装 Flux：GUI 内嵌 flux-operator 清单，apply 后创建 FluxInstance
3. 添加默认 catalog 源（OCIRepository + 镜像站 ConfigMap）
4. 勾选 host profile 组件 → 生成 Kustomization，按 dependsOn 顺序就绪
5. 安装 kubeyard-operator（host 模式），CRD 就绪后 GUI 切换到平台导航

### 4.2 开通租户

1. 管理员在 GUI 里创建 NodePool（选标签，预览匹配节点）
2. 向导创建 Tenant + VirtualCluster（min/max/desired、暴露方式）
3. operator：建 `tenant-<name>` namespace → 分配节点（NodeLease → label/taint）→ 渲染 vcluster HelmRelease → 等待就绪 → 回写 endpoint 与 kubeconfig Secret
4. GUI 导出 kubeconfig 交给租户

### 4.3 抢占

1. 高优先级 VC 的 want 增加（手动或 Pending Pod 驱动）
2. pool 无空闲 → operator 选出受害租约 → Draining → 宽限期 → 驱逐 → Released
3. 节点重新绑定给申请者；全过程写 Event，UsageReport 记录分段时长

### 4.4 租户管理员

1. GUI 连接 vc kubeconfig，检测为虚拟集群且未初始化 → 引导页
2. 安装虚拟集群内 Flux → 添加 catalog → 安装 tenant profile（kueue、trainer、agent-sandbox、kubeyard-operator tenant 模式）
3. 创建 Workspace → operator 生成 namespace、quota、netpol、RBAC 与 kubeconfig → 导出给普通用户

## 5. 安全

- 凭证：GUI 不上传、不缓存额外凭证；导出的 kubeconfig 只写到用户选择的本地路径
- 租户隔离：独占节点（taint + nodeSelector 强制）+ 每租户独立控制面 + host 侧 NetworkPolicy（vcluster chart 自带并由 operator 收紧）+ PodSecurity（默认 baseline，可按租户放宽到 privileged，例如 GPU 训练需要）
- 节点复用风险：节点从租户 A 转给 B 时，本地盘、hostPath、GPU 显存残留需要清理；v1 只保证 Pod 清空，清理钩子列入路线图并在 GUI 中提示
- catalog 供应链：OCIRepository 默认开启 cosign 校验；添加未签名的第三方源需要显式确认
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

## 7. 待确认问题

1. API Group 域名：`kubeyard.dev` 是否可用，或改用组织域名
2. 默认 catalog 发布地址：`ghcr.io/wutz/kubeyard-catalog`，内网是否同步到 `mir.blsc.cn`
3. mygo 还没到 1.0（v0.6.3），是否接受用 M1 技术验证来兜底（见 [gui.md](gui.md#5-技术栈) 风险评估）
4. 一个租户是否需要多个虚拟集群（模型已支持，GUI v1 只展示一个）
5. 普通用户 kubeconfig 默认有效期
