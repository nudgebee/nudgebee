# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

```bash
# Install dependencies (--legacy-peer-deps required due to peer conflicts)
npm install --legacy-peer-deps

# Development server (port 3000, webpack)
npm run dev

# Development server using Turbopack (faster, but see caveat below)
npm run dev:turbo

# Production build
npm run build

# Linting (oxlint + prettier - used in CI)
npm run lint2

# Auto-fix formatting and lint issues
npm run lint2:fix

# Type checking
npm run type-check

# Run tests
npm test

# Bundle size analysis
npm run analyze
```

> **Turbopack dev caveat.** `npm run dev` deliberately uses webpack. On Next 16.2.12, Turbopack's dev server intermittently fails to match requests that arrive during the cold-boot compile window against pages-router **dynamic** routes, so `/api/auth/*` (the `[...nextauth]` catch-all) resolves to the HTML `/404` page. That surfaces as `[next-auth][error][CLIENT_FETCH_ERROR] Unexpected token '<', "<!DOCTYPE "...` and makes sign-in impossible. It latches for the process lifetime — restarting doesn't reliably clear it; only a filesystem change under `src/pages/` does. It's most likely when the app boots while services-server is down and a browser is already sitting on `/signin`. Use `npm run dev:turbo` for speed when you aren't touching auth. See [`docs/architecture-decisions.md`](../docs/architecture-decisions.md).

## Architecture Overview

### Framework Stack

- **Next.js 16** with Turbopack, standalone output mode for containerization
- **React 18** with hooks-based architecture
- **TypeScript** with strict mode
- **Material-UI (MUI v5)** as primary component library
- **Emotion** for CSS-in-JS styling

### Directory Structure

```
src/
├── pages/          # Next.js pages and API routes
├── api1/           # GraphQL service modules (feature-organized)
├── components/     # All React components
│   ├── common/     # Shared UI components (@shared/*)
│   │   └── ds/     # Design-system primitives (@ui/*)
│   └── */          # Feature components (k8s, cloudaccount, llm, …)
├── context/        # React Context providers
├── hooks/          # Custom React hooks
├── lib/            # Utilities, HTTP service, auth
├── utils/          # Color, API, and common utilities
├── data/           # Constants and theme configuration
└── styles/         # Global CSS
```

### Path Aliases (tsconfig.json)

Use these instead of relative imports:

- `@api1/*` → `src/api1/*`
- `@components/*` → `src/components/*`
- `@ui/*` → `src/components/common/ds/*` (design-system primitives)
- `@shared/*` → `src/components/common/*` (shared UI components)
- `@lib/*` → `src/lib/*`
- `@hooks/*` → `src/hooks/*`
- `@context/*` → `src/context/*`
- `@data/*` → `src/data/*`
- `@assets/*` → `src/assets/images/*`
- `@utils/*` → `src/utils/*`

## Key Patterns

### GraphQL API Layer

All backend communication is GraphQL-shaped — the frontend issues GraphQL operations, which `@lib/rpcGateway` parses and dispatches to upstream action handlers (mounted under `/rpc/*` on each backend service). API modules follow this pattern:

```typescript
// src/api1/{feature}/index.ts

import { queryGraphQL } from '@lib/HttpService';

// 1. Define GraphQL query/mutation string
export const GET_ITEMS = `
query GetItems($id: uuid!) {
  items(where: {id: {_eq: $id}}) {
    id
    name
  }
}`;

// 2. Define TypeScript interfaces
interface GetItemsRequest {
  id: string;
}

// 3. Export async service function
export async function getItems({ id }: GetItemsRequest) {
  const response = await queryGraphQL(GET_ITEMS, 'GetItems', { id });
  return response?.data?.data?.items;
}
```

The `queryGraphQL` function (in `@lib/HttpService`) handles:

- Server vs client endpoint routing — on the client it POSTs to `/api/graphql`; in server-side code (NextAuth callbacks, API routes) it invokes the in-process gateway (`bypassGraphQLAsServer`) directly to avoid a self-call loop
- W3C traceparent headers for distributed tracing
- Auto-redirect to signin on 401/invalid-jwt errors

