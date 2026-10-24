# 路线图

每个里程碑都以"能在开发环境（10.243.144.51-53 管理节点、10.243.145.103/105/106 GPU 节点）端到端演示"为验收标准。

## M0 设计（当前）

- [x] 架构、CRD、catalog 格式、GUI 信息架构文档
- [x] API Group 确定为 `kubeyard.wutz.dev`
- [x] catalog 只以 OCI 发布到 GHCR
- [x] 接受 mygo 1.0 前的风险
- [x] 不引入租户实体，以虚拟集群为单位设计
- [x] 普通用户 kubeconfig 不过期、可撤销
- [x] 设计 PR 合并

## M1 连接与 catalog

- GUI：多 kubeconfig / context 管理，集群概览（节点、GPU、Pod）
- 一键安装 Flux（flux-operator），添加 OCI catalog 源（GHCR，cosign 校验）
- catalog 仓库：从 k8s-in-action 迁移 host profile（nfd、spegel、metallb、cert-manager、istio、gpu-operator、metrics-server）
- 组件页：浏览、按 schema 填参数、安装 / 升级 / 卸载、查看 Flux 状态与事件
- 技术验证：mygo 概览页与节点表格（用于兜底判断）

状态（M1 收尾）：

- [x] 概览、节点表格、kubeconfig context 切换
- [x] catalog 仓库：8 个 host 组件，cosign 签名发布到 GHCR，参数归属由 `x-kubeyard-target` 标记
- [x] 组件页：只读列表（已安装 / 已存在 / 未安装 / 无法判断）、参数表单、安装 / 卸载（二次确认，守卫在写入前复查）
- [x] 组件的 Flux 状态与最近事件（仅 Kubeyard 管理的组件）
- [x] 健康检查支持 CRD（`CustomResourceDefinition`），gateway-api-crds 据此判断
- [x] 参数更新：表单以集群中存储的参数为初值，只对 Kubeyard 管理的组件开放
- [x] 一键安装 Flux：Helm SDK 从 OCI 拉取 flux-operator 0.61.0，再创建 FluxInstance；已有 Flux 时拒绝
- [x] 添加 catalog 源：公钥随二进制嵌入，创建校验签名的 OCIRepository；源已存在或 Flux 未安装时不显示
- 版本升级不作为 GUI 操作：OCIRepository 跟随 stable tag 由 Flux 自动拉取（范围决定，见 catalog.md）
- [ ] 写入路径的端到端验证（k3d 集群，bz1-dev1 未触碰）：Flux 安装、重复安装拒绝、添加 catalog 源（cosign 校验通过）、安装 nfd（状态正确识别为已安装）、重复安装拒绝、卸载（Kustomization、参数、HelmRelease、Helm release 与工作负载全部移除）均已通过；参数更新只写入 ConfigMap，因 catalog 内联 values 覆盖而不生效，需修复 catalog（见 catalog.md 已知问题）。bz1-dev1 上的验证仍需明确授权

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
