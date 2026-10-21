import React, { useEffect, useRef, useState } from 'react';
import { Typography, Grid, Box } from '@mui/material';
import { Chip } from '@ui/Chip';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { Modal } from '@ui/Modal';
import { Button as DsButton } from '@ui/Button';
import { Label } from '@ui/Label';
import { Callout } from '@ui/Callout';
import apiKubernetes1 from '@api1/kubernetes1';
import k8sApi from '@api1/kubernetes';
import { toast as snackbar } from '@ui/Toast';
import { ds } from '@utils/colors';
import { hasWriteAccess } from '@lib/auth';
import { formatObjectivePercent } from 'src/utils/common';
import { CPU_SLO_TYPES, ForecastResult, forecastDailyLine, forecastStatus, forecastSummary, isCpuSloName, quantity } from './cpuForecast';

interface SLOConfigDialogProps {
  open: boolean;
  onClose: () => void;
  accountId: string;
  /** Pre-selected workload (from workloads page). If provided, namespace/workload selectors are hidden. */
  workload?: {
    name: string;
    namespace: string;
    cloud_resource_id: string;
  } | null;
  /** Pre-loaded SLO config for editing (from workloads page). */
  initialConfig?: any[];
  /** Whether the pre-loaded config is an existing one (edit mode). */
  isEdit?: boolean;
  /** Callback after successful create/update. */
  onSuccess?: () => void;
}

// slo_config_v2 casts numeric columns to TEXT, so rows the user never touched
// come back as strings and the Go decoder (strict float64) rejects them.
const toNumber = (value: any) => (value === null || value === undefined || value === '' || !Number.isFinite(Number(value)) ? null : Number(value));

// An SLI counts as configured once any of its values is set; an untouched or cleared
// one is left out of the save, so clearing a field never writes a 0 threshold.
const isConfigured = (c: any) =>
  isCpuSloName(c.name) ? toNumber(c.threshold) !== null : toNumber(c.goal) !== null || toNumber(c.threshold) !== null;

type ForecastState = { loading: boolean; result?: ForecastResult | null; error?: string };

const SectionCard = ({ title, children }: { title: string; children: React.ReactNode }) => (
  <Box
    sx={{
      border: `1px solid ${ds.blue[400]}`,
      borderRadius: 'var(--ds-radius-lg)',
      padding: 'var(--ds-space-4)',
      marginBottom: 'var(--ds-space-4)',
    }}
  >
    <Typography
      sx={{
        fontWeight: 'var(--ds-font-weight-semibold)',
        fontSize: 'var(--ds-text-body-lg)',
        marginBottom: 'var(--ds-space-3)',
        color: ds.blue[500],
      }}
    >
      {title}
    </Typography>
    {children}
  </Box>
);

