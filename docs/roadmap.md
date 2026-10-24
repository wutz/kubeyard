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
- 技术验证：webview 概览页与节点表格（GUI 实现改为 webview，见 [GUI 设计](gui.md) §5）

状态（M1 以未完成状态关闭）：

- [x] 概览、节点表格、kubeconfig context 切换
- [x] catalog 仓库：8 个 host 组件，cosign 签名发布到 GHCR，参数归属由 `x-kubeyard-target` 标记
- [x] 组件页：只读列表（已安装 / 已存在 / 未安装 / 无法判断）、参数表单、安装 / 卸载（二次确认，守卫在写入前复查）
- [x] 组件的 Flux 状态与最近事件（仅 Kubeyard 管理的组件）
- [x] 健康检查支持 CRD（`CustomResourceDefinition`），gateway-api-crds 据此判断
- [x] 参数更新：表单以集群中存储的参数为初值，只对 Kubeyard 管理的组件开放
- [x] 一键安装 Flux：Helm SDK 从 OCI 拉取 flux-operator 0.61.0，再创建 FluxInstance；已有 Flux 时拒绝
- [x] 添加 catalog 源：公钥随二进制嵌入，创建校验签名的 OCIRepository；源已存在或 Flux 未安装时不显示
- 版本升级不作为 GUI 操作：OCIRepository 跟随 stable tag 由 Flux 自动拉取（范围决定，见 catalog.md）
- [x] GUI 改为 webview：本地 loopback 服务 + 系统 webview，token / Host / CSP 保护，业务逻辑沿用 `pkg/`
- [ ] 写入路径的端到端验证未完成（M1 未完成关闭的原因）。
  - 2026-10-24，GUI 在 k3d 上验证（headless 服务，经 API 与浏览器页面操作）：`/api/contexts`、`/api/overview`、`/api/catalog` 返回正确；守卫拒绝重复安装 Flux、重复添加 catalog 源、重复安装 nfd、卸载非 Kubeyard 组件、未知组件，错误 token 返回 401，外部 Host 返回 403。卸载 nfd 后经 API 重新安装，Flux 协调到 Ready；参数更新经 API 与组件页两种方式都在 Deployment 上生效（nfd master 2→3→2）。
  - 发现并修复：参数 ConfigMap 未带 Flux 的 `reconcile.fluxcd.io/watch=Enabled` 标签，参数更新要等 Kustomization / HelmRelease 的 10 分钟间隔才生效（手动 reconcile 即时生效）。现在安装与更新都写入该标签，k3d 上更新约 5 秒生效，由 `TestInstallLabelsParamsForFluxWatch` 覆盖。
  - 发现并修复：Kubeyard 更新 Kustomization 时整对象 Update 会丢掉 Flux 的 `finalizers.fluxcd.io`，之后卸载会跳过 prune，HelmRelease 与工作负载残留（2026-10-24 在 k3d 复现：卸载后 HelmRelease 仍被协调，参数 ConfigMap 已删，Helm 回退为 chart 默认值）。修复：更新时保留现有 finalizer，由 `TestInstallKeepsFluxFinalizer` 覆盖。修复后同一流程（安装 → 更新 → 卸载）在 k3d 上 20 秒内 prune 全部对象，重装后恢复 Ready。
  - 观察并定位：卸载后 `nfd` namespace 仍保留。原因是 catalog 的 nfd 条目带 Helm `install.createNamespace: true`，namespace 由 Helm 创建，不带 Flux 归属，Kustomization prune 只删除 HelmRelease 与 Helm release secret。2026-10-24 在 k3d 上验证：删除 Kustomization `addon-nfd` 后 HelmRelease 与 Helm secret 消失，namespace `nfd` 仍为 Active。同样设置了 `createNamespace` 的还有 cert-manager、forgeai、gpu-operator、istio、metallb、metrics-server、spegel。
  - 待决定（未修改）：(A) 改为由 Flux 管理 Namespace 对象（`createNamespace: false`），卸载即删除 namespace；但 namespace 里可能有用户数据，需逐个评估，bz1-dev1 的 `forge-demo` 就是例子。(B) 保留 namespace，在文档和卸载确认框中说明。
  - 原生窗口（2026-10-24）：macOS 本机非 headless 启动后窗口标题 Kubeyard、尺寸 1120×720 出现，WebKit 网络进程连接本地服务并加载页面。本机截屏为黑屏（未授予屏幕录制权限），因此当时未目视确认。
  - 原生窗口 CI 检查（[#9](https://github.com/wutz/kubeyard/pull/9)、[#10](https://github.com/wutz/kubeyard/pull/10)、[#11](https://github.com/wutz/kubeyard/pull/11)）：ubuntu-22.04（xvfb）、macOS、Windows 三个 runner 上均启动 GUI 窗口，本地服务返回带 token 的页面，进程保持运行，并截屏上传。截屏显示 Kubeyard 侧边栏、概览 / 组件导航与"未找到 kubeconfig context"空状态；Linux 截屏中的中文显示为方框，是 runner 缺少中文字体，不是应用问题。
  - 真实数据（Linux）：CI 的 `gui-live-cluster` job 用 helm/kind-action 起 kind 集群，运行 `.github/scripts/gui-smoke.sh`，并设置 `KUBEYARD_CHECK_OVERVIEW=1`，断言 `/api/overview` 的 `nodesTotal ≥ 1` 且 `nodesReady ≥ 1`，然后截屏（`gui-kind.png`，[#13](https://github.com/wutz/kubeyard/pull/13)）。截屏显示窗口内的真实节点与 Pod 数。
  - 未验证：macOS 与 Windows 窗口内的真实数据。这两个 runner 没有集群，只覆盖空状态。M1 关闭前需补齐。
  - CI（[#7](https://github.com/wutz/kubeyard/pull/7)）：ubuntu-22.04、macOS、Windows 上 gofmt、vet、build、test 全部通过，GUI 的 cgo 构建包含在内。Windows 首次失败是 checkout 的 CRLF 让 gofmt 报错，已用 `.gitattributes` 固定 LF 修复。这三项已设为 main 的必需检查。
  - k3d 上已通过：Flux 安装、重复安装拒绝、添加 catalog 源（cosign 校验通过）、安装 nfd（状态识别为已安装）、重复安装拒绝、卸载（Kustomization、参数、HelmRelease、Helm release 与工作负载全部移除）。参数更新只写入 ConfigMap，因 catalog 内联 values 覆盖而不生效；修复已发布为 catalog v2026.10.4（k3d 上 nfd 2→1→3 生效）。
  - bz1-dev1 上：仅新增了 Flux 与 catalog 源（由本项目创建）。nfd、gpu-operator 等为非 Kubeyard 安装，只读检查完成，未修改。
  - bz1-dev1 上 forgeai 的手动卸载已执行：Project `forge-demo/demo`、forge-demo 的 5 个 Sandbox、forgeai CRD 与 RBAC 已删除。`forge-demo` 命名空间与 3 个 PVC（130Gi）因 Project 的 `deletionPolicy: Retain` 保留。
  - forgeai 重装：chart 0.3.0 把 `command` 写死为 `/manager`，而发布镜像的入口是 `/ko-app/cmd`，控制器 CrashLoop。修复在 [wutz/forgeai#58](https://github.com/wutz/forgeai/pull/58)（已合并），发版 [chart 0.3.1](https://github.com/wutz/forgeai/releases/tag/v0.3.1)（[#59](https://github.com/wutz/forgeai/pull/59)）。bz1-dev1 已于 0.3.1 重新安装 forgeai（`helm install` 从 OCI，控制器 1/1 Running，镜像 `ghcr.io/wutz/forgeai:0.3.1`）。0.3.1 chart 不含 CRD `inferenceservices.forgeai.wutz.dev`；bz1-dev1 上曾出现的该 CRD 来源不明（与 0.3.1 安装同一分钟创建），已在第二轮卸载时删除。catalog 的卸载警告目前只列出 `projects` CRD，需补全 `fileservers`、`instancetypes`。
  - 发现：chart 的 CRD 带 `helm.sh/resource-policy: keep`，`helm uninstall` 后 CRD 保留，需手动清理。卸载 forgeai 时 Kubeyard 的警告应说明这一点。
  - 第二轮升级、卸载测试（bz1-dev1）：`catalog-default` 强制协调，从 v2026.10.4（`sha256:49c4c8…`）升至 stable / v2026.10.5（`sha256:bdbbe84b…`），SourceVerified=True。`helm uninstall forgeai` 后 Deployment、Pod、ClusterRole 已移除；CRD 按 keep 策略保留，手动删除 projects、fileservers、instancetypes、inferenceservices（无 CR，无 finalizer）；agent-sandbox（agents.x-k8s.io）与 kueue 等 CRD 未动。重装 0.3.1（沿用同一 values），控制器 1/1 Running，4 个 InstanceType 由控制器重建。`helm upgrade` 同版本（revision 2，仅加 pod label 触发滚动更新）成功，`forgeai-platform` ConfigMap 内容不变。0.3.1 之上没有可升级的新 chart，因此升级测试只覆盖同版本原地升级，跨版本升级需等新 chart 发布。
  - k3d 集群 `kubeyard-gui` 为干净状态（无 Flux、无 catalog 源），GUI 验证从头开始。
  - GUI 验证结果未提供。
  - catalog 的 forgeai 与 agent-sandbox 条目已合并（[wutz/kubeyard-catalog#4](https://github.com/wutz/kubeyard-catalog/pull/4)），并以 `v2026.10.5` 发布；stable 指向该版本，cosign 校验通过，forgeai 为 chart 0.3.1。bz1-dev1 的 catalog 源已于第二轮升至 v2026.10.5。

## M2 虚拟集群与资源池

- [x] NodePool 状态计算（`pkg/pool`）：total / free / reserved / unhealthy；空选择器与非法选择器拒绝，有单元测试
- [x] 分配算法（`pkg/alloc`）：期望值（§3.1）、准入不变量（§3.2）、分配与抢占顺序（§3.3），纯函数，有单元测试覆盖每条规则（[#14](https://github.com/wutz/kubeyard/pull/14)）
  - 待确认：§3.2 称 min 永远可满足，§3.3 只允许更低优先级抢占。当前实现对 min 缺口也只抢更低优先级的弹性节点，满足不了时体现为 Shortfall。若要求 min 优先于同级，需先修改 §3.3
- [ ] kubeyard-operator host 模式：VirtualCluster、NodePool、NodeLease（operator 骨架、CRD、协调循环未开始）
  - [x] API 类型与 scheme（`pkg/api/v1alpha1`）：三个 kind 与 `operator.md` §1 的示例 JSON 一致，DeepCopy 手写并有独立性测试。CRD manifest 与控制器未开始
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
