# Kubeyard

用一个桌面客户端管理 GPU Kubernetes 集群的多租户：底层集群管理员开通租户、分配独占节点、计量用量；租户管理员在自己的虚拟集群里安装 AI 组件、给普通用户开通受限的 kubeconfig。

> 状态：架构设计阶段，尚未开始编码。设计评审通过后进入 M1。

## 三种角色，一个客户端

| 角色 | 连接对象 | 能做什么 |
|---|---|---|
| 平台管理员 | 底层（host）k8s 集群 | 安装 Flux 与基础组件（nfd、spegel、metallb、istio、cert-manager、gpu-operator…）；开通租户与虚拟集群；管理节点资源池、优先级与抢占；查看计量；导出租户 kubeconfig |
| 租户管理员 | 自己的虚拟 k8s 集群（vcluster） | 安装 kueue、trainer v2、agent-sandbox 等；为普通用户开通 namespace 级 kubeconfig（RBAC + quota + networkpolicy） |
| 普通用户 | 虚拟集群中的 namespace | 用 kubectl 或 Kubeyard 只读查看自己空间的资源与配额 |

## 核心组件

- **Kubeyard GUI**：基于 [mygo](https://github.com/egoist/mygo) native UI 的桌面客户端，直接用 kubeconfig 访问 API Server，无需额外服务端
- **kubeyard-operator**：在集群内协调租户、虚拟集群、节点租约、抢占、计量与用户空间
- **kubeyard-catalog**：以签名 OCI artifact 发布的 Flux 组件目录，默认源基于 [k8s-in-action](https://github.com/wutz/k8s-in-action)，组织可发布自己的源

## 文档

- [架构总览](docs/architecture.md)
- [Catalog 与 Flux 源](docs/catalog.md)
- [虚拟集群与资源池](docs/virtual-cluster.md)
- [Operator 设计（CRD、调度、抢占、计量）](docs/operator.md)
- [GUI 设计（信息架构与页面）](docs/gui.md)
- [路线图](docs/roadmap.md)
- [视觉规范](DESIGN.md)

## 许可

Apache-2.0
