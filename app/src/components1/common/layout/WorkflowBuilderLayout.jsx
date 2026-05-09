import { Box, Container, IconButton, Menu, Tooltip, tooltipClasses, Typography } from '@mui/material';
import React, { useState, useEffect } from 'react';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { useRouter } from 'next/router';
import { LogoIcon, addIconWhite, ProfileOutlineIcon, TracesIcon, assignmentBlackSvg } from '@assets';
import { getUserSession, withAuth } from '@lib/auth';
import { signOut } from 'next-auth/react';
import { SwitchTenant } from './SwitchTenant';
import TenantSettings from '@common/TenantSettings';
import ApiTokens from '@common/ApiTokens';
import { createGetMenuItem, generateMenuItems } from './UserMenuItems';
import Header1 from '@common/header/Header1';
import Head from 'next/head';
import Script from 'next/script';
import SafeIcon from '@components1/common/SafeIcon';
import CustomTooltip from '@common/CustomTooltip';

const collapsedWidth = 76;

const SideDrawerButton = ({ item = {}, isFirstItem: _isFirstItem = false, isActive = false }) => {
  const isDisabled = item.disabled || false;

  return (
    <Box
      sx={{
        ...styles.buttonBase,
        cursor: isDisabled ? 'default' : 'pointer',
        opacity: isDisabled ? 0.5 : 1,
        backgroundColor: isActive ? colors.background.transparent : 'transparent',
        borderLeft: isActive ? `3px solid ${colors.primary.main}` : '3px solid transparent',
      }}
      onClick={(e) => {
        if (isDisabled) {
          e.preventDefault();
          return;
        }
        if (item.onClick) {
          item.onClick();
        }
      }}
    >
      <Box sx={styles.buttonContent}>
        <Box sx={styles.iconContainer}>
          <SafeIcon priority src={item.icon} alt={item.text || 'icon'} aria-label={item.text || 'icon'} fill style={{ objectFit: 'contain' }} />
        </Box>
        {item.text && (
          <Box
            sx={{
              paddingTop: '12px',
              lineHeight: '4px',
              textTransform: 'capitalize',
              fontFamily: 'Roboto',
              fontWeight: 400,
              fontSize: '11px',
              color: '#FFFFFF',
              '@media (max-width:1535px)': {
                fontSize: '8px',
              },
            }}
          >
            {item.text}
          </Box>
        )}
      </Box>
    </Box>
  );
};

