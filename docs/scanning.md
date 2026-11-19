# Image scanning: SBOMs, vulnerabilities and exposed secrets

This page is the source of truth for the feature: what the registry stores,
the protocol the scanner speaks and how alerts are built from the results.

## Why

Container image scanning usually lives in the cluster (for example
trivy-operator), which keeps one report per workload as a Kubernetes object.
That stores the same data many times, ties it to etcd and forgets it as soon
as the workload goes away. On the cluster that motivated this design the SBOM
reports alone were ~680 MB of etcd and stalled the API server.

The registry is the one place where every image is already identified by its
digest. Stiva therefore keeps, next to the images:

- each image's **SBOM** (CycloneDX), as an OCI artifact attached to the image;
- an archive of **distinct packages**, each with its **vulnerabilities**,
  matched again every day against a fresh database, once per package however
  many images contain it;
- which packages each image contains, so an image's vulnerabilities are a
  join;
- the **secrets** exposed in each image's layers.

An image is analysed once, no matter how many clusters or workloads run it,
and the result outlives the workloads. "Which images contain openssl 3.0.15?"
and "where does CVE-X run?" are queries.

## Pieces

```
            ┌──────────────────── Stiva (registry) ────────────────────┐
 push/pull ─▶ manifests, blobs, tags                                    │
            │ referrers      (OCI 1.1: SBOM ──subject──▶ image)          │
            │ image_scans    (per image: analyser, SBOM, secrets)        │
            │ image_packages (image × target × package, occurrences)     │
            │ scan_packages  (distinct packages) ── package_vulnerabilities
            │ /api/v1/scan/* ◀──────────┐                                 │
            │ /metrics ──────────┐      │                                 │
            └────────────────────┼──────┼─────────────────────────────────┘
                                 │      │ work in, results out (API key)
                     Prometheus ◀┘   stiva-scanner (CronJob, Trivy built in)
                         │
   kube_pod_container_info (running digests)
                         ▼
        rules: running digest × findings ─▶ Alertmanager
```

- **Stiva** stays a lean single binary. It stores and serves results; it never
  analyses an image itself.
- **stiva-scanner** is a separate Go module (`scanner/`, its own `go.mod`) and
  a separate image. Trivy is built into it as a library: `stiva-scanner trivy
  <args>` is Trivy's own command line, and every analysis runs that way in a
  child process, so the findings are those of the `trivy` command with the
  same options. It runs as a Kubernetes CronJob: it asks Stiva what to do,
  does it, sends the results back and exits. Trivy's weight (dependencies,
  binary size, vulnerability databases) never reaches the registry.
- Trivy reads images from a **read-only OCI registry the scanner opens on the
  loopback interface**, which forwards to Stiva's scan API
  (`/api/v1/scan/manifest`, `/api/v1/scan/blob`). Images are streamed layer
  by layer as from any registry, nothing is copied to disk, and the scanner
  never needs to know through which host, port or base path clients reach
  each Stiva registry: the scan API names registries.
- **Prometheus** joins Stiva's per-digest findings with the digests the
  cluster is actually running. Alerts are evaluated by Prometheus and routed
  by Alertmanager like any other alert.

## 1. Referrers API (OCI Distribution 1.1)

The standard way to attach an artifact (SBOM, signature, attestation) to an
image. Stiva implements it for every OCI registry:

- A pushed manifest that carries a `subject` descriptor is recorded as a
  referrer of that subject. The `PUT` answers with the `OCI-Subject` header.
- `GET /v2/<repo>/referrers/<digest>` returns an OCI image index listing the
  referrers of `<digest>` in `<repo>`, each descriptor with its `artifactType`
  and `annotations`. `?artifactType=<type>` filters and the response then
  carries `OCI-Filters-Applied: artifactType`.
- The subject does not have to exist (the spec allows pushing a referrer
  first).
- Deleting a manifest deletes the referrers whose subject it is, recursively.
  A referrer never outlives its subject.

