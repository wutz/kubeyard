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

状态（2026-10-25 关闭：写入路径与即将回收提示已在 k3d 验证，卸载残留的决定与 catalog 发布已由用户确认）：

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
- [x] GUI 改为 Wails v2.17.0：去掉 loopback HTTP 服务与 token / Host / CSP 守卫，页面经 Wails 绑定调用 `App` 方法（不开端口）；管理员 kubeconfig 导出改为原生保存对话框，凭证由 Go 写入文件、不进入页面；业务逻辑沿用 `pkg/`。上一条 webview 记录保留为历史
- [x] 写入路径的端到端验证
  - 2026-10-25，k3d 全新集群，`gui/live_write_test.go`（设置 `KUBEYARD_LIVE_WRITE` 时运行，经 `App` 绑定）：安装 Flux、添加 catalog 源、安装 nfd（replicaCount 2，状态为已安装，卸载警告写出 namespace）、参数更新为 3（Deployment 3/3）、卸载（Kustomization、HelmRelease、工作负载全部移除），卸载后 `nfd` namespace 保留，与卸载警告一致。
  - 2026-10-24，GUI 在 k3d 上验证（headless 服务，经 API 与浏览器页面操作）：`/api/contexts`、`/api/overview`、`/api/catalog` 返回正确；守卫拒绝重复安装 Flux、重复添加 catalog 源、重复安装 nfd、卸载非 Kubeyard 组件、未知组件，错误 token 返回 401，外部 Host 返回 403。卸载 nfd 后经 API 重新安装，Flux 协调到 Ready；参数更新经 API 与组件页两种方式都在 Deployment 上生效（nfd master 2→3→2）。
  - 发现并修复：参数 ConfigMap 未带 Flux 的 `reconcile.fluxcd.io/watch=Enabled` 标签，参数更新要等 Kustomization / HelmRelease 的 10 分钟间隔才生效（手动 reconcile 即时生效）。现在安装与更新都写入该标签，k3d 上更新约 5 秒生效，由 `TestInstallLabelsParamsForFluxWatch` 覆盖。
  - 发现并修复：Kubeyard 更新 Kustomization 时整对象 Update 会丢掉 Flux 的 `finalizers.fluxcd.io`，之后卸载会跳过 prune，HelmRelease 与工作负载残留（2026-10-24 在 k3d 复现：卸载后 HelmRelease 仍被协调，参数 ConfigMap 已删，Helm 回退为 chart 默认值）。修复：更新时保留现有 finalizer，由 `TestInstallKeepsFluxFinalizer` 覆盖。修复后同一流程（安装 → 更新 → 卸载）在 k3d 上 20 秒内 prune 全部对象，重装后恢复 Ready。
  - 观察并定位：卸载后 `nfd` namespace 仍保留。原因是 catalog 的 nfd 条目带 Helm `install.createNamespace: true`，namespace 由 Helm 创建，不带 Flux 归属，Kustomization prune 只删除 HelmRelease 与 Helm release secret。2026-10-24 在 k3d 上验证：删除 Kustomization `addon-nfd` 后 HelmRelease 与 Helm secret 消失，namespace `nfd` 仍为 Active。同样设置了 `createNamespace` 的还有 cert-manager、forgeai、gpu-operator、istio、metallb、metrics-server、spegel。
  - 决定（2026-10-25，用户确认 B）：卸载后保留由 Helm 创建的 namespace，并在卸载确认框与文档中说明残留的 namespace 名称和原因。已实现（`UninstallWarning` 按 Helm 组件写出残留的 namespace 与 keep 策略 CRD，`docs/catalog.md` 说明手动删除方式；`TestUninstallWarningKeepsHelmLeftovers` 覆盖）。
    - 理由：(A) 由 Flux 管理 Namespace 对象（`createNamespace: false`），卸载即删除 namespace，这是不可逆的删除。namespace 里可能有用户数据，bz1-dev1 的 `forge-demo` 就是例子：其中 3 个 PVC（130Gi）因 Project 的 `deletionPolicy: Retain` 而保留。(B) 可逆，代价只是残留一个空 namespace。
    - (B) 的范围：卸载确认框列出将保留的 namespace；文档说明如何手动删除；不做自动删除。
    - 若用户选择 (A)：改动在 catalog 的 `createNamespace` 与 Kubeyard 的安装逻辑中，并需要先逐个评估各 namespace 中是否有用户数据。
  - 原生窗口（2026-10-24）：macOS 本机非 headless 启动后窗口标题 Kubeyard、尺寸 1120×720 出现，WebKit 网络进程连接本地服务并加载页面。本机截屏为黑屏（未授予屏幕录制权限），因此当时未目视确认。
  - 原生窗口 CI 检查（[#9](https://github.com/wutz/kubeyard/pull/9)、[#10](https://github.com/wutz/kubeyard/pull/10)、[#11](https://github.com/wutz/kubeyard/pull/11)）：ubuntu-22.04（xvfb）、macOS、Windows 三个 runner 上均启动 GUI 窗口，本地服务返回带 token 的页面，进程保持运行，并截屏上传。截屏显示 Kubeyard 侧边栏、概览 / 组件导航与"未找到 kubeconfig context"空状态；Linux 截屏中的中文显示为方框，是 runner 缺少中文字体，不是应用问题。
  - 真实数据（Linux）：CI 的 `gui-live-cluster` job 用 helm/kind-action 起 kind 集群，运行 `.github/scripts/gui-smoke.sh`，并设置 `KUBEYARD_CHECK_OVERVIEW=1`，断言 `/api/overview` 的 `nodesTotal ≥ 1` 且 `nodesReady ≥ 1`，然后截屏（`gui-kind.png`，[#13](https://github.com/wutz/kubeyard/pull/13)）。截屏显示窗口内的真实节点与 Pod 数。
  - 窗口数据（macOS、Windows）：三个 runner 的 GUI 步骤都经 `.github/scripts/with-fake-cluster.sh` 启动 `cmd/fakeapiserver`（假的 API server，不是真实集群，只返回一个 Ready 节点和两个 Running Pod），并设置 `KUBEYARD_CHECK_OVERVIEW=1` 断言 `/api/overview` 的 `nodesTotal ≥ 1` 且 `nodesReady ≥ 1`，Windows 用 PowerShell 做同样断言。截屏与 overview.json 上传。这证明 macOS 与 Windows 上窗口确实读到 API 返回的数据并显示。
  - 未验证：macOS 与 Windows 连接真实集群时窗口内的数据。真实集群的数据目前只在 Linux 的 kind 上验证。
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
- [x] kubeyard-operator host 模式：VirtualCluster、NodePool、NodeLease
  - [x] API 类型与 scheme（`pkg/api/v1alpha1`）：三个 kind 与 `operator.md` §1 的示例 JSON 一致，DeepCopy 手写并有独立性测试。
  - [x] CRD manifest（`deploy/crds/`，[#20](https://github.com/wutz/kubeyard/pull/20)）：字段与枚举由测试对齐 Go 类型；§1 的三个示例 CR 在本地 k3d 上 server-side dry-run 通过。
  - [x] 协调循环（`pkg/operator/host`，[#17](https://github.com/wutz/kubeyard/pull/17)）与入口（`cmd/kubeyard-operator --mode=host`）：计算 NodePool.status，按 §3 分配，写 NodeLease、节点 VC 标签 / pool 标签 / NoSchedule taint（§2），回写 VC allocations。`--mode=vcluster` 明确报错，未实现。RBAC 在 `deploy/operator/rbac.yaml`。
  - 2026-10-25 本地 k3d 实测（单节点 NodePool）：min=0 的 VC 获得空闲节点，lease 为 Bound，节点标签与 taint 正确，VC allocations 正确；更高优先级的不可抢占 VC 到达后，节点的 lease 转为该 VC、reason=Preempt、phase=Draining、drainDeadline 为 grace 之后。operator 日志无错误。
  - [x] drain 步骤（`pkg/operator/host/drain.go`，§3.4 步骤 1、3、4、5）：被抢占或被释放的节点先加 `kubeyard.wutz.dev/draining:NoSchedule`；宽限期结束后用 Eviction API 驱逐旧 VC 在 `vc-<name>` 中的 Pod（受 PDB 约束，被拒绝则下一轮重试）；到 drain 截止时间（宽限期 + drainTimeout）强制删除；节点清空后，被抢占的节点移交给申请者（lease Bound，标签与 taint 换成新 VC），被释放的节点解绑并删除 lease。单元测试 4 个，覆盖宽限期、驱逐后移交、PDB 阻塞后强制删除、释放后解绑再分配。
  - 2026-10-25 本地 k3d 实测（真实 API，operator 二进制以 admin kubeconfig 运行）：抢占，无 PDB，宽限期 30s 后 Pod 被驱逐，节点在 t+31s 移交给 high；PDB `minAvailable: 1` 时驱逐被拒绝，Pod 保留到截止时间（30s + 90s）后被强制删除，节点移交给 top；释放 top 后节点解绑，分配器随即按优先级把节点给了 high（而不是 low）。operator 日志无错误。
  - §3.4 步骤 2 已实现（见 M3）；已知缺口：释放的租约直接删除，未写 UsageReport（§1），归 M4；`--mode=vcluster` 未实现；draining 节点每 10 秒轮询一次，不监听 Pod 事件。
  - [x] 准入 webhook（`pkg/webhook`，§3.2、§7）：VirtualCluster 校验 `sum(min) ≤ total − unhealthy`、`min ≤ desired ≤ max`（开启 autoscale 时跳过）、pinnedNodes 存在且匹配选择器、每个节点只被一个 VC pin、poolClaim 的 pool 不重复且存在，valuesOverride 不得触碰 `sync.fromHost.nodes` 与 `sync.toHost.pods`（§2 保留路径），仍持有租约的 VC 拒绝删除；NodePool 校验选择器非空且合法、不与其它池重叠、不把已租出的节点移出选择器。证书自签发（ECDSA P-256，本地 CA + 服务证书，30 天内到期或名字不全时重签），启动时把 CA 写入 `kubeyard-validating` 的 caBundle，不依赖 cert-manager。准入判断直读 API 而非缓存，避免用旧缓存放过超额申请。单元测试 30 个。
  - 2026-10-25 本地 k3d 实测（operator 二进制在宿主机运行，webhook 配置用 `url` 指向 `host.k3d.internal:9443`）：`min=2` 的 VC 在 1 节点池上被拒（"各虚拟集群 min 之和 3 超过健康节点数 1"）；仍持有 Bound 租约的 VC 删除被拒；valuesOverride 写 `sync.fromHost.nodes` 被拒；把已租出的节点移出 NodePool 选择器被拒；合法的 VC（min=1）与 NodePool 创建放行；按拒绝信息把 min/desired 调 0 后租约 Draining → 释放，随后删除放行。operator 日志无错误。
  - 未验证：bz1-dev1 上的运行（只读，未部署）；多节点与 GPU 节点的真实分配；集群内部署形态（Service + 容器内证书目录）未经真实安装验证，只在宿主机上验证了同样的代码路径。
- vcluster 独占节点模式，手动 desired + pinnedNodes
- [x] 导出管理员 kubeconfig（LoadBalancer / Ingress:7443）：后端与 GUI 页面已完成，vcluster 生成 Secret 未完成
  - [x] `pkg/kube`：从 `status.adminKubeconfigSecretRef` 读取 Secret 的键；未生成、键缺失、值为空都报错；导出前必须写入审计 Event（`AdminKubeconfigExported`，写在 `default` 命名空间，因为 VirtualCluster 是 cluster 级对象）。单元测试 7 个。
  - [x] `POST /api/virtualclusters/{name}/admin-kubeconfig`：需要 `confirm` 与名称一致，响应 `Cache-Control: no-store`，审计 Event 写入失败时不返回凭证。测试覆盖 token 与确认。
  - 2026-10-25 本地 k3d 实测：手动创建 Secret 与 status 引用，GUI headless 运行。未确认返回 400；确认后返回 200 与 kubeconfig，审计 Event 写入，`Cache-Control: no-store`。实测中发现并修复：最初把 Event 写入宿主命名空间，API 因 involvedObject 为 cluster 级对象而拒绝（失败时正确地返回 502，且不返回凭证）。
  - 未完成：vcluster 尚未由 operator 生成 admin Secret，因此端到端只能用手动 Secret 验证。
  - [x] 以该 VC 管理员身份打开（`gui/api.go` 的 `OpenVirtualClusterAdmin`，`pkg/kube` 的 `AdminClient`，详情页两步确认）：读取 `status.adminKubeconfigSecretRef` 指向的 Secret，在内存中建立 admin 客户端，读取该 VC 的节点与 Pod，只把概览返回页面，凭证不返回。读取前先写入审计 Event `AdminKubeconfigOpened`，写入失败则不读取凭证。单元测试 3 个，覆盖确认、先记录后读取、记录失败时不读取凭证。
  - 2026-10-26 本地 k3d 实测：用宿主机 kubeconfig 作为 stand-in，放入 `vc-low/low-admin` 并把 `low` 的 status 引用指向它。详情页两步确认后显示 low 的 2 个节点与 11 个 Pod，带"正在以 low 的 cluster-admin 身份查看"提示；`default` 下写入 `AdminKubeconfigOpened` Event（操作来源含 context）；"关闭"后收起。引用指向不存在的 Secret 时页面显示错误，Event 仍已写入，未返回凭证。未验证：vcluster 生成的真实 admin Secret（见上条）。
  - 轮换注解（范围决定，未实现）：`docs/operator.md` §4.1 把 kubeconfig 失效交给 vcluster 自己的证书轮换行为，注释为"版本相关，实现阶段核实"。host 模式不渲染也不安装 vcluster values，写一个没有消费者的注解就是假功能，因此页面只提供导出，不提供轮换按钮。等 render 链路落地再按 vcluster 版本核实语义。
- GUI：虚拟集群、资源池页面，节点拖拽分配
  - [x] 资源池页（`GET /api/pools`）：每个池的 total / free / reserved / unhealthy 与该池上的 VC 声明，池与声明按名称排序；未安装 operator 时说明原因。
  - [x] 虚拟集群页（`GET /api/virtualclusters`，[#26](https://github.com/wutz/kubeyard/pull/26)）：列表按优先级从高到低；卡片显示持有节点数、阶段、优先级 / 是否可抢占、每个池声明的 min/max/desired；点开详情显示 provider、版本、对外地址（Ingress 带 7443）、节点分配表、条件，以及导出管理员 kubeconfig 的两步确认。阶段文案映射 Pending/Provisioning/Ready/Degraded/Draining/Deleting → 等待/创建中/就绪/降级/回收中/删除中。JSON 字段由测试逐个断言，空列表输出 `[]` 而不是 `null`。
  - 2026-10-25 浏览器实测（k3d context，深色与浅色两套主题）：4 个 VC 按优先级 10 → 5 → 3 → 1 排列，vc-demo 卡片点开详情，对外地址显示 `Ingress · vc-demo.example.com:7443`，未分配节点时表格显示"还没有分配节点"；导出按钮先变成"确认导出 vc-demo 的管理员 kubeconfig"，确认后出现成功提示与 kubeconfig 内容，"下载文件"生成 `application/yaml` 的 64 字节 blob；切换 context 后已导出的内容被清空。只读的 bz1-dev1（不写任何东西）上页面显示"未安装 Kubeyard operator"空状态。
  - [x] 节点拖拽分配（`gui/pinning.go`，[#50](https://github.com/wutz/kubeyard/pull/50)）：资源池页把节点拖到 VC 的声明即固定（写 pinnedNodes），拖到"解除固定"区即解除。写入前按 webhook 的规则检查，并拒绝会让 allocator 停转的情形：节点仍被其它 VC 占用或已固定给其它 VC、节点未就绪、不匹配选择器、超过 max。单元测试覆盖 `checkPin` / `checkUnpin` 与池的节点视图。
  - 2026-10-26 本地 k3d 实测（真实 API，operator 宿主机运行，GUI 经 headless 服务与浏览器）：拖到 low 后 pinnedNodes 写入，operator 在 37 秒内把节点租给 low（Bound）；拖到 high 被拒（"已固定给虚拟集群 low"）；节点仍被 high 占用时拖到 low 被拒（"仍由虚拟集群 high 占用，等它释放后再固定"）；不存在的节点被拒；拖到解除区后 pinnedNodes 清空，租约进入 Draining。operator 日志无错误。
  - 未完成：窄屏下侧边栏不折叠（375px 视口出现横向滚动），随 GUI 整体响应式一起处理。

## M3 弹性与抢占

- autoscale（Pending Pod 驱动）、空闲缩容
- 跨 VC 优先级抢占、drain 流程、dry-run 预览
- VC 管理员侧可见的"即将回收"提示
  - [x] operator 侧（`pkg/operator/host/notice.go`，§3.4 步骤 2）：抢占时在受害 VC 上写 Warning Event（reason `Preempting`，写在 `default`），并由租约状态派生 VC 的 `Preempting` 条件（`NodesReclaimed`，列出节点与回收截止时间），节点移交后自动清除。`TestPreemptionNoticesVictim` 等 3 个测试覆盖。
  - [x] GUI 侧（宿主管理员视角，[#34](https://github.com/wutz/kubeyard/pull/34)）：虚拟集群详情页在 `Preempting=True` 时显示"即将回收"提示，列出条件消息。
  - 2026-10-25 k3d 实测（真实 operator 在宿主机运行，`gui/live_reclaim_test.go`，设置 `KUBEYARD_LIVE_RECLAIM` 时运行）：`low` 持有节点时，`high` 到达，operator 写入 `Preempting=True`（NodesReclaimed，列出节点与截止时间）与 `default` 下的 Warning Event；`App.VirtualClusters` 读到该条件；移交后条件清除。把抓取到的真实返回值交给详情页渲染，横幅显示正确（静态页 + Wails 绑定替身）。未验证：在 Wails 原生窗口内点击操作，本机未自动化该窗口。
    - 2026-10-25 本地 k3d 实测（operator 二进制在宿主机运行，单节点 NodePool，grace 30s / drainTimeout 60s）：low 持有节点并运行 Pod，high（优先级 5）到达后 low 出现 Event 与条件，条件内容为 `1 个节点将于 … 前回收：[k3d-kubeyard-gui-agent-0]`；grace 到期后 Pod 被驱逐，约 10 秒内节点移交给 high，条件消失，Event 仅 1 条，operator 日志 0 条错误。测试后已删除验证对象并清除节点标签与 taint。
  - [x] GUI 侧验证（2026-10-25，k3d，按用户路径操作）：`low` 持有节点时，把 `high` 的 desired 调为 1，`low` 详情页出现"即将回收"横幅，内容为节点与截止时间；横幅内的节点列表原先显示为 `[k3d-kubeyard-ux-agent-0]`（Go 切片格式），已在 [#40](https://github.com/wutz/kubeyard/pull/40) 修为逗号分隔。移交给 `high` 后 `Preempting` 条件清除（API 确认）。未在修复后的 operator 上复测横幅文案。

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
