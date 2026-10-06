# KOSMOS Registry — Kubernetes deployment

This directory deploys the registry as an **in-cluster OCI registry** that any
pod can pull from **without an `imagePullSecret`**. Anonymous pull is enabled by
default (`REGISTRY_AUTH_ANON=true`); pushing always requires a token.

## 1. Apply

```bash
kubectl apply -k deploy/registry
```

Then fill in the real values in `secret.yaml` (PostgreSQL DSN, admin password)
and re-apply, or create the secret imperatively:

```bash
kubectl -n registry create secret generic registry-secrets \
  --from-literal=REGISTRY_POSTGRES='postgres://registry:...@postgres.registry.svc.cluster.local:5432/registry?sslmode=disable' \
  --from-literal=REGISTRY_ADMIN_USER=admin \
  --from-literal=REGISTRY_ADMIN_PASS='CHANGE_ME'
```

The registry is reachable in-cluster at
`registry.registry.svc.cluster.local:5000`.

## 2. Make nodes pull without imagePullSecret

The registry Service is HTTP (no TLS). For containerd to accept an in-cluster
plain-HTTP registry **and** treat it as a normal pull source, configure the
`cri` plugin on every node. Add the following to `/etc/containerd/config.toml`
on each node (or generate it with `containerd config default` first):

```toml
[plugins."io.containerd.cri.v1.images".registry.mirrors."registry.registry.svc.cluster.local:5000"]
  endpoint = ["http://registry.registry.svc.cluster.local:5000"]

[plugins."io.containerd.cri.v1.images".registry.configs."registry.registry.svc.cluster.local:5000".tls]
  insecure_skip_verify = true
```

Then restart containerd on the node:

```bash
systemctl restart containerd
```

After this, a pod can use the registry with **no** `imagePullSecret`:

```yaml
spec:
  containers:
    - name: app
      image: registry.registry.svc.cluster.local:5000/myteam/myapp:1.0.0
```

### Rolling this out across a cluster

For managed clusters you cannot edit node configs directly. Options:

- **Node bootstrap**: bake the `config.toml` snippet into your node image / user
  data (cloud-init, kubeadm `NodeRegistration`, etc.) so every new node gets it.
- **A cluster CA**: issue a TLS cert for `registry.registry.svc.cluster.local`
  from your cluster CA, terminate TLS in the registry (or an ingress), and drop
  the `insecure_skip_verify` flag. This is the production-safe path.

Never restart containerd from a privileged DaemonSet — that evicts every pod on
the node. Configure node containerd out-of-band and reload it via node
maintenance/cordoning.

## 3. Pushing images

Anonymous users can only pull. To push, authenticate with the bootstrap admin
(or any user with a `repo:push` permission) and use a bearer token:

```bash
# Get a session token.
TOKEN=$(curl -s -u admin:CHANGE_ME \
  http://registry.registry.svc.cluster.local:5000/auth/token | jq -r .token)

# Log in (credential helper stores the token).
docker login registry.registry.svc.cluster.local:5000 \
  -u admin -p "$TOKEN"

docker tag myapp:1.0.0 registry.registry.svc.cluster.local:5000/myteam/myapp:1.0.0
docker push registry.registry.svc.cluster.local:5000/myteam/myapp:1.0.0
```

Roles/permissions, additional users, service accounts, stores and repositories
are all managed from the web UI at `/`.

## 4. Blob storage backends

The default is a file-backed PVC. To use an object store, set the relevant
config (via `configmap.yaml` or the JSON config file) — the metadata store is
**always** PostgreSQL:

- `REGISTRY_BLOB_TYPE=s3` + `REGISTRY_S3_BUCKET` / `REGISTRY_S3_REGION` / `REGISTRY_S3_ENDPOINT`
- `REGISTRY_BLOB_TYPE=gcs` + `REGISTRY_GCS_BUCKET`
- `REGISTRY_BLOB_TYPE=azure` + `REGISTRY_AZURE_CONTAINER`

## 5. Cache registry (Kubernetes pull-through mirror)

A `cache` registry is a transparent, multi-upstream pull-through cache. Every pull
is served from its local store and, on a miss, fetched from the upstream resolved by
the **repo host**:

- host-less repos (`library/alpine`, `myuser/repo`) → the **default upstream**
  (Docker Hub, `https://registry-1.docker.io`, unless you set
  `cache_default_upstream`);
- any repo with a registry host segment (`gcr.io/x/y`, `myreg:5000/z`) → that host
  over HTTPS (override per-host URL/credentials via `cache_upstreams`).

