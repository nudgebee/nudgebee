# Getting Started with Next JS

This project was bootstrapped with [Create Next App](https://nextjs.org/). Major libraries the project uses [Material UI](https://mui.com/material-ui/getting-started/), Typescript, [GraphQL](https://graphql.org/), [next-auth](https://next-auth.js.org/getting-started/introduction) and [vis-network](https://github.com/visjs/vis-network).

## Prerequisites

Before you begin, make sure you have the following installed:

- Node.js: Next.js requires Node.js to run. You can download (version 21.x) it from [here](https://nodejs.org/).
- npm (Node Package Manager): npm comes with Node.js installation.

## Setup the app project:

1. Navigate to the app folder from the terminal.
2. Execute `npm install --legacy-peer-deps`
3. Add required environment variables in `.env` files.
4. Run `npm run dev`

## Available Scripts

In the project directory, you can run:

### `npm run dev`

Runs the app in the development mode.\
Open [http://localhost:3000](http://localhost:3000) to view it in the browser.

The page will reload if you make edits.\
You will also see any lint errors in the console.

### `npm test`

Launches the test runner in the interactive watch mode.\

### `npm run build --legacy-peer-deps --only=production`

Builds the app for production to the `build` folder.\
It correctly bundles React in production mode and optimizes the build for the best performance.

The build is minified and the filenames include the hashes.\
Your app is ready to be deployed!

---

## Development Context

### Project Overview

The App is a frontend application built with **Next.js** (v16) and **React** (v18.2). It uses **Turbopack** for the development server and **standalone output mode** for production container builds.

### Key Technologies

- **Styling:** Material UI (@mui/material), Emotion, SASS, Tailwind CSS.
- **State Management & Data Fetching:** React Hook Form, Axios, SWR.
- **Charts & Visuals:** Chart.js, D3, React Flow, XTerm.js.
- **Editors:** CodeMirror (JS, JSON, Markdown, YAML support).
- **Auth:** NextAuth.js.

### Development Conventions

- **GraphQL API Layer**: All backend communication is GraphQL-shaped, parsed and dispatched in-app by `@lib/rpcGateway` to upstream service handlers. Modules follow the `queryGraphQL` pattern in `HttpService` with TypeScript interfaces for requests.
- **Path Aliases**: Use `@api1`, `@components`, `@ui` (DS primitives), `@shared` (shared UI), `@lib`, `@hooks`, `@context`, `@data`, `@assets`, and `@utils` as defined in `tsconfig.json`.
- **Authentication & Authorization**: Protected routes use the `withAuth` HOC. Permission utilities (`hasReadAccess`, `isTenantAdmin`, etc.) are in `@lib/auth`.
- **Code Quality:** Linting via `next lint` and `oxlint`. Formatting with Prettier (`npm run prettier:check`).
- **Testing:** Unit tests with Jest and React Testing Library.
- **Type Safety:** Use strictly typed TypeScript.
- **Testing IDs**: Always include `id` or `data-testid` for automation testing on clickable elements.
- **Design System:** Reference [`design-system.md`](design-system.md) for components and typography.

---

## Partner Branding (White-Label)

The app supports per-deployment white-labeling — logo, favicon, app title, AI assistant name, signin imagery/carousel, MUI palette, and the full `--nb-*` CSS variable token set — driven by a single JSON file at runtime. No code changes or rebuilds are required to onboard a new partner.

**Enterprise only.** Everything that reads or writes the table — the frontend provider (`src/ee/branding/`), the `branding_*` actions (`src/ee/actions.yaml`), and services-server's `ee/branding` package with `GET /v1/branding/overrides` — is stripped from the OSS build. OSS renders the house brand everywhere, and its notifications-server keeps the bundled `theme.json` branding.

### How it works

1. The runtime endpoint [`/api/public/app_config`](src/pages/api/public/app_config.js) resolves branding for the **request's hostname** via [`resolveServerBranding`](src/lib/serverBranding.ts). The EE provider ([`serverInit.ts`](src/ee/branding/serverInit.ts)) picks the theme file: `TENANT_BRANDING_HOST_MAP` (JSON `{host: slug}`) → `branding/<slug>/theme.json`, else `TENANT_BRANDING_FILE`, else `branding/default/theme.json`.
2. A per-host **runtime override** from the `tenant_branding_override` table is merged on top of that file — see [Runtime overrides](#runtime-overrides-no-deploy) below.
3. On the client, [`useBrandingConfig`](src/hooks/useTenantBranding.js) fetches `/api/public/app_config` once per page load and caches it.
4. [`useThemeProvider`](src/hooks/useThemeProvider.ts) applies `theme` to MUI and writes `colorTokens` to `:root` as CSS variables. Critical above-the-fold tokens are inlined during SSR to prevent FOUC.
5. The notifications-server brands every outbound notification from the `tenant_branding_override` row whose host matches its **`BASE_URL`** (`title`, plus the row's `email` block for email), over its bundled theme. It no longer reads `TENANT_BRANDING_FILE`.

### Onboarding a new partner

**1. Drop static assets** into `public/branding/<partner>/`:

```
app/public/branding/<partner>/
├── logo.svg              # required — header / signin logo
├── favicon.ico
├── nubi-icon.svg         # assistant avatar (dark variant)
├── nubi-icon-light.svg   # assistant avatar (light variant)
├── helpbee-icon.svg      # optional
├── troubleshoot-bee.svg  # optional, empty-state illustrations
├── optimize-bee.svg      # optional
├── k8s-bee.svg           # optional
├── new-user-bee.svg      # optional
├── signin-left.svg       # optional, signin page left panel
└── carousel/             # optional, partner-supplied signin carousel slides
```

Assets are served at `/branding/<partner>/<filename>` by Next.js's static handler — no import or registration step. See `public/branding/default/` for the bundled defaults.

**2. Write `theme.json`** for the partner. The full schema:

```jsonc
{
  // --- Identity ---
  "title": "Acme Cloud", // tab title, app name in welcome strings
  "assistantName": "Ace", // AI assistant display name (default "NuBi")
  "logoUrl": "/branding/acme/logo.svg", // header + signin logo
  "faviconUrl": "/branding/acme/favicon.ico",

  // --- Assistant / empty-state illustrations (each optional; falls back to /branding/default/) ---
  "nubiIconUrl": "/branding/acme/nubi-icon.svg",
  "nubiIconLightUrl": "/branding/acme/nubi-icon-light.svg",
  "helpbeeIconUrl": "/branding/acme/helpbee-icon.svg",
  "troubleshootBeeUrl": "/branding/acme/troubleshoot-bee.svg",
  "optimizeBeeUrl": "/branding/acme/optimize-bee.svg",
  "k8sBeeUrl": "/branding/acme/k8s-bee.svg",
  "newUserBeeUrl": "/branding/acme/new-user-bee.svg",

  // --- Signin page ---
  "signinImageUrl": "", // optional hero image
  "signinLeftImageUrl": "/branding/acme/signin-left.svg", // optional left-panel illustration
  "loaderUrl": "", // optional GIF/SVG for the loading screen
  "carouselSlides": [
    // optional — overrides bundled carousel
    { "title": "Cut cloud spend 40%", "image": "/branding/acme/carousel/slide1.svg" },
    { "title": "Auto-remediate incidents", "image": "/branding/acme/carousel/slide2.svg" }
  ],

  // --- MUI theme (used by createDynamicTheme) ---
  "theme": {
    "palette": { "primary": "#0047AB", "success": "#00a800", "error": "#e60013" },
    "typography": { "fontFamily": "'Inter', sans-serif" },
    "components": { "borderRadius": 8 }
  },

  // --- CSS variable overrides (all keys are optional; missing keys fall back to defaults) ---
  // See app/src/styles/defaultTokens.ts for the full token list and
  // notifications-server/.../branding/default/theme.json for a complete worked example.
  "colorTokens": {
    "--nb-color-primary": "#0047AB",
    "--nb-bg-sidebar": "#001f4d",
    "--nb-btn-primary": "#0047AB",
    "--nb-btn-primary-hover": "#003380",
    "--nb-mui-primary": "#0047AB"
    // … any --nb-* token the partner wants to override
  },

  // --- Email branding (consumed by notifications-server) ---
  "email": {
    "logoUrl": "/branding/acme/logo.png",
    "supportEmail": "support@acme.com",
    "primaryColor": "#0047AB",
    "headerBgColor": "#001f4d",
    "footerBgColor": "#10264c",
    "footerLinkColor": "#ffcc00",
    "address": "1 Acme Way, Anytown",
    "copyrightStartYear": 2024
  }
}
```

A complete working example is committed at [`notifications-server/notifications_server/branding/default/theme.json`](../notifications-server/notifications_server/branding/default/theme.json) — clone it and edit.

**3. Point the deployment at the file** via env vars. The path is resolved relative to `app/public/` unless absolute:

```bash
# Relative path under app/public — typical when bundling the partner folder into the image
TENANT_BRANDING_FILE=branding/acme/theme.json

# Absolute path — typical when mounting a partner overlay ConfigMap/Secret
TENANT_BRANDING_FILE=/etc/nudgebee/theme.json
```

**Inline overrides** (skip editing the file — useful for hot-patching a single field): set `TENANT_THEME_CONFIG` or `TENANT_COLOR_TOKENS` to a JSON-encoded object. These take precedence over the file's `theme` / `colorTokens` blocks respectively. Invalid JSON is silently ignored.

### Runtime overrides (no deploy)

Everything above ships a partner's brand as **files in a mounted ConfigMap**, which means onboarding
and every subsequent tweak costs an infra PR plus a deploy (and prod deploys are `workflow_dispatch`
only). The `tenant_branding_override` table removes the deploy from the part that actually churns.

The split is by **churn rate**, not storage type:

|                                                               | Lives in                       | Changed by                           |
| ------------------------------------------------------------- | ------------------------------ | ------------------------------------ |
| Asset kit (SVGs, favicon) + base `theme.json`                 | ConfigMap mount                | infra PR + deploy (once per partner) |
| `title`, `assistantName`, `theme`, `colorTokens`, `fontRemap` | `tenant_branding_override` row | RPC action, takes effect in ≤30s     |

**The override is strictly additive.** No row, a row with `enabled=false`, or an unreachable
services-server all leave the mounted kit's branding exactly as it was — the file is always the
floor. This is deliberate: branding used to consult `hasLicenseFeature('branding.custom')`, so a
transient services-server failure at boot silently repainted a partner's UI as Nudgebee until the
next restart (see [`docs/ee/licensing-design.md`](../docs/ee/licensing-design.md) and migration V826).
Nothing in this layer may reintroduce that.

**The allow-list tracks `theme.json`.** Anything a mounted kit can express, an override can express
too — copy (`title`, `assistantName`), every asset URL (`logoUrl`, `faviconUrl`, `loaderUrl`, the
signin imagery, the nubi/bee illustration set, `carouselSlides`), and styling (`theme`,
`colorTokens`, `fontRemap`). A subset would force operators to remember which half of the brand
lives where.

Two fields are refused, each with its own reason in the error:

- `isWhiteLabel` — derived, never stored. A host is white-label when its kit is a partner kit **or**
  its row sets `title` or `logoUrl` (a rename or re-logo is a different brand, so the Nudgebee
  mascots are suppressed). A colour-only row stays a retint. A row that could set the flag directly
  would desynchronise mascot suppression from the actual brand.

**Asset URLs must be same-origin root-relative paths** (`/branding/acme/logo.svg`, or `""` to clear
back to the bundled default). Absolute and scheme-relative URLs are rejected: a stored row that could
point at another host is brand hijack and a tracking pixel on every page, neither of which shows up in
a diff. The _files_ still come from the mount — what an override buys is re-pointing a slot at a
different file the kit already ships, with no deploy. A path that resolves to nothing is just a broken
image: visible immediately, and undone with `branding_apply_previous_override`.

Merge semantics ([`mergeOverride.ts`](src/ee/branding/mergeOverride.ts)): `title`/`assistantName` and
asset URLs replace, `colorTokens` merge per key, `theme` merges one level deep (so setting `palette`
keeps `typography`), `fontRemap` and `carouselSlides` replace wholesale.

Managing overrides — super-admin only, because rows are keyed by hostname and carry no tenant column
(branding resolves before authentication, so there is no tenant scope to constrain a `tenant_admin`):

```
branding_list_overrides            list every row, including disabled
branding_get_override              { host }
branding_upsert_override           { host, overrides, enabled? }
branding_apply_previous_override   { host }   # one-step undo
branding_delete_override           { host }   # host falls back to its kit
```

`branding_upsert_override` validates against the allow-list and rejects CSS-injection payloads in
`colorTokens` / `fontRemap`. Each write stores the outgoing value in `previous_overrides`, which is
what `branding_apply_previous_override` restores — moving a brand out of git costs the `git revert`
the ConfigMap kit gets for free, and audit rows do not close that gap because `audit.LogChange`
skips super-admin actors.

**Email and bot branding.** notifications-server has no request host, so it applies the row for its own
**`BASE_URL`** hostname, refreshed every 60s ([`branding_overrides.py`](../notifications-server/notifications_server/configs/branding_overrides.py)).
The row's `title` names the product in every notification (email, Slack, Teams, Discord, Google Chat); an
optional `email` block restyles email:

```jsonc
"email": {
  "logoUrl": "/branding/acme/logo.png",   // root-relative; made absolute with BASE_URL
  "supportEmail": "support@acme.example.com",
  "primaryColor": "#0047AB", "headerBgColor": "#001f4d", "footerBgColor": "#10264c", "footerLinkColor": "#ffcc00",
  "address": "1 Acme Way, Anytown",
  "copyrightStartYear": 2024
}
```

Colours must be hex, `rgb()`/`rgba()` or a named colour, and `address` may not contain ` < > " ' \``
— these values are written straight into HTML email. One deployment sends one brand: a deployment
serving several partner hostnames under one  `BASE_URL` brands all of their notifications with that
host's row.

### Local development

```bash
# 1. Create partner asset folder
mkdir -p public/branding/acme
cp my-logo.svg public/branding/acme/logo.svg

# 2. Write theme.json (see schema above)
$EDITOR public/branding/acme/theme.json

# 3. Point the dev server at it via .env.local
echo "TENANT_BRANDING_FILE=branding/acme/theme.json" >> .env.local

# 4. Restart the dev server — the branding file is read once per process and cached
npm run dev
```

### Verifying

- `curl http://localhost:3000/api/public/app_config | jq` — confirms the resolved config the client will receive
- Open DevTools → Elements → `<html>` style — verify `--nb-*` CSS variables match `colorTokens`
- Signin page (`/signin`) — confirms `logoUrl`, `signinLeftImageUrl`, and `carouselSlides`

### Pitfalls

- **Asset paths must start with `/branding/<partner>/`** — assets outside `public/branding/` won't pick up the default-fallback behavior in `SafeIcon`.
- **The branding file is cached per path after a successful read** ([`loadBrandingFile.ts`](src/ee/branding/loadBrandingFile.ts)). Editing `theme.json` while the dev server runs requires a restart. Failed reads are not cached for an operator-configured path, so a transient miss self-heals.
- **Runtime overrides are per-replica and eventually consistent.** `app` runs 2 replicas, each refreshing on its own 30s clock, so an edit can take up to ~30s to appear everywhere. That is why overrides are limited to presentation fields — the divergence is a colour, never a missing asset.
- **MUI palette colors must be hex strings**, not CSS variable references — the MUI theme is built at module load, before CSS variables are applied.
- **Notifications follow `BASE_URL`, not the partner's hostname.** A row for `calsoft.example.com` only reaches notifications from a deployment whose `BASE_URL` is that host.
- **Don't commit partner secrets** to the public branding folder. For customer-specific deployments, ship `theme.json` via a Helm value or a ConfigMap mounted at `TENANT_BRANDING_FILE`.

---

## API Surface

The Next.js server (`src/pages/api/*`) exposes seven canonical surfaces. All client→backend traffic goes through one of these.

| Surface               | Authentication           | Purpose                                                                                                                                                                      |
| --------------------- | ------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `/api/public/*`       | None                     | Unauthenticated reads: app config, branding, health probe, mock data. Safe to call from outside the cluster.                                                                 |
| `/api/auth/*`         | NextAuth-managed         | NextAuth session, signin, SAML, magic-link, dummy-creds, callbacks                                                                                                           |
| `/api/graphql`        | Session cookie           | In-process GraphQL gateway — `@lib/rpcGateway` parses the GraphQL op and forwards to the right `/rpc/*` upstream handler                                                     |
| `/api/rpc`            | Service-to-service token | Direct RPC dispatcher (mirrors `/api/graphql` for non-GraphQL clients)                                                                                                       |
| `/api/proxy/*`        | Session cookie           | Proxies into upstream services that need a streaming/WebSocket transport (e.g. K8s pod-exec via the relay)                                                                   |
| `/api/integrations/*` | Mixed                    | OAuth install + callback flows for Slack, MS Teams, Google Chat, GitHub, AWS/Azure marketplace, etc. The Next.js handler forwards to notifications-server / services-server. |
| `/api/webhooks/*`     | Provider-signed          | Inbound webhooks from external providers (Slack events, MS Teams bot, GitHub, etc.). Signature is verified before forwarding.                                                |

A few legacy paths (`/api/slack/install`, `/api/marketplace/*`) are kept as **deprecated shims** that 301-style forward to the canonical `/api/integrations/*` URL with a `Deprecation: true` response header and a server log line. Provider dashboards can migrate at their own pace; shims will be removed once their traffic drops to zero (see [`@lib/deprecatedRouteShim`](src/lib/deprecatedRouteShim.ts)).

### Public endpoints quick reference

| Path                         | Purpose                                                        |
| ---------------------------- | -------------------------------------------------------------- |
| `GET /api/public/health`     | Liveness / readiness probe used by the K8s deployment manifest |
| `GET /api/public/app_config` | Resolved branding + feature-flag config (no auth required)     |
| `GET /api/public/mock/*`     | Static JSON fixtures used by Playwright tests + Storybook      |

## Component Organization

```
src/
├── pages/              # Next.js pages and API routes
├── components/        # Feature components organized by domain
│   ├── common/         # Shared UI components (@shared/*)
│   │   └── ds/         # Design System v2 primitives (@ui/*)
├── api1/               # GraphQL service modules (one folder per backend feature)
├── context/            # React Context providers
├── hooks/              # Custom React hooks
├── lib/                # Utilities, HTTP service, auth, RPC gateway
├── utils/              # Color, API, and other shared utilities
├── data/               # Constants, theme tokens
└── styles/             # Global CSS + design tokens
```

> **For path-alias mappings, the design system, GraphQL contract patterns, and detailed component conventions**, see [`app/CLAUDE.md`](CLAUDE.md) — it's the single source of truth for those topics. The "For human contributors" callout at the top of root [`CLAUDE.md`](../CLAUDE.md) lists which sections are worth reading even if you're not driving an AI agent.
