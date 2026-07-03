/**
 * CustomTabsForDrilldown — thin wrapper around @common/CustomTabs with
 * `variant="secondary"` + `behavior="filter"`. Preserves the legacy public API
 * (flat `options` array, MUI-style `onChange(event, newValue)`, `rightButton`)
 * so call-sites migrate with a one-line import swap.
 * *
 * Right-button uses ds/Button (tone="primary", size="sm")
 * Design tokens (`var(--ds-*)`) only — no `colors.*` imports.
 */
import React from 'react';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import Tabs from '@shared/navigation/Tabs';
import { Button } from '@ui/Button';
import SafeIcon from '@shared/icons/SafeIcon';
import { AlphaIcon } from '@assets';
import { ds } from '@utils/colors';

/**
 *
 * @param {{
 *   value?: any,
 *   onChange: Function,
 *   options?: Array<any>,
 *   smallSize?: boolean,
 *   disableIndicatorTransition?: boolean,
 *   padding?: string,
 *   rightButton?: { visible?: boolean, text?: string, onClick?: Function, startIcon?: any }
 * }} props
 */
const TabsForDrilldown = ({
  value,
  onChange,
  options = [],
  smallSize: _smallSize = false,
  disableIndicatorTransition: _disableIndicatorTransition = false,
  padding: _padding,
  rightButton = { visible: false },
}) => {
  const tabOptions = options.map((opt) =>
    opt.alphaIcon
      ? {
          ...opt,
          text: (
            <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
              <span>{opt.text}</span>
              <Box component='span' sx={{ display: 'inline-flex', marginTop: ds.space.mul(0, -5) }}>
                <SafeIcon src={AlphaIcon} alt='Alpha icon' style={{ height: ds.space.mul(0, 15), width: ds.space.mul(0, 15) }} />
              </Box>
            </Box>
          ),
        }
      : opt
  );

  // Legacy callers expect MUI-style (event, newValue); CustomTabs emits (newValue).
  const handleChange = (newValue) => onChange?.(null, newValue);

  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--ds-space-2)',
        width: '100%',
      }}
    >
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <Tabs value={value} onChange={handleChange} options={{ tabOptions }} variant='secondary' behavior='filter' />
      </Box>
      {rightButton?.visible && (
        <Button tone='primary' size='sm' icon={rightButton.startIcon} onClick={rightButton.onClick}>
          {rightButton.text}
        </Button>
      )}
    </Box>
  );
};

TabsForDrilldown.propTypes = {
  value: PropTypes.any,
  onChange: PropTypes.func.isRequired,
  options: PropTypes.array,
  smallSize: PropTypes.bool,
  disableIndicatorTransition: PropTypes.bool,
  padding: PropTypes.string,
  rightButton: PropTypes.shape({
    visible: PropTypes.bool,
    text: PropTypes.string,
    onClick: PropTypes.func,
    startIcon: PropTypes.node,
  }),
};

export default TabsForDrilldown;
