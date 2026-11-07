---
name: Burrow
description: Self-hosted error tracking for the Sentry SDKs you already use; one small binary, one SQLite file, one fox.
colors:
  ground: "#0b0b0a"
  surface: "#121211"
  surface-raised: "#1c1c1a"
  cream-ink: "#f2efe6"
  muted: "#a9a79e"
  border: "#2f2f2b"
  rule: "#706f68"
  signal-red: "#e5322d"
  signal-red-text: "#ff7a73"
  signal-blue: "#8aa2ff"
  warn-ochre: "#e3b552"
  fox-rust: "#e07a43"
  ok-bg: "#141a14"
  err-bg: "#221413"
  code-bg: "#10100f"
  hit: "#3d1817"
  stone-ground: "#f2f1ee"
  stone-surface: "#faf9f6"
  stone-surface-raised: "#e6e3dc"
  stone-ink: "#1a1916"
  stone-muted: "#58564f"
  stone-border: "#d5d2ca"
  stone-rule: "#86847e"
  stone-red: "#bc2621"
  stone-red-text: "#a81d19"
  stone-blue: "#2f49a3"
  stone-warn: "#7d5200"
  stone-fox: "#b4531f"
  stone-ok-bg: "#e2e9df"
  stone-err-bg: "#f4ddd8"
  stone-code-bg: "#e8e5df"
  stone-hit: "#f5d5cf"
typography:
  display:
    fontFamily: "system-ui, -apple-system, Segoe UI, Roboto, Helvetica Neue, Arial, sans-serif"
    fontSize: "clamp(2.2rem, 3.4vw, 3.1rem)"
    fontWeight: 650
    lineHeight: 1.12
    letterSpacing: "-0.02em"
  headline:
    fontFamily: "system-ui, -apple-system, Segoe UI, Roboto, Helvetica Neue, Arial, sans-serif"
    fontSize: "2rem"
    fontWeight: 600
    lineHeight: 1.25
    letterSpacing: "-0.02em"
  title:
    fontFamily: "system-ui, -apple-system, Segoe UI, Roboto, Helvetica Neue, Arial, sans-serif"
    fontSize: "1.25rem"
    fontWeight: 600
    lineHeight: 1.25
  body-lg:
    fontFamily: "system-ui, -apple-system, Segoe UI, Roboto, Helvetica Neue, Arial, sans-serif"
    fontSize: "1rem"
    fontWeight: 400
    lineHeight: 1.6
  body:
    fontFamily: "system-ui, -apple-system, Segoe UI, Roboto, Helvetica Neue, Arial, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.55
  label:
    fontFamily: "system-ui, -apple-system, Segoe UI, Roboto, Helvetica Neue, Arial, sans-serif"
    fontSize: "0.8125rem"
    fontWeight: 600
    lineHeight: 1.5
  mono-code:
    fontFamily: "IBM Plex Mono, ui-monospace, SFMono-Regular, Consolas, Liberation Mono, monospace"
    fontSize: "0.8125rem"
    fontWeight: 400
    lineHeight: 1.7
  mono-figure:
    fontFamily: "IBM Plex Mono, ui-monospace, SFMono-Regular, Consolas, Liberation Mono, monospace"
    fontSize: "0.875rem"
    fontWeight: 500
    lineHeight: 1.4
    fontFeature: "tnum"
  wordmark:
    fontFamily: "IBM Plex Mono, ui-monospace, SFMono-Regular, Consolas, Liberation Mono, monospace"
    fontSize: "1.25rem"
    fontWeight: 500
    lineHeight: 1
    letterSpacing: "-0.03em"
rounded:
  sm: "4px"
  lg: "6px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "16px"
  xl: "24px"
  2xl: "32px"
  3xl: "48px"
