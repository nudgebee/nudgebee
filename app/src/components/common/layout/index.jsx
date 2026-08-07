import * as React from 'react';
import { useState, useMemo, useEffect, useRef } from 'react';
import { useTenantBranding, DEFAULT_LOGO, DEFAULT_FAVICON } from '@hooks/useTenantBranding';
import Box from '@mui/material/Box';
import { Button, Container, Typography, Menu, MenuItem, IconButton, Popover } from '@mui/material';
import { useRouter } from 'next/router';
import { signOut } from 'next-auth/react';
import Head from 'next/head';
import Link from 'next/link';
import { renderSlot } from '@lib/slots';

// Internal Imports
import { LayoutHeaderActionSlot } from '@shared/layout/LayoutHeaderActionSlot';
import {
  getUserSession,
  withAuth,
  hasAdminSurfaceAccess,
  hasReadAccess,
  isGrantsOnlyUser,
  isUiFeatureEnabled,
  hasPermission,
  missingPermissionMessage,
} from '@lib/auth';
import {
  homeIcon1,
  KubernetesClusterIcon,
  ticketsIcon1,
  troubleshootIcon1,
  AdminIcon,
  ProfileOutlineIcon,
  WhiteOptimizeIcon,
  WorkflowIconWhite,
  AllEventsIcon,
  SearchBlueIcon,
  ServiceMapsIcon,
  AutomateBlue,
  dashboardIcon1,
  PlayCircleIcon,
  OptimizeSummaryIcon,
  RecommendationIcon,
  RecommendationResolutionIcon,
  LLMConsumptionIcon,
  IntegrationsIcon,
  CloudAccountIcon,
  TicketBlueIcon,
  UserIconOutline,
  User1,
  UserGroupIcon,
  AuditIcon,
  NotificationIcon1,
  ApplicationsIcon,
} from '@assets';
import Header1 from '@shared/header/Header1';
import ErrorBoundary from '@shared/ErrorBoundary';
import SafeIcon from '@shared/icons/SafeIcon';
import FirstLoginTour from '@components/onboarding/FirstLoginTour';
import SectionFirstVisitTour from '@components/onboarding/SectionFirstVisitTour';
import Tooltip from '@ui/Tooltip';
import TenantSettings from '@shared/settings/TenantSettings';
import ApiTokens from '@shared/settings/ApiTokens';
import { toast as snackbar } from '@ui/Toast';
import { tenantSwitcher } from '@lib/tenantSwitcherService';
import { useData } from '@context/DataContext';
import { createGetMenuItem, generateMenuItems } from './UserMenuItems';
import NubiBrainNav from './NubiBrainNav';
import { ds } from 'src/utils/colors';
import { isRenderedInIframe } from 'src/utils/common';

const COLLAPSED_WIDTH = 76;

const FLYOUT_CLOSE_DELAY_MS = 150;
const NAV_ITEM_HIGHLIGHT_BG = `color-mix(in srgb, ${ds.background[100]} 10%, transparent)`;
const SIDEBAR_FLYOUT_BG = `color-mix(in srgb, var(--ds-sidebar-bg, var(--ds-brand-600)) 65%, ${ds.gray[700]})`;
const SIDEBAR_FLYOUT_TEXT = `color-mix(in srgb, ${ds.background[100]} 72%, transparent)`;
const NAV_ITEM_HOVER_BG = SIDEBAR_FLYOUT_BG;
const FLYOUT_ITEM_HOVER_BG = `color-mix(in srgb, ${ds.background[100]} 14%, transparent)`;
const FLYOUT_ICON_PX = 20;

/**
 * Utility to calculate dynamic paths based on current route params.
 * Only executed on click now.
 */
const getDynamicPath = (path, router) => {
  // 1. Static paths that never accept params
  if (path === '/user-management' || path === '/tickets' || path === '/kubernetes') {
    return path;
  }

  // 2. EXPLICITLY HANDLE troubleshoot: Add tab=0, ignore accountId
  if (path === '/troubleshoot' || path === 'troubleshoot') {
    return `${path}#all-events`;
  }

  // 2b. Automations is tenant-level too — it has its own Account filter, so
  // seeding accountId from whatever page you clicked from would silently
  // pre-narrow the listing to one account.
  if (path === '/automation') {
    return path;
  }

  // Helper to get Account ID from various sources
  const getAccountId = () => {
    const { asPath, query } = router;
    const cloudAccountMatch = asPath.match(/\/cloud-account\/details\/([a-fA-F0-9-]+)/);
    const k8sMatch = asPath.match(/\/kubernetes\/details\/([a-fA-F0-9-]+)/);

    if (cloudAccountMatch) {
      return { id: cloudAccountMatch[1], type: 'aws' };
    }
    if (k8sMatch) {
      return { id: k8sMatch[1], type: 'k8s' };
    }
    if (query?.accountId) {
      return { id: query.accountId, type: null };
    }
    if (query?.KubernetesDetails) {
      return { id: query.KubernetesDetails, type: null };
    }
    return null;
  };

  const accountData = getAccountId();

  // 3. Special handling for optimize and home (requires type param sometimes)
  if (path === '/optimize' || path === '/home') {
    if (accountData?.id) {
      const typeParam = accountData.type ? `&type=${accountData.type}` : '';
      return `${path}?accountId=${accountData.id}${typeParam}`;
    }
    return path;
  }

  // 4. General handling for other paths: Append accountId if found
  if (accountData?.id) {
    return `${path}?accountId=${accountData.id}`;
  }

  return path;
};

