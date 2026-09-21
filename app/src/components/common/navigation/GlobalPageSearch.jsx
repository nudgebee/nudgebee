/**
 * GlobalPageSearch
 *
 * Cmd/Ctrl+K page search box shown in the app header. Self-contained: owns
 * the static + per-provider result lists, the tenant's dashboards, the
 * "@account" scoping picker, recent-search persistence, and the Cmd/Ctrl+K
 * shortcut. Renders with zero props — reads selectedCluster/allCluster from
 * DataContext, the session from NextAuth, and drives navigation itself.
 *
 * The trigger/popover/search-list chrome below is a trimmed-down port of
 * ds/FilterDropdown.jsx's single-select path: this box is never `multiple`
 * or `grouped` and never has a `value` (a pick either navigates away or sets
 * the "@account" scope), so the selected-state, checkbox, Select All/Clear
 * All, freeSolo, and grouped-list branches of that component don't apply
 * here and aren't ported. Keep the two in sync by hand for shared bits
 * (search ranking, virtualization, keyboard nav) if either changes.
 */
import React, { useState, useEffect, useRef, useMemo, useCallback } from 'react';
import { useRouter } from 'next/router';
import { useSession } from 'next-auth/react';
import { v4 as uuidv4 } from 'uuid';
import Box from '@mui/material/Box';
import { Typography, Popover, InputBase, Fade, IconButton } from '@mui/material';
import SearchIcon from '@mui/icons-material/Search';
import CloseIcon from '@mui/icons-material/Close';
import ChevronRightIcon from '@mui/icons-material/ChevronRight';
import { ds } from '@utils/colors';
import { getCloudProviderLabel } from '@utils/common';
import Chip from '@ui/Chip';
import Divider from '@ui/Divider';
import CustomTooltip from '@ui/Tooltip';
import { Button as DsButton } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import SafeIcon from '@shared/icons/SafeIcon';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import { useData } from '@context/DataContext';
import { hasFeatureAccess, hasPermission, hasReadAccess, isUiFeatureEnabled } from '@lib/auth';
import { trackProductEvent } from '@lib/productAnalytics';
import { useTenantBranding } from '@hooks/useTenantBranding';
import apiUser, { PREFERENCE_LAST_ACCOUNT_ID } from '@api1/user';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiDashboards from '@api1/dashboards';
import apiWorkflow from '@api1/workflow';
import homeApi from '@api1/home';
import { transformClusters } from '@shared/layout/UpdateDataContext';
import AdminIconBlue from '@assets/header/AdminIconBlue.icon.svg';
import OptimiseIconBlue from '@assets/header/OptimiseIconBlue.icon.svg';
import TicketIconBlue from '@assets/header/TicketIconBlue.icon.svg';
import TroubleshootIconBlue from '@assets/header/TroubleshootIconBlue.icon.svg';
import { AutomateBlue, AgentIconBlue, dashboardIcon1, KubernetesClusterIcon, VmIcon } from '@assets';
import {
  navSearchPages,
  accountScopedSearchFragments,
  automationSearchFragments,
  integrationProviders,
  k8sDetailsSearchFragments,
  awsDetailsSearchFragments,
  azureDetailsSearchFragments,
  gcpDetailsSearchFragments,
  pathAcronym,
  wordsOf,
  fuzzyTokenMatches,
  MIN_FUZZY_TOKEN_LENGTH,
} from '@lib/navSearchPages';

// Layout constants for the result list — mirrors ds/FilterDropdown.jsx's own
// (OPTION_HEIGHT/OVERSCAN_COUNT/VIRTUALIZATION_THRESHOLD), duplicated rather
// than imported since this file no longer depends on that component.
const OPTION_HEIGHT = 36;
const OVERSCAN_COUNT = 10;
const VIRTUALIZATION_THRESHOLD = 200;
const MAX_LIST_HEIGHT = 380;
const POPOVER_WIDTH = ds.space.mul(0, 340);

// Rows shown per category before its chevron is needed to reveal the rest.
const MAX_SECTION_ROWS = 5;

// Some sidebar icons are drawn white-on-dark (fills or strokes) and render
// invisible on this light popover — repaint with a --ds-* token. Scoped to
// `[fill]`/`[stroke]` attribute selectors, not every shape tag, so a
// fill-only icon doesn't get an unwanted stroke painted on and vice versa.
const recoloredSidebarIcon = (src) => (
  <Box
    component='span'
    sx={{
      display: 'inline-flex',
      flexShrink: 0,
      width: 16,
      height: 16,
      '& svg': { width: 16, height: 16 },
      '& svg [fill]': { fill: 'var(--ds-gray-600)' },
      '& svg [stroke]': { stroke: 'var(--ds-gray-600)' },
    }}
  >
    <SafeIcon src={src} alt='' width={16} height={16} />
  </Box>
);
const DashboardRowIcon = recoloredSidebarIcon(dashboardIcon1);

// Icon shown per header-search row: the parent page's icon (same icons the
// main nav uses for these sections), not a distinct icon per tab.
const NAV_SEARCH_GROUP_ICON = {
  Dashboards: DashboardRowIcon,
  Troubleshoot: TroubleshootIconBlue,
  Automation: AutomateBlue,
  'Agent Health': AgentIconBlue,
  Optimize: OptimiseIconBlue,
  Tickets: TicketIconBlue,
  Admin: AdminIconBlue,
  Overview: recoloredSidebarIcon(KubernetesClusterIcon),
  VM: recoloredSidebarIcon(VmIcon),
};

// Keyboard-hint bar rendered below the options list — static markup (no
// component state), so it's built once at module scope rather than
// re-created every render.
const NAV_SEARCH_KEYBOARD_HINTS = [{ keys: ['↑', '↓'], label: 'Navigate' }];

const searchKeyChipSx = {
  fontFamily: 'var(--ds-font-mono)',
  fontSize: `${ds.space.mul(0, 5)}`,
  color: 'var(--ds-gray-500)',
  border: '1px solid var(--ds-gray-200)',
  borderRadius: 'var(--ds-radius-sm)',
  padding: `${ds.space[0]} ${ds.space.mul(0, 3)}`,
};

const GlobalSearchFooterHints = ({ mentionMode = false }) => (
  <Box
    id='global-search-footer-hints'
    sx={{
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
      gap: 'var(--ds-space-4)',
      px: 'var(--ds-space-4)',
      py: 'var(--ds-space-2)',
    }}
  >
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-4)', flexShrink: 0 }}>
      {NAV_SEARCH_KEYBOARD_HINTS.map(({ keys, label }) => (
        <Box key={label} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
          {keys.map((k) => (
            <Box key={k} component='kbd' sx={searchKeyChipSx}>
              {k}
            </Box>
          ))}
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>{label}</Typography>
        </Box>
      ))}
    </Box>
    {/* Same short-form wording/example as the trigger's "How to search" tooltip, swapped for
        an account-picker-appropriate tip while mentionMode is active. */}
    <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', whiteSpace: 'nowrap' }}>
      {mentionMode ? (
        'Pick an account to search within it'
      ) : (
        <>Tip: first letters of each segment, e.g. &ldquo;umu&rdquo; for user-management/users</>
      )}
    </Typography>
  </Box>
);

// Small pill rendered inside the search box's own startAdornment once an
// "@account" pick has been made — collapses the typed "@name" text into a
// persistent chip so the user can keep typing a page query after it.
const AccountMentionChip = ({ account }) => (
  <Chip
    variant='tag'
    tone='info'
    size='xs'
    icon={<CloudProviderIcon cloud_provider={account.cloud_provider} width='12px' height='12px' />}
    sx={{ mr: ds.space[1], flexShrink: 0 }}
  >
    {account.label}
  </Chip>
);

// Max width for the row's trailing path text — wide enough for a full path
// like "/aws/optimize/recommendation-resolution" without colliding with the
// account-name chip on rows that have one.
const NAV_SEARCH_PATH_MAX_WIDTH = '60%';

// Search rows for one provider's detail-page tabs (K8s/AWS/Azure/GCP), or []
// if no account of that provider is resolvable yet (fresh tenant, no such
// accounts). Pure function of its arguments — kept at module scope (not a
// component closure) so each provider's rows can be memoized separately,
// keyed only on that provider's own resolved account id.
const navSearchProviderItems = (fragments, provider, accountId, basePath) =>
  accountId
    ? fragments.map((entry) => {
        const path = `${basePath}/${accountId}#${entry.fragment}`;
        return {
          label: entry.label,
          icon: <CloudProviderIcon cloud_provider={provider} width='16px' height='16px' />,
          type: `/${entry.slug}`,
          value: path,
          path,
          accountId,
          acronym: pathAcronym(entry.slug),
          searchText: `${provider} ${entry.slug} ${pathAcronym(entry.slug)}`,
        };
      })
    : [];

// Search rows for pages whose tabs need an accountId but aren't tied to a
// single cloud provider (any connected account works) — Automation and Agent
// Health today, see accountScopedSearchFragments' own doc comment in
// navSearchPages.ts. Same shape/contract as navSearchProviderItems above,
// kept as its own function (not folded into it) since these routes carry the
// account as a `?accountId=` query param rather than a `{basePath}/{id}`
// path segment. Each entry supplies its own basePath/group since the array
// now spans more than one page.
const navSearchAccountScopedItems = (fragments, accountId) =>
  accountId
    ? fragments.map((entry) => {
        const path = `${entry.basePath}?accountId=${accountId}#${entry.fragment}`;
        return {
          label: entry.label,
          icon: NAV_SEARCH_GROUP_ICON[entry.group],
          type: `/${entry.slug}`,
          value: path,
          path,
          accountId,
          group: entry.group,
          acronym: pathAcronym(entry.slug),
          searchText: `${entry.group} ${entry.slug} ${pathAcronym(entry.slug)}`,
        };
      })
    : [];

// Search rows for the Automation page's tabs. Separate from the helper above
// because /automation is tenant-level (#35113): the plain rows carry no account
// at all, and only an "@account" scoped pick appends one — as `?account=`, the
// param the listing's Account filter reads.
const navSearchAutomationItems = (fragments, accountId) =>
  fragments.map((entry) => {
    const path = accountId ? `/automation?account=${accountId}#${entry.fragment}` : `/automation#${entry.fragment}`;
    return {
      label: entry.label,
      icon: NAV_SEARCH_GROUP_ICON.Automation,
      type: `/${entry.slug}`,
      value: path,
      path,
      accountId,
      group: 'Automation',
      acronym: pathAcronym(entry.slug),
      searchText: `Automation ${entry.slug} ${pathAcronym(entry.slug)}`,
    };
  });

// The deep link that opens one dashboard — the same URL the listing itself
// writes when a dashboard is opened there: the id rides in a query param
// because /dashboards is hash-routed and the hash picks the tab (see
// CustomDashboards.jsx's DASHBOARD_PARAM).
const dashboardSearchPath = (id) => `/dashboards?dashboard=${id}#list`;

// Search rows for the tenant's own dashboards (not the /dashboards page's tabs,
// which are plain static rows in navSearchPages). A dashboard is tenant-level —
// it has no account of its own, each of its PANELS names the account it queries
// — so unlike the provider rows above these carry no accountId.
const navSearchDashboardItems = (dashboards) =>
  dashboards.map((dashboard) => {
    const path = dashboardSearchPath(dashboard.id);
    return {
      label: dashboard.title,
      icon: NAV_SEARCH_GROUP_ICON.Dashboards,
      // The page path, not this row's own deep link — the trailing path text is
      // there to say where a row lands, and an opaque uuid says nothing.
      type: '/dashboards',
      value: path,
      path,
      group: 'Dashboards',
      sectionLabel: 'Dashboards',
      // Title is already matched through `label` — this only adds what the row
      // doesn't show but is still worth finding a dashboard by.
      searchText: `Dashboards ${dashboard.description || ''} ${(dashboard.tags || []).join(' ')}`,
    };
  });

