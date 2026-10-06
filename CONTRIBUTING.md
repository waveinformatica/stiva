# Contributing to Stiva

Thanks for stopping by — issues and pull requests are welcome.

## Ground rules

- **Keep changes focused.** One PR, one concern. If you spot something else
  on the way, open a separate issue instead of widening the diff.
- **Match the existing style.** Short comments only where the code is not
  self-evident; no speculative generality (`You aren't gonna need it` applies
  doubly to a registry that stores other people's artifacts).
- **Fail closed.** Stiva sits in front of other people's software supply
  chain. When auth, authz, crypto or routing cannot decide, deny — and say
  why in the error the operator sees.
- **Deterministic behavior.** Same input, same outcome: no map-iteration
  order leaks, no silent fallbacks, no best-effort chains that hide failures.

## What to run

```bash
go build ./... && go test ./...   # backend (migrations-safe unit tests)
cd web && pnpm install && pnpm build   # typecheck + bundle (tsc && vite build)
```

Both must pass. New behavior needs tests where the repo already has them
(`internal/*/[a-z]*_test.go`); tests that need a live database do not belong
in the suite — keep them as throwaway scripts, never committed.

## Database

Schema changes go in `internal/storage/migrations/` as a new numbered
`up`/`down` pair (`IF NOT EXISTS` / `IF EXISTS` so they are idempotent).
They apply automatically on startup; never hand-edit a deployed database
when a migration can carry the change instead.

## Docs

If your change alters behavior or configuration, update `README.md` (and
`ROADMAP.md` when it moves an item). A user-facing change without docs is
half a change.

## Security

Do **not** open a public issue for vulnerabilities — see [SECURITY.md](./SECURITY.md).