const WorkflowBuilderLayout = ({ children, handleHomePage }) => {
  const router = useRouter();
  const { accountId: _accountId } = router.query;

  const [anchorElUser, setAnchorElUser] = useState(null);
  const [openSwitchAccount, setOpenSwitchAccount] = useState(false);
  const [switchAccountEnabled, setSwitchAccountEnabled] = useState(false);
  const [openSettings, setOpenSettings] = useState(false);
  const [openApiTokens, setOpenApiTokens] = useState(false);
  const [avatarSubMenu, setAvatarSubMenu] = useState(['UserInfo', 'Switch Tenant', 'Logout']);

  useEffect(() => {
    const session = getUserSession();
    const menu = generateMenuItems(switchAccountEnabled || session?.hasMultipleTenantAccess || false);
    setAvatarSubMenu(menu);
  }, [switchAccountEnabled]);

  const handleSwitchAccountClose = () => {
    setOpenSwitchAccount(false);
  };

  const handleSwitchAccountSuccess = () => {
    // do nothing
  };

  const onSwitchAccountEnabled = (tenants) => {
    if (tenants.length > 1) {
      setSwitchAccountEnabled(true);
    }
  };

  const handleSubMenuClick = (subMenu) => {
    setAnchorElUser(null);
    switch (subMenu) {
      case 'Logout':
        signOut({ callbackUrl: '/' });
        break;
      case 'Switch Tenant':
        setOpenSwitchAccount(true);
        break;
    }
  };

  const handleOpenUserMenu = (event) => {
    setAnchorElUser(event.currentTarget);
  };

  const getMenuItem = createGetMenuItem({
    setAnchorElUser,
    setOpenSwitchAccount,
    setOpenSettings,
    setOpenApiTokens,
    handleSubMenuClick,
  });

  const handleCloseUserMenu = () => {
    setAnchorElUser(null);
  };

  const handleNewWorkflow = () => {
    let path = '/workflow/new';
    if (_accountId) {
      path = path + '?accountId=' + _accountId;
    }
    router.push(path);
  };

  const handleWorkflows = () => {
    let path = '/workflow';
    if (_accountId) {
      path = path + '?accountId=' + _accountId;
    }
    router.push(path);
  };

  const handleTemplate = () => {
    // TODO: Add template navigation when ready
  };

  const getCurrentActiveItem = () => {
    const { pathname, query } = router;

    if (pathname.startsWith('/workflow/new') || query.workflowId === 'new') {
      return 'New';
    } else if (pathname.startsWith('/workflow')) {
      return 'Automations';
    } else if (pathname.startsWith('/home') || pathname.startsWith('/auto-pilot')) {
      return 'App';
    }
    return null;
  };

  const activeItem = getCurrentActiveItem();

  const menuItems = [
    { icon: LogoIcon, text: 'App', onClick: handleHomePage },
    { icon: addIconWhite, text: 'New', onClick: handleNewWorkflow },
    { icon: TracesIcon, text: 'Automations', onClick: handleWorkflows },
    { icon: assignmentBlackSvg, text: 'Template', onClick: handleTemplate, disabled: true }, // Keep template disabled for now
  ];

  const sideDrawerWidth = collapsedWidth;

  const isWorkflowBuilder = () => {
    const { pathname } = router;
    return pathname.startsWith('/workflow/');
  };

  return (
    <>
      <Head>
        <title>Automation Builder - Nudgebee</title>
        {!getUserSession()?.onPrem ? (
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
        ) : (
          <></>
        )}
      </Head>
      <SwitchTenant
        open={openSwitchAccount}
        title={'Switch Tenant'}
        onClose={handleSwitchAccountClose}
        onSuccess={handleSwitchAccountSuccess}
        onSwitchTenantEnabled={onSwitchAccountEnabled}
      />
      <TenantSettings
        open={openSettings}
        title={'Tenant Settings'}
        onClose={(_, msg) => {
          setOpenSettings(false);
          if (msg == 'show') {
            // Add snackbar if needed
          }
        }}
      />
      <ApiTokens open={openApiTokens} title={'API Tokens'} onClose={() => setOpenApiTokens(false)} />
      <Box sx={{ display: 'flex', flexDirection: 'column', width: '100%' }}>
        <Box sx={{ display: 'flex', alignItems: 'stretch', justifyContent: 'center' }}>
          <Box sx={{ width: sideDrawerWidth, ...styles.sideDrawer }}>
            <Box className='inner-side-drawer'>
              <Box>
                {menuItems?.map((item, idx) => (
                  <React.Fragment key={item.text + '-' + idx}>
                    <SideDrawerButton item={item} isFirstItem={idx === 0} isActive={activeItem === item.text} />
                  </React.Fragment>
                ))}
              </Box>
              <Box sx={styles.bottomSection}>
                <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
                  {getUserSession()?.tenant?.name && (
                    <CustomTooltip title={getUserSession()?.tenant?.name} placement='right'>
                      <Typography
                        data-testid='sidebar-tenant-name'
                        sx={{
                          fontSize: '10px',
                          fontWeight: 600,
                          color: '#94A3B8',
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
                    <IconButton onClick={handleOpenUserMenu} size='small'>
                      <Box>
                        <SafeIcon alt='Profile Icon' src={ProfileOutlineIcon} width={24} height={24} />
                      </Box>
                    </IconButton>
                  </Tooltip>
                  <Menu
                    id='menu-appbar'
                    sx={{
                      '& .MuiPaper-root': {
                        left: '62px !important',
                      },
                    }}
                    anchorEl={anchorElUser}
                    anchorOrigin={{
                      vertical: 'top',
                      horizontal: 'right',
                    }}
                    keepMounted
                    transformOrigin={{
                      vertical: 'top',
                      horizontal: 'right',
                    }}
                    open={Boolean(anchorElUser)}
                    onClose={handleCloseUserMenu}
                  >
                    {avatarSubMenu.map((setting) => getMenuItem(setting, getUserSession()?.hasMultipleTenantAccess || false))}
                  </Menu>
                </Box>
              </Box>
            </Box>
          </Box>

          <Box sx={{ display: 'flex', flexDirection: 'column', width: '100%', position: 'sticky', top: '0px' }}>
            {/* Use Header1 component for consistent header across all pages */}
            {!isWorkflowBuilder() && <Header1 />}
            <Box
              sx={{
                px: '0px',
                backgroundColor: colors.background.pages,
                ...styles.body,
                position: 'relative',
                paddingBottom: '0px',
              }}
            >
              <Container maxWidth='1800px' style={{ paddingInline: 0 }}>
                {children}
              </Container>
            </Box>
          </Box>
        </Box>
      </Box>
    </>
  );
};

WorkflowBuilderLayout.propTypes = {
  children: PropTypes.node.isRequired,
  handleHomePage: PropTypes.func,
};

const styles = {
  sideDrawer: {
    zIndex: 10,
    backgroundColor: '#1B2D4A',
    transition: 'all ease 0.2s',
    display: 'flex',
    justifyContent: 'start',
    alignItems: 'center',
    flexDirection: 'column',
    borderRight: `0.5px solid ${colors.border.secondary}`,
    p: 0,

    '& .inner-side-drawer': {
      position: 'sticky',
      display: 'flex',
      flexDirection: 'column',
      justifyContent: 'center',
      alignItems: 'center',
      gap: '2px',
      overflow: 'hidden',
      top: '0px',
      height: '100vh',
    },
  },
  buttonBase: {
    py: '0px',
    width: '76px',
    height: '70px',
    display: 'flex',
    justifyContent: 'center',
    alignItems: 'center',
    textAlign: 'center',
    borderRadius: '0px',
    transition: 'background-color 0.2s',
    '@media (max-width:1535px)': {
      py: '8px',
      height: '52px',
    },
    '&:hover': {
      backgroundColor: colors.background.transparent,
    },
  },
  buttonContent: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    gap: '0px',
  },
  iconContainer: {
    width: '20px',
    height: '24px',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    position: 'relative',
    '@media (max-width:1535px)': {
      width: '18px',
      height: '18px',
    },
  },

  body: {
    transition: 'ease 0.2s',
    flexGrow: 1,
    display: 'flex',
    alignItems: 'center',
    flexDirection: 'column',
  },
  bottomSection: {
    marginTop: 'auto',
    paddingBottom: '10px',
    gap: '10px',
    display: 'flex',
    flexDirection: 'column',
  },
};

export default withAuth(WorkflowBuilderLayout);
