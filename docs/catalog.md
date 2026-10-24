# Kubeyard Catalog（Flux 源）

Catalog 是 Kubeyard 的"应用商店"：一份由组织维护、可被 GUI 浏览并通过 Flux 安装到集群的组件集合。默认源由本项目基于 [k8s-in-action](https://github.com/wutz/k8s-in-action) 封装并发布到 GHCR；其他组织可以发布自己的源，GUI 支持同时添加多个。

## 1. 源格式：OCI，发布到 GHCR

- 作者在 Git 仓库里维护 catalog（可以 PR 评审），CI 打包后以 OCI artifact 发布到 GHCR，不直接引用 Git 仓库作为源
- 版本以 tag + digest 固定，`ghcr.io/wutz/kubeyard-catalog:v2026.05.0` 发布后不可变
- 浮动 tag `stable` 指向当前推荐版本
- GUI 只接受 OCI 源地址；Flux 的 OCIRepository 原生支持 cosign 校验
- GUI 读取 catalog 的方式：先读集群里 OCIRepository 的 `status.artifact.revision`（Flux 已校验签名的 digest），再用 `oci://<spec.url>@<digest>` 从 GHCR 拉取。digest 引用是内容寻址的，拉到的内容与 Flux 校验的一致；集群里的 SourceVerified 不为 True 时 GUI 不显示组件

发布流程：

```
catalog 仓库（Git）
  └─ CI: 校验 schema → kustomize build 冒烟 → flux push artifact
       oci://ghcr.io/wutz/kubeyard-catalog:v2026.05.0
       oci://ghcr.io/wutz/kubeyard-catalog:stable   (浮动 tag)
       → cosign sign --key（私钥存 CI secret）
```

签名使用 cosign 密钥对。Flux 对 keyless（OIDC）校验仍标为实验特性，待稳定后再提供 `matchOIDCIdentity` 选项。公钥随源一起下发为 Secret，OCIRepository 用 `verify.provider: cosign` + `verify.secretRef` 校验，成功后条件为 `SourceVerified=True`，GUI 在"源"页面展示该状态。

## 2. 仓库结构

```
kubeyard-catalog/
├── catalog.yaml                 # 索引：源元数据 + 所有条目摘要（GUI 首先读这个）
├── profiles/
│   ├── host.yaml                # 底层集群推荐组件集合
│   └── vcluster.yaml            # 虚拟集群推荐组件集合
└── addons/
    ├── nfd/
    │   ├── addon.yaml           # 元数据：名称、分类、版本、依赖、适用范围
    │   ├── schema.json          # 参数 JSON Schema，GUI 据此渲染表单
    │   └── manifests/           # Flux 对象：HelmRepository / OCIRepository + HelmRelease + 附加 CR
    ├── gpu-operator/
    ├── vcluster/                # 由 operator 引用，不在 GUI 列表里直接"安装"
    ├── kubeyard-operator/       # 同一个 addon，scope 分 host 与 vcluster 两种模式
    └── ...
```

`addon.yaml` 示例：

```yaml
apiVersion: catalog.kubeyard.wutz.dev/v1alpha1
kind: Addon
metadata:
  name: gpu-operator
spec:
  displayName: NVIDIA GPU Operator
  category: ai                    # base | network | storage | ai | o11y | compute | security
  scope: [host]                   # host | vcluster，决定在哪类集群显示
  version: v25.10.1               # 上游 chart 版本
  dependsOn: [nfd]                # 映射为 Flux Kustomization.spec.dependsOn
  namespace: gpu-operator
  params: schema.json
  health:                         # GUI 判断"已就绪"的依据
    - kind: HelmRelease
      name: gpu-operator
```

### 参数归属：x-kubeyard-target

`schema.json` 的每个顶层属性必须标 `x-kubeyard-target`，说明表单的值写到哪里。属性下的叶子参数继承该标记：

- `values`：写入 `addon-<name>-params` ConfigMap 的 `values.yaml`，由 HelmRelease 的 `valuesFrom` 读取。
- `substitute`：作为 Flux `postBuild.substitute` 变量注入清单，清单中以 `${参数名}` 引用，并带默认值（如 `${metallb_addresses:=192.0.2.10-192.0.2.20}`）。

`scripts/validate.py` 检查：每个顶层属性都有合法的 `x-kubeyard-target`；`substitute` 参数必须在清单中以 `${参数名` 出现，并且在允许的变量列表内。新增变量时两处都要改。

## 3. 安装模型

GUI 安装一个 addon 只创建两类对象，全部在 `kubeyard-system` 命名空间：

```yaml
# 1) 源（每个 catalog 源一个，添加源时创建）
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: { name: catalog-default, namespace: kubeyard-system }
spec:
  url: oci://ghcr.io/wutz/kubeyard-catalog
  ref: { tag: stable }
  interval: 1h
  verify:
    provider: cosign
    secretRef: { name: catalog-default-cosign-pub }
---
# 2) 参数（GUI 表单的结果）
apiVersion: v1
kind: ConfigMap
metadata: { name: addon-gpu-operator-params, namespace: kubeyard-system }
data:
  values.yaml: |
    driver: { enabled: false }
---
# 3) 安装单元
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: addon-gpu-operator
  namespace: kubeyard-system
  labels: { kubeyard.wutz.dev/addon: gpu-operator, kubeyard.wutz.dev/catalog: default }
spec:
  sourceRef: { kind: OCIRepository, name: catalog-default }
  path: ./addons/gpu-operator/manifests
  dependsOn: [{ name: addon-nfd }]
  prune: true
  wait: true
  interval: 10m
  postBuild:
    substitute: { addon_namespace: gpu-operator }
```

addon 自己的 HelmRelease（`helm.toolkit.fluxcd.io/v2`）用 `valuesFrom` 引用上面的 ConfigMap。卸载 = 删除 Kustomization（`prune: true` 负责清理）再删除参数 ConfigMap；升级 = 改 OCIRepository 的 tag，或修改 addon 参数。

安装与卸载的守卫（`catalog.Addon.Installable` / `Catalog.Uninstallable`），在写入前用集群的最新状态重新检查：

- 安装：只有状态为"未安装"才允许。已安装、已存在（有 Deployment / DaemonSet 但无 Helm release，即非 Kubeyard 安装）、无法判断的组件都拒绝；每个依赖必须已安装或已存在。
- 卸载：只允许 Kubeyard 管理的组件（即存在 `addon-<name>` Kustomization）。被其他已安装 / 已存在组件依赖时拒绝，需先卸载依赖方。

健康检查支持两类对象：`HelmRelease`（按 release 名判断已安装；同名 Deployment / DaemonSet 存在则为已存在）和 `CustomResourceDefinition`（CRD 存在即为已存在，集群级对象，namespace 留空）。只有 Kustomization 这类无法用 Helm release、工作负载或 CRD 查询的对象才会得到"无法判断"，此时拒绝安装。

卸载 gateway-api-crds 会删除集群中全部 Gateway API 资源。守卫只检查目录中声明的依赖（如 istio）；对于健康检查中声明了 CRD 的组件，GUI 在点击"卸载"后、确认前显示警告，列出将被删除的 CRD（`catalog.Addon.UninstallWarning`）。

### 添加 catalog 源

Flux 已安装且集群中没有 `kubeyard-system/catalog-default` 时，GUI 显示"添加 catalog 源"。它创建两个对象：

- Secret `catalog-default-cosign-pub`，其 `cosign.pub` 字段是随 Kubeyard 嵌入的公钥（`pkg/kube/cosign.pub`，是 catalog 仓库 `cosign.pub` 的副本，轮换密钥时两处必须同步发布）。
- OCIRepository `catalog-default`，即本节"安装模型"中的源对象。

源已存在时 GUI 不覆盖它，只显示其 Ready 与 SourceVerified 状态。

### 参数更新

已由 Kubeyard 安装的组件可以"更新参数"。表单以集群中存储的参数为初值（来自 `addon-<name>-params` 的 `values.yaml`，以及 Kustomization 的 `postBuild.substitute`），因此保存时不会把已安装的值重置为默认值。更新只重写这两处，之后由 Flux 协调。

**已知问题（catalog 待修）：参数更新对 catalog 在 HelmRelease 中内联设置的键不生效。** Flux 中 `spec.values`（内联）优先于 `valuesFrom`，而当前每个组件的 `manifests/release.yaml` 都在 `spec.values` 内联了值（nfd 的 `master.replicaCount: 2` 即是）。参数 ConfigMap 写入正确，但 Flux 判定为已同步，副本数不变。在 k3d 上的验证中，即使手动触发协调，结果也一样。修复方向：catalog 的 `release.yaml` 不再内联 schema 暴露的键，改由参数 ConfigMap 提供（默认值写在 schema 中）；这需要发布新的 catalog tag。在修复之前，GUI 的"更新参数"只写入 ConfigMap，不能保证生效。

版本升级不由 GUI 单独触发：OCIRepository 跟随 `stable` tag，新版本发布后 Flux 自动拉取。

### 安装 Flux

Flux 本身用 [flux-operator](https://github.com/controlplaneio-fluxcd/flux-operator) 安装：GUI 通过 Helm SDK 从 `oci://ghcr.io/controlplaneio-fluxcd/charts/flux-operator` 安装 chart 0.61.0（release `flux-operator`，namespace `flux-system`，Helm 等待 operator 就绪），然后创建 `FluxInstance`（`flux-system/flux`，Flux 2.x，upstream-alpine，四个核心控制器）。只有在没有 Flux 控制器、没有 `flux-operator` release、也没有 FluxInstance 时才可用，不接管其他方式安装的 Flux。升级 Flux 只改这个 FluxInstance 的 `distribution.version`，不用 shell 调 `flux bootstrap`。v1 的 addon 仍用上面的 Kustomization + ConfigMap，只依赖 Flux 核心 API，作者门槛更低；若后续需要"同一 addon 按虚拟集群批量实例化"，再评估切换到 flux-operator 的 ResourceSet。具体版本在 M1 从各 releases 页面锁定。

镜像仓库：catalog 中的镜像与 chart 地址保持上游原样。内网集群需要镜像覆盖（例如 k8s-in-action 现用的 `mir.blsc.cn`）时，在 GUI 的集群设置中配置，由安装时的 postBuild 变量注入，不写进 catalog 本身。

## 4. 默认 catalog 内容（v1）

| 分类 | host profile | vcluster profile |
|---|---|---|
| base | nfd, spegel, metrics-server | metrics-server |
| network | metallb, cert-manager, istio | — |
| ai | gpu-operator | kueue, trainer (v2), agent-sandbox, lws |
| compute | vcluster（operator 内部使用）, kubeyard-operator (host 模式) | kubeyard-operator (vcluster 模式) |
| o11y | victoria-metrics + dcgm 看板（可选，计量的利用率维度依赖它） | — |

上游安装方式（调研时版本，M1 重新锁定）：Kueue `oci://registry.k8s.io/kueue/charts/kueue`（v0.15.x）；Trainer v2 `oci://ghcr.io/kubeflow/charts/kubeflow-trainer`（2.1.0）；agent-sandbox 官方只发 release manifest（v0.4.x），catalog 中直接引用该清单。

从 k8s-in-action 迁移：现有 64 个 `helmwave.yml` 的 release 定义（chart、版本、values、namespace）可以半自动转换为 HelmRelease；`lifecycle.post_up` 中的 `kubectl apply -k xxx` 转为同一 Kustomization 内的附加清单。k8s-in-action 保持为手册仓库，catalog 作为独立仓库发布。

## 5. 虚拟集群内的 Flux

虚拟集群里安装 vcluster profile 时，GUI 先在虚拟集群内安装 Flux（与 host 相同方式），再走同样的安装模型。不复用 host 的 Flux：虚拟集群管理员只有该 VC 的权限，host Flux 对其不可见也不应可见。

GPU 驱动、device plugin、RuntimeClass（agent-sandbox 依赖的 gVisor / Kata）属于节点能力，只在 host 安装，vcluster 同步进虚拟集群即可。