components:
  button:
    backgroundColor: "transparent"
    textColor: "{colors.cream-ink}"
    typography: "{typography.body}"
    rounded: "{rounded.sm}"
    padding: "8px 14px"
    height: "40px"
  button-hover:
    backgroundColor: "{colors.surface-raised}"
    textColor: "{colors.cream-ink}"
  button-primary:
    backgroundColor: "{colors.cream-ink}"
    textColor: "{colors.ground}"
    typography: "{typography.body}"
    rounded: "{rounded.sm}"
    padding: "8px 14px"
    height: "40px"
  button-destructive:
    backgroundColor: "transparent"
    textColor: "{colors.signal-red-text}"
    rounded: "{rounded.sm}"
    padding: "8px 14px"
    height: "40px"
  button-destructive-hover:
    backgroundColor: "{colors.signal-red}"
    textColor: "{colors.ground}"
  button-persuade:
    backgroundColor: "transparent"
    textColor: "{colors.cream-ink}"
    rounded: "{rounded.sm}"
    padding: "10px 18px"
    height: "44px"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.cream-ink}"
    typography: "{typography.body}"
    rounded: "{rounded.sm}"
    padding: "8px 11px"
    height: "40px"
  pill:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    typography: "{typography.label}"
    rounded: "{rounded.sm}"
    padding: "1px 8px"
  pill-regression:
    backgroundColor: "transparent"
    textColor: "{colors.signal-red-text}"
    rounded: "{rounded.sm}"
    padding: "1px 8px"
  tab:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    typography: "{typography.body}"
    height: "44px"
  tab-active:
    textColor: "{colors.cream-ink}"
  tab-count:
    backgroundColor: "{colors.surface-raised}"
    textColor: "{colors.muted}"
    typography: "{typography.mono-figure}"
    rounded: "{rounded.sm}"
    padding: "0 6px"
  copybox:
    backgroundColor: "{colors.code-bg}"
    textColor: "{colors.cream-ink}"
    typography: "{typography.mono-code}"
    rounded: "{rounded.sm}"
    padding: "8px 8px 8px 12px"
  track-row:
    backgroundColor: "transparent"
    textColor: "{colors.cream-ink}"
    padding: "10px"
    height: "64px"
  track-row-hover:
    backgroundColor: "{colors.surface}"
  code-block:
    backgroundColor: "{colors.code-bg}"
    textColor: "{colors.cream-ink}"
    typography: "{typography.mono-code}"
    rounded: "{rounded.sm}"
    padding: "16px"
---

# Design System: Burrow

## Overview

**Creative North Star: "The Fox's Ledger"**

Burrow reads like a ledger kept by something small and patient that lives underground: near-black ground, cream ink, hairline rules, and figures in a typewriter-honest mono. Everything that carries information is drawn with the same instrument, the vertical hairline bar. Activity charts are fourteen 2px bars plotting real daily counts; the fox mascot is drawn from the same bars, so the brand and the data come from one pen. Colour is held back so the rare signals land: red means something broke or came back, rust means the fox is here, blue means you can act.

