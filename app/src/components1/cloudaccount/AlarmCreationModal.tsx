import React, { useState } from 'react';
import { Box, Typography, TextField, Accordion, AccordionSummary, AccordionDetails, Chip, Alert, Divider } from '@mui/material';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import apiRecommendations from '@api1/recommendation';
import type { Recommendation, Metric } from './types';

interface AlarmCreationModalProps {
  open: boolean;
  onClose: () => void;
  recommendation: Recommendation;
  accountId: string;
  onSuccess?: () => void;
  accountAccess?: string;
}

const AlarmCreationModal: React.FC<AlarmCreationModalProps> = ({ open, onClose, recommendation, accountId, onSuccess, accountAccess }) => {
  const alarmConfig = recommendation?.recommendation?.alarm_config;

  // Generate user-friendly alarm name
  const generateUserFriendlyAlarmName = () => {
    if (!alarmConfig) {
      return '';
    }

    // Get resource name (e.g., "my-load-balancer" or "db-instance-1")
    const resourceName = recommendation?.resource_name || recommendation?.resource_id || '';

    // Get metric name from alarm config
    let metricName = '';
    if (alarmConfig.metrics && alarmConfig.metrics.length > 0) {
      // For metric math alarms, use the expression label
      const expressionMetric = alarmConfig.metrics.find((m: Metric) => m.return_data && m.expression);
      metricName = expressionMetric?.label || 'metric-math';
    } else {
      // For simple alarms, use the metric name
      metricName = alarmConfig.metric_name || '';
    }

    // Convert PascalCase/camelCase to kebab-case (e.g., "BackendConnectionErrors" -> "backend-connection-errors")
    const metricKebab = metricName
      .replace(/([a-z])([A-Z])/g, '$1-$2') // Insert hyphen between camelCase
      .replace(/([A-Z])([A-Z][a-z])/g, '$1-$2') // Handle acronyms
      .toLowerCase()
      .replace(/[^a-z0-9-]/g, '-') // Replace non-alphanumeric with hyphen
      .replace(/-+/g, '-') // Replace multiple hyphens with single
      .replace(/(?:^-|-$)/g, ''); // Trim leading/trailing hyphens

    // Build the alarm name: {resource}-{metric}-alarm
    const parts = [];
    if (resourceName) {
      // Clean up resource name (remove ARN prefix if present, take last part)
      const cleanResourceName = resourceName.split('/').pop()?.split(':').pop() || resourceName;
      parts.push(cleanResourceName);
    }
    if (metricKebab) {
      parts.push(metricKebab);
    }
    parts.push('alarm');

    return parts.join('-').substring(0, 255); // CloudWatch alarm name max length is 255
  };

  const [reason, setReason] = useState('Creating CloudWatch alarm from Nudgebee recommendation');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [alarmName, setAlarmName] = useState(generateUserFriendlyAlarmName());
  const [threshold, setThreshold] = useState<number>(alarmConfig?.threshold || 0);

  const isMetricMathAlarm = alarmConfig?.metrics && alarmConfig.metrics.length > 0;

  // Update state when recommendation changes
  React.useEffect(() => {
    if (alarmConfig) {
      setAlarmName(generateUserFriendlyAlarmName());
      setThreshold(alarmConfig.threshold || 0);
    }
  }, [alarmConfig, recommendation]);

  const handleCreateAlarm = async () => {
    // Validate inputs
    if (!alarmName.trim()) {
      setError('Alarm name is required');
      return;
    }

    if (threshold === null || threshold === undefined || isNaN(threshold)) {
      setError('Threshold is required and must be a valid number');
      return;
    }

    setLoading(true);
    setError(null);

    try {
      // Send custom_alarm_name and custom_threshold as separate override fields
      const response = await apiRecommendations.applyRecommendation(accountId, recommendation.id, {
        reason,
        custom_alarm_name: alarmName,
        custom_threshold: threshold,
      });

      // Check for GraphQL errors in the response
      if (response?.errors && response.errors.length > 0) {
        const errorMessage = response.errors[0]?.message || 'Failed to create CloudWatch alarm';
        setLoading(false);
        setError(errorMessage);
        snackbar.error(errorMessage);
        return;
      }

      setLoading(false);
      snackbar.success(`CloudWatch alarm "${alarmName}" created successfully`);
      onClose();
      if (onSuccess) {
        onSuccess();
      }
    } catch (err) {
      setLoading(false);
      const errorMessage = (err as any)?.response?.data?.message || (err as Error)?.message || 'Failed to create CloudWatch alarm';
      setError(errorMessage);
      snackbar.error(errorMessage);
    }
  };

  const getComparisonOperatorDisplay = (operator: string) => {
    const operatorMap: Record<string, string> = {
      GreaterThanThreshold: '>',
      GreaterThanOrEqualToThreshold: '>=',
      LessThanThreshold: '<',
      LessThanOrEqualToThreshold: '<=',
      LessThanLowerOrGreaterThanUpperThreshold: 'Outside Range',
      LessThanLowerThreshold: '< Lower',
      GreaterThanUpperThreshold: '> Upper',
    };
    return operatorMap[operator] || operator;
  };

  const getTriggerExplanation = () => {
    if (!alarmConfig) {
      return '';
    }

    const operator = getComparisonOperatorDisplay(alarmConfig.comparison_operator);
    const evalPeriods = alarmConfig.evaluation_periods;
    const datapointsToAlarm = alarmConfig.datapoints_to_alarm;
    const periodMinutes = Math.floor(alarmConfig.period / 60);

    if (isMetricMathAlarm) {
      // Find the expression that returns data
      const expressionMetric = alarmConfig.metrics?.find((m: any) => m.return_data && m.expression);
      const expressionLabel = expressionMetric?.label || 'calculated value';

      return `Alarm triggers when ${expressionLabel} ${operator} ${threshold} for ${datapointsToAlarm} out of ${evalPeriods} evaluation periods (${periodMinutes} min each)`;
    }
    const metricName = alarmConfig.metric_name;
    const statistic = alarmConfig.statistic;

    return `Alarm triggers when ${metricName} (${statistic}) ${operator} ${threshold} for ${datapointsToAlarm} out of ${evalPeriods} evaluation periods (${periodMinutes} min each)`;
  };

  const renderSimpleMetricAlarm = () => {
    if (!alarmConfig) {
      return null;
    }

    return (
      <Box sx={{ mb: 2 }}>
        <Typography variant='subtitle2' sx={{ fontWeight: 600, mb: 1, color: '#374151' }}>
          Metric Configuration
        </Typography>
        <Box sx={{ bgcolor: '#F9FAFB', p: 2, borderRadius: 1, border: '1px solid #E5E7EB' }}>
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 2 }}>
            <Box>
              <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
                Namespace
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: 500 }}>
                {alarmConfig.namespace}
              </Typography>
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
                Metric Name
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: 500 }}>
                {alarmConfig.metric_name}
              </Typography>
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
                Statistic
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: 500 }}>
                {alarmConfig.statistic}
              </Typography>
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
                Period
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: 500 }}>
                {alarmConfig.period} seconds
              </Typography>
            </Box>
          </Box>

          {alarmConfig.dimensions && alarmConfig.dimensions.length > 0 && (
            <Box sx={{ mt: 2 }}>
              <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 1 }}>
                Dimensions
              </Typography>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1 }}>
                {alarmConfig.dimensions.map((dim: any) => (
                  <Chip key={`${dim.name}-${dim.value}`} label={`${dim.name}: ${dim.value}`} size='small' sx={{ bgcolor: '#E0E7FF' }} />
                ))}
              </Box>
            </Box>
          )}
        </Box>
      </Box>
    );
  };

  const renderMetricDetail = (metric: Metric) => {
    if (metric.expression) {
      return (
        <Box>
          <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
            Expression
          </Typography>
          <Typography variant='body2' sx={{ fontFamily: 'monospace', bgcolor: '#F3F4F6', p: 1, borderRadius: 0.5 }}>
            {metric.expression}
          </Typography>
        </Box>
      );
    }
    if (metric.metric_stat) {
      return (
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 2 }}>
          <Box>
            <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
              Namespace
            </Typography>
            <Typography variant='body2'>{metric.metric_stat.metric.namespace}</Typography>
          </Box>
          <Box>
            <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
              Metric Name
            </Typography>
            <Typography variant='body2'>{metric.metric_stat.metric.metric_name}</Typography>
          </Box>
          <Box>
            <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
              Statistic
            </Typography>
            <Typography variant='body2'>{metric.metric_stat.stat}</Typography>
          </Box>
          <Box>
            <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
              Period
            </Typography>
            <Typography variant='body2'>{metric.metric_stat.period} seconds</Typography>
          </Box>
          {metric.metric_stat.metric.dimensions && metric.metric_stat.metric.dimensions.length > 0 && (
            <Box sx={{ gridColumn: '1 / -1' }}>
              <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 1 }}>
                Dimensions
              </Typography>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1 }}>
                {metric.metric_stat.metric.dimensions.map((dim: any) => (
                  <Chip key={`${dim.name}-${dim.value}`} label={`${dim.name}: ${dim.value}`} size='small' sx={{ bgcolor: '#E0E7FF' }} />
                ))}
              </Box>
            </Box>
          )}
        </Box>
      );
    }
    return null;
  };

  const renderMetricMathAlarm = () => {
    if (!alarmConfig?.metrics) {
      return null;
    }

    return (
      <Box sx={{ mb: 2 }}>
        <Typography variant='subtitle2' sx={{ fontWeight: 600, mb: 1, color: '#374151' }}>
          Metrics Configuration (Multi-Metric Alarm)
        </Typography>
        {alarmConfig.metrics.map((metric: Metric) => (
          <Accordion key={metric.id} defaultExpanded={metric.return_data} sx={{ mb: 1 }}>
            <AccordionSummary expandIcon={<ExpandMoreIcon />} sx={{ bgcolor: metric.return_data ? '#FFF4E5' : '#F9FAFB' }}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, width: '100%' }}>
                <Typography variant='body2' sx={{ fontWeight: 600, fontFamily: 'monospace' }}>
                  {metric.id}
                </Typography>
                {metric.expression && <Chip label='Expression' size='small' color='primary' sx={{ height: 20 }} />}
                {metric.return_data && <Chip label='Evaluated' size='small' color='warning' sx={{ height: 20 }} />}
                {metric.label && (
                  <Typography variant='caption' sx={{ ml: 'auto', color: '#737373' }}>
                    {metric.label}
                  </Typography>
                )}
              </Box>
            </AccordionSummary>
            <AccordionDetails>{renderMetricDetail(metric)}</AccordionDetails>
          </Accordion>
        ))}
      </Box>
    );
  };

  const renderThresholdConfiguration = () => {
    if (!alarmConfig) {
      return null;
    }

    return (
      <Box sx={{ mb: 2 }}>
        <Typography variant='subtitle2' sx={{ fontWeight: 600, mb: 1, color: '#374151' }}>
          Threshold Configuration
        </Typography>
        <Box sx={{ bgcolor: '#F9FAFB', p: 2, borderRadius: 1, border: '1px solid #E5E7EB' }}>
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 2 }}>
            <Box>
              <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
                Comparison Operator
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: 500 }}>
                {alarmConfig.comparison_operator} ({getComparisonOperatorDisplay(alarmConfig.comparison_operator)})
              </Typography>
            </Box>
            <Box>
              <TextField
                fullWidth
                label='Threshold'
                type='number'
                value={threshold}
                onChange={(e) => setThreshold(parseFloat(e.target.value))}
                variant='outlined'
                size='small'
                required
                inputProps={{ step: 'any' }}
                helperText='Adjust the threshold value as needed'
              />
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
                Evaluation Periods
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: 500 }}>
                {alarmConfig.evaluation_periods}
              </Typography>
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
                Datapoints to Alarm
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: 500 }}>
                {alarmConfig.datapoints_to_alarm}
              </Typography>
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
                Treat Missing Data
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: 500 }}>
                {alarmConfig.treat_missing_data}
              </Typography>
            </Box>
          </Box>
        </Box>
      </Box>
    );
  };

  if (!alarmConfig) {
    return (
      <Modal open={open} handleClose={onClose} width='sm' title='Create CloudWatch Alarm'>
        <Alert severity='error'>No alarm configuration found in recommendation</Alert>
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', mt: 2 }}>
          <CustomButton variant='secondary' size='Medium' text='Close' onClick={onClose} />
        </Box>
      </Modal>
    );
  }

  return (
    <Modal
      open={open}
      handleClose={onClose}
      width='md'
      title='Create CloudWatch Alarm'
      loader={loading}
      contentStyles={{ pt: 3, pb: 2 }}
      actionButtons={
        <Box sx={{ display: 'flex', gap: 2, justifyContent: 'flex-end', p: 2 }}>
          <CustomButton variant='secondary' size='Medium' text='Cancel' onClick={onClose} disabled={loading} />
          <CustomButton
            size='Medium'
            text={loading ? 'Creating Alarm...' : 'Create Alarm'}
            onClick={handleCreateAlarm}
            disabled={loading || accountAccess === 'readonly'}
          />
        </Box>
      }
    >
      {/* Alarm Name - Editable */}
      <Box sx={{ mb: 3 }}>
        <Typography variant='subtitle2' sx={{ fontWeight: 600, mb: 1.5, color: '#374151' }}>
          Alarm Name
        </Typography>
        <TextField
          fullWidth
          value={alarmName}
          onChange={(e) => setAlarmName(e.target.value)}
          placeholder='Enter alarm name...'
          variant='outlined'
          required
          helperText='Customize the alarm name to match your naming conventions'
        />
      </Box>

      {/* Resource Information */}
      <Box sx={{ mb: 3 }}>
        <Typography variant='subtitle2' sx={{ fontWeight: 600, mb: 1, color: '#374151' }}>
          Resource Information
        </Typography>
        <Box
          sx={{
            bgcolor: '#F3F6FD',
            p: 2,
            borderRadius: 1,
            border: '1px solid rgba(49, 98, 208, 0.30)',
          }}
        >
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 2 }}>
            <Box>
              <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
                Service
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: 500 }}>
                {recommendation?.recommendation?.service_name || 'AWS CloudWatch'}
              </Typography>
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: '#737373', display: 'block', mb: 0.5 }}>
                Resource
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: 500 }}>
                {recommendation?.resource_name || recommendation?.resource_id || 'N/A'}
              </Typography>
            </Box>
          </Box>
        </Box>
      </Box>

      <Divider sx={{ my: 2 }} />

      {/* Metric Configuration */}
      {isMetricMathAlarm ? renderMetricMathAlarm() : renderSimpleMetricAlarm()}

      <Divider sx={{ my: 2 }} />

      {/* Threshold Configuration */}
      {renderThresholdConfiguration()}

      <Divider sx={{ my: 2 }} />

      {/* Trigger Explanation */}
      <Alert severity='info' sx={{ mb: 2 }}>
        <Typography variant='body2'>{getTriggerExplanation()}</Typography>
      </Alert>

      {/* Reason TextField */}
      <TextField
        fullWidth
        label='Reason (optional)'
        multiline
        rows={2}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        placeholder='Enter a reason for creating this alarm...'
        variant='outlined'
      />

      {/* Error Display */}
      {error && (
        <Alert severity='error' sx={{ mt: 2 }}>
          {error}
        </Alert>
      )}
    </Modal>
  );
};

export default AlarmCreationModal;
