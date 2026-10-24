# kubeyard-operator 设计

GUI 只负责"表达意图"，所有需要持续协调的逻辑（节点分配、抢占、计量、用户空间漂移修复）都放进 operator。这样 GUI 关掉之后集群状态依然正确，也方便以后接入 CLI / GitOps。

同一个二进制，两种运行模式，均通过 Kubeyard 目录（catalog）以 Flux 安装：

| 模式 | 部署位置 | 控制器 |
|---|---|---|
| `--mode=host` | 底层（host）集群 | Tenant、VirtualCluster、NodePool、NodeLease、UsageReport |
| `--mode=tenant` | 虚拟集群（也可装在 host 集群） | Workspace |

API Group：`kubeyard.dev/v1alpha1`（占位，域名待定）。

## 1. 资源模型

```
Tenant (cluster)            租户身份、优先级、默认配额
 └─ VirtualCluster (ns: tenant-<name>)   一个虚拟 k8s 集群，v1 每租户 1 个
      └─ poolClaims[]  ──引用──►  NodePool (cluster)   一类可分配节点
                                     └─ NodeLease (cluster)  "节点 X 当前租给 VC Y" 的记录
UsageReport (cluster)       按租户 + 日聚合的计量结果
Workspace (cluster, 装在虚拟集群内)  面向普通用户的 namespace + RBAC + quota + netpol
```

### Tenant

```yaml
apiVersion: kubeyard.dev/v1alpha1
kind: Tenant
metadata:
  name: acme
spec:
  displayName: ACME 研究院
  contacts: [ops@acme.example]
  priority: 100            # 越大越优先，用于跨租户抢占
  preemptible: true        # false = 该租户的节点永不被抢占（高于 min 的部分也不行）
status:
  namespace: tenant-acme   # operator 创建，承载 vcluster 控制面与同步过来的 Pod
  virtualClusters: 1
  allocatedNodes: 6
```

### VirtualCluster

```yaml
apiVersion: kubeyard.dev/v1alpha1
kind: VirtualCluster
metadata:
  name: acme-prod
  namespace: tenant-acme
spec:
  provider: vcluster       # vcluster | k3k（预留）
  mode: dedicated-nodes    # v1 只支持该模式，见 docs/virtual-cluster.md
  version: "0.x.y"         # 对应 catalog 中 vcluster chart 版本
  distro: k8s
  exposure:
    type: LoadBalancer     # LoadBalancer | Ingress
    loadBalancerIP: 10.243.145.200
    # ingress: { host: acme.k8s.example.com, port: 7443 }
  valuesOverride: {}       # 透传给 vcluster chart，受 webhook 白名单约束
  poolClaims:
    - pool: rtx-5090-x8
      min: 2               # 保底，永不被抢占
      max: 8               # 上限，扩容 / 抢占都不能超过
      desired: 4           # 手动模式下的期望节点数
      autoscale:
        enabled: true      # 根据 Pending Pod 自动在 [min,max] 内伸缩
        scaleDownIdle: 30m
      pinnedNodes: [gpu-node-07]   # 管理员手动指定，pinned 节点视同 min 的一部分
status:
  phase: Ready             # Pending | Provisioning | Ready | Degraded | Deleting
  endpoint: https://10.243.145.200
  kubeconfigSecretRef: { name: vc-acme-prod, key: config }
  allocations:
    - pool: rtx-5090-x8
      nodes: [gpu-node-07, gpu-node-08, gpu-node-11, gpu-node-12]
      pendingDemand: 1     # autoscale 观察到的额外需求
  conditions: []
```

### NodePool

```yaml
apiVersion: kubeyard.dev/v1alpha1
kind: NodePool
metadata:
  name: rtx-5090-x8
spec:
  displayName: rtx-5090.x8
  nodeSelector:            # 候选节点；通常基于 NFD / gpu-feature-discovery 标签
    matchLabels:
      nvidia.com/gpu.product: NVIDIA-GeForce-RTX-5090
      nvidia.com/gpu.count: "8"
  preemption:
    policy: LowerPriority  # Never | LowerPriority
    gracePeriod: 10m       # 给被抢占租户的通知窗口
    drainTimeout: 30m      # 超时后强制驱逐
  release:
    wipe: none             # none | reboot（预留：节点归还前的清理钩子）
status:
  total: 24
  free: 19
  reserved: 4              # sum(min) 占用
  unhealthy: 1
```

### NodeLease

节点归属的唯一事实来源。节点上的 label/taint 只是 NodeLease 的投影，可随时由 operator 重建。

```yaml
apiVersion: kubeyard.dev/v1alpha1
kind: NodeLease
metadata:
  name: gpu-node-07
spec:
  node: gpu-node-07
  pool: rtx-5090-x8
  tenant: acme
  virtualCluster: tenant-acme/acme-prod
  reason: Pinned           # Pinned | Min | Scale | Preempt
status:
  phase: Bound             # Binding | Bound | Draining | Released
  boundAt: "2026-05-01T08:00:00Z"
  drainDeadline: null
```

