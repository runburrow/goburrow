# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users
Solo developers and small teams who self-host Burrow (confirmed by owner). They run it on a small VPS or home server, open the dashboard to triage a new or regressed error, read the stack trace, decide resolve / ignore / fix, and leave. Visits are short and task-driven, often triggered by a webhook alert in Slack/Discord/Mattermost. The first user is the person who installed it: they create the admin account, create a project, and copy the DSN.

## Product Purpose
Lightweight, self-hosted, open-source error tracking that works with the Sentry SDKs teams already use. Success: an error sent from an existing Sentry SDK shows up, grouped into one issue, with enough context (stack frames, breadcrumbs, tags, user, request) to fix it, without running Redis, Kafka, or ClickHouse.

## Positioning
A single ~14 MB binary with an embedded dashboard and SQLite storage ("the error tracker you can run on a $5 VPS and forget about"). Measured: ~15 MB of memory idle, under 30 MB while ingesting ~1,750 events/s on one core, ~5 KB of disk per event (docs/benchmarks.md). Small and understandable rather than feature-parity with Sentry. Sentry-SDK compatible by implementing the public ingestion protocol.

## Operating Context
- Server-rendered Go html/template pages; no JS build chain; pages must work with JavaScript disabled (app.js is progressive enhancement only).
- Strict CSP: no inline scripts or third-party hosts; assets are embedded in the binary.
- Screens: setup (first-run admin account), login, projects list, new project, project settings (setup steps per platform, DSN, report API, key rotation, webhooks, test event), issues list (search, sort, status), issue detail (chained exceptions, frames with source context and locals, breadcrumbs, request, user, tags, contexts, raw JSON, 14-day activity chart; resolve/ignore/delete), error page.
- Issue states: unresolved, resolved, ignored; a resolved issue that recurs reopens as a regression. Levels: fatal, error, warning, info, debug.
- Light and dark colour schemes follow the OS setting.

## Capabilities and Constraints
- Plain CSS, no build step, no external fonts or CDNs (single binary, strict CSP) — design must be deliverable in `internal/web/static/style.css` and the templates.
- Planned (ROADMAP, not built, do not depict as shipped): multiple users and invites, tag facets, releases, source maps, merge/unmerge, email alerts, per-project alert rules.
- Non-goals: session replay, full APM/tracing UIs.
- Trademark: independent project, not affiliated with Functional Software, Inc.; never imitate Sentry's branding.

## Brand Commitments
Name: Burrow. Mascot: a fox (owner-confirmed; the current UI still uses a badger emoji and `internal/web/static/favicon.svg`, which the redesign replaces). The owner wants the mascot to have real moments: playful illustration and animation, e.g. the fox burrowing into the ground while the app searches for bug details. The product should be fun to use, especially for smaller teams. The owner asked for a full redesign: the existing earthy palette and layout are evidence of the subject, not binding. Name and the self-hosted / small / boring-tech positioning are kept.

## Evidence on Hand
No customers, testimonials, benchmarks, or usage numbers exist; do not fabricate them. Demonstration data may be authored but must be labelled synthetic. A seed script for realistic demo data is at `.design-review/seed.py` (untracked; targets localhost:8010).

## Product Principles
1. Triage first: what broke, how often, is it new or a regression, within a glance.
2. Small and boring on purpose: no feature theatre, one process, one file of state.
3. Honest about state: never imply planned features exist; show real empty, error, and first-run states.
4. Works everywhere: no JS required, no external requests, accessible in light and dark. Playful motion must be CSS-only where possible and honour prefers-reduced-motion.
5. Delight without getting in the way: the fox's personality shows in states and transitions, never at the cost of scanning an issue list.

## Accessibility & Inclusion
No product-specific standard was stated. Assumed (unconfirmed): WCAG AA contrast, keyboard operable, respects prefers-color-scheme.
