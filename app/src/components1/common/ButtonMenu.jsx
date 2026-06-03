import * as React from 'react';
import { styled, alpha } from '@mui/material/styles';
import Button from '@mui/material/Button';
import Menu from '@mui/material/Menu';
import MenuItem from '@mui/material/MenuItem';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';

import PropTypes from 'prop-types';

const StyledMenu = styled((props) => (
  <Menu
    elevation={0}
    anchorOrigin={{
      vertical: 'bottom',
      horizontal: 'right',
    }}
    transformOrigin={{
      vertical: 'top',
      horizontal: 'right',
    }}
    {...props}
  />
))(({ theme }) => ({
  '& .MuiPaper-root': {
    borderRadius: 6,
    marginTop: theme.spacing(1),
    minWidth: 180,
    color: theme.palette.mode === 'light' ? 'rgb(55, 65, 81)' : theme.palette.grey[300],
    textTransform: 'none',
    boxShadow:
      'rgb(255, 255, 255) 0px 0px 0px 0px, rgba(0, 0, 0, 0.05) 0px 0px 0px 1px, rgba(0, 0, 0, 0.1) 0px 10px 15px -3px, rgba(0, 0, 0, 0.05) 0px 4px 6px -2px',
    '& .MuiMenu-list': {
      padding: 'var(--ds-space-1) 0',
    },
    '& .MuiMenuItem-root': {
      '& .MuiSvgIcon-root': {
        fontSize: 18,
        color: theme.palette.text.secondary,
        marginRight: theme.spacing(1.5),
      },
      '&:active': {
        backgroundColor: alpha(theme.palette.primary.main, theme.palette.action.selectedOpacity),
      },
    },
  },
}));

export default function ButtonMenu(props) {
  const [anchorEl, setAnchorEl] = React.useState();
  const open = Boolean(anchorEl);

  const handleClick = (event) => {
    setAnchorEl(event.currentTarget);
  };

  const handleClose = () => {
    setAnchorEl(null);
  };

  const handleMenuItemClick = (onClick) => {
    if (onClick) {
      onClick();
    }
    handleClose();
  };

  return (
    <div>
      <Button
        id='buttonmenu-button'
        aria-controls={open ? 'buttonmenu-button' : undefined}
        aria-haspopup='true'
        aria-expanded={open ? 'true' : undefined}
        variant='contained'
        disableElevation
        onClick={handleClick}
        endIcon={<KeyboardArrowDownIcon />}
        sx={{
          backgroundColor: props.variant === 'primary' ? '#FACF39' : props.variant === 'tertiary' ? '#FFFFFF' : '#3B82F6',
          border: `0.5px solid ${props.variant === 'primary' ? '#FACF39' : props.variant === 'tertiary' ? '#60A5FA' : '#3B82F6'}`,
          color: props.variant === 'primary' ? 'black' : props.variant === 'tertiary' ? '#3B82F6' : '',
          textTransform: 'none',
          height: props.size === 'medium' ? '34px' : 'auto',
          '&:hover': {
            backgroundColor: props.variant === 'primary' ? '#FACF39' : props.variant === 'tertiary' ? '#FFFFFF' : '#3B82F6',
            color: props.variant === 'primary' ? 'black' : '',
          },
          ...props.sx,
        }}
      >
        {props.title || 'Options'}
      </Button>
      <StyledMenu
        id='buttonmenu-button'
        MenuListProps={{
          'aria-labelledby': 'buttonmenu-button',
        }}
        anchorEl={anchorEl}
        open={open}
        onClose={handleClose}
      >
        {props?.items.map((item) => {
          return (
            <MenuItem
              key={item.text}
              onClick={() => handleMenuItemClick(item.onClick)}
              disableRipple
              disabled={(item.disabled ?? false) || (item.accountsCount && item.accountsCount > 0)}
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: 'var(--ds-space-3)',
                p: 'var(--ds-space-2) var(--ds-space-4)',
                img: {
                  height: '18px',
                  width: '18px',
                },
                svg: {
                  height: '18px',
                  width: '18px',
                },
              }}
            >
              {item.icon}
              {item.text}
            </MenuItem>
          );
        })}
      </StyledMenu>
    </div>
  );
}

ButtonMenu.propTypes = {
  title: PropTypes.string,
  items: PropTypes.array,
  variant: PropTypes.oneOf(['primary']),
  size: PropTypes.oneOf(['small', 'medium', 'large', 'xSmall']),
  sx: PropTypes.object,
};
