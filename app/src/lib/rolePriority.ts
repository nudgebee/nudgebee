// Shared role-priority ordering. Used by both Hasura JWT signing
// (internal.ts) and the RPC-bypass session-variable construction
// (rpcGateway.ts) so a single user always resolves to the same
// default Hasura role regardless of which path their request takes.
//
// Order is "most-privileged first" — pickDefaultRole walks this list
// and returns the first match in the user's role set.

export const ROLE_PRIORITY: readonly string[] = [
  'super_admin',
  'super_admin_readonly',
  'tenant_admin',
  'tenant_admin_readonly',
  'account_admin',
  'account_admin_readonly',
  'k8s_namespace_admin',
  'k8s_namespace_admin_readonly',
];

/**
 * Returns the highest-priority role from the user's role set, falling
 * back to the supplied `fallback` if none match. Callers choose their
 * own fallback (e.g. internal.ts defaults to 'tenant_usage' for unmatched
 * minimal sessions; rpcGateway defaults to userRoles[0] || 'tenant_admin_readonly').
 */
export function pickDefaultRole(userRoles: readonly string[], fallback: string): string {
  return ROLE_PRIORITY.find((r) => userRoles.includes(r)) ?? fallback;
}