/**
 * Resolves a nav entry's `path` to the URL to actually visit. Sub-items already
 * name their own hash tab (`/troubleshoot#investigations`), so they're taken as
 * written; hash-less paths go through getDynamicPath to pick up the default tab
 * and the in-scope accountId.
 */
const resolveNavPath = (path, router) => (path.includes('#') ? path : getDynamicPath(path, router));

/**
 * Navigating out of Troubleshoot's Knowledge Graph tab with next/router is
 * blocked by the heavy elkjs layout running inside it, so leave that tab with a
 * full document load instead.
 */
const navigateTo = (router, targetPath) => {
  const onKnowledgeGraphTab = router.pathname === '/troubleshoot' && typeof window !== 'undefined' && window.location.hash === '#kg';
  if (onKnowledgeGraphTab) {
    window.location.assign(targetPath);
    return;
  }
  router.push(targetPath);
};

/** True when the route currently rendered is the one this sub-item points at. */
const isSubItemActive = (subPath, router) => {
  const [base, hash] = subPath.split('#');
  if (!router.pathname.startsWith(base)) {
    return false;
  }
  if (!hash) {
    return true;
  }
  // Compare only the top-level hash segment — `#all-events/triage-rules` is
  // still the All Events sub-item.
  return (router.asPath.split('#')[1] || '').split('/')[0] === hash;
};

const SubNavFlyout = ({ item, anchorEl, onMouseEnter, onMouseLeave, onNavigate }) => {
  const router = useRouter();

  if (!item || !anchorEl) {
    return null;
  }

  return (
    <Popover
      id='sidenav-flyout'
      anchorEl={anchorEl}
      open
      onClose={onMouseLeave}
      anchorOrigin={{ vertical: 'top', horizontal: 'right' }}
      transformOrigin={{ vertical: 'top', horizontal: 'left' }}
      sx={{ pointerEvents: 'none' }}
      disableAutoFocus
      disableEnforceFocus
      disableRestoreFocus
      disableScrollLock
      slotProps={{ paper: { onMouseEnter, onMouseLeave, sx: styles.flyoutPaper } }}
    >
      {item.subItems.map((sub) => {
        const row = (
          <MenuItem
            id={sub.id}
            key={sub.text}
            component={sub.disabled ? 'div' : Link}
            href={sub.disabled ? undefined : sub.path}
            disabled={sub.disabled}
            selected={isSubItemActive(sub.path, router)}
            onClick={(e) => {
              e.preventDefault();
              if (sub.disabled) {
                return;
              }
              onNavigate(resolveNavPath(sub.path, router));
            }}
            sx={styles.flyoutItem}
          >
            <Box component='span' className='flyout-item-icon' sx={styles.flyoutItemIcon}>
              <SafeIcon src={sub.icon} alt='' width={FLYOUT_ICON_PX} height={FLYOUT_ICON_PX} />
            </Box>
            {sub.text}
          </MenuItem>
        );
        return sub.disabled && sub.disabledTooltip ? (
          <Tooltip key={sub.text} title={sub.disabledTooltip} placement='right'>
            <span style={{ display: 'block' }}>{row}</span>
          </Tooltip>
        ) : (
          row
        );
      })}
    </Popover>
  );
};

