import React, { useEffect } from 'react';
import ErrorBoundary from '@shared/ErrorBoundary';
import AllUsers from '@components/user-management/AllUsers';
import UserGroup from '@components/user-management/UserGroup';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import { AuditsTable } from '@components/audits';
import { Box } from '@mui/material';
import Notifications from '@components/notifications';
import Integrations from '@components/accounts/integration';
import OwnershipRules from '@components/user-management/OwnershipRules';
import { AuditIcon, NotificationIcon1, User1, UserGroupIcon, IntegrationsIcon } from '@assets';
import { useSession } from 'next-auth/react';
import { useRouter } from 'next/router';
import { userManagementFilters } from '@lib/authHooks';
import { hasAdminSurfaceAccess, missingPermissionMessage } from '@lib/auth';
import Loader from '@shared/Loader';

// Base filters that ship in OSS. Extensions register additional filters via
// registerUserManagementFilter — those slot in at the end (e.g. billing on
// saas-tier deployments).
// `module` is the dynamic-RBAC permission module backing each section (see
// app/src/lib/permissionCatalog.ts). It drives the disabled-tab gating below:
// a custom-role user without Read on that module sees the tab greyed-out (not
// hidden) so the capability is discoverable and they can request access.
const baseFilters = [
  { name: 'Users', fragment: 'users', icon: User1, Body: AllUsers, module: 'users' },
  { name: 'Groups', fragment: 'groups', icon: UserGroupIcon, Body: UserGroup, module: 'usergroups' },
  // Roles & Permissions (dynamic RBAC) is EE-only: registered via
  // registerUserManagementFilter from app/src/ee (stripped in OSS). It slots in
  // after the base filters. OSS ships without it and uses the built-in roles.
  { name: 'Audits', fragment: 'audits', icon: AuditIcon, Body: AuditsTable, module: 'audits' },
  { name: 'Notifications', fragment: 'notifications', icon: NotificationIcon1, Body: Notifications, module: 'notifications' },
  { name: 'Integrations', fragment: 'integrations', icon: IntegrationsIcon, Body: Integrations, module: 'integrations' },
  { name: 'Ownership', fragment: 'ownership', icon: UserGroupIcon, Body: OwnershipRules, module: 'ownership' },
];

export default function UserManagement() {
  const router = useRouter();
  const sessionData = useSession({ required: true });
  const session = sessionData?.data;

  // Combine base filters with any registered extensions filtered by session,
  // then stamp positional values so AnchorComponent's routing keeps working.
  const filterOptions = React.useMemo(() => {
    const roles = session?.roles ?? [];
    const isAdmin = roles.includes('tenant_admin') || roles.includes('tenant_admin_readonly') || !!session?.isSuperAdmin;
    // Dynamic-RBAC grants ("<module>:<class>") the signed-in user holds.
    const perms = session?.permissions ?? [];
    const all = [...baseFilters, ...userManagementFilters(session)].filter((f) => !f.adminOnly || isAdmin);
    return all.map((f, i) => {
      // Tenant admins keep full access. A custom-role user gets a section only
      // if they hold Read on its module; the rest render disabled (visible but
      // not clickable). Filters without a module (extensions) are unaffected.
      const lacksPermission = !isAdmin && !!f.module && !perms.includes(`${f.module}:Read`);
      return {
        ...f,
        value: i,
        disabled: f.disabled ?? lacksPermission,
        // Only a permission block gets the request-access hint (not some other
        // future f.disabled reason).
        disabledTooltip: lacksPermission ? missingPermissionMessage(`${f.module}:Read`) : undefined,
      };
    });
  }, [session]);

  const [selectedFilter, setSelectedFilter] = React.useState(null);

  useEffect(() => {
    if (!filterOptions.length) return;
    const fragment = router.asPath.split('#')[1];
    // The tab AnchorComponent highlights: the URL hash's section, else the
    // first tab (its no-hash default). If that section is one the user can
    // open, honor it — body and highlight already agree.
    const current = filterOptions.find((opt) => opt.fragment == fragment);
    const anchorTab = current ?? filterOptions[0];
    if (anchorTab && !anchorTab.disabled) {
      setSelectedFilter(anchorTab.value);
      return;
    }
    // Otherwise the target section is disabled (e.g. an Audit-Read-only user
    // landing on the default Users tab). Steer to the first section they can
    // open and sync the hash, so AnchorComponent's highlight follows the body
    // instead of sitting on the disabled default.
    const firstEnabled = filterOptions.find((opt) => !opt.disabled);
    if (!firstEnabled) {
      setSelectedFilter(0);
      return;
    }
    setSelectedFilter(firstEnabled.value);
    if (firstEnabled.fragment && fragment !== firstEnabled.fragment) {
      const [pathWithQuery] = router.asPath.split('#');
      // Swallow the "Cancel rendering route" rejection Next.js emits when this
      // hash-only replace is superseded (e.g. dev strict-mode double-invoke) —
      // it's benign, the latest navigation still lands.
      router.replace(`${pathWithQuery}#${firstEnabled.fragment}`, undefined, { shallow: true }).catch(() => {});
    }
  }, [filterOptions, router.asPath]);

  // Same gate the sidebar's own "Admin" nav item uses (layout/index.jsx) — a
  // user who can't see that nav entry shouldn't be able to reach any of its
  // tabs (Users, Groups, Audits, Notifications, Integrations, Ownership) via
  // a typed/bookmarked URL either. hasAdminSurfaceAccess() (not hasReadAccess())
  // is what the sidebar uses, so a custom-role holder with a grant on an
  // Admin-page module (e.g. audits:Read) can open the page — the per-section
  // disabled-tab gating above then hides sections they lack Read on. Depends on
  // `session` itself (not just `sessionData.status`) so a mid-session role
  // change — status stays 'authenticated' throughout a refetch — still
  // re-evaluates access.
  useEffect(() => {
    if (session && !hasAdminSurfaceAccess()) {
      router.replace('/home');
    }
  }, [session, router]);

  const selectedOption = filterOptions[selectedFilter];
  const SelectedBody = selectedOption?.Body;

  if (!session || !hasAdminSurfaceAccess()) {
    return <Loader />;
  }

  return (
    <>
      <AnchorComponent
        manageRoute={true}
        options={selectedOption?.options || []}
        filterOptions={filterOptions}
        onChangeFilter={(val) => {
          setSelectedFilter(val);
        }}
      />
      <ErrorBoundary key={selectedFilter}>
        {/* Guard against the brief mount tick where AnchorComponent reports its
            disabled default (0) before the hash-steer above lands — never render
            a section the user can't open. */}
        <Box mt={2}>{SelectedBody && !selectedOption?.disabled && <SelectedBody session={session} />}</Box>
      </ErrorBoundary>
    </>
  );
}
