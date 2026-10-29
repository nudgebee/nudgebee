import React, { useEffect } from 'react';
import ErrorBoundary from '@shared/ErrorBoundary';
import AdminOverview from '@components/user-management/AdminOverview';
import AllUsers from '@components/user-management/AllUsers';
import UserGroup from '@components/user-management/UserGroup';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import PermissionDeniedContent from '@components/common/navigation/PermissionDeniedContent';
import { AuditsTable } from '@components/audits';
import { Box, Typography } from '@mui/material';
import Notifications from '@components/notifications';
import DeliveryHistory from '@components/notifications/delivery-history/DeliveryHistory';
import Integrations from '@components/accounts/integration';
import OwnershipRules from '@components/user-management/OwnershipRules';
import TenantSettings from '@shared/settings/TenantSettings';
import { AuditIcon, NotificationIcon1, User1, UserGroupIcon, IntegrationsIcon, SettingsIcon, AgentIcon, SummaryIconBlue } from '@assets';
import { useSession } from 'next-auth/react';
import { useRouter } from 'next/router';
import { userManagementFilters } from '@lib/authHooks';
import { hasAdminSurfaceAccess, missingPermissionMessage, isUiFeatureEnabled } from '@lib/auth';
import Loader from '@shared/Loader';
import { ds } from '@utils/colors';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import AssignmentIndOutlinedIcon from '@mui/icons-material/AssignmentIndOutlined';
import HistoryIcon from '@mui/icons-material/History';
import { AI_TOOLS_SUB_TABS, subTabAccess } from '@components/llm/admin/aiToolsConfig';
import { useBCortexEnabled, isOSSDeploymentMode } from '@hooks/useBCortexEnabled';
import withBrandBlue from '@shared/icons/withBrandBlue';

// AssignmentIndOutlinedIcon defaults to `currentColor` and would otherwise
// render gray/black instead of matching its blue-SVG siblings (Users,
// Groups, Audit Log) in the same Access & Users dropdown.
const OwnershipIcon = withBrandBlue(AssignmentIndOutlinedIcon);
const DeliveryHistoryIcon = withBrandBlue(HistoryIcon);

