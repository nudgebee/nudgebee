import React from 'react';
import { MenuItem, Typography, ListItemAvatar, Avatar, ListItemText } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { signOut } from 'next-auth/react';
import { getUserSession, isTenantAdmin } from '@lib/auth';
import { colors } from 'src/utils/colors';
import { SwitchTenentIconDark, LogoutIconDark, SettingsIcon, ApiIcon } from '@assets';
import CustomTooltip from '../CustomTooltip';

const VersionMenuItem = () => {
  const version = getUserSession()?.appVersion || 'N/A';

  const textRef = React.useRef(null);
  const [isOverflowing, setIsOverflowing] = React.useState(false);
  const displayText = `Version: ${version}`;
  React.useEffect(() => {
    const el = textRef.current;
    if (el) {
      setIsOverflowing(el.scrollWidth > el.clientWidth);
    }
  }, []);

  return (
    <MenuItem sx={{ p: '14px' }} disabled={true}>
      <CustomTooltip title={isOverflowing ? version : ''}>
        <Typography
          ref={textRef}
          sx={{
            fontSize: '14px',
            fontWeight: '400',
            color: colors.text.secondary,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            width: '100%',
            pointerEvents: 'auto',
          }}
        >
          {displayText}
        </Typography>
      </CustomTooltip>
    </MenuItem>
  );
};

/**
 * Creates a getMenuItem function with the provided handlers
 * @param {Object} params
 * @param {Function} params.setAnchorElUser
 * @param {Function} params.setOpenSwitchAccount
 * @param {Function} params.setOpenSettings
 * @param {Function} params.setOpenApiTokens
 * @param {Function} params.handleSubMenuClick
 * @returns {Function} getMenuItem function
 */
export const createGetMenuItem = ({ setAnchorElUser, setOpenSwitchAccount, setOpenSettings, setOpenApiTokens, handleSubMenuClick }) => {
  const getMenuItem = (setting) => {
    if (setting === 'UserInfo') {
      return (
        <MenuItem key={setting} sx={{ p: '14px', borderBottom: `1px solid ${colors.border.secondaryLight}` }}>
          <ListItemAvatar>
            {getUserSession()?.user?.image ? (
              <Avatar height={'38px'} width={'38px'}>
                <SafeIcon src={SwitchTenentIconDark} alt='switch tenent' />
              </Avatar>
            ) : (
              <Avatar height={'38px'} width={'38px'} />
            )}
          </ListItemAvatar>
          <ListItemText
            primary={getUserSession()?.user?.name}
            secondary={
              <>
                {getUserSession()?.user?.email}
                {getUserSession()?.tenant?.name && (
                  <Typography
                    component='span'
                    sx={{
                      display: 'block',
                      fontSize: '11px',
                      fontWeight: 500,
                      color: colors.text.secondary,
                      mt: '4px',
                      px: '6px',
                      py: '2px',
                      backgroundColor: colors.background.primaryLightest,
                      borderRadius: '4px',
                      width: 'fit-content',
                    }}
                  >
                    {getUserSession()?.tenant?.name}
                  </Typography>
                )}
              </>
            }
            primaryTypographyProps={{
              fontSize: '16px',
              fontWeight: '600',
              color: colors.text.secondary,
            }}
            secondaryTypographyProps={{
              fontSize: '12px',
              color: colors.text.secondaryDark,
              component: 'div',
            }}
          />
        </MenuItem>
      );
    } else if (setting === 'Switch Tenant') {
      return (
        <MenuItem
          key={setting}
          sx={{ p: '14px', borderBottom: `1px solid ${colors.border.secondaryLight}` }}
          onClick={() => {
            setAnchorElUser(null);
            setOpenSwitchAccount(true);
          }}
        >
          <Typography
            textAlign='left'
            fontSize={14}
            display={'flex'}
            alignItems={'center'}
            gap={'10px'}
            fontWeight={'400'}
            color={colors.text.secondary}
          >
            <SafeIcon src={SwitchTenentIconDark} alt='switch tenent' /> Switch Tenant
          </Typography>
        </MenuItem>
      );
    } else if (setting === 'Logout') {
      return (
        <MenuItem
          key={setting}
          sx={{ p: '14px', borderBottom: `1px solid ${colors.border.secondaryLight}` }}
          onClick={() => {
            setAnchorElUser(null);
            signOut({ callbackUrl: '/' });
          }}
        >
          <Typography
            textAlign='left'
            fontSize={14}
            display={'flex'}
            alignItems={'center'}
            gap={'10px'}
            fontWeight={'400'}
            color={colors.text.secondary}
          >
            <SafeIcon src={LogoutIconDark} alt='logout' /> Logout
          </Typography>
        </MenuItem>
      );
    } else if (setting === 'Version') {
      return <VersionMenuItem key={setting} />;
    } else if (setting === 'Settings') {
      return (
        <MenuItem
          key={setting}
          sx={{ p: '14px', borderBottom: `1px solid ${colors.border.secondaryLight}` }}
          onClick={() => {
            setAnchorElUser(null);
            setOpenSettings(true);
          }}
        >
          <Typography
            textAlign='left'
            fontSize={14}
            display={'flex'}
            alignItems={'center'}
            gap={'10px'}
            fontWeight={'400'}
            color={colors.text.secondary}
          >
            <SafeIcon src={SettingsIcon} alt='settings' /> Settings
          </Typography>
        </MenuItem>
      );
    } else if (setting === 'API Tokens') {
      return (
        <MenuItem
          key={setting}
          sx={{ p: '14px', borderBottom: `1px solid ${colors.border.secondaryLight}` }}
          onClick={() => {
            setAnchorElUser(null);
            setOpenApiTokens(true);
          }}
        >
          <Typography
            textAlign='left'
            fontSize={14}
            display={'flex'}
            alignItems={'center'}
            gap={'10px'}
            fontWeight={'400'}
            color={colors.text.secondary}
          >
            <SafeIcon src={ApiIcon} alt='api tokens' /> API Tokens
          </Typography>
        </MenuItem>
      );
    }
    return (
      <MenuItem key={setting} onClick={() => handleSubMenuClick(setting)}>
        <Typography textAlign='center'>{setting}</Typography>
      </MenuItem>
    );
  };

  getMenuItem.displayName = 'MenuItem';
  return getMenuItem;
};

/**
 * Generate the menu items array based on conditions
 * @param {Object} options
 * @returns {Array} Array of menu item names
 */
export const generateMenuItems = (hasMultipleTenantAccess = false) => {
  const menu = ['UserInfo'];

  if (hasMultipleTenantAccess) {
    menu.push('Switch Tenant');
  }
  if (isTenantAdmin()) {
    menu.push('Settings');
  }

  menu.push('API Tokens', 'Logout', 'Version');

  return menu;
};
