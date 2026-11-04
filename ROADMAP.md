# Roadmap

Burrow's goal is to be the error tracker you can run on a $5 VPS and forget about.
These are rough priorities, not promises.

## v0.1 (current MVP)
- [x] Sentry SDK ingestion (envelope + store), gzip/deflate, CORS
- [x] Fingerprint grouping with message normalization and custom fingerprints
- [x] Issues: list, search, sort, resolve/ignore/delete, auto-reopen on regression
- [x] Event detail: exceptions, frames with context, breadcrumbs, tags, user, request, contexts
- [x] Single admin user, projects with DSNs, key rotation
- [x] Webhook alerts, retention pruning, Docker image, CI

## v0.2
- [x] **Teams**: projects belong to teams, with owner/member roles and instance admins
- [x] **Invite links**: single-use, expiring, hashed, optionally email-locked
- [x] Account page (change password, which ends other sessions) and admin user management
- [x] **Login throttling** (per account and per IP)
- [x] Accessibility pass: WCAG AA contrast, keyboard focus, axe-core clean

## Next up
- [ ] **Ingest rate limiting** per project (respond with `429` + `X-Sentry-Rate-Limits` so SDKs back off)
- [ ] **Email**: SMTP for alerts and for sending invites directly
- [ ] **Per-team webhooks** (the webhook is currently instance-wide)
- [ ] **SSO**: OIDC login (GitHub, Google, Authentik, Keycloak…)
- [ ] **Email alerts** and per-project alert rules (env filters, "only regressions", etc.)
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
