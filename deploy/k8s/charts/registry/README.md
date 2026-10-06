# KOSMOS Registry — Helm chart

A packaged deployment of the multi-format artifact registry (OCI Distribution +
Maven / npm / Helm / PyPI / Go / NuGet / …) with explicit support for two
ingress modes and multiple hosts, mirroring the `kosmos` platform chart.

## Layout

```
deploy/k8s/charts/registry/
├── Chart.yaml          # declares the bitnami/postgresql subchart dependency
├── values.yaml
├── templates/
│   ├── _helpers.tpl
│   ├── namespace.yaml
│   ├── serviceaccount.yaml
│   ├── configmap.yaml
│   ├── secret.yaml
│   ├── pvc.yaml
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── ingress.yaml        # mode: ingress
│   └── virtualservice.yaml # mode: istio-vs
└── charts/
    └── postgresql-16.0.6.tgz
```

## Install

```bash
helm dependency build .            # optional if charts/ already has the tgz
helm install registry . \
  --namespace registry --create-namespace \
  --set ingress.hosts[0]=registry.example.com \
  --set admin.password='S3cret!'
```

## Ingress modes (explicit, no fallback)

`ingress.mode` selects exactly one behaviour:

| `mode`       | Renders                                  | Notes                                  |
|--------------|------------------------------------------|----------------------------------------|
| `ingress`    | a standard `Ingress` (nginx by default)  | set `ingress.className`, `ingress.tls` |
| `istio-vs`   | an Istio `VirtualService`                | set `ingress.istio.gateway`            |

`ingress.enabled: false` renders no ingress at all (e.g. in-cluster
`ClusterIP` only, for node-side containerd mirror config).

## Multiple hosts

`ingress.hosts` is a list of **all** external hostnames the registry is served
from. Each host is added as a rule to the Ingress (or as a `hosts:` entry in the
VirtualService) and routed to the single registry Service — the registry itself
dispatches per Host header.

> The registry resolves the incoming `Host` to a **registry definition**. For
> every host in `ingress.hosts` you must create a registry definition whose
> `hosts` field contains that hostname (via the UI or the admin API). This is how
> e.g. `charts.example.com` → a `helm` hosted repo, `docker.example.com` → the
> `docker` registry, etc. The chart only wires the L7 routing; the mapping
> host → repository lives in PostgreSQL.

## PostgreSQL

The metadata store is **always** PostgreSQL. With `postgresql.enabled: true` a
bitnami/postgresql subchart is deployed; otherwise point `postgresql.external`
at an existing instance. The connection string is built automatically and placed
in the `registry-secrets` Secret as `REGISTRY_POSTGRES`.

## Blob storage

Default is a file-backed PVC (`persistence.*`). To use an object store, set
`config.blob.type` and the relevant vars via `config.extraEnv`, e.g.:

```yaml
config:
  blob:
    type: s3
  extraEnv:
    REGISTRY_S3_BUCKET: my-bucket
    REGISTRY_S3_REGION: eu-west-1
    REGISTRY_S3_ENDPOINT: s3.amazonaws.com
```

## Notes

- The web UI, the OCI `/v2` surface, the JSON admin API and the path-based
  artifact routes are all served on the same container port (`containerPort`,
  exposed in-cluster as `service.port`, default 5000).
- The `cache` repository type (Kubernetes pull-through image cache) is part of
  the product. This chart optionally deploys the two helpers that make it
  transparent (both default `enabled: false`):
  - `cacheMirror` — a DaemonSet that rewires containerd on every node to use the
    registry as a mirror for **all** registries (`"*"` endpoint). Best-effort for
    systemd-managed containerd; adapt `cacheMirror.containerdConfDir` /
    `cacheMirror.restartCommand` to your distribution.
  - `warmer` — a Deployment that watches cluster workloads and pre-pulls the
    referenced images into the `cache` registry (POST `/api/v1/admin/registries/cache/warm`).
    Requires an admin token: set `warmer.token` (or manage the
    `<release>-registry-warmer` Secret out of band). It is granted a
    cluster-scoped `ClusterRole` to list/watch Pods/Deployments/StatefulSets/
    ReplicaSets/DaemonSets/Jobs/CronJobs.

  Enable example:
  ```bash
  helm install registry . --set cacheMirror.enabled=true --set warmer.enabled=true \
    --set warmer.token="$(curl -s -u admin:... http://.../auth/token | jq -r .token)"
  ```
  (Create a `cache` registry first, e.g. from the UI → Registries → New → type `cache`.)
