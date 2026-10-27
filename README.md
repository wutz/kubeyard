# Kubeyard

用一个桌面客户端管理 Kubernetes 集群的组件：通过 Flux 从签名的 OCI catalog 安装、更新、卸载基础组件与 AI 组件（nfd、gpu-operator、istio、cert-manager…），并查看每个组件的 Flux 状态与事件。

> 状态：M1 已完成。范围只包括 Flux 与组件安装管理；虚拟集群、资源池、抢占与计量已移除，历史见 [路线图](docs/roadmap.md)。

## 功能

- **集群**：读取本地 kubeconfig 的所有 context，侧边栏切换当前集群
- **Flux**：未安装时一键安装 flux-operator 与 FluxInstance；已有 Flux 时拒绝，不接管其他方式安装的 Flux
- **catalog 源**：添加默认的 OCI catalog（`ghcr.io/wutz/kubeyard-catalog`，cosign 校验）；源未经校验时不显示组件
- **组件**：浏览 catalog，按 schema 填写参数，安装 / 更新参数 / 卸载；Kubeyard 管理的组件可查看 Flux 的 conditions 与最近事件
- **守卫**：安装与卸载在写入前用集群的最新状态复查，依赖未就绪、已存在的非 Helm 组件、非 Kubeyard 安装的组件都会被拒绝

## 核心组件

- **Kubeyard GUI**：基于 [Wails](https://wails.io) v2 的桌面客户端（`gui/`）。页面通过 Wails 绑定调用 Go 的 `App` 方法，不开端口，直接用 kubeconfig 访问 API Server，没有后端服务
- **pkg/kube、pkg/catalog**：读取 kubeconfig、安装 Flux 与 catalog 源、解析 catalog 并执行安装守卫
- **kubeyard-catalog**：以签名 OCI artifact 发布到 GHCR 的 Flux 组件目录，默认源基于 [k8s-in-action](https://github.com/wutz/k8s-in-action)，其他组织可以发布自己的源

## 文档

- [架构总览](docs/architecture.md)
- [Catalog 与 Flux 安装模型](docs/catalog.md)
- [GUI 设计](docs/gui.md)
- [路线图](docs/roadmap.md)
- [视觉规范](DESIGN.md)

## 开发

```bash
go vet ./...
go test ./...
go build -tags production -o kubeyard-gui ./gui
```

GUI 使用 cgo 与系统 webview，构建需求见 [GUI 设计](docs/gui.md) §5。

读取真实集群的测试默认跳过。设置 `KUBEYARD_LIVE_CONTEXT` 为 kubeconfig 中的 context 名即可运行 `TestLiveCatalog`。写入路径的测试 `TestLiveWritePath`（`gui/live_write_test.go`）会安装与卸载组件，设置 `KUBEYARD_LIVE_WRITE` 为一个可丢弃集群的 context 名才会运行。

## 许可

Apache-2.0
