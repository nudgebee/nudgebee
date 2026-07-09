---
description: Audit the running app via chrome-devtools MCP — console errors/warnings + failed/slow network by default; perf (LCP/CLS) and Lighthouse (a11y/SEO) are opt-in. Token-efficient (compacts findings in-browser via a console/network hook); the report is shown inline in the response — no files to open. Target one tab / an exact URL / the page you're on (`here`) / the whole app, and drives interactions so click-triggered bugs are caught too. Usage: `console-audit [target] [ui|backend|ai | +perf | +a11y | full] [no-drilldown|origin=<url>|no-reload]` — e.g. `console-audit home`, `console-audit here +perf`, `console-audit backend`, `console-audit /kubernetes/details/<id>#events/all-events`.
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Write
  - Glob
  - Grep
  - mcp__chrome-devtools__list_pages
  - mcp__chrome-devtools__new_page
  - mcp__chrome-devtools__select_page
  - mcp__chrome-devtools__navigate_page
  - mcp__chrome-devtools__take_snapshot
  - mcp__chrome-devtools__take_screenshot
  - mcp__chrome-devtools__wait_for
  - mcp__chrome-devtools__click
  - mcp__chrome-devtools__fill
  - mcp__chrome-devtools__fill_form
  - mcp__chrome-devtools__list_console_messages
  - mcp__chrome-devtools__list_network_requests
  - mcp__chrome-devtools__get_network_request
  - mcp__chrome-devtools__performance_start_trace
  - mcp__chrome-devtools__performance_stop_trace
  - mcp__chrome-devtools__performance_analyze_insight
  - mcp__chrome-devtools__lighthouse_audit
---

# Console / Performance Audit

Drive the **running** Nudgebee app (default `http://localhost:3000`) through a list of tabs and drilldowns using the `chrome-devtools` MCP, and produce a per-tab report of:

- **Console** — errors + warnings (deduped, app-originated only)
- **Network** — failed (status ≥ 400) and slow (> 1500 ms) requests
- **Performance** — LCP, CLS, TBT + top insights (via a reload trace)
- **Lighthouse** — accessibility, best-practices, SEO scores

## Arguments — pick a target, no editing needed

`$ARGUMENTS` is a space-separated list of **one target** + optional **flags**. The target decides *what* to audit; default (no target) is the full `routes.md` sweep.

**Target modes** (first non-flag token):
| you type | meaning |
|----------|---------|
| *(nothing)* | **defaults to `here`** — audit the page you're already on (cheap, intuitive). Full sweeps must be explicit (`all`). |
| `here` / `current` / `.` | audit **whatever page the browser is already on** (see the `here` branch in Step 1 for the blank-browser bootstrap) |
| `all` / `sweep` | full sweep of every tab in `routes.md` |
| a routes.md id, e.g. `home`, `cloud-summary`, `k8s-monitoring-traces` | audit just that one tab (ids filled from the live session) |
| a path or URL, e.g. `/cloud-account/details/883efbbc-…#ec2/instances`, `kubernetes/details/a2a30b02-…#events/all-events`, `http://localhost:3000/home` | audit **exactly that URL** — navigate there directly, even if it's not in `routes.md`. A leading `/` or bare `host/path` is treated as a path and gets the origin prepended; a full `http(s)://…` is used as-is. Keep the `#fragment`. |
| a loose word that matches several ids/urls as a substring, e.g. `cloud`, `optimize` | audit every matching tab (a mini-sweep) |

**Collectors** — choose what to run (default = `console` + `network`, the cheap high-signal pair). Lighthouse and perf are **opt-in** because they're the expensive ones.
- `+perf` — add the reload perf trace (LCP/CLS/INP). ~5–10 s/tab.
- `+a11y` (or `+lighthouse`) — add the Lighthouse audit (a11y/best-practices/SEO). **~45 s/tab — use sparingly.**
- `full` — everything (console + network + perf + lighthouse).
- `console-only` — just console (skip network too).

**Persona presets** (a collector bundle tuned to a role):
- `ui` → console + a11y + perf (a UI dev cares about warnings, accessibility, CLS)
- `backend` → console + network (failed APIs, slow/chatty calls)
- `ai` → console + network, and auto-includes the `ask-nudgebee` drilldown (LLM/streaming/tool-call flows)

