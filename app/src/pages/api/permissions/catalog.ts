import type { NextApiRequest, NextApiResponse } from 'next';
import { authenticateRequest } from '@lib/rpcGateway';
import { loadRpcRoutes } from '@lib/rpcRoutes';
import { buildCatalog, type CatalogEntry, type PermissionClass } from '@lib/permissionCatalog';
import { deriveSystemRoleGrants, DERIVED_SYSTEM_ROLE_KEYS, SYSTEM_ROLE_LABELS } from '@lib/systemRoleGrants';

// Returns the permission catalog the Roles & Permissions UI renders:
//   - `catalog`     — the module × {Read,Write,Execute} matrix derived from
//                     actions.yaml (the grant surface a custom role can grant).
//   - `systemRoles` — the built-in roles' grant sets, derived from each role's
//                     actions.yaml `permissions:` entries (same source as the
//                     V766 seed via systemRoleGrants.ts). Rendered READ-ONLY so
//                     admins see what each built-in role grants in the same
//                     vocabulary as custom roles.
// Both are computed from actions.yaml (same classifyAction the gateway uses), so
// there is no generated artifact to drift. Tenant-admin gated.

type SystemRoleView = { systemKey: string; label: string; permissions: Array<{ module: string; class: PermissionClass }> };
type CatalogResponse = { catalog: CatalogEntry[]; systemRoles: SystemRoleView[] };

let cache: CatalogResponse | null = null;

function getCatalog(): CatalogResponse {
  if (cache) return cache;
  const routes = loadRpcRoutes();
  const actionNames = Object.keys(routes);
  // Adapt routes → ActionDef[] (each route's allowedRoles are the action's
  // permitted built-in roles), then derive each system role's grant set.
  const actions = actionNames.map((name) => ({
    name,
    permissions: [...routes[name].allowedRoles].map((role) => ({ role })),
  }));
  const rows = deriveSystemRoleGrants(actions);
  const byKey = new Map<string, Array<{ module: string; class: PermissionClass }>>();
  for (const k of DERIVED_SYSTEM_ROLE_KEYS) byKey.set(k, []);
  for (const r of rows) byKey.get(r.systemKey)?.push({ module: r.module, class: r.class });
  const systemRoles: SystemRoleView[] = DERIVED_SYSTEM_ROLE_KEYS.map((k) => ({
    systemKey: k,
    label: SYSTEM_ROLE_LABELS[k] ?? k,
    permissions: byKey.get(k) ?? [],
  }));
  // Feed the yaml `comment:` through so each grant can be explained in the
  // editor; buildCatalog falls back to the action name where none exists.
  cache = { catalog: buildCatalog(actionNames.map((name) => ({ name, comment: routes[name].comment }))), systemRoles };
  return cache;
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'GET') {
    res.setHeader('Allow', 'GET');
    return res.status(405).json({ error: 'method_not_allowed' });
  }
  const auth = await authenticateRequest(req);
  if (!auth) {
    return res.status(401).json({ error: 'unauthenticated' });
  }
  const roles = (auth.jwt.roles as string[]) || [];
  const isTenantAdmin = roles.includes('tenant_admin') || roles.includes('tenant_admin_readonly');
  if (!auth.jwt.isSuperAdmin && !auth.jwt.isSuperAdminReadonly && !isTenantAdmin) {
    return res.status(403).json({ error: 'forbidden' });
  }
  return res.status(200).json(getCatalog());
}
