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

The app supports per-hostname white-labeling — logo, favicon, app title, AI assistant name, signin imagery/carousel, MUI palette, fonts, and the full `--nb-*` CSS variable token set. **Branding is configured in one place: the `tenant_branding_override` table**, one row per hostname. No rebuild, and no deploy once the image files are mounted.

**Enterprise only.** Everything that reads or writes the table — the frontend provider (`src/ee/branding/`), the `branding_*` actions (`src/ee/actions.yaml`), and services-server's `ee/branding` package with `GET /v1/branding/overrides` — is stripped from the OSS build. OSS renders the house brand everywhere, and its notifications-server keeps the bundled `theme.json` branding.

### How it works

1. Every host starts from the bundled house brand, [`public/branding/default/theme.json`](public/branding/default/theme.json).
2. The EE provider ([`serverInit.ts`](src/ee/branding/serverInit.ts)) merges the `tenant_branding_override` row for the **request's hostname** (`X-Forwarded-Host`) on top. The table is loaded into an in-process snapshot by [`overrideClient.ts`](src/ee/branding/overrideClient.ts) and refreshed every 30s, because SSR resolves branding synchronously.
3. [`/api/public/app_config`](src/pages/api/public/app_config.js) returns the result. On the client, [`useBrandingConfig`](src/hooks/useTenantBranding.js) fetches it once per page load and renders nothing brand-specific until it answers.
4. [`useThemeProvider`](src/hooks/useThemeProvider.ts) applies `theme` to MUI and writes `colorTokens` to `:root` as CSS variables. Critical above-the-fold tokens are inlined during SSR to prevent FOUC.

A host with no row renders the house brand.

notifications-server has no request host, so it brands every outbound notification from the row whose host matches its **`BASE_URL`** — see **Email and bot branding** below.

> The per-host theme file map (`TENANT_BRANDING_HOST_MAP`, chart `branding.hostMap`), the deployment-wide theme file (`TENANT_BRANDING_FILE`, chart `branding.defaultTenant`) and the inline env overrides (`TENANT_THEME_CONFIG`, `TENANT_COLOR_TOKENS`) were **removed**. The app ignores them, and the chart refuses to render with `hostMap` or `defaultTenant` set.

### Onboarding a new partner

**1. Mount the image files.** A row stores URLs, not bytes, so the partner's images still have to be served from `/branding/<partner>/`. Create a ConfigMap with one key per file (names must match `[-._a-zA-Z0-9]+`) and list it under the chart's `branding.tenants`:

```yaml
branding:
  enabled: true
  tenants:
    - slug: acme
      configMapName: acme-branding # mounted at /app/public/branding/acme/
```

**2. Save the row** for the partner's hostname (super-admin):

```jsonc
// branding_upsert_override { host: "cloud.acme.example.com", overrides: { ... } }
{
  "title": "Acme Cloud", // tab title, app name in copy
  "assistantName": "Ace",
  "logoUrl": "/branding/acme/logo.svg",
  "faviconUrl": "/branding/acme/favicon.svg",
  "nubiIconUrl": "/branding/acme/assistant.svg",
  "nubiIconLightUrl": "/branding/acme/assistant-light.svg",
  "loaderUrl": "/branding/acme/assistant.svg", // otherwise the loading screen falls back to a spinner
  "signinImageUrl": "/branding/acme/logo.svg",
  "theme": { "palette": { "primary": "#0047AB" }, "typography": { "fontFamily": "'Inter', sans-serif" } },
  "colorTokens": { "--nb-color-primary": "#0047AB", "--nb-bg-sidebar": "#001f4d" },
  "fontRemap": [{ "family": "Roboto", "src": "url(https://fonts.gstatic.com/...) format(woff2)" }]
}
```

It takes effect within ~30s. See [`app/src/styles/defaultTokens.ts`](src/styles/defaultTokens.ts) for the full token list.

### The row

**Allowed fields** mirror the house `theme.json`: copy (`title`, `assistantName`), every asset URL (`logoUrl`, `faviconUrl`, `loaderUrl`, `signinImageUrl`, `signinLeftImageUrl`, the nubi/bee illustration set, `connectClusterGifUrl`, `carouselSlides`), and styling (`theme`, `colorTokens`, `fontRemap`), plus an `email` block read only by notifications-server (see below). One field is refused, with its reason in the error:

- `isWhiteLabel` — derived, never stored. A host is white-label when its row sets `title` or `logoUrl` (a rename or re-logo is a different brand, so the Nudgebee mascots are suppressed). A colour-only row is a retint of the house brand and keeps them.

**Asset URLs must be same-origin root-relative paths** (`/branding/acme/logo.svg`, or `""` to clear back to the house default). Absolute and scheme-relative URLs are rejected: a stored row that could point at another host is brand hijack and a tracking pixel on every page.

