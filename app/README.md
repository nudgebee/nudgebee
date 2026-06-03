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
- **Path Aliases**: Use `@api1`, `@components1`, `@common`, `@lib`, `@hooks`, `@context`, `@data`, `@assets`, and `@utils` as defined in `tsconfig.json`.
- **Authentication & Authorization**: Protected routes use the `withAuth` HOC. Permission utilities (`hasReadAccess`, `isTenantAdmin`, etc.) are in `@lib/auth`.
- **Code Quality:** Linting via `next lint` and `oxlint`. Formatting with Prettier (`npm run prettier:check`).
- **Testing:** Unit tests with Jest and React Testing Library.
- **Type Safety:** Use strictly typed TypeScript.
- **Testing IDs**: Always include `id` or `data-testid` for automation testing on clickable elements.
- **Design System:** Reference [`design-system.md`](design-system.md) for components and typography.

---

## Partner Branding (White-Label)

The app supports per-deployment white-labeling — logo, favicon, app title, AI assistant name, signin imagery/carousel, MUI palette, and the full `--nb-*` CSS variable token set — driven by a single JSON file at runtime. No code changes or rebuilds are required to onboard a new partner.

### How it works

1. The runtime endpoint [`/api/public/app_config`](src/pages/api/public/app_config.js) reads the file pointed to by the `TENANT_BRANDING_FILE` env var (falling back to `branding/default/theme.json`).
2. On the client, [`useBrandingConfig`](src/hooks/useTenantBranding.js) fetches `/api/public/app_config` once per page load and caches it.
3. [`useThemeProvider`](src/hooks/useThemeProvider.ts) applies `theme` to MUI and writes `colorTokens` to `:root` as CSS variables. Critical above-the-fold tokens are inlined during SSR to prevent FOUC.
4. The notifications-server reads the **same** `TENANT_BRANDING_FILE` env to brand outbound emails — keep one file per partner, used by both services.

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

Set the same value on the notifications-server deployment so emails match the UI.

**Inline overrides** (skip editing the file — useful for hot-patching a single field): set `TENANT_THEME_CONFIG` or `TENANT_COLOR_TOKENS` to a JSON-encoded object. These take precedence over the file's `theme` / `colorTokens` blocks respectively. Invalid JSON is silently ignored.

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
- **The branding file is read once per process** ([`loadBrandingFile.js`](src/lib/loadBrandingFile.js)). Editing `theme.json` while the dev server runs requires a restart.
- **MUI palette colors must be hex strings**, not CSS variable references — the MUI theme is built at module load, before CSS variables are applied.
- **Keep email branding in sync** — `theme.email` is consumed by notifications-server. A partner with mismatched UI/email colors looks broken.
- **Don't commit partner secrets** to the public branding folder. For customer-specific deployments, ship `theme.json` via a Helm value / ConfigMap (see `nudgebee-infra/deploy/customers/<partner>/`).

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
├── component-new/ds/   # Design System v2 primitives — see app/design-system/
├── components1/        # Feature components organized by domain
│   └── common/         # Shared UI components
├── api1/               # GraphQL service modules (one folder per backend feature)
├── context/            # React Context providers
├── hooks/              # Custom React hooks
├── lib/                # Utilities, HTTP service, auth, RPC gateway
├── utils/              # Color, API, and other shared utilities
├── data/               # Constants, theme tokens
└── styles/             # Global CSS + design tokens
```

> **For path-alias mappings, the design system, GraphQL contract patterns, and detailed component conventions**, see [`app/CLAUDE.md`](CLAUDE.md) — it's the single source of truth for those topics. The "For human contributors" callout at the top of root [`CLAUDE.md`](../CLAUDE.md) lists which sections are worth reading even if you're not driving an AI agent.
