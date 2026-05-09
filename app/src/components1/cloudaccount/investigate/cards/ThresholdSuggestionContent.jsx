import PropTypes from 'prop-types';
import { Box, Typography, Chip, Divider } from '@mui/material';
import { colors } from 'src/utils/colors';
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, LineElement, PointElement, Title, Tooltip, Legend } from 'chart.js';
import { Bar } from 'react-chartjs-2';

ChartJS.register(CategoryScale, LinearScale, BarElement, LineElement, PointElement, Title, Tooltip, Legend);

const CONFIDENCE_COLORS = {
  high: { bg: '#e8f5e9', text: '#2e7d32' },
  medium: { bg: '#fff3e0', text: '#e65100' },
  low: { bg: '#fce4ec', text: '#c62828' },
};

const formatValue = (val) => {
  if (val == null) {
    return '-';
  }
  if (Math.abs(val) >= 1000000) {
    return `${(val / 1000000).toFixed(1)}M`;
  }
  if (Math.abs(val) >= 1000) {
    return `${(val / 1000).toFixed(1)}K`;
  }
  return val.toFixed(2);
};

const RECOMMENDATION_LABELS = {
  tune_threshold: { label: 'Tune Threshold', color: '#1565c0', bg: '#e3f2fd' },
  increase_duration: { label: 'Increase Duration', color: '#e65100', bg: '#fff3e0' },
  tune_both: { label: 'Tune Both', color: '#6a1b9a', bg: '#f3e5f5' },
  disable: { label: 'Consider Disabling', color: '#c62828', bg: '#fce4ec' },
  none: { label: 'No Change Needed', color: '#2e7d32', bg: '#e8f5e9' },
  review_alert: { label: 'Review Alert', color: '#bf360c', bg: '#fbe9e7' },
  not_eligible: { label: 'Not Eligible', color: '#546e7a', bg: '#eceff1' },
};

const CLASSIFICATION_LABELS = {
  false_positive: { label: 'False Positive', color: '#c62828', bg: '#fce4ec' },
  noisy_but_real: { label: 'Noisy but Real', color: '#e65100', bg: '#fff3e0' },
  broken: { label: 'Broken', color: '#c62828', bg: '#fce4ec' },
  healthy: { label: 'Healthy', color: '#2e7d32', bg: '#e8f5e9' },
};

const getOperatorSymbol = (operator) => {
  const op = (operator || '').toLowerCase();
  if (op.includes('greaterthanorequalto') || op === '>=') {
    return '>=';
  }
  if (op.includes('greaterthan') || op === '>') {
    return '>';
  }
  if (op.includes('lessthanorequalto') || op === '<=') {
    return '<=';
  }
  if (op.includes('lessthan') || op === '<') {
    return '<';
  }
  return '>';
};