// Same URL WorkflowListing.jsx's handleEditWorkflow already navigates to.
const workflowSearchPath = (id, accountId) => `/automation/${id}?accountId=${accountId}`;

// workflow.tags isn't reliably an array at runtime (can be a tag-map object
// or a string) — normalizes the same shapes WorkflowListing.tsx's TagsDisplay
// already handles.
const workflowTagsToWords = (tags) => {
  if (!tags) {
    return [];
  }
  if (Array.isArray(tags)) {
    return tags;
  }
  if (typeof tags === 'object') {
    return Object.entries(tags).map(([key, value]) => (value ? `${key}: ${value}` : key));
  }
  return [String(tags)];
};

// Search rows for the tenant's own automations. Unlike a dashboard, an
// automation belongs to one account — accountName/cloud_provider are resolved
// eagerly (not deferred to Recents) since names can collide across accounts.
// Rows whose account doesn't resolve in allCluster are dropped (same rule
// resolveRecentOption's account-scoped branches use) rather than shown with a
// blank chip — the fetch itself can return accounts outside allCluster.
const navSearchWorkflowItems = (workflows, allCluster) =>
  workflows
    .map((workflow) => ({ workflow, account: allCluster?.find((c) => c.value === workflow.account_id) }))
    .filter(({ account }) => account)
    .map(({ workflow, account }) => {
      const path = workflowSearchPath(workflow.id, workflow.account_id);
      return {
        label: workflow.name,
        icon: NAV_SEARCH_GROUP_ICON.Automation,
        type: '/automation',
        value: path,
        path,
        accountId: workflow.account_id,
        accountName: account.label,
        cloud_provider: account.cloud_provider,
        group: 'Automation',
        sectionLabel: 'Automations',
        searchText: `Automation ${workflowTagsToWords(workflow.tags).join(' ')} ${account.label || ''}`,
      };
    });

// Per-provider fragment list + base path for the "@account" scoped search —
// keyed by cloud_provider.toUpperCase() since allCluster entries' casing
// isn't guaranteed to match the mixed-case provider labels used elsewhere.
// Only providers with a detail-page fragment list are eligible for scoping —
// mentioning an account of any other provider would be a dead end.
const SCOPED_SEARCH_PROVIDER_CONFIG = {
  K8S: { fragments: k8sDetailsSearchFragments, basePath: '/kubernetes/details', label: 'K8s' },
  AWS: { fragments: awsDetailsSearchFragments, basePath: '/cloud-account/details', label: 'AWS' },
  AZURE: { fragments: azureDetailsSearchFragments, basePath: '/cloud-account/details', label: 'Azure' },
  GCP: { fragments: gcpDetailsSearchFragments, basePath: '/cloud-account/details', label: 'GCP' },
};

