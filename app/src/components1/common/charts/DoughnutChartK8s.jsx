import React from 'react';
import { Chart as ChartJS, ArcElement, Tooltip, Legend } from 'chart.js';
import { Doughnut } from 'react-chartjs-2';
import { Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { withErrorBoundary } from '@common/ErrorBoundary';
import { resolveColor, withAlpha } from 'src/utils/colors';

ChartJS.register(ArcElement, Tooltip, Legend);

const DoughnutChartK8s = ({ isDecimal = false, value = 20, size = '77px', color: colorProp = '#81D97F', id = null, rounded = '10px' }) => {
  const color = resolveColor(colorProp);
  const options = {
    responsive: true,
    plugins: {
      legend: { display: false },
      title: { display: false },
      tooltip: { enabled: false },
    },
    cutout: '75%',
  };

  const data = {
    datasets: [
      {
        data: [value, 100 - value],
        backgroundColor: [color, withAlpha(color, 0.44)],
        borderColor: [color, withAlpha(color, 0.44)],
        borderWidth: 0,
        borderRadius: rounded,
      },
    ],
  };
  const getFontSize = (size) => {
    let fontSize;
    if (size < 40) {
      fontSize = 10;
    } else if (size < 50) {
      fontSize = 12;
    } else {
      fontSize = 13;
    }
    return fontSize;
  };
  const getTypographyFontSize = (size) => {
    if (size < 36) {
      return '14px';
    } else if (size < 55) {
      return '18px';
    }
    return '20px';
  };
  return (
    <div
      style={{
        width: size,
        height: size,
        position: 'relative',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        flexShrink: 0,
      }}
    >
      <Typography fontSize={getTypographyFontSize(size)} fontWeight={600} color='#303A5C' sx={{ position: 'absolute', zIndex: 1 }}>
        {isDecimal ? Math.round(value) : value}
        <span style={{ fontSize: `${getFontSize(size)}px` }}>%</span>
      </Typography>
      <Doughnut id={id} data={data} options={options} style={{ zIndex: 2 }} />
    </div>
  );
};

export default withErrorBoundary(DoughnutChartK8s);

DoughnutChartK8s.propTypes = {
  isDecimal: PropTypes.bool,
  value: PropTypes.any,
  size: PropTypes.any,
  color: PropTypes.string,
  id: PropTypes.any,
  rounded: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
};