每个节点同一时刻最多一个非 Released 的 NodeLease（name = 节点名 保证唯一）。Released 的租约归档进计量后删除。

### UsageReport / Workspace

见第 5、6 节。

## 2. 节点绑定机制（独占节点）

NodeLease 进入 Bound 时，operator 对节点做三件事：

1. 打标签 `kubeyard.dev/tenant=<tenant>`、`kubeyard.dev/vcluster=<ns>.<name>`、`kubeyard.dev/pool=<pool>`
2. 打污点 `kubeyard.dev/tenant=<tenant>:NoSchedule`，挡住其他租户和 host 上的普通工作负载
3. host 侧 DaemonSet（gpu-operator、CNI、CSI、监控 exporter）需容忍 `kubeyard.dev/tenant`，由 catalog 中这些组件的默认 values 统一加上

vcluster 侧生成的 values（operator 渲染后写入 HelmRelease，不允许 `valuesOverride` 覆盖）：

```yaml
sync:
  fromHost:
    nodes:
      enabled: true
      selector:
        labels:
          kubeyard.dev/vcluster: tenant-acme.acme-prod
  toHost:
    pods:
      enforceTolerations:
        - kubeyard.dev/tenant=acme:NoSchedule
```

再配合 host 侧 ValidatingAdmissionPolicy：`tenant-*` namespace 中的 Pod 必须带 `nodeSelector kubeyard.dev/vcluster=<自己>`，防止租户在虚拟集群里手写 nodeName / nodeSelector 跳到别人的节点。具体 vcluster 字段名以 `docs/virtual-cluster.md` 中核对过的版本为准。

解绑（Released）时按相反顺序：先确认节点上该 VC 的 Pod 已清空，再摘污点和标签，节点回到 free。

## 3. 分配与抢占

operator 对每个 NodePool 单独、串行地运行一次协调（pool 级别互斥，避免两个 VC 同时抢同一个节点）。

### 3.1 期望值

对每个引用该 pool 的 VC 计算 `want`：

```
manual   : want = clamp(desired, min, max)
autoscale: want = clamp(current + pendingDemand - idleNodes, min, max)
want     = max(want, len(pinnedNodes))
```

`pendingDemand` 来自 host 侧 `tenant-*` namespace 中因资源不足 Pending 的 Pod（vcluster 同步到 host 的 Pod），按 pool 的单节点规格向上取整。`idleNodes` 是持续 `scaleDownIdle` 没有非 DaemonSet Pod 的节点。

### 3.2 准入不变量（webhook 强制）

- 所有 VC 在该 pool 的 `sum(min) ≤ pool.total - unhealthy`，否则拒绝创建/修改 VC
- `min ≤ desired ≤ max`；pinnedNodes 必须匹配 pool 的 nodeSelector 且未被其他 VC pin

这保证 min 永远可满足，抢占只发生在 min 以上的"弹性部分"。

### 3.3 分配顺序

1. pinned 节点 → 对应 VC
2. 满足所有 VC 的 min
3. 剩余 free 节点按 `priority 降序, 创建时间升序` 分给 `want > current` 的 VC
4. 还有缺口时尝试抢占：

```
victim 候选 = 同 pool 中满足以下条件的 NodeLease
  - 所属 Tenant.priority < 申请者 priority（同优先级不互相抢占）
  - 所属 Tenant.preemptible = true
  - 所属 VC 当前节点数 > min，且节点不是 pinned
排序   = priority 升序 → (current - min) 降序 → boundAt 降序（最新绑定的先让）
```

每次只取够缺口的数量。被选中的租约进入 Draining。

### 3.4 Drain 流程

```
Bound ──抢占/缩容──► Draining
  1. 节点加 kubeyard.dev/draining:NoSchedule，新 Pod 不再调度上来
  2. 向受害 VC 发 Event，并在其 VirtualCluster.status.conditions 写 Preempting
     （租户 GUI 能看到"x 个节点将在 10m 后回收"）
  3. gracePeriod 到期后按 PDB 驱逐该节点上属于 VC 的 Pod（Eviction API）
  4. drainTimeout 到期仍有 Pod → 强制删除
  5. 节点清空 ──► Released ──► 新 NodeLease(Binding) 给申请者
```

抢占是破坏性操作：GUI 在把某个租户优先级调高、或调大 desired 时，先调用 operator 的 dry-run（`VirtualCluster` 加注解 `kubeyard.dev/dry-run=true`，结果写 status）预览会被抢占的节点，管理员确认后再提交。

## 4. kubeconfig 导出

