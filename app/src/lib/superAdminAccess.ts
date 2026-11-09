// Platform super admin is the Nudgebee-staff role that opens any customer's
// workspace and skips the gateway's role gates. It is off unless the
// deployment sets SUPER_ADMIN_ENABLED=true. Read per call, so changing the
// pod's env takes effect on the next request without a rebuild.
//
// Off means no human session carries super admin: the sign-in lookup is skipped,
// a session minted as super admin is refused, and a `super_admin` role row is
// dropped from the session's roles. The server's own synthetic admin
// (SERVER_ADMIN_JWT in rpcGateway) never passes through here and is unaffected.
//
// No imports: sessionCookie.ts uses this and must not pull in the app.

const SUPER_ADMIN_ROLES = new Set(['super_admin', 'super_admin_readonly']);

export function isSuperAdminAccessEnabled(): boolean {
  return process.env.SUPER_ADMIN_ENABLED === 'true';
}

function isSuperAdminRole(role: unknown): boolean {
  // Case-insensitive, like the citext role column.
  return typeof role === 'string' && SUPER_ADMIN_ROLES.has(role.toLowerCase());
}

// What a decoded session may be used as. With super admin off:
//   - a session minted as super admin is refused (null), not cleaned. One that
//     switched into a workspace it is not a member of also holds that workspace's
//     read-only admin role, so clearing the flag alone would leave the access.
//   - any other session loses super-admin role strings from `roles`.
export function restrictSuperAdmin<T extends Record<string, unknown>>(token: T): T | null {
  if (isSuperAdminAccessEnabled()) return token;
  if (token.isSuperAdmin || token.isSuperAdminReadonly) return null;
  const roles = token.roles;
  if (Array.isArray(roles) && roles.some(isSuperAdminRole)) {
    return { ...token, roles: roles.filter((r) => !isSuperAdminRole(r)) };
  }
  return token;
}