### Authentication & Authorization

Protected routes use the `withAuth` HOC:

```typescript
import { withAuth } from '@lib/auth';

function MyProtectedPage() {
  // Component code
}

export default withAuth(MyProtectedPage);
```

Permission utilities in `@lib/auth`:

- `hasReadAccess(accountId, namespace)` - Check read permission
- `hasWriteAccess(accountId, namespace)` - Check write permission
- `isTenantAdmin()` - Check admin role
- `hasFeatureAccess(featureName)` - Check feature flag
- `getAllowedNamespaces(accountId)` - Get namespace ACL (null = all access)

### State Management

**Global state via React Context:**

1. `DataContext` (`@context/DataContext`) - Cluster selection, pod logs

   ```typescript
   import { useData } from '@context/DataContext';
   const { selectedCluster, setSelectedCluster, allCluster } = useData();
   ```

2. `GlobalFilterContext` (`@lib/contexts`) - Date range filtering
   ```typescript
   import { useGlobalFilter } from '@lib/contexts';
   const { startDate, endDate, setStartDate, setEndDate } = useGlobalFilter();
   ```

**Session data** is accessed via NextAuth:

```typescript
import { useSession } from 'next-auth/react';
const { data: session } = useSession();
// session contains: roles, tenant, accountIds, accountPermissions
```

### Component Styling

Always use colors from app/src/utils/colors.ts
Add colors if needed.

Primary approach is MUI's `sx` prop:

```typescript
<Box sx={{ p: 2, display: 'flex', gap: 1 }}>
```

Additional styling options: SASS modules, Tailwind utilities, Emotion styled components.

### Testing IDs for UI Components

When generating UI components with clickable elements or navigation buttons, always include `id` or `data-testid` attributes for automation testing. Use descriptive, kebab-case IDs that match the element's function.

```tsx
<Button data-testid="submit-form-btn">Submit</Button>
<Link href="/dashboard" id="nav-dashboard-link">Dashboard</Link>
<Link href="/settings" id="nav-settings-link">Settings</Link>
```

