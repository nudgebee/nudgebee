import React, { useEffect } from 'react';
import ErrorBoundary from '@shared/ErrorBoundary';
import AllUsers from '@components/user-management/AllUsers';
import UserGroup from '@components/user-management/UserGroup';
import AnchorComponent from '@components/common/navigation/AnchorComponent';
import { AuditsTable } from '@components/audits';
import { Box, Typography } from '@mui/material';
import Notifications from '@components/notifications';
import Integrations from '@components/accounts/integration';
import OwnershipRules from '@components/user-management/OwnershipRules';
import TenantSettings from '@shared/settings/TenantSettings';
import { AuditIcon, NotificationIcon1, User1, UserGroupIcon, IntegrationsIcon, SettingsIcon, AgentIcon } from '@assets';
import { useSession } from 'next-auth/react';
import { useRouter } from 'next/router';
import { userManagementFilters } from '@lib/authHooks';
import { hasAdminSurfaceAccess, missingPermissionMessage, isUiFeatureEnabled } from '@lib/auth';
import Loader from '@shared/Loader';
import { ds } from '@utils/colors';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import AssignmentIndOutlinedIcon from '@mui/icons-material/AssignmentIndOutlined';
import { AI_TOOLS_SUB_TABS } from '@components/llm/admin/aiToolsConfig';
import { useBCortexEnabled } from '@hooks/useBCortexEnabled';
import { useFeatureAccess } from '@hooks/useFeatureAccess';
import withBrandBlue from '@shared/icons/withBrandBlue';

// AssignmentIndOutlinedIcon defaults to `currentColor` and would otherwise
// render gray/black instead of matching its blue-SVG siblings (Users,
// Groups, Audit Log) in the same Access & Users dropdown.
const OwnershipIcon = withBrandBlue(AssignmentIndOutlinedIcon);

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
  // id is pinned so the DOM id (#anchor-tab-Notifications) that app-e2e-tests
  // locates the tab by stays stable if the label ever changes independently.
  {
    name: 'Notification Rules',
    id: 'Notifications',
    fragment: 'notification-rules',
    icon: NotificationIcon1,
    Body: Notifications,
    module: 'notifications',
    description:
      'Rules that decide which troubleshooting, optimization, SLO and cloud events are delivered — for which clusters and applications, and to which channels.',
  },
  {
    name: 'Integrations',
    fragment: 'integrations',
    icon: IntegrationsIcon,
    Body: Integrations,
    module: 'integrations',
    description: `Connect ${baseTitle} to your clouds, observability platforms, ticketing, repositories and messaging tools — and see what is already connected.`,
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
  // Gates AI & Tools' Functions sub-tab (requiresFeature) — same
  // hasFeatureAccess('LLM_FUNCTION') check Settings used to decide whether
  // to push the tab at all.
  const llmFunctionEnabled = useFeatureAccess('LLM_FUNCTION');

  // Combine base filters with any registered extensions filtered by session,
  // then stamp positional values so AnchorComponent's routing keeps working.
  const filterOptions = React.useMemo(() => {
    const roles = session?.roles ?? [];
    const isAdmin = roles.includes('tenant_admin') || roles.includes('tenant_admin_readonly') || !!session?.isSuperAdmin;
    // Dynamic-RBAC grants ("<module>:<class>") the signed-in user holds.
    const perms = session?.permissions ?? [];
    // userManagementFilters(session) already applies each entry's own
    // shouldShow(session) — an entry is simply absent when it fails (OSS build,
    // or e.g. CUSTOM_ROLES off for this tenant), not present-but-disabled. Roles
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
        // requiresFeature (Functions) / requiresUiFeature (Gateway) are the
        // same two extra gates Settings applied on top of its own module
        // check — dropped, not just disabled, matching Settings never
        // pushing the tab at all when either failed. requiresFeature only
        // ever names 'LLM_FUNCTION' today, so it's checked against
        // llmFunctionEnabled directly rather than a per-feature-name map —
        // revisit if a second requiresFeature value is ever added.
        const visibleTabOptions = f.tabOptions.filter((sub) => {
          if (sub.legacyOnly && bcortexEnabled !== false) {
            return false;
          }
          if (sub.requiresFeature && llmFunctionEnabled !== true) {
            return false;
          }
          if (sub.requiresUiFeature && !isUiFeatureEnabled(sub.requiresUiFeature)) {
            return false;
          }
          return true;
        });
        const tabOptions = visibleTabOptions.map((sub, subIdx) => {
          const lacksPermission = !isAdmin && !!sub.module && !perms.includes(`${sub.module}:Read`);
          return {
            ...sub,
            value: subIdx,
            disabled: sub.disabled ?? lacksPermission,
            disabledTooltip: lacksPermission ? missingPermissionMessage(`${sub.module}:Read`) : undefined,
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
  }, [session, baseTitle, bcortexEnabled, llmFunctionEnabled]);

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
    const requested = subFragment ? tab.tabOptions.find((opt) => opt.fragment === subFragment) : null;
    if (requested && !requested.disabled) return requested.value;
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
    // The tab AnchorComponent highlights: the URL hash's section, else the
    // first tab (its no-hash default). If that section is one the user can
    // open, honor it — body and highlight already agree.
    const current = filterOptions.find((opt) => opt.fragment == fragment);
    const anchorTab = current ?? filterOptions[0];
    if (anchorTab && !anchorTab.disabled) {
      setSelectedFilter(anchorTab.value);
      setSelectedSubFilter(resolveSubFilter(anchorTab, subFragment));
      return;
    }
    // Otherwise the target section is disabled (e.g. an Audit-Read-only user
    // landing on the default Users tab). Steer to the first section they can
    // open and sync the hash, so AnchorComponent's highlight follows the body
    // instead of sitting on the disabled default.
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
  const activeSubOption = selectedOption?.tabOptions?.[selectedSubFilter];
  const SelectedBody = activeSubOption?.Body ?? selectedOption?.Body;
  const activeDescription = activeSubOption?.description ?? selectedOption?.description;
  const isSelectedDisabled = activeSubOption ? activeSubOption.disabled : selectedOption?.disabled;

  if (!session || !hasAdminSurfaceAccess()) {
    return <Loader />;
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
        {/* Guard against the brief mount tick where AnchorComponent reports its
            disabled default (0) before the hash-steer above lands — never render
            a section the user can't open. */}
        <Box mt={2}>
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
