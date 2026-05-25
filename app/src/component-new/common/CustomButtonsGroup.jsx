import { Button, ButtonGroup } from '@mui/material';
import React from 'react';
import { colors } from 'src/utils/colors';

const CustomButtonsGroup = ({ selected, onClick, options = [], borderColor = 'var(--ds-teal-300)', fontWeight = 600, tabType }) => {
  return (
    <ButtonGroup
      disableElevation
      variant='outlined'
      aria-label='outlined button group'
      sx={{
        minHeight: 0,
        minWidth: 0,

        '& button': {
          padding: '4px 14px',
          minHeight: 0,
          minWidth: 0,
          lineHeight: '14px',
          borderRadius: tabType && 'var(--ds-radius-sm)',
          height: '30px',
          fontSize: 'var(--ds-text-body-lg)',
          color: tabType ? colors.text.secondary : 'var(--ds-gray-600)',
          fontWeight: fontWeight,
          textTransform: 'unset',
          borderColor: borderColor,
          borderWidth: 0.4,
          '&:hover': {
            borderColor: borderColor,
            borderWidth: 0.4,
          },

          '&.selected': {
            bgcolor: tabType ? colors.background.primaryLightest : 'var(--ds-gray-100)',
            color: tabType && 'var(--ds-gray-700)',
            fontWeight: tabType && 600,
            borderBottom: tabType && `2px solid ${colors.border.primary}`,
            borderBottomLeftRadius: tabType && 'var(--ds-radius-sm)',
            borderBottomRightRadius: tabType && 'var(--ds-radius-sm)',
          },
        },
      }}
    >
      {options.map((opt, _idx) => (
        <Button key={_idx} className={selected === opt.value ? 'selected' : ''} onClick={() => onClick?.(opt)} disabled={opt?.disabled || false}>
          {opt.text}
        </Button>
      ))}
    </ButtonGroup>
  );
};

export default CustomButtonsGroup;
