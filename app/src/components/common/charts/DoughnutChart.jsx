import React from 'react';
import { Chart as ChartJS, ArcElement, Tooltip, Legend } from 'chart.js';
import { Doughnut } from 'react-chartjs-2';
import { Box, Typography, Button } from '@mui/material';
import PropTypes from 'prop-types';
import { colors as color, rawColors as rawColor, resolveColor } from 'src/utils/colors';
import { withErrorBoundary } from '@shared/ErrorBoundary';

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

function getOrCreateChartTooltip(parent) {
  let el = parent.querySelector('.ds-chart-tooltip');
  if (el) return el;

  el = document.createElement('div');
  el.className = 'ds-chart-tooltip';
  Object.assign(el.style, {
    position: 'absolute',
    pointerEvents: 'none',
    opacity: '0',
    transition: 'opacity 0.12s ease',
    zIndex: '10',
    transform: 'translate(-50%, calc(-100% - 10px))',
  });

  const body = document.createElement('div');
  body.className = 'ds-chart-tooltip-body';
  Object.assign(body.style, {
    position: 'relative',
    backgroundColor: 'var(--ds-background-100)',
    color: 'var(--ds-brand-600)',
    border: '1px solid var(--ds-brand-300)',
    boxShadow: '0px 6px 10px var(--ds-gray-alpha-300)',
    borderRadius: 'var(--ds-radius-lg)',
    padding: 'var(--ds-space-2  ) var(--ds-space-3)',
    fontSize: 'var(--ds-text-small)',
    fontWeight: 'var(--ds-font-weight-medium)',
    lineHeight: '1.5',
    fontFamily: 'Roboto, sans-serif',
    whiteSpace: 'nowrap',
  });

  const text = document.createElement('span');
  text.className = 'ds-chart-tooltip-text';
  body.appendChild(text);

  const arrow = document.createElement('div');
  arrow.className = 'ds-chart-tooltip-arrow';
  Object.assign(arrow.style, {
    position: 'absolute',
    left: '50%',
    bottom: '-5px',
    width: '8px',
    height: '8px',
    marginLeft: '-4px',
    backgroundColor: 'var(--ds-background-100)',
    borderRight: '1px solid var(--ds-brand-300)',
    borderBottom: '1px solid var(--ds-brand-300)',
    transform: 'rotate(45deg)',
  });
  body.appendChild(arrow);

  el.appendChild(body);
  parent.appendChild(el);
  return el;
}

function externalTooltipHandler(context) {
  const { chart, tooltip } = context;
  const parent = chart?.canvas?.parentNode;
  if (!parent || !tooltip) return;

  const el = getOrCreateChartTooltip(parent);

  if (tooltip.opacity === 0) {
    el.style.opacity = '0';
    return;
  }

  const lines = (tooltip.body || []).map((b) => b.lines).flat();
  el.querySelector('.ds-chart-tooltip-text').textContent = lines.join(' ').trim();

  el.style.opacity = '1';
  el.style.left = chart.canvas.offsetLeft + tooltip.caretX + 'px';
  el.style.top = chart.canvas.offsetTop + tooltip.caretY + 'px';
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

  const parsedSize = typeof size === 'string' ? parseInt(size, 10) : size;
  const baseFont = parsedSize < 50 ? 12 : 16;
  const valueLength = String(valueToDisplay ?? '').length;
  const valueFontSize = valueLength > 3 ? Math.max(9, Math.floor((baseFont * 3) / valueLength)) : baseFont;

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
        fontSize: 'var(--ds-text-small)',
        fontWeight: 'var(--ds-font-weight-medium)',
      },
      tooltip: {
        enabled: false,
        external: !displayValue || enableTooltip ? externalTooltipHandler : undefined,
        callbacks: {
          title: () => '',
          label: (context) => buildTooltipLabel(context, displayOnlyValueOnTooltip, valueToDisplay),
        },
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
      <Box sx={{ display: 'flex', flexDirection: 'column', marginTop: 'var(--ds-space-3)' }}>
        {truncatedlabels?.map((item, index) => {
          return (
            <Button
              key={index}
              sx={{
                display: 'flex',
                flexDirection: 'row',
                justifyContent: 'space-between',
                height: '20px',
                marginBottom: 'var(--ds-space-1)',
                textTransform: 'none',
              }}
            >
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center' }}>
                <Box
                  sx={{
                    background: resolvedColors[index],
                    borderRadius: 'var(--ds-radius-sm)',
                    height: '8px',
                    width: '8px',
                    marginRight: 'var(--ds-space-1)',
                  }}
                />
                <Typography
                  id={index}
                  sx={{ color: color.text.secondary, fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}
                >
                  {item}
                </Typography>
              </Box>
              <Typography
                id={index}
                sx={{ color: color.text.secondary, fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}
              >
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
          <Typography
            fontSize={valueFontSize}
            fontWeight={600}
            color={color.text.secondary}
            sx={{ position: 'absolute', zIndex: 2, whiteSpace: 'nowrap', pointerEvents: 'none' }}
          >
            {valueToDisplay}
            {!isNaN(Math.floor(values.reduce((a, b) => a + b, 0))) ? <span style={{ fontSize: parsedSize < 50 ? 8 : 16 }}>{valueUnit}</span> : ''}
          </Typography>
        ) : (
          <Typography
            fontSize={valueFontSize}
            fontWeight={600}
            color={color.text.secondary}
            sx={{ position: 'absolute', zIndex: 2, whiteSpace: 'nowrap', pointerEvents: 'none' }}
          >
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