Writes are rejected (405): a cache is populated by pulls and by the pre-warm
controller, never by client pushes. Create one from the web UI (Registries → New →
type `cache`) or the API:

```bash
curl -u admin:CHANGE_ME -X POST http://registry.registry.svc.cluster.local:5000/api/v1/admin/registries \
  -H 'Content-Type: application/json' -d '{
    "name":"cache","type":"cache","format":"docker","online":true,"default":true,
    "cache_default_upstream":"https://registry-1.docker.io",
    "cache_upstreams":{"gcr.io":{"url":"https://gcr.io"},"myreg:5000":{"url":"http://myreg:5000","insecure":true}},
    "blob":{"type":"file","root":"/data/cache"}
  }'
```

### Make every node use it as a mirror

`cache-mirror.yaml` ships a DaemonSet that drops a containerd mirror config pointing
`*` at the cache service and reloads containerd on each node, so **all** image pulls
(also external ones) are cached automatically:

```bash
kubectl -n registry apply -f deploy/registry/cache-mirror.yaml
```

(Adapt the hostPath and restart command if your distribution keeps containerd config
elsewhere or uses a different runtime. Do not run this on nodes you cannot cordon —
reloading containerd briefly interrupts pulls.)

### Proactively pre-warm the cache

`warmer.yaml` deploys the pre-warm controller. It watches Pods/Deployments/DaemonSets/
StatefulSets/ReplicaSets/Jobs/CronJobs cluster-wide and calls the warm endpoint for
each referenced image, so images are cached before nodes pull them (useful for
air-gapped readiness or to absorb registry outages):

```bash
# fill the admin token, then:
kubectl -n registry apply -f deploy/registry/warmer.yaml
```

The warmer honors `REGISTRY_WARMER_*` env vars (cache URL, registry name, token,
interval). The same binary can also run locally against a kubeconfig:

```bash
./registry-warmer -kubeconfig ~/.kube/config \
  -cache-url http://localhost:8080 -registry cache -token "$TOKEN" -interval 10m
```

## 6. Artifact formats (helm / maven / npm)

Besides OCI images (`format: docker`), a registry can speak the classic artifact
repository protocols by setting `format` to `helm`, `maven` or `npm`. These use the
**`hosted`, `proxy` and `group`** types — the `cache` type is **docker/OCI only**
(see §5: the Kubernetes/containerd persistent image cache). So the matrix for
non-docker formats is:

| type    | helm                         | maven                              | npm                                  |
|---------|------------------------------|------------------------------------|--------------------------------------|
| hosted  | push `*.tgz`; `GET /index.yaml` generated | push jars/poms; `maven-metadata.xml` generated | push `*-<v>.tgz`; `GET /<pkg>` metadata generated (reads `package.json`) |
| proxy   | mirror an upstream chart repo | mirror Maven Central, etc.         | mirror an npm registry               |
| group   | aggregate member repos       | aggregate member repos             | aggregate member repos               |

Non-OCI registries are addressed by **host** (set `hosts`) or a dedicated `port`,
exactly like OCI registries; the artifact path is the request path (e.g.
`GET /com/example/foo/1.0/foo-1.0.jar`, `GET /@scope/pkg/-/pkg-1.0.0.tgz`). The
`/v2` OCI surface is reserved for `docker` registries only.

- **hosted** generates format metadata on the fly (`index.yaml`, `maven-metadata.xml`,
  npm package documents) by scanning stored artifacts — no separate index upload.
- **proxy** mirrors one upstream (`remote_url`); on a miss it fetches from upstream and
  caches locally. Writes are rejected unless `proxy_allow_write` is set, in which case
  they are stored locally (write-through to the upstream is not performed, since upstream
  artifact repos are read-only mirrors).
- **group** aggregates reads across `members` and writes to `write_member`.

```bash
# a hosted helm chart repo
curl -u admin:CHANGE_ME -X POST http://registry.registry.svc.cluster.local:5000/api/v1/admin/registries \
  -H 'Content-Type: application/json' -d '{
    "name":"charts","type":"hosted","format":"helm","online":true,
    "hosts":["charts.internal"],"blob":{"type":"file","root":"/data/charts"}}'

# push a chart, then fetch the generated index
curl -T mychart-1.0.0.tgz -H "Host: charts.internal" \
  http://registry.registry.svc.cluster.local:5000/mychart-1.0.0.tgz
curl -H "Host: charts.internal" http://registry.registry.svc.cluster.local:5000/index.yaml
```
