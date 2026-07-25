/**
 * GlobalPageSearch
 *
 * Cmd/Ctrl+K page search box shown in the app header. Self-contained: owns
 * the static + per-provider result lists, the "@account" scoping picker,
 * recent-search persistence, and the Cmd/Ctrl+K shortcut. Renders with zero
 * props — reads selectedCluster/allCluster from DataContext, the session
 * from NextAuth, and drives navigation itself.
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
import Box from '@mui/material/Box';
import { Typography, Popover, InputBase, Fade } from '@mui/material';
import SearchIcon from '@mui/icons-material/Search';
import { ds } from '@utils/colors';
import Chip from '@ui/Chip';
import Divider from '@ui/Divider';
import { Label } from '@ui/Label';
import CustomTooltip from '@ui/Tooltip';
import SafeIcon from '@shared/icons/SafeIcon';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import { useData } from '@context/DataContext';
import { hasReadAccess } from '@lib/auth';
import apiUser, { PREFERENCE_LAST_ACCOUNT_ID } from '@api1/user';
import AdminIconBlue from '@assets/header/AdminIconBlue.icon.svg';
import OptimiseIconBlue from '@assets/header/OptimiseIconBlue.icon.svg';
import TicketIconBlue from '@assets/header/TicketIconBlue.icon.svg';
import TroubleshootIconBlue from '@assets/header/TroubleshootIconBlue.icon.svg';
import { AutomateBlue } from '@assets';
import {
  navSearchPages,
  automationSearchFragments,
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
const MAX_LIST_HEIGHT = 420;
const POPOVER_WIDTH = ds.space.mul(0, 340);

// Icon shown per header-search row: the parent page's icon (same icons the
// main nav uses for these sections), not a distinct icon per tab.
const NAV_SEARCH_GROUP_ICON = {
  Troubleshoot: TroubleshootIconBlue,
  Automation: AutomateBlue,
  Optimize: OptimiseIconBlue,
  Tickets: TicketIconBlue,
  Admin: AdminIconBlue,
};

// Keyboard-hint bar rendered below the options list — static markup (no
// component state), so it's built once at module scope rather than
// re-created every render.
const NAV_SEARCH_KEYBOARD_HINTS = [{ keys: ['↑', '↓'], label: 'Navigate' }];

const searchKeyChipSx = {
  fontFamily: 'var(--ds-font-mono)',
  fontSize: 'var(--ds-text-caption)',
  color: 'var(--ds-gray-500)',
  backgroundColor: 'var(--ds-gray-100)',
  border: '1px solid var(--ds-gray-200)',
  borderRadius: 'var(--ds-radius-sm)',
  padding: `${ds.space[0]} ${ds.space.mul(0, 3)}`,
};

const GlobalSearchFooterHints = ({ mentionMode = false }) => (
  <Box
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

// ds/FilterDropdown.jsx's own `type` chip (which OptionItem below is ported
// from) defaults to a 15-char tooltip-truncation limit and a 40%-of-row max
// width — both sized for short region/namespace chips, too narrow for a full
// path like "/aws/optimize/recommendation-resolution". Every nav-search row
// needs the wider path-sized limits below, so OptionItem applies these
// directly instead of the FilterDropdown defaults.
const NAV_SEARCH_PATH_CHAR_LIMIT = 50;
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

// Search rows for the Automation page's tabs, or [] if no account is
// resolvable yet — same shape/contract as navSearchProviderItems above, kept
// as its own function (not folded into it) since Automation isn't tied to a
// single cloud provider (any connected account works) and its route carries
// the account as a `?accountId=` query param rather than a `{basePath}/{id}`
// path segment (see src/pages/automation/index.jsx).
const navSearchAutomationItems = (fragments, accountId) =>
  accountId
    ? fragments.map((entry) => {
        const path = `/automation?accountId=${accountId}#${entry.fragment}`;
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
      })
    : [];

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

// Matches an Automation search value stamped by navSearchAutomationItems
// (`/automation?accountId={accountId}#{fragment}`) and captures the
// accountId — used to re-resolve a recent pick against allCluster (see
// resolveRecentOption below), same reasoning as ACCOUNT_SCOPED_SEARCH_PATH_RE
// above: a recent value's account isn't necessarily whichever one
// automationAccountId currently resolves to.
const AUTOMATION_SCOPED_SEARCH_PATH_RE = /^\/automation\?accountId=([^#]+)#/;

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

// Static (non-account) search rows — same for every render, so built once at
// module load instead of inside a per-render useMemo.
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

const ChevronIcon = ({ open = false }) => (
  <svg
    width='12'
    height='12'
    viewBox='0 0 10 10'
    fill='none'
    style={{
      opacity: 0.3,
      transition: 'transform 0.2s ease',
      transform: open ? 'rotate(180deg)' : 'rotate(0deg)',
      flexShrink: 0,
    }}
  >
    <path d='M2 3.5L5 6.5L8 3.5' stroke='currentColor' strokeWidth='1.5' strokeLinecap='round' strokeLinejoin='round' />
  </svg>
);

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
              past this 30% slot and collide with the path chip. displayTooltip
              shortens the actual text (not just visually), same fix the
              `type` Label chip below already uses for the same row-crowding
              problem. */}
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
        <Box sx={{ ml: 'auto', flexShrink: 0, maxWidth: NAV_SEARCH_PATH_MAX_WIDTH }}>
          {/* Label capitalizes by default; every opt.type here is a slash-joined
              path (case-sensitive), so textTransform is forced off. */}
          <Label text={opt.type} textTransform='none' maxWidth='100%' displayTooltip tooltipCharLimit={NAV_SEARCH_PATH_CHAR_LIMIT} />
        </Box>
      )}
    </Box>
  );
});

