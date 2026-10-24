# 路线图

每个里程碑都以"能在开发环境（10.243.144.51-53 管理节点、10.243.145.103/105/106 GPU 节点）端到端演示"为验收标准。

## M0 设计（当前）

- [x] 架构、CRD、catalog 格式、GUI 信息架构文档
- [x] API Group 确定为 `kubeyard.wutz.dev`
- [x] catalog 只以 OCI 发布到 GHCR
- [x] 接受 mygo 1.0 前的风险
- [x] 不引入租户实体，以虚拟集群为单位设计
- [x] 普通用户 kubeconfig 不过期、可撤销
- [ ] 设计 PR 合并

## M1 连接与 catalog

- GUI：多 kubeconfig / context 管理，集群概览（节点、GPU、Pod）
- 一键安装 Flux（flux-operator），添加 OCI catalog 源（GHCR，cosign 校验）
- catalog 仓库：从 k8s-in-action 迁移 host profile（nfd、spegel、metallb、cert-manager、istio、gpu-operator、metrics-server）
- 组件页：浏览、按 schema 填参数、安装 / 升级 / 卸载、查看 Flux 状态与事件
- 技术验证：mygo 概览页与节点表格（用于兜底判断）

## M2 虚拟集群与资源池

- kubeyard-operator host 模式：VirtualCluster、NodePool、NodeLease
- vcluster 独占节点模式，手动 desired + pinnedNodes
- 导出管理员 kubeconfig（LoadBalancer / Ingress:7443）
- GUI：虚拟集群、资源池页面，节点拖拽分配

## M3 弹性与抢占

- autoscale（Pending Pod 驱动）、空闲缩容
- 跨 VC 优先级抢占、drain 流程、dry-run 预览
- VC 管理员侧可见的"即将回收"提示

## M4 计量

- operator 指标、每日 UsageReport、CSV 导出
- 可选 VictoriaMetrics + dcgm 利用率维度
- GUI：计量页、单价表

## M5 虚拟集群管理员体验

- VC profile：kueue、trainer v2、agent-sandbox、lws
- kubeyard-operator vcluster 模式：Workspace（namespace、RBAC、quota、netpol）
- 用户 kubeconfig 签发与撤销（长期 token，撤销即时性验证）
- GUI：用户空间页、签发 / 撤销流程

## 之后

- k3k provider（shared / virtual 模式），以及 vcluster 共享节点模式
- OIDC / 企业 IdP 接入，替代 ServiceAccount token
- 节点归还前清理钩子（重启、擦盘）
- CLI（`kubeyard`）复用同一套 API，便于 GitOps 与脚本化
