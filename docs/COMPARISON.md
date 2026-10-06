# Stiva compared

Where Stiva sits among artifact registries, stated plainly — including where
it loses. Verify everything here against the projects themselves; versions
move and this file can go stale.

|  | Stiva | Sonatype Nexus | JFrog Artifactory | Harbor | zot / distribution |
|---|---|---|---|---|---|
| License | Apache-2.0 | OSS (EPL) / Pro | Commercial | Apache-2.0 (CNCF) | Apache-2.0 |
| Runtime | Single Go binary + Postgres | JVM stack | JVM stack | Many services (core, jobservice, DB, Redis…) | Single binary / minimal |
| OCI images | Yes | Yes | Yes | Yes (core focus) | Yes (core focus) |
| Non-OCI formats | 25 (Maven, npm, PyPI, Helm, Go, APT/YUM, …) | Very broad | Very broad | No (OCI only) | No (OCI only) |
| hosted / proxy / group | Yes | Yes (Stiva mirrors the vocabulary on purpose) | Yes | Proxy/cache, replication | No (single-repo model) |
| Pull-through cache for K8s | Yes, with node pre-warming | Via proxy repos | Via remote repos | Yes (proxy cache) | Via sync |
| Auth | Local, LDAP, OIDC/OAuth, SSO (Entra/Google/GitHub/GitLab/LinkedIn/CAS), API keys, anonymous-by-CIDR | LDAP, SAML, crowded | LDAP, SAML, OIDC, tokens | OIDC, LDAP, robot accounts | htpasswd, LDAP, OIDC (varies) |
| UI | Explorer, RBAC admin, SSO management | Full admin UI | Full admin UI | Full admin UI | Minimal / none |

## When Stiva fits

- You run Kubernetes and want **one** registry for images *and* language
  packages, without operating a JVM or a dozen microservices.
- You pull images onto nodes that cannot authenticate (edge, bare metal):
  address-restricted anonymous identities cover exactly that.
- You are on Nexus OSS today: hosted/proxy/group mean the same thing here,
  and the migration is mostly re-pointing clients.

## When it does not

- You need a format Stiva does not serve, or hosted REST APIs Stiva has not
  implemented (see [ROADMAP.md](../ROADMAP.md)).
- You need vendor support contracts, HA blueprints or a decade-old plugin
  ecosystem — the incumbents win those on maturity alone.
- You only serve OCI images and want the smallest possible moving part: the
  reference `distribution` or zot may be enough.
