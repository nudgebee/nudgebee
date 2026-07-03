import { Box, Button, Container, Menu, Typography } from '@mui/material';
import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';
import { useRouter } from 'next/router';
import { PlusIconSecondary, ProfileOutlineIcon, ChatOutlineDarkIcon, ArrowBackGrayIcon } from '@assets';
import { getUserSession, withAuth } from '@lib/auth';
import { KeyboardArrowDownRounded } from '@mui/icons-material';
import { signOut } from 'next-auth/react';
import { LayoutHeaderActionSlot } from './LayoutHeaderActionSlot';
import { Button as DsButton } from '@ui/Button';
import apiAskNudgebee from '@api1/ask-nudgebee';
import NubiBrainNav from './NubiBrainNav';
import TenantSettings from '@shared/settings/TenantSettings';
import ApiTokens from '@shared/settings/ApiTokens';
import { createGetMenuItem, generateMenuItems } from './UserMenuItems';
import Head from 'next/head';
import { renderSlot } from '@lib/slots';
import SafeIcon from '@shared/icons/SafeIcon';
import Tooltip from '@ui/Tooltip';
import { useTenantBranding } from '@hooks/useTenantBranding';

const collapsedWidth = 68;

const SideDrawerButton = ({ open = false, item = {}, handleDrawerOpen, isFirstItem }) => {
  const router = useRouter();
  const haveSubItems = !!item?.subItems?.length;
  const currentPath = router.pathname === '/' ? '/' : router.pathname;
  const [isActive, setIsActive] = useState(item.path == '' ? false : currentPath.includes(item.path));

  useEffect(() => {
    if (item.path == '') {
      return;
    }
    const path = router.pathname === '/' ? '/' : router.pathname;
    setIsActive(path.startsWith(item.path));
  }, [open, router.asPath]);

  const handleButtonClick = () => {
    if (!open && haveSubItems) {
      handleDrawerOpen();
    } else {
      if (item.onClick) {
        item.onClick();
      } else {
        let path = item.path;
        if (router.query?.accountId) {
          path = path + '?accountId=' + router.query?.accountId;
        } else if (router.query?.KubernetesDetails) {
          path = path + '?accountId=' + router.query?.KubernetesDetails;
        }
        router.push(path);
      }
    }
  };

  return (
    <React.Fragment>
      <Button
        sx={{
          ...(isActive ? styles.activeButton : undefined),
          ...(isFirstItem && {
            '& > :first-child': {
              padding: 'var(--ds-space-2)',
              border: `1px solid ${ds.blue[300]}`,
              borderRadius: 'var(--ds-radius-xl)',
              marginTop: 'var(--ds-space-3)',
            },
          }),
        }}
        aria-labelledby={item.text}
        onClick={() => handleButtonClick()}
        // onMouseEnter={onMouseEnter}
        // onMouseLeave={onMouseLeave}
      >
        {isActive && <Box sx={{ width: ds.space[1], height: '100%', position: 'absolute', left: 0, background: 'var(--ds-yellow-500)' }} />}
        <Box
          sx={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            gap: 0,
          }}
        >
          <Box
            sx={{
              width: ds.space.mul(0, 13),
              height: ds.space.mul(0, 13),
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              position: 'relative',
              '@media (max-width:1535px)': {
                width: ds.space.mul(0, 9),
                height: ds.space.mul(0, 9),
              },
            }}
          >
            <SafeIcon
              priority
              src={item.icon}
              alt={item.text}
              aria-label={item.text}
              style={{ objectFit: 'contain', width: `${item.iconSize || 22}px`, height: `${item.iconSize || 22}px` }}
              width={item.iconSize || 22}
              height={item.iconSize || 18}
            />
          </Box>
          {item.text && (
            <Typography
              sx={{
                paddingTop: 'var(--ds-space-3)',
                lineHeight: ds.space[1],
                textTransform: 'capitalize',
                fontFamily: 'Roboto',
                fontWeight: 'var(--ds-font-weight-regular)',
                fontSize: 'var(--ds-text-caption)',
                color: ds.gray[600],
                '@media (max-width:1535px)': {
                  fontSize: 'var(--ds-text-caption)',
                },
              }}
            >
              {item.text}
            </Typography>
          )}
        </Box>

        {open && (
          <nobr style={{ flexGrow: 1, display: 'flex', flexDirection: 'column' }}>
            <span>{item.text}</span>
            <span className='sub-text'>{item.subText}</span>
          </nobr>
        )}
        {open && haveSubItems && <KeyboardArrowDownRounded style={{ height: 10, transform: `rotate(0deg)`, transition: 'all ease 0.2s' }} />}
      </Button>
    </React.Fragment>
  );
};

