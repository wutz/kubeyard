# Kubeyard Catalog（Flux 源）

Catalog 是 Kubeyard 的"应用商店"：一份由组织维护、可被 GUI 浏览并通过 Flux 安装到集群的组件集合。默认源由本项目基于 [k8s-in-action](https://github.com/wutz/k8s-in-action) 封装，其他组织可以发布自己的 catalog，GUI 支持同时添加多个源。

## 1. 源格式：Git 还是 OCI

结论：**作者在 Git 仓库里维护，发布为签名的 OCI artifact，集群和 GUI 只消费 OCI。**

| | GitRepository | OCIRepository |
|---|---|---|
| 版本语义 | 分支 / tag / commit | tag + 不可变 digest |
| 离线 / 内网 | 需要 git 镜像 | 和镜像共用同一个 registry（Harbor、mir 镜像站） |
| 签名校验 | GPG commit 签名 | cosign / notation，Flux 原生 `spec.verify` |
| 拉取体积 | 整个仓库历史 | 只有打包后的清单 |
| GUI 读取 | 需要 git 客户端 | 普通 registry API，GUI 本地即可拉取解析 |
| 发布门槛 | push 即发布 | 需要 CI 打包推送 |

OCI 在内网 GPU 集群场景优势明显：集群里本来就有镜像仓库，catalog 跟着镜像一起同步即可；digest 固定让"装了什么"可追溯。Git 作为兼容选项保留：GUI 添加源时允许填 Git URL（开发调试、小团队自建），底层生成 GitRepository，其余流程不变。

发布流程：

```
catalog 仓库（Git）
  └─ CI: 校验 schema → kustomize build 冒烟 → flux push artifact
       oci://ghcr.io/wutz/kubeyard-catalog:v2026.05.0
       oci://ghcr.io/wutz/kubeyard-catalog:stable   (浮动 tag)
       → cosign sign --key（私钥存 CI secret）
```

签名默认用 cosign 密钥对：Flux 对 keyless（OIDC）校验仍标为实验特性。公钥随源一起下发为 Secret，OCIRepository 用 `verify.provider: cosign` + `verify.secretRef` 校验，成功后条件为 `SourceVerified=True`，GUI 在"源"页面展示该状态。keyless 稳定后再提供 `matchOIDCIdentity` 选项。

## 2. 仓库结构

```
kubeyard-catalog/
├── catalog.yaml                 # 索引：源元数据 + 所有条目摘要（GUI 首先读这个）
├── profiles/
│   ├── host.yaml                # 底层集群推荐组件集合
│   └── tenant.yaml              # 虚拟集群推荐组件集合
└── addons/
    ├── nfd/
    │   ├── addon.yaml           # 元数据：名称、分类、版本、依赖、适用范围
    │   ├── schema.json          # 参数 JSON Schema，GUI 据此渲染表单
    │   └── manifests/           # Flux 对象：HelmRepository/OCIRepository + HelmRelease + 附加 CR
    ├── gpu-operator/
    ├── vcluster/                # 由 operator 引用，不直接在 GUI 列表里"安装"
    ├── kubeyard-operator/
    └── ...
```

`addon.yaml` 示例：

```yaml
apiVersion: catalog.kubeyard.dev/v1alpha1
kind: Addon
metadata:
  name: gpu-operator
spec:
  displayName: NVIDIA GPU Operator
  category: ai                    # base | network | storage | ai | o11y | compute | security
  scope: [host]                   # host | tenant，决定在哪类集群显示
  version: v25.10.1               # 上游 chart 版本
  dependsOn: [nfd]                # 映射为 Flux Kustomization.spec.dependsOn
  namespace: gpu-operator
  params: schema.json
  health:                         # GUI 判断"已就绪"的依据
    - kind: HelmRelease
      name: gpu-operator
```

## 3. 安装模型

GUI 安装一个 addon 只创建三类对象，全部在 `kubeyard-system` 命名空间：

```yaml
# 1) 源（每个 catalog 源一个，添加源时创建）
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata: { name: catalog-default, namespace: kubeyard-system }
spec:
  url: oci://ghcr.io/wutz/kubeyard-catalog
  ref: { tag: v2026.05.0 }
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
  labels: { kubeyard.dev/addon: gpu-operator, kubeyard.dev/catalog: default }
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

addon 自己的 HelmRelease（`helm.toolkit.fluxcd.io/v2`）用 `valuesFrom` 引用上面的 ConfigMap。

Flux 本身用 [flux-operator](https://github.com/controlplaneio-fluxcd/flux-operator) 安装：GUI apply operator 的 chart 后创建一个 `FluxInstance`（`flux-system/flux`），升级 Flux 只改这个对象，不用 shell 调 `flux bootstrap`。flux-operator 的 `ResourceSet`（带 inputs 的参数化对象组）也能表达"一个 addon + 参数"，v1 仍用上面的 Kustomization + ConfigMap：只依赖 Flux 核心 API，第三方 catalog 的作者门槛更低。若后续需要"同一 addon 按租户批量实例化"，再评估切到 ResourceSet。具体版本在 M1 从各 releases 页面锁定。卸载 = 删除 Kustomization（`prune: true` 负责清理）；升级 = 改 OCIRepository 的 tag 或 addon 参数。

镜像与 chart 地址：catalog 内统一写成 `${registry_mirror:=docker.io}` 这类变量，安装时由源级别的 ConfigMap 替换（例如替换为 `mir.blsc.cn`），沿用 k8s-in-action 现有的镜像站习惯。

## 4. 默认 catalog 内容（v1）

| 分类 | host profile | tenant profile |
|---|---|---|
| base | nfd, spegel, metrics-server | metrics-server |
| network | metallb, cert-manager, istio | — |
| ai | gpu-operator | kueue, trainer (v2), agent-sandbox, lws |
| compute | vcluster（operator 内部使用）, kubeyard-operator (host) | kubeyard-operator (tenant) |
| o11y | victoria-metrics + dcgm 看板（可选，计量利用率维度依赖它） | — |

tenant profile 上游安装方式（调研时版本，M1 重新锁定）：Kueue `oci://registry.k8s.io/kueue/charts/kueue`（v0.15.x）；Trainer v2 `oci://ghcr.io/kubeflow/charts/kubeflow-trainer`（2.1.0）；agent-sandbox 官方只发 release manifest（v0.4.x），catalog 中直接引用该清单。

从 k8s-in-action 迁移：现有 64 个 `helmwave.yml` 的 release 定义（chart、版本、values、namespace）可以半自动转换为 HelmRelease；`lifecycle.post_up` 中的 `kubectl apply -k xxx` 转为同一 Kustomization 内的附加清单。k8s-in-action 保持为文档/手册仓库，catalog 作为独立仓库发布。

## 5. 虚拟集群内的 Flux

虚拟集群里安装 tenant profile 时，GUI 先在虚拟集群内安装 Flux（与 host 相同方式），再走同样的安装模型。不复用 host 的 Flux：租户管理员只有虚拟集群权限，host Flux 对租户不可见也不应可见。

GPU 驱动、device plugin、RuntimeClass（agent-sandbox 依赖 gVisor/Kata）属于节点能力，只在 host 安装，vcluster 同步进虚拟集群即可。
