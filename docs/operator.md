# kubeyard-operator 设计

GUI 只负责"表达意图"，所有需要持续协调的逻辑（节点分配、抢占、计量、用户空间与凭证）都放进 operator。GUI 关掉之后集群状态依然正确，也方便以后接入 CLI / GitOps。

同一个二进制，两种运行模式，均通过 Kubeyard catalog 以 Flux 安装：

| 模式 | 部署位置 | 控制器 |
|---|---|---|
| `--mode=host` | 底层（host）集群 | VirtualCluster、NodePool、NodeLease、UsageReport |
| `--mode=vcluster` | 虚拟集群内部 | Workspace |

API Group：`kubeyard.wutz.dev/v1alpha1`。

本项目不管理"租户"实体。组织、客户、账单主体都在项目之外；本项目的最小单位是**虚拟集群（VirtualCluster）**，配额、优先级、节点、计量、kubeconfig 都挂在它上面。

## 1. 资源模型

```
VirtualCluster (cluster)          本项目的核心对象：优先级、节点声明、控制面
 ├─ 宿主命名空间 vc-<name>         operator 创建，承载 vcluster 控制面与同步下来的 Pod
 ├─ poolClaims[] ──引用──► NodePool (cluster)      一类可分配节点
 │                            └─ NodeLease (cluster)  节点 X 当前归属 VC Y
 └─ UsageReport (cluster)         按 VC + 日聚合的计量结果

Workspace (虚拟集群内部)           普通用户空间，由 vcluster 模式 operator 协调
```

每个虚拟集群只有一个控制面。需要多个虚拟集群的组织，各自开通一个 VirtualCluster 即可。

### VirtualCluster

```yaml
apiVersion: kubeyard.wutz.dev/v1alpha1
kind: VirtualCluster
metadata:
  name: acme-prod              # 全局唯一；宿主命名空间为 vc-acme-prod
spec:
  displayName: ACME 生产集群
  description: ""
  priority: 100                # 越大越优先，用于跨 VC 抢占
  preemptible: true            # false = 本 VC 的节点永不被抢占
  provider: vcluster           # vcluster | k3k（预留）
  mode: dedicated-nodes        # v1 只支持该模式，见 virtual-cluster.md
  version: "0.27.0"            # 对应 catalog 中 vcluster chart 版本
  distro: k8s
  exposure:
    type: LoadBalancer         # LoadBalancer | Ingress
    loadBalancerIP: 10.243.145.200
    # ingress: { host: acme.k8s.example.com, port: 7443 }
  valuesOverride: {}           # 透传给 vcluster chart，受 webhook 白名单约束
  poolClaims:
    - pool: rtx-5090-x8
      min: 2                   # 保底，永不被抢占
      max: 8                   # 上限，扩容与抢占都不能超过
      desired: 4               # 手动模式下的期望节点数
      autoscale:
        enabled: true          # 根据 Pending Pod 自动在 [min,max] 内伸缩
        scaleDownIdle: 30m
      pinnedNodes: [gpu-node-07]   # 管理员手动指定，视同 min 的一部分
status:
  phase: Ready                 # Pending | Provisioning | Ready | Degraded | Draining | Deleting
  hostNamespace: vc-acme-prod
  endpoint: https://10.243.145.200
  adminKubeconfigSecretRef: { namespace: vc-acme-prod, name: vc-acme-prod-admin, key: config }
  allocations:
    - pool: rtx-5090-x8
      nodes: [gpu-node-07, gpu-node-08, gpu-node-11, gpu-node-12]
      pendingDemand: 1         # autoscale 观察到的额外需求
  conditions: []
```

### NodePool

```yaml
apiVersion: kubeyard.wutz.dev/v1alpha1
kind: NodePool
metadata:
  name: rtx-5090-x8
spec:
  displayName: rtx-5090.x8
  nodeSelector:                # 候选节点；通常基于 NFD / gpu-feature-discovery 标签
    matchLabels:
      nvidia.com/gpu.product: NVIDIA-GeForce-RTX-5090
      nvidia.com/gpu.count: "8"
  preemption:
    policy: LowerPriority      # Never | LowerPriority
    gracePeriod: 10m           # 给被抢占 VC 的通知窗口
    drainTimeout: 30m          # 超时后强制驱逐
  release:
    wipe: none                 # none | reboot（预留：节点归还前的清理钩子）
status:
  total: 24
  free: 19
  reserved: 4                  # sum(min) 占用
  unhealthy: 1
```

### NodeLease

节点归属的唯一事实来源。节点上的 label / taint 只是 NodeLease 的投影，可随时由 operator 重建。

```yaml
apiVersion: kubeyard.wutz.dev/v1alpha1
kind: NodeLease
metadata:
  name: gpu-node-07            # name = 节点名，保证同一节点只有一条活跃租约
spec:
  node: gpu-node-07
  pool: rtx-5090-x8
  virtualCluster: acme-prod
  reason: Pinned               # Pinned | Min | Scale | Preempt
status:
  phase: Bound                 # Binding | Bound | Draining | Released
  boundAt: "2026-05-01T08:00:00Z"
  drainDeadline: null
```

