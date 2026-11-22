// Super admin is platform-wide. Its only grant is a user_roles row with
// tenant_id IS NULL, which reaches a session as jwt.isSuperAdmin /
// jwt.isSuperAdminReadonly (set at sign-in through @lib/authHooks), never as an entry in the
// session's roles. Those roles are read from the user's rows in one tenant, so a
// super-admin role among them came from a tenant-scoped row and grants nothing.
// Every producer of allowed_roles strips
// them (rpcGateway.buildSessionVariables, internal.encodeSessionJWT).
//
// Keep the list in step with security.IsSuperAdminRole in the api-server and the
// CHECK constraints added by migration V1107.
export const SUPER_ADMIN_ROLES: readonly string[] = ['super_admin', 'super_admin_readonly'];

// Case-insensitive, like the citext role column. Anything that is not an array
// (a malformed token) yields no roles: fail closed rather than throw, and never
// treat a string's characters or substrings as roles.
export function stripSuperAdminRoles(roles: string[]): string[] {
  if (!Array.isArray(roles)) {
    return [];
  }
  return roles.filter((role) => !SUPER_ADMIN_ROLES.includes(String(role).toLowerCase()));
}
