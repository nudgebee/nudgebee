import React, { useEffect, useRef, useState } from 'react';
import { Chart as ChartJS, CategoryScale, LinearScale, PointElement, LineElement, Title, Tooltip, Legend, Colors } from 'chart.js';
import { Line, getElementAtEvent } from 'react-chartjs-2';
import PropTypes from 'prop-types';
import { v4 as uuidv4 } from 'uuid';
import { withErrorBoundary } from '@common/ErrorBoundary';
import { resolveColor } from 'src/utils/colors';

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Title, Tooltip, Legend, Colors);

// Create a global tooltip state that persists across renders
const tooltipState = {
  isHovered: false,
  hideTimeout: null,
};

const SearchIcon = () => (
  <svg width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='currentColor' strokeWidth='2' strokeLinecap='round' strokeLinejoin='round'>
    <circle cx='11' cy='11' r='8' />
    <line x1='21' y1='21' x2='16.65' y2='16.65' />
  </svg>
);

const askNubiButtonStyle = {
  background: 'linear-gradient(135deg, var(--ds-blue-500), #2563EB)',
  color: 'white',
  border: 'none',
  borderRadius: 'var(--ds-radius-md)',
  padding: 'var(--ds-space-1) var(--ds-space-3)',
  cursor: 'pointer',
  fontSize: 'var(--ds-text-small)',
  fontWeight: 'var(--ds-font-weight-medium)',
  fontFamily: 'Roboto, sans-serif',
  whiteSpace: 'nowrap',
  boxShadow: '0 2px 8px rgba(59, 130, 246, 0.4)',
  display: 'flex',
  alignItems: 'center',
  gap: 'var(--ds-space-1)',
};

/**
 * @param {{
 *   data?: any[],
 *   labels?: (string | number)[],
 *   timestamps?: number[],
 *   colors?: string[],
 *   chartLabel?: string | any[],
 *   dataset?: any[],
 *   id?: string,
 *   chartTitle?: string,
 *   loading?: boolean,
 *   minHeight?: number,
 *   legendOptions?: object,
 *   interactionOptions?: object,
 *   scaleOptions?: object,
 *   onDataPointClick?: (e: any) => void,
 *   onAskNubi?: (e: any) => void,
 *   useFixedHeight?: boolean,
 *   fixedHeight?: number,
 *   dynamicHeight?: boolean,
 *   integerYlabel?: boolean,
 * }} props
 */

