import React, { useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Input } from '@ui/Input';
import { Modal } from '@ui/Modal';
import { Button as DsButton } from '@ui/Button';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { Accordion } from '@ui/Accordion';
import { Divider } from '@ui/Divider';
import { Select } from '@ui/Select';
import { toast as snackbar } from '@ui/Toast';
import apiRecommendations from '@api1/recommendation';
import apiCloudAccount, { CloudNotificationTarget } from '@api1/cloud-account';
import { ds } from '@utils/colors';
import type { Recommendation, Metric } from './types';

interface AlarmCreationModalProps {
  open: boolean;
  onClose: () => void;
  recommendation: Recommendation;
  accountId: string;
  onSuccess?: () => void;
  accountAccess?: string;
  provider?: string;
  region?: string;
}

// Provider-specific wording for the notification-target picker. Providers not
// listed here (or an unknown provider) hide the picker entirely.
const TARGET_VOCAB: Record<string, { label: string; singular: string; plural: string; empty: string; system: string }> = {
  aws: {
    label: 'SNS Topics',
    singular: 'SNS topic',
    plural: 'SNS topics',
    empty: 'No SNS topics exist in this region',
    system: 'CloudWatch',
  },
  gcp: {
    label: 'Notification Channels',
    singular: 'notification channel',
    plural: 'notification channels',
    empty: 'No notification channels exist in this project',
    system: 'Cloud Monitoring',
  },
  azure: {
    label: 'Action Groups',
    singular: 'action group',
    plural: 'action groups',
    empty: 'No enabled action groups exist in this subscription',
    system: 'Azure Monitor',
  },
};

const AlarmCreationModal: React.FC<AlarmCreationModalProps> = ({
  open,
  onClose,
  recommendation,
  accountId,
  onSuccess,
  accountAccess,
  provider,
  region,
}) => {
  const alarmConfig = recommendation?.recommendation?.alarm_config;
  const normalizedProvider = (provider || '').toLowerCase();
  const targetVocab = TARGET_VOCAB[normalizedProvider];
  // The alarm is created in the resource's region; list SNS topics for exactly
  // that region so a picked topic is always attachable.
  const alarmRegion = region || recommendation?.recommendation?.region || recommendation?.cloud_resourse?.meta?.region || '';

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
  const [notificationTargets, setNotificationTargets] = useState<CloudNotificationTarget[]>([]);
  const [selectedTargets, setSelectedTargets] = useState<string[]>([]);
  const [targetsLoading, setTargetsLoading] = useState(false);
  const [targetsError, setTargetsError] = useState<string | null>(null);

  const isMetricMathAlarm = alarmConfig?.metrics && alarmConfig.metrics.length > 0;

  // Update state when recommendation changes
  React.useEffect(() => {
    if (alarmConfig) {
      setAlarmName(generateUserFriendlyAlarmName());
      setThreshold(alarmConfig.threshold || 0);
      setSelectedTargets([]);
    }
  }, [alarmConfig, recommendation]);

  React.useEffect(() => {
    if (!open || !targetVocab || !accountId || accountId === 'demo') {
      return undefined;
    }
    if (normalizedProvider === 'aws' && !alarmRegion) {
      setTargetsError("Couldn't determine the alarm's region to list SNS topics");
      return undefined;
    }
    let active = true;
    setTargetsLoading(true);
    setTargetsError(null);
    setNotificationTargets([]);
    apiCloudAccount.listNotificationTargets(accountId, normalizedProvider === 'aws' ? alarmRegion : undefined).then((result) => {
      if (!active) {
        return;
      }
      setNotificationTargets(result.targets);
      setTargetsError(result.error || null);
      setTargetsLoading(false);
    });
    return () => {
      active = false;
    };
  }, [open, normalizedProvider, accountId, alarmRegion, targetVocab]);

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
      // Send custom_alarm_name, custom_threshold and custom_notification_targets
      // as separate override fields
      const response = await apiRecommendations.applyRecommendation(accountId, recommendation.id, {
        reason,
        custom_alarm_name: alarmName,
        custom_threshold: threshold,
        ...(selectedTargets.length > 0 ? { custom_notification_targets: selectedTargets } : {}),
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

  const getNotificationExplanation = () => {
    if (!targetVocab) {
      return '';
    }
    if (selectedTargets.length > 0) {
      const noun = selectedTargets.length === 1 ? targetVocab.singular : targetVocab.plural;
      return `${targetVocab.system} will notify ${selectedTargets.length} ${noun} when it fires.`;
    }
    return `No notification channel selected — ${targetVocab.system} won't notify anyone when it fires; NudgeBee still tracks the alarm and records its state changes.`;
  };

  const renderNotificationTargets = () => {
    if (!targetVocab) {
      return null;
    }

    const targetOptions = notificationTargets.map((target) => ({
      value: target.id,
      label: normalizedProvider === 'gcp' && target.type ? `${target.name} (${target.type})` : target.name,
    }));

    return (
      <Box sx={{ mb: ds.space[4] }}>
        <Typography variant='subtitle2' sx={{ fontWeight: ds.weight.semibold, mb: ds.space[2], color: ds.gray[700] }}>
          Notifications
        </Typography>
        <Select
          multiple
          id='alarm-notification-targets'
          label={targetVocab.label}
          size='sm'
          value={selectedTargets}
          onChange={setSelectedTargets}
          options={targetOptions}
          loading={targetsLoading}
          error={targetsError || undefined}
          placeholder={`Select ${targetVocab.plural}...`}
          help={
            !targetsLoading && !targetsError && targetOptions.length === 0
              ? `${targetVocab.empty} — the alarm will be created without cloud-native notifications.`
              : `Optional — ${targetVocab.system} will notify the selected ${targetVocab.plural} when the alarm fires.`
          }
        />
      </Box>
    );
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

      {targetVocab && (
        <>
          <Divider sx={{ my: ds.space[4] }} />
          {renderNotificationTargets()}
        </>
      )}

      <Divider sx={{ my: ds.space[4] }} />

      {/* Trigger Explanation */}
      <Box sx={{ mb: ds.space[4] }}>
        <Banner tone='info' surface='section' message={[getTriggerExplanation(), getNotificationExplanation()].filter(Boolean).join(' ')} />
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
