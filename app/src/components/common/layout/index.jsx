import * as React from 'react';
import { useState, useMemo, useEffect } from 'react';
import { useTenantBranding, DEFAULT_LOGO, DEFAULT_FAVICON } from '@hooks/useTenantBranding';
import Box from '@mui/material/Box';
import { Button, Collapse, Container, Typography, Menu, IconButton } from '@mui/material';
import { useRouter } from 'next/router';
import { KeyboardArrowDownRounded } from '@mui/icons-material';
import { signOut } from 'next-auth/react';
import Head from 'next/head';
import Link from 'next/link';
import { renderSlot } from '@lib/slots';

// Internal Imports
import { LayoutHeaderActionSlot } from '@shared/layout/LayoutHeaderActionSlot';
import { getUserSession, withAuth, hasAdminSurfaceAccess, isGrantsOnlyUser, hasPermission, missingPermissionMessage } from '@lib/auth';
import {
  homeIcon1,
  KubernetesClusterIcon,
  ticketsIcon1,
  troubleshootIcon1,
  AdminIcon,
  ProfileOutlineIcon,
  CloudAccountIcon,
  WhiteOptimizeIcon,
  WorkflowIconWhite,
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

const SideDrawerButton = ({ open = false, item = {}, onClick, handleDrawerOpen }) => {
  const router = useRouter();
  const haveSubItems = !!item?.subItems?.length;

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
    // 0. Permission-disabled item: swallow the click entirely (no navigate, no
    // drawer-open). The Button also carries pointer-events:none, but a keyboard
    // Enter still fires onClick, so guard here too.
    if (item.disabled) {
      e.preventDefault();
      return;
    }

    // 1. If sidebar is closed and item has sub-items, just open drawer
    if (!open && haveSubItems) {
      e.preventDefault();
      handleDrawerOpen();
      return;
    }

    // 2. Lazy Execution: Calculate dynamic path ONLY when clicked
    e.preventDefault(); // Stop default Link behavior (which would go to static item.path)

    const targetPath = getDynamicPath(item.path, router);

    // 3. Navigate programmatically
    const getFragmentFromUrl = () => {
      if (typeof window === 'undefined') {
        return null;
      }
      return window.location.hash.replace('#', '');
    };

    const isTroubleshootTab2 = router.pathname === '/troubleshoot' && getFragmentFromUrl() === 'kg';
    if (isTroubleshootTab2) {
      // navigation using router is blocked due to heavy library(elkjs) inside troubleshoot tab2
      window.location.assign(targetPath);
      return;
    }
    router.push(targetPath);
  };

  // Base button. When the item is permission-disabled it's greyed and made
  // non-interactive; the Tooltip below still fires because the hover is caught
  // by the wrapping <span> (the Button itself has pointer-events:none).
  const navButton = (
    <Button
      component={Link}
      // Disabled items shouldn't advertise a real href to assistive tech / hover.
      href={item.disabled ? '#' : item.path || '#'}
      onClick={handleLinkClick}
      className={isActive ? 'active-nav' : undefined}
      aria-label={item.text}
      aria-disabled={item.disabled || undefined}
      id={item?.id}
      sx={
        item.disabled ? { ...(isActive ? styles.activeButton : {}), opacity: 0.4, pointerEvents: 'none' } : isActive ? styles.activeButton : undefined
      }
    >
      {isActive && <Box sx={styles.activeIndicator} />}

      <Box sx={styles.iconContainer}>
        <Box sx={styles.iconWrapper}>
          <SafeIcon src={item.icon} alt={item.text} fill style={{ objectFit: 'contain' }} />
        </Box>

        <Typography sx={styles.iconLabel}>{item.text}</Typography>
      </Box>

      {open && (
        <Box component='span' sx={styles.openTextContainer}>
          <span>{item.text}</span>
          <span className='sub-text'>{item.subText}</span>
        </Box>
      )}

      {open && haveSubItems && <KeyboardArrowDownRounded sx={{ height: 10, transition: 'all 0.2s ease' }} />}
    </Button>
  );

  return (
    <React.Fragment>
      {/* We keep item.path here for semantic HTML, but override the click */}
      {item.disabled && item.disabledTooltip ? (
        <Tooltip title={item.disabledTooltip} placement='right'>
          <span style={{ display: 'block' }}>{navButton}</span>
        </Tooltip>
      ) : (
        navButton
      )}
      {haveSubItems && (
        <Collapse in={open}>
          <Box className='collapsable'>
            {item.subItems?.map((sub, idx) => (
              <Button key={`${sub.text}-${idx}`} onClick={() => onClick(sub.path)} className={`menu-item sub-item`}>
                <Box sx={{ width: ds.space.mul(1, 5), height: ds.space.mul(1, 5), position: 'relative' }}>
                  <SafeIcon priority={true} src={sub.icon} alt={sub.text} fill style={{ objectFit: 'contain' }} />
                </Box>
                {open && (
                  <Box component='span' sx={{ flexGrow: 1, whiteSpace: 'nowrap' }}>
                    {sub.text}
                  </Box>
                )}
                {open && sub.haveSubItems && <KeyboardArrowDownRounded />}
              </Button>
            ))}
          </Box>
        </Collapse>
      )}
    </React.Fragment>
  );
};

