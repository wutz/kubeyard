# 虚拟集群与资源池

虚拟集群（VirtualCluster，VC）是本项目的核心对象。每个 VC 有自己的控制面，工作负载运行在分配给它的独占节点上。配额、优先级、节点、计量、凭证都挂在 VC 上。

## 1. 形态选择

虚拟 k8s 集群在"控制面是否独立"和"数据面节点是否独占"两个维度上有不同组合。参照 k3k 的划分：

| 形态 | 控制面 | 工作负载节点 | 隔离强度 | Kubeyard |
|---|---|---|---|---|
| 共享模式（shared） | 每个 VC 独立，Pod 形式跑在 host | 与其他 VC 共享 host 节点 | 弱（同节点内核、GPU 共享） | 路线图 |
| 共享模式 + 独占节点 | 每个 VC 独立，Pod 形式跑在 host | host 节点按 VC 划分，互不混用 | 中强（节点级隔离） | **v1（vcluster）** |
| 虚拟模式（virtual） | 每个 VC 独立 | 每个 VC 独立的 k3s agent Pod 作为节点 | 中 | 路线图（k3k） |

v1 只做"共享模式 + 独占节点"：控制面（api-server、syncer、etcd）以 Pod 形式跑在 host 的 `vc-<name>` 命名空间；VC 内的工作负载由 syncer 同步到 host，只能落在分配给该 VC 的节点上。GPU 集群多 VC 的主要诉求是"节点不混用、可计量、可伸缩"，这个形态以最小代价满足。

### 两个实现的对应关系

| | vcluster（v1） | k3k（路线图） |
|---|---|---|
| 模式字段 | 由 values 组合决定 | `Cluster.spec.mode`：`shared` / `virtual` / `hcp`，创建后不可改 |
| 独占节点 | `sync.fromHost.nodes.selector.labels`：只同步匹配的 host 节点，同时把该 selector 作为 nodeSelector 注入同步到 host 的 Pod | `Cluster.spec.nodeSelector` + `VirtualClusterPolicy.defaultNodeSelector`；Policy 强制生效，VC 管理员改 Cluster 也放不宽 |
| 策略对象 | 无，Kubeyard 用 VirtualCluster CR + 准入策略补齐 | `VirtualClusterPolicy`（cluster 级）：quota、limit、PSA 级别、允许的模式、网络策略开关 |
| 安装 | `oci://ghcr.io/loft-sh/charts/vcluster`（Apache-2.0） | `https://rancher.github.io/k3k`，CRD 组 `k3k.io/v1beta1` |
| 注意 | 同时有第三种 "private nodes"（外部机器直接加入 VC，自带 CNI / CSI），但需要 vCluster Platform（免费档即可），**Kubeyard 不依赖** | `virtual` 模式下 agent Pod 在 host 上以 privileged 运行，不适合不可信负载，除非配合 `runtimeClassName: kata` |

## 2. vcluster 落地方式

现有 k8s-in-action（`compute/vcluster/vcluster-demo`，chart 0.27.0）已经验证的做法：

- 用 NFD `NodeFeatureRule` 按节点名打 `scclabs.dev/node-pool=<vcluster>` 标签
- vcluster values 设置 `sync.fromHost.nodes.selector.labels`，VC 只看到这些节点
- `controlPlane.service`（MetalLB `loadBalancerIPs`）或 `controlPlane.ingress` 暴露 API，`exportKubeConfig.server` 写对外地址
- `policies.networkPolicy`、`policies.limitRange`、`policies.resourceQuota` 由 chart 生成
- etcd 3 副本 + 控制面 2 副本，控制面优先调度到 host 控制节点

Kubeyard 的变化：

| k8s-in-action 手工步骤 | Kubeyard |
|---|---|
| 编辑 NodeFeatureRule 列节点名 | NodePool（按 GPU 型号等标签选候选）+ NodeLease（operator 打 `kubeyard.wutz.dev/*` 标签与污点），不再依赖 NFD 规则做分配 |
| 只靠 node selector 让 VC "看见"节点 | 额外加污点 + 强制容忍 + host 侧准入策略，防止越界调度（见 [operator.md 第 2 节](operator.md#2-节点绑定机制独占节点)） |
| 改 5 个文件里的名字 | VirtualCluster CR 一个对象，operator 渲染 HelmRelease |
| `helmwave up` | Flux HelmRelease，chart 来自 catalog |
| 手工 base64 解 Secret | GUI 导出管理员 kubeconfig |

Cilium 相关的 NetworkPolicy 经验（DNS / LB 流量需 ports-only 规则）保留到 vcluster addon 的默认 values 中。

## 3. 扩缩容与 vcluster 的交互

- 节点加入：NodeLease Bound → 节点打上 `kubeyard.wutz.dev/vcluster` 标签 → syncer 自动把节点同步进 VC，VC 管理员 `kubectl get nodes` 立即可见
- 节点移除：Draining 时先打 `kubeyard.wutz.dev/draining` 污点 → 驱逐 → 摘标签后 syncer 将节点从 VC 移除
- VC 内看到的节点污点、标签由 syncer 决定，需要确认 draining 污点能被同步进去，以便 VC 内的 Kueue 等调度器感知（实现阶段验证）

## 4. provider 抽象

operator 内部定义 provider 接口，v1 只实现 vcluster：

```go
type Provider interface {
    // 根据 VirtualCluster 与当前分配渲染安装对象（HelmRelease 等）
    Render(vc *v1alpha1.VirtualCluster, alloc Allocation) ([]client.Object, error)
    // 从 provider 的状态中读出就绪情况、端点、管理员 kubeconfig Secret
    Observe(ctx context.Context, vc *v1alpha1.VirtualCluster) (Status, error)
    // 节点选择所需的 label key，用于 NodeLease 投影
    NodeSelectorLabels(vc *v1alpha1.VirtualCluster) map[string]string
}
```

k3k 接入时实现同一接口，`mode` 字段扩展为 `shared` / `virtual`。

## 5. 为什么 selector 不够

vcluster 的 node selector 只负责"吸引"：让 VC 的 Pod 落到自己的节点上，不能阻止其他 Pod 落到这些节点。所以 Kubeyard 额外做三件事：

1. 节点打 `kubeyard.wutz.dev/vcluster=<name>:NoSchedule` 污点，挡住其他 VC 与 host 上的普通负载
2. vcluster 用 `sync.toHost.pods.enforceTolerations` 给本 VC 的 Pod 自动加上对应容忍
3. host 侧 ValidatingAdmissionPolicy 拒绝 `vc-*` 命名空间中 nodeSelector 不属于本 VC 的 Pod（VC 内用户手写的 nodeSelector 与注入值的合并行为随版本变化，不能只靠 vcluster）

字段名以 catalog 中固定的 vcluster 版本为准（调研时文档最新为 v0.36，k8s-in-action 当前用 0.27.0），实现阶段对固定版本跑一遍越界调度测试。
