# 🦡 Burrow

**Lightweight, self-hosted, open-source error tracking that works with the Sentry SDKs you already use.**

Burrow is a single ~14 MB binary with an embedded dashboard and SQLite storage.
There's no Redis, no Kafka, no ClickHouse, and no 16 GB RAM requirement. Point your existing
Sentry SDK's DSN at it and your errors show up, grouped into issues, with stack
traces, breadcrumbs, tags and user context.

> Burrow is an independent project and is not affiliated with or endorsed by
> Functional Software, Inc. "Sentry" is their trademark. Burrow implements the
> publicly documented Sentry ingestion protocol so the MIT-licensed Sentry SDKs
> can talk to it.

## Features

- **Drop-in SDK compatibility.** It accepts `/api/<id>/envelope/` and legacy `/api/<id>/store/`,
  gzip/deflate bodies, all SDK auth styles, and CORS for browser SDKs. It's tested against
  the official `sentry-sdk` (Python) and `@sentry/node`.
- **Smart grouping.** Events group by exception type plus in-app call path. Line numbers,
  JS bundle hashes, and the volatile parts of messages (numbers, UUIDs, IPs, hex,
  timestamps, quoted values) are ignored, so `timed out after 3000ms` and `timed out after
  4521ms` land in the same issue. SDK-side `fingerprint` (including `{{ default }}`) is honored.
- **Teams & access control.** Projects belong to teams, and people only see their teams'
  projects. **Owners** manage members, invites, and projects. **Members** view and triage.
  **Instance admins** manage everyone. Invite links are single-use, expire in 7 days, are
  stored hashed, and can be locked to an email address.
- **Issue workflow.** Resolve, ignore, or delete issues. A resolved issue that happens again
  **reopens automatically** (regression).
- **Event detail.** Chained exceptions, stack frames with source context and locals,
  breadcrumbs, request, user, tags, contexts, raw JSON, and a 14-day activity chart.
- **Alerts.** An optional webhook fires on new and regressed issues. It works with Slack,
  Discord, Mattermost, or any custom endpoint.
- **Idempotent ingestion.** SDK retries never double-count.
- **Retention.** Old events are pruned automatically (30 days by default).
- **Secure defaults.** bcrypt passwords, login throttling, CSRF tokens, a strict CSP,
  SameSite cookies, and a non-root distroless container. Changing your password signs out
  your other sessions.
- **Accessible.** WCAG AA contrast in light and dark themes, full keyboard support, and
  screen-reader labels. Every page is checked with axe-core. Set in
  [Atkinson Hyperlegible Next](https://www.brailleinstitute.org/freefont/) and JetBrains Mono,
  both self-hosted (OFL) so the dashboard makes no third-party requests.

## Quick start

### Docker

```sh
docker compose up -d
open http://localhost:8000
```

### From source (Go 1.26+)

```sh
go build -o burrow ./cmd/burrow
./burrow
```

On first visit you'll create the admin account and your first team. Then create a
project, copy its DSN, and:

```python
import sentry_sdk
sentry_sdk.init(dsn="http://<key>@localhost:8000/1")
1 / 0
```

Not ready to wire up an SDK? Every project has a **Send test event** button.

To add teammates, open your team and click **Create invite link**. There's no email setup
to do: send them the link however you like.

### Upgrading from v0.1

Just run the new binary on your existing database. Existing accounts become instance admins
and owners of a new **Default** team that holds all your current projects. Sessions,
issues, and DSNs keep working unchanged.

## Configuration

Every option can be set with a flag or an environment variable.

| Env var | Flag | Default | Description |
|---|---|---|---|
| `BURROW_ADDR` | `-addr` | `:8000` | Listen address |
| `BURROW_DB` | `-db` | `burrow.db` | SQLite database file |
| `BURROW_BASE_URL` | `-base-url` | `http://localhost<addr>` | Public URL. It goes into DSNs and alert links, and `https://` enables secure cookies. **Set this in production.** |
| `BURROW_RETENTION_DAYS` | `-retention-days` | `30` | Delete events older than this. `0` keeps them forever. |
| `BURROW_WEBHOOK_URL` | `-webhook-url` | | POST new or regressed issues here |

### Running in production

- Put Burrow behind a TLS-terminating reverse proxy (Caddy, nginx, Traefik) and set
  `BURROW_BASE_URL=https://errors.example.com`.
- Back up the SQLite file. `sqlite3 burrow.db ".backup backup.db"` is safe while it's running.
- Point SDKs at the same public URL. Browser SDKs need it reachable from your users' browsers.

## Architecture

```
cmd/burrow          entrypoint, config, background maintenance
internal/ingest     Sentry-protocol endpoints: envelope parsing, auth, decompression
internal/event      event normalization (many SDK shape variants -> one) + grouping
internal/store      SQLite (pure Go, no cgo), migrations, queries
internal/web        dashboard: server-rendered html/template, embedded static assets
internal/notify     webhook alerts
```

Design choices, in short: **boring tech, one process, one file of state.** Server-rendered
HTML means no JS build chain for contributors, and pages work even with JavaScript disabled.
SQLite in WAL mode comfortably handles the error volumes of small and medium teams. See
[ROADMAP.md](ROADMAP.md) for where it's going, including Postgres support for larger installs.

## Development

```sh
go test ./...          # unit + end-to-end tests (spins up a real server)
go run ./cmd/burrow    # http://localhost:8000
```

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
