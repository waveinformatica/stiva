# Security Policy

## Supported versions

Only the latest release is supported with security fixes. If you are behind,
upgrade first and check whether the issue reproduces.

## Reporting a vulnerability

**Please do not open a public issue.** Contact the maintainers privately so a
fix can ship before disclosure. Include:

- what you did, step by step, to reproduce;
- what you expected vs. what happened;
- the Stiva version (image tag or commit) and how it is deployed.

You will get an acknowledgement, and we will keep you posted until a fixed
release is out. Public credit in the release notes is yours if you want it.

## Scope notes

Stiva is designed to fail closed: authentication, authorization, vault
decryption and routing deny when they cannot decide. If you find a path that
fails *open* — access granted where it should not be — that is always treated
as a vulnerability, not a bug.
