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

## Next up
- [ ] **Multiple users & invites** (the schema already supports many users; this needs UI and roles)
- [ ] **Ingest rate limiting** per project (respond with `429` + `X-Sentry-Rate-Limits` so SDKs back off)
- [ ] **Login throttling** against brute force
- [ ] **Email alerts** (SMTP) and per-project alert rules (env filters, "only regressions", etc.)
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