// Matches a provider-detail search value stamped by navSearchProviderItems
// (`{basePath}/{accountId}#{fragment}`) and captures the accountId — used to
// re-resolve a recent pick against allCluster (see resolveRecentOption
// below), since a recent value's account isn't necessarily the provider's
// current single resolved account.
const ACCOUNT_SCOPED_SEARCH_PATH_RE = /^\/(?:kubernetes\/details|cloud-account\/details)\/([^/#]+)#/;

// Matches a search value stamped by navSearchAccountScopedItems
// (`{basePath}?accountId={accountId}#{fragment}`) and captures the accountId
// — used to re-resolve a recent pick against allCluster (see
// resolveRecentOption below), same reasoning as ACCOUNT_SCOPED_SEARCH_PATH_RE
// above: a recent value's account isn't necessarily whichever one
// defaultAccountId currently resolves to. The alternation must list every
// basePath used in accountScopedSearchFragments (navSearchPages.ts) — add a
// new one there when adding a new basePath.
const ACCOUNT_SCOPED_QUERY_SEARCH_PATH_RE = /^\/(?:agentHealth)\?accountId=([^#]+)#/;

// Matches an Automation search value stamped by navSearchAutomationItems and
// captures the account, if any:
//   /automation#{fragment}                — the tenant-level row
//   /automation?account={id}#{fragment}   — an "@account" scoped pick
// The `accountId` alternative only exists so recents predating #35113 (and the
// pre-split /automation?accountId= rows) are still recognised as Automation
// rather than falling through to the provider-detail branch; they no longer
// match a generated value, so they drop out of Recents on first use — the same
// self-healing path a renamed page already takes. A captured account is
// re-resolved against allCluster, same reasoning as above.
const AUTOMATION_SCOPED_SEARCH_PATH_RE = /^\/automation(?:\?(?:accountId|account)=([^#&]+))?#/;

// Matches a dashboard row's value stamped by navSearchDashboardItems. Only used
// to route a recent pick to the dashboard branch of resolveRecentOption — the
// id isn't captured, since that branch re-resolves the whole value against the
// live dashboard list anyway (a deleted dashboard then drops out of Recents,
// same as a removed page already does).
const DASHBOARD_SEARCH_PATH_RE = /^\/dashboards\?dashboard=/;

// Matches a workflow row's value (`/automation/{id}?accountId=...`). Distinct
// from AUTOMATION_SCOPED_SEARCH_PATH_RE (requires a trailing `#fragment`).
const WORKFLOW_SEARCH_PATH_RE = /^\/automation\/([^/?]+)\?accountId=([^#&]+)/;

// Rows asked of dashboards_list per open. 500 is that action's own maximum
// (anything higher silently falls back to its default 200), and asking for the
// maximum is what lets a short response be read as "this is every dashboard in
// the tenant" — which is the precondition for pruning a recent pick whose
// dashboard is missing from it (see the fetch effect below).
const DASHBOARD_SEARCH_FETCH_LIMIT = 500;

// Rows asked of workflow_list per open. Unlike dashboards, this API returns
// total_count directly, so completeness (the precondition for pruning a
// recent pick) is judged against that rather than against this limit.
const WORKFLOW_SEARCH_FETCH_LIMIT = 500;

// Same provider-order + connection-status + alphabetical sort ClusterDropDown
// itself uses (CustomDropdown.jsx's groupedOptions, groupByCloudProvider mode)
// — ported rather than imported since that version wraps each provider in a
// {label, options, isGroup} header object for its own group-header rendering,
// and this just needs a flat, identically-ordered list for the "@account"
// picker. Keep in sync with CustomDropdown.jsx if that sort ever changes.
const MENTION_PROVIDER_ORDER = (provider) => {
  const p = provider.toLowerCase();
  if (p === 'aws') return 0;
  if (p === 'azure') return 1;
  if (p === 'gcp') return 2;
  if (p === 'k8s') return 3;
  if (p === 'oci') return 4;
  if (p === 'cloudfoundry') return 5;
  return 999;
};

const isConnectedUsingDate = (lastConnectedDateStr) => {
  if (!lastConnectedDateStr) {
    return false;
  }
  const lastConnectedDate = new Date(lastConnectedDateStr);
  return new Date().getTime() - lastConnectedDate.getTime() < 2 * 24 * 3600 * 1000;
};

const checkAccountConnections = (account) => {
  if (account.cloud_provider?.toLowerCase() != 'k8s') {
    const connectionStatus = account.agent?.connection_status;
    if (!connectionStatus) {
      return account.agent?.status === 'CONNECTED';
    }
    const servicesStatus = {
      events: isConnectedUsingDate(connectionStatus?.events?.end),
      resources: isConnectedUsingDate(connectionStatus?.resources?.updated_at),
      recommendations: isConnectedUsingDate(connectionStatus?.recommendations?.updated_at),
      spends: isConnectedUsingDate(connectionStatus?.spends?.updated_at),
    };
    return Object.values(servicesStatus).every((status) => status === true);
  }
  const connectionStatus = account.agent?.connection_status;
  if (!connectionStatus) {
    return false;
  }
  const requiredProps = ['logsConnection', 'nodeAgentConnection', 'prometheusConnection', 'relayConnection'];
  for (const prop of requiredProps) {
    if (!connectionStatus[prop]) {
      return false;
    }
  }
  if (!connectionStatus.opencostConnection && !connectionStatus.opencostServerSide) {
    return false;
  }
  return true;
};

const getAccountConnectionPriority = (account) => {
  if (account.agent?.status === 'CONNECTED') {
    return checkAccountConnections(account) ? 0 : 1;
  }
  return 2;
};

const sortAccountsLikeClusterDropdown = (accounts) => {
  const groups = {};
  accounts.forEach((account) => {
    // Normalized to uppercase so e.g. 'AWS' and 'aws' accounts merge into one
    // group instead of splitting into two separately-sorted runs — allCluster
    // entries' casing isn't guaranteed consistent (see SCOPED_SEARCH_PROVIDER_CONFIG's
    // own comment on this).
    const provider = account.cloud_provider?.toUpperCase() || 'OTHER';
    if (!groups[provider]) {
      groups[provider] = [];
    }
    groups[provider].push(account);
  });
  Object.values(groups).forEach((group) => {
    group.sort((a, b) => {
      const aPriority = getAccountConnectionPriority(a);
      const bPriority = getAccountConnectionPriority(b);
      if (aPriority !== bPriority) {
        return aPriority - bPriority;
      }
      const labelA = (a.label || '').toString().toLowerCase();
      const labelB = (b.label || '').toString().toLowerCase();
      return labelA.localeCompare(labelB, undefined, { numeric: true, sensitivity: 'base' });
    });
  });
  return Object.entries(groups)
    .sort(([providerA], [providerB]) => MENTION_PROVIDER_ORDER(providerA) - MENTION_PROVIDER_ORDER(providerB))
    .flatMap(([, group]) => group);
};

// Search rows for individual integration providers (Admin > Integrations
// tab's cards) — a real deep link past that tab's card grid, but `type` is a
// synthetic "integrations/{slug}" display path, not the row's real URL.
const navSearchIntegrationItems = integrationProviders.map((provider) => {
  const slug = provider.toLowerCase().replace(/_/g, '-');
  const fragmentPath = `integrations/${slug}`;
  const accountFormPath = `/accounts/account-form?cloudProvider=${provider}`;
  return {
    label: getCloudProviderLabel(provider),
    icon: <CloudProviderIcon cloud_provider={provider} width='16px' height='16px' />,
    type: `/${fragmentPath}`,
    value: accountFormPath,
    path: accountFormPath,
    acronym: pathAcronym(fragmentPath),
    searchText: `Admin ${fragmentPath} ${pathAcronym(fragmentPath)}`,
    group: 'Admin',
    sectionLabel: 'Integrations',
  };
});

// Static (non-account) search rows — built once at module load. Integration
// rows are kept separate (navSearchIntegrationItems above) since they get
// their own "Integrations" section rather than Suggested Pages.
const navSearchStaticItems = navSearchPages.map((page) => {
  const fragmentPath = page.path.replace(/^\//, '').replace('#', '/');
  return {
    label: page.label,
    icon: NAV_SEARCH_GROUP_ICON[page.group],
    type: `/${fragmentPath}`,
    value: page.path,
    path: page.path,
    acronym: pathAcronym(fragmentPath),
    searchText: `${page.group} ${fragmentPath} ${pathAcronym(fragmentPath)}`,
    group: page.group,
  };
});

// --- Result-list chrome (trimmed port of ds/FilterDropdown.jsx, single-select only) ---

// Option-row label that truncates with an ellipsis and shows a tooltip with
// the full text *only when clipped*. Open state is controlled so we can
// force-close on scroll: otherwise scrolling the option list moves the
// hovered row away from the cursor while MUI keeps the tooltip open and the
// Popper flips it up to stay in view — a tooltip left floating above the panel.
function OptionLabel({ label }) {
  const [overflowing, setOverflowing] = useState(false);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!(open && overflowing)) return undefined;
    const close = () => setOpen(false);
    // Capture phase so a scroll inside the option list (an inner scroll
    // container, which doesn't bubble) triggers it too, not just window scroll.
    window.addEventListener('scroll', close, true);
    return () => window.removeEventListener('scroll', close, true);
  }, [open, overflowing]);

  return (
    <CustomTooltip
      title={overflowing ? label : ''}
      placement='top'
      open={open && overflowing}
      onOpen={() => setOpen(true)}
      onClose={() => setOpen(false)}
    >
      <span
        onMouseEnter={(e) => {
          const el = e.currentTarget;
          setOverflowing(el.scrollWidth > el.clientWidth);
        }}
        style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'inherit' }}
      >
        {label}
      </span>
    </CustomTooltip>
  );
}

// One result row. No selected/checkbox state — this list never has a
// "value", a pick either navigates away or sets the "@account" scope.
const OptionItem = React.memo(function OptionItem({ opt, highlighted = false, navIndex, navActive = false, onSelect }) {
  const handleKeyDown = (e) => {
    if (e.key !== 'Enter' || navActive) {
      // While Arrow-key nav is active, Enter is owned by the Popover-level
      // handler (acts on `highlightedIndex`) — don't also select *this* row
      // here, since DOM focus (Tab) and the arrow-highlighted row can differ.
      // Letting the event bubble there is what actually selects the highlighted row.
      return;
    }
    e.preventDefault();
    onSelect(opt);
  };

  return (
    <Box
      role='option'
      aria-selected={false}
      tabIndex={0}
      data-option-index={navIndex}
      onClick={() => onSelect(opt)}
      onKeyDown={handleKeyDown}
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: ds.space.mul(0, 5),
        height: `${OPTION_HEIGHT}px`,
        padding: 'var(--ds-overlay-item-padding-md)',
        margin: '0 var(--ds-overlay-item-margin-x)',
        borderRadius: 'var(--ds-overlay-item-radius)',
        cursor: 'pointer',
        fontSize: 'var(--ds-text-body)',
        fontWeight: 'var(--ds-font-weight-regular)',
        color: 'var(--ds-gray-700)',
        backgroundColor: 'transparent',
        // Keyboard-navigated row (ArrowUp/ArrowDown) gets a ring, independent
        // of hover, so the two stay visually distinguishable.
        boxShadow: highlighted ? 'inset 0 0 0 1.5px var(--ds-blue-400)' : 'none',
        transition: 'background var(--ds-motion-micro) var(--ds-motion-ease)',
        boxSizing: 'border-box',
        '&:hover': { backgroundColor: 'var(--ds-overlay-item-hover-bg)' },
      }}
    >
      {opt?.icon && <SafeIcon src={opt.icon} alt={opt?.type ?? ''} style={{ width: 16, height: 16, flexShrink: 0, objectFit: 'contain' }} />}
      <OptionLabel label={opt?.label ?? ''} />
      {opt?.accountName && (
        <Box sx={{ flexShrink: 0, maxWidth: '30%' }}>
          {/* Chip has no CSS truncation of its own (whiteSpace: 'nowrap', no
              overflow: hidden) — a long account name would otherwise spill
              past this 30% slot and collide with the path text. displayTooltip
              shortens the actual text (not just visually) to fit. */}
          <Chip
            variant='tag'
            tone='info'
            size='xs'
            icon={<CloudProviderIcon cloud_provider={opt.cloud_provider} width='12px' height='12px' />}
            displayTooltip
            tooltipCharLimit={15}
          >
            {opt.accountName}
          </Chip>
        </Box>
      )}
      {opt?.type && (
        <Typography
          sx={{
            ml: 'auto',
            flexShrink: 0,
            maxWidth: NAV_SEARCH_PATH_MAX_WIDTH,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            fontSize: 'var(--ds-text-caption)',
            color: 'var(--ds-gray-500)',
          }}
        >
          {opt.type}
        </Typography>
      )}
    </Box>
  );
});

// Caption above a run of options sharing a `sectionLabel` — renders once per
// contiguous run, whenever the label changes from the row before it.
const startsNewSection = (opt, prevOpt) => !!opt?.sectionLabel && opt.sectionLabel !== prevOpt?.sectionLabel;

// Stable identity for a navigableItems entry — used to track the keyboard
// highlight by *what* is highlighted rather than its raw array position, so
// it survives navigableItems reshuffling for a reason unrelated to the
// highlighted entry itself (e.g. a different, earlier section's chevron
// toggled via mouse click, which shifts every later index without changing
// what the user was actually looking at). Includes sectionLabel, not just
// `.value` — a recent pick intentionally duplicates its origin section's row
// (same value, e.g. a dashboard shown under both "Recents" and "Dashboards"),
// same reason renderRow's own React `key` prop below already does this.
const navItemKey = (item) => (item?.__sectionToggle ? `__toggle:${item.sectionLabel}` : `${item?.sectionLabel || ''}-${item?.value}`);

// `collapsible` = the section's true count (from filteredOptions, not the
// truncated render) exceeds MAX_SECTION_ROWS. Chevron is a plain icon, not
// its own IconButton, so clicking it doesn't double-fire onToggle via bubbling.
function SectionCaption({ label, collapsible, expanded, onToggle, highlighted = false, navIndex, navActive = false }) {
  return (
    <Box
      // Stable id so the guide tour can spotlight each section.
      id={`global-search-section-${label.toLowerCase().replace(/\s+/g, '-')}`}
      data-option-index={navIndex}
      onClick={collapsible ? onToggle : undefined}
      onKeyDown={
        collapsible
          ? (e) => {
              // Space has no Popover-level handling, so always act on it. Enter
              // defers to the bubbled Popover-level handler while Arrow-key nav
              // is active — same reason OptionItem's own Enter handling does,
              // since Tab-focus and the arrow-highlighted row can differ.
              if (e.key === ' ') {
                e.preventDefault();
                onToggle();
              } else if (e.key === 'Enter' && !navActive) {
                e.preventDefault();
                onToggle();
              }
            }
          : undefined
      }
      role={collapsible ? 'button' : undefined}
      tabIndex={collapsible ? 0 : undefined}
      aria-label={collapsible ? (expanded ? `Show fewer ${label}` : `Show all ${label}`) : undefined}
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: ds.space[0],
        padding: 'var(--ds-overlay-item-padding-md)',
        margin: '0 var(--ds-overlay-item-margin-x)',
        cursor: collapsible ? 'pointer' : 'default',
        borderRadius: 'var(--ds-overlay-item-radius)',
        boxShadow: highlighted ? 'inset 0 0 0 1.5px var(--ds-blue-400)' : 'none',
        '&:hover': collapsible ? { backgroundColor: 'var(--ds-overlay-item-hover-bg)' } : undefined,
      }}
    >
      <Typography
        sx={{
          fontSize: 'var(--ds-text-caption)',
          fontWeight: 'var(--ds-font-weight-semibold)',
          color: 'var(--ds-gray-500)',
          textTransform: 'uppercase',
          letterSpacing: '0.02em',
        }}
      >
        {label}
      </Typography>
      {collapsible && (
        <ChevronRightIcon
          sx={{
            fontSize: 16,
            color: 'var(--ds-gray-500)',
            transform: expanded ? 'rotate(90deg)' : 'none',
            transition: 'transform var(--ds-motion-micro) var(--ds-motion-ease)',
          }}
        />
      )}
    </Box>
  );
}

const scrollboxSx = {
  maxHeight: `${MAX_LIST_HEIGHT}px`,
  overflowY: 'auto',
  padding: 'var(--ds-overlay-padding-y) 0',
  '&::-webkit-scrollbar': { width: ds.space[1] },
  '&::-webkit-scrollbar-track': { background: 'transparent' },
  '&::-webkit-scrollbar-thumb': { background: 'var(--ds-gray-300)', borderRadius: ds.radius.sm },
  '&::-webkit-scrollbar-thumb:hover': { background: 'var(--ds-gray-400)' },
};

// Flat, virtualized-when-large result list. No "selected" section (this box
// never has a `value`) and no group headers — see the file-level comment.
// `filteredOptions` here is `navigableItems` — displayedOptions with a
// `{ __sectionToggle, sectionLabel }` marker spliced in before each
// collapsible section, so Arrow keys can reach and toggle it.
// `queryResultsKey` is the parent's un-truncated list, used only to key the
// scroll-reset effect below (see there for why).
function OptionsList({ filteredOptions, highlightedIndex, onSelect, mentionMode, expandedSections, onToggleSection, queryResultsKey }) {
  const navActive = highlightedIndex >= 0;
  const scrollRef = useRef(null);
  const [scrollTop, setScrollTop] = useState(0);

  const handleScroll = useCallback((e) => setScrollTop(e.currentTarget.scrollTop), []);

  // Keyed off queryResultsKey, not the rendered `filteredOptions` — the
  // latter also changes identity on a chevron toggle, which shouldn't yank
  // the list back to the top.
  useEffect(() => {
    setScrollTop(0);
    if (scrollRef.current) {
      scrollRef.current.scrollTop = 0;
    }
  }, [queryResultsKey]);

  const useVirtualization = filteredOptions.length > VIRTUALIZATION_THRESHOLD;

  const virtualizedContent = useMemo(() => {
    if (!useVirtualization) {
      return null;
    }
    const startIndex = Math.max(0, Math.floor(scrollTop / OPTION_HEIGHT) - OVERSCAN_COUNT);
    const endIndex = Math.min(filteredOptions.length, Math.ceil((scrollTop + MAX_LIST_HEIGHT) / OPTION_HEIGHT) + OVERSCAN_COUNT);
    return {
      startIndex,
      endIndex,
      topSpacerHeight: startIndex * OPTION_HEIGHT,
      bottomSpacerHeight: Math.max(0, (filteredOptions.length - endIndex) * OPTION_HEIGHT),
    };
  }, [useVirtualization, scrollTop, filteredOptions.length]);

  // Keep the ArrowUp/ArrowDown-highlighted row scrolled into view. Uses the
  // real rendered DOM node (via scrollIntoView) rather than computed pixel
  // math — the scrollbox has its own padding a hand-rolled offset calc would
  // need to duplicate exactly. highlightedIndex only ever moves by 1 (Arrow
  // keys), and OVERSCAN_COUNT pads the virtualized render window well past a
  // single-row step, so the target is always mounted.
  useEffect(() => {
    if (highlightedIndex < 0 || !scrollRef.current) {
      return;
    }
    scrollRef.current.querySelector(`[data-option-index="${highlightedIndex}"]`)?.scrollIntoView({ block: 'nearest' });
  }, [highlightedIndex]);

  if (filteredOptions.length === 0) {
    // The "Ask {assistantName}" button next to the search input is always
    // visible, so a non-mention empty state needs no text of its own here.
    // It's only mentionMode (no account matches the typed "@partial-name")
    // where that button doesn't apply and this text is the sole indicator.
    return (
      <Box id='global-search-options-list' data-mention-mode={mentionMode ? 'true' : 'false'} sx={scrollboxSx}>
        {mentionMode && (
          <Typography
            sx={{
              padding: `${ds.space[4]} ${ds.space.mul(0, 7)}`,
              fontSize: 'var(--ds-text-body)',
              color: 'var(--ds-gray-500)',
              textAlign: 'center',
            }}
          >
            No results found
          </Typography>
        )}
      </Box>
    );
  }

  const renderRow = (opt, idx) => {
    // A collapsible section's own caption is this marker, not a startsNewSection
    // hit on the first real row below it — its sectionLabel matches, so that
    // check naturally stays false for that row.
    if (opt.__sectionToggle) {
      return (
        <React.Fragment key={`toggle-${opt.sectionLabel}`}>
          {idx !== 0 && <Divider sx={{ marginTop: 0, marginBottom: 0 }} />}
          <SectionCaption
            label={opt.sectionLabel}
            collapsible
            expanded={expandedSections.has(opt.sectionLabel)}
            onToggle={() => onToggleSection(opt.sectionLabel)}
            highlighted={idx === highlightedIndex}
            navIndex={idx}
            navActive={navActive}
          />
        </React.Fragment>
      );
    }
    const isNewSection = startsNewSection(opt, filteredOptions[idx - 1]);
    return (
      <React.Fragment key={(opt.sectionLabel || '') + '-' + opt.value}>
        {/* idx !== 0 excludes the very first section's own caption. Only ever
            fires here for a non-collapsible section — a collapsible one's
            caption is the toggle-entry branch above. */}
        {isNewSection && idx !== 0 && <Divider sx={{ marginTop: 0, marginBottom: 0 }} />}
        {isNewSection && <SectionCaption label={opt.sectionLabel} collapsible={false} />}
        <OptionItem opt={opt} highlighted={idx === highlightedIndex} navIndex={idx} navActive={navActive} onSelect={onSelect} />
      </React.Fragment>
    );
  };

  return (
    <Box id='global-search-options-list' data-mention-mode={mentionMode ? 'true' : 'false'} ref={scrollRef} onScroll={handleScroll} sx={scrollboxSx}>
      {useVirtualization ? (
        <>
          <div style={{ height: virtualizedContent.topSpacerHeight }} aria-hidden='true' />
          {filteredOptions
            .slice(virtualizedContent.startIndex, virtualizedContent.endIndex)
            .map((opt, i) => renderRow(opt, virtualizedContent.startIndex + i))}
          <div style={{ height: virtualizedContent.bottomSpacerHeight }} aria-hidden='true' />
        </>
      ) : (
        filteredOptions.map(renderRow)
      )}
    </Box>
  );
}

// Popover paper's own enter/exit transition. MUI Popover defaults to Grow,
// which times its exit off an auto-computed, height-dependent duration and
// also animates `transform` (scale-down) — fighting the `globalSearchPopoverPopIn`
// keyframe below, which owns transform on enter and (via `forwards`) holds it
// at rest afterward. Swapping in a Fade tuned the same way ds/Modal's own
// ModalTransition is (fixed timeout, slower custom-eased exit) makes closing
// a clean, opacity-only fade — actually matching the "same as Modal's exit"
// this file already aimed for, rather than falling back to Grow's defaults.
const GlobalSearchPopoverTransition = React.forwardRef(function GlobalSearchPopoverTransition(props, ref) {
  return (
    <Fade
      ref={ref}
      {...props}
      easing={{
        enter: 'var(--ds-overlay-enter-easing)',
        exit: 'cubic-bezier(0.4, 0, 0.2, 1)',
      }}
      timeout={{ enter: 250, exit: 220 }}
    />
  );
});

function GlobalPageSearch({ hasClusterDropdown = true }) {
  const { data } = useSession();
  const router = useRouter();
  const { selectedCluster, allCluster, setSelectedCluster, setAllCluster } = useData();
  const { assistantName, nubiIconUrl } = useTenantBranding();

  // allCluster is deliberately NOT a dependency (or read in the guard) here —
  // this effect itself calls setAllCluster, and transformClusters returns a
  // new array reference every time, so an allCluster dependency would
  // re-trigger this same effect on its own write and loop forever. Keying
  // only on hasClusterDropdown means this fires once per page that has no
  // ClusterDropdown, which is what we want. setAllCluster IS included below —
  // it's stable (wrapped in useCallback with an empty dep array in
  // DataContext.jsx) so it never actually changes, but listing it keeps this
  // effect honest for exhaustive-deps instead of needing a disable comment.
  useEffect(() => {
    if (hasClusterDropdown || allCluster?.length > 0) {
      return;
    }
    let active = true;
    homeApi
      .getCloudAccounts('', false, true)
      .then((res) => {
        if (active) {
          setAllCluster(transformClusters(res));
        }
      })
      .catch((err) => {
        console.error('Failed to fetch cloud accounts:', err);
      });
    return () => {
      active = false;
    };
  }, [hasClusterDropdown, setAllCluster]);

  // Resolves "an account of this provider" for the details-page search
  // entries below: the current cluster if it matches, else the last account
  // the user picked for that provider (per-provider preference cache), else
  // the first matching account available — same fallback-to-first-available
  // precedent ClusterDropDown.jsx already uses when resolving "an account" for
  // a provider with no prior selection.
  const resolveSearchAccountId = useCallback(
    (providerKey) => {
      const upperProvider = providerKey.toUpperCase();
      if (selectedCluster?.cloud_provider?.toUpperCase() === upperProvider && selectedCluster?.value) {
        return selectedCluster.value;
      }
      const cachedId = apiUser.getLastAccountIdForProvider(providerKey, data?.tenant?.id);
      if (cachedId && allCluster?.some((c) => c.value === cachedId && c.cloud_provider?.toUpperCase() === upperProvider)) {
        return cachedId;
      }
      return allCluster?.find((c) => c.cloud_provider?.toUpperCase() === upperProvider)?.value || null;
    },
    [selectedCluster, allCluster, data?.tenant?.id]
  );

  const k8sSearchAccountId = useMemo(() => resolveSearchAccountId('K8s'), [resolveSearchAccountId]);
  const awsSearchAccountId = useMemo(() => resolveSearchAccountId('AWS'), [resolveSearchAccountId]);
  const azureSearchAccountId = useMemo(() => resolveSearchAccountId('Azure'), [resolveSearchAccountId]);
  const gcpSearchAccountId = useMemo(() => resolveSearchAccountId('GCP'), [resolveSearchAccountId]);

  // Navigates to a search result or an Ask-AI hand-off. K8s/AWS/Azure/GCP
  // search results carry `accountId` for whichever account
  // resolveSearchAccountId picked; handleAskAi below passes its own resolved
  // accountId (e.g. a "@account" scoped pick) the same way. ClusterDropDown
  // (mounted in the header alongside this component) self-heals the URL back
  // toward its own selectedCluster whenever its local clusterValue is unset —
  // which happens on first mount and on any hard/first navigation for a
  // session. Left alone, that race clobbers the account we just navigated to
  // (and drops the #hash) as soon as ClusterDropDown's effect runs. Syncing
  // selectedCluster + the persisted preferences here, before the push, makes
  // that effect a no-op instead.
  const navigateToSearchResult = useCallback(
    (path, accountId) => {
      if (accountId && accountId !== selectedCluster?.value) {
        const targetCluster = allCluster?.find((c) => c.value === accountId);
        if (targetCluster) {
          setSelectedCluster(targetCluster);
          apiUser.storeUserPreferences(PREFERENCE_LAST_ACCOUNT_ID, accountId);
          if (targetCluster.cloud_provider) {
            apiUser.setLastAccountIdForProvider(targetCluster.cloud_provider, accountId, data?.tenant?.id);
          }
        }
      }
      router.push(path);
    },
    [router, allCluster, selectedCluster, setSelectedCluster, data?.tenant?.id]
  );

  // Result rows: leading `icon` is the parent page/provider's icon (not a
  // per-tab icon), `label` is the tab's title, and the right-aligned `type`
  // chip shows the path — mirrors a "Go to..." command-palette layout (icon
  // · title · path). `value` is the full path — unique per option, since
  // `label` collides across groups (e.g. "Summary" appears under Optimize
  // and under every cloud service).
  //
  // Two things keep this cheap even though it's built from ~200 rows:
  //  1. Lazy — hasOpenedSearch stays false (and the provider memos below
  //     short-circuit to []) until the user actually opens the search box,
  //     so the header (rendered on every page) never pays this cost for a
  //     search box most page views never open.
  //  2. Once opened, each provider's rows are memoized independently, keyed
  //     only on that provider's own resolved account id — switching e.g. the
  //     active K8s cluster only rebuilds the ~47 K8s rows, not all ~200 rows
  //     across every provider (which a single combined useMemo would do,
  //     since any one of the four account ids changing invalidates it).
  //
  // Counted rather than flagged: the static rows only care that the box has
  // been opened at least once (hasOpenedSearch below), but the dashboard fetch
  // needs to re-run on *each* open, and a counter gives both off one state.
  const [searchOpenSeq, setSearchOpenSeq] = useState(0);
  const hasOpenedSearch = searchOpenSeq > 0;
  // Top-3 most-recently-selected search results for this tenant, re-read from
  // localStorage on every open (not just once) so a pick made in another tab
  // shows up here too. Only the `value` (path) is persisted — re-resolved
  // against the live navSearchItems below — so a stale/renamed/removed page
  // is silently dropped instead of rendering a broken row.
  const [recentSearchValues, setRecentSearchValues] = useState([]);

  // "@account" picker rows — every connected account across all providers
  // (minus the synthetic demo account and any provider with no detail-page
  // search integration, which would otherwise be a scoped dead end). Each
  // row's searchText carries its own '@' prefix so the ranking logic below
  // (which matches typed text against label/searchText) filters this list
  // against "@partial-name" for free — matching from right after the '@',
  // i.e. prefix-style, same as mention pickers elsewhere (Slack/GitHub).
  // Computed up front (not lazily under hasOpenedSearch like the per-provider
  // lists below) because mentionMode itself needs its length before the
  // search box has necessarily opened.
  const accountMentionOptions = useMemo(() => {
    if (!allCluster) {
      return [];
    }
    const eligible = allCluster.filter((c) => c.value !== 'demo' && SCOPED_SEARCH_PROVIDER_CONFIG[c.cloud_provider?.toUpperCase()]);
    return sortAccountsLikeClusterDropdown(eligible).map((c) => ({
      label: c.label,
      icon: <CloudProviderIcon cloud_provider={c.cloud_provider} width='16px' height='16px' />,
      value: c.value,
      cloud_provider: c.cloud_provider,
      searchText: `@${c.label}`,
    }));
  }, [allCluster]);
  // Whether the "@" feature has anything to offer at all — while this is
  // false (no accounts yet loaded, or none eligible for scoping) the whole
  // feature stays dormant: no mode switch on '@', no placeholder hint, same
  // as if it didn't exist. Prevents advertising/entering a mode with an
  // empty picker.
  const hasMentionAccounts = accountMentionOptions.length > 0;
  // "@account" scoping — typing '@' as the first character swaps the search
  // box's option list to a picker over every connected account; selecting one
  // sets scopedAccount and re-scopes results to just that account's provider
  // pages. scopedAccount is cleared both by backspacing an empty box and by
  // closing the popover — each reopen starts from the unscoped state. `search`
  // mirrors the search box's own typed text (it's the InputBase's `value`), so
  // mentionMode can be derived from it instead of tracked as its own flag —
  // that avoids a whole class of desync bugs between a boolean and the text
  // it's supposed to reflect.
  const [scopedAccount, setScopedAccount] = useState(null);
  const [search, setSearch] = useState('');
  const mentionMode = !scopedAccount && hasMentionAccounts && search.startsWith('@');
  // The tenant's dashboards, offered as results alongside pages. Re-read on
  // every open (like the recent-search list above, and for the same reason:
  // one created, renamed or deleted meanwhile should be reflected on the next
  // open, not on the next full page load) but never per keystroke — filtering
  // happens client-side, through the same ranking every other row goes
  // through. A failure leaves the list empty and is only logged: page search
  // must keep working for a user whose tenant has no dashboards, or who may
  // not read them.
  const [dashboards, setDashboards] = useState([]);
  useEffect(() => {
    if (!searchOpenSeq) {
      return undefined;
    }
    let active = true;
    apiDashboards
      .listDashboardsBrief({ limit: DASHBOARD_SEARCH_FETCH_LIMIT })
      .then((res) => {
        if (!active) {
          return;
        }
        // apiDashboards resolves with {data, errors} rather than throwing, so a
        // GraphQL-level failure (a 403 on dashboards, say) arrives here with
        // data null — checked explicitly so it's logged rather than read as
        // "this tenant has no dashboards". Either way the previous list and the
        // stored recents are left untouched: nothing below runs.
        if (res?.errors) {
          console.error('Failed to fetch dashboards for search:', res.errors);
          return;
        }
        if (!res?.data) {
          return;
        }
        setDashboards(res.data);
        // A recent pick whose dashboard has since been deleted already stops
        // rendering (resolveRecentOption can't match it any more), but the
        // stored value would sit in the tenant's three-slot list forever. Drop
        // it here, where a full listing is in hand to judge against.
        //
        // Only when the response is short of the page size: a full page means
        // there may be more dashboards we haven't seen, and a value missing
        // from a partial list is no proof the dashboard is gone. Recents for
        // pages/accounts are left alone — those resolve against lists that
        // load separately, so "unresolvable" there can just mean "not loaded
        // yet".
        if (res.data.length >= DASHBOARD_SEARCH_FETCH_LIMIT) {
          return;
        }
        const live = new Set(res.data.map((dashboard) => dashboardSearchPath(dashboard.id)));
        const stale = apiUser.getRecentPageSearches(data?.tenant?.id).filter((value) => DASHBOARD_SEARCH_PATH_RE.test(value) && !live.has(value));
        if (stale.length) {
          setRecentSearchValues(apiUser.removeRecentPageSearches(stale, data?.tenant?.id));
        }
      })
      .catch((err) => {
        console.error('Failed to fetch dashboards for search:', err);
      });
    return () => {
      active = false;
    };
  }, [searchOpenSeq, data?.tenant?.id]);

  const dashboardSearchItems = useMemo(() => navSearchDashboardItems(dashboards), [dashboards]);

  // The tenant's automations, offered as results alongside dashboards/pages —
  // same reload-on-every-open reasoning as the dashboards fetch above.
  const [workflows, setWorkflows] = useState([]);
  useEffect(() => {
    if (!searchOpenSeq) {
      return undefined;
    }
    let active = true;
    apiWorkflow
      .listWorkflows(undefined, undefined, undefined, undefined, WORKFLOW_SEARCH_FETCH_LIMIT)
      .then((res) => {
        if (!active) {
          return;
        }
        if (res?.errors) {
          console.error('Failed to fetch automations for search:', res.errors);
          return;
        }
        const workflowList = res?.data?.workflow_list;
        if (!workflowList) {
          return;
        }
        const workflows = workflowList.workflows || [];
        setWorkflows(workflows);
        // Unlike dashboards, workflow_list reports total_count directly, so
        // completeness (needed before pruning a stale recent) is judged
        // against that instead of a short-response guess.
        if (workflows.length < (workflowList.total_count ?? 0)) {
          return;
        }
        const live = new Set(workflows.map((workflow) => workflowSearchPath(workflow.id, workflow.account_id)));
        const stale = apiUser.getRecentPageSearches(data?.tenant?.id).filter((value) => WORKFLOW_SEARCH_PATH_RE.test(value) && !live.has(value));
        if (stale.length) {
          setRecentSearchValues(apiUser.removeRecentPageSearches(stale, data?.tenant?.id));
        }
      })
      .catch((err) => {
        console.error('Failed to fetch automations for search:', err);
      });
    return () => {
      active = false;
    };
  }, [searchOpenSeq, data?.tenant?.id]);

  const workflowSearchItems = useMemo(() => navSearchWorkflowItems(workflows, allCluster), [workflows, allCluster]);

  const k8sNavItems = useMemo(
    () => (hasOpenedSearch ? navSearchProviderItems(k8sDetailsSearchFragments, 'K8s', k8sSearchAccountId, '/kubernetes/details') : []),
    [hasOpenedSearch, k8sSearchAccountId]
  );
  const awsNavItems = useMemo(
    () => (hasOpenedSearch ? navSearchProviderItems(awsDetailsSearchFragments, 'AWS', awsSearchAccountId, '/cloud-account/details') : []),
    [hasOpenedSearch, awsSearchAccountId]
  );
  const azureNavItems = useMemo(
    () => (hasOpenedSearch ? navSearchProviderItems(azureDetailsSearchFragments, 'Azure', azureSearchAccountId, '/cloud-account/details') : []),
    [hasOpenedSearch, azureSearchAccountId]
  );
  const gcpNavItems = useMemo(
    () => (hasOpenedSearch ? navSearchProviderItems(gcpDetailsSearchFragments, 'GCP', gcpSearchAccountId, '/cloud-account/details') : []),
    [hasOpenedSearch, gcpSearchAccountId]
  );
  // LLM_ANALYSER is a per-tenant feature flag, so it can only be resolved
  // client-side — same pattern as optimise/index.jsx, which owns the tab this
  // search entry jumps to. Fails closed until it resolves.
  const [llmAnalyserEnabled, setLlmAnalyserEnabled] = useState(false);
  useEffect(() => {
    hasFeatureAccess('LLM_ANALYSER')
      .then(setLlmAnalyserEnabled)
      .catch(() => setLlmAnalyserEnabled(false));
  }, []);

  // Same gate the sidebar's own "Admin" nav item uses (layout/index.jsx) — a
  // user who can't see that nav entry shouldn't find its pages (Users,
  // Groups, Audits, Notifications, Integrations, Ownership) via search either.
  const canAccessAdmin = hasReadAccess();
  // Same gate the Automation page's own Task Runner tab uses (automation/index.jsx's
  // isAdmin) — only tenant_admin/account_admin can see that tab there, so a
  // non-admin shouldn't be able to jump to it via search either.
  const canAccessTaskRunner = !!(data?.roles?.includes('tenant_admin') || data?.roles?.includes('account_admin'));
  // Same gate user-management's Billing tab uses (billingFilter.tsx's
  // shouldShow) — it's only registered into userManagementFilters(session) for
  // SaaS-tier tenants, so a non-SaaS tenant's search shouldn't offer it either
  // (canAccessAdmin alone isn't enough — that only checks nav-level admin access).
  const canAccessBilling = data?.tier === 'saas';
  // Same gate the Roles tab's own registration uses (RolePermissions.jsx's
  // shouldShow): the CUSTOM_ROLES feature must be on, and the user must hold a
  // tenant-wide role or the delegated customroles:Read grant. canAccessAdmin
  // alone isn't enough — it only checks nav-level admin access, so an Audits-only
  // reviewer would otherwise get a row leading to a tab that isn't there.
  const canAccessRoles = !!(
    data?.customRolesEnabled &&
    (data?.roles?.includes('tenant_admin') ||
      data?.roles?.includes('tenant_admin_readonly') ||
      data?.isSuperAdmin ||
      data?.permissions?.includes('customroles:Read'))
  );
  // Same two gates optimise/index.jsx's own LLM Analyser/AI Gateway tabs use.
  // They are different KINDS of gate: LLM_ANALYSER is a per-tenant feature flag
  // (resolved asynchronously, hence the state above), while AI Gateway is still
  // a per-deployment UI_ENABLE_LLM_GATEWAY env var read off the session by
  // isUiFeatureEnabled. Both are then narrowed by
  // hasReadAccess(selectedCluster?.value), mirroring the page's per-account check.
  const canAccessLlmAnalyser = llmAnalyserEnabled && hasReadAccess(selectedCluster?.value);
  // The `llm:Read` disjunct mirrors the page too: the gateway usage API is
  // tenant-scoped, so hasReadAccess (per-account) never sees that grant and the
  // row went missing for grant holders. Keep in lockstep with optimise/index.jsx.
  const canAccessAiGateway = isUiFeatureEnabled('llmGateway') && (hasReadAccess(selectedCluster?.value) || hasPermission('llm', 'Read'));

  // Provider-agnostic "some account" resolution — used by Automation/Agent
  // Health's search entries (no K8s/AWS/Azure/GCP concept to key
  // resolveSearchAccountId's per-provider cache off of) and by handleAskAi's
  // own accountId fallback
  // chain below. Falls back in the same order ClusterDropDown.jsx uses when it
  // resolves an account with no prior URL/selectedCluster to go on: the
  // cached last account (any provider), else the first connected account.
  const defaultAccountId = useMemo(() => {
    if (selectedCluster?.value) {
      return selectedCluster.value;
    }
    const cachedId = apiUser.getUserPreferences()?.[PREFERENCE_LAST_ACCOUNT_ID];
    if (cachedId && allCluster?.some((c) => c.value === cachedId)) {
      return cachedId;
    }
    return allCluster?.[0]?.value || null;
  }, [selectedCluster, allCluster]);

  // Whether a static nav-search row should be visible to the current user —
  // shared by the live result list below and resolveRecentOption further
  // down, so a role gate only has to be expressed once.
  const isNavSearchItemVisible = useCallback(
    (opt) => {
      if (opt.group === 'Admin' && !canAccessAdmin) {
        return false;
      }
      if (opt.label === 'Task Runner' && !canAccessTaskRunner) {
        return false;
      }
      if (opt.label === 'Billing' && !canAccessBilling) {
        return false;
      }
      if (opt.label === 'Roles' && !canAccessRoles) {
        return false;
      }
      if (opt.label === 'LLM Analyser' && !canAccessLlmAnalyser) {
        return false;
      }
      if (opt.label === 'AI Gateway' && !canAccessAiGateway) {
        return false;
      }
      return true;
    },
    [canAccessAdmin, canAccessTaskRunner, canAccessBilling, canAccessRoles, canAccessLlmAnalyser, canAccessAiGateway]
  );

  const accountScopedNavItems = useMemo(
    () => (hasOpenedSearch ? navSearchAccountScopedItems(accountScopedSearchFragments, defaultAccountId) : []),
    [hasOpenedSearch, defaultAccountId]
  );

  // No account passed: the plain rows point at the tenant-level listing. Only
  // the "@account" scoped list below pre-seeds one.
  const automationNavItems = useMemo(() => (hasOpenedSearch ? navSearchAutomationItems(automationSearchFragments) : []), [hasOpenedSearch]);

  const navSearchItems = useMemo(
    () => [
      ...navSearchStaticItems.filter(isNavSearchItemVisible),
      ...automationNavItems.filter(isNavSearchItemVisible),
      ...accountScopedNavItems.filter(isNavSearchItemVisible),
      ...k8sNavItems,
      ...awsNavItems,
      ...azureNavItems,
      ...gcpNavItems,
    ],
    [isNavSearchItemVisible, automationNavItems, accountScopedNavItems, k8sNavItems, awsNavItems, azureNavItems, gcpNavItems]
  );

  // Kept out of navSearchItems/Suggested Pages so Integrations gets its own
  // section, same as Dashboards/Automations.
  const integrationSearchOptions = useMemo(() => navSearchIntegrationItems.filter(isNavSearchItemVisible), [isNavSearchItemVisible]);

  // Re-resolves one recent value into a full display option, checked against
  // allCluster directly rather than navSearchItems (built for only the
  // currently-resolved account) — a recent whose account no longer exists
  // drops silently, same as a renamed/removed static page already does.
  const resolveRecentOption = useCallback(
    (value) => {
      const staticMatch = navSearchStaticItems.find((opt) => opt.value === value) || navSearchIntegrationItems.find((opt) => opt.value === value);
      if (staticMatch) {
        return isNavSearchItemVisible(staticMatch) ? staticMatch : null;
      }
      if (DASHBOARD_SEARCH_PATH_RE.test(value)) {
        return dashboardSearchItems.find((opt) => opt.value === value) || null;
      }
      const workflowMatch = WORKFLOW_SEARCH_PATH_RE.exec(value);
      if (workflowMatch) {
        const [, , accountId] = workflowMatch;
        if (!allCluster?.some((c) => c.value === accountId)) {
          return null;
        }
        return workflowSearchItems.find((opt) => opt.value === value) || null;
      }
      const automationMatch = AUTOMATION_SCOPED_SEARCH_PATH_RE.exec(value);
      if (automationMatch) {
        const [, accountId] = automationMatch;
        // Only an account-scoped pick needs the account to still exist; the
        // tenant-level row is always resolvable.
        if (accountId && !allCluster?.some((c) => c.value === accountId)) {
          return null;
        }
        const match = navSearchAutomationItems(automationSearchFragments, accountId).find((opt) => opt.value === value);
        return match && isNavSearchItemVisible(match) ? match : null;
      }
      const accountScopedQueryMatch = ACCOUNT_SCOPED_QUERY_SEARCH_PATH_RE.exec(value);
      if (accountScopedQueryMatch) {
        const [, accountId] = accountScopedQueryMatch;
        if (!allCluster?.some((c) => c.value === accountId)) {
          return null;
        }
        const match = navSearchAccountScopedItems(accountScopedSearchFragments, accountId).find((opt) => opt.value === value);
        return match && isNavSearchItemVisible(match) ? match : null;
      }
      const match = ACCOUNT_SCOPED_SEARCH_PATH_RE.exec(value);
      if (!match) {
        return null;
      }
      const [, accountId] = match;
      const account = allCluster?.find((c) => c.value === accountId);
      if (!account) {
        return null;
      }
      const config = SCOPED_SEARCH_PROVIDER_CONFIG[account.cloud_provider?.toUpperCase()];
      if (!config) {
        return null;
      }
      return navSearchProviderItems(config.fragments, config.label, accountId, config.basePath).find((opt) => opt.value === value);
    },
    [allCluster, isNavSearchItemVisible, dashboardSearchItems, workflowSearchItems]
  );

  // Recent picks, captioned "Recents". A recent pick intentionally still
  // appears in its own section further down too (as a separate option copy),
  // not just here.
  //
  // Recent rows additionally get `accountName`/`cloud_provider` stamped on
  // (when the recent pick carries an accountId) so OptionItem can render an
  // account-name chip — a recent value's account isn't shown anywhere else
  // in the row, and (unlike a provider's current resolved account) isn't
  // necessarily the provider's current one — it's whatever account the user
  // was actually in when they picked it.
  const recentSearchOptions = useMemo(
    () =>
      recentSearchValues
        .map(resolveRecentOption)
        .filter(Boolean)
        .map((opt) => {
          const account = opt.accountId ? allCluster?.find((c) => c.value === opt.accountId) : null;
          return { ...opt, sectionLabel: 'Recents', accountName: account?.label, cloud_provider: account?.cloud_provider };
        }),
    [recentSearchValues, resolveRecentOption, allCluster]
  );

  const suggestedPageOptions = useMemo(() => navSearchItems.map((opt) => ({ ...opt, sectionLabel: 'Suggested Pages' })), [navSearchItems]);

  // Once an account is picked, results are scoped to it — provider detail
  // pages, Automation/Agent Health tabs, and this account's own automations,
  // split into two sections: "Automations" (real workflows only, matching how
  // the unscoped list treats the term — the Automations/Task Runner/Executions
  // TAB rows go under "Suggested Pages" instead, same as they land there
  // unscoped) then "Suggested Pages". Order matters: each label must stay one
  // contiguous run or it'll split into two captions.
  const scopedSearchItems = useMemo(() => {
    if (!scopedAccount) {
      return [];
    }
    const config = SCOPED_SEARCH_PROVIDER_CONFIG[scopedAccount.cloud_provider?.toUpperCase()];
    const providerItems = (config ? navSearchProviderItems(config.fragments, config.label, scopedAccount.value, config.basePath) : []).map((opt) => ({
      ...opt,
      sectionLabel: 'Suggested Pages',
    }));
    const accountScopedItems = navSearchAccountScopedItems(accountScopedSearchFragments, scopedAccount.value)
      .filter(isNavSearchItemVisible)
      .map((opt) => ({ ...opt, sectionLabel: 'Suggested Pages' }));
    const automationItems = navSearchAutomationItems(automationSearchFragments, scopedAccount.value)
      .filter(isNavSearchItemVisible)
      .map((opt) => ({ ...opt, sectionLabel: 'Suggested Pages' }));
    // sectionLabel: 'Automations' already set by navSearchWorkflowItems.
    const scopedWorkflowItems = workflowSearchItems
      .filter((opt) => opt.accountId === scopedAccount.value)
      .map((opt) => ({ ...opt, accountName: undefined, cloud_provider: undefined }));
    return [...scopedWorkflowItems, ...providerItems, ...automationItems, ...accountScopedItems];
  }, [scopedAccount, isNavSearchItemVisible, workflowSearchItems]);

  // The full (unfiltered) option list for whichever mode is active — mirrors
  // ds/FilterDropdown.jsx's `options` prop. Five peer sections (opt.sectionLabel):
  // Recents, Dashboards, Automations, Integrations, Suggested Pages — each
  // renders only when it has rows. Dashboards/Automations/Integrations sit
  // above Suggested Pages (~200 rows) so they aren't buried below it.
  //
  // Dashboards and Integrations stay out of the "@account" scoped list —
  // neither is tied to one connected account. Automations IS, so it's
  // included there too, under scopedSearchItems' own two-section split.
  const searchBoxOptions = useMemo(() => {
    if (mentionMode) {
      return accountMentionOptions;
    }
    if (scopedAccount) {
      return scopedSearchItems;
    }
    return [...recentSearchOptions, ...dashboardSearchItems, ...workflowSearchItems, ...integrationSearchOptions, ...suggestedPageOptions];
  }, [
    mentionMode,
    accountMentionOptions,
    scopedAccount,
    scopedSearchItems,
    recentSearchOptions,
    dashboardSearchItems,
    workflowSearchItems,
    integrationSearchOptions,
    suggestedPageOptions,
  ]);

  // Filters searchBoxOptions by `search`. Supports glob wildcards `*` (any
  // sequence) and `?` (single char) — useful for long index/label lists.
  // Plain queries keep case-insensitive substring semantics, ranked so an
  // exact/prefix name match surfaces above a row that only matches as a
  // substring or via `searchText` (e.g. "services-server" should show the
  // exact "services-server" row above "nudgebee-services-server"). An exact
  // hit on the option's own pathAcronym short form (e.g. "umu" for
  // user-management/users) ranks just below an exact label match and above
  // every plain prefix/substring tier, since typing the acronym is a
  // deliberate, unambiguous shorthand rather than an incidental substring
  // hit. Below that, two more permissive tiers handle multi-word and mis-typed queries: the
  // query is split on whitespace and every token must match *somewhere* in
  // the haystack, independent of order (so "rds aws" finds "AWS RDS
  // Summary" same as "aws rds" would) — a token that fails a substring check
  // falls back to a small edit-distance match against individual haystack
  // words (see fuzzyTokenMatches in @lib/navSearchPages), so e.g. "servics"
  // still finds "AWS Services". Ranking is scoped to each contiguous
  // sectionLabel run, not the whole array: a global sort would let a
  // well-matching "Suggested Pages" row sort ahead of a weaker-matching "Recents"
  // row, splitting the "Recents" run in two and firing its caption a second
  // time further down (startsNewSection re-triggers on every re-entry into a
  // label).
  const filteredOptions = useMemo(() => {
    const rawQuery = search.trim();
    // In mention mode every candidate's searchText is `@label` and the typed
    // text is guaranteed to start with '@' (that's what gates mentionMode
    // itself) — stripping it before matching means that guaranteed character
    // doesn't count against the token below, in particular the fuzzy
    // fallback's edit-distance budget, which would otherwise spend part of
    // its typo tolerance accounting for the '@' instead of an actual typo.
    const q = mentionMode ? rawQuery.slice(1).trimStart() : rawQuery;
    if (!q) {
      return searchBoxOptions;
    }
    const haystack = (opt) => `${opt?.label ?? ''}${opt?.searchText ? ` ${opt.searchText}` : ''}`;
    const hasWildcard = /[*?]/.test(q);
    if (hasWildcard) {
      const escaped = q
        .replace(/[.+^${}()|[\]\\]/g, '\\$&')
        .replace(/\*/g, '.*')
        .replace(/\?/g, '.');
      try {
        const re = new RegExp(escaped, 'i');
        return searchBoxOptions.filter((opt) => re.test(haystack(opt)));
      } catch {
        // Fall through to substring match on regex compile failure.
      }
    }
    const lower = q.toLowerCase();
    const segments = [];
    searchBoxOptions.forEach((opt) => {
      const label = opt?.sectionLabel;
      const last = segments[segments.length - 1];
      if (last && last.label === label) {
        last.items.push(opt);
      } else {
        segments.push({ label, items: [opt] });
      }
    });
    const queryTokens = lower.split(/\s+/).filter(Boolean);
    const rankOf = (opt) => {
      const label = (opt?.label ?? '').toLowerCase();
      const extra = opt?.searchText ? String(opt.searchText).toLowerCase() : '';
      const full = extra ? `${label} ${extra}` : label;
      if (label === lower) return 0;
      // An exact hit on the option's own pathAcronym (e.g. "umu" for
      // user-management/users) is a deliberate, unambiguous shorthand the
      // user typed on purpose — rank it above a mere label prefix/substring
      // match instead of letting it fall in with every other searchText hit
      // (tier 4 below), where it could tie with an unrelated option that
      // just happens to contain "umu" as a substring somewhere.
      if (opt?.acronym && opt.acronym === lower) return 1;
      if (label.startsWith(lower)) return 2;
      if (label.includes(lower)) return 3;
      if (full.includes(lower)) return 4;

      // Neither the whole query nor a single-token query matched as a
      // substring — try per-token AND matching, falling back to fuzzy only
      // for the tokens that don't substring-match. A single fully-unmatched
      // token is tolerated (ranked below a complete match) once the query has
      // 3+ tokens — enough remaining tokens still constrain the match — but a
      // 1-2 token query stays strict AND, since tolerating a miss there would
      // let a single word carry the whole match (e.g. "aws rds" surfacing any
      // page that merely mentions "aws"). A second miss always excludes.
      let usedFuzzy = false;
      let missedCount = 0;
      let words = null;
      for (const token of queryTokens) {
        if (full.includes(token)) continue;
        if (token.length >= MIN_FUZZY_TOKEN_LENGTH) {
          words = words ?? wordsOf(full);
          if (fuzzyTokenMatches(token, words)) {
            usedFuzzy = true;
            continue;
          }
        }
        missedCount += 1;
        if (missedCount > 1 || queryTokens.length < 3) {
          return null;
        }
      }
      if (missedCount === 1) return 7;
      return usedFuzzy ? 6 : 5;
    };
    return segments.flatMap(({ items }) => {
      const ranked = [];
      items.forEach((opt, i) => {
        const rank = rankOf(opt);
        if (rank === null) return;
        ranked.push({ opt, rank, i });
      });
      ranked.sort((a, b) => a.rank - b.rank || a.i - b.i);
      return ranked.map((r) => r.opt);
    });
  }, [searchBoxOptions, search, mentionMode]);

  // Expanded categories, keyed by sectionLabel (survives refining the search
  // text while a category stays open). Reset to collapsed on popover close, below.
  const [expandedSections, setExpandedSections] = useState(() => new Set());
  const toggleSection = useCallback((label) => {
    setExpandedSections((prev) => {
      const next = new Set(prev);
      if (next.has(label)) {
        next.delete(label);
      } else {
        next.add(label);
      }
      return next;
    });
  }, []);

  // True per-category counts (from filteredOptions, not the truncated
  // displayedOptions) — SectionCaption needs the real total to decide
  // whether a chevron is warranted, independent of collapse state.
  const sectionCounts = useMemo(() => {
    const counts = {};
    filteredOptions.forEach((opt) => {
      if (!opt?.sectionLabel) {
        return;
      }
      counts[opt.sectionLabel] = (counts[opt.sectionLabel] || 0) + 1;
    });
    return counts;
  }, [filteredOptions]);

  // What's actually rendered/navigated: filteredOptions with each category
  // capped unless expanded. Assumes each sectionLabel is one contiguous run
  // (true per the ranking above), so a single left-to-right pass suffices.
  const displayedOptions = useMemo(() => {
    const result = [];
    let i = 0;
    while (i < filteredOptions.length) {
      const label = filteredOptions[i]?.sectionLabel;
      let j = i + 1;
      while (j < filteredOptions.length && filteredOptions[j]?.sectionLabel === label) {
        j += 1;
      }
      const run = filteredOptions.slice(i, j);
      const capped = !label || run.length <= MAX_SECTION_ROWS || expandedSections.has(label) ? run : run.slice(0, MAX_SECTION_ROWS);
      result.push(...capped);
      i = j;
    }
    return result;
  }, [filteredOptions, expandedSections]);

  // Arrow-key-navigable list: displayedOptions with a synthetic toggle entry
  // spliced in front of each collapsible section (only those — a section with
  // 5 or fewer rows has nothing to reveal, so its caption stays un-navigable).
  // Enter on a toggle entry collapses/expands instead of selecting.
  const navigableItems = useMemo(() => {
    const result = [];
    let i = 0;
    while (i < displayedOptions.length) {
      const label = displayedOptions[i]?.sectionLabel;
      let j = i + 1;
      while (j < displayedOptions.length && displayedOptions[j]?.sectionLabel === label) {
        j += 1;
      }
      if (label && (sectionCounts[label] ?? 0) > MAX_SECTION_ROWS) {
        result.push({ __sectionToggle: true, sectionLabel: label });
      }
      result.push(...displayedOptions.slice(i, j));
      i = j;
    }
    return result;
  }, [displayedOptions, sectionCounts]);

  const searchPlaceholder = scopedAccount
    ? `Search for ${scopedAccount.label}…`
    : hasMentionAccounts
    ? 'Search pages/dashboard or just ask anything… (type @ for an account)'
    : 'Search pages or dashboards…';

  // Only offered once a typed query has actually come up empty, and never in
  // mention mode — picking an account, not asking a question, is that mode's
  // only action (see GlobalSearchFooterHints' own mentionMode guard above).
  const askAiEmptyQuery = !mentionMode && filteredOptions.length === 0 && search.trim() ? search.trim() : null;

  const handleBackspaceWhenEmpty = useCallback(() => {
    if (scopedAccount) {
      setScopedAccount(null);
    }
  }, [scopedAccount]);

  // --- Trigger/popover state (mirrors ds/FilterDropdown.jsx's own) ---
  const [anchorEl, setAnchorEl] = useState(null);
  // Tracked by identity (navItemKey), not a raw index — see navItemKey's own
  // comment for why. highlightedIndex is derived by re-locating that key in
  // the current navigableItems on every render; -1 (not found / no key) means
  // "nothing highlighted", same meaning the old index-based state had.
  const [highlightedKey, setHighlightedKey] = useState(null);
  const highlightedIndex = useMemo(
    () => (highlightedKey === null ? -1 : navigableItems.findIndex((item) => navItemKey(item) === highlightedKey)),
    [highlightedKey, navigableItems]
  );
  const [askingAi, setAskingAi] = useState(false);
  const searchRef = useRef(null);
  const triggerRef = useRef(null);
  const open = Boolean(anchorEl);

  // A pick either sets the "@account" scope (mention mode — popover stays
  // open so the newly-scoped results load in place) or navigates away
  // (normal mode — popover closes, matching ds/FilterDropdown's
  // `closeOnSelect` behavior for everything but the mention picker).
  const handleOptionSelect = useCallback(
    (option) => {
      if (mentionMode) {
        if (!option) {
          return;
        }
        setScopedAccount({ value: option.value, cloud_provider: option.cloud_provider, label: option.label });
        setSearch('');
        return;
      }
      if (!option?.path) {
        return;
      }
      apiUser.addRecentPageSearch(option.value, data?.tenant?.id);
      setRecentSearchValues(apiUser.getRecentPageSearches(data?.tenant?.id));
      navigateToSearchResult(option.path, option.accountId);
      setAnchorEl(null);
    },
    [mentionMode, navigateToSearchResult, data?.tenant?.id]
  );

  // "Ask {assistantName}" — the search box's AI hand-off. Reuses the same
  // aiGenerateInvestigate + session_id flow the home page's own "Ask nubi" input
  // already drives (home/index.jsx's handleGenerateInvestigation): create a
  // session, submit the typed text as the first question, then land on that
  // conversation. An empty query (persistent footer button with nothing typed)
  // just opens a fresh chat instead of submitting nothing. No resolvable account
  // (e.g. before any cluster/account has loaded) falls back to a bare /ask-nudgebee
  // rather than silently doing nothing.
  const handleAskAi = useCallback(
    async (rawQuery) => {
      if (askingAi) {
        return;
      }
      const query = (rawQuery || '').trim();
      // A deliberate "@account" pick takes priority over the page's own account
      // context — the user explicitly scoped the search to it, so the question
      // should go there too, not wherever the current page happens to be.
      const accountId =
        scopedAccount?.value ||
        router?.query?.accountId ||
        router?.query?.KubernetesDetails ||
        router?.query?.CloudAccountDetails ||
        defaultAccountId;
      if (!query || !accountId) {
        setAnchorEl(null);
        setSearch('');
        const path = accountId ? `/ask-nudgebee?accountId=${accountId}` : '/ask-nudgebee';
        navigateToSearchResult(path, accountId);
        return;
      }
      setAskingAi(true);
      const sessionId = uuidv4();
      try {
        const res = await apiAskNudgebee.aiGenerateInvestigate({ account_id: accountId, query, session_id: sessionId });
        const response = res?.data?.data?.ai_execute_investigation ?? {};
        if (!response?.data?.query) {
          snackbar.error("Can't process your request right now.");
          return;
        }
        setAnchorEl(null);
        setSearch('');
        const path = `/ask-nudgebee?accountId=${accountId}&session_id=${sessionId}`;
        navigateToSearchResult(path, accountId);
      } catch (error) {
        console.error('Failed to start AI investigation:', error);
        snackbar.error("Can't process your request right now.");
      } finally {
        setAskingAi(false);
      }
    },
    [askingAi, router, defaultAccountId, scopedAccount, navigateToSearchResult]
  );

  // Fires once per popover open, before it actually opens: lazily builds the
  // per-provider result rows on first open (see hasOpenedSearch above),
  // re-reads the tenant's dashboards, and refreshes the recent-searches list
  // (a pick made in another tab should show up here too).
  // Read the current route through a ref so openSearch keeps a stable
  // identity — it is a dependency of the Ctrl/Cmd+K listener effect below,
  // which would otherwise re-attach on every navigation.
  const pagePathRef = useRef(router.pathname);
  useEffect(() => {
    pagePathRef.current = router.pathname;
  }, [router.pathname]);

  // `source` distinguishes the trigger button from the Ctrl/Cmd+K shortcut
  // below — a shortcut open produces no DOM click, so a Pendo click-tagged
  // Feature on #auto-complete-global-page-search cannot see it at all.
  const openSearch = useCallback(
    (target, source = 'click') => {
      setSearchOpenSeq((seq) => seq + 1);
      setRecentSearchValues(apiUser.getRecentPageSearches(data?.tenant?.id));
      setAnchorEl(target);
      trackProductEvent('global_search_opened', { source, page: pagePathRef.current });
    },
    [data?.tenant?.id]
  );

  // Keyboard shortcut: Cmd/Ctrl + K toggles the global page search popover.
  // Skipped while a MUI Dialog-based modal (e.g. Create Ticket, K8s/Jira/Github
  // account modals) is open — those render role="dialog" and sit above the
  // header, so toggling the search behind them would be invisible and steal
  // the shortcut from whatever the modal itself wants to do with it. Also
  // skipped while the AI chat sidebar (NubiChatSidebar) is open — it isn't a
  // role="dialog" but owns the same Cmd/Ctrl+K shortcut itself (to close the
  // chat), so both would otherwise fire off one keypress.
  useEffect(() => {
    const handleGlobalKeyDown = (e) => {
      // e.key.toLowerCase() (not a bare 'k' check) so Caps Lock or a Shift
      // chord — which report e.key as 'K' — still match on both Mac (metaKey)
      // and Windows/Linux (ctrlKey).
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        if (document.querySelector('[role="dialog"], [data-nubi-chat-open]')) {
          return;
        }
        // Close whatever dropdown/menu/select popover is already open first, so
        // Ctrl+K doesn't just stack the page search on top of it. MUI Popover /
        // Menu / Select all close on Escape, and MUI autofocuses into a
        // popover's content when it opens, so document.activeElement sits
        // inside whichever one is currently open — dispatching a real Escape
        // keydown there reaches its own close handler. A no-op when nothing
        // is open.
        document.activeElement?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
        e.preventDefault();
        if (anchorEl) {
          setAnchorEl(null);
        } else {
          openSearch(triggerRef.current, 'shortcut');
        }
      }
    };
    window.addEventListener('keydown', handleGlobalKeyDown);
    return () => window.removeEventListener('keydown', handleGlobalKeyDown);
  }, [anchorEl, openSearch]);

  // Discard the keyboard highlight on a genuinely new result set (search
  // text, mode switch) or when the panel closes. Safe to leave alone on a
  // mere expand/collapse toggle — highlightedIndex re-locates highlightedKey
  // in the current navigableItems on every render, so it already follows the
  // same highlighted entry (or clears itself if that entry no longer exists)
  // regardless of how much everything after it shifted.
  useEffect(() => {
    setHighlightedKey(null);
  }, [filteredOptions, open]);

  // wasOpenRef (not a dep) tracks the previous `open` value so the
  // scopedAccount reset fires exactly once per real open→closed transition,
  // regardless of how many setAnchorEl(null) call sites there are (Escape,
  // outside click, a pick, the Cmd/Ctrl+K toggle).
  const wasOpenRef = useRef(false);
  useEffect(() => {
    if (!open) {
      setSearch('');
      if (wasOpenRef.current) {
        // Drops the account-mention chip once the popover actually closes,
        // so a scoped search doesn't linger into the next unrelated session.
        setScopedAccount(null);
        setExpandedSections(new Set());
      }
      wasOpenRef.current = false;
      return;
    }
    wasOpenRef.current = true;
    // autoFocus on InputBase is unreliable inside MUI Popover — the popover
    // reclaims focus after mounting. Explicitly focus after the open transition.
    const timer = setTimeout(() => searchRef.current?.focus(), 0);
    return () => clearTimeout(timer);
  }, [open]);

  const handleKeyDown = useCallback(
    (e) => {
      if (e.key === 'Escape') {
        setAnchorEl(null);
        return;
      }
      // ArrowUp/ArrowDown navigation + Enter-to-select the highlighted row.
      // Gated on `open` so these keys still behave normally (e.g. page
      // scroll) when the trigger button has focus but the panel is closed.
      // Indexes into navigableItems, not filteredOptions — a collapsed
      // category isn't Arrow-key reachable until expanded (except its own
      // toggle entry, which is).
      const navCount = navigableItems.length;
      if (!open || navCount === 0) {
        return;
      }
      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault();
          setHighlightedKey(navItemKey(navigableItems[Math.min(navCount - 1, highlightedIndex + 1)]));
          break;
        case 'ArrowUp':
          e.preventDefault();
          setHighlightedKey(navItemKey(navigableItems[Math.max(0, highlightedIndex - 1)]));
          break;
        case 'Enter': {
          if (highlightedIndex < 0) {
            break;
          }
          e.preventDefault();
          const item = navigableItems[highlightedIndex];
          if (!item) {
            break;
          }
          if (item.__sectionToggle) {
            toggleSection(item.sectionLabel);
          } else {
            handleOptionSelect(item);
          }
          break;
        }
        default:
          break;
      }
    },
    [open, navigableItems, highlightedIndex, handleOptionSelect, toggleSection]
  );

  return (
    // Forces this instance's popover to the viewport center regardless of trigger
    // position, and gives it a Modal-like (@ui/Modal) pop-in + dark backdrop.
    // Backdrop mirrors Modal's default dim color (MUI Backdrop's own
    // rgba(0,0,0,0.5)) and its opacity-transition timing.
    <Box
      sx={{
        position: 'relative',
        width: '100%',
        maxWidth: '60%',
        '@media (max-width: 1300px)': {
          maxWidth: '70%',
        },
      }}
    >
      {/* Outer pill shell — widened from the old single-button trigger to make
          room for the Ask AI avatar on the right, now that the standalone
          gradient "Ask nubi" button next to this search box (formerly in
          Header1.jsx) has been folded into this component instead. */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          width: '100%',
          minWidth: '260px',
          height: ds.space.mul(0, 18),
          backgroundColor: 'var(--ds-background-100)',
          border: '1px solid var(--ds-gray-300)',
          borderRadius: 'var(--ds-radius-md)',
          boxSizing: 'border-box',
          overflow: 'hidden',
          transition: 'border-color 120ms ease, box-shadow 120ms ease, background-color 120ms ease',
          '&:hover': { borderColor: 'var(--ds-gray-400)' },
          '&:focus-within': { borderColor: 'var(--ds-blue-500)', boxShadow: '0 0 0 3px var(--ds-blue-100)' },
          ...(open && { borderColor: 'var(--ds-blue-500)', boxShadow: '0 0 0 3px var(--ds-blue-100)' }),
        }}
      >
        <Box
          component='button'
          type='button'
          id='auto-complete-global-page-search'
          ref={triggerRef}
          onClick={(e) => openSearch(e.currentTarget)}
          onKeyDown={handleKeyDown}
          sx={{
            flex: 1,
            minWidth: 0,
            display: 'inline-flex',
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
            height: '100%',
            padding: '0 var(--ds-space-3)',
            fontFamily: 'inherit',
            fontSize: 'var(--ds-text-small)',
            fontWeight: 'var(--ds-font-weight-regular)',
            lineHeight: 1.4,
            color: 'var(--ds-gray-700)',
            background: 'none',
            border: 'none',
            outline: 'none',
            cursor: 'pointer',
            whiteSpace: 'nowrap',
          }}
        >
          <SearchIcon sx={{ fontSize: 16, color: 'var(--ds-gray-400)', flexShrink: 0 }} />
          <span
            style={{
              color: 'var(--ds-gray-600)',
              fontWeight: 'var(--ds-font-weight-regular)',
              flex: 1,
              textAlign: 'left',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
            }}
          >
            Search pages, dashboards or just ask…
          </span>
          <Box component='kbd' sx={searchKeyChipSx}>
            Ctrl/⌘
          </Box>
          <Box component='kbd' sx={searchKeyChipSx}>
            K
          </Box>
        </Box>

        <Box sx={{ width: '1px', height: ds.space.mul(0, 10), backgroundColor: 'var(--ds-gray-200)', flexShrink: 0 }} />

        {/* Ask AI avatar — a direct shortcut to /ask-nudgebee, distinct from the
            trigger button beside it (which opens page search). Same plain
            white circle-badge treatment as the nubi icon on the home page's
            own "Ask nubi" input (home/index.jsx) — no gradient, no motion. */}
        <CustomTooltip title={`Ask ${assistantName}`} placement='bottom'>
          <Box
            component='button'
            type='button'
            id='global-search-trigger-ask-ai'
            aria-label={`Ask ${assistantName}`}
            onClick={(e) => {
              e.stopPropagation();
              handleAskAi('');
            }}
            sx={{
              flexShrink: 0,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              width: 22,
              height: 22,
              margin: `0 ${ds.space[2]}`,
              border: 'none',
              cursor: 'pointer',
              backgroundColor: 'var(--ds-background-100)',
              overflow: 'hidden',
            }}
          >
            <SafeIcon src={nubiIconUrl} alt='' width={22} height={22} />
          </Box>
        </CustomTooltip>
      </Box>

      <Popover
        open={open}
        anchorEl={anchorEl}
        onClose={() => setAnchorEl(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
        transformOrigin={{ vertical: 'top', horizontal: 'left' }}
        TransitionComponent={GlobalSearchPopoverTransition}
        slotProps={{
          // No disablePortal: this renders straight to document.body (like
          // ds/Modal), so it lands at MUI's default modal z-index (1300) —
          // clear of the sidebar's own z-index:100 stacking context. With
          // disablePortal, the whole Modal (backdrop included) used to nest
          // inside #app-sticky-header's position:sticky/z-index:20 subtree,
          // which caps it below the sidebar regardless of its own z-index —
          // the sidebar's hover flyout would paint (and stay clickable) over
          // the dimmed backdrop. Because it no longer nests under the
          // trigger, the paper override below goes straight on the paper
          // slot's own sx (not an ancestor `&` selector) so it still applies
          // wherever the portal lands. Same reasoning for the backdrop dim,
          // via `sx` on the Popover itself below.
          paper: {
            sx: {
              // Surface chrome shared via --ds-overlay-* tokens with DropdownMenu/Select/FilterDropdown.
              mt: 'var(--ds-overlay-anchor-gap)',
              backgroundColor: 'var(--ds-overlay-bg)',
              borderRadius: 'var(--ds-overlay-radius)',
              border: 'none',
              boxShadow: 'var(--ds-overlay-shadow)',
              width: POPOVER_WIDTH,
              overflow: 'hidden',
              // Forces the popover to the viewport center regardless of
              // trigger position. top is a fixed offset (not 50%) so the
              // panel's top edge stays put as its height changes with the
              // result count — true vertical centering would re-center
              // around a shrinking/growing box, making the top edge visibly
              // jump on every keystroke. Only left is 50%, so horizontal
              // centering still uses translateX.
              position: 'fixed !important',
              top: `${ds.space.mul(0, 50)} !important`,
              left: '50% !important',
              // Static resting transform, matching the keyframe's own 100% value
              // — kept OUTSIDE the animation (see `forwards` note below) so it's
              // still in effect once the animation ends.
              transform: 'translate(-50%, 0)',
              margin: 0,
              padding: ds.space[4],
              transformOrigin: 'top center',
              // No `forwards`: an animation held via fill-mode: forwards keeps
              // its GPU compositing layer pinned indefinitely after it finishes,
              // which left Chrome occasionally failing to repaint this element's
              // screen region once it was actually removed from the DOM on
              // close — a stale "ghost" frame of the popover stayed visible
              // until something else forced a repaint (e.g. typing). The base
              // `transform` above already matches the animation's end state, so
              // dropping `forwards` doesn't cause a visible snap once it ends.
              animation: 'globalSearchPopoverPopIn 360ms cubic-bezier(0.22, 1, 0.36, 1)',
              '@keyframes globalSearchPopoverPopIn': {
                '0%': { transform: 'translate(-50%, 0) translateY(20px) scale(0.96)', opacity: 0 },
                '100%': { transform: 'translate(-50%, 0) translateY(0) scale(1)', opacity: 1 },
              },
            },
          },
        }}
        // Popover has no slotProps.backdrop slot (it hardcodes slotProps.root's
        // own backdrop config to `invisible: true`, which a nested override
        // would collide with) — so the dim backdrop is styled here instead, via
        // a descendant selector on the Popover's own sx. That still lands on
        // Modal's root wrapper regardless of portal target, and the Backdrop
        // is always rendered as a real DOM child of that wrapper, so this
        // reaches it correctly whether or not the popover is portalled.
        sx={{
          '& .MuiBackdrop-root': {
            backgroundColor: 'rgba(0, 0, 0, 0.5)',
            transition: 'opacity 300ms cubic-bezier(0.22, 1, 0.36, 1) !important',
          },
        }}
        onKeyDown={handleKeyDown}
      >
        <CustomTooltip title='Close (Esc/Ctrl/⌘+K)' placement='top'>
          <IconButton
            id='global-search-close-btn'
            data-testid='global-search-close-btn'
            onClick={() => setAnchorEl(null)}
            aria-label='Close'
            sx={{
              position: 'absolute',
              top: ds.space.mul(0, 3),
              right: ds.space.mul(0, 3),
              padding: ds.space.mul(0, 1),
              zIndex: 2,
              color: 'var(--ds-gray-500)',
            }}
          >
            <CloseIcon sx={{ fontSize: 16 }} />
          </IconButton>
        </CustomTooltip>

        <Box
          sx={{
            margin: `${ds.space.mul(0, 5)} ${ds.space.mul(0, 5)} ${ds.space.mul(0, 3)} ${ds.space.mul(0, 5)}`,
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
          }}
        >
          <Box sx={{ position: 'relative', flex: 1, minWidth: 0 }}>
            <SearchIcon
              sx={{
                position: 'absolute',
                left: ds.space.mul(0, 5),
                top: '50%',
                transform: 'translateY(-50%)',
                fontSize: 12,
                opacity: 0.35,
                pointerEvents: 'none',
                zIndex: 1,
              }}
            />
            <InputBase
              id='global-search-input'
              inputRef={searchRef}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              startAdornment={scopedAccount ? <AccountMentionChip account={scopedAccount} /> : undefined}
              placeholder={searchPlaceholder}
              onKeyDown={(e) => {
                if (e.key === 'Backspace' && search === '') {
                  handleBackspaceWhenEmpty();
                }
                handleKeyDown(e);
                // handleKeyDown is also wired to the Popover's own onKeyDown,
                // which this event would otherwise reach too via bubbling —
                // stop it for exactly the keys handleKeyDown consumes so
                // Arrow nav / Enter-select don't double-apply. Everything
                // else (typing, Ctrl+K, etc.) bubbles normally.
                if (e.key === 'Escape' || e.key === 'ArrowUp' || e.key === 'ArrowDown' || (e.key === 'Enter' && highlightedIndex >= 0)) {
                  e.stopPropagation();
                }
                if (e.key === 'Enter' && highlightedIndex < 0 && filteredOptions.length > 0) {
                  e.preventDefault();
                  // Select exact match first, otherwise select if only one result.
                  const q = search.trim().toLowerCase();
                  const exactMatch = filteredOptions.find((opt) => (opt?.label ?? '').toLowerCase() === q);
                  if (exactMatch) {
                    handleOptionSelect(exactMatch);
                  } else if (filteredOptions.length === 1) {
                    handleOptionSelect(filteredOptions[0]);
                  }
                }
                // A query that matches no page is a dead end otherwise — hand it
                // straight to the AI assistant, same as clicking the "Ask
                // {assistantName}" button beside this input.
                if (e.key === 'Enter' && askAiEmptyQuery) {
                  e.preventDefault();
                  handleAskAi(askAiEmptyQuery);
                }
              }}
              sx={{
                width: '100%',
                fontSize: 'var(--ds-text-body)',
                color: 'var(--ds-gray-700)',
                border: '1px solid var(--ds-gray-200)',
                borderRadius: ds.radius.md,
                padding: `${ds.space.mul(0, 3)} ${ds.space.mul(0, 5)} ${ds.space.mul(0, 3)} ${ds.space.mul(0, 14)}`,
                transition: 'all 0.15s ease',
                '&.Mui-focused': {
                  backgroundColor: 'var(--ds-background-100)',
                  borderColor: 'var(--ds-blue-500)',
                  boxShadow: '0 0 0 3px var(--ds-blue-100)',
                },
                '& input::placeholder': { color: 'var(--ds-gray-500)', opacity: 1 },
                '& .MuiInputBase-input': { padding: 0 },
              }}
            />
          </Box>

          {/* "Ask {assistantName}" — sits outside the input's own border, to its
              right, rather than as its own row below (the former AskAiPinnedRow).
              Stays visible even mid @account-pick — unlike that row, this is a
              persistent shortcut, not a per-query "no match" hand-off. */}
          <DsButton
            id='global-search-ask-ai-top'
            tone='primary'
            size='sm'
            icon={<SafeIcon src={nubiIconUrl} alt='' width={14} height={14} />}
            onClick={() => handleAskAi(search.trim())}
            loading={askingAi}
            sx={{ flexShrink: 0 }}
          >
            Ask {assistantName}
          </DsButton>
        </Box>

        <OptionsList
          filteredOptions={navigableItems}
          highlightedIndex={highlightedIndex}
          onSelect={handleOptionSelect}
          mentionMode={mentionMode}
          expandedSections={expandedSections}
          onToggleSection={toggleSection}
          queryResultsKey={filteredOptions}
        />

        <Divider sx={{ marginTop: 0, marginBottom: 0 }} />
        <GlobalSearchFooterHints mentionMode={mentionMode} />
      </Popover>
    </Box>
  );
}
export default React.memo(GlobalPageSearch);