Standard clients work unchanged: `oras discover`, `oras attach`, `cosign` with
OCI 1.1 referrers, `trivy --sbom-sources oci`.

## 2. What gets scanned

An **image manifest** is a manifest (`application/vnd.oci.image.manifest.v1+json`
or `application/vnd.docker.distribution.manifest.v2+json`) whose layers are
all image layers (`…image.layer.v1.tar`, `+gzip`, `+zstd`, and the Docker
`…rootfs.diff.tar.gzip`). Everything else is not an image and is never
scanned: indexes (each platform manifest is scanned on its own), cosign
signatures, SBOMs and other artifacts.

Every image manifest in every OCI registry (hosted, proxy, cache) is scanned.
In a cache registry this means every image the cluster pulled.

## 3. Packages

A package is identified by:

- its **context**, what Trivy's match depends on besides the package: for an
  OS package the OS (`{"class":"os-pkgs","type":"debian","os":{"Family":
  "debian","Name":"12.8"}}`), for a library the ecosystem
  (`{"class":"lang-pkgs","type":"npm"}`);
- the **package** as Trivy describes it, without what depends on where it
  sits in an image: layer, file path, installed files, dependency graph and
  the identifiers Trivy derives from them. Everything the match reads stays:
  name, versions, source package, Red Hat build info (content sets) and
  modularity label, repository.

The key is the sha256 of the canonical JSON of `{context, package}`, computed
by the registry. The same package in a thousand images is one row.

An image lists its packages per **target** (the OS, a lock file, a jar…) with
the number of **occurrences**: Trivy reports a vulnerability once per
occurrence, and so do the registry's counts.

## 4. Scan protocol

The scanner authenticates with an API key bound to the `scanner` role, which
the registry creates: `registry:read` (pull images) and `registry:scan` (new,
submit results). The package archive is shared by all registries, so its
endpoints require both on every OCI registry.

A run does one of two modes, chosen by the scanner's `-mode` flag.

### Full scan (`-mode full`)

For images never scanned, or scanned by another analyser version.

1. `GET /api/v1/scan/pending?analyzer=<name/version>&limit=N` → image
   manifests (registry, repo, digest) with no scan, or whose full scan was
   made by a different `analyzer` (`trivy/v0.76.2`: the Trivy module version
   built into the scanner). Trivy's package detectors and secret rules change
   between versions; the database does not count (that is the refresh).
