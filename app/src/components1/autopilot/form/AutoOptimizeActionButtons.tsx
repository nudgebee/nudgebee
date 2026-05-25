import CustomButton from '@components1/common/NewCustomButton';
import { Box } from '@mui/material';
import React, { type Dispatch, type SetStateAction } from 'react';

interface ButtonProps {
  label: string;
  backgroundColor: string;
  onClick: () => void;
  isDisabled?: boolean;
}

interface ActionButtonsProps {
  buttons: ButtonProps[];
  activeButton: string | number;
  setActiveButton: Dispatch<SetStateAction<string | number>>;
}

const ActionButtons = ({ buttons, setActiveButton }: ActionButtonsProps) => {
  const cancelIndex = buttons.findIndex((button) => button.label === 'Cancel');

  const leftButtons = buttons.slice(0, cancelIndex + 1);
  const rightButtons = buttons.slice(cancelIndex + 1);

  return (
    <Box
      sx={{
        display: 'flex',
        height: '56px',
        justifyContent: 'flex-end',
        alignItems: 'center',
        gap: '10px',
        flexShrink: 0,
        paddingX: '10px',
        button: {
          minWidth: '140px',
        },
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center' }}>
        {leftButtons.map((button) => (
          <React.Fragment key={button.label}>
            <CustomButton
              text={button.label}
              size='Medium'
              variant='secondary'
              onClick={() => {
                setActiveButton(button.label);
                button.onClick();
              }}
            />
          </React.Fragment>
        ))}
      </Box>

      <Box sx={{ display: 'flex', gap: '6px', alignItems: 'center' }}>
        {rightButtons.map((button) => (
          <React.Fragment key={button.label}>
            <CustomButton
              text={button.label}
              size='Medium'
              onClick={() => {
                setActiveButton(button.label);
                button.onClick();
              }}
              disabled={button.isDisabled}
            />
          </React.Fragment>
        ))}
      </Box>
    </Box>
  );
};

export default ActionButtons;