const SLOConfigDialog: React.FC<SLOConfigDialogProps> = ({ open, onClose, accountId, workload, initialConfig, isEdit = false, onSuccess }) => {
  const [sloConfig, setSloConfig] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [isEditMode, setIsEditMode] = useState(false);
  // Raw text per SLI while the objective field is being edited.
  const [goalInputs, setGoalInputs] = useState<Record<string, string>>({});
  // Raw text per CPU SLO threshold, and the last Check now result per type.
  const [cpuInputs, setCpuInputs] = useState<Record<string, string>>({});
  const [forecasts, setForecasts] = useState<Record<string, ForecastState>>({});

  // Workload selection state (only used when no workload prop is provided)
  const [availableNamespaces, setAvailableNamespaces] = useState<string[]>([]);
  const [availableWorkloads, setAvailableWorkloads] = useState<any[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState<string>('');
  const [selectedWorkload, setSelectedWorkload] = useState<any>(null);

  const showWorkloadSelector = !workload;
  const activeWorkload = workload || selectedWorkload;
  const requestIdRef = useRef(0);

  // Reset state when dialog closes, hydrate when open/props change
  useEffect(() => {
    if (!open) {
      setSloConfig([]);
      setIsEditMode(false);
      setGoalInputs({});
      setCpuInputs({});
      setForecasts({});
      setSelectedNamespace('');
      setSelectedWorkload(null);
      setAvailableWorkloads([]);
      requestIdRef.current++;
      return;
    }

    // If workload is pre-selected, use initialConfig
    if (workload) {
      setSloConfig(initialConfig || []);
      setIsEditMode(isEdit);
    }
  }, [open, workload, initialConfig, isEdit]);

  // Fetch namespaces when dialog opens (only for workload selector mode)
  useEffect(() => {
    if (!open || !showWorkloadSelector) return;
    const reqId = ++requestIdRef.current;
    k8sApi.getK8sNamespaceNames(accountId).then((res: any) => {
      if (reqId !== requestIdRef.current) return;
      setAvailableNamespaces(res?.data?.namespaces || []);
    });
  }, [open, showWorkloadSelector]);

  // Fetch workloads when namespace is selected
  useEffect(() => {
    if (!selectedNamespace || !open || !showWorkloadSelector) {
      setAvailableWorkloads([]);
      setSelectedWorkload(null);
      return;
    }
    const reqId = ++requestIdRef.current;
    k8sApi.getAllK8sWorkload({ accountId, namespaceName: selectedNamespace }).then((res: any) => {
      if (reqId !== requestIdRef.current) return;
      setAvailableWorkloads(res?.data || []);
    });
  }, [selectedNamespace, open]);

  // Check existing SLO config when workload is selected via selector
  useEffect(() => {
    if (!selectedWorkload || !open || !showWorkloadSelector) return;
    const reqId = ++requestIdRef.current;
    apiKubernetes1
      .getSLOConfig({
        cloud_account_id: accountId,
        namespace: selectedWorkload.namespace,
        workload_name: selectedWorkload.name,
      })
      .then((res: any) => {
        if (reqId !== requestIdRef.current) return;
        const workloadSloConfig = res?.data?.data?.slo_config_list?.data || [];
        if (workloadSloConfig && workloadSloConfig.length > 0) {
          setSloConfig(workloadSloConfig.map((n: any) => n.config[0]));
          setIsEditMode(true);
        } else {
          setSloConfig([]);
          setIsEditMode(false);
        }
      })
      .catch(() => {
        if (reqId !== requestIdRef.current) return;
        setSloConfig([]);
        setIsEditMode(false);
      });
  }, [selectedWorkload]);

  const handleInput = (event: any, inspection: string, type: string) => {
    const raw = String(event.target.value ?? '');

    if (type === 'goal') {
      // Keep the raw text so a decimal objective can actually be typed. The
      // old handler ran parseInt on every keystroke, so "99." collapsed back
      // to "99" and 99.9 / 99.95 / 99.99 were impossible to enter -- the
      // objectives most SLOs actually use.
      setGoalInputs((prev) => ({ ...prev, [inspection]: raw }));
    }

    const parsed = type === 'goal' ? parseFloat(raw) : parseInt(raw, 10);
    // Objectives are a percentage in (0, 100]; goal is stored as a fraction.
    const value = type === 'goal' ? (Number.isFinite(parsed) && parsed > 0 && parsed <= 100 ? parsed / 100 : null) : parsed;

    const existingConfigIndex = sloConfig.findIndex((config: any) => config.name === inspection);
    if (existingConfigIndex === -1) {
      setSloConfig((prev) => [
        ...prev,
        {
          enabled: true,
          name: inspection,
          goal: type === 'goal' ? value : null,
          threshold: type === 'threshold' ? value : null,
        },
      ]);
    } else {
      const updated = [...sloConfig];
      const updatedConfig = { ...updated[existingConfigIndex] };
      if (type === 'goal') {
        updatedConfig.goal = value;
      } else if (type === 'threshold') {
        updatedConfig.threshold = value;
      }
      updated[existingConfigIndex] = updatedConfig;
      setSloConfig(updated);
    }
  };

  const showConfiguredValue = (inspection: string, type: string) => {
    // While the field is being edited, show exactly what was typed — rendering
    // the parsed value back would fight the user mid-keystroke on "99.".
    if (type === 'goal' && goalInputs[inspection] !== undefined) {
      return goalInputs[inspection];
    }
    if (sloConfig && sloConfig.length > 0) {
      const filterInspection = sloConfig.filter((n: any) => n.name === inspection);
      if (filterInspection && filterInspection.length === 1) {
        if (type === 'goal') {
          if (!isNaN(filterInspection[0].goal)) {
            return formatObjectivePercent(filterInspection[0].goal);
          }
        } else if (type === 'threshold') {
          return filterInspection[0].threshold != null ? String(filterInspection[0].threshold) : undefined;
        }
      }
    }
  };

  const handleSubmit = () => {
    if (!activeWorkload) {
      snackbar.error('Please select a workload');
      return;
    }
    // Any one SLI is enough, so a workload can have CPU SLOs only; each one that is
    // configured must be complete.
    const configured = sloConfig.filter(isConfigured);
    if (configured.length === 0) {
      snackbar.error('Configure at least one SLO');
      return;
    }
    for (const c of configured) {
      if (c.name === 'availability' && !toNumber(c.goal)) {
        snackbar.error('Configure Availability Objective');
        return;
      }
      if (c.name === 'latency' && !toNumber(c.goal)) {
        snackbar.error('Configure Latency Objective');
        return;
      }
      if (c.name === 'latency' && !toNumber(c.threshold)) {
        snackbar.error('Configure Latency Threshold');
        return;
      }
      const cpu = CPU_SLO_TYPES.find((t) => t.name === c.name);
      const threshold = toNumber(c.threshold) ?? 0;
      if (cpu && (threshold <= 0 || threshold > 100)) {
        snackbar.error(`CPU ${cpu.title.toLowerCase()} threshold must be above 0% and at most 100%`);
        return;
      }
    }

    const data = {
      cloud_account_id: accountId,
      config: configured.map((c: any) => ({ ...c, goal: toNumber(c.goal), threshold: toNumber(c.threshold) })),
      namespace: activeWorkload.namespace,
      workload_id: activeWorkload.cloud_resource_id,
      workload_name: activeWorkload.name,
    };

    setLoading(true);
    const apiCall = isEditMode ? apiKubernetes1.updateSLOConfig(data) : apiKubernetes1.createSLOConfig(data);
    const successKey = isEditMode ? 'slo_config_update' : 'slo_config_create';
    const actionLabel = isEditMode ? 'Update' : 'Create';

    apiCall
      .then((res: any) => {
        const success = res?.data?.data?.[successKey]?.data?.success || false;
        if (success) {
          snackbar.success(`SLO Config ${actionLabel}d`);
          onClose();
          onSuccess?.();
        } else {
          snackbar.error(`Failed to ${actionLabel} SLO Config`);
        }
      })
      .catch(() => {
        snackbar.error(`Failed to ${actionLabel} SLO Config`);
      })
      .finally(() => setLoading(false));
  };

  const setCpuThreshold = (name: string, raw: string) => {
    setCpuInputs((prev) => ({ ...prev, [name]: raw }));
    const parsed = parseFloat(raw);
    const threshold = Number.isFinite(parsed) ? parsed : null;
    setSloConfig((prev) =>
      prev.some((c: any) => c.name === name)
        ? prev.map((c: any) => (c.name === name ? { ...c, threshold } : c))
        : [...prev, { enabled: true, name, goal: 0, threshold }]
    );
    setForecasts((prev) => ({ ...prev, [name]: { loading: false } }));
  };

  const cpuThresholdText = (name: string) => {
    if (cpuInputs[name] !== undefined) return cpuInputs[name];
    const threshold = toNumber(sloConfig.find((c: any) => c.name === name)?.threshold);
    return threshold !== null ? String(threshold) : '';
  };

  // A saved CPU SLO (it has an id) is removed on the server; an unsaved one only here.
  const isSavedCpu = (name: string) => sloConfig.some((c: any) => c.name === name && c.id);

  const handleCheckNow = (name: string) => {
    const threshold = toNumber(sloConfig.find((c: any) => c.name === name)?.threshold);
    if (!activeWorkload || threshold === null || threshold <= 0 || threshold > 100) {
      snackbar.error('Enter a threshold between 0% and 100% first');
      return;
    }
    setForecasts((prev) => ({ ...prev, [name]: { loading: true } }));
    apiKubernetes1
      .generateForecast({
        account_id: accountId,
        workload_namespace: activeWorkload.namespace,
        workload_name: activeWorkload.name,
        metric: name,
        threshold,
      })
      .then((result: ForecastResult) => setForecasts((prev) => ({ ...prev, [name]: { loading: false, result } })))
      .catch((err: any) => setForecasts((prev) => ({ ...prev, [name]: { loading: false, error: err?.message || 'Could not run the forecast' } })));
  };

  const handleRemoveCpu = (name: string) => {
    const dropLocally = () => {
      setSloConfig((prev) => prev.filter((c: any) => c.name !== name));
      setCpuInputs((prev) => ({ ...prev, [name]: '' }));
      setForecasts((prev) => ({ ...prev, [name]: { loading: false } }));
    };
    if (!activeWorkload || !isSavedCpu(name)) {
      dropLocally();
      return;
    }
    setLoading(true);
    apiKubernetes1
      .deleteSLOConfig({ cloud_account_id: accountId, workload_name: activeWorkload.name, namespace: activeWorkload.namespace, name })
      .then((res: any) => {
        if (res?.data?.data?.slo_config_delete?.data?.success) {
          dropLocally();
          snackbar.success('CPU SLO removed');
          onSuccess?.();
        } else {
          snackbar.error('Failed to remove CPU SLO');
        }
      })
      .catch(() => snackbar.error('Failed to remove CPU SLO'))
      .finally(() => setLoading(false));
  };

  const renderForecast = (name: string) => {
    const f = forecasts[name];
    if (!f) return null;
    if (f.loading) {
      return (
        <Typography variant='body2' sx={{ color: ds.gray[700], marginTop: 'var(--ds-space-2)' }}>
          Checking the last 3 days of metrics…
        </Typography>
      );
    }
    if (f.error) {
      return (
        <Box sx={{ marginTop: 'var(--ds-space-2)' }}>
          <Callout tone='critical'>{f.error}</Callout>
        </Box>
      );
    }
    if (!f.result) return null;
    const status = forecastStatus(f.result);
    const daily = forecastDailyLine(f.result);
    return (
      <Box
        sx={{ marginTop: 'var(--ds-space-2)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}
        data-testid={`slo-${name}-forecast`}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
          <Label text={status.text} tone={status.tone} size='sm' />
          <Typography variant='body2'>{forecastSummary(f.result)}</Typography>
        </Box>
        {daily && (
          <Typography variant='caption' sx={{ color: ds.gray[700] }}>
            {daily}
          </Typography>
        )}
        {f.result.suggested_limit ? (
          <Typography variant='caption' sx={{ color: ds.gray[700] }}>
            {`Suggested CPU limit per pod: ${quantity(f.result.suggested_limit, f.result.limit_unit)}`}
          </Typography>
        ) : null}
      </Box>
    );
  };

  const formContent = () => {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', paddingTop: 'var(--ds-space-2)' }}>
        {/* Workload selector (only when opened from SLO page) */}
        {showWorkloadSelector && (
          <SectionCard title='Select Workload'>
            <Grid container spacing={2}>
              <Grid item xs={6} data-testid='slo-namespace-dropdown'>
                <Select
                  id='slo-namespace'
                  label='Namespace'
                  value={selectedNamespace}
                  options={availableNamespaces}
                  onChange={(next) => {
                    setSelectedNamespace(next || '');
                    setSelectedWorkload(null);
                  }}
                  placeholder='Select namespace'
                />
              </Grid>
              <Grid item xs={6} data-testid='slo-workload-dropdown'>
                <Select
                  id='slo-workload'
                  label='Workload'
                  value={selectedWorkload?.name || ''}
                  options={availableWorkloads.map((w: any) => w.name)}
                  onChange={(next) => {
                    const wl = availableWorkloads.find((w: any) => w.name === next);
                    setSelectedWorkload(wl || null);
                  }}
                  placeholder='Select workload'
                  disabled={!selectedNamespace}
                />
              </Grid>
            </Grid>
            {isEditMode && activeWorkload && (
              <Chip tone='info' size='sm' sx={{ marginTop: 'var(--ds-space-3)' }}>
                Existing SLO config found - values pre-filled
              </Chip>
            )}
          </SectionCard>
        )}

        {/* SLO config form - shown after workload is available */}
        {activeWorkload && (
          <>
            {/* Availability Section */}
            <SectionCard title='Availability'>
              <Typography variant='body2' sx={{ color: ds.gray[700], marginBottom: 'var(--ds-space-3)' }}>
                Percentage of requests that should complete successfully
              </Typography>
              <Grid container alignItems='center' spacing={1}>
                <Grid item>
                  <Typography variant='body2' sx={{ color: ds.blue[500] }}>
                    Objective:
                  </Typography>
                </Grid>
                <Grid item>
                  <Box sx={{ width: ds.space.mul(0, 50) }} data-testid='slo-availability-objective'>
                    <Input
                      size='sm'
                      id='slo-availability-objective'
                      type='text'
                      inputMode='decimal'
                      suffix='%'
                      // handleInput expects event-shape; synthesize one.
                      onChange={(value) => handleInput({ target: { value } }, 'availability', 'goal')}
                      value={String(showConfiguredValue('availability', 'goal') ?? '')}
                    />
                  </Box>
                </Grid>
                <Grid item>
                  <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                    of requests should not fail
                  </Typography>
                </Grid>
              </Grid>
            </SectionCard>

            {/* Latency Section */}
            <SectionCard title='Latency'>
              <Typography variant='body2' sx={{ color: ds.gray[700], marginBottom: 'var(--ds-space-3)' }}>
                Percentage of requests that should respond within the threshold
              </Typography>
              <Grid container alignItems='center' spacing={1} sx={{ marginBottom: 'var(--ds-space-3)' }}>
                <Grid item>
                  <Typography variant='body2' sx={{ color: ds.blue[500] }}>
                    Objective:
                  </Typography>
                </Grid>
                <Grid item>
                  <Box sx={{ width: ds.space.mul(0, 50) }} data-testid='slo-latency-objective'>
                    <Input
                      size='sm'
                      id='slo-latency-objective'
                      type='text'
                      inputMode='decimal'
                      suffix='%'
                      onChange={(value) => handleInput({ target: { value } }, 'latency', 'goal')}
                      value={String(showConfiguredValue('latency', 'goal') ?? '')}
                    />
                  </Box>
                </Grid>
                <Grid item>
                  <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                    of requests should be served faster than
                  </Typography>
                </Grid>
              </Grid>
              <Grid container alignItems='center' spacing={1}>
                <Grid item>
                  <Typography variant='body2' sx={{ color: ds.blue[500] }}>
                    Threshold:
                  </Typography>
                </Grid>
                <Grid item data-testid='slo-latency-threshold-dropdown'>
                  <Box sx={{ width: ds.space.mul(0, 80) }}>
                    <Select
                      id='duration'
                      label='Duration (ms)'
                      value={showConfiguredValue('latency', 'threshold') ?? ''}
                      options={['5', '10', '25', '50', '100', '125', '500', '1000', '2500', '5000', '10000']}
                      onChange={(next) => {
                        handleInput({ target: { value: next } }, 'latency', 'threshold');
                      }}
                      placeholder='Select duration'
                    />
                  </Box>
                </Grid>
              </Grid>
            </SectionCard>

            {/* CPU Section: forecast-based thresholds */}
            <SectionCard title='CPU'>
              <Typography variant='body2' sx={{ color: ds.gray[700], marginBottom: 'var(--ds-space-3)' }}>
                Forecast hourly from the last 3 days of metrics, alerting before the threshold is reached. Leave a field empty to skip it; Remove
                deletes a saved one.
              </Typography>
              {CPU_SLO_TYPES.map((t) => (
                <Box key={t.name} sx={{ marginBottom: 'var(--ds-space-3)' }}>
                  <Grid container alignItems='center' spacing={1}>
                    <Grid item>
                      <Typography variant='body2' sx={{ color: ds.blue[500] }}>
                        {`${t.title}:`}
                      </Typography>
                    </Grid>
                    <Grid item>
                      <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                        {t.description}
                      </Typography>
                    </Grid>
                    <Grid item>
                      <Box sx={{ width: ds.space.mul(0, 50) }} data-testid={`slo-${t.name}-threshold`}>
                        <Input
                          size='sm'
                          id={`slo-${t.name}-threshold`}
                          type='text'
                          inputMode='decimal'
                          suffix='%'
                          onChange={(value) => setCpuThreshold(t.name, String(value ?? ''))}
                          value={cpuThresholdText(t.name)}
                        />
                      </Box>
                    </Grid>
                    <Grid item>
                      <Typography variant='body2' sx={{ color: ds.gray[700] }}>
                        {t.unit}
                      </Typography>
                    </Grid>
                    <Grid item>
                      <DsButton
                        id={`slo-${t.name}-check-now`}
                        tone='secondary'
                        size='sm'
                        onClick={() => handleCheckNow(t.name)}
                        disabled={loading || forecasts[t.name]?.loading}
                      >
                        Check now
                      </DsButton>
                    </Grid>
                    {/* Deleting a saved SLO is account-level, as in the SLO list; namespace admins can still edit it. */}
                    {(isSavedCpu(t.name) ? hasWriteAccess(accountId) : cpuThresholdText(t.name) !== '') && (
                      <Grid item>
                        <DsButton id={`slo-${t.name}-remove`} tone='ghost' size='sm' onClick={() => handleRemoveCpu(t.name)} disabled={loading}>
                          Remove
                        </DsButton>
                      </Grid>
                    )}
                  </Grid>
                  {renderForecast(t.name)}
                </Box>
              ))}
            </SectionCard>
          </>
        )}

        {/* Empty state when no workload selected */}
        {showWorkloadSelector && !activeWorkload && (
          <Box
            sx={{
              textAlign: 'center',
              padding: 'var(--ds-space-5)',
              color: ds.gray[700],
            }}
          >
            <Typography variant='body2'>Select a namespace and workload above to configure SLO</Typography>
          </Box>
        )}
      </Box>
    );
  };

  const dialogTitle = `${isEditMode ? 'Update' : 'Create'} SLO Config${activeWorkload ? ' for ' + activeWorkload.name : ''}`;

  return (
    <Modal
      open={open}
      handleClose={onClose}
      title={dialogTitle}
      width='md'
      loader={loading}
      actionButtons={
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-3)', p: 'var(--ds-space-3) var(--ds-space-5)' }}>
          <DsButton id='slo-cancel-btn' tone='secondary' size='md' onClick={onClose} disabled={loading}>
            Cancel
          </DsButton>
          <DsButton id='slo-submit-btn' tone='primary' size='md' onClick={handleSubmit} disabled={!activeWorkload || loading}>
            {isEditMode ? 'Update' : 'Create'}
          </DsButton>
        </Box>
      }
    >
      <Box sx={{ padding: '0 var(--ds-space-5)' }}>{formContent()}</Box>
    </Modal>
  );
};

export default SLOConfigDialog;
