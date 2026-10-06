# Roadmap

Where Stiva is going, and what is already done. Items are checked off as they
land; anything unchecked is a statement of intent, not a promise.

## Done

- [x] Multi-format hosted/proxy/group registries with generated metadata
- [x] Deterministic host/port/base-path routing across registries
- [x] RBAC with scoped grants + scoped API keys
- [x] Browser SSO (Microsoft 365, Google, GitHub, GitLab, LinkedIn, OIDC, OAuth2, CAS)
- [x] Explorer: treeview, global search, per-format browsers

## Planned

- [ ] Garbage collection (orphan blob reaping)
- [ ] Cleanup policies (retention, keep-last-N)
- [ ] Webhooks & event notifications
- [ ] Signed metadata / checksum verification (APT `Release`, cosign)
- [ ] SAML SSO
- [ ] Blob store replication / sharding
- [ ] Full hosted REST APIs for Conan / p2 clients