// Base filters that ship in OSS. Extensions register additional filters via
// registerUserManagementFilter — those slot in at the end (e.g. billing on
// saas-tier deployments).
// `module` is the dynamic-RBAC permission module backing each section (see
// app/src/lib/permissionCatalog.ts). It drives the disabled-tab gating below:
// a custom-role user without Read on that module sees the tab greyed-out (not
// hidden) so the capability is discoverable and they can request access.
// `description` is the one-line "what is this tab for" caption rendered under
// the tab strip. Extension-registered filters carry their own (see
// UserManagementFilter in @lib/authHooks).
// A function taking the brand, not a constant: one caption names the product, which
// is tenant-branded and only resolved once /api/public/app_config has landed.
// A filter can carry `tabOptions` instead of its own `Body`/`module` — Access
// & Users below consolidates Users, Groups, Ownership and Audit Log this way,
// as sibling sub-tabs reached at `#access-users/<fragment>` (AnchorComponent's
// built-in 2-level hash routing; the default sub-tab, Users, keeps the bare
// `#access-users` URL working unchanged). Each sub-tab is gated on its own
// `module`, independently of its siblings — see the parent/child disabled
// computation in filterOptions below.
// Roles & Permissions (dynamic RBAC) merges into Access & Users' tabOptions
// the same way, but it isn't a baseFilters entry to merge statically — it's
// EE-only, registered at runtime via registerUserManagementFilter (stripped
// in OSS), gated by its own shouldShow(session) rather than a `module`. See
// the rolesFilter handling in filterOptions below, which folds it in (right
// after Groups) only when userManagementFilters(session) actually returns it.
const baseFilters = (baseTitle) => [
  {
    name: 'Overview',
    fragment: 'overview',
    icon: SummaryIconBlue,
    iconSize: 16,
    Body: AdminOverview,
    description: 'A snapshot of everything set up for this tenant, with a quick link into each admin area.',
  },
  {
    name: 'Access & Users',
    // Explicit id: AnchorComponent falls back to `anchor-tab-${name}` when no id
    // is given, and "Access & Users" contains a space and an ampersand — neither
    // safe in a bare CSS id selector (see the identical reasoning pinning
    // Notification Rules' id below).
    id: 'AccessUsers',
    fragment: 'access-users',
    icon: User1,
    tabOptions: [
      {
        id: 'users',
        fragment: '',
        text: 'Users',
        icon: User1,
        Body: AllUsers,
        module: 'users',
        description:
          'Everyone who can sign in to this tenant — invite users, set their role and status, and review the groups and integration profiles each one belongs to.',
      },
      {
        id: 'groups',
        fragment: 'groups',
        text: 'Groups',
        icon: UserGroupIcon,
        Body: UserGroup,
        module: 'usergroups',
        description:
          'Bundle users into groups and assign roles at the group level — scoped to an account or namespace — instead of granting access user by user.',
      },
      {
        id: 'ownership',
        fragment: 'ownership',
        text: 'Ownership',
        icon: OwnershipIcon,
        Body: OwnershipRules,
        module: 'ownership',
        description:
          'Rules that map a namespace, workload label or cloud resource to an owning user or group, so resources are attributed to a team automatically.',
      },
      {
        id: 'audit-log',
        fragment: 'audit-log',
        text: 'Audit Log',
        icon: AuditIcon,
        Body: AuditsTable,
        module: 'audits',
        description:
          'The searchable trail of configuration, access and automation changes in this tenant — who did what, when, and what the change altered.',
      },
    ],
  },
  {
    name: 'Integrations',
    fragment: 'integrations',
    icon: IntegrationsIcon,
    Body: Integrations,
    module: 'integrations',
    description: `Connect ${baseTitle} to your clouds, observability platforms, ticketing, repositories and messaging tools — and see what is already connected.`,
  },
  // id is pinned so the DOM id (#anchor-tab-Notifications) that app-e2e-tests
  // locates the tab by stays stable if the label ever changes independently.
  // Rules stays the default sub-tab, so `#notification-rules` and the Overview
  // card's alert-* actions land on it unchanged. Delivery history is read like
  // alerts, so it is gated on events rather than notifications.
  {
    name: 'Notification Rules',
    id: 'Notifications',
    fragment: 'notification-rules',
    icon: NotificationIcon1,
    tabOptions: [
      {
        id: 'notification-rules-list',
        fragment: '',
        text: 'Rules',
        icon: NotificationIcon1,
        Body: Notifications,
        module: 'notifications',
        description:
          'Rules decide which troubleshooting, SLO, cloud spend, optimization and automation notifications are delivered, and where. Every matching rule applies; if any says Don’t send, it isn’t sent.',
      },
      {
        id: 'delivery-history',
        fragment: 'delivery-history',
        text: 'Delivery history',
        icon: DeliveryHistoryIcon,
        Body: DeliveryHistory,
        module: 'events',
        description: baseTitle
          ? `Every notification ${baseTitle} sent or tried to send in the last 7 days: where it went, which rule sent it, and whether it arrived.`
          : 'Every notification sent or tried to send in the last 7 days: where it went, which rule sent it, and whether it arrived.',
      },
    ],
  },
  // AI & Tools (docs/ia-consolidation-plan.md, PR 3) — "what the AI is made
  // of", relocated out of the Settings modal into Admin. Same tabOptions
  // pattern Access & Users uses above: flat siblings, each independently
  // module-gated, hash-routed at `#ai-tools/<fragment>`. The sub-tab list
  // itself lives in AI_TOOLS_SUB_TABS (components/llm/admin/aiToolsConfig.js)
  // — shared with the Nubi rail's AIToolsModal per the "mirrors" rule (one
  // component, two mounts), rather than duplicated here.
  //
  // Scope note: this wires the UI-level gate only. actions.yaml's own
  // `permissions:` role lists for these actions are untouched — narrowing
  // them to Admin's usual tenant_admin-only pattern is a distinct, larger
  // change (removing account_admin/k8s_namespace_admin API access, not just
  // a menu entry) held pending explicit confirmation (Decision U).
  {
    name: 'AI & Tools',
    id: 'AITools',
    fragment: 'ai-tools',
    icon: AgentIcon,
    iconSize: 16,
    tabOptions: AI_TOOLS_SUB_TABS,
  },
  {
    name: 'Tenant Settings',
    fragment: 'tenant-settings',
    icon: SettingsIcon,
    // SettingsIcon's own SVG renders larger than its sibling top-level icons at
    // the default size — iconSize normalizes it, same fix troubleshoot/index.jsx
    // and optimise/index.jsx apply to their own top-level tab icons.
    iconSize: 16,
    Body: TenantSettings,
    module: 'tenants',
    description: 'Tenant identity, self-onboarding, label mapping for logs and webhook alerts, and feature flags for this tenant.',
  },
];

