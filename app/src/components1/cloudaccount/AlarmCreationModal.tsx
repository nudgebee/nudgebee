import React, { useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Input } from '@components1/ds/Input';
import { Modal } from '@components1/ds/Modal';
import { Button as DsButton } from '@components1/ds/Button';
import { Banner } from '@components1/ds/Banner';
import { Chip } from '@components1/ds/Chip';
import { Accordion } from '@components1/ds/Accordion';
import { Divider } from '@components1/ds/Divider';
import { snackbar } from '@components1/common/snackbarService';
import apiRecommendations from '@api1/recommendation';
import { ds } from '@utils/colors';
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
      <Box sx={{ mb: ds.space[4] }}>
        <Typography variant='subtitle2' sx={{ fontWeight: ds.weight.semibold, mb: ds.space[2], color: ds.gray[700] }}>
          Metric Configuration
        </Typography>
        <Box sx={{ bgcolor: ds.background[200], p: ds.space[4], borderRadius: ds.radius.sm, border: `1px solid ${ds.gray[200]}` }}>
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: ds.space[4] }}>
            <Box>
              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
                Namespace
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: ds.weight.medium }}>
                {alarmConfig.namespace}
              </Typography>
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
                Metric Name
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: ds.weight.medium }}>
                {alarmConfig.metric_name}
              </Typography>
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
                Statistic
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: ds.weight.medium }}>
                {alarmConfig.statistic}
              </Typography>
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
                Period
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: ds.weight.medium }}>
                {alarmConfig.period} seconds
              </Typography>
            </Box>
          </Box>

          {alarmConfig.dimensions && alarmConfig.dimensions.length > 0 && (
            <Box sx={{ mt: ds.space[4] }}>
              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[2] }}>
                Dimensions
              </Typography>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[2] }}>
                {alarmConfig.dimensions.map((dim: any) => (
                  <Chip key={`${dim.name}-${dim.value}`} variant='tag' hue='blue' size='sm'>{`${dim.name}: ${dim.value}`}</Chip>
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
          <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
            Expression
          </Typography>
          <Typography variant='body2' sx={{ fontFamily: 'monospace', bgcolor: ds.gray[100], p: ds.space[2], borderRadius: ds.radius.sm }}>
            {metric.expression}
          </Typography>
        </Box>
      );
    }
    if (metric.metric_stat) {
      return (
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: ds.space[4] }}>
          <Box>
            <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
              Namespace
            </Typography>
            <Typography variant='body2'>{metric.metric_stat.metric.namespace}</Typography>
          </Box>
          <Box>
            <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
              Metric Name
            </Typography>
            <Typography variant='body2'>{metric.metric_stat.metric.metric_name}</Typography>
          </Box>
          <Box>
            <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
              Statistic
            </Typography>
            <Typography variant='body2'>{metric.metric_stat.stat}</Typography>
          </Box>
          <Box>
            <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
              Period
            </Typography>
            <Typography variant='body2'>{metric.metric_stat.period} seconds</Typography>
          </Box>
          {metric.metric_stat.metric.dimensions && metric.metric_stat.metric.dimensions.length > 0 && (
            <Box sx={{ gridColumn: '1 / -1' }}>
              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[2] }}>
                Dimensions
              </Typography>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[2] }}>
                {metric.metric_stat.metric.dimensions.map((dim: any) => (
                  <Chip key={`${dim.name}-${dim.value}`} variant='tag' hue='blue' size='sm'>{`${dim.name}: ${dim.value}`}</Chip>
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
      <Box sx={{ mb: ds.space[4] }}>
        <Typography variant='subtitle2' sx={{ fontWeight: ds.weight.semibold, mb: ds.space[2], color: ds.gray[700] }}>
          Metrics Configuration (Multi-Metric Alarm)
        </Typography>
        <Accordion
          items={alarmConfig.metrics.map((metric: Metric) => ({
            id: metric.id,
            label: (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                <Typography variant='body2' sx={{ fontWeight: ds.weight.semibold, fontFamily: 'monospace' }}>
                  {metric.id}
                </Typography>
                {metric.expression && (
                  <Chip tone='info' size='xs'>
                    Expression
                  </Chip>
                )}
                {metric.return_data && (
                  <Chip tone='warning' size='xs'>
                    Evaluated
                  </Chip>
                )}
              </Box>
            ),
            meta: metric.label ? (
              <Typography variant='caption' sx={{ color: ds.gray[600] }}>
                {metric.label}
              </Typography>
            ) : undefined,
            body: renderMetricDetail(metric),
          }))}
          defaultExpandedIds={alarmConfig.metrics.filter((m: Metric) => m.return_data).map((m: Metric) => m.id)}
        />
      </Box>
    );
  };

  const renderThresholdConfiguration = () => {
    if (!alarmConfig) {
      return null;
    }

    return (
      <Box sx={{ mb: ds.space[4] }}>
        <Typography variant='subtitle2' sx={{ fontWeight: ds.weight.semibold, mb: ds.space[2], color: ds.gray[700] }}>
          Threshold Configuration
        </Typography>
        <Box sx={{ bgcolor: ds.background[200], p: ds.space[4], borderRadius: ds.radius.sm, border: `1px solid ${ds.gray[200]}` }}>
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: ds.space[4] }}>
            <Box>
              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
                Comparison Operator
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: ds.weight.medium }}>
                {alarmConfig.comparison_operator} ({getComparisonOperatorDisplay(alarmConfig.comparison_operator)})
              </Typography>
            </Box>
            <Box>
              <Input
                label='Threshold'
                type='number'
                value={isNaN(threshold) ? '' : String(threshold)}
                onChange={(value) => setThreshold(parseFloat(value))}
                size='sm'
                required
                help='Adjust the threshold value as needed'
              />
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
                Evaluation Periods
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: ds.weight.medium }}>
                {alarmConfig.evaluation_periods}
              </Typography>
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
                Datapoints to Alarm
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: ds.weight.medium }}>
                {alarmConfig.datapoints_to_alarm}
              </Typography>
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
                Treat Missing Data
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: ds.weight.medium }}>
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
        <Banner tone='critical' surface='section' message='No alarm configuration found in recommendation' />
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', mt: ds.space[4] }}>
          <DsButton tone='secondary' size='md' onClick={onClose}>
            Close
          </DsButton>
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
      contentStyles={{ pt: ds.space[5], pb: ds.space[4] }}
      actionButtons={
        <Box sx={{ display: 'flex', gap: ds.space[4], justifyContent: 'flex-end', p: ds.space[4] }}>
          <DsButton tone='secondary' size='md' onClick={onClose} disabled={loading}>
            Cancel
          </DsButton>
          <DsButton tone='primary' size='md' onClick={handleCreateAlarm} loading={loading} disabled={loading || accountAccess === 'readonly'}>
            Create Alarm
          </DsButton>
        </Box>
      }
    >
      {/* Alarm Name - Editable */}
      <Box sx={{ mb: ds.space[5] }}>
        <Typography variant='subtitle2' sx={{ fontWeight: ds.weight.semibold, mb: ds.space[3], color: ds.gray[700] }}>
          Alarm Name
        </Typography>
        <Input
          value={alarmName}
          onChange={setAlarmName}
          placeholder='Enter alarm name...'
          required
          size='sm'
          help='Customize the alarm name to match your naming conventions'
        />
      </Box>

      {/* Resource Information */}
      <Box sx={{ mb: ds.space[5] }}>
        <Typography variant='subtitle2' sx={{ fontWeight: ds.weight.semibold, mb: ds.space[2], color: ds.gray[700] }}>
          Resource Information
        </Typography>
        <Box
          sx={{
            bgcolor: ds.blue[100],
            p: ds.space[4],
            borderRadius: ds.radius.sm,
            border: `1px solid ${ds.blue[300]}`,
          }}
        >
          <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: ds.space[4] }}>
            <Box>
              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
                Service
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: ds.weight.medium }}>
                {recommendation?.recommendation?.service_name || 'AWS CloudWatch'}
              </Typography>
            </Box>
            <Box>
              <Typography variant='caption' sx={{ color: ds.gray[600], display: 'block', mb: ds.space[1] }}>
                Resource
              </Typography>
              <Typography variant='body2' sx={{ fontWeight: ds.weight.medium }}>
                {recommendation?.resource_name || recommendation?.resource_id || 'N/A'}
              </Typography>
            </Box>
          </Box>
        </Box>
      </Box>

      <Divider sx={{ my: ds.space[4] }} />

      {/* Metric Configuration */}
      {isMetricMathAlarm ? renderMetricMathAlarm() : renderSimpleMetricAlarm()}

      <Divider sx={{ my: ds.space[4] }} />

      {/* Threshold Configuration */}
      {renderThresholdConfiguration()}

      <Divider sx={{ my: ds.space[4] }} />

      {/* Trigger Explanation */}
      <Box sx={{ mb: ds.space[4] }}>
        <Banner tone='info' surface='section' message={getTriggerExplanation()} />
      </Box>

      {/* Reason */}
      <Input
        label='Reason (optional)'
        type='textarea'
        rows={2}
        value={reason}
        onChange={setReason}
        placeholder='Enter a reason for creating this alarm...'
        size='sm'
      />

      {/* Error Display */}
      {error && (
        <Box sx={{ mt: ds.space[4] }}>
          <Banner tone='critical' surface='section' message={error} />
        </Box>
      )}
    </Modal>
  );
};

export default AlarmCreationModal;