The system has two surfaces on one token set. **Operate** (the embedded dashboard) is dense, quiet, and triage-first: a 14px body, a five-step type scale, list rows at 64px, the fox kept to the edges of the work. **Persuade** (the landing page, in [runburrow/landing](https://github.com/runburrow/landing)) uses the same tokens, glyphs, bars and fox, and is allowed a larger display size, a 16px body, roomier sections, and one lifted container: the live sample issue list that is the hero. Persuade proves the product by running a piece of it at app scale, never by depicting it.

Light mode is warm stone rather than white or cream: a hint of the earth the fox digs in. Both schemes follow the OS setting; neither is the "alternate".

**Key Characteristics:**
- One token set across Operate and Persuade, dark first, warm-stone light.
- Hairline vertical bars as the shared drawing instrument (activity, fox, spec label barcode).
- Shape-coded severity glyphs, so level reads without colour.
- 4px corners, 1px rules, flat tonal layering; shadow only on the one floating dialog and the Persuade hero container.
- Sans for UI and prose; IBM Plex Mono for code, culprits, IDs, DSNs, figures and the wordmark.
- The fox appears in states and transitions, never inside the triage list.

## Colors

A near-monochrome warm ledger with three reserved signals, each with exactly one job.

### Primary
- **Cream Ink** (`cream-ink`; light `stone-ink`): all body text, and inverted as the fill of the primary button. The primary action is ink on ground, not a brand colour.

### Secondary
- **Signal Blue** (`signal-blue`; light `stone-blue`): the interaction colour. Focus rings (2px outline, 3px offset), link-hover underline, text selection, caret, checkbox accent, the 18% DSN highlight in code, the 14% tint on a just-arrived issue row, and the 18% mix into the primary button on hover.

### Tertiary
- **Fox Rust** (`fox-rust`; light `stone-fox`): the fox's coat and nothing else. Mark, peek, sit and burrow art only.

### Status
- **Signal Red** (`signal-red`, `signal-red-text`; light `stone-red`, `stone-red-text`): fatal level, regression pill, the hit line in source context (`hit` background with a red left rule), unhandled mechanism, fatal/error breadcrumbs, destructive buttons and the danger zone. `signal-red` draws borders and fills; `signal-red-text` is the legible text variant.
- **Warn Ochre** (`warn-ochre`; light `stone-warn`): warning level glyph and label only.
- **Feedback grounds** (`ok-bg`, `err-bg`): alert backgrounds; the error alert adds a `signal-red` border.

### Neutral
- **Burrow Black** (`ground`; light **Warm Stone** `stone-ground`): page ground.
- **Surface** (`surface`; light `stone-surface`): inputs, hovered rows, the Persuade stream and spec label, dialogs.
- **Raised Surface** (`surface-raised`; light `stone-surface-raised`): hover fill on buttons and rail items, the active rail item, selected rows, tab count chips.
- **Code Ground** (`code-bg`; light `stone-code-bg`): copyboxes, code blocks, source context, the incoming-event strip.
- **Muted** (`muted`; light `stone-muted`): secondary text, culprits, axis labels, info/debug levels, hole ring and dirt in the fox art.
- **Border** (`border`; light `stone-border`): the 1px structural rule between rows, panels, rail and topbar.
- **Rule** (`rule`; light `stone-rule`): the stronger 1px line on controls (buttons, inputs, pills, kbd), table heads, and zero-count bars. Holds 3:1 against the ground.

### Named Rules
**The Fox Owns Rust Rule.** `fox-rust` never marks state, severity, links, or emphasis. If something rust-coloured is not the fox, it is wrong.

**The Red Means Broken Rule.** Red is for fatal, regression, the failing line, and destructive actions. Never for brand, decoration, or "attention".

**The Ink Primary Rule.** The primary button is inverted ink (`cream-ink` on `ground`), hovering toward blue. No coloured CTA.

## Typography

**Body Font:** system sans (`system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif`)
**Label/Mono Font:** IBM Plex Mono 400 and 500 (embedded woff2), falling back to `ui-monospace, "SFMono-Regular", Consolas, monospace`

**Character:** A plain, fast UI sans for everything a person reads as language, and IBM Plex Mono for everything a machine produced or a person compares. The mono is the voice of the data; the wordmark `burrow` is set in it, lowercase, at -0.03em.

### Hierarchy
Operate uses five sizes only: 13 / 14 / 16 / 20 / 32px (`--fs-1`..`--fs-5`).
- **Headline** (600, `clamp(1.5rem, 2.4vw, 2rem)`, 1.25, -0.02em): page h1 in the dashboard. Issue titles on the detail page use `clamp(1.25rem, 2vw, 1.625rem)` at 1.3.
- **Title** (600, 20px, 1.25): h2 panel heads; the exception type uses this size in mono 600.
- **Body large** (600 for row titles / 400 prose, 16px): issue and project row titles, h3, Persuade body (1.6 line height).
- **Body** (400, 14px, 1.55): Operate default body, buttons, inputs, tabs. Prose max 72ch.
- **Label** (600, 13px): level labels, pills, help text, table heads, axis labels, breadcrumbs.
- **Mono code** (400, 13px, 1.7 to 1.8): culprits, frame files, source context, key-value keys, DSNs.
- **Mono figure** (500, 14px, tabular numerals): event counts, tab counts, stats.

Persuade adds two display sizes on top of the scale: **Display** (650, `clamp(2.2rem, 3.4vw, 3.1rem)`, 1.12, -0.02em) for the hero h1, and section h2 at `clamp(1.7rem, 3vw, 2.5rem)`, capped at 22ch. Persuade body is 16px with a 62ch measure and lead paragraphs in `muted`.

The display and headline roles currently render in the system sans because no display face has been chosen; that is a gap the build carries, not a rule. A designed display face, embedded like Plex Mono, may fill those roles without a system change elsewhere.

### Named Rules
**The Five Sizes Rule.** Operate screens use the five scale steps and nothing between them. A new size is a system change, not a local fix.

**The Machine Speaks Mono Rule.** Code, culprits, file paths, IDs, DSNs, keys, counts and figures are IBM Plex Mono with tabular numerals. Prose, labels, buttons and headings never are, except the wordmark.

## Layout

**Operate:** a 60px topbar with a 1px bottom rule, a 196px sticky left rail (40px items, 2px gap), and a main column padded 28px 32px 56px. Lists are CSS-grid "track rows" with fixed column tracks (issue list: check 22px, level 76px, title flexible, activity 140px, events 76px, seen 84px), 18px column gap, 64px minimum row height, 1px `border` between rows. Issue detail is a body column plus a 272px sidebar separated by a 1px rule. Panels are separated by a top rule and 24px vertical padding, not boxed.

Responsive Operate: at 1100px the activity column drops and the sidebar narrows to 240px; at 900px the sidebar stacks under the body; at 860px the rail becomes a horizontal strip and main padding goes to 22px 16px; at 700px list rows reflow into named grid areas with visible unit labels. Coarse pointers lift all buttons and crumb links to 44px.

**Persuade:** content width `min(1200px, 100% - 48px)` (32px gutter under 640px). The hero is a 1fr / 2fr grid: copy left, the live issue list right. Sections are separated by a 1px top rule with `clamp(64px, 9vw, 120px)` vertical padding; split sections use 5fr / 7fr. At 960px everything stacks and the hero reorders to headline, actions, live list, then supporting copy; at 640px issue rows reflow into areas and secondary nav links hide.

Spacing rhythm is a 4px base, used mostly at 8 / 12 / 16 / 24 / 32 / 48, with 10px and 14px as common in-component paddings.

## Elevation & Depth

Flat by default. Depth comes from tonal steps (`ground` to `surface` to `surface-raised`) and 1px rules, not shadow. Two things float and only they cast a soft ambient shadow: the keyboard-shortcuts dialog in Operate and the live stream container in the Persuade hero.

### Shadow Vocabulary
- **Dialog** (`box-shadow: 0 18px 50px -12px rgb(0 0 0 / .45)`, backdrop `rgb(0 0 0 / .45)`): the shortcuts dialog.
- **Hero lift** (`box-shadow: 0 30px 60px -36px rgb(0 0 0 / .6)`): the Persuade stream only.

### Named Rules
**The Tonal Step Rule.** Hover and selection raise a surface by one tonal step (`surface` for rows, `surface-raised` for controls and selected rows). They never add a shadow.

## Shapes

Small, square-shouldered geometry. Controls, inputs, pills, chips, copyboxes, code blocks and frames take a 4px corner (`rounded.sm`). Larger framed containers (the shortcuts dialog, the Persuade stream, the spec label, screenshots) take 6px (`rounded.lg`). The one exception is the phone screenshot on the landing page, which takes a 22px device corner so it reads as a phone. Every line is 1px; the only 2px lines are the focus ring and the active tab underline. Disclosure chevrons are drawn as 7px bordered corners rotated 45 degrees, not glyphs.

### Level Glyphs
Each level is a 9px shape before its label, drawn with a 1.5px `currentColor` border, so severity reads without colour (and in forced-colours mode):
- **Fatal:** filled diamond (square rotated 45deg), `signal-red-text`.
- **Error:** filled square, `cream-ink`.
- **Warning:** filled triangle (clip-path), `warn-ochre`.
- **Info:** hollow circle, `muted`, weight 500.
- **Debug:** hollow dashed square, `muted`, weight 500.

### Hairline Activity Bars
Fourteen bars, one per day, oldest left, spread across the column with `justify-content: space-between`. Each bar is 2px wide in `currentColor` (the text colour); zero days are a 1px stub in `rule`. List height 28px (26px on Persuade); the detail sidebar uses 52px tall, 4px bars. In Operate, heights come from `h0`/`h5`..`h100` classes in 5% steps, never inline styles (strict CSP).

### Named Rules
**The One Pen Rule.** Bars, fox and barcode are drawn from vertical hairlines with round caps. New illustration joins the family by being drawn the same way, not by adding a new style.

## Components

### Buttons
Quiet outlined controls; the primary is inverted ink.
- **Shape:** gently squared (4px), 1px `rule` border, 40px minimum height (44px on coarse pointers and on Persuade), 8px 14px padding, 500 weight at 14px.
- **Default:** transparent with `cream-ink` text. Hover fills `surface-raised` and the border goes to full ink.
- **Primary:** `cream-ink` fill, `ground` text; hover mixes 18% `signal-blue` into the ink.
- **Destructive:** `signal-red-text` text on a `signal-red` border; hover fills solid red with ground text.
- **Small:** 34px, 5px 10px, 13px. **Link button:** no border, underlined at 0.2em offset.
- **Focus:** 2px `signal-blue` outline, 3px offset everywhere. Disabled is 45% opacity.

### Pills and Chips
- **Status pill:** 1px `rule` border, 4px corner, 13px `muted` 500, capitalised. **Regression** switches to `signal-red-text` on a `signal-red` border at 600. **Ignored** is a dashed border.
- **Tab count:** `surface-raised` chip with a mono tabular figure.
- **Sample-data tag (Persuade):** dashed `rule` border, marks simulated content.

### Inputs / Fields
- **Style:** `surface` fill, 1px `rule` border, 4px corner, 40px tall, 8px 11px padding, 14px text, `muted` placeholder.
- **Focus:** 2px `signal-blue` outline at 1px offset, border turns blue. The search field shows the ring on its wrapper (`:focus-within`) with a muted search icon inside.
- **Error:** alerts on `err-bg` with a red border; no red input borders.

### Navigation
- **Topbar:** fox mark (34px) + mono wordmark, then a muted separator and the project crumb.
- **Rail:** 40px items, `muted` text; hover and active fill `surface-raised`, active is 600 weight. Account and version sit at the bottom above a 1px rule.
- **Status tabs:** 44px tall, `muted`, active is ink at 600 with a 2px ink underline overlapping the 1px tab rule.
- **Persuade header:** fox mark (36px) + wordmark, muted text links, one primary "Try the demo" button. Until the public demo exists it is a placeholder: "Demo coming soon" with a dashed `rule` border and `muted` text, inert (no link, not focusable), the same dashed language as the "Sample data" tag.

### Track Rows (signature)
The list unit for issues and projects. A grid row with a 1px bottom `border`, 64px minimum height, hover to `surface` with the title underlined in blue. The title link's `::after` covers the row so the whole row is the target; focus draws a 2px blue inset outline on the row. Title at 16px 600, culprit beneath in 13px mono `muted`. Selected rows (checkbox) fill `surface-raised`.

### Code Surfaces
- **Copybox / command:** `code-bg`, 1px `border`, 4px corner, mono text, a small outlined Copy button on the right (JS only). Persuade commands prefix `$ ` in `muted`.
- **Source context:** an ordered list with muted tabular line numbers and a 1px left rule per line; the failing line gets the `hit` background and a `signal-red` left rule with a red, 600-weight marker.
- **Stack frames:** a bordered stack of disclosures, 52px summaries, in-app frames in ink and library frames in `muted`.

### The Fox (signature)
One-ink bar-drawn art in three poses, all `aria-hidden`: **fox-mark** (52 viewBox, brand), **fox-peek** (head rising from a dashed hole ring, with grass ticks, dirt mound and particles in `muted`), **fox-sit** (full body with tail). Coat bars are `fox-rust` at 1.2px round-capped strokes; ear tips, ruff and tail tip are `cream-ink`; eyes and nose are ink with eye grounds and the hole cover in `ground`, so the art works on both schemes.

**Where it appears:** the brand mark; empty states (no issues, waiting for the first event, cleared, no results); setup, login and error pages; the search dig; and, on Persuade, the header and the closing section. Never inside a populated list, beside an issue row, or as decoration in data panels.

**Motion** (all inside `prefers-reduced-motion: no-preference`):
- **Blink:** eyes scale to 0.12 for two frames every 9s.
- **Wiggle:** brand mark rotates -7deg / 4deg over 460ms on hover or focus.
- **Burrow loop:** 2.6s crouch, head-first dive below the hole cover, scrabble with dirt bursts and a squashing mound, pop back up, ear twitch; loops with an 800ms delay while a new project waits for its first event (600ms on Persuade's closing fox).
- **Search dig:** a tight 700ms dive and scrabble that plays in place of the results (results dim to 25%, "Digging for issues" caption in `muted`), only when the typed query changed.
- **Pop-up:** 900ms rise from the hole with one dirt burst and two ear twitches, on the search results line, empty states, and error page.
- **Celebrate:** the sitting fox swishes its tail twice when the last issue is cleared.

**Reduced motion:** every animation and transition is removed globally; JS skips the search wait entirely, and the Persuade stream starts paused. The fox renders in its resting pose; nothing is hidden.

### Persuade-only: Live Issue Stream
The landing hero: a 6px-cornered `surface` container with the hero lift shadow, a header strip (project, unresolved count, "Sample data" tag, Pause), an incoming-event strip on `code-bg` in mono, and four issue rows built from the same level glyphs, hairline bars (height transitions 400ms `cubic-bezier(.16, 1, .3, 1)`), mono counts and a working Resolve button. New events flash a 14% blue tint for 900ms; new rows slide in 8px over 500ms. Resolved titles go `muted` with a `rule`-coloured strike. It works as a static list without JS.

## Do's and Don'ts

### Do:
- **Do** reserve `fox-rust` for fox art; use `signal-blue` for anything interactive and `signal-red` only for fatal, regression, the failing line, and destructive actions.
- **Do** keep Operate on the five sizes (13 / 14 / 16 / 20 / 32px).
- **Do** set code, culprits, IDs, DSNs, keys and every count in IBM Plex Mono with tabular numerals.
- **Do** pair every level colour with its glyph shape (diamond, square, triangle, circle, dashed square).
- **Do** draw activity as fourteen 2px hairline bars in `currentColor`, with 1px `rule` stubs for zero days, using height classes in Operate.
- **Do** separate content with 1px rules and tonal steps; keep corners at 4px for controls and 6px for large framed containers.
- **Do** give the fox moments in states and transitions (empty, waiting, searching, cleared, error) and gate all motion behind `prefers-reduced-motion: no-preference`.
- **Do** label any simulated content on Persuade as sample data.

### Don't:
- **Don't** use rust for state, severity, links or emphasis, or red for brand or decoration.
- **Don't** put the fox inside a populated issue list or data panel, or let its motion delay triage.
- **Don't** use a coloured primary button; the primary is inverted ink.
- **Don't** convey level by colour alone.
- **Don't** add shadows for hover or cards; only the dialog and the Persuade hero container float.
- **Don't** set prose, buttons or headings in mono (the lowercase wordmark is the one exception).
- **Don't** imitate Sentry's branding.
