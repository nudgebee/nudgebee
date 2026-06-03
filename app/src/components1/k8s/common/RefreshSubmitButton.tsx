import React from 'react';
import SyncIcon from '@mui/icons-material/Sync';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { Box, ButtonBase } from '@mui/material';
import { Divider } from '@components1/ds/Divider';
import { DropdownMenu } from '@components1/ds/DropdownMenu';
import { ds } from '@utils/colors';

interface RefreshSubmitButtonProps {
  loading: boolean;
  interval: number;
  onSubmit: () => void;
  setInterval: (interval: number) => void;
  disabled?: boolean;
}

const timerOptions: Array<{ label: string; value: number }> = [
  { label: 'Off', value: 0 },
  { label: 'Live', value: 5 },
  { label: '10s', value: 10 },
  { label: '15s', value: 15 },
  { label: '30s', value: 30 },
  { label: '45s', value: 45 },
  { label: '60s', value: 60 },
];

const PRIMARY_BG = 'var(--ds-brand-500)';
const PRIMARY_BG_HOVER = 'var(--ds-brand-400)';
const PRIMARY_TEXT = ds.background[100];

export const RefreshSubmitButton: React.FC<RefreshSubmitButtonProps> = ({ loading = false, interval, onSubmit, setInterval, disabled = false }) => {
  const isDisabled = loading || disabled;
  const activeOption = timerOptions.find((opt) => opt.value === interval) || timerOptions[0];

  return (
    <Box
      sx={{
        display: 'inline-flex',
        alignItems: 'stretch',
        height: ds.space[6],
        borderRadius: 'var(--ds-radius-md)',
        overflow: 'hidden',
        backgroundColor: PRIMARY_BG,
        opacity: isDisabled ? 0.6 : 1,
        boxShadow: 'var(--ds-shadow-xs)',
      }}
    >
      <ButtonBase
        onClick={() => onSubmit()}
        disabled={isDisabled}
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 'var(--ds-space-1)',
          px: 'var(--ds-space-3)',
          color: PRIMARY_TEXT,
          fontSize: 'var(--ds-text-small)',
          fontWeight: 'var(--ds-font-weight-medium)',
          fontFamily: 'var(--ds-font-display)',
          cursor: isDisabled ? 'not-allowed' : 'pointer',
          transition: 'background-color 150ms ease',
          '&:hover': {
            backgroundColor: isDisabled ? PRIMARY_BG : PRIMARY_BG_HOVER,
          },
        }}
      >
        <SyncIcon
          sx={{
            fontSize: 'var(--ds-text-title)',
            color: PRIMARY_TEXT,
            animation: loading ? 'rsb-spin 2s linear infinite' : undefined,
            '@keyframes rsb-spin': {
              '0%': { transform: 'rotate(360deg)' },
              '100%': { transform: 'rotate(0deg)' },
            },
          }}
        />
        Run Query
      </ButtonBase>

      <Divider orientation='vertical' color='rgba(255,255,255,0.25)' sx={{ m: 0 }} />

      <DropdownMenu
        trigger={
          <ButtonBase
            aria-label='Auto Refresh interval'
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 'var(--ds-space-1)',
              px: 'var(--ds-space-2)',
              color: PRIMARY_TEXT,
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-medium)',
              fontFamily: 'var(--ds-font-display)',
              cursor: 'pointer',
              transition: 'background-color 150ms ease',
              '&:hover': {
                backgroundColor: PRIMARY_BG_HOVER,
              },
            }}
          >
            {activeOption.label}
            <KeyboardArrowDownIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: PRIMARY_TEXT }} />
          </ButtonBase>
        }
        items={timerOptions.map((opt) => ({
          id: String(opt.value),
          label: opt.label,
          selected: opt.value === interval,
          onSelect: () => setInterval(opt.value),
        }))}
      />
    </Box>
  );
};
