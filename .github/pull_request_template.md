# Pull request

## What and why

<!-- One concern per PR. Link the issue if there is one. -->

## Checklist

- [ ] `go build ./... && go test ./...` passes
- [ ] `cd web && pnpm build` passes (if the UI changed)
- [ ] New behavior has tests where the repo already has them
- [ ] `README.md` updated (if behavior or configuration changed)
- [ ] New migration added as numbered up/down pair (if the schema changed)
- [ ] No secrets, tokens or credentials in the diff