const ThresholdSuggestionContent = ({ data }) => {
  if (!data?.available || !data?.suggestion) {
    return null;
  }

  const { alert_definition: alert, suggestion, firing_analysis: firing, alert_quality: quality } = data;
  const confidenceColor = CONFIDENCE_COLORS[suggestion.confidence] || CONFIDENCE_COLORS.low;
  const operatorLabel = getOperatorSymbol(alert?.operator);

  const percentiles = [
    { label: 'P50', value: suggestion.metric_p50 },
    { label: 'P90', value: suggestion.metric_p90 },
    { label: 'P95', value: suggestion.metric_p95 },
    { label: 'P99', value: suggestion.metric_p99 },
  ].filter((p) => p.value != null);

  const currentThreshold = alert?.current_threshold;
  const suggestedThreshold = suggestion.suggested_threshold;

  const chartData = {
    labels: percentiles.map((p) => p.label),
    datasets: [
      {
        type: 'bar',
        label: 'Metric Value',
        data: percentiles.map((p) => p.value),
        backgroundColor: 'rgba(37, 99, 235, 0.6)',
        borderColor: 'rgba(37, 99, 235, 1)',
        borderWidth: 1,
        borderRadius: 4,
        barPercentage: 0.5,
        order: 2,
      },
      ...(currentThreshold != null
        ? [
            {
              type: 'line',
              label: `Current Threshold (${operatorLabel} ${formatValue(currentThreshold)})`,
              data: percentiles.map(() => currentThreshold),
              borderColor: '#DC2626',
              borderWidth: 2,
              borderDash: [6, 4],
              pointRadius: 0,
              fill: false,
              order: 1,
            },
          ]
        : []),
      ...(suggestedThreshold != null
        ? [
            {
              type: 'line',
              label: `Suggested Threshold (${operatorLabel} ${formatValue(suggestedThreshold)})`,
              data: percentiles.map(() => suggestedThreshold),
              borderColor: '#16A34A',
              borderWidth: 2,
              borderDash: [6, 4],
              pointRadius: 0,
              fill: false,
              order: 1,
            },
          ]
        : []),
    ],
  };

  const chartOptions = {
    responsive: true,
    maintainAspectRatio: false,
    interaction: { intersect: false, mode: 'index' },
    scales: {
      x: {
        grid: { display: false },
        ticks: { font: { size: 12, weight: 600 } },
      },
      y: {
        beginAtZero: true,
        grid: { color: 'rgba(0,0,0,0.06)' },
        ticks: {
          callback: (val) => formatValue(val),
          font: { size: 11 },
        },
      },
    },
    plugins: {
      legend: {
        position: 'bottom',
        labels: {
          boxWidth: 12,
          boxHeight: 2,
          padding: 16,
          font: { size: 11 },
          usePointStyle: false,
        },
      },
      tooltip: {
        callbacks: {
          label: (ctx) => `${ctx.dataset.label}: ${typeof ctx.raw === 'number' ? ctx.raw.toFixed(2) : ctx.raw}`,
        },
      },
    },
  };

  return (
    <Box sx={{ p: 2, overflow: 'hidden', maxWidth: '100%' }}>
      {/* Alert info */}
      {alert?.alarm_name && (
        <Typography variant='body2' sx={{ fontWeight: 600, mb: 0.5 }}>
          {alert.alarm_name}
        </Typography>
      )}
      {alert?.metric_name && (
        <Typography variant='caption' sx={{ color: colors.text.secondary, display: 'block', mb: 2 }}>
          {alert.metric_namespace ? `${alert.metric_namespace} / ` : ''}
          {alert.metric_name}
          {alert.aggregation ? ` (${alert.aggregation})` : ''}
        </Typography>
      )}

      {/* Recommendation type and confidence */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 2, flexWrap: 'wrap' }}>
        {suggestion.recommendation_type && RECOMMENDATION_LABELS[suggestion.recommendation_type] && (
          <Chip
            label={RECOMMENDATION_LABELS[suggestion.recommendation_type].label}
            size='small'
            sx={{
              backgroundColor: RECOMMENDATION_LABELS[suggestion.recommendation_type].bg,
              color: RECOMMENDATION_LABELS[suggestion.recommendation_type].color,
              fontWeight: 600,
            }}
          />
        )}
        {quality?.classification && CLASSIFICATION_LABELS[quality.classification] && (
          <Chip
            label={CLASSIFICATION_LABELS[quality.classification].label}
            size='small'
            sx={{
              backgroundColor: CLASSIFICATION_LABELS[quality.classification].bg,
              color: CLASSIFICATION_LABELS[quality.classification].color,
              fontWeight: 600,
            }}
          />
        )}
        {suggestion.confidence && (
          <Chip
            label={`${suggestion.confidence} confidence`}
            size='small'
            sx={{
              backgroundColor: confidenceColor.bg,
              color: confidenceColor.text,
              fontWeight: 600,
              textTransform: 'capitalize',
            }}
          />
        )}
        {suggestion.estimated_reduction > 0 && (
          <Chip
            label={`~${Math.round(suggestion.estimated_reduction)}% noise reduction`}
            size='small'
            sx={{
              backgroundColor: '#e3f2fd',
              color: '#1565c0',
              fontWeight: 600,
            }}
          />
        )}
      </Box>

      {/* Duration recommendation */}
      {suggestion.suggested_duration > 0 && (
        <Box sx={{ mb: 2, p: 1.5, backgroundColor: '#fff8e1', borderRadius: 1, border: '1px solid #ffe082' }}>
          <Typography variant='body2' sx={{ fontWeight: 600, mb: 0.5 }}>
            Suggested evaluation window: {suggestion.suggested_duration} minutes
          </Typography>
          {suggestion.duration_reason && (
            <Typography variant='caption' sx={{ color: colors.text.secondary }}>
              {suggestion.duration_reason}
            </Typography>
          )}
        </Box>
      )}

      {/* Chart */}
      {percentiles.length > 0 && (
        <Box sx={{ position: 'relative', width: '100%', height: 240, mb: 2 }}>
          <Bar data={chartData} options={chartOptions} />
        </Box>
      )}

      {/* Reason */}
      {suggestion.reason && (
        <Typography variant='body2' sx={{ mb: 2, color: colors.text.secondary, wordBreak: 'break-word', overflowWrap: 'break-word' }}>
          {suggestion.reason}
        </Typography>
      )}

      {/* Firing analysis */}
      {firing && (
        <>
          <Divider sx={{ mb: 1.5 }} />
          <Typography variant='body2' sx={{ color: colors.text.secondary }}>
            {firing.total_occurrences} firings in {firing.time_range_days} days
            {firing.avg_firings_per_day > 0 ? ` (~${firing.avg_firings_per_day.toFixed(1)}/day)` : ''}
          </Typography>
        </>
      )}

      {/* Alert quality stats */}
      {quality && (
        <>
          <Divider sx={{ my: 1.5 }} />
          <Box sx={{ display: 'flex', gap: 2, flexWrap: 'wrap' }}>
            {quality.resolution_rate != null && (
              <Typography variant='caption' sx={{ color: colors.text.secondary }}>
                Resolution: {Math.round(quality.resolution_rate * 100)}%
              </Typography>
            )}
            {quality.transient_rate > 0 && (
              <Typography variant='caption' sx={{ color: colors.text.secondary }}>
                Transient: {Math.round(quality.transient_rate * 100)}%
              </Typography>
            )}
            {quality.engagement_rate != null && (
              <Typography variant='caption' sx={{ color: colors.text.secondary }}>
                Engagement: {Math.round(quality.engagement_rate * 100)}%
              </Typography>
            )}
          </Box>
        </>
      )}
    </Box>
  );
};

