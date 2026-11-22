import type { JWT } from 'next-auth/jwt';

// Who may connect a messaging platform (Discord, Microsoft Teams) to the session's
// workspace, for API routes that call notifications-server directly rather than
// through the gateway's action permissions.
export function canManageMessagingPlatforms(jwt: JWT): boolean {
  const roles = (jwt.roles as string[]) || [];
  const permissions = (jwt.permissions as string[]) || [];
  return roles.includes('tenant_admin') || jwt.isSuperAdmin === true || permissions.includes('messagingplatforms:Write');
}