**Flags** (any order, combine with a target/collectors):
- `no-drilldown` — load-time only; skip the L2 interaction recipes.
- `origin=<url>` — override the base origin (e.g. `origin=http://localhost:3001`, or a deployed/prod build for real perf numbers).
- `cluster=<name>` — pin a specific cluster for this run (default is the `Preferred cluster` in `routes.md`, `k8s-dev`); `cluster=current` keeps whatever you've manually selected.
- `no-reload` — for `here` mode only: audit the page's **current** state without reloading (use when you've set up a specific interacted state you want captured as-is). Default is reload-first (see below).

> Default (no collector/persona) is **console + network** — fast and token-cheap. Add `+perf` / `+a11y` only when you need them. Example: `console-audit home +a11y`, `console-audit backend`, `console-audit here +perf`.

**Examples**
- `console-audit home` → just the Home tab (all collectors).
- `console-audit /cloud-account/details/883efbbc-…#ec2/instances` → that exact URL, even though it isn't in `routes.md`.
- `console-audit here console-only` → fast console/network check of the page you're already looking at.
- `console-audit k8s-monitoring-traces` → the traces tab + its drilldown recipe.
- `console-audit cloud` → every `cloud*` tab.

The default full-sweep matrix lives in **`routes.md`** next to this file. Edit it only to change what the *no-target* sweep covers or to add reusable drilldown recipes — ad-hoc single URLs don't need an entry.

---

## Step 0 — Preflight (browser + session)

> **`here` mode is conditional here.** Always do 0.1's `list_pages` connectivity check first. Then: if a **real app page is already open**, `here` skips the rest of Step 0 (no base-origin nav, no auth, no id-capture) and audits that page in place. If the browser is **blank/unauthenticated** (fresh MCP Chrome), `here` instead **falls through to the full Step 0** (navigate base origin + auto-login) and audits the landing page — see Step 1's `here` branch for the exact fallback + messages.

1. Confirm the MCP browser is connected and the app is up:
   - `mcp__chrome-devtools__list_pages`. If it errors, tell the user the chrome-devtools MCP isn't connected (start it / open Chrome) and stop.
   - **(skip for `here`)** `mcp__chrome-devtools__navigate_page` → the base origin (default `http://localhost:3000`, or the override from `$ARGUMENTS`). If it can't connect, ask the user to start the dev server (`cd app && npm run dev`) and stop.
2. **Auth check + auto-login.** After navigating to the base origin, `take_snapshot`. A fresh MCP Chrome is **not** authenticated and redirects to `/signin?...error=SessionRequired`. (If it lands on `/home` directly, a session already exists — skip login.) When on `/signin`, log in via **LDAP** using the gitignored local credentials:
   - `Read` `.claude/skills/console-audit/.credentials.local` for `USERNAME` and `PASSWORD`. If the file is missing, tell the user to create it (copy the format below) and stop. **Never print the password** in your output.
   - `take_snapshot` → `click` the **"Login via LDAP"** button (accessible name from its title/subtitle).
   - `take_snapshot` → `fill` the **LDAP Username** input (`#ldapUsername`) with `USERNAME`, then `fill` the **LDAP Password** input (`#ldapPassword`) with `PASSWORD`.
   - `click` the **"Sign in"** button.
   - `wait_for` text `Home` (successful landing on `/home`). If still on `/signin` after ~5 s, the creds are likely wrong/expired — report that and stop (do **not** retry in a loop or brute-force).
   - **LDAP only.** This auto-login drives the LDAP form. If a teammate signs in with SSO / Google / magic-link instead, don't attempt it — tell them to log in once manually in the MCP-controlled Chrome window, then re-run (the existing session is reused and login is skipped).

   `.credentials.local` format (this file is gitignored; values live only on the machine):
   ```
   USERNAME=<ldap-username>
   PASSWORD=<ldap-password>
   ```
   Note: the password value is passed to the MCP `fill` tool, so it is visible in the session tool-call log (local only) — it is never written to git.
