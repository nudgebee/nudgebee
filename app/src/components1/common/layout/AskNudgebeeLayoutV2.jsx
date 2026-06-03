import { Box, Button, Container, IconButton, Menu, Typography } from '@mui/material';
import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { useRouter } from 'next/router';
import { PlusIconSecondary, ProfileOutlineIcon, ChatOutlineDarkIcon, SettingOutlineIcon, ArrowBackGrayIcon } from '@assets';
import { getUserSession, withAuth } from '@lib/auth';
import { KeyboardArrowDownRounded } from '@mui/icons-material';
import { signOut } from 'next-auth/react';
import { LayoutHeaderActionSlot } from './LayoutHeaderActionSlot';
import CustomButton from '@common/NewCustomButton';
import apiAskNudgebee from '@api1/ask-nudgebee';
import SettingsModal from '@components1/llm/SettingsModal';
import TenantSettings from '@common/TenantSettings';
import ApiTokens from '@common/ApiTokens';
import { createGetMenuItem, generateMenuItems } from './UserMenuItems';
import Head from 'next/head';
import { renderSlot } from '@lib/slots';
import SafeIcon from '@components1/common/SafeIcon';
import Tooltip from '@components1/ds/Tooltip';
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
              border: `1px solid #93C5FD`,
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
        {isActive && <Box sx={{ width: '4px', height: '100%', position: 'absolute', left: 0, background: 'var(--ds-yellow-500)' }} />}
        <Box
          sx={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            gap: '0px',
          }}
        >
          <Box
            sx={{
              width: '26px',
              height: '26px',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              position: 'relative',
              '@media (max-width:1535px)': {
                width: '18px',
                height: '18px',
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
                lineHeight: '4px',
                textTransform: 'capitalize',
                fontFamily: 'Roboto',
                fontWeight: 'var(--ds-font-weight-regular)',
                fontSize: 'var(--ds-text-caption)',
                color: colors.text.tertiary,
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
  const { accountId } = router.query;
  const { baseTitle } = useTenantBranding();

  const [open, setOpen] = useState(false);
  const [avatarSubMenu, setAvatarSubMenu] = useState(['UserInfo', 'Switch Tenant', 'Logout']);
  const [anchorElUser, setAnchorElUser] = useState(null);
  const [openSwitchAccount, setOpenSwitchAccount] = useState(false);
  const [openSettingsModal, setOpenSettingsModal] = useState(false);
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
    if (accountId && !externalAgents) {
      listAgents();
    }
  }, [accountId, externalAgents]);

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
      <SettingsModal
        open={openSettingsModal}
        onClose={() => setOpenSettingsModal(false)}
        accountId={accountId}
        allAgents={effectiveAgents}
        refreshAgentListing={() => (externalAgents ? onAgentsRefreshed() : listAgents())}
        loadingAgents={effectiveLoading}
      />
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
                    {idx === 0 && <Box sx={{ borderTop: `1px solid ${colors.border.secondaryLightest}`, my: 'var(--ds-space-1)' }} />}
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
                    height: '30px !important',
                    py: 'var(--ds-space-4)',
                  },
                }}
              >
                <Box>
                  <CustomButton
                    variant='secondary'
                    startIcon={<SafeIcon src={SettingOutlineIcon} height={20} width={20} alt={'settings'} />}
                    onClick={() => setOpenSettingsModal(true)}
                    sx={{
                      height: '28px',
                      width: '28px',
                      border: 'none',
                      boxShadow: 'none',
                      backgroundColor: 'transparent',
                      '&:hover': {
                        border: '0px',
                        backgroundColor: 'transparent',
                      },
                    }}
                    showTooltip
                    toolTipTitle={`Settings`}
                    tooltipPlacement='right'
                    marginLeft
                  />
                </Box>

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
                    <IconButton onClick={handleOpenUserMenu} size='small'>
                      <Box>
                        <SafeIcon alt='Profile Icon' src={ProfileOutlineIcon} width={24} height={24} />
                      </Box>
                    </IconButton>
                  </Tooltip>
                  <Menu
                    id='menu-appbar'
                    sx={{
                      '.css-1xyun6z-MuiPaper-root-MuiPopover-paper-MuiMenu-paper': {
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
                    {avatarSubMenu.map((setting) => getMenuItem(setting))}
                  </Menu>
                </Box>
              </Box>
            </Box>
          </Box>

          <Box sx={{ display: 'flex', flexDirection: 'column', width: '100%', position: 'sticky', top: '0px' }}>
            <Box
              sx={{
                px: open ? '64px' : isAskNudgebeePage ? '0px' : '40px',
                backgroundColor:
                  router.pathname == '/home' || router.pathname.includes('/investigate')
                    ? colors.background.home
                    : isAskNudgebeePage
                    ? colors.background.askNudgebeePage
                    : colors.background.pages,
                ...styles.body,
                position: 'relative',
                paddingBottom: isAskNudgebeePage ? '0px' : '40px',
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
    backgroundColor: colors.background.pages,
    transition: 'all ease 0.2s',
    display: 'flex',
    justifyContent: 'start',
    alignItems: 'center',
    flexDirection: 'column',
    borderRight: `0.5px solid ${colors.border.secondaryLightest}`,
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
    '& .collapsable': {
      display: 'flex',
      flexDirection: 'column',
      gap: 'var(--ds-space-2)',
    },

    '& button': {
      py: 'var(--ds-space-4)',
      width: '68px',
      height: '70px',
      display: 'flex',
      justifyContent: 'center',
      textAlign: 'left',
      borderRadius: '0px',
      '@media (max-width:1535px)': {
        py: 'var(--ds-space-2)',
        height: '52px',
      },
      '&:hover': {
        backgroundColor: colors.background.transparent,
      },
      '&.menu-item': {
        borderBottom: 'none',
        justifyContent: 'flex-start',
        gap: 'var(--ds-space-3)',
        borderRadius: 'var(--ds-radius-xl)',
        color: colors.text.secondaryDark,
        fontSize: 13,
        lineHeight: '15px',
        fontWeight: 'var(--ds-font-weight-semibold)',
        textTransform: 'none',

        '&.sub-item': {
          pl: 'var(--ds-space-6)',
        },

        '& .sub-text': {
          fontSize: 8,
          color: colors.text.tertiary,
        },

        svg: {
          minHeight: '20px',
          minWidth: '20px',
          height: '20px',
          width: '20px',
          '&.color-switching-icon': {
            path: {
              fill: colors.switchIconColor,
            },
          },
        },

        '&.selected': {
          backgroundColor: colors.secondary.default,
          color: colors.white,
          svg: {
            '&.color-switching-icon': {
              path: {
                fill: colors.white,
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
        height: 4,
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
    background: colors.background.activeButtonColor,
  },
};

export default withAuth(AskNudgebeeLayout);
