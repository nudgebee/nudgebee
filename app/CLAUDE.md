# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Development Commands

```bash
# Install dependencies (--legacy-peer-deps required due to peer conflicts)
npm install --legacy-peer-deps

# Development server (port 3000 with Turbopack)
npm run dev

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
├── components1/    # React components by feature
│   └── common/     # Shared UI components
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
- `@components1/*` → `src/components1/*`
- `@common/*` → `src/components1/common/*`
- `@lib/*` → `src/lib/*`
- `@hooks/*` → `src/hooks/*`
- `@context/*` → `src/context/*`
- `@data/*` → `src/data/*`
- `@assets/*` → `src/assets/images/*`
- `@utils/*` → `src/utils/*`

## Key Patterns

### GraphQL API Layer

All backend communication uses GraphQL via Hasura. API modules follow this pattern:

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

- Server vs client endpoint routing (`/api/graphql` on client, direct Hasura on server)
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

## Environment Variables

Required for local development (create `.env.local`):

```bash
GRAPHQL_API_ENDPOINT=http://localhost:8080/v1/graphql
HASURA_GRAPHQL_ADMIN_SECRET=your-secret
RELAY_SERVER_ENDPOINT=http://localhost:52832
NEXTAUTH_SECRET=your-nextauth-secret
NEXTAUTH_PRIVATE_KEY=your-pem-key
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