These IDs are a contract with the Playwright suite in `app-e2e-tests/`, not decoration — see [Keeping app-e2e-tests in sync](#keeping-app-e2e-tests-in-sync-required) before renaming or removing one.

## Environment Variables

Required for local development (create `.env.local`):

```bash
RELAY_SERVER_ENDPOINT=http://localhost:52832
NEXTAUTH_SECRET=your-nextauth-secret
```

## Testing

Jest with React Testing Library. Test files in `__tests__/` directory.

```bash
# Run all tests
npm test

# Run specific test file
npm test -- KubernetesLogs.test.jsx
```

## Formatting

Always run Prettier after editing files to avoid CI failures. CI uses prettier v2 (from package-lock.json), so use the project's local version:

```bash
cd app
npx prettier@2.8.8 --write <file-path>   # Format with CI-matching version
npm run lint2:fix                          # Auto-fix all lint + format issues
```

## CI/CD Validation

Before pushing, ensure these pass (matches CI checks):

```bash
npm run lint2           # oxlint + prettier check
npm run type-check      # TypeScript compilation
npm run build           # Production build succeeds
```

## Design System

For the complete design system reference (typography classes, component catalog with props/variants/examples, format components, charts, and utilities), see [`design-system.md`](design-system.md).

### Keeping the spec in sync (REQUIRED)

The DS has two parallel artifacts that **must stay in sync**:

1. **Code** — `app/src/components/common/ds/<Name>.tsx` (the runtime primitive)
2. **Spec** — `app/design-system/primitives/<category>/<name>.html` + visual styles in `app/design-system/shared/primitive-helpers.css`

Each `ds/<Name>.tsx` declares its spec path in the top JSDoc — e.g. `* Spec: app/design-system/primitives/action/chip.html`. **Read that line first.**

Whenever you change any of the following in a `ds/*.tsx` file, update the matching spec HTML _in the same commit_:

- Public props or types (`Props` interface, `Size`/`Tone`/`Variant`/`Shape` unions)
- Size/tone/variant tokens (`SIZE_TOKENS`, `TONE_PALETTE`, etc.)
- Interaction states (`:hover`, `:active`, `:focus-visible`, `aria-pressed`)
- Validation rules / dev warnings
- "Don't" rules in the file docstring

If the visual styles changed (new size, new state, new tone), also update `primitive-helpers.css` so the spec page renders the new state. Bump the `<meta name="ds-last-changed-on">` date in the HTML.

Refactors with no public-API or visual change don't require a spec update — but mention it explicitly in the PR body so reviewers don't have to guess.

## Keeping Global Search in sync (REQUIRED, HIGH PRIORITY)

Every navigable tab/sub-tab must be reachable from the header's global search (`GlobalPageSearch.jsx`). Its data lives in [`src/lib/navSearchPages.ts`](src/lib/navSearchPages.ts):

- Top-level hash-routed tabs (`/troubleshoot`, `/optimise`, `/tickets`, `/user-management`) → `navSearchPages`
- Detail-page tabs needing a runtime `accountId` scoped to one cloud provider (K8s/AWS/Azure/GCP details) → the matching `*SearchFragments` array (`k8sDetailsSearchFragments`, `awsDetailsSearchFragments`, `azureDetailsSearchFragments`, `gcpDetailsSearchFragments`)
- Detail-page tabs needing a runtime `accountId` but NOT tied to one cloud provider (`/automation`, `/agentHealth`) → `accountScopedSearchFragments` (each entry names its own `basePath`/`group`)

Whenever a PR adds, renames, or removes a `fragment` in any page's `filterOptions`/`tabOptions` (or the equivalent `baseOptions`/`awsOptions`/`azureOptions`/`gcpOptions` in the detail pages), the same change MUST land in `navSearchPages.ts` in the same PR — either a new/updated entry, or an explicit new entry (with a reason) in the exported `navSearchIgnoredFragments` list at the bottom of that file if it's intentionally excluded (e.g. a `disabled: true` tab).

**This is a HIGH severity finding, not a nitpick** — a tab missing from global search is a silent discoverability regression that won't surface in tests or manual QA of the tab itself. Any AI review pass (Gemini Code Assist, `/create-pr` self-review, `/review-pr`) MUST flag a diff that adds/changes a tab `fragment` without a matching `navSearchPages.ts` change in the same PR, and should treat it as blocking rather than optional cleanup.

The sidebar's hover flyout (`menuItems[].subItems` in [`src/components/common/layout/index.jsx`](src/components/common/layout/index.jsx)) is the second hand-maintained copy of the same tab list — it carries only the **top-level** tabs of each page. Adding, renaming, or removing a top-level `fragment` means updating it too. Sub-tabs never appear there, so a sub-tab-only change doesn't.

## Keeping app-e2e-tests in sync (REQUIRED)

**Rule: no change under `app/` is done until the e2e suite has been reconciled against it, in the same PR.**

`app-e2e-tests/` is a separate Playwright project that drives this app through a contract that lives in `app/` source — element ids, testids, visible text, accessible names, placeholders, routes, and the conditions under which any of those render. Nothing in `app/`'s validation can see that contract: `lint2`, `type-check` and `npm test` all pass while the suite is broken. It fails later, on a run nobody traces back to the PR that caused it. So reconciling is a step you perform, not a thing you notice.

The rule is three passes, in order. The first is mechanical, the second is judgement, the third is evidence.

### Pass 1 — mechanical sweep (always, no exceptions)

```bash
cd app-e2e-tests && node scripts/e2e-impact.js [base-ref]   # default base: enterprise/main
```

It reads every static selector the suite requires, reads what each changed `app/` file provided before and after your diff, and reports the intersection: strings the tests need that your diff deleted or reworded, each with the app file that dropped it and the spec line that needs it. It reads the **working tree**, so run it while the change is still uncommitted — you do not have to commit first to find out. It also prints which spec areas cover the files you touched. Exit 1 means at least one selector is broken.

Comparing against the pre-diff version is what keeps it quiet — a selector the app composes at runtime (`toKebabCase(field.display_name)`) is never a literal in either version, so it is never flagged. It reports its own blind spots too: the count of dynamic locators (regex / template-literal) it could not check statically.

### Pass 2 — judgement pass (what no script can see)

A selector that still exists in source can still be unreachable. Static text matching cannot catch any of these, so read your own diff and ask:

- **Did an element become conditional?** `{options.length > 8 && <Search/>}` removes no string and breaks the suite completely. This is the most common miss, and the one that looks safest in review.
- **Did a default change?** Page size, initial tab, default filter, collapsed-vs-expanded, sort order. Tests that use `.first()` / `.nth(n)` bind to order, and a row that moved to page 2 is a row that no longer exists.
- **Did something become async, slower, or lazier?** Deferred loading, a new skeleton, a request behind a queue — every one of them invalidates a wait that used to be sufficient.
- **Did the accessible name or role change?** Swapping a `<Button>` for a `<Link>`, or moving text into an `aria-label`, changes `getByRole` without changing any visible copy.
- **Did a form gain a required field, a confirm step, or a new modal?** A flow test that filled 4 fields and clicked Save now stops on validation.
- **Did a route, hash fragment or redirect change?** `goto` and `waitForURL` bind to those.

If your diff does any of these, find the e2e path for that element **by behaviour, not by name** — grep the spec area the script printed and read the helper.

### Pass 3 — evidence

Run the spec areas the script named, against dev:

```bash
cd app-e2e-tests && npm run test:dev -- tests/<area>
```

Put the result in the PR body. If you concluded a change has no e2e impact, prove it with the command that shows it (`node scripts/e2e-impact.js` output, or the grep that returns nothing) rather than leaving reviewers to take it on faith.

### Fixing what you find

Selectors are centralised per area in `app-e2e-tests/tests/**/[Ll]ocators*.ts` (`GlobalLocators.ts`, `CloudAccountLocators.ts`, `ClusterDetailsLocators.ts`, `OptimizeLocators.ts`, `TroubleshootLocators.ts`, `admin/*/…Locators.ts`, `nubiLocators.ts`, `workflowlocators.ts`) — a hit is usually one line in one of those files, not scattered through the specs. Shared flow helpers live beside them (e.g. `tests/admin/Integrations/util.ts`).

Two rules for the fix itself:

- **Handle both shapes, not the one your data produces.** When behaviour became conditional, the helper must cope with the condition being either way — probe for the element, branch, and take the other path when it is absent. A helper that assumes the branch your dev environment happens to hit is the same bug with a new owner.
- **Prefer making the app stable over making the test clever.** Keeping an `id` and changing only the label costs nothing; the suite leans on ids far more than on text. **Adding** an `id`/`data-testid` is always safe — **renaming or deleting one is a breaking change to another repo.**

## Key Libraries Reference

| Library                    | Usage                                  |
| -------------------------- | -------------------------------------- |
| ReactFlow                  | Workflow/DAG editor components         |
| Chart.js / react-chartjs-2 | Charts and visualizations              |
| CodeMirror                 | Code editing (YAML, JSON, SQL, PromQL) |
| XTerm.js                   | Terminal emulator for K8s pod exec     |
| react-hook-form + yup      | Form handling and validation           |
| dayjs / date-fns           | Date manipulation                      |
| lodash                     | Utility functions                      |

<!-- BEGIN:nextjs-agent-rules -->

# This is NOT the Next.js you know

This version has breaking changes — APIs, conventions, and file structure may all differ from your training data. Read the relevant guide in `node_modules/next/dist/docs/` (resolved from this file's directory; in monorepos the `next` package may not be visible from the repo root) before writing any code. Heed deprecation notices.

This block is written and re-added by `next dev` — verify at `node_modules/next/dist/server/lib/generate-agent-files.js`. Removing it from a diff only re-creates the uncommitted change; committing it with your work keeps the tree clean.

<!-- END:nextjs-agent-rules -->
