import { IconButton } from '@mui/material';
import React from 'react';
import SafeIcon from '@components1/common/SafeIcon';
import { colors } from 'src/utils/colors';
import { TableAccordionArrowDownIcon } from '@assets';

const ExpandButton = ({ expanded, onClick, isSmallSize, sx }) => {
  return (
    <IconButton
      sx={{
        width: isSmallSize ? '20px' : '28px',
        height: isSmallSize ? '20px' : '28px',
        color: colors.text.white,
        background: colors.background.transparent,
        ...sx,
      }}
      onClick={onClick}
    >
      <SafeIcon
        priority={true}
        src={TableAccordionArrowDownIcon}
        alt='arrow'
        style={{
          transform: expanded ? 'rotate(180deg)' : 'rotate(0deg)',
          transition: 'transform 0.3s ease',
        }}
      />
    </IconButton>
  );
};

export default ExpandButton;