Released 的租约写入 UsageReport 后删除。

## 2. 节点绑定机制（独占节点）

NodeLease 进入 Bound 时，operator 对节点做三件事：

1. 打标签 `kubeyard.wutz.dev/vcluster=<name>`、`kubeyard.wutz.dev/pool=<pool>`
2. 打污点 `kubeyard.wutz.dev/vcluster=<name>:NoSchedule`，挡住其他 VC 和 host 上的普通工作负载
3. host 侧 DaemonSet（gpu-operator、CNI、CSI、exporter）需容忍该污点，由 catalog 中这些组件的默认 values 统一加上

vcluster 侧 values 由 operator 渲染进 HelmRelease，不允许 `valuesOverride` 覆盖这两段：

```yaml
sync:
  fromHost:
    nodes:
      enabled: true
      selector:
        labels:
          kubeyard.wutz.dev/vcluster: acme-prod
  toHost:
    pods:
      enforceTolerations:
        - kubeyard.wutz.dev/vcluster=acme-prod:NoSchedule
```

再配合 host 侧 ValidatingAdmissionPolicy：`vc-*` 命名空间中的 Pod 必须带 `nodeSelector kubeyard.wutz.dev/vcluster=<对应的 VC 名>`，防止虚拟集群内的用户手写 nodeName / nodeSelector 跳到别人的节点。具体字段以 catalog 固定的 vcluster 版本为准，实现阶段跑越界调度测试。

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

`pendingDemand` 来自 host 侧 `vc-*` 命名空间中因资源不足 Pending 的 Pod（vcluster 同步到 host 的 Pod），按 pool 的单节点规格向上取整。`idleNodes` 是持续 `scaleDownIdle` 内没有非 DaemonSet Pod 的节点。

### 3.2 准入不变量（webhook 强制）

- 每个 pool 上 `sum(min) ≤ pool.total - unhealthy`，否则拒绝创建或修改 VC
- `min ≤ desired ≤ max`；pinnedNodes 必须匹配 pool 的 nodeSelector，且未被其他 VC pin

这保证 min 永远可满足，抢占只发生在 min 以上的弹性部分。

### 3.3 分配顺序

1. pinned 节点 → 对应 VC
2. 满足所有 VC 的 min
3. 剩余 free 节点按 `priority 降序, 创建时间升序` 分给 `want > current` 的 VC
4. 还有缺口时尝试抢占：

```
victim 候选 = 同 pool 中满足以下条件的 NodeLease
  - 所属 VC.priority < 申请者 priority（同优先级不互相抢占）
  - 所属 VC.preemptible = true
  - 所属 VC 当前节点数 > min，且节点不是 pinned
排序   = priority 升序 → (current - min) 降序 → boundAt 降序（最新绑定的先让）
```

每次只取够缺口的数量。被选中的租约进入 Draining。

### 3.4 Drain 流程

```
Bound ──抢占/缩容──► Draining
  1. 节点加 kubeyard.wutz.dev/draining:NoSchedule，新 Pod 不再调度上来
  2. 向受害 VC 发 Event，并在其 VirtualCluster.status.conditions 写 Preempting
     （VC 管理员在 GUI 中能看到"x 个节点将在 10m 后回收"）
  3. gracePeriod 到期后按 PDB 驱逐该节点上属于该 VC 的 Pod（Eviction API）
  4. drainTimeout 到期仍有 Pod → 强制删除
  5. 节点清空 ──► Released ──► 新 NodeLease(Binding) 给申请者
```

抢占是破坏性操作。GUI 在调高某个 VC 的优先级或 desired 之前，先给 VC 加注解 `kubeyard.wutz.dev/dry-run=true`，operator 把预览结果（会被抢占的 VC、节点、宽限期）写入 status，管理员确认后再提交。

## 4. 凭证

### 4.1 虚拟集群管理员 kubeconfig

- 来源：vcluster 根据 `exportKubeConfig` 生成的 Secret，记录在 `status.adminKubeconfigSecretRef`
- operator 渲染 values 时，把 `exposure` 中的对外地址（Ingress 走 7443）同时写进 `exportKubeConfig.server` 与 `controlPlane.proxy.extraSANs`，否则证书校验失败
- 这是该 VC 内的 cluster-admin。GUI 导出时二次确认，并记录审计 Event
- 轮换：VirtualCluster 加注解 `kubeyard.wutz.dev/rotate-kubeconfig=<时间戳>` 触发重签；旧凭证失效依赖 vcluster 的证书轮换能力（版本相关，实现阶段核实）

### 4.2 普通用户 kubeconfig

见第 6 节。

## 5. 计量

计量以"分配"为主、"使用"为辅：独占节点模式下 VC 占着节点就该计量，不管 GPU 是否跑满。

