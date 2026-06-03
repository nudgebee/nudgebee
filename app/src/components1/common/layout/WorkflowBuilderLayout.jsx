import { Box, Container, IconButton, Menu, Typography } from '@mui/material';
import React, { useState, useEffect } from 'react';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { useRouter } from 'next/router';
import { LogoIcon, addIconWhite, ProfileOutlineIcon, TracesIcon, assignmentBlackSvg } from '@assets';
import { getUserSession, withAuth } from '@lib/auth';
import { signOut } from 'next-auth/react';
import { LayoutHeaderActionSlot } from './LayoutHeaderActionSlot';
import TenantSettings from '@common/TenantSettings';
import ApiTokens from '@common/ApiTokens';
import { createGetMenuItem, generateMenuItems } from './UserMenuItems';
import Header1 from '@common/header/Header1';
import Head from 'next/head';
import { renderSlot } from '@lib/slots';
import SafeIcon from '@components1/common/SafeIcon';
import Tooltip from '@components1/ds/Tooltip';

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
              paddingTop: 'var(--ds-space-3)',
              lineHeight: '4px',
              textTransform: 'capitalize',
              fontFamily: 'Roboto',
              fontWeight: 'var(--ds-font-weight-regular)',
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-background-100)',
              '@media (max-width:1535px)': {
                fontSize: 'var(--ds-text-caption)',
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
  const [openSettings, setOpenSettings] = useState(false);
  const [openApiTokens, setOpenApiTokens] = useState(false);
  const [avatarSubMenu, setAvatarSubMenu] = useState(['UserInfo', 'Switch Tenant', 'Logout']);

  useEffect(() => {
    const session = getUserSession();
    setAvatarSubMenu(generateMenuItems(session?.hasMultipleTenantAccess || false));
  }, []);

  const handleSwitchAccountClose = () => {
    setOpenSwitchAccount(false);
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
      </Head>
      {renderSlot('LayoutHeadExtras')}
      <LayoutHeaderActionSlot open={openSwitchAccount} title={'Switch Tenant'} onClose={handleSwitchAccountClose} />
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
                    <Tooltip title={getUserSession()?.tenant?.name} placement='right'>
                      <Typography
                        data-testid='sidebar-tenant-name'
                        sx={{
                          fontSize: 'var(--ds-text-caption)',
                          fontWeight: 'var(--ds-font-weight-semibold)',
                          color: 'var(--ds-brand-300)',
                          maxWidth: '48px',
                          textAlign: 'center',
                          mb: 'var(--ds-space-1)',
                        }}
                      >
                        {getUserSession()?.tenant?.name}
                      </Typography>
                    </Tooltip>
                  )}
                  <Tooltip title='Account Settings' placement='left'>
                    <IconButton id='wf-layout-profile-btn' onClick={handleOpenUserMenu} size='small'>
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
    backgroundColor: 'var(--ds-brand-600)',
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
      gap: 'var(--ds-space-1)',
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
      py: 'var(--ds-space-2)',
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
    paddingBottom: 'var(--ds-space-2)',
    gap: 'var(--ds-space-2)',
    display: 'flex',
    flexDirection: 'column',
  },
};

export default withAuth(WorkflowBuilderLayout);
