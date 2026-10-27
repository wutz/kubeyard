# 路线图

每个里程碑都以"能在开发环境端到端演示"为验收标准。

## M0 设计

- [x] 架构、catalog 格式、GUI 信息架构文档
- [x] catalog 只以 OCI 发布到 GHCR，cosign 签名
- [x] 设计 PR 合并

## M1 连接与 catalog（已完成）

- [x] 读取 kubeconfig，列出 context，侧边栏切换集群
- [x] 一键安装 Flux：Helm SDK 从 OCI 拉取 flux-operator 0.61.0，再创建 FluxInstance；已有 Flux 时拒绝
- [x] 添加 catalog 源：公钥随二进制嵌入，创建校验签名的 OCIRepository；源已存在或 Flux 未安装时不显示
- [x] catalog 仓库：host 组件以 cosign 签名发布到 GHCR，参数归属由 `x-kubeyard-target` 标记
- [x] 组件页：只读列表（已安装 / 已存在 / 未安装 / 无法判断）、参数表单、安装 / 卸载（二次确认，守卫在写入前复查）
- [x] 参数更新：表单以集群中存储的参数为初值，只对 Kubeyard 管理的组件开放
- [x] 组件的 Flux 状态与最近事件（仅 Kubeyard 管理的组件）
- [x] 健康检查支持 `HelmRelease` 与 `CustomResourceDefinition`
- [x] 卸载的残留提示：Helm 创建的 namespace 与 keep 策略的 CRD 在确认框中列出，不自动删除（用户确认的方案）
- [x] GUI 使用 Wails v2.17.0，页面经绑定调用 `App` 方法，不开端口
- [x] CI：ubuntu-22.04、macOS、Windows 上 gofmt、vet、build、test 与 GUI 窗口启动；kind 集群上运行 `TestLiveCatalog`

版本升级不作为 GUI 操作：OCIRepository 跟随 stable tag 由 Flux 自动拉取（范围决定，见 [catalog.md](catalog.md) §3）。

### 验证记录

- 写入路径（`gui/live_write_test.go`，设置 `KUBEYARD_LIVE_WRITE` 时运行，经 `App` 绑定）：在 k3d 全新集群上依次完成安装 Flux、添加 catalog 源、安装 nfd、参数更新（replicaCount 2→3，Deployment 3/3）、卸载；卸载后 Kustomization、HelmRelease 与工作负载全部移除，`nfd` namespace 保留，与卸载警告一致。
- 发现并修复：参数 ConfigMap 缺少 `reconcile.fluxcd.io/watch=Enabled` 标签，参数更新要等 10 分钟间隔才生效。现在安装与更新都写入该标签，k3d 上约 5 秒生效（`TestInstallLabelsParamsForFluxWatch`）。
- 发现并修复：更新 Kustomization 时整对象 Update 会丢掉 Flux 的 `finalizers.fluxcd.io`，之后卸载会跳过 prune。更新时保留现有 finalizer（`TestInstallKeepsFluxFinalizer`）。
- 发现并修复：catalog 在 HelmRelease 中内联设置 schema 暴露的键，覆盖了参数 ConfigMap，参数更新不生效。修复发布为 catalog v2026.10.4，k3d 上 nfd replicaCount 2→1→3 随参数生效。
- 发现：chart 的 CRD 带 `helm.sh/resource-policy: keep`，`helm uninstall` 后 CRD 保留，需手动清理。卸载警告已覆盖。
- 原生窗口：三个 CI runner 上窗口启动、进程保持运行并截屏；Linux 截屏中的中文显示为方框，是 runner 缺少中文字体。
- 真实数据：未在 macOS 与 Windows 连接真实集群时验证页面的数据。真实集群只在 Linux 的 kind 与 k3d 上验证。

## 已移除的范围

以下里程碑的规划与实现已从本项目移除，代码保留在 `99a1921`（`feat(gui): open a virtual cluster as its admin to read its nodes and pods (#52)`）及之前的历史中，需要时可以从那里查阅：

- **M2 虚拟集群与资源池**：kubeyard-operator（host 模式）、VirtualCluster / NodePool / NodeLease CRD、分配算法与准入 webhook、导出与"以虚拟集群管理员身份打开"虚拟集群、节点拖拽固定、资源池页与虚拟集群页。
- **M3 弹性与抢占**：autoscale、跨虚拟集群抢占、drain 流程、"即将回收"提示、调整 desired 前的预览。
- **M4 计量**：operator 指标、UsageReport、CSV 导出、利用率维度。
- **M5 虚拟集群管理员体验**：VC profile（kueue、trainer、agent-sandbox、lws）、kubeyard-operator vcluster 模式、Workspace 与用户 kubeconfig 的签发与撤销。

同时移除的还有概览页（集群节点、Pod 与 GPU 指标）与 `cmd/fakeapiserver`。

## 之后

- 组件页的列表 watch（informer），代替手动刷新
- 窄屏下侧边栏折叠