const SideDrawerButton = ({ item = {}, isFlyoutOpen = false, onHoverOpen, onHoverClose }) => {
  const router = useRouter();

  const isActive = useMemo(() => {
    if (item.path === '') {
      return false;
    }
    const currentPath = router.pathname === '/' ? '/' : router.pathname;
    const paths = item.activePaths ? [item.path, ...item.activePaths] : [item.path];
    return paths.some((p) => currentPath.startsWith(p));
  }, [router.pathname, item.path, item.activePaths]);

  // NOTE: destinationPath memoization removed. Logic moved to handleLinkClick.

  const handleLinkClick = (e) => {
    // Permission-disabled item: swallow the click entirely. The Button also
    // carries pointer-events:none, but a keyboard Enter still fires onClick, so
    // guard here too.
    if (item.disabled) {
      e.preventDefault();
      return;
    }
    e.preventDefault();
    navigateTo(router, resolveNavPath(item.path, router));
  };

  const navButton = (
    <Button
      component={Link}
      href={item.disabled ? '#' : item.path || '#'}
      onClick={handleLinkClick}
      onMouseEnter={(e) => onHoverOpen(item, e.currentTarget)}
      onMouseLeave={onHoverClose}
      className={isActive ? 'active-nav' : undefined}
      aria-label={item.text}
      aria-disabled={item.disabled || undefined}
      id={item?.id}
      sx={{
        ...styles.railButton,
        ...(isActive ? styles.activeButton : {}),
        ...(isFlyoutOpen ? styles.railButtonFlyoutOpen : {}),
        ...(item.disabled ? { opacity: 0.4, pointerEvents: 'none' } : {}),
      }}
    >
      {isActive && <Box sx={styles.activeIndicator} />}

      <Box sx={styles.iconContainer}>
        <Box sx={styles.iconWrapper}>
          <SafeIcon src={item.icon} alt={item.text} fill style={{ objectFit: 'contain' }} />
        </Box>

        <Typography sx={styles.iconLabel}>{item.text}</Typography>
      </Box>
    </Button>
  );

  return item.disabled && item.disabledTooltip ? (
    <Tooltip title={item.disabledTooltip} placement='right'>
      <span style={{ display: 'block' }} onMouseEnter={onHoverClose}>
        {navButton}
      </span>
    </Tooltip>
  ) : (
    navButton
  );
};

