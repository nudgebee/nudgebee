import { Box } from '@mui/material';
import ShowChartIcon from '@mui/icons-material/ShowChart';
import BarChartIcon from '@mui/icons-material/BarChart';
import { ToggleGroup } from '@components1/ds/ToggleGroup';

const CHART_OPTIONS = [
  { value: 'line', icon: <ShowChartIcon />, label: 'Line', tooltip: 'Line chart' },
  { value: 'bar', icon: <BarChartIcon />, label: 'Bar', tooltip: 'Bar chart' },
];

const SELECTED_INK_OVERRIDE = {
  '& [aria-checked="true"]': { color: 'var(--ds-blue-600) !important' },
};

const ChartSwitcher = ({ isBarChart, leftButtonClick, rightButtonClick }) => {
  const value = isBarChart ? 'bar' : 'line';
  const handleChange = (next) => {
    if (next === 'line') leftButtonClick?.();
    else if (next === 'bar') rightButtonClick?.();
  };

  return (
    <Box sx={SELECTED_INK_OVERRIDE}>
      <ToggleGroup selection='single' size='md' ariaLabel='Chart type' options={CHART_OPTIONS} value={value} onChange={handleChange} />
    </Box>
  );
};

export default ChartSwitcher;
