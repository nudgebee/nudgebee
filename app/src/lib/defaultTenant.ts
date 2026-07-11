// Default-tenant selection for login.
//
// A user can belong to several tenants (tenant_users rows) but hold a role in
// only some of them. If their `is_default` tenant is one where they have no
// role, the session resolves to that tenant with `roles: []` and the app loads
// with zero access — the user has to manually switch tenants on every login.
// (Observed: a user who is tenant_admin in tenant B via a group, but whose
// default is tenant A where they have nothing. See issue #32594.)
//
// These helpers pick a tenant where the user actually has access, falling back
// to today's behavior when they have no role anywhere — so the change is
// strictly additive. The `user` shape matches `parseUserRow` in UserService.ts
// (which is what reaches `adapterUser`): `user_roles[]` carry `tenant_id`, and
// `groups[].user_group.group_roles[]` carry `entity_type` / `entity_id`.

export type TenantRole = { entity_type?: string; entity_id?: string; tenant_id?: string; role?: string };
export type TenantRef = { id: string; name?: string; is_default?: boolean };
export type UserForTenantPick = {
  tenants?: TenantRef[];
  user_roles?: TenantRole[];
  groups?: Array<{ user_group?: { group_roles?: TenantRole[] } }>;
};

// True when the user holds at least one effective role scoped to `tenantId`:
// a direct role (every `user_roles` row carries a `tenant_id`) or a
// group-derived TENANT role (`entity_type === 'tenant'`, `entity_id` is the
// tenant).
//
// Group account/k8s roles are intentionally NOT resolved here: their
// `entity_id` is an account id, and mapping it to a tenant would need an async
// per-tenant account lookup. Missing that narrow case only means we don't
// *auto-prefer* such a tenant — it never flips a tenant that has access to "no
// access", so it cannot regress the current default-tenant choice.
export function userHasRoleInTenant(user: UserForTenantPick, tenantId: string): boolean {
  if ((user.user_roles ?? []).some((r) => r.tenant_id === tenantId)) {
    return true;
  }
  for (const group of user.groups ?? []) {
    const groupRoles = group.user_group?.group_roles ?? [];
    if (groupRoles.some((gr) => gr.entity_type === 'tenant' && gr.entity_id === tenantId)) {
      return true;
    }
  }
  return false;
}

// Choose the tenant the session should open in. Preference order:
//   1. the `is_default` tenant — but only if the user has a role there, so an
//      explicit default is respected whenever it's usable;
//   2. otherwise the first tenant where the user has a role;
//   3. otherwise the `is_default` tenant, else the first tenant — preserves the
//      previous behavior for users who genuinely have no role anywhere.
// Returns undefined only when the user belongs to no tenant.
export function pickDefaultTenant(user: UserForTenantPick): TenantRef | undefined {
  const tenants = user.tenants ?? [];
  if (tenants.length === 0) {
    return undefined;
  }
  const marked = tenants.find((t) => t.is_default);
  if (marked && userHasRoleInTenant(user, marked.id)) {
    return marked;
  }
  const firstWithRole = tenants.find((t) => userHasRoleInTenant(user, t.id));
  return firstWithRole ?? marked ?? tenants[0];
}