- 管理员 kubeconfig：vcluster 根据 `exportKubeConfig` 生成的 Secret（`status.kubeconfigSecretRef`）。operator 渲染 values 时把 `exposure` 中的对外地址（Ingress 走 7443 端口）同时写进 `exportKubeConfig.server` 和 `controlPlane.proxy.extraSANs`，否则证书校验会失败。这是虚拟集群内的 cluster-admin，GUI 导出时二次确认，并记录审计 Event。
- 轮换：VirtualCluster 加注解 `kubeyard.dev/rotate-kubeconfig=<时间戳>` 触发重新签发，旧凭证失效依赖 vcluster 证书轮换能力（版本相关，需在实现阶段核实）。

## 5. 计量

计量以"分配"为主、"使用"为辅：独占节点模式下租户占着节点就该计费，不管 GPU 是否跑满。

| 维度 | 来源 | 粒度 |
|---|---|---|
| 节点时长 / GPU 时长 / CPU·内存时长 | NodeLease 的 boundAt → releasedAt，乘节点 allocatable | 精确到秒 |
| GPU 利用率、显存 | dcgm-exporter，按节点上 `tenant-*` Pod 的标签聚合 | Prometheus 采样 |
| CPU / 内存实际使用 | kubelet cAdvisor + kube-state-metrics，按 namespace 聚合 | Prometheus 采样 |
| 存储 | host 侧 `tenant-*` namespace 的 PVC requests | 每小时快照 |

实现：

- operator 暴露 Prometheus 指标：`kubeyard_lease_allocated{tenant,vcluster,pool,node,resource}`，可直接用 VictoriaMetrics / Prometheus 做实时看板
- 每日 00:05（可配时区）生成 `UsageReport`，即使 Prometheus 不存在也能从 NodeLease 历史得出"分配"维度：

```yaml
apiVersion: kubeyard.dev/v1alpha1
kind: UsageReport
metadata:
  name: acme-2026-05-01
spec:
  tenant: acme
  period: { start: "2026-05-01T00:00:00+08:00", end: "2026-05-02T00:00:00+08:00" }
status:
  allocated:
    - pool: rtx-5090-x8
      nodeHours: 96
      gpuHours: 768
  utilization:              # Prometheus 可用时填充
    gpuAvg: "0.63"
  storageGiHours: 3120
```

- 已结束租约的明细在写入 UsageReport 后删除；UsageReport 默认保留 400 天，GUI 可导出 CSV
- 不内置价格；单价表放在 GUI 设置里，只在展示/导出时相乘
- 租户归属：vcluster 同步到 host 的 Pod 都在 `tenant-<name>` namespace，按 namespace 就能归到租户。如果要细到虚拟集群内部的 namespace，就读 syncer 写在 host Pod 上的原 namespace 标签（实现时按固定版本确认 key）
- OpenCost 作为可选集成：它以 namespace 为主维度，可以直接复用上面的归属。Kubeyard 不依赖它，因为独占节点的"分配时长"不需要按 request 分摊成本

## 6. Workspace（虚拟集群内的普通用户空间）

由租户管理员在虚拟集群里创建，`--mode=tenant` 的 operator 负责协调，GUI 只写这一个对象：

```yaml
apiVersion: kubeyard.dev/v1alpha1
kind: Workspace
metadata:
  name: alice
spec:
  namespaces: [alice]      # 可多个，第一个为默认
  members:
    - name: alice
      role: edit           # view | edit | admin，映射到内置 ClusterRole
  quota:
    hard:
      requests.cpu: "64"
      requests.memory: 512Gi
      requests.nvidia.com/gpu: "8"
      persistentvolumeclaims: "20"
  limitRange:
    defaultRequest: { cpu: 100m, memory: 128Mi }
  network:
    isolation: namespace   # namespace: 默认拒绝跨 ns 入流量，允许 DNS 与公网出口
    allowFrom: []          # 额外放通的 namespace（例如 ingress、monitoring）
  kueue:
    localQueue: true       # 装了 Kueue 时自动建 LocalQueue 指向租户 ClusterQueue
status:
  phase: Ready
  kubeconfigs:
    - member: alice
      secretRef: { name: ws-alice-alice-kubeconfig }
      expiresAt: "2026-08-01T00:00:00Z"
```

协调产物：Namespace、ResourceQuota、LimitRange、NetworkPolicy（default-deny + 放通规则）、每个成员一个 ServiceAccount + RoleBinding、用 TokenRequest 签发有期限的 token 组装成 kubeconfig。漂移（有人手改 quota / 删 netpol）会被改回。

v1 不做 OIDC；身份就是 ServiceAccount。接入企业 IdP 列入路线图。

## 7. Webhook 与权限

- Validating：VirtualCluster（不变量、valuesOverride 白名单）、NodePool（nodeSelector 不重叠）、Tenant（删除前须无 VC）
- Host 侧 ValidatingAdmissionPolicy：`tenant-*` namespace Pod 的 nodeSelector 约束（见第 2 节）
- operator ServiceAccount 需要：nodes（patch label/taint）、pods/eviction、namespaces、Flux HelmRelease/OCIRepository、自身 CRD；不需要 secrets 的全局读权限，只读 `tenant-*` 中 vcluster 生成的 kubeconfig Secret
