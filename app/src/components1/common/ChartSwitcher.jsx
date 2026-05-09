import { BarChartRounded, StackedLineChartRounded } from '@mui/icons-material';
import { Box, IconButton } from '@mui/material';

const OneSwitch = ({ isLeftButton, icon, isSelected, onClick }) => {
  return (
    <IconButton
      sx={{
        width: '32px',
        height: '32px',
        border: '0.5px solid',
        borderRadius: isLeftButton ? '4px 0px 0px 4px' : '0px 4px 4px 0px',
        borderColor: !isSelected ? '#D0D0D0' : '#374294',
        color: !isSelected ? '#898989' : '#374294',
        backgroundColor: !isSelected ? 'white' : '#EDF2FB',
        fontSize: 16,
        boxSizing: 'border-box',
        '& svg': {
          height: 16,
          width: 16,
        },
      }}
      onClick={onClick}
    >
      {icon}
    </IconButton>
  );
};

const ChartSwitcher = ({ isBarChart, leftButtonClick, rightButtonClick }) => {
  return (
    <Box>
      <OneSwitch icon={<StackedLineChartRounded />} isLeftButton isSelected={!isBarChart} onClick={leftButtonClick} />
      <OneSwitch icon={<BarChartRounded />} isSelected={isBarChart} onClick={rightButtonClick} />
    </Box>
  );
};

export default ChartSwitcher;
