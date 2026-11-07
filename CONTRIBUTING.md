# Contributing to Burrow

Thanks for digging in! 🦊

## Ground rules
- **Keep it boring.** The stack is stdlib Go plus pure-Go SQLite, with server-rendered templates.
  Please open an issue before adding a dependency, a JS framework, or a new service.
- **Tests come with fixes.** If you fix a grouping or parsing bug, add the payload that broke
  it to `internal/event/*_test.go`. Real-world SDK payloads are gold.
- **Migrations are append-only.** Never edit a shipped entry in `internal/store/store.go`'s
  `migrations` list. Add a new one.
- **No inline scripts or styles.** The dashboard runs under a strict CSP. Put JS in
  `internal/web/static/app.js` and CSS in `style.css`.
- **Run checks before pushing:** `gofmt -l .`, `go vet ./...`, `go test -race ./...`

## Where things live
See the Architecture section of the README. The most interesting code is in
`internal/event/grouping.go`. Grouping quality is what makes or breaks an error tracker.

## Reporting a security issue
Please don't open a public issue. Use GitHub's private vulnerability reporting on this
repository instead.