**Legal links are the one exception.** `privacyUrl` and `termsUrl` take an absolute, ASCII-only `https://` URL to the partner's own policy pages (punycode for an internationalised domain), or `""` for none. When a host's row sets one, `/privacy.html` or `/terms_of_use.html` on that host redirects there (`src/pages/api/public/legal/[doc].ts`) and the email footer links to it. A host that sets neither serves the house pages as before; the OSS build ships no house pages (`.oss-exclude`), so there they answer 404. The house brand's own hosts set these too, so their emails carry the links — point them at an external page (the marketing site), never at the host's own `/privacy.html`, which would then redirect to itself.

**Merge semantics** ([`mergeOverride.ts`](src/ee/branding/mergeOverride.ts)): `title`/`assistantName` and asset URLs replace, `colorTokens` merge per key over the house tokens, `theme` merges one level deep (setting `palette` keeps `typography`), `fontRemap` and `carouselSlides` replace wholesale.

**Actions** — super-admin only, because rows are keyed by hostname and carry no tenant column (branding resolves before authentication, so there is no tenant scope to constrain a `tenant_admin`):

```
branding_list_overrides            list every row, including disabled
branding_get_override              { host }
branding_upsert_override           { host, overrides, enabled? }
branding_apply_previous_override   { host }   # one-step undo
branding_delete_override           { host }   # host falls back to the house brand
```

`branding_upsert_override` validates against the allow-list and rejects CSS-injection payloads in `colorTokens` / `fontRemap`. Each write keeps the outgoing value in `previous_overrides` for `branding_apply_previous_override`; audit rows do not cover this because `audit.LogChange` skips super-admin actors.

**Failure behaviour.** Once a snapshot has loaded, an unreachable services-server keeps the last good values — a failed refresh never repaints a partner. Before the first successful fetch there is nothing to keep, so a cold start behind an unreachable services-server renders the house brand until it recovers. Branding never consults a license feature: that coupling once repainted partners as Nudgebee on a transient failure (see [`docs/ee/licensing-design.md`](../docs/ee/licensing-design.md), migration V826).

**Email and bot branding.** notifications-server has no request host, so it applies the row for its own
**`BASE_URL`** hostname, refreshed every 60s ([`branding_overrides.py`](../notifications-server/notifications_server/configs/branding_overrides.py)).
The row's `title` names the product in every notification (email, Slack, Teams, Discord, Google Chat); an
optional `email` block restyles email:

```jsonc
"email": {
  "logoUrl": "/branding/acme/logo.png",   // root-relative; made absolute with BASE_URL
  "supportEmail": "support@acme.example.com",
  "fromName": "Acme Ops",                 // display name on the From line; the address stays EMAIL_FROM
  "primaryColor": "#0047AB", "headerBgColor": "#001f4d", "footerBgColor": "#10264c", "footerLinkColor": "#ffcc00",
  "address": "1 Acme Way, Anytown",
  "copyrightStartYear": 2024
}
```

Colours must be hex, `rgb()`/`rgba()` or a named colour, and `address` may not contain ` < > " ' \``
— these values are written straight into HTML email. One deployment sends one brand: a deployment
serving several partner hostnames under one  `BASE_URL` brands all of their notifications with that
host's row.

`fromName` is written into a mail header, so it takes visible characters and plain spaces only, with no
angle brackets or double quotes.

### Local development

```bash
# 1. Put image files where the dev server serves them
mkdir -p public/branding/acme && cp my-logo.svg public/branding/acme/logo.svg

# 2. Run services-server locally and point the app at it (SERVICE_API_SERVER_URL in .env.local)

# 3. Save a row for localhost (as a super admin), then reload within ~30s
#    branding_upsert_override { host: "localhost", overrides: { title: "Acme", logoUrl: "/branding/acme/logo.svg" } }
```

The row's host is matched after normalisation (lower-cased, port stripped), so `localhost:3000` and `localhost` are the same key.

### Verifying

- `curl http://localhost:3000/api/public/app_config | jq` — the resolved config the client will receive
- DevTools → Elements → `<html>` style — `--nb-*` CSS variables match `colorTokens`
- Signin page (`/signin`) — `logoUrl`, `signinImageUrl`, `carouselSlides`

### Pitfalls

- **Asset paths must start with `/branding/<partner>/`** and the file must be mounted, or the slot renders a broken image.
- **Overrides are per-replica and eventually consistent.** `app` runs 2 replicas, each refreshing on its own 30s clock, so an edit can take up to ~30s to appear everywhere.
- **MUI palette colors must be hex strings**, not CSS variable references — the MUI theme is built before CSS variables are applied.
- **A hostname with no row is the house brand.** Save the row before pointing a partner's domain at the deployment.
- **Notifications follow `BASE_URL`, not the partner's hostname.** A row for `calsoft.example.com` only reaches notifications from a deployment whose `BASE_URL` is that host.

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
