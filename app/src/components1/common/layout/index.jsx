import * as React from 'react';
import { useState, useMemo, useEffect, useCallback } from 'react';
import { useTenantBranding, DEFAULT_LOGO, DEFAULT_FAVICON } from '@hooks/useTenantBranding';
import Box from '@mui/material/Box';
import { Button, Collapse, Container, Typography, Menu, IconButton, Tooltip, tooltipClasses } from '@mui/material';
import { useRouter } from 'next/router';
import { KeyboardArrowDownRounded } from '@mui/icons-material';
import { signOut } from 'next-auth/react';
import Head from 'next/head';
import Link from 'next/link';
import Script from 'next/script';

// Internal Imports
import { SwitchTenant } from '@components1/common/layout/SwitchTenant';
import { getUserSession, withAuth, hasReadAccess } from '@lib/auth';
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
import Header1 from '@common/header/Header1';
import ErrorBoundary from '@common/ErrorBoundary';
import SafeIcon from '@common/SafeIcon';
import CustomTooltip from '@common/CustomTooltip';
import ChatwootWidget from '@components1/ChatwootWidget';
import TenantSettings from '@common/TenantSettings';
import ApiTokens from '@common/ApiTokens';
import { snackbar } from '@common/snackbarService';
import { createGetMenuItem, generateMenuItems } from './UserMenuItems';
import { colors } from 'src/utils/colors';
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

  return (
    <React.Fragment>
      {/* We keep item.path here for semantic HTML, but override the click */}
      <Button
        component={Link}
        href={item.path || '#'}
        onClick={handleLinkClick}
        sx={isActive ? styles.activeButton : undefined}
        aria-label={item.text}
        id={item?.id}
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
      {haveSubItems && (
        <Collapse in={open}>
          <Box className='collapsable'>
            {item.subItems?.map((sub, idx) => (
              <Button key={`${sub.text}-${idx}`} onClick={() => onClick(sub.path)} className={`menu-item sub-item`}>
                <Box sx={{ width: '20px', height: '20px', position: 'relative' }}>
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

  // Derived Values
  const session = getUserSession();
  const { baseTitle, logoUrl: brandingLogoUrl, faviconUrl: brandingFaviconUrl, loading: brandingLoading } = useTenantBranding();

  // Logo with fallback to default on error
  const [logoSrc, setLogoSrc] = useState(brandingLogoUrl || DEFAULT_LOGO);
  useEffect(() => {
    setLogoSrc(brandingLogoUrl || DEFAULT_LOGO);
  }, [brandingLogoUrl]);
  const handleLogoError = useCallback(() => {
    setLogoSrc(DEFAULT_LOGO);
  }, []);

  // Favicon from config, fallback to default
  const favicon = brandingFaviconUrl || DEFAULT_FAVICON;

  const avatarSubMenu = useMemo(() => {
    return generateMenuItems(session?.hasMultipleTenantAccess || false);
  }, []);

  const menuItems = useMemo(() => {
    const items = [
      { path: '/home', icon: homeIcon1, text: 'Home', id: 'home-sidenavbutton' },
      {
        path: '/troubleshoot',
        activePaths: ['/investigate', '/agentHealth'],
        icon: troubleshootIcon1,
        text: 'Troubleshoot',
        id: 'troubleshoot-sidenavbutton',
      },
      { path: '/optimise', icon: WhiteOptimizeIcon, text: 'Optimize', id: 'optimize-sidenavbutton' },
      { path: '/kubernetes', icon: KubernetesClusterIcon, text: 'Clusters', haveSubItems: true, id: 'clusters-sidenavbutton' },
      { path: '/cloud-account', icon: CloudAccountIcon, text: 'Cloud', haveSubItems: true, id: 'cloud-sidenavbutton' },
      { path: '/auto-pilot', icon: WorkflowIconWhite, text: 'Automations', id: 'auto-pilot-sidenavbutton' },
      { path: '/tickets', icon: ticketsIcon1, text: 'Tickets', id: 'tickets-sidenavbutton' },
    ];
    if (hasReadAccess()) {
      items.push({ path: '/user-management', activePaths: ['/accounts'], icon: AdminIcon, text: 'Admin', id: 'admin-sidenav' });
    }
    return items;
  }, []);

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

  const renderGoogleAnalytics = () => (
    <>
      <Script id='google-analytics'>
        {`
          (function(w,d,s,l,i){w[l]=w[l]||[];w[l].push({'gtm.start':
          new Date().getTime(),event:'gtm.js'});var f=d.getElementsByTagName(s)[0],
          j=d.createElement(s),dl=l!='dataLayer'?'&l='+l:'';j.async=true;j.src=
          'https://www.googletagmanager.com/gtm.js?id='+i+dl;f.parentNode.insertBefore(j,f);
          })(window,document,'script','dataLayer','GTM-NSB63NXS');
        `}
      </Script>
      <noscript>
        <iframe
          src='https://www.googletagmanager.com/ns.html?id=GTM-NSB63NXS'
          height='0'
          width='0'
          style={{ display: 'none', visibility: 'hidden' }}
          title='GA Tags'
        />
      </noscript>
    </>
  );

  return (
    <>
      {isPlainLayout ? (
        <>
          <Head>
            {!brandingLoading && <link rel='icon' href={favicon} />}
            <title>{baseTitle}</title>
          </Head>
          {children}
        </>
      ) : (
        <>
          <Head>
            {!brandingLoading && <link rel='icon' href={favicon} />}
            <title>{baseTitle}</title>
            {!session?.onPrem && renderGoogleAnalytics()}
          </Head>

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
          <SwitchTenant open={openSwitchAccount} title={'Switch Tenant'} onClose={handleSwitchAccountClose} />

          <Box sx={{ display: 'flex', alignItems: 'stretch', justifyContent: 'center' }}>
            {!isRenderedInIframe() && !pageFlags.isWorkflow && (
              <Box sx={{ width: COLLAPSED_WIDTH, ...styles.sideDrawer }}>
                <Box className='inner-side-drawer'>
                  <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', marginTop: '12px' }}>
                    <Link href={homeUrl} passHref>
                      {/* eslint-disable-next-line @next/next/no-img-element */}
                      {!brandingLoading && (
                        <img
                          src={logoSrc}
                          onError={handleLogoError}
                          alt={baseTitle}
                          aria-label={baseTitle}
                          width={50}
                          height={40}
                          style={{ maxWidth: '50px', maxHeight: '40px', objectFit: 'contain' }}
                        />
                      )}
                    </Link>
                  </Box>
                  <Box sx={styles.separator} />

                  {menuItems.map((item, idx) => (
                    <React.Fragment key={item.id || `${item.text}-${idx}`}>
                      {['Troubleshoot', 'Clusters', 'Tickets'].includes(item.text) && <Box sx={styles.subSeparator} />}
                      <SideDrawerButton open={open} item={item} onClick={onMenuClick} handleDrawerOpen={handleDrawerOpen} />
                    </React.Fragment>
                  ))}

                  <Box sx={styles.userMenuContainer}>
                    <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
                      {getUserSession()?.tenant?.name && (
                        <CustomTooltip title={getUserSession()?.tenant?.name} placement='right'>
                          <Typography
                            data-testid='sidebar-tenant-name'
                            sx={{
                              fontSize: '10px',
                              fontWeight: 600,
                              color: colors.text.white,
                              maxWidth: '48px',
                              textAlign: 'center',
                              mb: '2px',
                            }}
                          >
                            {getUserSession()?.tenant?.name}
                          </Typography>
                        </CustomTooltip>
                      )}
                      <Tooltip
                        title='Account Settings'
                        placement='left'
                        slotProps={{
                          popper: {
                            sx: {
                              [`&.${tooltipClasses.popper}[data-popper-placement*="right"] .${tooltipClasses.tooltip}`]: {
                                marginLeft: '-12px',
                              },
                            },
                          },
                        }}
                      >
                        <IconButton id='account-setting' onClick={(e) => setAnchorElUser(e.currentTarget)} size='small'>
                          <Box>
                            <SafeIcon alt='Settings Icon' src={ProfileOutlineIcon} width={16} height={16} />
                          </Box>
                        </IconButton>
                      </Tooltip>
                      <Menu
                        id='menu-appbar'
                        sx={{ '.css-1xyun6z-MuiPaper-root-MuiPopover-paper-MuiMenu-paper': { left: '62px !important' } }}
                        anchorEl={anchorElUser}
                        anchorOrigin={{ vertical: 'top', horizontal: 'right' }}
                        keepMounted
                        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
                        open={Boolean(anchorElUser)}
                        onClose={() => setAnchorElUser(null)}
                        slotProps={{ paper: { sx: { minWidth: 360, maxWidth: 360, maxHeight: 'none' } } }}
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
                  maxWidth: `calc(100vw - ${COLLAPSED_WIDTH}px - 90px)`,
                  width: `calc(100vw - ${COLLAPSED_WIDTH}px - 84px)`,
                  px: open ? '64px' : pageFlags.isAskNudgebee || pageFlags.isAskNudgebeeV2 ? '0px' : '40px',
                  backgroundColor:
                    pageFlags.isHome || pageFlags.isInvestigate || pageFlags.isOptimize || pageFlags.isTroubleshoot || pageFlags.isAgentic
                      ? colors.background.home
                      : pageFlags.isAskNudgebee
                      ? colors.background.askNudgebeePage
                      : colors.background.pages,
                  ...styles.body,
                  position: 'relative',
                  paddingBottom: isPaddedLayout ? '12px' : '0px',
                }}
              >
                <Container maxWidth='1800px' style={{ paddingInline: 0 }}>
                  <ErrorBoundary resetKey={router.asPath}>{children}</ErrorBoundary>
                </Container>
              </Box>
            </Box>
          </Box>
          {!session?.onPrem && !isRenderedInIframe() && <ChatwootWidget />}
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
    backgroundColor: colors.background.sideBar,
    minHeight: '100vh',
    transition: 'all ease 0.2s',
    boxShadow: '2px 0 2px 0 rgba(0,0,0,0.25)',
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
      gap: '2px',
      overflow: 'hidden',
      top: 0,
      height: '100vh',
    },
    '& .collapsable': {
      display: 'flex',
      flexDirection: 'column',
      gap: '8px',
    },
    '& button': {
      py: '16px',
      width: '76px',
      height: '60px',
      display: 'flex',
      justifyContent: 'center',
      textAlign: 'left',
      borderRadius: '0px',
      '@media (max-width:1535px)': {
        py: '8px',
        height: '52px',
      },
      '&:hover': {
        backgroundColor: colors.secondary.default,
      },
      '&.menu-item': {
        borderBottom: 'none',
        justifyContent: 'flex-start',
        gap: '12px',
        borderRadius: '12px',
        color: colors.text.secondaryDark,
        fontSize: 13,
        lineHeight: '15px',
        fontWeight: 700,
        textTransform: 'none',
        '&.sub-item': { pl: '40px' },
        '& .sub-text': { fontSize: 8, color: colors.text.tertiary },
        svg: {
          minHeight: '20px',
          minWidth: '20px',
          height: '20px',
          width: '20px',
          '&.color-switching-icon': { path: { fill: colors.switchIconColor } },
        },
        '&.selected': {
          backgroundColor: colors.secondary.default,
          color: colors.white,
          svg: { '&.color-switching-icon': { path: { fill: colors.white } } },
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
    background: colors.background.activeButtonColor,
  },
  activeIndicator: {
    width: '4px',
    height: '100%',
    position: 'absolute',
    left: 0,
    background: 'var(--nb-color-sidebar-indicator)',
  },
  iconContainer: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    gap: '0px',
  },
  iconWrapper: {
    width: '22px',
    height: '22px',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    position: 'relative',
    '@media (max-width:1535px)': {
      width: '18px',
      height: '18px',
    },
  },
  iconLabel: {
    paddingTop: '12px',
    lineHeight: '4px',
    textTransform: 'capitalize',
    fontFamily: 'Roboto',
    fontWeight: 400,
    fontSize: '9px',
    color: colors.text.white,
    '@media (max-width:1535px)': {
      fontSize: '8px',
    },
  },
  openTextContainer: {
    flexGrow: 1,
    display: 'flex',
    flexDirection: 'column',
    whiteSpace: 'nowrap',
  },
  separator: {
    width: '46px',
    marginY: '4px',
    height: '0.5px',
    background: colors.background.white,
    display: 'list-item',
    '::marker': { content: '""' },
  },
  subSeparator: {
    width: '46px',
    marginY: '4px',
    height: '0.25px',
    opacity: '50%',
    background: colors.background.secondaryDark,
    display: 'list-item',
    '::marker': { content: '""' },
  },
  userMenuContainer: {
    marginTop: 'auto',
    paddingBottom: '10px',
    '& button': {
      height: '20px',
      py: '17px',
    },
  },
};
