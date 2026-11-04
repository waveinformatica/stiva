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
- [x] Cleanup policies: OCI tags and package versions for every other format (keep-last-N, max age, protected patterns, dry-run first)
- [x] Checksum verification on download (OCI digests always; published checksums for proxied artifacts: warn/enforce)
- [x] Cosign signature verification with public keys: signed/unsigned badge in the Explorer, warn/enforce on pull
- [x] Background jobs: PostgreSQL-backed queue, run history, schedules for cleanup and garbage collection

## Planned

- [ ] Webhooks & event notifications
- [ ] Cosign keyless verification (Fulcio/Rekor) and the new bundle format
- [ ] Blob store replication / sharding
- [ ] Full hosted REST APIs for Conan / p2 clients