const AskNudgebeeLayout = ({
  children,
  handleNewChat,
  handleHomePage,
  handleToggle,
  onAgentsRefreshed,
  externalAgents = null,
  externalAgentsLoading = false,
}) => {
  const router = useRouter();
  // Routes that use this global layout may not carry an `accountId` query
  // param (e.g. when launched from the sidebar). Empty is OK — the
  // backend's ai_list_agents handler routes empty account_id to the
  // tenant-wide agent catalog, and SettingsModal / b-Cortex render their
  // own tenant-wide views in that case.
  const { accountId } = router.query;
  const { baseTitle } = useTenantBranding();

  const [open, setOpen] = useState(false);
  const [avatarSubMenu, setAvatarSubMenu] = useState(['UserInfo', 'Switch Tenant', 'Logout']);
  const [anchorElUser, setAnchorElUser] = useState(null);
  const [openSwitchAccount, setOpenSwitchAccount] = useState(false);
  const [openSettings, setOpenSettings] = useState(false);
  const [openApiTokens, setOpenApiTokens] = useState(false);
  const [internalAgents, setInternalAgents] = useState([]);
  const [internalLoading, setInternalLoading] = useState(false);
  const [_enabledAgents, setEnabledAgents] = useState([]);

  const effectiveAgents = externalAgents || internalAgents;
  const effectiveLoading = externalAgents ? externalAgentsLoading : internalLoading;

  const handleDrawerOpen = () => setOpen(true);

  const listAgents = () => {
    if (externalAgents) {
      return;
    }

    setInternalAgents([]);
    setInternalLoading(true);

    apiAskNudgebee.listAgents({ accountId }).then((res) => {
      let listAgentResponse = res?.data?.data?.ai_list_agents?.data ?? [];
      if (listAgentResponse.length > 0) {
        const agents = listAgentResponse
          .filter((agent) => agent.status === 'enabled')
          .map((agent) => {
            return { name: agent.name, display_name: agent.aliases?.[0] ?? agent.name };
          });
        setEnabledAgents(agents.sort());
        setInternalAgents(listAgentResponse);
      }
      setInternalLoading(false);
      if (onAgentsRefreshed) {
        onAgentsRefreshed();
      }
    });
  };

  useEffect(() => {
    // Empty accountId is valid post-collapse: the backend's
    // agentListAgent handler routes it to ListAgentsForTenant (system
    // catalog + every custom agent the caller can read across the
    // tenant). Drop the previous `accountId &&` gate so the sidebar
    // picker populates on tenant-wide layout entries too.
    if (!externalAgents && router.isReady) {
      listAgents();
    }
  }, [accountId, externalAgents, router.isReady]);

  useEffect(() => {
    const menu = generateMenuItems(getUserSession()?.hasMultipleTenantAccess || false);
    setAvatarSubMenu(menu);
  }, []);

  const handleSwitchAccountClose = () => {
    setOpenSwitchAccount(false);
  };

  const handleSubMenuClick = (subMenu) => {
    setAnchorElUser(null);
    // Perform actions based on the sub-menu item clicked
    // For example, you can router to different pages
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

  const onMenuClick = (onClick) => {
    if (onClick) {
      onClick();
    }
    if (open) {
      setOpen(!open);
    }
  };

  const menuItems = [
    { icon: ArrowBackGrayIcon, text: 'App', onClick: handleHomePage, iconSize: 16 },
    { icon: PlusIconSecondary, text: null, onClick: handleNewChat },
    {
      icon: ChatOutlineDarkIcon,
      text: 'Chats',
      onClick: handleToggle,
    },
  ];

  const sideDrawerWidth = collapsedWidth;

  // homeUrl construction removed as it was unused

  const isAskNudgebeePage = router.pathname?.includes('/ask-nudgebee');

  return (
    <>
      <Head>
        <title>{baseTitle}</title>
      </Head>
      {renderSlot('LayoutHeadExtras')}
      <LayoutHeaderActionSlot open={openSwitchAccount} title={'Switch Tenant'} onClose={handleSwitchAccountClose} />
      <TenantSettings
        open={openSettings}
        title={'Tenant Settings'}
        onClose={(_, _msg) => {
          setOpenSettings(false);
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
                    <SideDrawerButton
                      open={open}
                      item={item}
                      onClick={onMenuClick}
                      handleDrawerOpen={handleDrawerOpen}
                      isColorSwitchingIcon
                      isFirstItem={idx === 1}
                    />
                    {idx === 0 && <Box sx={{ borderTop: `1px solid ${ds.gray[300]}`, my: 'var(--ds-space-1)' }} />}
                  </React.Fragment>
                ))}
              </Box>
              <Box
                sx={{
                  marginTop: 'auto',
                  paddingBottom: 'var(--ds-space-2)',
                  gap: 'var(--ds-space-2)',
                  display: 'flex',
                  flexDirection: 'column',
                  '& button': {
                    height: `${ds.space.mul(0, 15)} !important`,
                    py: 'var(--ds-space-4)',
                  },
                }}
              >
                <NubiBrainNav
                  surface='light'
                  accountId={accountId}
                  agents={effectiveAgents}
                  loadingAgents={effectiveLoading}
                  onRefreshAgents={() => (externalAgents ? onAgentsRefreshed() : listAgents())}
                />

                <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
                  {getUserSession()?.tenant?.name && (
                    <Tooltip title={getUserSession()?.tenant?.name} placement='right'>
                      <Typography
                        data-testid='sidebar-tenant-name'
                        sx={{
                          fontSize: 'var(--ds-text-caption)',
                          fontWeight: 'var(--ds-font-weight-semibold)',
                          color: 'var(--ds-brand-300)',
                          maxWidth: ds.space.mul(1, 12),
                          textAlign: 'center',
                          mb: 'var(--ds-space-1)',
                        }}
                      >
                        {getUserSession()?.tenant?.name}
                      </Typography>
                    </Tooltip>
                  )}
                  <DsButton
                    tone='ghost'
                    size='sm'
                    composition='icon-only'
                    icon={<SafeIcon alt='Profile Icon' src={ProfileOutlineIcon} width={24} height={24} />}
                    aria-label='Account Settings'
                    tooltip='Account Settings'
                    tooltipPlacement='right'
                    onClick={handleOpenUserMenu}
                  />
                  <Menu
                    id='menu-appbar'
                    sx={{
                      '.css-1xyun6z-MuiPaper-root-MuiPopover-paper-MuiMenu-paper': {
                        left: '62px !important',
                      },
                    }}
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
                    {avatarSubMenu.map((setting) => getMenuItem(setting))}
                  </Menu>
                </Box>
              </Box>
            </Box>
          </Box>

          <Box sx={{ display: 'flex', flexDirection: 'column', width: '100%', position: 'sticky', top: 0 }}>
            <Box
              sx={{
                px: open ? ds.space.mul(1, 16) : isAskNudgebeePage ? 0 : ds.space.mul(1, 10),
                backgroundColor:
                  router.pathname == '/home' || router.pathname.includes('/investigate')
                    ? ds.background[100]
                    : isAskNudgebeePage
                    ? ds.background[100]
                    : ds.background[300],
                ...styles.body,
                position: 'relative',
                paddingBottom: isAskNudgebeePage ? 0 : ds.space.mul(1, 10),
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

AskNudgebeeLayout.propTypes = {
  children: PropTypes.node.isRequired,
  handleNewChat: PropTypes.func,
  handleHomePage: PropTypes.func,
  handleRecentChat: PropTypes.func,
  handleToggle: PropTypes.func,
  onAgentsRefreshed: PropTypes.func,
};

const styles = {
  sideDrawer: {
    zIndex: 10,
    backgroundColor: ds.background[300],
    transition: 'all ease 0.2s',
    display: 'flex',
    justifyContent: 'start',
    alignItems: 'center',
    flexDirection: 'column',
    borderRight: `0.5px solid ${ds.gray[300]}`,
    p: 0,

    '& .inner-side-drawer': {
      position: 'sticky',
      display: 'flex',
      flexDirection: 'column',
      justifyContent: 'center',
      alignItems: 'center',
      gap: 'var(--ds-space-1)',
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
      width: ds.space.mul(1, 17),
      height: ds.space.mul(0, 35),
      display: 'flex',
      justifyContent: 'center',
      textAlign: 'left',
      borderRadius: 0,
      '@media (max-width:1535px)': {
        py: 'var(--ds-space-2)',
        height: ds.space.mul(1, 13),
      },
      '&:hover': {
        backgroundColor: 'transparent',
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

        '&.sub-item': {
          pl: 'var(--ds-space-6)',
        },

        '& .sub-text': {
          fontSize: 'var(--ds-text-caption)',
          color: ds.gray[600],
        },

        svg: {
          minHeight: ds.space.mul(1, 5),
          minWidth: ds.space.mul(1, 5),
          height: ds.space.mul(1, 5),
          width: ds.space.mul(1, 5),
          '&.color-switching-icon': {
            path: {
              fill: ds.brand[500],
            },
          },
        },

        '&.selected': {
          backgroundColor: ds.brand[500],
          color: ds.background[100],
          svg: {
            '&.color-switching-icon': {
              path: {
                fill: ds.background[100],
              },
            },
          },
        },
      },
    },

    '& .premium-section-heading': {
      width: 'calc(100% + 32px)',
      ml: '-16px',
      my: 'var(--ds-space-4)',
      display: 'flex',
      alignItems: 'center',
      gap: 'var(--ds-space-2)',
      fontWeight: 'var(--ds-font-weight-medium)',
      color: 'var(--ds-brand-300)',
      textAlign: 'center',

      '& .line': {
        height: ds.space[1],
        backgroundColor: 'var(--ds-gray-200)',

        '&.line-2': {
          flexGrow: 1,
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
};

export default withAuth(AskNudgebeeLayout);