const Charts = ({
  data = [],
  labels = [],
  timestamps = [],
  colors = [],
  chartLabel = '',
  dataset = [],
  id = '',
  chartTitle = '',
  loading = false,
  minHeight = 230,
  legendOptions = {},
  interactionOptions = {},
  scaleOptions = {},
  onDataPointClick,
  onAskNubi,
  useFixedHeight = false,
  fixedHeight = 400,
  dynamicHeight = true,
  customPlugins = [],
  integerYlabel = false,
  fixedWidth = undefined,
}) => {
  const uniqueId = id || uuidv4();
  const legendContainerId = `legend-container-${uniqueId}`;
  const chartContainerRef = useRef(null);
  const chartRef = useRef(null);
  const [chartHeight, setChartHeight] = useState(minHeight);
  const estimatedLegendHeight = Array.isArray(chartLabel) ? (Math.floor(chartLabel.length / 5) + 1) * 24 : 24;

  // Pinned point state for "What happened here?" button
  const [pinnedPoint, setPinnedPoint] = useState(null);
  const pinnedPointRef = useRef(null);
  pinnedPointRef.current = pinnedPoint;

  const options = {
    clip: false,
    responsive: true,
    maintainAspectRatio: false,
    interaction: {
      intersect: false,
      mode: 'nearest',
      axis: 'xy',
    },
    plugins: {
      legend: {
        position: 'top',
        align: 'end',
        labels: {
          boxWidth: 8,
          boxHeight: 8,
          borderRadius: 10,
        },
      },
      title: {
        display: false,
        text: 'Line Chart',
      },
      tooltip: {
        enabled: false,
      },
    },
    scales: {
      x: {
        type: 'category',
        grid: {
          display: false,
          color: 'rgba(0,0,0,0.1)',
          drawBorder: false,
          lineWidth: 0.2,
        },
        ticks: {
          autoSkip: true,
          maxTicksLimit: fixedWidth ? Math.max(8, Math.floor(fixedWidth / 500)) : 4,
        },
      },
      y: {
        grid: {
          display: true,
          color: 'rgba(0,0,0,0.1)',
          drawBorder: false,
          lineWidth: 0.2,
        },
        ...(integerYlabel && {
          ticks: {
            precision: 0, // removes decimals
            callback: function (value) {
              return Number.isInteger(value) ? value : '';
            },
          },
        }),
      },
    },
    elements: {
      line: {
        tension: 0.4,
      },
    },
  };

  let localOptions = JSON.parse(JSON.stringify(options));

  // For scrollable (fixed-width) charts, show one tick per hour instead of auto-skipped ticks.
  if (fixedWidth) {
    localOptions.scales.x.ticks = {
      autoSkip: false,
      maxRotation: 0,
      callback: function (value, index) {
        const label = String(labels[index] || '');
        if (!/[T ]\d{2}:00:00/.test(label)) return null;
        const normalized = label.replace('T', ' ');
        const timePart = normalized.slice(11, 16); // "HH:MM"
        // Show full date only at midnight to mark day boundaries; otherwise just "HH:MM"
        return timePart === '00:00' ? normalized.slice(0, 16) : timePart;
      },
    };
  }

  if (!data) {
    data = [[]];
  } else if (data.length === 0) {
    data = [[]];
  }

  if (typeof colors === 'string') {
    colors = [colors];
  }
  colors = colors.map(resolveColor);

  // Resolve CSS variable colors in pre-built dataset objects for Chart.js canvas
  const processedDataset =
    dataset && dataset.length > 0
      ? dataset.map((obj) => ({
          ...obj,
          ...(obj.borderColor && {
            borderColor: Array.isArray(obj.borderColor) ? obj.borderColor.map(resolveColor) : resolveColor(obj.borderColor),
          }),
          ...(obj.backgroundColor && {
            backgroundColor: Array.isArray(obj.backgroundColor) ? obj.backgroundColor.map(resolveColor) : resolveColor(obj.backgroundColor),
          }),
        }))
      : dataset;

  if (data && data.length > 0 && !Array.isArray(data[0])) {
    data = [data];
    if (!Array.isArray(chartLabel)) chartLabel = [chartLabel];
  }

  let chartDatasets = [];
  let hasValidData = true;
  const isSingleDataPoint = labels && labels.length === 1;

  if (isSingleDataPoint) {
    labels = ['', ...labels, ''];
    if (processedDataset && processedDataset.length > 0) {
      chartDatasets = processedDataset.map((obj) => ({
        ...obj,
        borderWidth: obj.borderWidth ?? 1,
        pointRadius: obj.pointRadius || 5,
        pointHoverRadius: 7,
        data: [null, ...obj.data, null],
      }));
    } else if (data.length > 0 && data[0].length !== 0) {
      chartDatasets = data.map((item, index) => ({
        borderWidth: 1,
        data: [null, ...item, null],
        label: (Array.isArray(chartLabel) ? chartLabel[index] : chartLabel) || 'Value',
        borderColor: colors[index],
        backgroundColor: colors[index],
        pointRadius: 5,
        pointHoverRadius: 7,
      }));
    } else {
      chartDatasets = [
        {
          data: [],
          label: [],
          borderColor: colors[0],
          backgroundColor: colors[0],
        },
      ];
      hasValidData = false;
    }
  } else if (processedDataset && processedDataset.length > 0) {
    if ('data' in processedDataset[0] && processedDataset[0].data && processedDataset[0].data.length == 1) {
      chartDatasets = processedDataset.map((obj) => {
        let newData = ['', ...obj.data, ''];
        return { label: obj.label, data: newData, borderWidth: 1, pointRadius: 0.5 };
      });
      labels = labels.map((_item) => ['', _item, ''])[0];
    } else {
      chartDatasets = processedDataset.map((obj) => {
        obj.borderWidth = obj.borderWidth ?? 1;
        obj.pointRadius = obj.pointRadius ?? 0;
        return obj;
      });
    }
  } else if (data.length > 0 && data[0].length != 0) {
    chartDatasets = data.map((item, index) => ({
      borderWidth: 1,
      data: item.every((value) => value === '') ? [] : item,
      label: (Array.isArray(chartLabel) ? chartLabel[index] : chartLabel) || 'Value',
      borderColor: colors[index],
      backgroundColor: colors[index],
      pointRadius: 0,
    }));
  } else {
    chartDatasets = [
      {
        borderWidth: 1,
        data: [],
        label: [],
        borderColor: colors[0],
        backgroundColor: colors[0],
        pointRadius: 0,
      },
    ];
    hasValidData = false;
  }

  const chartData = {
    labels: labels || [],
    datasets: chartDatasets,
  };

  const noDataPlugin = {
    id: 'noDataPlugin',
    afterDraw: function (chart) {
      if (chart.data.datasets[0]?.data.length < 1) {
        let ctx = chart.ctx;
        let width = chart.width;
        let height = chart.height;
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        ctx.font = '30px Arial';
        ctx.fillText('No data to display', width / 2, height / 2);
        ctx.restore();
      }
    },
  };

  const getOrCreateLegendList = (chart, id) => {
    const legendContainer = document.getElementById(id);
    if (!legendContainer) {
      return null;
    }

    let listContainer = legendContainer.querySelector('ul');

    if (!listContainer) {
      listContainer = document.createElement('ul');
      listContainer.style.display = 'grid';
      listContainer.style.gridTemplateColumns = '1fr 1fr';
      listContainer.style.gap = '12px 16px';
      listContainer.style.margin = 0;
      listContainer.style.padding = 0;
      listContainer.style.maxHeight = '300px';
      listContainer.style.overflowY = 'auto';

      legendContainer.appendChild(listContainer);
    }

    return listContainer;
  };

  const htmlLegendPlugin = (containerId) => ({
    id: 'htmlLegend',
    afterUpdate(chart, _args, _options) {
      const ul = getOrCreateLegendList(chart, containerId);
      if (!ul) {
        return;
      }

      while (ul.firstChild) {
        ul.firstChild.remove();
      }

      const items = chart.options.plugins.legend.labels.generateLabels(chart);
      items.forEach((item, _i) => {
        const li = document.createElement('li');
        li.style.alignItems = 'center';
        li.style.cursor = 'pointer';
        li.style.display = 'flex';
        li.style.flexDirection = 'row';
        li.style.marginLeft = '10px';
        li.style.marginRight = '10px';
        li.style.marginBottom = '4px';

        li.onclick = () => {
          const { type } = chart.config;
          if (type === 'pie' || type === 'doughnut') {
            chart.toggleDataVisibility(item.index);
          } else {
            chart.setDatasetVisibility(item.datasetIndex, !chart.isDatasetVisible(item.datasetIndex));
          }
          item.hidden = !item?.hidden;
          labelSpan.style.textDecoration = item.hidden ? 'line-through' : '';
          chart.update();
        };

        const boxSpan = document.createElement('span');
        boxSpan.style.background = item.fillStyle;
        boxSpan.style.borderColor = item.strokeStyle;
        boxSpan.style.borderWidth = item.lineWidth + 'px';
        boxSpan.style.display = 'inline-block';
        boxSpan.style.flexShrink = 0;
        boxSpan.style.height = '20px';
        boxSpan.style.marginLeft = '10px';
        boxSpan.style.marginRight = '10px';
        boxSpan.style.width = '20px';

        const textContainer = document.createElement('p');
        textContainer.style.color = item.fontColor;
        textContainer.style.margin = 0;
        textContainer.style.padding = 0;
        textContainer.style.textDecoration = item?.hidden ? 'line-through' : '';

        const labelSpan = document.createElement('span');
        labelSpan.appendChild(document.createTextNode(item.text));
        textContainer.appendChild(labelSpan);

        let series = chart.data.datasets[item.datasetIndex];
        let sortedSeries = series.data.filter((item) => item !== '').sort((a, b) => a - b);

        let min = sortedSeries[0];
        let max = sortedSeries[sortedSeries.length - 1];
        let p99 = sortedSeries[Math.floor(sortedSeries.length * 0.99)];
        let avg = sortedSeries.reduce((a, b) => a + b, 0) / sortedSeries.length;

        const metrices = document.createElement('p');
        metrices.style.margin = 0;
        metrices.style.padding = 0;
        if (!isNaN(max)) {
          const maxElement = document.createElement('b');
          maxElement.appendChild(document.createTextNode('Max: '));
          metrices.appendChild(maxElement);
          metrices.appendChild(document.createTextNode(max?.toFixed(2) ?? '-'));
        }

        if (!isNaN(min)) {
          const minElement = document.createElement('b');
          minElement.appendChild(document.createTextNode('Min: '));
          metrices.appendChild(document.createTextNode(', '));
          metrices.appendChild(minElement);
          metrices.appendChild(document.createTextNode(min?.toFixed(2) ?? '-'));
        }

        if (!isNaN(p99)) {
          const p99Element = document.createElement('b');
          p99Element.appendChild(document.createTextNode('P99: '));
          metrices.appendChild(document.createTextNode(', '));
          metrices.appendChild(p99Element);
          metrices.appendChild(document.createTextNode(p99?.toFixed(2) ?? '-'));
        }

        if (!isNaN(avg)) {
          const avgElement = document.createElement('b');
          avgElement.appendChild(document.createTextNode('Avg: '));
          metrices.appendChild(document.createTextNode(', '));
          metrices.appendChild(avgElement);
          metrices.appendChild(document.createTextNode(avg?.toFixed(2) ?? '-'));
        }

        textContainer.appendChild(metrices);

        li.appendChild(boxSpan);
        li.appendChild(textContainer);
        ul.appendChild(li);
      });

      calculateChartHeight();
    },
  });

  const calculateChartHeight = () => {
    if (useFixedHeight) {
      setChartHeight(fixedHeight);
      return;
    }
    if (!dynamicHeight) {
      setChartHeight(minHeight);
      return;
    }

    if (!chartContainerRef.current) {
      return;
    }

    const datasetCount = chartDatasets.length;
    const labelCount = labels.length;

    let calculatedHeight = minHeight;

    if (datasetCount > 3) {
      calculatedHeight += (datasetCount - 3) * 20;
    }

    if (labelCount > 10) {
      calculatedHeight += 30;
    }

    const legendContainer = document.getElementById(legendContainerId);
    if (legendContainer) {
      calculatedHeight = Math.max(calculatedHeight, datasetCount * 50 + 100);
    }
    calculatedHeight = Math.max(calculatedHeight, minHeight);

    const viewportHeight = window.innerHeight;
    calculatedHeight = Math.min(calculatedHeight, viewportHeight * 0.7);

    setChartHeight(calculatedHeight);
  };

  if (chartTitle) {
    localOptions.plugins.title = {
      display: false,
      text: chartTitle,
      align: 'start',
    };
  }

  const TOOLTIP_MAX_WIDTH = 500;
  const TOOLTIP_MAX_HEIGHT = '60vh';
  const TOOLTIP_OFFSET_X = 15;
  const TOOLTIP_OFFSET_Y = 10;

  // Get or create the tooltip element
  const getOrCreateTooltipElement = () => {
    let tooltipEl = document.getElementById('chartjs-tooltip');

    if (!tooltipEl) {
      tooltipEl = document.createElement('div');
      tooltipEl.id = 'chartjs-tooltip';

      Object.assign(tooltipEl.style, {
        position: 'absolute',
        background: 'rgba(51, 51, 51, 0.95)',
        color: 'white',
        padding: 'var(--ds-space-2) var(--ds-space-3)',
        borderRadius: 'var(--ds-radius-md)',
        pointerEvents: 'auto',
        whiteSpace: 'normal',
        fontSize: 'var(--ds-text-small)',
        zIndex: '9999',
        transition: 'opacity 0.1s ease',
        maxWidth: `${TOOLTIP_MAX_WIDTH}px`,
        maxHeight: TOOLTIP_MAX_HEIGHT,
        overflowY: 'auto',
        overflowX: 'auto',
        boxSizing: 'border-box',
        boxShadow: '0 2px 8px rgba(0,0,0,0.2)',
        opacity: '0',
        display: 'none',
        willChange: 'opacity', // Hint to browser for GPU acceleration
      });

      // Add event listeners for mouse enter/leave on tooltip
      tooltipEl.addEventListener('mouseenter', () => {
        tooltipState.isHovered = true;
        if (tooltipState.hideTimeout) {
          clearTimeout(tooltipState.hideTimeout);
          tooltipState.hideTimeout = null;
        }
      });

      tooltipEl.addEventListener('mouseleave', () => {
        tooltipState.isHovered = false;
        if (!tooltipState.hideTimeout) {
          tooltipState.hideTimeout = setTimeout(() => {
            if (!tooltipState.isHovered) {
              tooltipEl.style.opacity = 0;
              tooltipEl.style.display = 'none';
            }
          }, 200);
        }
      });

      document.body.appendChild(tooltipEl);
    }

    return tooltipEl;
  };

  function safeFormat(value) {
    return Number.isFinite(value) ? value.toFixed(2) : '-';
  }

  const externalTooltipHandler = (context) => {
    const { chart, tooltip } = context;
    const tooltipEl = getOrCreateTooltipElement();
    if (!tooltip) {
      return;
    }

    // Suppress tooltip when a point is pinned (floating button is shown)
    if (pinnedPointRef.current && onAskNubi) {
      tooltipEl.style.opacity = '0';
      tooltipEl.style.display = 'none';
      return;
    }

    // If tooltip is not active or there are no data points, set up hide timeout
    if (tooltip && (tooltip.opacity === 0 || !tooltip.dataPoints || tooltip.dataPoints.length === 0)) {
      if (!tooltipState.isHovered) {
        if (tooltipState.hideTimeout) {
          clearTimeout(tooltipState.hideTimeout);
        }

        tooltipState.hideTimeout = setTimeout(() => {
          if (!tooltipState.isHovered) {
            tooltipEl.style.opacity = 0;
            tooltipEl.style.display = 'none';
          }
        }, 200);
      }
      return;
    }

    // If tooltip is active, clear any hide timeout
    if (tooltipState.hideTimeout) {
      clearTimeout(tooltipState.hideTimeout);
      tooltipState.hideTimeout = null;
    }

    // Generate tooltip content
    const content =
      tooltip?.dataPoints
        .map((dataPoint) => {
          const datasetLabel = dataPoint.dataset.label || '';
          const rawValue = dataPoint.raw || dataPoint.parsed?.y || 0;

          // Extract unit from label (e.g., "Memory (MB)" -> "MB")
          // Limit capture group length to prevent ReDoS
          const unitMatch = datasetLabel.match(/\(([^)]{1,50})\)/);
          const unit = unitMatch ? ` ${unitMatch[1]}` : '';

          const value = typeof rawValue === 'number' ? rawValue.toFixed(2) + unit : rawValue;
          const color = dataPoint.dataset.backgroundColor || dataPoint.dataset.borderColor || '#ccc';

          const metrics = chartDataMetrices[datasetLabel] || {};
          const metricsTextArray = [];

          if (metrics.max != null) {
            metricsTextArray.push(`Max: ${metrics.max}${unit}`);
          }
          if (metrics.min != null) {
            metricsTextArray.push(`Min: ${metrics.min}${unit}`);
          }
          if (metrics.p99 != null) {
            metricsTextArray.push(`P99: ${metrics.p99}${unit}`);
          }
          if (metrics.avg != null) {
            metricsTextArray.push(`Avg: ${metrics.avg}${unit}`);
          }

          const metricsText = metricsTextArray.filter(Boolean).join(', ') || '';

          return `<div style="margin-bottom: 4px; max-width: ${TOOLTIP_MAX_WIDTH}px; z-index: 1000;">
              <div style="display: flex; align-items: center; margin-bottom: 4px;">
                <span
                  style="width: 10px; height: 10px; background: ${color}; display: inline-block; margin-right: 6px; border-radius: 2px;"></span>
                <strong>${tooltip.title?.[0] || ''}</strong>
              </div>
              <div style="margin: 4px 0;"><strong>Value:</strong> ${value}</div>
              <div style="margin: 6px 0; word-break: break-all; overflow-wrap: break-word;"><strong>Label:</strong> ${datasetLabel}</div>
              ${metricsText ? `<div><strong>Metrics:</strong><br>${metricsText}</div>` : ''}
            </div>`;
        })
        ?.join('') || '';

    tooltipEl.innerHTML = `
      <div style="max-width: ${TOOLTIP_MAX_WIDTH}px; max-height: ${TOOLTIP_MAX_HEIGHT};">
        ${content}
      </div>
    `;

    // Position the tooltip
    const { left, top } = chart.canvas.getBoundingClientRect();
    const windowWidth = window.innerWidth;
    const windowHeight = window.innerHeight;
    const scrollX = window.pageXOffset;
    const scrollY = window.pageYOffset;

    // Set tooltip position to 0,0 to measure its size
    tooltipEl.style.opacity = 0;
    tooltipEl.style.left = '0px';
    tooltipEl.style.top = '0px';
    tooltipEl.style.display = 'block';

    const tooltipWidth = tooltipEl.offsetWidth;
    const tooltipHeight = tooltipEl.offsetHeight;

    // Calculate optimal position
    let tooltipLeft = left + scrollX + (tooltip.caretX ?? 0) + TOOLTIP_OFFSET_X;
    let tooltipTop = top + scrollY + (tooltip.caretY ?? 0) + TOOLTIP_OFFSET_Y;

    // Adjust if tooltip would go off-screen
    if (tooltipTop + tooltipHeight > windowHeight + scrollY) {
      tooltipTop = top + scrollY + (tooltip.caretY ?? 0) - tooltipHeight - TOOLTIP_OFFSET_Y;
    }
    if (tooltipLeft + tooltipWidth > windowWidth + scrollX) {
      tooltipLeft = windowWidth + scrollX - tooltipWidth - 10;
    }
    if (tooltipLeft < 10 + scrollX) {
      tooltipLeft = 10 + scrollX;
    }
    if (tooltipTop < 10 + scrollY) {
      tooltipTop = 10 + scrollY;
    }

    // Apply final position and show tooltip
    tooltipEl.style.left = `${tooltipLeft}px`;
    tooltipEl.style.top = `${tooltipTop}px`;
    tooltipEl.style.opacity = 1;
    tooltipEl.style.display = 'block';
  };

  const customHtmlTooltipPlugin = {
    id: 'customHtmlTooltip',
    beforeEvent(chart, args) {
      // Reset the tooltip hover state when a new hover event starts
      if (args.event.type === 'mousemove') {
        tooltipState.isHovered = false;
      }
    },
    afterDraw: (chart, _args, _options) => {
      externalTooltipHandler({ chart, tooltip: chart.tooltip });
    },
  };

  const pinnedPointPlugin = {
    id: 'pinnedPointMarker',
    afterDraw: (chart) => {
      const pin = pinnedPointRef.current;
      if (!pin || !onAskNubi) {
        return;
      }
      const ctx = chart.ctx;
      ctx.save();
      // Outer ring
      ctx.beginPath();
      ctx.arc(pin.x, pin.y, 8, 0, Math.PI * 2);
      ctx.strokeStyle = '#3B82F6';
      ctx.lineWidth = 2;
      ctx.stroke();
      // Inner dot
      ctx.beginPath();
      ctx.arc(pin.x, pin.y, 3, 0, Math.PI * 2);
      ctx.fillStyle = '#3B82F6';
      ctx.fill();
      ctx.restore();
    },
  };

  let plugins = [noDataPlugin, customHtmlTooltipPlugin, pinnedPointPlugin, ...customPlugins];

  localOptions.animation = false;
  if (hasValidData && legendOptions && Object.keys(legendOptions).length > 0) {
    if (legendOptions.renderer === 'html') {
      localOptions.plugins.legend = {
        display: false,
      };
      plugins.push(htmlLegendPlugin(legendContainerId));
    } else {
      localOptions.plugins.legend = legendOptions;
    }
  }

  if (interactionOptions && Object.keys(interactionOptions).length > 0) {
    localOptions.interaction = interactionOptions;
  }

  let chartDataMetrices = {};
  for (let series of chartDatasets) {
    if (typeof series.data?.[0] === 'string') {
      series.data = series?.data?.map((item) => parseFloat(item));
    }

    let seriesData = isSingleDataPoint ? series?.data?.slice(1, -1) : series?.data;
    let sortedSeries = seriesData?.filter((item) => item !== '').sort((a, b) => a - b) ?? [];

    let min = sortedSeries[0];
    let max = sortedSeries[sortedSeries.length - 1];
    let p99 = sortedSeries[Math.floor(sortedSeries.length * 0.99)];
    let avg = sortedSeries.reduce((a, b) => a + b, 0) / sortedSeries.length;

    chartDataMetrices[series.label] = {
      min: safeFormat(min),
      max: safeFormat(max),
      p99: safeFormat(p99),
      avg: safeFormat(avg),
    };
  }

  // Ensure data points are clickable for "What happened here?" even with pointRadius: 0
  if (onAskNubi) {
    chartDatasets.forEach((ds) => {
      if (!ds.pointHitRadius || ds.pointHitRadius < 10) {
        ds.pointHitRadius = 10;
      }
    });
  }

  // Set up tooltip
  localOptions.plugins.tooltip = {
    enabled: false,
    external: externalTooltipHandler, // Use custom tooltip
    mode: 'index',
    intersect: false,
  };

  if (scaleOptions && Object.keys(scaleOptions).length > 0) {
    localOptions.scales = scaleOptions;
  }

  // Show all series values at the hovered x-position for multiple lines
  if (chartDatasets?.length > 1) {
    if (!localOptions.interaction) {
      localOptions.interaction = {};
    }
    localOptions.interaction.intersect = false;
    localOptions.interaction.mode = 'index';
  }

  useEffect(() => {
    calculateChartHeight();
    const handleResize = () => {
      calculateChartHeight();
      setPinnedPoint(null);
    };

    window.addEventListener('resize', handleResize);
    return () => {
      window.removeEventListener('resize', handleResize);
    };
  }, [data, dataset, labels.length, chartDatasets.length]);

  // Force the canvas to re-measure when its container's width changes (e.g. grid reflow,
  // sidebar toggle). Chart.js's built-in responsive mode listens for window resize, not
  // container resize, so cells that grow without a window event leave the canvas pinned
  // to its first-measured width.
  useEffect(() => {
    const container = chartContainerRef.current;
    if (!container || typeof ResizeObserver === 'undefined') {
      return;
    }
    const observer = new ResizeObserver(() => {
      const chart = chartRef.current;
      if (chart && typeof chart.resize === 'function') {
        chart.resize();
      }
    });
    observer.observe(container);
    return () => observer.disconnect();
  }, []);

  const handleClick = (event) => {
    if (!chartRef.current || (!onDataPointClick && !onAskNubi)) {
      return;
    }

    const chart = chartRef.current;
    const elements = getElementAtEvent(chart, event);

    if (elements.length > 0) {
      const { datasetIndex, index } = elements[0];
      const dataValue = chartData.datasets?.[datasetIndex]?.data?.[index] || '';
      const labelValue = chartData.datasets?.[datasetIndex]?.label || '';
      const label = chartData.labels?.[index] || '';

      if (onDataPointClick) {
        onDataPointClick({ dataValue, labelValue, label });
      }

      if (onAskNubi && labelValue.toLowerCase().includes('usage')) {
        const element = elements[0];
        const meta = chart.getDatasetMeta(element.datasetIndex);
        const point = meta.data[element.index];
        // Hide tooltip so it doesn't overlap the floating button
        const tooltipEl = document.getElementById('chartjs-tooltip');
        if (tooltipEl) {
          tooltipEl.style.opacity = '0';
          tooltipEl.style.display = 'none';
        }
        setPinnedPoint({
          x: point.x,
          y: point.y,
          dataValue,
          labelValue,
          label,
          epochTimestamp: timestamps?.[index] || null,
          metrics: chartDataMetrices[labelValue] || {},
        });
      }
    } else if (onAskNubi) {
      setPinnedPoint(null);
    }
  };

  // Clean up tooltip on unmount
  useEffect(() => {
    return () => {
      // Clean up tooltip
      if (tooltipState.hideTimeout) {
        clearTimeout(tooltipState.hideTimeout);
        tooltipState.hideTimeout = null;
      }

      const tooltipEl = document.getElementById('chartjs-tooltip');
      if (tooltipEl) {
        // Remove event listeners
        tooltipEl.removeEventListener('mouseenter', () => {
          // Handle mouse enter event
        });
        tooltipEl.removeEventListener('mouseleave', () => {
          // Handle mouse leave event
        });

        // Remove element
        if (tooltipEl.parentNode) {
          tooltipEl.parentNode.removeChild(tooltipEl);
        }
      }

      // Reset state
      tooltipState.isHovered = false;
    };
  }, []);

  return (
    <div ref={chartContainerRef} className='chart-responsive-container'>
      {loading ? (
        <div className='shimmer' style={{ height: chartHeight }} />
      ) : (
        <>
          {chartTitle && (
            <div
              style={{
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: 'var(--ds-brand-500)',
                wordBreak: 'break-all',
                textAlign: 'left',
                marginBottom: 'var(--ds-space-2)',
                lineHeight: '1.3',
              }}
            >
              {chartTitle}
            </div>
          )}
          <div
            style={{
              height: chartHeight + (legendOptions?.renderer === 'html' ? 0 : estimatedLegendHeight),
              width: '100%',
              minHeight: minHeight,
              overflow: 'visible',
              position: 'relative',
            }}
          >
            <Line ref={chartRef} id={uniqueId} options={localOptions} data={chartData} plugins={plugins} onClick={handleClick} />
            {onAskNubi && pinnedPoint && (
              <div
                style={{
                  position: 'absolute',
                  left: pinnedPoint.x + 12,
                  top: pinnedPoint.y - 16,
                  zIndex: 100,
                  pointerEvents: 'auto',
                }}
              >
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    onAskNubi({
                      dataValue: pinnedPoint.dataValue,
                      labelValue: pinnedPoint.labelValue,
                      label: pinnedPoint.label,
                      epochTimestamp: pinnedPoint.epochTimestamp,
                      chartTitle: chartTitle || '',
                      metrics: pinnedPoint.metrics,
                    });
                    setPinnedPoint(null);
                  }}
                  style={askNubiButtonStyle}
                >
                  <SearchIcon />
                  What happened here?
                </button>
              </div>
            )}
          </div>
          {legendOptions.renderer === 'html' && hasValidData && (
            <div
              id={legendContainerId}
              className='chart-legend-container'
              style={{
                overflow: 'auto',
                marginTop: 'var(--ds-space-4)',
                fontSize: 'var(--ds-text-small)',
                maxHeight: '300px',
              }}
            />
          )}
        </>
      )}
    </div>
  );
};

Charts.propTypes = {
  data: PropTypes.array,
  labels: PropTypes.array,
  timestamps: PropTypes.array,
  colors: PropTypes.array,
  chartLabel: PropTypes.oneOfType([PropTypes.string, PropTypes.array]),
  dataset: PropTypes.array,
  id: PropTypes.string,
  chartTitle: PropTypes.string,
  loading: PropTypes.bool,
  minHeight: PropTypes.number,
  legendOptions: PropTypes.any,
  interactionOptions: PropTypes.any,
  scaleOptions: PropTypes.any,
  onDataPointClick: PropTypes.func,
  onAskNubi: PropTypes.func,
  customPlugins: PropTypes.array,
  useFixedHeight: PropTypes.bool,
  fixedHeight: PropTypes.number,
  dynamicHeight: PropTypes.bool,
  integerYlabel: PropTypes.bool,
  fixedWidth: PropTypes.number,
};

export default withErrorBoundary(Charts);
