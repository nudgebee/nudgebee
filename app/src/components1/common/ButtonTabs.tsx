import React, { useState } from 'react';
import { Button, Box, Typography, Grid } from '@mui/material';
import { colors } from 'src/utils/colors';

interface ButtonConfig {
  id: string | number;
  label: string;
  value?: any;
}

interface ButtonTabsProps {
  disabled?: boolean;
  title?: string;
  buttons: ButtonConfig[];
  callBack: (buttonId: string | number, buttonValue: any, button: ButtonConfig) => void;
  fontSize?: string;
  color?: string;
  background?: string;
  borderColor?: string;
  borderRadius?: string;
  width?: string | number;
  height?: string | number;
  selectedButton?: string | number | null;
  sx?: object;
}

export default function ButtonTabs({
  disabled = false,
  title = '',
  buttons,
  callBack,
  fontSize = '14px',
  color = colors.text.tertiary,
  background = colors.background.buttonTab,
  borderColor = colors.border.buttonTab,
  borderRadius = '6px',
  width = 'auto',
  height = '31px',
  selectedButton,
}: ButtonTabsProps) {
  const [activeButton, setActiveButton] = useState(selectedButton);
  const handleClick = (buttonId: string | number, buttonValue: any, button: ButtonConfig) => {
    if (!disabled) {
      if (callBack) {
        setActiveButton(buttonId);
        callBack(buttonId, buttonValue, button);
      }
    }
  };

  return (
    <Box sx={{ display: 'flex', alignItems: 'baseline' }}>
      {title && (
        <Typography sx={{ color: colors.text.secondary, fontSize: '10px', fontWeight: 400, marginRight: '4px', minWidth: '43px' }}>
          {title}
        </Typography>
      )}
      <Grid container spacing={1}>
        {buttons.map((button) => (
          <Grid item key={button.id}>
            <Button
              disabled={disabled}
              size='small'
              onClick={() => handleClick(button.id, button.value, button)}
              sx={{
                width: width,
                textTransform: 'none',
                borderWidth: 0.5,
                borderRadius: borderRadius,
                padding: '4px 8px',
                fontSize: fontSize,
                height: height,
                fontWeight: activeButton === button.id ? '600' : '400',
                background: activeButton === button.id ? background : undefined,
                color: activeButton === button.id ? color : colors.text.tertiary,
                border: activeButton === button.id ? 'none' : `0.75px solid ${colors.border.primaryLight}`,
                borderColor: activeButton === button.id ? borderColor : undefined,
                '&:hover': {
                  background: activeButton === button.id ? background : undefined,
                  color: activeButton === button.id ? color : colors.text.tertiary,
                },
              }}
            >
              {button.label}
            </Button>
          </Grid>
        ))}
      </Grid>
    </Box>
  );
}
