import React from 'react';
import { MenuItem, Typography, ListItemAvatar, Avatar, ListItemText } from '@mui/material';
import SafeIcon from '@shared/icons/SafeIcon';
import { signOut } from 'next-auth/react';
import { getUserSession, isTenantAdmin } from '@lib/auth';
import { SwitchTenentIconDark, LogoutIconDark, SettingsIcon, ApiIcon } from '@assets';
import { ds } from 'src/utils/colors';
import Tooltip from '@ui/Tooltip';

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
    <MenuItem
      sx={{
        padding: 'var(--ds-overlay-item-padding-md)',
        margin: '0 var(--ds-overlay-item-margin-x)',
        borderRadius: 'var(--ds-overlay-item-radius)',
      }}
      disabled={true}
    >
      <Tooltip title={isOverflowing ? version : ''}>
        <Typography
          ref={textRef}
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-regular)',
            color: 'var(--ds-gray-700)',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            width: '100%',
            pointerEvents: 'auto',
          }}
        >
          {displayText}
        </Typography>
      </Tooltip>
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
        <MenuItem
          key={setting}
          sx={{
            padding: 'var(--ds-overlay-item-padding-md)',
            margin: '0 var(--ds-overlay-item-margin-x)',
            borderRadius: 'var(--ds-overlay-item-radius)',
            borderBottom: '0.5px solid var(--ds-gray-200)',
            '&:hover': { backgroundColor: 'var(--ds-overlay-item-hover-bg)' },
            '&.Mui-disabled': { opacity: 1 },
          }}
          disabled={true}
        >
          <ListItemAvatar>
            {getUserSession()?.user?.image ? (
              <Avatar sx={{ width: ds.space.mul(0, 19), height: ds.space.mul(0, 19) }}>
                <SafeIcon src={SwitchTenentIconDark} alt='switch tenent' />
              </Avatar>
            ) : (
              <Avatar sx={{ width: ds.space.mul(0, 19), height: ds.space.mul(0, 19) }} />
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
                      fontSize: 'var(--ds-text-caption)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      color: 'var(--ds-blue-600)',
                      mt: 'var(--ds-space-1)',
                      px: 'var(--ds-space-1)',
                      py: 'var(--ds-space-1)',
                      backgroundColor: 'var(--ds-overlay-item-selected-bg)',
                      borderRadius: 'var(--ds-radius-sm)',
                      width: 'fit-content',
                    }}
                  >
                    {getUserSession()?.tenant?.name}
                  </Typography>
                )}
              </>
            }
            primaryTypographyProps={{
              fontSize: 'var(--ds-text-title)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
            }}
            secondaryTypographyProps={{
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-500)',
              component: 'div',
            }}
          />
        </MenuItem>
      );
    } else if (setting === 'Switch Tenant') {
      return (
        <MenuItem
          key={setting}
          sx={{
            padding: 'var(--ds-overlay-item-padding-md)',
            margin: '0 var(--ds-overlay-item-margin-x)',
            borderRadius: 'var(--ds-overlay-item-radius)',
            borderBottom: '0.5px solid var(--ds-gray-200)',
            '&:hover': { backgroundColor: 'var(--ds-overlay-item-hover-bg)' },
          }}
          onClick={() => {
            setAnchorElUser(null);
            setOpenSwitchAccount(true);
          }}
        >
          <Typography
            textAlign='left'
            fontSize={'var(--ds-text-body-lg)'}
            display={'flex'}
            alignItems={'center'}
            gap={'var(--ds-space-2)'}
            fontWeight={'400'}
            color={'var(--ds-gray-700)'}
          >
            <SafeIcon src={SwitchTenentIconDark} alt='switch tenent' /> Switch Tenant
          </Typography>
        </MenuItem>
      );
    } else if (setting === 'Logout') {
      return (
        <MenuItem
          key={setting}
          sx={{
            padding: 'var(--ds-overlay-item-padding-md)',
            margin: '0 var(--ds-overlay-item-margin-x)',
            borderRadius: 'var(--ds-overlay-item-radius)',
            borderBottom: '0.5px solid var(--ds-gray-200)',
            '&:hover': { backgroundColor: 'var(--ds-overlay-item-hover-bg)' },
          }}
          onClick={() => {
            setAnchorElUser(null);
            signOut({ callbackUrl: '/' });
          }}
        >
          <Typography
            textAlign='left'
            fontSize={'var(--ds-text-body-lg)'}
            display={'flex'}
            alignItems={'center'}
            gap={'var(--ds-space-2)'}
            fontWeight={'400'}
            color={'var(--ds-gray-700)'}
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
          sx={{
            padding: 'var(--ds-overlay-item-padding-md)',
            margin: '0 var(--ds-overlay-item-margin-x)',
            borderRadius: 'var(--ds-overlay-item-radius)',
            borderBottom: '0.5px solid var(--ds-gray-200)',
            '&:hover': { backgroundColor: 'var(--ds-overlay-item-hover-bg)' },
          }}
          onClick={() => {
            setAnchorElUser(null);
            setOpenSettings(true);
          }}
        >
          <Typography
            textAlign='left'
            fontSize={'var(--ds-text-body-lg)'}
            display={'flex'}
            alignItems={'center'}
            gap={'var(--ds-space-2)'}
            fontWeight={'400'}
            color={'var(--ds-gray-700)'}
          >
            <SafeIcon src={SettingsIcon} alt='settings' /> Settings
          </Typography>
        </MenuItem>
      );
    } else if (setting === 'API Tokens') {
      return (
        <MenuItem
          key={setting}
          sx={{
            padding: 'var(--ds-overlay-item-padding-md)',
            margin: '0 var(--ds-overlay-item-margin-x)',
            borderRadius: 'var(--ds-overlay-item-radius)',
            borderBottom: '0.5px solid var(--ds-gray-200)',
            '&:hover': { backgroundColor: 'var(--ds-overlay-item-hover-bg)' },
          }}
          onClick={() => {
            setAnchorElUser(null);
            setOpenApiTokens(true);
          }}
        >
          <Typography
            textAlign='left'
            fontSize={'var(--ds-text-body-lg)'}
            display={'flex'}
            alignItems={'center'}
            gap={'var(--ds-space-2)'}
            fontWeight={'400'}
            color={'var(--ds-gray-700)'}
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