export default function UserManagement() {
  const { title: baseTitle } = useBrandingConfig();
  const router = useRouter();
  const sessionData = useSession({ required: true });
  const session = sessionData?.data;
  // Gates AI & Tools' legacyOnly sub-tabs (Memory, Account Context) — see
  // aiToolsConfig.js. `true` keeps the lookup always-active, same as
  // AIToolsModal's own call.
  const bcortexEnabled = useBCortexEnabled(true);

  // Combine base filters with any registered extensions filtered by session,
  // then stamp positional values so AnchorComponent's routing keeps working.
  const filterOptions = React.useMemo(() => {
    const roles = session?.roles ?? [];
    const isAdmin = roles.includes('tenant_admin') || roles.includes('tenant_admin_readonly') || !!session?.isSuperAdmin;
    // Dynamic-RBAC grants ("<module>:<class>") the signed-in user holds.
    const perms = session?.permissions ?? [];
    // userManagementFilters(session) already applies each entry's own
    // shouldShow(session) — an entry is simply absent when it fails (OSS build,
    // or e.g. custom roles disabled on the deployment), not present-but-disabled. Roles
    // gets pulled out and folded into Access & Users' tabOptions (right after
    // Groups) instead of staying a top-level tab; any OTHER registered filter
    // (none today — Billing's registration exists but isn't imported) is
    // unaffected and still renders as its own top-level tab, same as before.
    const registeredFilters = userManagementFilters(session);
    const rolesFilter = registeredFilters.find((f) => f.fragment === 'roles');
    const otherRegisteredFilters = registeredFilters.filter((f) => f.fragment !== 'roles');
    const filtersWithMergedGroups = baseFilters(baseTitle).map((f) => {
      if (f.fragment !== 'access-users' || !rolesFilter) return f;
      const rolesOption = {
        id: 'roles',
        fragment: 'roles',
        text: rolesFilter.name,
        icon: rolesFilter.icon,
        Body: rolesFilter.Body,
        description: rolesFilter.description,
        // No `module`: Roles' own shouldShow already decided whether it exists
        // here at all (see rolesFilter above) — same "hidden, not disabled"
        // semantics it had as a top-level tab, preserved by simply never
        // marking it disabled below (sub.module is undefined, so the
        // lacksPermission check for it is always false).
      };
      const groupsIdx = f.tabOptions.findIndex((opt) => opt.id === 'groups');
      const tabOptions = [...f.tabOptions];
      tabOptions.splice(groupsIdx + 1, 0, rolesOption);
      return { ...f, tabOptions };
    });
    const all = [...filtersWithMergedGroups, ...otherRegisteredFilters].filter((f) => !f.adminOnly || isAdmin);
    return all.map((f, i) => {
      if (f.tabOptions) {
        // A merged tab (Groups + Ownership): each sub-tab is gated on its own
        // module independently, same rule as a top-level tab below. The parent
        // tab is only disabled if every one of its sub-tabs is — otherwise a
        // custom-role user holding a grant on just one of the merged modules
        // (e.g. ownership:Read without usergroups:Read) would lose access to
        // both instead of just the one they actually lack.
        //
        // legacyOnly sub-tabs (AI & Tools' Memory / Account Context) are
        // dropped entirely — not just disabled — unless this tenant lacks
        // b-Cortex (aiToolsConfig.js). `bcortexEnabled !== false` (i.e. true
        // or still resolving) treats them as hidden by default so a tenant
        // that DOES have b-Cortex never sees a flash of the extra tabs while
        // the flag lookup is in flight.
        //
        // requiresUiFeature (Gateway) is the extra gate Settings applied on
        // top of its own module check — dropped, not just disabled, matching
        // Settings never pushing the tab at all when it failed.
        const visibleTabOptions = f.tabOptions.filter((sub) => {
          if (sub.eeOnly && isOSSDeploymentMode()) {
            return false;
          }
          if (sub.legacyOnly && bcortexEnabled !== false) {
            return false;
          }
          if (sub.requiresUiFeature && !isUiFeatureEnabled(sub.requiresUiFeature)) {
            return false;
          }
          return true;
        });
        const tabOptions = visibleTabOptions.map((sub, subIdx) => {
          const access = subTabAccess(sub, { isAdmin, permissions: perms, hasBuiltInRole: roles.length > 0 });
          return {
            ...sub,
            value: subIdx,
            disabled: sub.disabled ?? access.disabled,
            disabledTooltip: access.tooltip,
          };
        });
        return {
          ...f,
          value: i,
          tabOptions,
          disabled: f.disabled ?? tabOptions.every((sub) => sub.disabled),
          disabledTooltip: undefined,
        };
      }
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
  }, [session, baseTitle, bcortexEnabled]);

  const [selectedFilter, setSelectedFilter] = React.useState(null);
  // Which sub-tab is active within the selected top-level filter, when it has
  // tabOptions (only Access & Users today). Meaningless — and ignored — otherwise.
  const [selectedSubFilter, setSelectedSubFilter] = React.useState(0);

  // Given a resolved top-level tab, picks its active sub-tab from the URL's
  // child fragment (the part after `/`, e.g. `ownership` in `#groups/ownership`),
  // falling back to the first enabled sub-tab if the requested one is missing,
  // disabled, or absent (bare `#groups` lands on sub-tab 0, "Groups" itself).
  const resolveSubFilter = (tab, subFragment) => {
    if (!tab?.tabOptions) return 0;
    // A sub-tab named directly in the URL wins even when disabled -- its
    // content area renders a permission-denied placeholder (see
    // isSelectedDisabled below) rather than silently swapping to another
    // sub-tab. Only a missing/unmatched fragment falls back to the first
    // enabled sub-tab.
    const requested = subFragment ? tab.tabOptions.find((opt) => opt.fragment === subFragment) : null;
    if (requested) return requested.value;
    const firstEnabledSub = tab.tabOptions.find((opt) => !opt.disabled);
    return firstEnabledSub ? firstEnabledSub.value : 0;
  };

  useEffect(() => {
    if (!filterOptions.length) return;
    const hashFragment = router.asPath.split('#')[1];
    // AnchorComponent's own 2-level hash shape is `parentFragment/childFragment`
    // (see its getInitialState) — split the same way here so a deep link into a
    // merged tab's sub-tab (e.g. `#groups/ownership`) still matches its parent
    // by fragment, instead of failing to match `groups/ownership` against `groups`.
    const [fragment, subFragment] = (hashFragment || '').split('/');
    // A tab named directly in the hash wins even when disabled -- it still
    // highlights and settles here, and its content area renders a
    // permission-denied placeholder (isSelectedDisabled, see the render below)
    // instead of this effect silently steering the user elsewhere. Only a
    // hash that matches nothing at all falls through to the "steer to the
    // first reachable tab" branch below.
    const current = filterOptions.find((opt) => opt.fragment == fragment);
    if (current) {
      setSelectedFilter(current.value);
      setSelectedSubFilter(resolveSubFilter(current, subFragment));
      return;
    }
    // No hash, or a fragment that matches nothing: steer to the first section
    // the user can actually open (e.g. an Audit-Read-only user lands on Audit
    // Log, not a disabled default) and sync the hash, so AnchorComponent's
    // highlight follows the body instead of sitting on the disabled default.
    const firstEnabled = filterOptions.find((opt) => !opt.disabled);
    if (!firstEnabled) {
      setSelectedFilter(0);
      setSelectedSubFilter(0);
      return;
    }
    setSelectedFilter(firstEnabled.value);
    setSelectedSubFilter(resolveSubFilter(firstEnabled, undefined));
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
  // tabs (Access & Users — Users/Groups/Roles/Ownership/Audit Log — Notification
  // Rules, Integrations, Tenant Settings) via a typed/bookmarked URL either.
  // hasAdminSurfaceAccess() (not hasReadAccess()) is what the sidebar uses, so a custom-role holder with a grant on an
  // Admin-page module (e.g. audits:Read) can open the page — the per-section
  // disabled-tab gating above then hides sections they lack Read on.

  const selectedOption = filterOptions[selectedFilter];
  const activeSubOption = selectedOption?.tabOptions?.[selectedSubFilter];
  const SelectedBody = activeSubOption?.Body ?? selectedOption?.Body;
  const activeDescription = activeSubOption?.description ?? selectedOption?.description;
  const isSelectedDisabled = activeSubOption ? activeSubOption.disabled : selectedOption?.disabled;

  if (!session) {
    return <Loader />;
  }

  if (!hasAdminSurfaceAccess()) {
    return <PermissionDeniedContent />;
  }

  return (
    <>
      <AnchorComponent
        manageRoute={true}
        options={selectedOption?.options || []}
        filterOptions={filterOptions}
        onChangeFilter={(val, subVal) => {
          setSelectedFilter(val);
          setSelectedSubFilter(subVal ?? 0);
        }}
      />
      <ErrorBoundary key={`${selectedFilter}-${selectedSubFilter}`}>
        <Box mt={2}>
          {isSelectedDisabled && <PermissionDeniedContent message={(activeSubOption ?? selectedOption)?.disabledTooltip} />}
          {SelectedBody && !isSelectedDisabled && (
            <>
              {/* One-line "what is this tab for" caption. Rendered here rather
                  than inside each section body so every tab — including the
                  EE-registered ones — gets it from a single place. */}
              {activeDescription && (
                <Typography
                  id='user-management-tab-description'
                  sx={{
                    pb: ds.space[3],
                    fontSize: ds.text.small,
                    fontWeight: ds.weight.regular,
                    color: ds.gray[600],
                  }}
                >
                  {activeDescription}
                </Typography>
              )}
              <SelectedBody session={session} />
            </>
          )}
        </Box>
      </ErrorBoundary>
    </>
  );
}
