/**
 * ButtonMenu — domain composition: a labelled MUI Button trigger with a
 * `ds/DropdownMenu` for its menu. Preserves the brand-specific Button color
 * variants (primary/tertiary/default) used across recommendations pages and
 * the page header — those colors are app-specific and outside the scope of
 * `ds/Button` today.
 *
 * Previously deprecated 2026-05-07 → demoted to domain composition 2026-05-07.
 * The deprecation header pointed callers at `<DropdownMenu trigger={<Button .../>}>`
 * directly, but the Button styling block (lines 99–108 of the original) would
 * be duplicated at all 10 call sites — anti-DS code spread. Internals now use
 * `ds/DropdownMenu` for the menu; trigger Button kept as MUI for the variants.
 *
 * For action-menu use cases without the brand-Button trigger, prefer
 * `{ DropdownMenu } from '@components1/ds/DropdownMenu'` directly.
 *
 * Item shape preserved from V1: `{ text, onClick, icon, disabled?, accountsCount? }`
 *   - `accountsCount > 0` is OR'd into `disabled` (legacy domain quirk).
 */
import * as React from 'react';
import Button from '@mui/material/Button';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import PropTypes from 'prop-types';
import { DropdownMenu } from '@components1/ds/DropdownMenu';

export default function ButtonMenu({ title, items = [], variant, size, sx }) {
  const triggerSx = {
    backgroundColor: variant === 'primary' ? '#FACF39' : variant === 'tertiary' ? '#FFFFFF' : '#3B82F6',
    border: `0.5px solid ${variant === 'primary' ? '#FACF39' : variant === 'tertiary' ? '#60A5FA' : '#3B82F6'}`,
    color: variant === 'primary' ? 'black' : variant === 'tertiary' ? '#3B82F6' : '',
    textTransform: 'none',
    height: size === 'medium' ? '34px' : 'auto',
    '&:hover': {
      backgroundColor: variant === 'primary' ? '#FACF39' : variant === 'tertiary' ? '#FFFFFF' : '#3B82F6',
      color: variant === 'primary' ? 'black' : '',
    },
    ...sx,
  };

  const dsItems = items.map((item, index) => ({
    id: item.text ?? `item-${index}`,
    label: item.text,
    icon: item.icon,
    onSelect: item.onClick ?? (() => {}),
    disabled: (item.disabled ?? false) || (item.accountsCount && item.accountsCount > 0),
  }));

  return (
    <DropdownMenu
      align='end'
      trigger={
        <Button id='buttonmenu-button' variant='contained' disableElevation endIcon={<KeyboardArrowDownIcon />} sx={triggerSx}>
          {title || 'Options'}
        </Button>
      }
      items={dsItems}
    />
  );
}

ButtonMenu.propTypes = {
  title: PropTypes.string,
  items: PropTypes.array,
  variant: PropTypes.oneOf(['primary', 'tertiary']),
  size: PropTypes.oneOf(['small', 'medium', 'large', 'xSmall']),
  sx: PropTypes.object,
};