3. **Capture ids + pin the cluster context.** From the `/home` snapshot capture the current `accountId` (nav-link query param) + one cloud-account detail id + one kubernetes cluster id — these fill the `{accountId}`/`{cloudDetailId}`/`{clusterId}` placeholders in `routes.md`. Never hardcode ids; they differ per environment.
   - **Cluster pin (reproducibility).** Read the active cluster from the header cluster selector (the combobox showing e.g. `dev-aws` / `k8s-dev`). Compare it to the **Preferred cluster** in `routes.md` (default `k8s-dev`, pinned **by name**, not id — the id is resolved live). If they differ, switch to the preferred cluster via that selector so every default run audits the **same** context (findings are only comparable — and the future baseline/diff only works — if the cluster is constant). Overrides: `cluster=<name>` pins a different cluster for this run; `cluster=current` keeps whatever the user has manually selected (this is the "respect my manual choice" escape hatch).
   - **Always record** the resolved `cluster` + `accountId` in the report header, so every report says which context produced it.

   *(`here` mode skips this step — it audits the page and cluster the user is currently looking at, as-is.)*

## Step 1 — Resolve the target into a run list

First parse `$ARGUMENTS` into `{ target, flags }` (see **Arguments** above), then branch:

- **`here` / `current` / `.`** — `list_pages` → current URL, then:
  - **Real app page already open** (URL is on the base-origin host): audit it **in place** — skip preflight, reload that URL with the hook (`navigate_page type=reload` + `initScript`), then collect. (With `no-reload`, collect current state as-is.) This is the normal `here` case.
  - **Nothing useful open** (`about:blank`, `chrome://`/`chrome-devtools://`, or a non-app host — common when the MCP just launched a fresh Chrome): **don't give up — bootstrap the app.** Run the normal preflight: `navigate_page` → base origin, then Step 0.2 **auth/auto-login**.
    - Auth **succeeds** → the browser lands on `/home`; audit that as the run item and tell the user: *"Nothing was open, so I opened the app and audited Home. Pass a target (e.g. `console-audit /cloud-account/...#summary`) to audit a specific page."*
    - Auth **fails** (creds missing/wrong, or SSO) → stop with: *"Couldn't sign in automatically. Please sign in manually in the MCP-controlled Chrome window, then re-run `console-audit`."*

  Label the run item by its path+hash.