2. The scanner pulls the image **by digest** through the loopback registry and
   runs Trivy twice: `trivy image --format cyclonedx` (the SBOM) and `trivy
   image --format json --scanners vuln,secret --list-all-pkgs` (packages,
   vulnerabilities and secrets; the layer analyses of the first run are
   reused from Trivy's cache).
3. `PUT /api/v1/scan/results?registry=&repo=&digest=` with the SBOM, the
   packages (context, package, vulnerabilities), the occurrences and the
   secrets. One transaction replaces the image's previous result. A package
   already in the archive keeps the vulnerabilities matched against the
   newest database.

Secrets are part of the layers, which never change for a digest, so they are
found by the full scan only.

When the analyser cannot scan an image (a file it cannot read, a blob the
registry does not hold), the scanner records it with `PUT
/api/v1/scan/failures?registry=&repo=&digest=` (`analyzer`, `error`). The
image is pending again a day later, or at once for another analyser; a
successful scan removes the record. Failures are exported as
`stiva_image_scan_failure_timestamp_seconds`, and a run fails only when it
could not record one.

### Vulnerability refresh (`-mode vulnerabilities`, daily)

New vulnerabilities are published every day for packages that do not change.
The refresh matches **every distinct package once** against today's database,
without reading any image:

1. `POST /api/v1/scan/packages/prune` removes the packages no image contains
   any more.
2. `GET /api/v1/scan/packages?after=<key>&limit=N` pages through the archive.
3. For each context, the scanner builds one CycloneDX document holding all of
   that context's packages with Trivy's own encoder, each package's BOM
   reference set to its key, and runs `trivy sbom` on it with the same
   options. Trivy carries the BOM reference into every vulnerability it
   finds, so each finding goes back to its package. A day's refresh is a
   handful of Trivy runs (one per OS release and per ecosystem), not one per
   image.
4. `PUT /api/v1/scan/packages/vulnerabilities` with up to 500 packages and
   their vulnerabilities. A package matched against a newer database already
   is left alone; keys that left the archive meanwhile are reported back.

### Verification

Against `trivy image` run directly on the original images, with the same
options and database, entry by entry (vulnerability, package URL, severity,
fixed version, number of occurrences): nginx 1.21.0 (Debian, 309), node
16-alpine (Alpine + npm, 89), python 3.9-slim (Debian + pip, 205), tomcat
9.0.50 (jars, 445), traefik 2.5.0 (Go binaries, 296), UBI 8.4 minimal (Red
Hat, 434): identical after the full scan, and identical after a refresh
started from an empty vulnerability table. 1,778 image vulnerabilities came
from 1,554 package vulnerabilities over 962 distinct packages.

### Results document (full scan)

```json
{
  "analyzer": "trivy/v0.76.2",
  "database": { "version": 2, "updated_at": "2026-11-18T06:12:40Z" },
  "options": { "ignore_unfixed": true, "ignore_policy": "sha256:…" },
  "sbom": { "format": "cyclonedx", "document": { … } },
  "packages": [
    { "context": { "class": "os-pkgs", "type": "debian", "os": { "Family": "debian", "Name": "12.8" } },
      "package": { "Name": "openssl", "Version": "3.0.15-1~deb12u1", "Identifier": { "PURL": "pkg:deb/debian/openssl@…" }, … },
      "vulnerabilities": [
        { "id": "CVE-2026-1234", "severity": "Critical", "fixed": "3.0.16-1", "title": "…" }
      ] }
  ],
  "occurrences": [ { "target": "debian 12.8", "package": 0, "count": 1 } ],
  "secrets": [
    { "rule_id": "aws-access-key-id", "category": "AWS", "severity": "Critical",
      "title": "AWS Access Key ID", "target": "/app/.env", "line": 3 }
  ]
}
```

Every list is required (empty when there is nothing); unknown fields are
refused; a package must occur somewhere in the image; a vulnerability is
listed once per package. Severities use exactly five values: `Critical`,
`High`, `Medium`, `Low`, `Unknown` (the spelling trivy-operator's metrics use,
so dashboards and rules keep working). `options` records how the findings
were filtered, so a result always says what it is a result of.

### What Stiva stores

- **SBOM**: a blob plus an OCI artifact manifest in the same registry and
  repository as the image: `artifactType: application/vnd.cyclonedx+json`,
  empty config, one layer (the document), `subject` = the image manifest. It
  is a referrer like any other: visible in the Explorer and to `oras
  discover`, removed with its image, reclaimed by garbage collection. Cache
  registries store it too; they are read-only for clients, not for Stiva.
  A new full scan replaces the previous SBOM artifact.
- `image_scans`: per image, the analyser, options, SBOM, secrets and scan time.
- `scan_packages` and `package_vulnerabilities`: the archive, with the
  database and options each package's vulnerabilities were matched with.
- `image_packages`: per image, target and package, the occurrences.

Everything of an image goes away with its manifest; packages no image
contains are pruned by the next refresh.

`GET /api/v1/scan/vulnerabilities?registry=&repo=&digest=` lists an image's
vulnerabilities (target, occurrences, package URL, name, version,
vulnerability), for whoever can read the image.

## 5. Where images run, and vulnerability sets

Each pre-warm agent reports, every round, the containers of its node with
the digest each runs and the node's platform (`PUT
/api/v1/scan/placements?registry=<cache>&node=<node>`, registry:warm); a
report replaces the node's previous one and counts for 30 minutes. A
container is resolved onto the image manifest it runs: the digest itself, or
the entry of the node's platform when the digest is an index.

A **vulnerability set** is, for one image and one severity, the set of its
(package, vulnerability) pairs, identified by their digest: images built from
the same base with the same vulnerabilities share it. It is the unit of
alerting and of tickets.

- `GET /api/v1/scan/sets?severity=Critical,High` lists the sets that run, with
  package names, number of vulnerabilities, images, containers and namespaces;
- `GET /api/v1/scan/sets/<set>?severity=Critical` gives one set in full: its
  packages with their vulnerabilities, and its images with where they run;
- `stiva_vulnerability_set{set, severity, packages, vulnerabilities,
  namespaces}` (Critical and High) is the number of running images that carry
  the set.

All three cover the registries the caller can read.

Alerts are raised per set, not per image: `StivaCriticalVulnerabilitySet`
(severity critical) and `StivaHighVulnerabilitySet` (severity warning) on
`max by (set, packages, vulnerabilities) (stiva_vulnerability_set{severity=...}) > 0`,
with the set's page in the `stiva_url` annotation. One alert, and so one
ticket, is one fix: the same packages with the same vulnerabilities, wherever
they run. Kosmos (alt-infra) writes the ticket's description from
`GET /api/v1/scan/sets/<set>`, as the declared service account
`kosmos-alt-infra`.

### Browsing findings

The interface (`/vulnerabilities`) reads, always in the registries the caller
can read and with the running containers counted from the placements:

- `GET /api/v1/findings/packages?q=` — packages whose name or purl contains
  `q`, with their vulnerabilities by severity, images and running containers;
- `GET /api/v1/findings/packages/<key>` — one package, its vulnerabilities
  and the images containing it (200 at most, the running ones first);
- `GET /api/v1/findings/vulnerabilities?q=&severity=` — vulnerabilities by
  identifier or title, or all of one severity, the most widely running first;
- `GET /api/v1/findings/vulnerabilities/<id>` — one vulnerability, the
  packages carrying it with the fixed version and the images containing them;
- `GET /api/v1/findings/image?registry=&repo=&digest=` — one image manifest:
  its packages (where they sit, their vulnerabilities), its secrets (rule,
  file and line, never the value), where it runs, and why its scan failed if
  it did. The SBOM document is `GET /api/v1/scan/sbom` with the same
  parameters.

The pages are the running sets (Critical, High), the vulnerability and
package searches, a page per set, vulnerability, package and image; the
explorer shows the findings of the image it displays, and the counts of each
platform of an index.

## 6. Metrics and alerts

`GET /metrics` (Prometheus text format) exposes, for the registries the caller
can read:

```
stiva_image_vulnerabilities{registry, repository, image_digest, platform, severity}       count
stiva_image_exposedsecrets{registry, repository, image_digest, platform, severity}        count
stiva_image_scan_timestamp_seconds{registry, repository, image_digest, platform}           full scan
stiva_image_vulnerabilities_timestamp_seconds{registry, repository, image_digest, platform} oldest package match
stiva_image_scan_failure_timestamp_seconds{registry, repository, image_digest, analyzer} last failed scan
```

`image_digest` is a digest a client can pull the image by. A scanned image is
exported under its own manifest digest (`platform=""`) and under the digest
of every index that lists it, with the platform it is listed for
(`platform="linux/amd64"`): a node that pulls a multi-platform tag records
the index digest, and the join below must find it.

Only non-zero counts are exported. `/metrics` requires authentication like the
rest of the API (fail closed): Prometheus scrapes it with an API key that has
`registry:read`.

Stiva does not know which cluster runs what, and it does not need to.
`kube_pod_container_info` (kube-state-metrics) carries the digest a container
is really running in `image_id`. A rule joins the two on the digest and
reproduces the trivy-operator alerts with the same labels (`namespace`,
`image_repository`, `image_tag`), the same `category`/`severity` and therefore
the same Alertmanager routing:

```promql
sum by (namespace, image_repository, image_tag) (
    max by (image_digest) (stiva_image_vulnerabilities{severity="Critical", platform=~"|linux/amd64"})
  * on (image_digest) group_right
    max by (namespace, image_digest, image_repository, image_tag) (
      label_replace(label_replace(label_replace(kube_pod_container_info,
        "image_digest", "$1", "image_id", ".*@(sha256:[0-9a-f]+)$"),
        "image_repository", "$1", "image", "^(?:[^/]+\\.[^/]+/)?([^:@]+).*$"),
        "image_tag", "$1", "image", "^[^@]*:([^:/@]+)(?:@.*)?$")
    )
) > 0
```

Because the join is on the digest, it does not matter whether a node pulled
the image from Stiva, through the cache or straight from an upstream: the same
content has the same digest. An image that Stiva has never seen has no
findings and raises no alert; `stiva_image_scan_timestamp_seconds` makes that
visible (a rule can alert on running digests without a scan), and
`stiva_image_vulnerabilities_timestamp_seconds` on findings that stopped
being refreshed.

## 7. Deployment

The Helm chart (`deploy/k8s/charts/registry`, values `scanner.*` and
`metrics.serviceMonitor.*`) deploys:

- the `stiva-scanner` image (`scanner/Dockerfile`) as two CronJobs that never
  overlap: `-mode=full` every 15 minutes (it exits at once when nothing is
  pending) and `-mode=vulnerabilities` once a day;
- a persistent volume for Trivy's cache: the vulnerability and Java databases
  (downloaded at the start of every run from `scanner.dbRepository` /
  `scanner.javaDbRepository`, so every analysis of a run uses the same
  database) and the layer analyses;
- the options: `scanner.ignoreUnfixed`, and `scanner.ignorePolicy`, a Trivy
  ignore policy (`.trivyignore.yaml`, each exception with its `statement`)
  mounted from a ConfigMap. Its digest is recorded with the results.
  Vulnerabilities are matched per package, once for every image that contains
  it, so a vulnerability exception limited to `paths` cannot be honoured: the
  scanner refuses such a policy (use `purls`, or no restriction). Secret
  exceptions may use paths;
- the declared service account `stiva-scanner` (role `scanner` on every
  registry). The chart generates its key once in the Secret
  `<release>-stiva-scanner` and mounts it both into the registry, which
  provisions the account from it at every start (`REGISTRY_SERVICE_ACCOUNTS`),
  and into the scanner. No account is created by hand; deleting the Secret and
  upgrading rotates the key;
- optionally a ServiceMonitor scraping `/metrics` with that same key.

The pre-warm agent (`warmer.enabled`, a DaemonSet) is declared the same way,
as `stiva-warmer` with the role `warmer` (`registry:warm` on the cache
registry only). It copies into the cache the images each node is running,
read from containerd's content store: of a multi-platform image the cache
gets the index and the platform the node runs, so the scanner analyses what
the cluster runs and nothing else, and no origin registry is asked.

A full-scan run exits non-zero when an image could not be analysed; that image
stays pending and is retried by the next run. A refresh run exits non-zero
when a context could not be matched; its packages keep their previous match.

## Coexistence with trivy-operator

Stiva takes over what is about images: SBOMs (`sbomGenerationEnabled: false`),
vulnerabilities and exposed secrets. trivy-operator keeps what is about the
cluster's state: configuration audit, RBAC assessment, infrastructure
assessment and compliance, as Kubernetes objects next to the resources they
describe.

1. Deploy Stiva with this feature and the scanner; let the full scan cover the
   images the cluster runs (the cache registry makes that automatic).
2. Add the Stiva rules next to the Trivy ones; compare the two sets of alerts.
3. Then, and not before: remove `TrivyCriticalVulnerabilities` and
   `TrivyCriticalExposedSecrets`, and turn off trivy-operator's
   `vulnerabilityScannerEnabled`, `exposedSecretScannerEnabled` and
   `builtInTrivyServer`. `TrivyCriticalMisconfiguration` and
   `TrivyCriticalRBACFindings` stay.
