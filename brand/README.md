# Burrow brand assets

All drawn from the same bar-drawn fox the app uses (`internal/web/templates/partials.html`).
Rust is the fox's colour and nobody else's.

## Fox (`fox/`)

Three poses: **sit** (the mascot, by its burrow), **peek** (coming out of the burrow) and
**mark** (the logo, used at small sizes). Each comes in two colourways:

| Suffix | For | Fox | Details |
|---|---|---|---|
| `on-dark` | dark backgrounds | `#e07a43` | `#f2efe6` |
| `on-light` | light backgrounds | `#b4531f` | `#1a1916` |

- `.svg`: any size; plain attributes, no stylesheet, opens in any editor.
- `.png`: transparent, 2048px wide (mark 1024px). The eyes are real cut-outs.
- `.jpg`: on the app's own background (`#0b0b0a` / `#f2f1ee`), with padding.

## Social (`social/`)

| File | Size | Use |
|---|---|---|
| `og.png` | 1200×630 | Link previews for the site and the app (Open Graph, X/Twitter) |
| `github-social.png` | 1280×640 | GitHub repository Settings → Social preview |
| `banner-dark.png`, `banner-light.png` | 1280×320 | README header; GitHub picks the one matching the reader's theme |

Copies of `og.png` and the icons live in `site/` and `internal/web/static/`, where the pages
reference them.
