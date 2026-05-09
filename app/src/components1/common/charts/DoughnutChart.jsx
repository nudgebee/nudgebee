import React from 'react';
import { Chart as ChartJS, ArcElement, Tooltip, Legend } from 'chart.js';
import { Doughnut } from 'react-chartjs-2';
import { Box, Typography, Button } from '@mui/material';
import PropTypes from 'prop-types';
import { colors as color, rawColors as rawColor, resolveColor } from 'src/utils/colors';
import { withErrorBoundary } from '@common/ErrorBoundary';

ChartJS.register(ArcElement, Tooltip, Legend);

function generateColorShades(baseColor, count) {
  const baseR = parseInt(baseColor.substring(1, 3), 16);
  const baseG = parseInt(baseColor.substring(3, 5), 16);
  const baseB = parseInt(baseColor.substring(5, 7), 16);
  const shades = [];
  for (let i = 0; i < count; i++) {
    const r = Math.min(baseR + i * 5, 255);
    const g = Math.min(baseG + i * 5, 255);
    const b = Math.min(baseB + i * 5, 255);
    shades.push('#' + r.toString(16).padStart(2, '0') + g.toString(16).padStart(2, '0') + b.toString(16).padStart(2, '0'));
  }
  return shades;
}

function computeValueToDisplay(displayValue, values) {
  if (!displayValue) return '';
  if (displayValue === true && !isNaN(Math.floor(values.reduce((a, b) => a + b, 0)))) {
    return Math.floor(values.reduce((a, b) => a + b, 0));
  }
  if (typeof displayValue === 'string') return displayValue;
  if (!isNaN(displayValue)) {
    return parseFloat(displayValue) !== parseInt(displayValue) ? displayValue?.toFixed(1) : displayValue?.toFixed(0);
  }
  return '';
}

function buildTooltipLabel(context, displayOnlyValueOnTooltip, valueToDisplay) {
  if (!displayOnlyValueOnTooltip) return ` ${context?.label}: ${context.raw}%`;
  let percentage = (context.raw / valueToDisplay) * 100;
  percentage = parseFloat(percentage) !== parseInt(percentage) ? parseFloat(percentage.toFixed(1)) : parseInt(percentage);
  return `${percentage}%`;
}

function truncateLabel(item) {
  return item.length > 28 ? item.slice(0, 28) + '...' : item;
}

function reduceValue(item) {
  const num = Number(item);
  if (item == null || isNaN(num)) return '';
  return parseFloat(item) !== parseInt(item) ? num.toFixed(1) : num.toFixed(0);
}

