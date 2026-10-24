# 路线图

每个里程碑都以"能在开发环境（10.243.144.51-53 管理节点、10.243.145.103/105/106 GPU 节点）端到端演示"为验收标准。

## M0 设计（当前）

- [x] 架构、CRD、catalog 格式、GUI 信息架构文档
- [ ] 设计评审，确认 API Group 域名、默认 catalog 地址、mygo 选型风险

## M1 连接与 catalog

- GUI：多 kubeconfig / context 管理，集群概览（节点、GPU、Pod）
- 一键安装 Flux（flux-operator），添加 catalog 源（OCI 优先，Git 兼容）
- catalog 仓库：从 k8s-in-action 迁移 host profile（nfd、spegel、metallb、cert-manager、istio、gpu-operator、metrics-server）
- 组件页：浏览、按 schema 填参数、安装 / 升级 / 卸载、查看 Flux 状态与事件

## M2 租户与虚拟集群

- kubeyard-operator host 模式：Tenant、VirtualCluster、NodePool、NodeLease
- vcluster 独占节点模式，手动 desired + pinnedNodes
- 导出租户 kubeconfig（LoadBalancer / Ingress:7443）
- GUI：租户、虚拟集群、资源池页面，节点拖拽分配

## M3 弹性与抢占

- autoscale（Pending Pod 驱动）、空闲缩容
- 跨租户优先级抢占、drain 流程、dry-run 预览
- 租户侧可见的"即将回收"提示

## M4 计量

- operator 指标、每日 UsageReport、CSV 导出
- 可选 VictoriaMetrics + dcgm 利用率维度
- GUI：计量页、单价表

## M5 租户管理员体验

- tenant profile：kueue、trainer v2、agent-sandbox、lws
- kubeyard-operator tenant 模式：Workspace（namespace、RBAC、quota、netpol、TokenRequest kubeconfig）
- GUI：用户空间页、kubeconfig 签发与续期

## 之后

- k3k provider（shared / virtual 模式），以及 vcluster 共享节点模式
- OIDC / 企业 IdP 接入，替代 ServiceAccount token
- 节点归还前清理钩子（重启、擦盘）
- CLI（`kubeyard`）复用同一套 API，便于 GitOps 与脚本化
