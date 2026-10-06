# Roadmap

Where Stiva is going, and what is already done. Items are checked off as they
land; anything unchecked is a statement of intent, not a promise.

## Done

- [x] Multi-format hosted/proxy/group registries with generated metadata
- [x] Deterministic host/port/base-path routing across registries
- [x] RBAC with scoped grants + scoped API keys
- [x] Browser SSO (Microsoft 365, Google, GitHub, GitLab, LinkedIn, OIDC, OAuth2, CAS, SAML)
- [x] Explorer: treeview, global search, per-format browsers
- [x] Signed APT Release (InRelease / Release.gpg + KEY.gpg, per-registry keys in the vault)
- [x] Garbage collection (orphan blob reaping, dry-run first)

## Planned

- [ ] Cleanup policies (retention, keep-last-N)
- [ ] Webhooks & event notifications
- [ ] Checksum verification on download + cosign signature verification
- [ ] Blob store replication / sharding
- [ ] Full hosted REST APIs for Conan / p2 clients
