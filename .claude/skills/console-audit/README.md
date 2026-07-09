# console-audit

A Claude Code skill that drives the **running** Nudgebee app through its tabs with the
`chrome-devtools` MCP and reports, per tab:

- **Console** — errors + warnings (app-originated only, deduped)
- **Network** — failed (≥ 400) and slow (> 1.5 s) requests
- **Performance** — LCP / CLS / INP + insights (reload trace)
- **Lighthouse** — accessibility / best-practices / SEO

It also drives **interactions** (drilldowns, form submits) so it catches the errors that
only fire on click/submit, not just on page load.

## Prerequisites (one-time)

1. **chrome-devtools MCP** connected in your Claude Code session. It manages its **own**
   Chrome instance (separate from your daily browser) and launches it on demand — you do
   not need to open a browser yourself. Verify with `/mcp` (chrome-devtools should be listed).
2. **Dev server running**: `cd app && npm run dev` (defaults to `http://localhost:3000`).
3. **Credentials** for auto-login:
   ```bash
   cd .claude/skills/console-audit
   cp .credentials.example .credentials.local   # then edit with your LDAP username/password
   ```
   `.credentials.local` is gitignored — your password never gets committed.
   **Auto-login supports LDAP only.** SSO / Google / magic-link users: log in once manually
   in the MCP Chrome window, then re-run (the skill reuses the session).

## Usage

```
console-audit                                   # DEFAULT = here (the page you're on) — cheap, no sweep
console-audit here                              # same as bare — the page the MCP browser is on
console-audit all                               # full sweep of every tab in routes.md (minutes)
console-audit home                              # one tab (routes.md id)
console-audit /cloud-account/details/<id>#ec2/instances   # an exact URL, no routes.md entry needed
console-audit cloud                             # every tab matching "cloud"
```

**Cluster context:** non-`here` runs pin the header cluster to the **Preferred cluster** in
`routes.md` (default `k8s-dev`) so findings stay comparable across runs. Override with
`cluster=<name>`, or `cluster=current` to keep your manual selection. The active cluster is
recorded in every report header.

**Dev vs prod for perf:** local `npm run dev` inflates LCP/TTFB (not representative). On dev,
trust **CLS / a11y / best-practices / SEO / structural insights**; ignore absolute timing. For
real perf numbers, audit a prod build or deployed env: `console-audit all +perf origin=<url>`.

**Collectors** — default is `console` + `network` (fast, token-cheap). Perf and Lighthouse are
opt-in because they're slow:
- `+perf` — add LCP/CLS trace (~5–10 s/tab)
- `+a11y` — add Lighthouse a11y/best-practices/SEO (**~45 s/tab — use sparingly**)
- `full` — everything · `console-only` — just console

**Persona presets** (run only what your role cares about):
- `ui` → console + a11y + perf · `backend` → console + network · `ai` → console + network + ask-nudgebee drilldown

**Flags:** `no-drilldown` (load-time only), `origin=<url>`, `no-reload` (`here` — audit current state as-is).

```
console-audit backend            # a backend dev: console + failed/slow APIs across the app
console-audit home +a11y         # Home, with accessibility scores
console-audit here +perf         # current page + LCP/CLS
```

**Why it stays cheap:** the skill installs an in-page hook that records console + failed/slow
network compactly, then pulls back only a reduced, deduped summary — the raw 2 KB stacks and
full request lists never enter the conversation. So even a full sweep stays token-light.
Lighthouse is the one heavy collector — opt-in only.

**The report is shown inline** in the response — no `.md` file to open, everything is right
there. Want a ticket from it? Run `/create-issue`. (Reports are point-in-time and not meant to
be committed.)

## Extending coverage

The tab list and drilldown recipes live in **`routes.md`**. Add a row to audit a new tab;
add a `### drilldown:` block to exercise an interaction. Ad-hoc single URLs don't need an
entry — just pass the URL.

## What it does NOT check

Functional correctness, API payload correctness, visual/pixel regressions, deep security,
full manual a11y, memory leaks. It is a console/network/vitals/lighthouse scanner, not a
functional test suite.

## Notes

- **Read-only**: never edits app code, never commits, never clicks destructive actions.
- **Dev-server caveat**: on `npm run dev`, TTFB/LCP are inflated by on-demand compilation
  (a heavy page can show LCP 7 s+). Treat LCP/TTFB as dev-inflated; **CLS and forced-reflow
  are genuine**. For real performance numbers, run against a production build.
- Files: `SKILL.md` (the procedure), `routes.md` (coverage matrix), `.credentials.example`
  (template), `.credentials.local` (your gitignored creds).
