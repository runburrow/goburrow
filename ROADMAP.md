# Roadmap

Burrow's goal is to be the error tracker you can run on a $5 VPS and forget about.
These are rough priorities, not promises.

## v0.1 (current MVP)
- [x] Sentry SDK ingestion (envelope + store), gzip/deflate, CORS
- [x] Fingerprint grouping with message normalization and custom fingerprints
- [x] Issues: list, search, sort, resolve/ignore/delete, auto-reopen on regression
- [x] Event detail: exceptions, frames with context, breadcrumbs, tags, user, request, contexts
- [x] Projects with DSNs, key rotation
- [x] Webhook alerts, retention pruning, Docker image, CI

## v0.2
- [x] Per-project webhooks in settings (new issues and/or regressions, send test)
- [x] Report API for runtimes without a Sentry SDK, with Roblox (Luau) support
- [x] Setup steps for 20 platforms
- [x] Ingest rate limiting per project (`429` + `X-Sentry-Rate-Limits` so SDKs back off)
- [x] Login throttling, hashed session tokens, SSRF-safe webhooks
- [x] Bulk resolve/ignore with undo
- [x] **Teams**: projects belong to teams, with owner/member roles and instance admins
- [x] **Invite links**: single-use, expiring, hashed, optionally email-locked
- [x] Account page (change password, which ends other sessions) and admin user management
- [x] **Email**: SMTP for alert emails (per project, rate-limited) and sending invites, with test buttons

## Next up
- [ ] **SSO**: OIDC login (GitHub, Google, Authentik, Keycloak…)
- [ ] Per-project alert rules (env filters, etc.)
- [ ] **Tag facets** on the issue page (e.g. "80% of events: browser=Safari")
- [ ] **Source maps** for minified JavaScript stack traces
- [ ] **Merge / unmerge issues** manually
- [ ] **Release tracking**: "first seen in release X", and resolving in the next release
- [ ] Brotli (`br`) request bodies

## Later
- [ ] Postgres storage backend for bigger installs / HA
- [ ] REST API + API tokens (for CLI tools and integrations)
- [ ] Cron monitor check-ins (`check_in` envelope items)
- [ ] Basic performance/transaction summaries (currently accepted and dropped)
- [ ] Debug symbol (dSYM / PDB) support for native crash symbolication

## Non-goals (for now)
- Feature parity with Sentry. Burrow aims to be small and understandable, not a clone.
- Session replay and full APM/tracing UIs.