const PageLayout = ({ children }) => {
  const router = useRouter();

  // State
  const [anchorElUser, setAnchorElUser] = useState(null);
  const [openSwitchAccount, setOpenSwitchAccount] = useState(false);
  const [openSettings, setOpenSettings] = useState(false);
  const [openApiTokens, setOpenApiTokens] = useState(false);
  // Which nav item's sub-section flyout is open, and the rail button it hangs
  // off. One flyout for the whole rail, re-anchored per item.
  const [flyout, setFlyout] = useState(null);
  const flyoutCloseTimer = useRef(null);
  const userMenuCloseTimer = useRef(null);

  // Let any component (e.g. the cross-tenant AccountGuard) request the tenant
  // switcher open without prop-drilling. subscribe() returns its unsubscribe
  // fn, which becomes the effect cleanup.
  useEffect(() => {
    return tenantSwitcher.subscribe(() => setOpenSwitchAccount(true));
  }, []);

  // A pending close must not fire after unmount (or navigation away).
  useEffect(
    () => () => {
      clearTimeout(flyoutCloseTimer.current);
      clearTimeout(userMenuCloseTimer.current);
    },
    []
  );

  // Derived Values
  const session = getUserSession();
  const { selectedCluster } = useData();
  const { baseTitle, logoUrl: brandingLogoUrl, faviconUrl: brandingFaviconUrl, loading: brandingLoading } = useTenantBranding();

  // Logo: derived inline from branding. The `!brandingLoading` gate on the <img> below holds the
  // render until the config resolves, so logoSrc is only ever read with the final value — branded
  // logo when set, DEFAULT_LOGO only for the default tenant (empty logoUrl). No mirrored state
  // (which lagged a render and flashed the default logo) and no onError fallback to DEFAULT_LOGO
  // (on a branded tenant that would leak the Nudgebee logo; a broken URL should surface, not swap).
  const logoSrc = brandingLogoUrl || DEFAULT_LOGO;

  // Favicon from config, fallback to default
  const favicon = brandingFaviconUrl || DEFAULT_FAVICON;

  const avatarSubMenu = useMemo(() => {
    return generateMenuItems(session?.hasMultipleTenantAccess || false);
  }, []);

  const menuItems = useMemo(() => {
    const isAdmin = !!(session?.roles?.includes('tenant_admin') || session?.roles?.includes('account_admin'));
    const canReadAccount = hasReadAccess(selectedCluster?.value);
    const optimizeSections = [
      { text: 'Summary', path: '/optimise#summary', id: 'sidenav-optimise-summary', icon: OptimizeSummaryIcon },
      { text: 'Recommendations', path: '/optimise#recommendations', id: 'sidenav-optimise-recommendations', icon: RecommendationIcon },
      { text: 'Resolutions', path: '/optimise#resolutions', id: 'sidenav-optimise-resolutions', icon: RecommendationResolutionIcon },
      { text: 'Auto Optimize', path: '/optimise#auto-optimize', id: 'sidenav-optimise-auto-optimize', icon: AutomateBlue },
      ...(isUiFeatureEnabled('llmAnalyser') && canReadAccount
        ? [{ text: 'LLM Analyser', path: '/optimise#cost-analyser', id: 'sidenav-optimise-cost-analyser', icon: LLMConsumptionIcon }]
        : []),
      ...(isUiFeatureEnabled('llmGateway') && canReadAccount
        ? [{ text: 'AI Gateway', path: '/optimise#ai-gateway', id: 'sidenav-optimise-ai-gateway', icon: IntegrationsIcon }]
        : []),
    ];

    const items = [
      { path: '/home', icon: homeIcon1, text: 'Home', id: 'home-sidenavbutton', module: 'insights' },
      // No `module`: a dashboard panel may query any connected account, so the
      // page itself gates on nothing — each panel's query is authorised per
      // account by the backend it reads.
      {
        path: '/dashboards',
        icon: dashboardIcon1,
        text: 'Dashboards',
        id: 'dashboards-sidenavbutton',
        subItems: [
          { text: 'Dashboard List', path: '/dashboards#list', id: 'sidenav-dashboards-list', icon: dashboardIcon1 },
          { text: 'Application Grouping', path: '/dashboards#groups', id: 'sidenav-dashboards-groups', icon: ApplicationsIcon },
        ],
      },
      {
        path: '/troubleshoot',
        activePaths: ['/investigate', '/agentHealth'],
        icon: troubleshootIcon1,
        text: 'Troubleshoot',
        id: 'troubleshoot-sidenavbutton',
        module: 'events',
        subItems: [
          { text: 'All Events', path: '/troubleshoot#all-events', id: 'sidenav-troubleshoot-all-events', icon: AllEventsIcon },
          { text: 'Investigations', path: '/troubleshoot#investigations', id: 'sidenav-troubleshoot-investigations', icon: SearchBlueIcon },
          { text: 'Knowledge Graph', path: '/troubleshoot#kg', id: 'sidenav-troubleshoot-kg', icon: ServiceMapsIcon },
        ],
      },
      {
        path: '/automation',
        icon: WorkflowIconWhite,
        text: 'Automations',
        id: 'auto-pilot-sidenavbutton',
        module: 'workflows',
        subItems: [
          { text: 'Automations', path: '/automation#automations', id: 'sidenav-automation-automations', icon: AutomateBlue },
          { text: 'Executions', path: '/automation#executions', id: 'sidenav-automation-executions', icon: dashboardIcon1 },
          {
            text: 'Task Runner',
            path: '/automation#task-runner',
            id: 'sidenav-automation-task-runner',
            icon: PlayCircleIcon,
            disabled: !(isAdmin || hasPermission('workflows', 'Execute') || hasPermission('workflows', 'Write')),
            disabledTooltip: missingPermissionMessage('workflows:Execute'),
          },
        ],
      },
      {
        path: '/optimise',
        icon: WhiteOptimizeIcon,
        text: 'Optimize',
        id: 'optimize-sidenavbutton',
        module: 'recommendations',
        subItems: optimizeSections,
      },
      {
        path: '/kubernetes',
        activePaths: ['/cloud-account'],
        icon: KubernetesClusterIcon,
        text: 'Infra',
        id: 'infra-sidenavbutton',
        subItems: [
          { text: 'K8s', path: '/kubernetes', id: 'sidenav-infra-k8s', module: 'k8s', icon: KubernetesClusterIcon },
          { text: 'Cloud', path: '/cloud-account', id: 'sidenav-infra-cloud', module: 'cloud', icon: CloudAccountIcon },
        ],
      },
      {
        path: '/tickets',
        icon: ticketsIcon1,
        text: 'Tickets',
        id: 'tickets-sidenavbutton',
        module: 'tickets',
        subItems: [
          { text: 'All Tickets', path: '/tickets#tickets', id: 'sidenav-tickets-all', icon: TicketBlueIcon },
          { text: 'Assigned to me', path: '/tickets#assigned-me', id: 'sidenav-tickets-assigned-me', icon: UserIconOutline },
        ],
      },
    ];
    if (hasAdminSurfaceAccess()) {
      items.push({
        path: '/user-management',
        activePaths: ['/accounts'],
        icon: AdminIcon,
        text: 'Admin',
        id: 'admin-sidenav',
        subItems: [
          { text: 'Users', path: '/user-management#users', id: 'sidenav-admin-users', module: 'users', icon: User1 },
          { text: 'Groups', path: '/user-management#groups', id: 'sidenav-admin-groups', module: 'usergroups', icon: UserGroupIcon },
          { text: 'Audits', path: '/user-management#audits', id: 'sidenav-admin-audits', module: 'audits', icon: AuditIcon },
          {
            text: 'Notifications',
            path: '/user-management#notifications',
            id: 'sidenav-admin-notifications',
            module: 'notifications',
            icon: NotificationIcon1,
          },
          {
            text: 'Integrations',
            path: '/user-management#integrations',
            id: 'sidenav-admin-integrations',
            module: 'integrations',
            icon: IntegrationsIcon,
          },
          { text: 'Ownership', path: '/user-management#ownership', id: 'sidenav-admin-ownership', module: 'ownership', icon: UserGroupIcon },
        ],
      });
    }
    if (!isGrantsOnlyUser(selectedCluster?.value)) return items;
    const gate = (entry) =>
      entry.module && !hasPermission(entry.module, 'Read')
        ? { ...entry, disabled: true, disabledTooltip: missingPermissionMessage(`${entry.module}:Read`) }
        : entry;
    return items.map((item) => {
      const subItems = item.subItems?.map(gate);
      const gated = gate({ ...item, ...(subItems ? { subItems } : {}) });
      return gated.disabled || !subItems?.length || subItems.some((sub) => !sub.disabled)
        ? gated
        : { ...gated, disabled: true, disabledTooltip: subItems[0].disabledTooltip };
    });
  }, [selectedCluster?.value, session]);

  // Route/Page Type Detection
  const pageFlags = useMemo(
    () => ({
      isAskNudgebee: router.pathname === '/ask-nudgebee',
      isAskNudgebeeV2: router.pathname === '/ask-nudgebee-v2',
      isInvestigate: router.pathname?.includes('/investigate') || router.pathname?.includes('/investigate2'),
      isWorkflow: router.pathname?.startsWith('/automation/'),
      isOptimize: router.pathname?.includes('/optimise'),
      isTroubleshoot: router.pathname?.includes('/troubleshoot'),
      isHome: router.pathname === '/home',
      isAgentic: router.pathname?.startsWith('/agentic'),
    }),
    [router.pathname]
  );

  const isPlainLayout = pageFlags.isAskNudgebee || pageFlags.isWorkflow;
  const isPaddedLayout = !(pageFlags.isAskNudgebee || pageFlags.isInvestigate || pageFlags.isAskNudgebeeV2);

  // Note: This one is still calculated on render as it's used for the top logo link
  // If you want to optimize this too, you'd need to make the logo a Button with onClick handler similar to above
  const homeUrl = useMemo(() => getDynamicPath('/home', router), [router]);

  // Handlers
  const handleSwitchAccountClose = () => setOpenSwitchAccount(false);

  const openFlyout = (item, anchorEl) => {
    clearTimeout(flyoutCloseTimer.current);
    setFlyout(item.subItems?.length ? { item, anchorEl } : null);
  };

  const scheduleFlyoutClose = () => {
    clearTimeout(flyoutCloseTimer.current);
    flyoutCloseTimer.current = setTimeout(() => setFlyout(null), FLYOUT_CLOSE_DELAY_MS);
  };

  const cancelFlyoutClose = () => clearTimeout(flyoutCloseTimer.current);

  const handleFlyoutNavigate = (targetPath) => {
    setFlyout(null);
    navigateTo(router, targetPath);
  };

  const openUserMenu = (e) => {
    clearTimeout(userMenuCloseTimer.current);
    setAnchorElUser(e.currentTarget);
  };

  const scheduleUserMenuClose = () => {
    clearTimeout(userMenuCloseTimer.current);
    userMenuCloseTimer.current = setTimeout(() => setAnchorElUser(null), FLYOUT_CLOSE_DELAY_MS);
  };

  const cancelUserMenuClose = () => clearTimeout(userMenuCloseTimer.current);

  const handleSubMenuClick = (subMenu) => {
    setAnchorElUser(null);
    switch (subMenu) {
      case 'Logout':
        signOut({ callbackUrl: '/' });
        break;
      case 'Switch Tenant':
        setOpenSwitchAccount(true);
        break;
      case 'API Tokens':
        setOpenApiTokens(true);
        break;
    }
  };

  const getMenuItem = createGetMenuItem({
    setAnchorElUser,
    setOpenSwitchAccount,
    setOpenSettings,
    setOpenApiTokens,
    handleSubMenuClick,
  });

  return (
    <>
      {isPlainLayout ? (
        <>
          <Head>
            {!brandingLoading && <link rel='icon' href={favicon} />}
            <title>{baseTitle}</title>
          </Head>
          {/* Mounted here too so the AccountGuard "Switch Tenant" CTA works
              on plain-layout pages (e.g. ask-nudgebee). */}
          <LayoutHeaderActionSlot open={openSwitchAccount} title={'Switch Tenant'} onClose={handleSwitchAccountClose} />
          {children}
        </>
      ) : (
        <>
          <Head>
            {!brandingLoading && <link rel='icon' href={favicon} />}
            <title>{baseTitle}</title>
          </Head>
          {/* Rendered outside <Head> — next/head only walks immediate children
              for tag extraction, so a slot returning a wrapper Component (vs.
              inline JSX) drops the tags. next/script with the default
              afterInteractive strategy injects itself regardless of position. */}
          {renderSlot('LayoutHeadExtras')}

          <TenantSettings
            open={openSettings}
            title={'Tenant Settings'}
            onClose={(_, msg) => {
              setOpenSettings(false);
              if (msg === 'show') {
                snackbar.success('Tenant Settings saved successfully');
              }
            }}
          />
          <ApiTokens open={openApiTokens} title={'API Tokens'} onClose={() => setOpenApiTokens(false)} />
          <LayoutHeaderActionSlot open={openSwitchAccount} title={'Switch Tenant'} onClose={handleSwitchAccountClose} />

          <Box sx={{ display: 'flex', alignItems: 'stretch', justifyContent: 'center' }}>
            {!isRenderedInIframe() && !pageFlags.isWorkflow && (
              <Box sx={{ width: COLLAPSED_WIDTH, ...styles.sideDrawer }}>
                <Box className='inner-side-drawer'>
                  <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', marginTop: 'var(--ds-space-3)' }}>
                    <Link href={homeUrl} passHref>
                      {/* eslint-disable-next-line @next/next/no-img-element */}
                      {/* oxlint-disable nextjs/no-img-element -- dynamic branding URL; next/image requires static domain allowlist */}
                      {!brandingLoading && (
                        <img
                          src={logoSrc}
                          alt={baseTitle}
                          aria-label={baseTitle}
                          width={50}
                          height={40}
                          style={{ maxWidth: ds.space.mul(0, 25), maxHeight: ds.space.mul(1, 10), objectFit: 'contain' }}
                        />
                      )}
                      {/* oxlint-enable nextjs/no-img-element */}
                    </Link>
                  </Box>
                  <Box sx={styles.separator} />

                  {menuItems.map((item, idx) => (
                    <React.Fragment key={item.id || `${item.text}-${idx}`}>
                      {['Troubleshoot', 'Infra', 'Tickets'].includes(item.text) && <Box sx={styles.subSeparator} />}
                      <SideDrawerButton
                        item={item}
                        isFlyoutOpen={!!flyout && flyout.item.id === item.id}
                        onHoverOpen={openFlyout}
                        onHoverClose={scheduleFlyoutClose}
                      />
                    </React.Fragment>
                  ))}

                  <SubNavFlyout
                    item={flyout?.item}
                    anchorEl={flyout?.anchorEl}
                    onMouseEnter={cancelFlyoutClose}
                    onMouseLeave={scheduleFlyoutClose}
                    onNavigate={handleFlyoutNavigate}
                  />

                  {/* Auto-launches the first-login sidebar walkthrough once; renders nothing. */}
                  <FirstLoginTour />

                  {/* Offers a section's guided tour on first visit (Troubleshoot); renders nothing. */}
                  <SectionFirstVisitTour />

                  <Box sx={styles.userMenuContainer}>
                    <Box sx={{ mb: 'var(--ds-space-3)' }}>
                      <NubiBrainNav surface='dark' />
                    </Box>
                    <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
                      <IconButton
                        id='account-setting'
                        onMouseEnter={openUserMenu}
                        onMouseLeave={scheduleUserMenuClose}
                        onClick={openUserMenu}
                        size='small'
                      >
                        <Box>
                          <SafeIcon alt='Settings Icon' src={ProfileOutlineIcon} width={16} height={16} />
                        </Box>
                      </IconButton>

                      {getUserSession()?.tenant?.name && (
                        <Tooltip title={getUserSession()?.tenant?.name} placement='right'>
                          <Typography
                            data-testid='sidebar-tenant-name'
                            sx={{
                              fontSize: 'var(--ds-text-caption)',
                              fontWeight: 'var(--ds-font-weight-semibold)',
                              color: ds.background[100],
                              maxWidth: ds.space.mul(1, 12),
                              textAlign: 'center',
                              mb: 'var(--ds-space-1)',
                            }}
                          >
                            {getUserSession()?.tenant?.name}
                          </Typography>
                        </Tooltip>
                      )}
                      <Menu
                        id='menu-appbar'
                        sx={{ pointerEvents: 'none' }}
                        anchorEl={anchorElUser}
                        anchorOrigin={{ vertical: 'top', horizontal: 'right' }}
                        keepMounted
                        transformOrigin={{ vertical: 'top', horizontal: 'left' }}
                        open={Boolean(anchorElUser)}
                        onClose={() => setAnchorElUser(null)}
                        autoFocus={false}
                        disableAutoFocus
                        disableEnforceFocus
                        disableRestoreFocus
                        slotProps={{
                          paper: {
                            onMouseEnter: cancelUserMenuClose,
                            onMouseLeave: scheduleUserMenuClose,
                            sx: {
                              pointerEvents: 'auto',
                              minWidth: 360,
                              maxWidth: 360,
                              maxHeight: 'none',
                              outline: 'none',
                              border: 'none',
                              borderRadius: 'var(--ds-overlay-radius)',
                              boxShadow: 'var(--ds-overlay-shadow)',
                              backgroundColor: 'var(--ds-overlay-bg)',
                            },
                          },
                        }}
                        MenuListProps={{ sx: { outline: 'none', py: 'var(--ds-overlay-padding-y)' } }}
                      >
                        {avatarSubMenu.map((setting) => getMenuItem(setting))}
                      </Menu>
                    </Box>
                  </Box>
                </Box>
              </Box>
            )}

            <Box sx={{ display: 'flex', flexDirection: 'column', width: '100%' }}>
              {!isRenderedInIframe() && !pageFlags.isWorkflow && (
                <ErrorBoundary resetKey={router.pathname}>
                  <Header1 />
                </ErrorBoundary>
              )}
              <Box
                sx={{
                  maxWidth: `calc(100vw - ${COLLAPSED_WIDTH}px - ${ds.space.mul(0, 45)})`,
                  width: `calc(100vw - ${COLLAPSED_WIDTH}px - ${ds.space.mul(0, 42)})`,
                  px: pageFlags.isAskNudgebee || pageFlags.isAskNudgebeeV2 ? 0 : ds.space.mul(1, 10),
                  backgroundColor:
                    pageFlags.isOptimize || pageFlags.isTroubleshoot || pageFlags.isAgentic
                      ? ds.background[100]
                      : pageFlags.isAskNudgebee
                      ? ds.background[100]
                      : ds.background[300],
                  ...styles.body,
                  position: 'relative',
                  paddingBottom: isPaddedLayout ? ds.space[3] : 0,
                }}
              >
                <Container maxWidth={false} sx={{ maxWidth: ds.space.mul(0, 900) }} style={{ paddingInline: 0 }}>
                  <ErrorBoundary resetKey={router.asPath}>{children}</ErrorBoundary>
                </Container>
              </Box>
            </Box>
          </Box>
          {!isRenderedInIframe() && renderSlot('LayoutFloatingOverlay')}
        </>
      )}
    </>
  );
};