ThresholdSuggestionContent.propTypes = {
  data: PropTypes.shape({
    available: PropTypes.bool,
    alert_definition: PropTypes.shape({
      alarm_name: PropTypes.string,
      metric_name: PropTypes.string,
      metric_namespace: PropTypes.string,
      current_threshold: PropTypes.number,
      operator: PropTypes.string,
      aggregation: PropTypes.string,
    }),
    suggestion: PropTypes.shape({
      confidence: PropTypes.string,
      estimated_reduction: PropTypes.number,
      recommendation_type: PropTypes.string,
      suggested_threshold: PropTypes.number,
      suggested_duration: PropTypes.number,
      duration_reason: PropTypes.string,
      reason: PropTypes.string,
      metric_p50: PropTypes.number,
      metric_p90: PropTypes.number,
      metric_p95: PropTypes.number,
      metric_p99: PropTypes.number,
    }),
    firing_analysis: PropTypes.shape({
      total_occurrences: PropTypes.number,
      time_range_days: PropTypes.number,
      avg_firings_per_day: PropTypes.number,
    }),
    alert_quality: PropTypes.shape({
      classification: PropTypes.string,
      resolution_rate: PropTypes.number,
      transient_rate: PropTypes.number,
      engagement_rate: PropTypes.number,
    }),
  }).isRequired,
};

export default ThresholdSuggestionContent;
