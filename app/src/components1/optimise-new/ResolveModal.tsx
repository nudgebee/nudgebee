import { useState, useCallback, useEffect, useMemo } from 'react';
import { Box, Typography, CircularProgress, Grid } from '@mui/material';
import { Modal } from '@components1/common/modal';
import AutoPilotHeaderCard from '@components1/autopilot/card/AutoPilotHeaderCard';
import AutoOptimizeForm from '@components1/autopilot/form/AutoOptimizeVerticalRightSizingForm';
import { formatMemory } from '@lib/formatter';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';
import recommendationApi from '@api1/recommendation';
import apiIntegrations from '@api1/integrations';
import k8sApi from '@api1/kubernetes';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import CustomDropdown from '@components1/common/CustomDropdown';
import CustomButton from '@components1/common/NewCustomButton';
import SafeIcon from '@components1/common/SafeIcon';
import { BetaIcon } from '@assets';

const betaBadge = <SafeIcon src={BetaIcon} alt='Beta Icon' width={25} height={20} style={{ marginLeft: '1px' }} />;

interface ResolveModalProps {
  open: boolean;
  onClose: () => void;
  recommendation: any;
  clusterName?: string;
  onSuccess?: () => void;
}

const ResolveModal = ({ open, onClose, recommendation, clusterName, onSuccess }: ResolveModalProps) => {
  const [updatedData, setUpdatedData] = useState<Record<string, any>>({});
  const [allocatedData, setAllocatedData] = useState<Record<string, any>>({});
  const [additionalCpuInfo, setAdditionalCpuInfo] = useState<Record<string, any>>({});
  const [additionalMemInfo, setAdditionalMemInfo] = useState<Record<string, any>>({});
  const [selectedButtons, setSelectedButtons] = useState<Record<string, number>>({
    algo: 0,
    buffer: 0,
    memory: 0,
    memBuffer: 0,
    cpuLimit: 0,
    memLimit: 0,
  });
  const [algo, setAlgo] = useState('NBALGO');
  const [deploying, setDeploying] = useState(false);
  const [initialized, setInitialized] = useState(false);

  // PR creation state
  const [showPRModal, setShowPRModal] = useState(false);
  const [prLoading, setPRLoading] = useState(false);
  const [allGitIntegrations, setAllGitIntegrations] = useState<any[]>([]);
  const [selectedGitIntegration, setSelectedGitIntegration] = useState('');
  const [selectedWorkloadAnnotations, setSelectedWorkloadAnnotations] = useState<Record<string, string>>({});
  const [isGitReposLoading, setIsGitReposLoading] = useState(false);

  // Ticket state
  const [isTicketFormOpen, setIsTicketFormOpen] = useState(false);

  // Build data structures from recommendation JSONB when modal opens
  const initializeData = useCallback(() => {
    if (initialized || !recommendation?.recommendation) return;

    const recommendations =
      typeof recommendation.recommendation === 'string' ? JSON.parse(recommendation.recommendation) : recommendation.recommendation;

    if (!recommendations || typeof recommendations !== 'object') return;

    const newCpuInfo: Record<string, any> = {};
    const newMemInfo: Record<string, any> = {};
    const allocatedObject: Record<string, any> = {};
    const recommendedObject: Record<string, any> = {};

    for (const c of Object.keys(recommendations)) {
      const containerObject = recommendations[c];
      if (!Array.isArray(containerObject)) continue;

      const cpuObject = containerObject.find((g: any) => g.resource === 'cpu') || {};
      const memoryObject = containerObject.find((g: any) => g.resource === 'memory') || {};

      newCpuInfo[c] = {
        p99: cpuObject?.add_info?.cpu_percentile_99 || null,
        p97: cpuObject?.add_info?.cpu_percentile_97 || null,
        p95: cpuObject?.add_info?.cpu_percentile_95 || null,
        nbalgo: cpuObject?.recommended?.request || null,
      };
      newMemInfo[c] = {
        limit: memoryObject?.add_info?.actual_recommended_limit || null,
        req: memoryObject?.add_info?.actual_recommended_request || null,
        nbalgoReq: memoryObject?.recommended?.request || null,
        nbalgoLimit: memoryObject?.recommended?.limit || null,
      };
      allocatedObject[c] = {
        cpu: {
          request: cpuObject?.allocated?.request || null,
          limit: cpuObject?.allocated?.limit || null,
        },
        memory: {
          request: formatMemory(memoryObject?.allocated?.request, 'bytes', 'mb', false) || undefined,
          limit: formatMemory(memoryObject?.allocated?.limit, 'bytes', 'mb', false) || null,
        },
      };
      recommendedObject[c] = {
        cpu: {
          request: cpuObject?.recommended?.request || undefined,
          limit: cpuObject?.recommended?.limit || undefined,
        },
        memory: {
          request: formatMemory(memoryObject?.recommended?.request, 'bytes', 'mb', false) || undefined,
          limit: formatMemory(memoryObject?.recommended?.limit, 'bytes', 'mb', false) || undefined,
        },
      };
    }

    setAdditionalCpuInfo(newCpuInfo);
    setAdditionalMemInfo(newMemInfo);
    setAllocatedData(allocatedObject);
    setUpdatedData(recommendedObject);
    setInitialized(true);
  }, [recommendation, initialized]);

  // Initialize when modal opens
  useEffect(() => {
    if (open && !initialized) {
      initializeData();
    }
  }, [open, initialized, initializeData]);

  // Fetch workload annotations for PR creation when modal opens
  useEffect(() => {
    if (!open || !recommendation) return;
    getWorkloadAnnotations();
  }, [open, recommendation?.id]);

  const getWorkloadAnnotations = async () => {
    try {
      const data = recommendation;
      const accountId = data?.account_id;
      if (!accountId) return;

      const namespaceName = data?.cloud_resourse?.meta?.config?.namespace || data?.cloud_resourse?.meta?.namespace;
      const workloadName = data?.cloud_resourse?.type === 'Pod' ? data?.cloud_resourse?.meta?.controller : data?.cloud_resourse?.name;
      const workloadType = data?.cloud_resourse?.type === 'Pod' ? data?.cloud_resourse?.meta?.controllerKind : data?.cloud_resourse?.type;

      const res = await k8sApi.getK8sWorkload(1, 0, {
        accountId,
        namespaceName,
        workloadName,
        workloadType,
        exactNameMatch: true,
      });

      const workloads = res?.data?.k8s_workloads || [];
      if (workloads.length === 1) {
        const workload = workloads[0];
        const annotations = workload.meta?.config?.annotations || {};
        const filteredKeys = Object.keys(annotations).filter((key) => key.startsWith('ci.nudgebee.com') || key.startsWith('argocd.argoproj.io'));
        if (filteredKeys.length > 0) {
          const filteredObject: Record<string, string> = {};
          filteredKeys.forEach((key) => {
            filteredObject[key] = annotations[key];
          });
          setSelectedWorkloadAnnotations(filteredObject);
          return;
        }

        // Check cloud_resource_attributes for manual CI configuration
        if (workload.cloud_resource_id) {
          const attributes = await k8sApi.getResourceAttributes(workload.cloud_resource_id);
          const manualConfig: Record<string, string> = {};
          attributes.forEach((attr: any) => {
            if (attr.name.startsWith('ci.nudgebee.com')) {
              manualConfig[attr.name] = attr.value;
            }
          });
          if (Object.keys(manualConfig).length > 0) {
            setSelectedWorkloadAnnotations(manualConfig);
            return;
          }
        }
        setSelectedWorkloadAnnotations({});
      }
    } catch (error) {
      console.error('Error fetching workload annotations:', error);
      setSelectedWorkloadAnnotations({});
    }
  };

  // Helper to detect git provider
  const detectGitProvider = (repoUrl: string | undefined) => {
    if (!repoUrl) return null;
    const url = repoUrl.toLowerCase();
    if (url.includes('github.com')) return 'github';
    if (url.includes('gitlab')) return 'gitlab';
    return null;
  };

  const filteredGitIntegrations = useMemo(() => {
    const repoUrl = selectedWorkloadAnnotations['ci.nudgebee.com/git.repo'] || selectedWorkloadAnnotations['workloads.nudgebee.com/git.repo'];
    const detectedProvider = detectGitProvider(repoUrl);
    if (!detectedProvider) return allGitIntegrations;
    return allGitIntegrations.filter((i: any) => i.type === detectedProvider);
  }, [selectedWorkloadAnnotations, allGitIntegrations]);

  // Auto-select first filtered integration
  useEffect(() => {
    if (filteredGitIntegrations.length > 0 && !selectedGitIntegration) {
      setSelectedGitIntegration(filteredGitIntegrations[0].key);
    }
  }, [filteredGitIntegrations, selectedGitIntegration]);

  // Reset when modal closes
  const handleClose = () => {
    setInitialized(false);
    setUpdatedData({});
    setAllocatedData({});
    setAdditionalCpuInfo({});
    setAdditionalMemInfo({});
    setSelectedButtons({ algo: 0, buffer: 0, memory: 0, memBuffer: 0, cpuLimit: 0, memLimit: 0 });
    setAlgo('NBALGO');
    setDeploying(false);
    setShowPRModal(false);
    setPRLoading(false);
    setAllGitIntegrations([]);
    setSelectedGitIntegration('');
    setSelectedWorkloadAnnotations({});
    onClose();
  };

  // ── Helper: get data with Mi suffix for memory ──
  const getDataWithMemorySuffix = () => {
    const dataToSubmit = JSON.parse(JSON.stringify(updatedData));
    for (const d in dataToSubmit) {
      if (dataToSubmit[d].memory) {
        for (const key in dataToSubmit[d].memory) {
          const value = dataToSubmit[d].memory[key];
          if (value) {
            // Strip locale commas (e.g. "1,024" → "1024") before appending Mi suffix;
            // formatMemory uses toLocaleString which adds commas for values >= 1000
            const cleaned = String(value).replace(/,/g, '');
            dataToSubmit[d].memory[key] = cleaned + 'Mi';
          }
        }
      }
    }
    return dataToSubmit;
  };

  // ── Button handlers (same logic as KubernetesRightSizing.jsx) ──

  const updateDataBasedOnButtonValueForCpu = (value: any, containerName: string) => {
    const selectedKey = algo?.toLowerCase();

    const getCpuLimit = (newRequest: number) => {
      switch (selectedButtons.cpuLimit) {
        case 1:
          return allocatedData[containerName]?.cpu?.limit || null;
        case 2:
          return (newRequest * 1.05).toFixed(2);
        case 3:
          return (newRequest * 1.15).toFixed(2);
        default:
          return null;
      }
    };

    const updateCpu = (newRequest: number) => {
      setUpdatedData((prev: any) => ({
        ...prev,
        [containerName]: {
          ...prev[containerName],
          cpu: { ...prev[containerName]?.cpu, request: newRequest.toFixed(4), limit: getCpuLimit(newRequest) },
        },
      }));
    };

    switch (value) {
      case 'NBALGO':
        updateCpu(parseFloat(additionalCpuInfo[containerName]?.nbalgo) || 0);
        break;
      case 'P99':
        updateCpu(parseFloat(additionalCpuInfo[containerName]?.p99) || 0);
        break;
      case 'P97':
        updateCpu(parseFloat(additionalCpuInfo[containerName]?.p97) || 0);
        break;
      case 'P95':
        updateCpu(parseFloat(additionalCpuInfo[containerName]?.p95) || 0);
        break;
      default: {
        if (typeof value === 'number' && value > 0) {
          const base = parseFloat(additionalCpuInfo[containerName]?.[selectedKey]) || 0;
          updateCpu(base * (1 + value / 100));
        }
        break;
      }
    }
  };

  const updateDataBasedOnButtonValueForMemory = (value: any, containerName: string) => {
    const getMemoryLimit = (newRequestBytes: number) => {
      switch (selectedButtons.memLimit) {
        case 1:
          return allocatedData[containerName]?.memory?.limit || null;
        case 2:
          return formatMemory(newRequestBytes * 1.05, 'bytes', 'mb', false);
        case 3:
          return formatMemory(newRequestBytes * 1.15, 'bytes', 'mb', false);
        default:
          return formatMemory(newRequestBytes, 'bytes', 'mb', false);
      }
    };

    const nbalgoReq = additionalMemInfo[containerName]?.nbalgoReq || 0;
    const multiplier = typeof value === 'number' && value > 0 ? 1 + value / 100 : 1;
    const newRequestBytes = nbalgoReq * multiplier;

    setUpdatedData((prev: any) => ({
      ...prev,
      [containerName]: {
        ...prev[containerName],
        memory: {
          ...prev[containerName]?.memory,
          request: formatMemory(newRequestBytes, 'bytes', 'mb', false),
          limit: getMemoryLimit(newRequestBytes),
        },
      },
    }));
  };

  const handleSelectedAlgo = (buttonId: number, buttonValue: string, containerName: string) => {
    setSelectedButtons((prev) => ({ ...prev, algo: buttonId }));
    setAlgo(buttonValue);
    updateDataBasedOnButtonValueForCpu(buttonValue, containerName);
  };

  const handleSelectedBuffer = (buttonId: number, buttonValue: any, containerName: string) => {
    setSelectedButtons((prev) => ({ ...prev, buffer: buttonId }));
    updateDataBasedOnButtonValueForCpu(buttonValue, containerName);
  };

  const handleSelectedMemoryBuffer = (buttonId: number, buttonValue: any, containerName: string) => {
    setSelectedButtons((prev) => ({ ...prev, memBuffer: buttonId }));
    updateDataBasedOnButtonValueForMemory(buttonValue, containerName);
  };

  const handleSelectedMemoryAlgo = (buttonId: number, buttonValue: any, containerName: string) => {
    setSelectedButtons((prev) => ({ ...prev, memory: buttonId }));
    updateDataBasedOnButtonValueForMemory(buttonValue, containerName);
  };

  const handleSelectedCpuLimit = (buttonId: number, buttonValue: string, containerName: string) => {
    setSelectedButtons((prev) => ({ ...prev, cpuLimit: buttonId }));
    const requestStr = String(updatedData[containerName]?.cpu?.request || '0').replace(/,/g, '');
    const currentRequest = parseFloat(requestStr) || 0;
    let newLimit: string | null = null;
    if (buttonValue === 'KEEP_PREVIOUS') {
      newLimit = allocatedData[containerName]?.cpu?.limit || null;
    } else if (buttonValue === 'PLUS_5') {
      newLimit = (currentRequest * 1.05).toFixed(2);
    } else if (buttonValue === 'PLUS_15') {
      newLimit = (currentRequest * 1.15).toFixed(2);
    }
    setUpdatedData((prev: any) => ({
      ...prev,
      [containerName]: { ...prev[containerName], cpu: { ...prev[containerName]?.cpu, limit: newLimit } },
    }));
  };

  const handleSelectedMemLimit = (buttonId: number, buttonValue: string, containerName: string) => {
    setSelectedButtons((prev) => ({ ...prev, memLimit: buttonId }));
    const requestStr = String(updatedData[containerName]?.memory?.request || '0').replace(/,/g, '');
    const currentRequest = parseFloat(requestStr) || 0;
    let newLimit: number | string | null = null;
    if (buttonValue === 'KEEP_PREVIOUS') {
      newLimit = allocatedData[containerName]?.memory?.limit || null;
    } else if (buttonValue === 'PLUS_5') {
      newLimit = Math.round(currentRequest * 1.05);
    } else if (buttonValue === 'PLUS_15') {
      newLimit = Math.round(currentRequest * 1.15);
    } else {
      newLimit = Math.round(currentRequest);
    }
    setUpdatedData((prev: any) => ({
      ...prev,
      [containerName]: { ...prev[containerName], memory: { ...prev[containerName]?.memory, limit: newLimit } },
    }));
  };

  const handleInputChange = (value: string, type: string, type1: string, containerName: string) => {
    setUpdatedData((prev: any) => ({
      ...prev,
      [containerName]: {
        ...prev[containerName],
        [type === 'cpu' ? 'cpu' : 'memory']: {
          ...prev[containerName]?.[type === 'cpu' ? 'cpu' : 'memory'],
          [type1]: value,
        },
      },
    }));
  };

  const shouldShowKeepPreviousCpuLimit = (containerName: string) => {
    const allocatedLimit = allocatedData[containerName]?.cpu?.limit;
    const recommendedRequest = updatedData[containerName]?.cpu?.request;
    return (
      allocatedLimit != null &&
      parseFloat(allocatedLimit) > 0 &&
      recommendedRequest != null &&
      parseFloat(recommendedRequest) < parseFloat(allocatedLimit)
    );
  };

  const shouldShowKeepPreviousMemLimit = (containerName: string) => {
    const allocatedLimit = allocatedData[containerName]?.memory?.limit;
    const recommendedRequestStr = String(updatedData[containerName]?.memory?.request || '0').replace(/,/g, '');
    const recommendedRequest = parseFloat(recommendedRequestStr) || 0;
    const allocatedLimitStr = String(allocatedLimit || '0').replace(/,/g, '');
    const allocatedLimitNum = parseFloat(allocatedLimitStr) || 0;
    return allocatedLimitNum > 0 && allocatedLimitNum >= recommendedRequest;
  };

  // ── Deploy Fix ──

  const submitRecommendation = async () => {
    setDeploying(true);
    try {
      const dataToSubmit = getDataWithMemorySuffix();
      const accountId = recommendation.account_id || '';
      const recommendationId = recommendation.id;
      const result = await recommendationApi.applyRecommendation(accountId, recommendationId, dataToSubmit);

      if (result?.errors) {
        snackbar.error('An error occurred while deploying');
      } else {
        snackbar.success('Deployed fix successfully');
        onSuccess?.();
        handleClose();
      }
    } catch (err: any) {
      snackbar.error(err?.message || 'Failed to deploy fix');
    } finally {
      setDeploying(false);
    }
  };

  // ── Create PR ──

  const openCreatePRModal = () => {
    setShowPRModal(true);
    listGitConfigurations();
  };

  const listGitConfigurations = () => {
    setIsGitReposLoading(true);
    Promise.all([
      apiIntegrations.listTicketConfigurationsByTool({ status: 'enabled', tool: 'github' }),
      apiIntegrations.listTicketConfigurationsByTool({ status: 'enabled', tool: 'gitlab' }),
    ])
      .then(([githubRes, gitlabRes]: any[]) => {
        const githubData =
          githubRes?.data?.length > 0
            ? githubRes.data.map((g: any) => ({ name: g.name, type: 'github', key: `github:${g.name}`, label: `GitHub: ${g.name}` }))
            : [];
        const gitlabData =
          gitlabRes?.data?.length > 0
            ? gitlabRes.data.map((g: any) => ({ name: g.name, type: 'gitlab', key: `gitlab:${g.name}`, label: `GitLab: ${g.name}` }))
            : [];
        setAllGitIntegrations([...githubData, ...gitlabData]);
      })
      .catch((error: any) => {
        console.error('Error fetching Git configurations:', error);
        setAllGitIntegrations([]);
      })
      .finally(() => {
        setIsGitReposLoading(false);
      });
  };

  const handleCreatePR = () => {
    if (!selectedGitIntegration) return;
    const [integrationType, ...nameParts] = selectedGitIntegration.split(':');
    const integrationName = nameParts.join(':');
    setPRLoading(true);
    const data = getDataWithMemorySuffix();
    const accountId = recommendation.account_id || '';
    recommendationApi
      .applyRecommendation(accountId, recommendation.id, data, integrationType, { name: integrationName })
      .then((res: any) => {
        if (res?.errors?.length > 0) {
          snackbar.error('Failed to create Pull Request');
        } else if (res?.data?.length > 0) {
          snackbar.success(
            'PR creation initiated! The code agent is creating the PR in the background. Check the Resolution tab to track progress.',
            6000
          );
        }
        setShowPRModal(false);
        setPRLoading(false);
      })
      .catch((error: any) => {
        snackbar.error('Failed to raise pull request');
        console.error(error);
        setShowPRModal(false);
        setPRLoading(false);
      });
  };

  const closePRModal = () => {
    setShowPRModal(false);
    setSelectedGitIntegration('');
    setAllGitIntegrations([]);
  };

  // ── Create Ticket ──

  const openTicketForm = () => {
    setIsTicketFormOpen(true);
  };

  const buildTicketDescription = (): string => {
    const _ruleName = recommendation?.rule_name || '';
    const category = recommendation?.category || '';
    const resourceName = recommendation?.resource_name || recommendation?.cloud_resourse?.name || '';
    const namespace = recommendation?.resource_k8s_namespace || recommendation?.cloud_resourse?.meta?.namespace || '';
    let description = `**Recommendation**: Pod Right Sizing\n`;
    description += `**Category**: ${category}\n`;
    description += `**Resource**: ${resourceName}\n`;
    if (namespace) description += `**Namespace**: ${namespace}\n`;
    if (recommendation?.estimated_savings) {
      description += `**Estimated Savings**: $${recommendation.estimated_savings.toFixed(2)}/mo\n`;
    }
    for (const [containerName, entries] of Object.entries(updatedData)) {
      description += `\n**Container**: ${containerName}\n`;
      description += `  CPU: ${allocatedData[containerName]?.cpu?.request || 'N/A'} → ${entries?.cpu?.request || 'N/A'}\n`;
      description += `  Memory: ${allocatedData[containerName]?.memory?.request || 'N/A'} → ${entries?.memory?.request || 'N/A'} MB\n`;
    }
    return description;
  };

  // ── Auto-optimize links ──
  const handleScheduleAutoOptimize = () => {
    const accountId = recommendation.account_id || '';
    window.open(`/auto-pilot/task/new?accountId=${accountId}&category=vertical_rightsize`, '_blank');
  };

  const handleContinuousAutoOptimize = () => {
    const accountId = recommendation.account_id || '';
    const continuousId = recommendation.continuousAutoPilotId;
    if (continuousId) {
      window.open(`/auto-pilot/task/${continuousId}?accountId=${accountId}`, '_blank');
    } else {
      window.open(`/auto-pilot/task/new?accountId=${accountId}&category=continuous_rightsize`, '_blank');
    }
  };

  // ── Build autoPilotData shape expected by AutoPilotHeaderCard ──
  const workloadName =
    recommendation?.cloud_resourse?.type === 'Pod' ? recommendation?.cloud_resourse?.meta?.controller : recommendation?.cloud_resourse?.name;

  const autoPilotData = {
    id: recommendation?.id,
    accountId: recommendation?.account_id,
    resourceId: recommendation?.cloud_resource_id,
    data: recommendation,
    saving: recommendation?.estimated_savings,
    clusterName,
    resource_filter: [
      {
        namespace: recommendation?.cloud_resourse?.meta?.config?.namespace || recommendation?.cloud_resourse?.meta?.namespace,
        name: workloadName,
        type:
          recommendation?.cloud_resourse?.type === 'Pod'
            ? recommendation?.cloud_resourse?.meta?.controllerKind
            : recommendation?.cloud_resourse?.type,
      },
    ],
    recommendationId: recommendation?.id,
  };

  // ── Action buttons (footer) ──
  const ticketExists = !!recommendation?.ticket;
  const actionButtons = (
    <Box
      sx={{
        display: 'flex',
        height: '56px',
        justifyContent: 'space-between',
        alignItems: 'center',
        gap: '10px',
        flexShrink: 0,
        paddingX: '10px',
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center' }}>
        <CustomButton
          text='Cancel'
          variant='secondary'
          size='Medium'
          onClick={handleClose}
          disabled={deploying}
          sx={{ width: '100px', textTransform: 'none' }}
          data-testid='resolve-modal-cancel'
        />
      </Box>
      <Box sx={{ display: 'flex', gap: '6px', alignItems: 'center' }}>
        <CustomButton
          text='Create Ticket'
          variant='secondary'
          size='Medium'
          onClick={openTicketForm}
          disabled={ticketExists}
          sx={{ textTransform: 'none', '& img, & svg': { filter: 'none !important' } }}
          data-testid='resolve-modal-ticket'
        />
        <CustomButton
          text='Create Pull Request'
          variant='secondary'
          size='Medium'
          onClick={openCreatePRModal}
          endIcon={betaBadge}
          sx={{ textTransform: 'none', '& img, & svg': { filter: 'none !important' } }}
          data-testid='resolve-modal-pr'
        />
        <CustomButton
          text='Continuous Auto Optimize'
          variant='secondary'
          size='Medium'
          onClick={handleContinuousAutoOptimize}
          endIcon={betaBadge}
          sx={{ textTransform: 'none', '& img, & svg': { filter: 'none !important' } }}
          data-testid='resolve-modal-continuous'
        />
        <CustomButton
          text='Schedule Auto Optimize'
          variant='secondary'
          size='Medium'
          onClick={handleScheduleAutoOptimize}
          sx={{ textTransform: 'none' }}
          data-testid='resolve-modal-schedule'
        />
        <CustomButton
          text={deploying ? 'Deploying...' : 'Deploy Fix'}
          variant='secondary'
          size='Medium'
          onClick={submitRecommendation}
          disabled={deploying}
          sx={{ textTransform: 'none' }}
          data-testid='resolve-modal-deploy'
        />
      </Box>
    </Box>
  );

  const ticketData = {
    subject: `RightSizing - Pod Right Sizing: ${recommendation?.resource_name || workloadName || ''}`,
    description: buildTicketDescription(),
    accountId: recommendation?.account_id || '',
  };

  return (
    <>
      <Modal
        width='lg'
        open={open}
        handleClose={handleClose}
        title='Resolve this issue'
        loader={deploying}
        actionButtons={actionButtons}
        sx={{
          '& .MuiPaper-root': {
            maxWidth: '1010px',
            '& .MuiDialogContent-root': {
              padding: '16px 40px',
            },
          },
        }}
      >
        <Box sx={{ pb: '30px' }}>
          <AutoPilotHeaderCard header='' data={autoPilotData} />
          {Object.keys(updatedData).length > 0
            ? Object.keys(updatedData).map((containerName) => (
                <Box key={containerName} sx={{ display: 'flex', gap: '16px', marginTop: '16px' }}>
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: '24px', width: '100%' }}>
                    <Typography>Container Name- {containerName}</Typography>
                    <AutoOptimizeForm
                      handleSelectedAlgo={(buttonId: number, buttonValue: string) => handleSelectedAlgo(buttonId, buttonValue, containerName)}
                      handleSelectedBuffer={(buttonId: number, buttonValue: any) => handleSelectedBuffer(buttonId, buttonValue, containerName)}
                      handleSelectedMemoryBuffer={(buttonId: number, buttonValue: any) =>
                        handleSelectedMemoryBuffer(buttonId, buttonValue, containerName)
                      }
                      handleSelectedMemoryAlgo={(buttonId: number, buttonValue: any) =>
                        handleSelectedMemoryAlgo(buttonId, buttonValue, containerName)
                      }
                      handleSelectedCpuLimit={(buttonId: number, buttonValue: string) => handleSelectedCpuLimit(buttonId, buttonValue, containerName)}
                      handleSelectedMemLimit={(buttonId: number, buttonValue: string) => handleSelectedMemLimit(buttonId, buttonValue, containerName)}
                      data={updatedData[containerName]}
                      currentData={allocatedData[containerName]}
                      activeButton={selectedButtons}
                      additionalInfoCPUAndMem={{
                        cpuInfo: additionalCpuInfo[containerName],
                        memInfo: additionalMemInfo[containerName],
                      }}
                      handleInputChange={handleInputChange}
                      handleUpdateData={() => {}}
                      containerName={containerName}
                      showKeepPreviousCpuLimit={shouldShowKeepPreviousCpuLimit(containerName)}
                      showKeepPreviousMemLimit={shouldShowKeepPreviousMemLimit(containerName)}
                    />
                  </Box>
                </Box>
              ))
            : null}
        </Box>
      </Modal>

      {/* ── Create PR Modal ── */}
      <Modal width='md' open={showPRModal} handleClose={closePRModal} title='Create Pull Request' loader={prLoading}>
        {prLoading && (
          <Box sx={{ display: 'flex', justifyContent: 'center', py: '20px' }}>
            <CircularProgress size={24} />
          </Box>
        )}
        {isGitReposLoading || filteredGitIntegrations.length > 0 ? (
          <>
            {Object.keys(selectedWorkloadAnnotations).length > 0 ? (
              <>
                <Grid container gap={3}>
                  <CustomDropdown
                    label='Git Integration'
                    value={filteredGitIntegrations.find((i: any) => i.key === selectedGitIntegration)?.label || ''}
                    minWidth='175px'
                    options={filteredGitIntegrations.map((i: any) => i.label)}
                    onChange={(e: any) => {
                      const selected = filteredGitIntegrations.find((i: any) => i.label === e.target.value);
                      if (selected) setSelectedGitIntegration(selected.key);
                    }}
                    showNormalField={true}
                    isLoading={isGitReposLoading}
                    disableClearable
                  />
                </Grid>
                <Typography sx={{ mt: 2, mb: 1, color: 'success.main', fontWeight: 500 }}>Source configuration detected</Typography>
                <ul>
                  {Object.entries(selectedWorkloadAnnotations).map(([key, value]) => (
                    <li key={key}>
                      <strong>{key}:</strong> {value}
                    </li>
                  ))}
                </ul>
                <Typography variant='body2' sx={{ mt: 1, color: 'text.secondary' }}>
                  The system will automatically detect the repository and values files to create the pull request.
                </Typography>
              </>
            ) : (
              <>
                <Typography sx={{ color: 'warning.main', mb: 1 }}>No source configuration detected</Typography>
                <Typography variant='body2' sx={{ mb: 2 }}>
                  To enable pull request creation, configure one of the following on your workload:
                </Typography>
                <Typography variant='body2' sx={{ fontWeight: 600, mb: 1 }}>
                  Option 1: Nudgebee Annotations
                </Typography>
                <ul>
                  <li>
                    <strong>ci.nudgebee.com/git.repo</strong> - Git repository URL
                  </li>
                  <li>
                    <strong>ci.nudgebee.com/git.hash</strong> - Commit hash (optional)
                  </li>
                  <li>
                    <strong>ci.nudgebee.com/helm.values.filePath</strong> - Path to Helm values file (optional)
                  </li>
                </ul>
                <Typography variant='body2' sx={{ fontWeight: 600, mb: 1, mt: 2 }}>
                  Option 2: ArgoCD Deployment
                </Typography>
                <ul>
                  <li>
                    <strong>argocd.argoproj.io/tracking-id</strong> - ArgoCD tracking annotation
                  </li>
                </ul>
              </>
            )}
            <Grid container sx={{ justifyContent: 'end', mb: 3, mt: 2, button: { minWidth: '140px' } }} gap={1}>
              <Grid item>
                <CustomButton text='Cancel' variant='secondary' size='Medium' onClick={closePRModal} disabled={prLoading} />
              </Grid>
              <Grid item>
                <CustomButton
                  disabled={!selectedGitIntegration || !Object.keys(selectedWorkloadAnnotations).length || prLoading}
                  text='Create PR'
                  onClick={handleCreatePR}
                  size='Medium'
                />
              </Grid>
            </Grid>
          </>
        ) : (
          <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '16px', py: '30px' }}>
            <Typography sx={{ color: colors.text.secondary, fontSize: '14px', textAlign: 'center' }}>
              No GitHub or GitLab integrations configured. Connect a repository to enable pull request creation.
            </Typography>
            <CustomButton
              text='Configure Git Integration'
              variant='primary'
              size='Medium'
              onClick={() => window.open('/accounts/account-form?cloudProvider=GITHUB', '_blank')}
              sx={{ minWidth: '220px' }}
            />
          </Box>
        )}
      </Modal>

      {/* ── Ticket Create Form Modal ── */}
      <TicketCreatePopupForm
        open={isTicketFormOpen}
        handleClose={() => setIsTicketFormOpen(false)}
        onClose={() => setIsTicketFormOpen(false)}
        onSuccess={() => {
          setIsTicketFormOpen(false);
          snackbar.success('Ticket created successfully');
        }}
        onFailure={(error: string) => {
          snackbar.error(error || 'Failed to create ticket');
        }}
        ticketData={ticketData}
        ticketUrl={{}}
        reference={{
          id: recommendation?.id,
          type: 'kubernetes',
        }}
      />
    </>
  );
};

export default ResolveModal;