const PageLayout = ({ children }) => {
  const router = useRouter();

  // State
  const [open, setOpen] = useState(false);
  const [anchorElUser, setAnchorElUser] = useState(null);
  const [openSwitchAccount, setOpenSwitchAccount] = useState(false);
  const [openSettings, setOpenSettings] = useState(false);
  const [openApiTokens, setOpenApiTokens] = useState(false);

  // Let any component (e.g. the cross-tenant AccountGuard) request the tenant
  // switcher open without prop-drilling. subscribe() returns its unsubscribe
  // fn, which becomes the effect cleanup.
  useEffect(() => {
    return tenantSwitcher.subscribe(() => setOpenSwitchAccount(true));
  }, []);

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
    // `module` is the dynamic-RBAC permission module (permissionCatalog.ts) that
    // backs each product area — it drives the disabled-icon gating below. Home is
    // gated on `insights` (its landing surfaces account insights). b-Cortex/
    // Settings/Nudgebee (rendered separately) carry none.
    const items = [
      { path: '/home', icon: homeIcon1, text: 'Home', id: 'home-sidenavbutton', module: 'insights' },
      {
        path: '/troubleshoot',
        activePaths: ['/investigate', '/agentHealth'],
        icon: troubleshootIcon1,
        text: 'Troubleshoot',
        id: 'troubleshoot-sidenavbutton',
        module: 'events',
      },
      { path: '/automation', icon: WorkflowIconWhite, text: 'Automations', id: 'auto-pilot-sidenavbutton', module: 'workflows' },
      { path: '/optimise', icon: WhiteOptimizeIcon, text: 'Optimize', id: 'optimize-sidenavbutton', module: 'recommendations' },
      { path: '/kubernetes', icon: KubernetesClusterIcon, text: 'Clusters', haveSubItems: true, id: 'clusters-sidenavbutton', module: 'k8s' },
      { path: '/cloud-account', icon: CloudAccountIcon, text: 'Cloud', haveSubItems: true, id: 'cloud-sidenavbutton', module: 'cloud' },
      { path: '/tickets', icon: ticketsIcon1, text: 'Tickets', id: 'tickets-sidenavbutton', module: 'tickets' },
    ];
    if (hasAdminSurfaceAccess()) {
      items.push({ path: '/user-management', activePaths: ['/accounts'], icon: AdminIcon, text: 'Admin', id: 'admin-sidenav' });
    }

    // Per-module nav gating applies ONLY to grants-only custom-role users — those
    // whose access comes purely from dynamic-RBAC grants, with no tenant-wide role
    // and no account access. Tenant admins and any account user keep every icon
    // (these product areas are account-scoped and theirs by role). For a grants-only
    // user, an icon they lack `<module>:Read` for renders disabled (greyed) with a
    // request-access tooltip rather than being hidden, so the capability stays
    // discoverable. Admin is already gated by its own hasAdminSurfaceAccess() push.
    //
    // Demo bypass: the shared `demo` account (`value === 'demo'`) is the
    // unrestricted product showcase — when it's the selected account, every icon
    // stays enabled regardless of grants, so the demo is never hobbled. The whole
    // block is also inert while the tenant's CUSTOM_ROLES feature is off (see
    // isGrantsOnlyUser), so the nav looks exactly as it did pre-dynamic-RBAC.
    if (!isGrantsOnlyUser(selectedCluster?.value)) return items;
    return items.map((item) =>
      item.module && !hasPermission(item.module, 'Read')
        ? { ...item, disabled: true, disabledTooltip: missingPermissionMessage(`${item.module}:Read`) }
        : item
    );
    // selectedCluster?.value re-evaluates the demo bypass on account switch.
  }, [selectedCluster?.value]);

  // Route/Page Type Detection
  const pageFlags = useMemo(
    () => ({
      isAskNudgebee: router.pathname === '/ask-nudgebee',
      isAskNudgebeeV2: router.pathname === '/ask-nudgebee-v2',
      isInvestigate: router.pathname?.includes('/investigate') || router.pathname?.includes('/investigate2'),
      isWorkflow: router.pathname === '/workflow' || router.pathname.startsWith('/workflow/'),
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
  const handleDrawerOpen = () => setOpen(true);
  const handleSwitchAccountClose = () => setOpenSwitchAccount(false);

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

  const onMenuClick = (path) => {
    if (path) {
      router.push(path);
    }
    if (open) {
      setOpen(!open);
    }
  };

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
                      {['Troubleshoot', 'Clusters', 'Tickets'].includes(item.text) && <Box sx={styles.subSeparator} />}
                      <SideDrawerButton open={open} item={item} onClick={onMenuClick} handleDrawerOpen={handleDrawerOpen} />
                    </React.Fragment>
                  ))}

                  {/* Auto-launches the first-login sidebar walkthrough once; renders nothing. */}
                  <FirstLoginTour />

                  {/* Offers a section's guided tour on first visit (Troubleshoot); renders nothing. */}
                  <SectionFirstVisitTour />

                  <Box sx={styles.userMenuContainer}>
                    <Box sx={{ mb: 'var(--ds-space-3)' }}>
                      <NubiBrainNav surface='dark' />
                    </Box>
                    <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
                      <IconButton id='account-setting' onClick={(e) => setAnchorElUser(e.currentTarget)} size='small'>
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
                        sx={{ '.css-1xyun6z-MuiPaper-root-MuiPopover-paper-MuiMenu-paper': { left: '62px !important' } }}
                        anchorEl={anchorElUser}
                        anchorOrigin={{ vertical: 'top', horizontal: 'right' }}
                        keepMounted
                        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
                        open={Boolean(anchorElUser)}
                        onClose={() => setAnchorElUser(null)}
                        slotProps={{
                          paper: {
                            sx: {
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
                  px: open ? ds.space.mul(1, 16) : pageFlags.isAskNudgebee || pageFlags.isAskNudgebeeV2 ? 0 : ds.space.mul(1, 10),
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
    boxShadow: `${ds.space[0]} 0 ${ds.space[0]} 0 color-mix(in srgb, ${ds.gray[700]} 25%, transparent)`,
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
    '& .collapsable': {
      display: 'flex',
      flexDirection: 'column',
      gap: 'var(--ds-space-2)',
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
      '&:not(.active-nav):hover': {
        backgroundColor: 'rgba(0, 0, 0, 0.3)',
      },
      '&.menu-item': {
        borderBottom: 'none',
        justifyContent: 'flex-start',
        gap: 'var(--ds-space-3)',
        borderRadius: 'var(--ds-radius-xl)',
        color: ds.gray[400],
        fontSize: 'var(--ds-text-small)',
        lineHeight: ds.space.mul(0, 8),
        fontWeight: 'var(--ds-font-weight-semibold)',
        textTransform: 'none',
        '&.sub-item': { pl: 'var(--ds-space-6)' },
        '& .sub-text': { fontSize: 'var(--ds-text-caption)', color: ds.gray[600] },
        svg: {
          minHeight: ds.space.mul(1, 5),
          minWidth: ds.space.mul(1, 5),
          height: ds.space.mul(1, 5),
          width: ds.space.mul(1, 5),
          '&.color-switching-icon': { path: { fill: ds.brand[500] } },
        },
        '&.selected': {
          backgroundColor: ds.brand[500],
          color: ds.background[100],
          svg: { '&.color-switching-icon': { path: { fill: ds.background[100] } } },
        },
      },
    },
  },
  body: {
    transition: 'ease 0.2s',
    flexGrow: 1,
    display: 'flex',
    alignItems: 'center',
    flexDirection: 'column',
  },
  activeButton: {
    background: ds.gray.alpha[200],
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
  openTextContainer: {
    flexGrow: 1,
    display: 'flex',
    flexDirection: 'column',
    whiteSpace: 'nowrap',
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
