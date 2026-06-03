import { Button as DsButton } from '@components1/ds/Button';
import { Box } from '@mui/material';
import React, { type Dispatch, type SetStateAction } from 'react';
import { ds } from '@utils/colors';

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
        gap: ds.space[3],
        flexShrink: 0,
        paddingX: ds.space[3],
      }}
    >
      {leftButtons.map((button) => (
        <DsButton
          key={button.label}
          tone='secondary'
          size='md'
          onClick={() => {
            setActiveButton(button.label);
            button.onClick();
          }}
          disabled={button.isDisabled}
        >
          {button.label}
        </DsButton>
      ))}
      {rightButtons.map((button) => (
        <DsButton
          key={button.label}
          tone={button.label.toLowerCase() === 'reject' ? 'danger' : 'primary'}
          size='md'
          onClick={() => {
            setActiveButton(button.label);
            button.onClick();
          }}
          disabled={button.isDisabled}
        >
          {button.label}
        </DsButton>
      ))}
    </Box>
  );
};

export default ActionButtons;
