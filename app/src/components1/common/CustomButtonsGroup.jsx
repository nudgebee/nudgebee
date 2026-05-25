import { Button, ButtonGroup } from '@mui/material';
import React from 'react';
import { colors } from 'src/utils/colors';

const CustomButtonsGroup = ({ selected, onClick, options = [], borderColor = '#97DCE4', fontWeight = 600, tabType }) => {
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
          borderRadius: tabType && '4px',
          height: '30px',
          fontSize: 14,
          color: tabType ? colors.text.secondary : '#7A88A4',
          fontWeight: fontWeight,
          textTransform: 'unset',
          borderColor: borderColor,
          borderWidth: 0.4,
          '&:hover': {
            borderColor: borderColor,
            borderWidth: 0.4,
          },

          '&.selected': {
            bgcolor: tabType ? colors.background.primaryLightest : '#F0F0F0',
            color: tabType && '#374151',
            fontWeight: tabType && 600,
            borderBottom: tabType && `2px solid ${colors.border.primary}`,
            borderBottomLeftRadius: tabType && '4px',
            borderBottomRightRadius: tabType && '4px',
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