function DoughnutChart({
  values,
  labels,
  size = 77,
  colors = ['#778899'],
  displayLegend = false,
  displayCustomLegend = false,
  displayValue = false,
  valueUnit = '%',
  cutout = '65%',
  borderRadius = 3,
  borderWidth = 2,
  chartRadius = '100%',
  id = null,
  enableTooltip = false,
  displayOnlyValueOnTooltip = false,
  onItemClick,
}) {
  values = values || [];

  const truncatedlabels = labels?.map(truncateLabel);
  let resolvedColors;
  if (Array.isArray(colors)) {
    resolvedColors = colors.map(resolveColor);
  } else {
    const baseColor = resolveColor(colors);
    resolvedColors = /^#[0-9A-Fa-f]{6}$/.test(baseColor) ? generateColorShades(baseColor, values.length) : Array(values.length).fill(baseColor);
  }
  const reducedValues = values.map(reduceValue);
  const valueToDisplay = computeValueToDisplay(displayValue, values);

  const options = {
    maintainAspectRatio: false,
    responsive: true,
    radius: chartRadius ? chartRadius : '100%',
    fullWidth: true,
    tooltipFontSize: 10,
    onClick: (_, elements) => {
      if (elements && elements.length > 0 && onItemClick) {
        onItemClick(labels[elements[0].index]);
      }
    },
    plugins: {
      datalabels: {
        formatter: function (value) {
          return value + '%';
        },
        color: rawColor.text.white,
        fontSize: '12px',
        fontWeight: 500,
      },
      tooltip: {
        enabled: !displayValue || enableTooltip,
        callbacks: {
          title: () => '',
          label: (context) => buildTooltipLabel(context, displayOnlyValueOnTooltip, valueToDisplay),
        },
        titleFont: {
          size: 12,
          weight: '500',
          family: 'Roboto',
        },
        bodyFont: {
          size: 12,
          weight: '500',
          family: 'Roboto',
        },
        backgroundColor: 'white',
        bodyColor: rawColor.text.secondary,
        cornerRadius: 4,
        boxHeight: 12,
        boxWidth: 12,
        boxShadow: '0px 4px 10px 0px #89899340',
        color: rawColor.text.secondary,
        showShadow: true,
        borderWidth: 0.7,
        borderColor: rawColor.border.secondary,
      },
      legend: {
        display: displayLegend,
        position: 'bottom',
        padding: 2,
        borderRadius: 2,
        labels: {
          pointStyle: 'rectRounded',
          radius: 4,
          usePointStyle: true,
        },
      },
      title: { display: false },
    },
    cutout: cutout,
    animation: {
      duration: 500,
      easing: 'easeOutQuart',
      onComplete: function (arg) {
        var ctx = arg.chart.ctx;
        ctx.font = ChartJS?.helpers?.fontString(ChartJS.defaults.global.defaultFontFamily, 'normal', ChartJS.defaults.global.defaultFontFamily);
        ctx.textAlign = 'center';
        ctx.textBaseline = 'bottom';
      },
    },
  };

  const data = {
    labels: truncatedlabels,
    datasets: [
      {
        data: reducedValues,
        backgroundColor: resolvedColors,
        borderWidth: borderWidth,
        borderRadius: borderRadius,
      },
    ],
  };

  const CustomLegends = () => {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', marginTop: '12px' }}>
        {truncatedlabels?.map((item, index) => {
          return (
            <Button
              key={index}
              sx={{
                display: 'flex',
                flexDirection: 'row',
                justifyContent: 'space-between',
                height: '20px',
                marginBottom: '2px',
                textTransform: 'none',
              }}
            >
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center' }}>
                <Box sx={{ background: resolvedColors[index], borderRadius: '2px', height: '8px', width: '8px', marginRight: '4px' }} />
                <Typography id={index} sx={{ color: color.text.secondary, fontSize: '12px', fontWeight: 500 }}>
                  {item}
                </Typography>
              </Box>
              <Typography id={index} sx={{ color: color.text.secondary, fontSize: '12px', fontWeight: 500 }}>
                {reducedValues[index]}%
              </Typography>
            </Button>
          );
        })}
      </Box>
    );
  };

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column' }}>
      <div
        id={'doughnutChart'}
        style={{
          width: size,
          height: size,
          position: 'relative',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <Doughnut id={id} data={data} options={options} style={{ zIndex: '1', cursor: 'pointer' }} />
        {displayValue ? (
          <Typography fontSize={size < 50 ? 12 : 16} fontWeight={600} color={color.text.secondary} sx={{ position: 'absolute' }}>
            {valueToDisplay}
            {!isNaN(Math.floor(values.reduce((a, b) => a + b, 0))) ? <span style={{ fontSize: size < 50 ? 8 : 16 }}>{valueUnit}</span> : ''}
          </Typography>
        ) : (
          <Typography fontSize={size < 50 ? 12 : 16} fontWeight={600} color={color.text.secondary} sx={{ position: 'absolute' }}>
            {0}
          </Typography>
        )}
      </div>
      {displayCustomLegend && <CustomLegends />}
    </Box>
  );
}

DoughnutChart.propTypes = {
  values: PropTypes.arrayOf(PropTypes.number),
  labels: PropTypes.arrayOf(PropTypes.string),
  size: PropTypes.number,
  colors: PropTypes.oneOfType([PropTypes.arrayOf(PropTypes.string), PropTypes.string]),
  displayLegend: PropTypes.bool,
  displayCustomLegend: PropTypes.bool,
  displayValue: PropTypes.oneOfType([PropTypes.bool, PropTypes.string, PropTypes.number]),
  valueUnit: PropTypes.string,
  cutout: PropTypes.string,
  borderRadius: PropTypes.number,
  borderWidth: PropTypes.number,
  chartRadius: PropTypes.string,
  id: PropTypes.string,
  enableTooltip: PropTypes.bool,
  displayOnlyValueOnTooltip: PropTypes.bool,
  onItemClick: PropTypes.func,
};

export default withErrorBoundary(DoughnutChart);