- **A path or full URL** (starts with `/`, or `http(s)://`, or looks like `host/path` / contains `#`) — build one run item from it: if it's a path, prepend the origin; use a full URL as-is. **No id-capture or `routes.md` lookup needed** — the user gave concrete ids. Label by path+hash.
- **`all` / `sweep`** — the full `routes.md` matrix (this is the only way to trigger a full sweep; it's never the default).
- **A routes.md id or substring** — `Read` `routes.md`, substitute the captured ids (Step 0.3) into `{...}` placeholders, then keep tabs whose id **or** url contains the target. Exact-id match → just that one.
- **No target (bare `console-audit`)** — treat as **`here`** (audit the current page, with the blank-browser bootstrap above). A bare command must never kick off the expensive full sweep.

Then emit a one-line plan to the user: the tab(s), which collectors are on (respect the collector flags), and est. time (~15–30 s/tab with `+a11y`; ~3–5 s/tab default). For a **single target**, just run it — no need to ask. For `all`, confirm first (it's minutes). Then proceed to Step 2.

> A single-tab run is the common case (`console-audit home`, `console-audit here`, or a pasted URL). Keep it fast: one navigate → collect → (drilldown if the tab has a recipe and `no-drilldown` isn't set) → report. No batching concerns at one tab.

## Step 2 — Per-tab audit loop (token-efficient)

**Golden rule: raw browser data must never land in the conversation.** Capture it compactly *inside the page* and pull back only the reduced result. This is what keeps a 25-tab sweep cheap — each tab costs ~200 bytes of context instead of ~10 KB of stacks.

For **each** tab, in order:

1. **Navigate with a capture hook.** `navigate_page` (or `type: reload` in `here` mode) passing this `initScript`. **CRITICAL: `initScript` runs a script *body*, not a function it calls for you — so the hook MUST be a self-executing IIFE `(() => { … })();`.** A bare `() => { … }` just constructs a function that never runs, `window.__audit` is never installed, and the read-back returns an empty default that is indistinguishable from a genuinely clean page (a silent false-negative). The `installed` sentinel below is the liveness signal that proves the hook actually ran.
   ```js
   (() => {
     if (window.__audit && window.__audit.installed) return;               // idempotent across reloads
     const A = (window.__audit = { console: [], net: [], installed: true, v: 2 });
     const frame = (s) => (String(s).match(/\/(src|app)\/[^\s)]+?:\d+/) || [])[0] || '';
     for (const lvl of ['error', 'warn']) {
       const orig = console[lvl];
       console[lvl] = (...a) => {
         try {
           const txt = a.map(x => { try { return x instanceof Error ? x.message : (x && typeof x === 'object' ? JSON.stringify(x) : String(x)); } catch (_) { return String(x); } }).join(' ').slice(0, 200);
           A.console.push({ lvl, txt, src: a.map(frame).find(Boolean) || frame(new Error().stack) || '' });   // handle Error/object args; stack fallback for plain-string logs
         } catch (e) {}
         return orig.apply(console, a);
       };
     }
     const rec = (x, status, ms, err) => {                                  // x may be a string, Request, or URL
       if (status < 400 && ms <= 1500 && !err) return;
       const url = x && typeof x === 'object' ? (x.url || x.href || String(x)) : String(x);
       A.net.push({ url: url.slice(0, 120), status, ms: Math.round(ms), ...(err ? { err: String(err).slice(0, 80) } : {}) });
     };
     const f = window.fetch;                                               // fetch
     if (f) window.fetch = async (...a) => {
       const t0 = performance.now();
       try { const r = await f(...a); rec(a[0], r.status, performance.now() - t0); return r; }
       catch (e) { rec(a[0], 0, performance.now() - t0, e); throw e; }
     };
     const XP = XMLHttpRequest.prototype, xo = XP.open, xs = XP.send;      // XHR (axios/legacy — fetch-only leaves a gap)
     XP.open = function (m, u, ...r) { this.__u = u; return xo.call(this, m, u, ...r); };
     XP.send = function (...r) {
       const t0 = performance.now();
       this.addEventListener('loadend', () => rec(this.__u, this.status, performance.now() - t0, this.status === 0 ? 'xhr-error' : null));
       return xs.apply(this, r);
     };
   })();
   ```
   React's prop-type / DOM-nesting / ref warnings all route through `console.error`, so the hook catches them — recording only the first `src/`|`app/` frame, not the 2 KB stack. Both `fetch` and `XMLHttpRequest` are wrapped (axios and legacy code use XHR). It is also **clear-proof**: capture starts at load, so the DevTools "clear" button (visual only) can't hide anything.
2. **Settle** — `wait_for` the tab's `waitFor` text (from `routes.md`), else a short fixed settle. For hash sub-tabs, navigate the full `url#fragment` so the SPA router runs. Use a generous per-nav `timeout` (~25 s) — these pages are heavy; on timeout, collect what loaded and note `slow-load`, never hang.
3. **Collect console + network in ONE call** — `evaluate_script` that returns the already-compact, already-noise-filtered result (dedupe + drop noise happen *in the browser*):
   ```js
   () => {
     const a = window.__audit;
     if (!a || !a.installed) return { error: 'HOOK_NOT_INSTALLED' };   // liveness: NOT the same as "clean page"
     const drop = /\[HMR\]|Fast Refresh|MIME type \('application\/json'\)|chrome-extension/;
     const seen = {}, out = [];
     for (const m of a.console) {
       if (drop.test(m.txt) || (!m.src && !/Warning:|Error/.test(m.txt))) continue;
       const k = m.txt.slice(0, 80) + '|' + m.src;
       if (seen[k]) { out[seen[k] - 1].count++; continue; }
       seen[k] = out.push({ ...m, count: 1 });
     }
     return { installed: true, rawCounts: { console: a.console.length, net: a.net.length }, console: out, net: a.net };
   }
   ```
   **Trust the liveness signal.** If this returns `{ error: 'HOOK_NOT_INSTALLED' }` (or no `installed: true`), the hook never ran — do **NOT** report "0 findings / clean". Re-inject via a fresh reload with the `initScript`, or fall back to one `list_console_messages` call; then flag that the tab used the fallback. `rawCounts` also lets you sanity-check the reduction (e.g. `raw 17 → 5 deduped`) so a silently-empty collector can't masquerade as a green tick. For large results, use `evaluate_script`'s `filePath` to stream straight to disk; keep only a one-line-per-finding summary in context.
4. **Performance** — only if `+perf` / `full`. `performance_start_trace` (reload + autoStop). Extract **only** the metrics line (LCP, CLS, INP) + insight *names*; ignore the call-tree / format appendix entirely. **Dev vs prod — this matters, state it next to the numbers:** a local `npm run dev` server is NOT representative of production (dev compiles routes on demand, no minify/cache/CDN), so timing is inflated (heavy page ⇒ LCP 7 s+, TTFB 6 s+).
   - **Don't trust on dev:** LCP, TTFB, absolute load time — these are dev artifacts.
   - **Do trust on dev (structural, not timing):** **CLS** (layout shift is code-driven), ForcedReflow, RenderBlocking, DOM size, and the *relative* ranking of tabs. Lighthouse a11y/best-practices/SEO are static analysis — fully valid on dev.
   - **For real timing numbers**, point the audit at a production build or a deployed env: `origin=<prod-or-staging-url>` (or run `npm run build && npm start` locally and audit that). Say so in the report instead of presenting dev LCP as truth.
5. **Lighthouse** — only if `+a11y` / `full`. `lighthouse_audit` `device: desktop`, `mode: navigation`, and set `outputDirPath` to a **stable** folder (`<scratchpad>/lh`) — not a random temp dir. Keep the emitted `report.html` **only if a category scores < 90** (else it's clutter); note its path. Record a11y / best-practices / SEO from the text output.
6. **Hold the tab's compact findings in memory** (they're already small — a few deduped lines). Do **not** write a file; the report is rendered inline in Step 5. The only thing that must never enter context is a *raw* MCP dump (full stacks, the 312-row network list, the perf call-tree appendix) — the hook already prevents that. For an unusually large `all` sweep where even compact findings would be big, you may stream them to a temp file via `evaluate_script filePath` and summarize; otherwise keep them inline.

## Step 3 — L2 drilldowns (skip if `no-drilldown`)

For every tab that has a `drilldown` recipe in `routes.md`:

1. Land on the tab (reuse Step 2's navigation).
2. `take_snapshot` to get fresh `uid`s (uids change every snapshot — never reuse an old one).
3. Follow the recipe's steps (`click` a row/button by its accessible name, `fill` + submit a prompt, switch an inner tab). Between steps, `wait_for` the expected result text. **Do not reload** — that would wipe the interacted state (and re-arm the hook); the `initScript` hook from Step 2.1 is still live and keeps appending to `window.__audit`.
4. After each interaction, re-run the Step 2.3 `evaluate_script`. Because the hook accumulates, compare against the pre-interaction finding count and keep only the **new** entries — attribute them to `"{tab} › {interaction}"`. (This is exactly how today's `Chip`→`TrailingSlot` nested-`<button>` bug surfaced: absent at load, appeared only after the filter click.)

Drilldowns are where most real bugs hide (this app's tooltip-ref, tab-value, and useEffect-deps warnings all fired on interaction, not page load). Keep recipes resilient: match elements by **accessible name/text**, not brittle positions; if a target isn't in the snapshot, log `drilldown-skipped: <reason>` and continue — never hang.

## Step 4 — Noise filtering

Keep a finding only if it is **app-originated**. Drop:

- Next.js dev-overlay / HMR chatter, `[Fast Refresh]`, `[HMR]`.
- `Refused to execute script ... MIME type ('application/json')` — a dev-server artifact, not app code.
- Browser-extension frames, `chrome-extension://`, source-less warnings.
- Third-party SDK logs with no `src/` frame in the component/stack trace.

**Keep** anything whose component stack or source references `src/` (e.g. `Tooltip.tsx`, `MessageItem.jsx`) — those are ours. **Dedupe** by `(message text, first src/ frame)` and record an occurrence count instead of repeating. When you drop a whole class, say how many were dropped — never silently hide.

## Step 5 — Report (inline — no file)

**Render the whole report directly in the response.** Do **not** create a `.md` file — the user wants everything visible right here, nothing to open. (If they want to file a ticket from it, they'll run `/create-issue` themselves.) Because the hook already compacts findings, the inline report stays small even for a sweep. Structure:

1. **Summary table** — one row per tab: `Tab | ✗ errors | ⚠ warns | net fails | LCP | CLS | a11y | best-prac | SEO`. Only include columns for collectors that actually ran (`—` otherwise). Sort worst-first (errors, then warns).
2. **Findings detail** — group by **root cause across tabs** (a shared-component bug appears once, with the tab list), not per-tab-repeated. Each finding: message · first `src/` frame as a clickable `path:line` · occurrence count · load-time vs `› interaction` · **Suggested fix** (from the recipe table below).
3. **Network flags** — failed (≥400) and slow (>1.5 s) calls; also note chattiness (e.g. "~300 `/api/graphql` on load — check for N+1"). Backend-relevant.
4. **Performance flags** — tabs breaching LCP > 2.5 s / CLS > 0.1, with the top insight and the dev-inflation caveat on LCP/TTFB.
5. **Next actions** — a short, ranked fix list. Do **not** edit code in this skill; auditing and fixing are separate. If the user says "fix finding N", *that* goes through the normal teach-while-fixing + validate flow.

**Suggested-fix recipe table** (map each finding class to its known fix — these recur in this app):

| finding pattern | suggested fix |
|-----------------|---------------|
| `children supplied to Tooltip` / `Function components cannot be given refs` | wrap the child in `<Box component='span'>`, or make it `forwardRef` if it owns a DOM node |
| `Invalid prop \`alt\` of type number` / null alt on `SafeIcon` | coerce to a string: `alt={x || 'icon'}` |
| `<div>`/`<p>` cannot appear as a descendant of `<p>` | set `component='div'` on the wrapping `Typography` |
| `<a>` cannot be a descendant of `<a>` | render the inner nav as `<button>`/`<span>`, not a nested `Link`/`<a>` |
| `<button>` cannot be a descendant of `<button>` | the inner slot must not be a `<button>` (e.g. `Chip` `TrailingSlot` → non-button element) |
| `prop \`X\` is invalid; it must be a function` | fix the PropTypes value (`PropTypes.bool`, not bare `PropTypes`) |
| `[Chip] tag chips are read-only` | clickable chip → `variant='action'` (or `'filter'`), not `'tag'` |
| `useEffect ... changed size between renders` | make the dependency array a fixed-shape constant (union, stable order) |
| Next.js `Image ... width/height modified but not both` | add `style={{ height: 'auto' }}` (or width) to keep aspect ratio |

## Notes & gotchas

- **uids are per-snapshot.** Always `take_snapshot` immediately before a `click`/`fill`; never reuse a uid from an earlier snapshot.
- **One page at a time.** The tools act on the *currently selected* page. If you open extra pages, `select_page` before collecting.
- **Order within a tab: console/network (Step 2.3) BEFORE perf/lighthouse.** Both the perf trace and Lighthouse *reload* the page, which re-runs the `initScript` and resets `window.__audit`. So always read `window.__audit` first, then run the expensive reload-based collectors.
- **Token budget is the real budget.** Thanks to the in-page hook + compact `evaluate_script`, each tab costs ~a few hundred bytes of context, so even a 25-tab sweep won't blow up. Never paste a raw `list_console_messages`/`list_network_requests`/perf-trace dump into the report — those are the token sinks the hook exists to avoid. Stream big intermediate data to files via `evaluate_script filePath`.
- **Time budget.** Default (console + network) ≈ 3–5 s/tab. `+perf` adds ~5–10 s/tab; `+a11y` (Lighthouse) adds **~45 s/tab** — the one genuinely expensive collector, so keep it opt-in and representative, never blanket-on for a big sweep.
- **Read-only by contract.** This skill never edits app code, never commits, and never navigates to destructive actions (delete/terminate buttons). Drilldown recipes must only read/expand, never mutate.