// Plain, non-interactive caption above a run of options sharing a
// `sectionLabel` (e.g. "Recents" ahead of the full list) — renders once per
// contiguous run, whenever an option's sectionLabel differs from the one
// right before it.
const startsNewSection = (opt, prevOpt) => !!opt?.sectionLabel && opt.sectionLabel !== prevOpt?.sectionLabel;

function SectionCaption({ label }) {
  return (
    <Box
      sx={{
        padding: 'var(--ds-overlay-item-padding-md)',
        margin: '0 var(--ds-overlay-item-margin-x)',
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
function OptionsList({ filteredOptions, highlightedIndex, onSelect }) {
  const navActive = highlightedIndex >= 0;
  const scrollRef = useRef(null);
  const [scrollTop, setScrollTop] = useState(0);

  const handleScroll = useCallback((e) => setScrollTop(e.currentTarget.scrollTop), []);

  useEffect(() => {
    setScrollTop(0);
    if (scrollRef.current) {
      scrollRef.current.scrollTop = 0;
    }
  }, [filteredOptions]);

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
    return (
      <Box sx={scrollboxSx}>
        <Typography
          sx={{ padding: `${ds.space[4]} ${ds.space.mul(0, 7)}`, fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)', textAlign: 'center' }}
        >
          No results found
        </Typography>
      </Box>
    );
  }

  const renderRow = (opt, idx) => {
    const isNewSection = startsNewSection(opt, filteredOptions[idx - 1]);
    return (
      <React.Fragment key={(opt.sectionLabel || '') + '-' + opt.value}>
        {/* idx !== 0 excludes the very first section's own caption (nothing
            above it to divide from) — the only other section boundary is
            Recents -> Suggested Pages, so this only ever renders when a
            Recents run precedes it. */}
        {isNewSection && idx !== 0 && <Divider sx={{ marginTop: 0, marginBottom: 0 }} />}
        {isNewSection && <SectionCaption label={opt.sectionLabel} />}
        <OptionItem opt={opt} highlighted={idx === highlightedIndex} navIndex={idx} navActive={navActive} onSelect={onSelect} />
      </React.Fragment>
    );
  };

  return (
    <Box ref={scrollRef} onScroll={handleScroll} sx={scrollboxSx}>
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

export default function GlobalPageSearch() {
  const { data } = useSession();
  const router = useRouter();
  const { selectedCluster, allCluster, setSelectedCluster } = useData();

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

  // Navigates to a search result. K8s/AWS/Azure/GCP results carry `accountId`
  // for whichever account resolveSearchAccountId picked. ClusterDropDown
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
  const [hasOpenedSearch, setHasOpenedSearch] = useState(false);
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
  // Same gate the sidebar's own "Admin" nav item uses (layout/index.jsx) — a
  // user who can't see that nav entry shouldn't find its pages (Users,
  // Groups, Audits, Notifications, Integrations, Ownership) via search either.
  const canAccessAdmin = hasReadAccess();
  // Same gate the Automation page's own Task Runner tab uses (automation/index.jsx's
  // isAdmin) — only tenant_admin/account_admin can see that tab there, so a
  // non-admin shouldn't be able to jump to it via search either.
  const canAccessTaskRunner = !!(data?.roles?.includes('tenant_admin') || data?.roles?.includes('account_admin'));

  // Automation isn't provider-scoped (no K8s/AWS/Azure/GCP concept), so its
  // search entries can't reuse resolveSearchAccountId's per-provider cache.
  // Falls back in the same order ClusterDropDown.jsx uses when it resolves
  // an account with no prior URL/selectedCluster to go on: the cached last
  // account (any provider), else the first connected account.
  const automationSearchAccountId = useMemo(() => {
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
      return true;
    },
    [canAccessAdmin, canAccessTaskRunner]
  );

  const automationNavItems = useMemo(
    () => (hasOpenedSearch ? navSearchAutomationItems(automationSearchFragments, automationSearchAccountId) : []),
    [hasOpenedSearch, automationSearchAccountId]
  );

  const navSearchItems = useMemo(
    () => [
      ...navSearchStaticItems.filter(isNavSearchItemVisible),
      ...automationNavItems.filter(isNavSearchItemVisible),
      ...k8sNavItems,
      ...awsNavItems,
      ...azureNavItems,
      ...gcpNavItems,
    ],
    [isNavSearchItemVisible, automationNavItems, k8sNavItems, awsNavItems, azureNavItems, gcpNavItems]
  );

  // Re-resolves one recent value into a full display option. A static page's
  // value is always in navSearchStaticItems. An Automation or provider-detail
  // value carries its accountId in the value itself — that account isn't
  // necessarily the current automationSearchAccountId/provider's single
  // resolved account (for providers, the @mention picker also lets you pick
  // and search within ANY connected account, not just the resolved one), so
  // navSearchItems alone (built for just the currently-resolved account) can't
  // be used as the existence check here. Checking against allCluster directly
  // instead means a recent pick whose account has since been
  // disconnected/removed is silently dropped, same as a renamed/removed
  // static page already was.
  const resolveRecentOption = useCallback(
    (value) => {
      const staticMatch = navSearchStaticItems.find((opt) => opt.value === value);
      if (staticMatch) {
        return isNavSearchItemVisible(staticMatch) ? staticMatch : null;
      }
      const automationMatch = AUTOMATION_SCOPED_SEARCH_PATH_RE.exec(value);
      if (automationMatch) {
        const [, accountId] = automationMatch;
        if (!allCluster?.some((c) => c.value === accountId)) {
          return null;
        }
        const match = navSearchAutomationItems(automationSearchFragments, accountId).find((opt) => opt.value === value);
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
    [allCluster, isNavSearchItemVisible]
  );

  // Two lists under one plain caption each (opt.sectionLabel): recent picks
  // under "Recents", then the full navSearchItems under "Suggested Pages" — a
  // recent pick intentionally still appears in "Suggested Pages" too (as a
  // separate option copy), not just "Recents". "Suggested Pages" is always tagged
  // (even with zero recents, when it's the only section) for a consistent
  // caption. "Recents" is prepended only once there's at least one recent
  // pick. Still one flat array under the hood, so the ArrowUp/ArrowDown +
  // Enter-to-select keyboard nav below works identically whether one or both
  // sections are present.
  //
  // Recent rows additionally get `accountName`/`cloud_provider` stamped on
  // (when the recent pick carries an accountId) so OptionItem can render an
  // account-name chip — a recent value's account isn't shown anywhere else
  // in the row, and (unlike a provider's current resolved account) isn't
  // necessarily the provider's current one — it's whatever account the user
  // was actually in when they picked it.
  const navSearchItemsWithRecent = useMemo(() => {
    const allOptions = navSearchItems.map((opt) => ({ ...opt, sectionLabel: 'Suggested Pages' }));
    if (recentSearchValues.length === 0) {
      return allOptions;
    }
    const recentOptions = recentSearchValues
      .map(resolveRecentOption)
      .filter(Boolean)
      .map((opt) => {
        const account = opt.accountId ? allCluster?.find((c) => c.value === opt.accountId) : null;
        return { ...opt, sectionLabel: 'Recents', accountName: account?.label, cloud_provider: account?.cloud_provider };
      });
    return [...recentOptions, ...allOptions];
  }, [navSearchItems, recentSearchValues, resolveRecentOption, allCluster]);

  // Once an account is picked, results are scoped to just that account's
  // provider detail pages — reuses the same navSearchProviderItems helper the
  // unscoped per-provider lists above already use, so accountId/path/icon
  // wiring stays identical. Automation isn't provider-specific (any connected
  // account works there), so it's appended after the provider's own pages
  // rather than gated behind a cloud_provider match, same filter (Admin/Task
  // Runner) as the unscoped list.
  const scopedSearchItems = useMemo(() => {
    if (!scopedAccount) {
      return [];
    }
    const config = SCOPED_SEARCH_PROVIDER_CONFIG[scopedAccount.cloud_provider?.toUpperCase()];
    const providerItems = config ? navSearchProviderItems(config.fragments, config.label, scopedAccount.value, config.basePath) : [];
    const automationItems = navSearchAutomationItems(automationSearchFragments, scopedAccount.value).filter(isNavSearchItemVisible);
    return [...providerItems, ...automationItems];
  }, [scopedAccount, isNavSearchItemVisible]);

  // The full (unfiltered) option list for whichever mode is active — mirrors
  // ds/FilterDropdown.jsx's `options` prop.
  const searchBoxOptions = mentionMode ? accountMentionOptions : scopedAccount ? scopedSearchItems : navSearchItemsWithRecent;

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

  const searchPlaceholder = scopedAccount
    ? `Search for ${scopedAccount.label}…`
    : hasMentionAccounts
    ? 'Search pages… (type @ to scope by account)'
    : 'Search pages…';

  const handleBackspaceWhenEmpty = useCallback(() => {
    if (scopedAccount) {
      setScopedAccount(null);
    }
  }, [scopedAccount]);

  // --- Trigger/popover state (mirrors ds/FilterDropdown.jsx's own) ---
  const [anchorEl, setAnchorEl] = useState(null);
  const [highlightedIndex, setHighlightedIndex] = useState(-1);
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

  // Fires once per popover open, before it actually opens: lazily builds the
  // per-provider result rows on first open (see hasOpenedSearch above) and
  // refreshes the recent-searches list (a pick made in another tab should
  // show up here too).
  const openSearch = useCallback(
    (target) => {
      setHasOpenedSearch(true);
      setRecentSearchValues(apiUser.getRecentPageSearches(data?.tenant?.id));
      setAnchorEl(target);
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
          openSearch(triggerRef.current);
        }
      }
    };
    window.addEventListener('keydown', handleGlobalKeyDown);
    return () => window.removeEventListener('keydown', handleGlobalKeyDown);
  }, [anchorEl, openSearch]);

  // Discard the keyboard highlight whenever the option set it indexes into
  // changes (search text) or the panel closes.
  useEffect(() => {
    setHighlightedIndex(-1);
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
      if (!open || filteredOptions.length === 0) {
        return;
      }
      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault();
          setHighlightedIndex((i) => Math.min(filteredOptions.length - 1, i + 1));
          break;
        case 'ArrowUp':
          e.preventDefault();
          setHighlightedIndex((i) => Math.max(0, i - 1));
          break;
        case 'Enter':
          if (highlightedIndex >= 0 && filteredOptions[highlightedIndex]) {
            e.preventDefault();
            handleOptionSelect(filteredOptions[highlightedIndex]);
          }
          break;
        default:
          break;
      }
    },
    [open, filteredOptions, highlightedIndex, handleOptionSelect]
  );

  return (
    // Forces this instance's popover to the viewport center regardless of trigger
    // position, and gives it a Modal-like (@ui/Modal) pop-in + dark backdrop.
    // Overrides the MuiPopover-paper's JS-computed inline top/left —
    // disablePortal keeps the paper in this subtree, so the selector reaches
    // it — with !important, since author !important beats both inline
    // styles and the component's own transform/slide-in keyframes.
    // Backdrop mirrors Modal's default dim color (MUI Backdrop's own
    // rgba(0,0,0,0.5)) and its opacity-transition timing.
    <Box
      sx={{
        position: 'relative',
        width: '100%',
        maxWidth: '80%',
        // top is a fixed offset (not 50%) so the panel's top edge stays put as its
        // height changes with the result count — true vertical centering would
        // re-center around a shrinking/growing box, making the top edge visibly
        // jump on every keystroke. Only left is 50%, so horizontal centering still
        // uses translateX; there's no translateY left to do.
        '& .MuiPopover-paper': {
          position: 'fixed !important',
          top: `${ds.space.mul(0, 60)} !important`,
          left: '50% !important',
          // Static resting transform, matching the keyframe's own 100% value
          // — kept OUTSIDE the animation (see `forwards` note below) so it's
          // still in effect once the animation ends.
          transform: 'translate(-50%, 0)',
          margin: 0,
          padding: ds.space[4],
          // No `forwards`: an animation held via fill-mode: forwards keeps
          // its GPU compositing layer pinned indefinitely after it finishes,
          // which left Chrome occasionally failing to repaint this element's
          // screen region once it was actually removed from the DOM on
          // close — a stale "ghost" frame of the popover stayed visible
          // until something else forced a repaint (e.g. typing). The base
          // `transform` above already matches the animation's end state, so
          // dropping `forwards` doesn't cause a visible snap once it ends.
          animation: 'globalSearchPopoverPopIn 360ms cubic-bezier(0.22, 1, 0.36, 1) !important',
        },
        '@keyframes globalSearchPopoverPopIn': {
          '0%': { transform: 'translate(-50%, 0) translateY(20px) scale(0.96)', opacity: 0 },
          '100%': { transform: 'translate(-50%, 0) translateY(0) scale(1)', opacity: 1 },
        },
        '& .MuiBackdrop-root': {
          backgroundColor: 'rgba(0, 0, 0, 0.5) !important',
          transition: 'opacity 300ms cubic-bezier(0.22, 1, 0.36, 1) !important',
        },
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
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 'var(--ds-space-2)',
          minWidth: '150px',
          height: ds.space.mul(0, 18),
          padding: '0 var(--ds-space-3)',
          fontFamily: 'inherit',
          fontSize: 'var(--ds-text-small)',
          fontWeight: 'var(--ds-font-weight-regular)',
          lineHeight: 1.4,
          color: 'var(--ds-gray-700)',
          backgroundColor: 'var(--ds-background-100)',
          border: '1px solid var(--ds-gray-300)',
          borderRadius: 'var(--ds-radius-md)',
          outline: 'none',
          cursor: 'pointer',
          transition: 'border-color 120ms ease, box-shadow 120ms ease, background-color 120ms ease',
          whiteSpace: 'nowrap',
          boxSizing: 'border-box',
          '&:hover': { borderColor: 'var(--ds-gray-400)', backgroundColor: 'var(--ds-background-200)' },
          '&:focus-visible': { borderColor: 'var(--ds-blue-500)', boxShadow: '0 0 0 3px var(--ds-blue-100)' },
          ...(open && { borderColor: 'var(--ds-blue-500)', boxShadow: '0 0 0 3px var(--ds-blue-100)' }),
          width: '100%',
          maxWidth: '80%',
          pl: ds.space[6],
        }}
      >
        <span style={{ color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-regular)', flex: 1, textAlign: 'left' }}>Search pages…</span>
        <ChevronIcon open={open} />
      </Box>

      <Popover
        open={open}
        anchorEl={anchorEl}
        onClose={() => setAnchorEl(null)}
        disablePortal
        anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
        transformOrigin={{ vertical: 'top', horizontal: 'left' }}
        TransitionComponent={GlobalSearchPopoverTransition}
        slotProps={{
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
              transformOrigin: 'top left',
              animation: 'globalSearchSlideIn var(--ds-overlay-enter-duration) var(--ds-overlay-enter-easing)',
              '@keyframes globalSearchSlideIn': {
                '0%': { opacity: 0, transform: 'scaleY(0.9) translateY(-8px)' },
                '100%': { opacity: 1, transform: 'scaleY(1) translateY(0)' },
              },
            },
          },
        }}
        onKeyDown={handleKeyDown}
      >
        <Box sx={{ margin: `${ds.space.mul(0, 5)} ${ds.space.mul(0, 5)} ${ds.space.mul(0, 3)} ${ds.space.mul(0, 5)}`, position: 'relative' }}>
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
            }}
            sx={{
              width: '100%',
              fontSize: 'var(--ds-text-body)',
              color: 'var(--ds-gray-700)',
              border: '1px solid var(--ds-gray-200)',
              backgroundColor: 'var(--ds-gray-100)',
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

        <OptionsList filteredOptions={filteredOptions} highlightedIndex={highlightedIndex} onSelect={handleOptionSelect} />

        <Divider sx={{ marginTop: 0, marginBottom: 0 }} />
        <GlobalSearchFooterHints mentionMode={mentionMode} />
      </Popover>

      {/* Leading search icon, matching the one shown inside the open popover's own
          search input — pointerEvents: 'none' so it doesn't block the trigger click. */}
      <SearchIcon
        sx={{
          position: 'absolute',
          left: ds.space.mul(0, 5),
          top: '50%',
          transform: 'translateY(-50%)',
          fontSize: 16,
          color: 'var(--ds-gray-400)',
          pointerEvents: 'none',
        }}
      />
      {/* Overlaid, not part of the trigger button's own layout — pointerEvents:
          'none' lets clicks fall through to the button underneath so the whole
          trigger area (including under this hint) still opens the search. Sits at
          a fixed offset clear of the trigger's own chevron icon. */}
      <Box
        sx={{
          position: 'absolute',
          right: '30%',
          top: '50%',
          transform: 'translateY(-50%)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-1)',
          pointerEvents: 'none',
        }}
      >
        <Box component='kbd' sx={searchKeyChipSx}>
          Ctrl/⌘
        </Box>
        <Box component='kbd' sx={searchKeyChipSx}>
          K
        </Box>
      </Box>
    </Box>
  );
}
