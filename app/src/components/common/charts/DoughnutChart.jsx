import React from 'react';
import { Chart as ChartJS, ArcElement, Tooltip, Legend } from 'chart.js';
import { Doughnut } from 'react-chartjs-2';
import { Box, Typography, Button } from '@mui/material';
import PropTypes from 'prop-types';
import { ds, resolveColor } from 'src/utils/colors';
import { withErrorBoundary } from '@shared/ErrorBoundary';
import { makeExternalTooltip } from './externalTooltip';

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

/**
 * @param {{
 *   values?: number[],
 *   labels?: string[],
 *   size?: number,
 *   colors?: string[] | string,
 *   displayLegend?: boolean,
 *   displayCustomLegend?: boolean,
 *   displayValue?: boolean | string | number,
 *   valueUnit?: string,
 *   cutout?: string,
 *   borderRadius?: number,
 *   borderWidth?: number,
 *   chartRadius?: string | null,
 *   id?: string | null,
 *   enableTooltip?: boolean,
 *   displayOnlyValueOnTooltip?: boolean,
 *   onItemClick?: (label: string) => void,
 *   formatValue?: (raw: number, label: string) => string,
 *   centerLabel?: string,
 *   centerValue?: string,
 *   externalTooltip?: boolean,
 * }} props
 *
 * Opt-in extensions (all default off — existing callers are unaffected):
 *   - `formatValue`      — format slice values with arbitrary units (e.g. currency)
 *                          instead of the default `%`. Drives both the tooltip and
 *                          skips the percentage rounding applied to the dataset.
 *   - `centerLabel` /    — render a 2-line center (small caption + bold figure),
 *     `centerValue`        e.g. "Total" / "$729". Overrides the `displayValue` center.
 *   - `externalTooltip`  — use the shared white tabular tooltip (`externalTooltip.ts`)
 *                          instead of Chart.js' built-in tooltip.
 */
function DoughnutChart({
  values,
  labels,
  size = 77,
  colors = [String(ds.gray[500])],
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
  formatValue,
  centerLabel,
  centerValue,
  externalTooltip = false,
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
  // With a custom formatter (e.g. currency) keep the true numbers — the default
  // `%` rounding in reduceValue would otherwise lose precision before formatting.
  const datasetValues = formatValue ? values : reducedValues;
  const valueToDisplay = computeValueToDisplay(displayValue, values);
  // A 2-line center (caption + figure) takes precedence over the numeric center.
  const hasCustomCenter = Boolean(centerValue) || Boolean(centerLabel);

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
        color: resolveColor(ds.background[100]),
        fontSize: 'var(--ds-text-small)',
        fontWeight: 'var(--ds-font-weight-medium)',
      },
      tooltip: {
        // External (shared tabular) tooltip takes over rendering when opted in.
        enabled: externalTooltip ? false : !displayValue || enableTooltip,
        ...(externalTooltip ? { external: makeExternalTooltip((raw, label) => (formatValue ? formatValue(Number(raw), label) : `${raw}`)) } : {}),
        callbacks: {
          title: () => '',
          label: (context) =>
            formatValue ? formatValue(Number(context.raw), context.label) : buildTooltipLabel(context, displayOnlyValueOnTooltip, valueToDisplay),
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
        bodyColor: resolveColor(ds.brand[500]),
        cornerRadius: 4,
        boxHeight: 12,
        boxWidth: 12,
        boxShadow: `0px ${ds.space[1]} ${ds.space.mul(0, 5)} 0px color-mix(in srgb, ${ds.gray[500]} 25%, transparent)`,
        color: resolveColor(ds.brand[500]),
        showShadow: true,
        borderWidth: 0.7,
        borderColor: resolveColor(ds.brand[200]),
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
        data: datasetValues,
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
                height: ds.space.mul(0, 10),
                marginBottom: 'var(--ds-space-1)',
                textTransform: 'none',
              }}
            >
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center' }}>
                <Box
                  sx={{
                    background: resolvedColors[index],
                    borderRadius: 'var(--ds-radius-sm)',
                    height: ds.space[2],
                    width: ds.space[2],
                    marginRight: 'var(--ds-space-1)',
                  }}
                />
                <Typography id={index} sx={{ color: ds.brand[500], fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                  {item}
                </Typography>
              </Box>
              <Typography id={index} sx={{ color: ds.brand[500], fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
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
        {hasCustomCenter ? (
          <Box sx={{ position: 'absolute', display: 'flex', flexDirection: 'column', alignItems: 'center', lineHeight: 1.15 }}>
            {centerLabel && <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[500] }}>{centerLabel}</Typography>}
            {centerValue && (
              <Typography sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                {centerValue}
              </Typography>
            )}
          </Box>
        ) : displayValue ? (
          <Typography fontSize={size < 50 ? 12 : 16} fontWeight={600} color={ds.brand[500]} sx={{ position: 'absolute' }}>
            {valueToDisplay}
            {!isNaN(Math.floor(values.reduce((a, b) => a + b, 0))) ? <span style={{ fontSize: size < 50 ? 8 : 16 }}>{valueUnit}</span> : ''}
          </Typography>
        ) : (
          <Typography fontSize={size < 50 ? 12 : 16} fontWeight={600} color={ds.brand[500]} sx={{ position: 'absolute' }}>
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
  formatValue: PropTypes.func,
  centerLabel: PropTypes.string,
  centerValue: PropTypes.string,
  externalTooltip: PropTypes.bool,
};

export default withErrorBoundary(DoughnutChart);
