/**
 * CustomTabsForDrilldown (V2) — thin wrapper around @common-new/CustomTabs with
 * `variant="secondary"` + `behavior="filter"`. Preserves the legacy public API
 * (flat `options` array, MUI-style `onChange(event, newValue)`, `rightButton`)
 * so call-sites migrate with a one-line import swap.
 *
 * Migration: `import CustomTabs from '@components1/common/CustomTabsForDrilldown'`
 *         → `import CustomTabs from '@common-new/CustomTabsForDrilldown'`
 *
 * Right-button uses ds/Button (tone="primary", size="sm") instead of legacy
 * NewCustomButton. Design tokens (`var(--ds-*)`) only — no `colors.*` imports.
 */
import React from 'react';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import CustomTabs from '@common-new/CustomTabs';
import { Button } from '@components1/ds/Button';
import SafeIcon from '@components1/common/SafeIcon';
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
const CustomTabsForDrilldown = ({
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
        <CustomTabs value={value} onChange={handleChange} options={{ tabOptions }} variant='secondary' behavior='filter' />
      </Box>
      {rightButton?.visible && (
        <Button tone='primary' size='sm' icon={rightButton.startIcon} onClick={rightButton.onClick}>
          {rightButton.text}
        </Button>
      )}
    </Box>
  );
};

CustomTabsForDrilldown.propTypes = {
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

export default CustomTabsForDrilldown;