| 维度 | 来源 | 粒度 |
|---|---|---|
| 节点时长 / GPU 时长 / CPU·内存时长 | NodeLease 的 boundAt → releasedAt，乘节点 allocatable | 精确到秒 |
| GPU 利用率、显存 | dcgm-exporter，按节点上 `vc-*` Pod 聚合 | Prometheus 采样 |
| CPU / 内存实际使用 | kubelet cAdvisor + kube-state-metrics，按命名空间聚合 | Prometheus 采样 |
| 存储 | `vc-*` 命名空间的 PVC requests | 每小时快照 |

实现要点：

- 归属无需额外映射：`vc-<name>` 命名空间直接对应 VirtualCluster
- operator 暴露 Prometheus 指标 `kubeyard_lease_allocated{virtualcluster,pool,node,resource}`，可直接接 VictoriaMetrics / Prometheus 做实时看板
- 每日 00:05（可配时区）生成 `UsageReport`。即使没有 Prometheus，也能从 NodeLease 历史得出分配维度：

```yaml
apiVersion: kubeyard.wutz.dev/v1alpha1
kind: UsageReport
metadata:
  name: acme-prod-2026-05-01
spec:
  virtualCluster: acme-prod
  period: { start: "2026-05-01T00:00:00+08:00", end: "2026-05-02T00:00:00+08:00" }
status:
  allocated:
    - pool: rtx-5090-x8
      nodeHours: 96
      gpuHours: 768
  utilization:                 # Prometheus 可用时填充
    gpuAvg: "0.63"
  storageGiHours: 3120
```

- 已结束租约的明细写入 UsageReport 后删除；UsageReport 默认保留 400 天，GUI 可导出 CSV
- 不内置价格。单价表放在 GUI 设置里，只在展示与导出时相乘
- OpenCost 作为可选集成。它以 namespace 为主维度，可以直接复用 `vc-*` 的归属；Kubeyard 不依赖它，因为独占节点的"分配时长"不需要按 request 分摊

## 6. Workspace（虚拟集群内的普通用户空间）

由虚拟集群管理员在 VC 内创建，`--mode=vcluster` 的 operator 负责协调。GUI 只写这一个对象：

```yaml
apiVersion: kubeyard.wutz.dev/v1alpha1
kind: Workspace
metadata:
  name: alice
spec:
  namespaces: [alice]          # 可多个，第一个为默认
  members:
    - name: alice
      role: edit               # view | edit | admin，映射到内置 ClusterRole
      revoked: false           # true = 撤销该成员的全部凭证
  quota:
    hard:
      requests.cpu: "64"
      requests.memory: 512Gi
      requests.nvidia.com/gpu: "8"
      persistentvolumeclaims: "20"
  limitRange:
    defaultRequest: { cpu: 100m, memory: 128Mi }
  network:
    isolation: namespace       # namespace: 默认拒绝跨 ns 入流量，允许 DNS 与公网出口
    allowFrom: []              # 额外放通的 namespace（例如 ingress、monitoring）
  kueue:
    localQueue: true           # 装了 Kueue 时自动建 LocalQueue
status:
  phase: Ready
  members:
    - name: alice
      kubeconfigSecretRef: { name: ws-alice-alice-kubeconfig }
      issuedAt: "2026-05-01T09:00:00Z"
      revoked: false
```

协调产物：Namespace、ResourceQuota、LimitRange、NetworkPolicy（default-deny + 放通规则）、每个成员一个 ServiceAccount 与 RoleBinding，以及组装好的 kubeconfig Secret。漂移（有人手改 quota、删 netpol）会被改回。

### 凭证：不过期，可撤销

- 凭证使用 ServiceAccount 的长期 token（`kubernetes.io/service-account-token` 类型 Secret，由 operator 创建，token controller 填充），**不设过期时间**
- 撤销：将 `members[].revoked` 置为 `true`，或删除成员。operator 删除该 ServiceAccount 对应的 token Secret 与 kubeconfig Secret，并移除 RoleBinding。API Server 校验 legacy token 时会检查 Secret 是否存在，所以撤销即时生效
- 为什么不用客户端证书：Kubernetes 不支持吊销证书，撤销只能等证书过期，与"不过期、可撤销"的要求冲突
- 轮换：成员可以重新签发（删除旧 Secret、生成新 Secret），GUI 中提供"重新签发"按钮
- 验证项（M5）：确认目标 k8s 版本下 legacy token 撤销的即时性，以及审计日志能否支持"最后使用时间"展示

v1 不做 OIDC，身份就是 ServiceAccount。接入企业 IdP 列入路线图。

## 7. Webhook 与权限

- Validating：VirtualCluster（不变量、valuesOverride 白名单、删除前节点已全部释放）、NodePool（nodeSelector 不重叠）
- Host 侧 ValidatingAdmissionPolicy：`vc-*` 命名空间中 Pod 的 nodeSelector 约束（见第 2 节）
- host 上 operator 的 ServiceAccount 需要：nodes（patch 标签与污点）、pods/eviction、namespaces、Flux HelmRelease / OCIRepository、自身 CRD；只读 `vc-*` 中 vcluster 生成的 kubeconfig Secret，不做全局 secret 读取
