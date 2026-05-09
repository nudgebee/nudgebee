import React, { useEffect, useRef, useState } from 'react';
import { Typography, TextField, InputAdornment, Grid, Box, Chip } from '@mui/material';
import NDialog from '@components1/common/modal/NDialog';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import apiKubernetes1 from '@api1/kubernetes1';
import k8sApi from '@api1/kubernetes';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from '@utils/colors';

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

const SectionCard = ({ title, children }: { title: string; children: React.ReactNode }) => (
  <Box
    sx={{
      border: `1px solid ${colors.border.primaryLightest}`,
      borderRadius: '8px',
      padding: '16px',
      marginBottom: '16px',
    }}
  >
    <Typography sx={{ fontWeight: 600, fontSize: '14px', marginBottom: '12px', color: colors.text.primary }}>{title}</Typography>
    {children}
  </Box>
);

const SLOConfigDialog: React.FC<SLOConfigDialogProps> = ({ open, onClose, accountId, workload, initialConfig, isEdit = false, onSuccess }) => {
  const [sloConfig, setSloConfig] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [isEditMode, setIsEditMode] = useState(false);

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
    let inputValue = event.target.value;
    if (!isNaN(inputValue) && inputValue >= 0 && type === 'goal') {
      inputValue = parseInt(inputValue, 10);
      inputValue = Math.min(Math.max(1, inputValue), 100);
    } else {
      inputValue = parseInt(inputValue, 10);
    }
    const existingConfigIndex = sloConfig.findIndex((config: any) => config.name === inspection);
    if (existingConfigIndex === -1) {
      setSloConfig((prev) => [
        ...prev,
        {
          enabled: true,
          name: inspection,
          goal: type === 'goal' ? parseFloat(inputValue) / 100 : null,
          threshold: type === 'threshold' ? parseFloat(inputValue) : null,
        },
      ]);
    } else {
      const updated = [...sloConfig];
      const updatedConfig = { ...updated[existingConfigIndex] };
      if (type === 'goal') {
        updatedConfig.goal = parseFloat(inputValue) / 100;
      } else if (type === 'threshold') {
        updatedConfig.threshold = parseFloat(inputValue);
      }
      updated[existingConfigIndex] = updatedConfig;
      setSloConfig(updated);
    }
  };

  const showConfiguredValue = (inspection: string, type: string) => {
    if (sloConfig && sloConfig.length > 0) {
      const filterInspection = sloConfig.filter((n: any) => n.name === inspection);
      if (filterInspection && filterInspection.length === 1) {
        if (type === 'goal') {
          if (!isNaN(filterInspection[0].goal)) {
            return (filterInspection[0].goal * 100).toFixed();
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
    if (sloConfig && sloConfig.length > 0) {
      const availabilityObj = sloConfig.filter((f: any) => f.name === 'availability');
      if (availabilityObj && availabilityObj.length === 1) {
        if (!availabilityObj[0].goal) {
          snackbar.error('Configure Availability Objective');
          return;
        }
      } else {
        snackbar.error('Configure Availability');
        return;
      }
      const latencyObj = sloConfig.filter((f: any) => f.name === 'latency');
      if (latencyObj && latencyObj.length === 1) {
        if (!latencyObj[0].goal) {
          snackbar.error('Configure Latency Objective');
          return;
        }
        if (!latencyObj[0].threshold) {
          snackbar.error('Configure Latency Threshold');
          return;
        }
      } else {
        snackbar.error('Configure Latency');
        return;
      }
    } else {
      snackbar.error('Configure Availability & Latency');
      return;
    }

    const data = {
      cloud_account_id: accountId,
      config: sloConfig,
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

  const formContent = () => {
    return (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: '4px', paddingTop: '8px' }}>
        {/* Workload selector (only when opened from SLO page) */}
        {showWorkloadSelector && (
          <SectionCard title='Select Workload'>
            <Grid container spacing={2}>
              <Grid item xs={6} data-testid='slo-namespace-dropdown'>
                <FilterDropdownButton
                  label='Namespace'
                  value={selectedNamespace}
                  options={availableNamespaces}
                  onSelect={(_e: any, value: any) => {
                    setSelectedNamespace(value || '');
                    setSelectedWorkload(null);
                  }}
                />
              </Grid>
              <Grid item xs={6} data-testid='slo-workload-dropdown'>
                <FilterDropdownButton
                  label='Workload'
                  value={selectedWorkload?.name || ''}
                  options={availableWorkloads.map((w: any) => w.name)}
                  onSelect={(_e: any, value: any) => {
                    const wl = availableWorkloads.find((w: any) => w.name === value);
                    setSelectedWorkload(wl || null);
                  }}
                />
              </Grid>
            </Grid>
            {isEditMode && activeWorkload && (
              <Chip
                label='Existing SLO config found - values pre-filled'
                size='small'
                sx={{
                  marginTop: '12px',
                  backgroundColor: colors.background.primaryLightest,
                  color: colors.text.primary,
                  fontSize: '12px',
                }}
              />
            )}
          </SectionCard>
        )}

        {/* SLO config form - shown after workload is available */}
        {activeWorkload && (
          <>
            {/* Availability Section */}
            <SectionCard title='Availability'>
              <Typography variant='body2' sx={{ color: colors.text.secondary, marginBottom: '12px' }}>
                Percentage of requests that should complete successfully
              </Typography>
              <Grid container alignItems='center' spacing={1}>
                <Grid item>
                  <Typography variant='body2' sx={{ color: colors.text.primary }}>
                    Objective:
                  </Typography>
                </Grid>
                <Grid item>
                  <TextField
                    sx={{ width: '100px' }}
                    size='small'
                    id='slo-availability-objective'
                    data-testid='slo-availability-objective'
                    type='number'
                    onChange={(e) => handleInput(e, 'availability', 'goal')}
                    InputProps={{
                      endAdornment: <InputAdornment position='end'>%</InputAdornment>,
                    }}
                    value={showConfiguredValue('availability', 'goal') ?? ''}
                  />
                </Grid>
                <Grid item>
                  <Typography variant='body2' sx={{ color: colors.text.secondary }}>
                    of requests should not fail
                  </Typography>
                </Grid>
              </Grid>
            </SectionCard>

            {/* Latency Section */}
            <SectionCard title='Latency'>
              <Typography variant='body2' sx={{ color: colors.text.secondary, marginBottom: '12px' }}>
                Percentage of requests that should respond within the threshold
              </Typography>
              <Grid container alignItems='center' spacing={1} sx={{ marginBottom: '12px' }}>
                <Grid item>
                  <Typography variant='body2' sx={{ color: colors.text.primary }}>
                    Objective:
                  </Typography>
                </Grid>
                <Grid item>
                  <TextField
                    sx={{ width: '100px' }}
                    size='small'
                    id='slo-latency-objective'
                    data-testid='slo-latency-objective'
                    type='number'
                    onChange={(e) => handleInput(e, 'latency', 'goal')}
                    InputProps={{
                      endAdornment: <InputAdornment position='end'>%</InputAdornment>,
                    }}
                    value={showConfiguredValue('latency', 'goal') ?? ''}
                  />
                </Grid>
                <Grid item>
                  <Typography variant='body2' sx={{ color: colors.text.secondary }}>
                    of requests should be served faster than
                  </Typography>
                </Grid>
              </Grid>
              <Grid container alignItems='center' spacing={1}>
                <Grid item>
                  <Typography variant='body2' sx={{ color: colors.text.primary }}>
                    Threshold:
                  </Typography>
                </Grid>
                <Grid item data-testid='slo-latency-threshold-dropdown'>
                  <FilterDropdownButton
                    id='duration'
                    label='Duration (ms)'
                    value={showConfiguredValue('latency', 'threshold')}
                    options={['5', '10', '25', '50', '100', '125', '500', '1000', '2500', '5000', '10000']}
                    onSelect={(_e: any, value: any) => {
                      handleInput({ target: { value } }, 'latency', 'threshold');
                    }}
                  />
                </Grid>
              </Grid>
            </SectionCard>
          </>
        )}

        {/* Empty state when no workload selected */}
        {showWorkloadSelector && !activeWorkload && (
          <Box
            sx={{
              textAlign: 'center',
              padding: '24px',
              color: colors.text.secondary,
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
    <NDialog
      buttonText={isEditMode ? 'Update' : 'Create'}
      handleClose={onClose}
      dialogTitle={dialogTitle}
      handleSubmit={handleSubmit}
      open={open}
      additionalComponent={formContent()}
      loading={loading}
      disabled={!activeWorkload}
      dialogContent={undefined}
    />
  );
};

export default SLOConfigDialog;