export default withAuth(PageLayout);

// Styles
const styles = {
  sideDrawer: {
    zIndex: 100,
    backgroundColor: 'var(--ds-sidebar-bg, var(--ds-brand-600))',
    minHeight: '100vh',
    transition: 'all ease 0.2s',
    boxShadow: 'none',
    display: 'flex',
    justifyContent: 'start',
    alignItems: 'center',
    flexDirection: 'column',
    p: 0,
    pt: 0,
    position: 'sticky',
    top: 0,
    '& .inner-side-drawer': {
      position: 'sticky',
      display: 'flex',
      flexDirection: 'column',
      justifyContent: 'center',
      alignItems: 'center',
      gap: 'var(--ds-space-0)',
      overflow: 'hidden',
      top: 0,
      height: '100vh',
    },
    '& button': {
      py: 'var(--ds-space-4)',
      width: ds.space.mul(1, 19),
      height: ds.space.mul(1, 15),
      display: 'flex',
      justifyContent: 'center',
      textAlign: 'left',
      borderRadius: 0,
      transition: 'background-color 0.2s ease',
      '@media (max-width:1535px)': {
        py: 'var(--ds-space-2)',
        height: ds.space.mul(1, 13),
      },
      '&:hover': {
        backgroundColor: NAV_ITEM_HOVER_BG,
      },
    },
  },
  flyoutPaper: {
    pointerEvents: 'auto',
    minWidth: ds.space.mul(0, 110),
    backgroundColor: SIDEBAR_FLYOUT_BG,
    backgroundImage: 'none',
    color: ds.background[100],
    borderRadius: '0 var(--ds-overlay-radius) var(--ds-overlay-radius) 0',
    border: 'none',
    boxShadow: 'none',
    overflow: 'hidden',
    padding: `var(--ds-space-2) 0`,
    animation: 'sidenavFlyoutEnter var(--ds-overlay-enter-duration) var(--ds-overlay-enter-easing)',
    '@keyframes sidenavFlyoutEnter': {
      '0%': { opacity: 0, transform: `translateX(${ds.space.mul(0, -4)})` },
      '100%': { opacity: 1, transform: 'translateX(0)' },
    },
  },
  flyoutItem: {
    fontSize: 'var(--ds-text-body)',
    color: SIDEBAR_FLYOUT_TEXT,
    minHeight: 'unset',
    borderRadius: 'var(--ds-radius-md)',
    display: 'flex',
    alignItems: 'center',
    gap: 'var(--ds-space-3)',
    mx: 'var(--ds-space-2)',
    padding: 'var(--ds-space-2) var(--ds-space-4) var(--ds-space-2) var(--ds-space-3)',
    transition: `background-color ${ds.motion.micro} ${ds.motion.ease}, color ${ds.motion.micro} ${ds.motion.ease}, border-left-color ${ds.motion.micro} ${ds.motion.ease}`,
    '&:hover': { backgroundColor: FLYOUT_ITEM_HOVER_BG, color: ds.background[100], '& .flyout-item-icon': { opacity: 1 } },
    '&.Mui-selected': {
      backgroundColor: NAV_ITEM_HIGHLIGHT_BG,
      // blue-400 over blue-500 — the deeper blue muddies against the dark panel.
      borderLeftColor: ds.blue[400],
      color: ds.background[100],
      fontWeight: 'var(--ds-font-weight-semibold)',
      '& .flyout-item-icon': { opacity: 1 },
      '&:hover': { backgroundColor: FLYOUT_ITEM_HOVER_BG },
    },
  },
  flyoutItemIcon: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
    width: `${FLYOUT_ICON_PX}px`,
    height: `${FLYOUT_ICON_PX}px`,
    opacity: 0.72,
    transition: `opacity ${ds.motion.micro} ${ds.motion.ease}`,
    '& img, & svg': { filter: 'brightness(0) invert(1)' },
  },
  body: {
    transition: 'ease 0.2s',
    flexGrow: 1,
    display: 'flex',
    alignItems: 'center',
    flexDirection: 'column',
  },
  railButton: {
    width: `${COLLAPSED_WIDTH}px`,
    minWidth: 0,
    borderRadius: 0,
    px: 0,
    '&:hover': { backgroundColor: NAV_ITEM_HOVER_BG },
  },
  railButtonFlyoutOpen: {
    backgroundColor: NAV_ITEM_HOVER_BG,
  },
  activeButton: {
    background: NAV_ITEM_HIGHLIGHT_BG,
  },
  activeIndicator: {
    width: ds.space[1],
    height: '100%',
    position: 'absolute',
    left: 0,
    background: ds.yellow[500],
  },
  iconContainer: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    gap: 0,
  },
  iconWrapper: {
    width: ds.space.mul(0, 10),
    height: ds.space.mul(0, 10),
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    position: 'relative',
    '@media (max-width:1535px)': {
      width: ds.space.mul(0, 9),
      height: ds.space.mul(0, 9),
    },
  },
  iconLabel: {
    paddingTop: 'var(--ds-space-3)',
    paddingBottom: 'var(--ds-space-1)',
    lineHeight: ds.space[1],
    textTransform: 'capitalize',
    fontFamily: 'Roboto',
    fontWeight: 'var(--ds-font-weight-regular)',
    fontSize: '10px',
    color: ds.background[100],
    '@media (max-width:1535px)': {
      fontSize: '10px',
    },
  },
  separator: {
    width: ds.space.mul(0, 23),
    marginY: ds.space[1],
    height: '0.5px',
    background: ds.background[100],
    display: 'list-item',
    '::marker': { content: '""' },
  },
  subSeparator: {
    width: ds.space.mul(0, 23),
    marginY: ds.space[1],
    height: '0.25px',
    opacity: '50%',
    background: ds.gray[400],
    display: 'list-item',
    '::marker': { content: '""' },
  },
  userMenuContainer: {
    marginTop: 'auto',
    paddingBottom: 'var(--ds-space-2)',
    '& button': {
      height: ds.space.mul(1, 5),
      py: 'var(--ds-space-4)',
    },
  },
};
