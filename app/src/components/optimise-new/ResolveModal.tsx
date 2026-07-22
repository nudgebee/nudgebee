import { useState, useCallback, useEffect, useMemo } from 'react';
import { Box, Typography, CircularProgress, Grid } from '@mui/material';
import { Modal } from '@ui/Modal';
import AutoPilotHeaderCard from '@components/autopilot/card/AutoPilotHeaderCard';
import AutoOptimizeForm from '@components/autopilot/form/AutoOptimizeVerticalRightSizingForm';
import { formatMemory } from '@lib/formatter';
import { ds } from 'src/utils/colors';
import { toast as snackbar } from '@ui/Toast';
import { ANNOTATIONS, CI_PREFIX } from '@lib/annotationKeys';
import recommendationApi from '@api1/recommendation';
import apiTickets from '@api1/tickets';
import k8sApi from '@api1/kubernetes';
import apiAccount from '@api1/account';
import TicketCreatePopupForm from '@components/tickets/TicketCreatePopupForm';
import AutoOptimizeScheduledConfiguration from '@components/autopilot/form/AutoOptimizeVerticalRightSizingSingleConfiguration';
import AutoOptimizeContinuousConfiguration from '@components/autopilot/form/AutoOptimizeContinuousVerticalRightSizingSingleConfiguration';
import { Select } from '@ui/Select';
import { Checkbox } from '@ui/Checkbox';
import Tooltip from '@ui/Tooltip';
import Heading from '@components/common/Heading';
import { Button } from '@ui/Button';
import SafeIcon from '@shared/icons/SafeIcon';
import { BetaIcon, ExternalLinkIcon } from '@assets';

const betaBadge = <SafeIcon src={BetaIcon} alt='Beta Icon' width={16} height={12} style={{ marginLeft: ds.space[0] }} />;
const externalLinkBadge = <SafeIcon src={ExternalLinkIcon} alt='Open in new tab' width={12} height={12} />;

// Detects whether a workload is declaratively managed (GitOps / Helm / Argo CD /
// Flux) so a manual "Deploy Fix" — which patches the live pods directly — can warn
// that the change will be reverted on the next reconcile. Returns the manager's
// display name and the git repo to raise a PR against, when either is known.
const detectGitOpsManager = (
  annotations: Record<string, string> = {},
  labels: Record<string, string> = {}
): { manager: string | null; gitRepo: string | null } => {
  const annKeys = Object.keys(annotations);
  const labKeys = Object.keys(labels);
  const gitRepo = annotations[ANNOTATIONS.CI_GIT_REPO] || annotations[ANNOTATIONS.WORKLOAD_GIT_REPO] || null;
  let manager: string | null = null;
  if (annKeys.some((k) => k.startsWith('argocd.argoproj.io/')) || labels['argocd.argoproj.io/instance']) {
    manager = 'Argo CD';
  } else if ([...annKeys, ...labKeys].some((k) => k.includes('fluxcd.io'))) {
    manager = 'Flux';
  } else if (labels['app.kubernetes.io/managed-by'] === 'Helm' || annotations['meta.helm.sh/release-name']) {
    manager = 'Helm';
  } else if (gitRepo) {
    manager = 'GitOps';
  }
  return { manager, gitRepo };
};

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
  // In-place resize policy written into the PR (KEP-1287). Applied only on
  // Kubernetes 1.35+ clusters; older clusters fall back to a rollout on deploy.
  const [resizePolicyMode, setResizePolicyMode] = useState<string>('in-place');
  // Deploy Fix: apply in place (no restart) when the cluster supports it
  // (Kubernetes 1.35+); otherwise it auto-falls back to a rolling restart.
  const [inPlace, setInPlace] = useState<boolean>(true);
  const [selectedWorkloadAnnotations, setSelectedWorkloadAnnotations] = useState<Record<string, string>>({});
  const [isGitReposLoading, setIsGitReposLoading] = useState(false);
  // Declarative-management detection (GitOps/Helm/Argo/Flux) so a manual "Deploy
  // Fix" can warn it will be reverted on the next sync. driftConfirm holds the
  // pending large-change confirmation rows (null = no confirmation open).
  const [gitOpsInfo, setGitOpsInfo] = useState<{ manager: string | null; gitRepo: string | null }>({ manager: null, gitRepo: null });
  const [driftConfirm, setDriftConfirm] = useState<Array<{ label: string; from: string; to: string; pct: number }> | null>(null);

  // Ticket state
  const [isTicketFormOpen, setIsTicketFormOpen] = useState(false);

  // Auto-optimize config modal state (in-app, no redirect)
  const [isAutoPilotScheduledFormOpen, setIsAutoPilotScheduledFormOpen] = useState(false);
  const [isAutoPilotContinuousFormOpen, setIsAutoPilotContinuousFormOpen] = useState(false);
  const [autoOptimizeLoading, setAutoOptimizeLoading] = useState(false);
  const [msTeamsData, setMsTeamsData] = useState<any[]>([]);
  const [googleChannelList, setGoogleChannelList] = useState<any[]>([]);
  const [isMsTeamsLoading, setIsMsTeamsLoading] = useState(false);
  const [isGoogleChannelsLoading, setIsGoogleChannelsLoading] = useState(false);

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
        const labels = workload.meta?.config?.labels || {};
        setGitOpsInfo(detectGitOpsManager(annotations, labels));
        const filteredKeys = Object.keys(annotations).filter((key) => key.startsWith(CI_PREFIX) || key.startsWith('argocd.argoproj.io'));
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
            if (attr.name.startsWith(CI_PREFIX)) {
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
    const repoUrl = selectedWorkloadAnnotations[ANNOTATIONS.CI_GIT_REPO] || selectedWorkloadAnnotations[ANNOTATIONS.WORKLOAD_GIT_REPO];
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
    setGitOpsInfo({ manager: null, gitRepo: null });
    setDriftConfirm(null);
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

  // Rows describing resource changes that deviate sharply (>4x up or >75% down)
  // from the current allocation. New and current share a display unit (cores /
  // MB), so the ratio is unit-agnostic. Empty when the current value is unknown
  // or nothing is large — those are handled by the backend sanity validator.
  const computeDriftRows = () => {
    const rows: Array<{ label: string; from: string; to: string; pct: number }> = [];
    // CPU may be a bare-cores number ("0.5") or a millicores string ("500m"); parse
    // both to cores so the ratio isn't skewed (500m must read as 0.5, not 500).
    const parseCpu = (val: any) => {
      const s = String(val ?? '').trim();
      if (!s) return NaN;
      return s.endsWith('m') ? parseFloat(s.slice(0, -1)) / 1000 : parseFloat(s);
    };
    const add = (label: string, oldRaw: any, newRaw: any, unit: string, isCpu = false) => {
      const from = isCpu ? parseCpu(oldRaw) : parseFloat(String(oldRaw ?? '').replace(/,/g, ''));
      const to = isCpu ? parseCpu(newRaw) : parseFloat(String(newRaw ?? '').replace(/,/g, ''));
      if (!Number.isFinite(from) || from <= 0 || !Number.isFinite(to) || to <= 0) return;
      const ratio = to / from;
      if (ratio >= 0.25 && ratio <= 4) return;
      rows.push({ label, from: `${from}${unit}`, to: `${to}${unit}`, pct: Math.round((ratio - 1) * 100) });
    };
    for (const c of Object.keys(updatedData)) {
      const cur = allocatedData[c] || {};
      const next = updatedData[c] || {};
      add(`${c} · CPU request`, cur.cpu?.request, next.cpu?.request, '', true);
      add(`${c} · CPU limit`, cur.cpu?.limit, next.cpu?.limit, '', true);
      add(`${c} · memory request`, cur.memory?.request, next.memory?.request, 'MB');
      add(`${c} · memory limit`, cur.memory?.limit, next.memory?.limit, 'MB');
    }
    return rows;
  };

  const submitRecommendation = async (skipConfirm = false) => {
    // Warn before a large deviation from the current allocation (a likely mistake
    // or fat-fingered value) — the user confirms once, then we proceed.
    if (!skipConfirm) {
      const rows = computeDriftRows();
      if (rows.length > 0) {
        setDriftConfirm(rows);
        return;
      }
    }
    setDeploying(true);
    try {
      const dataToSubmit = getDataWithMemorySuffix();
      const accountId = recommendation.account_id || '';
      const recommendationId = recommendation.id;
      const result = await recommendationApi.applyRecommendation(accountId, recommendationId, dataToSubmit, undefined, { in_place: inPlace });

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
    // Single fetch: listTicketConfigsForCreate returns all tenant configs and filters
    // client-side, so fetch once and split by tool to avoid a redundant request.
    apiTickets
      .listTicketConfigsForCreate({ status: 'enabled' })
      .then((res: any) => {
        const configs = res?.data || [];
        return [{ data: configs.filter((c: any) => c?.tool === 'github') }, { data: configs.filter((c: any) => c?.tool === 'gitlab') }];
      })
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
      .applyRecommendation(accountId, recommendation.id, data, integrationType, { name: integrationName, resize_policy: resizePolicyMode })
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

  // ── Auto-optimize config (in-app modal, no redirect / new tab) ──
  // Channel options for the notification step of the config forms; Slack is
  // fetched by the forms themselves.
  const getChannelsListSlackMsTeams = async () => {
    setIsMsTeamsLoading(true);
    setIsGoogleChannelsLoading(true);
    try {
      const [resMsTeams, resGoogle]: any[] = await Promise.all([
        apiAccount.getNotificationChannelList('ms_teams'),
        apiAccount.getNotificationChannelList('google_chat'),
      ]);
      setMsTeamsData(resMsTeams?.data?.data?.map((item: any) => ({ label: item.name, value: item.id, channels: item.channels })) || []);
      setGoogleChannelList(resGoogle?.data?.data?.map((item: any) => ({ label: item.name, value: item.id })) || []);
    } catch (error) {
      console.error('Failed to fetch notification channels:', error);
    } finally {
      setIsMsTeamsLoading(false);
      setIsGoogleChannelsLoading(false);
    }
  };

  const handleScheduleAutoOptimize = () => {
    getChannelsListSlackMsTeams();
    setIsAutoPilotScheduledFormOpen(true);
  };

  const handleContinuousAutoOptimize = () => {
    getChannelsListSlackMsTeams();
    setIsAutoPilotContinuousFormOpen(true);
  };

  const closeAutoPilotScheduledConfigModal = (success?: boolean) => {
    setIsAutoPilotScheduledFormOpen(false);
    setMsTeamsData([]);
    setGoogleChannelList([]);
    if (success) onSuccess?.();
  };

  const closeAutoPilotContinuousConfigModal = (success?: boolean) => {
    setIsAutoPilotContinuousFormOpen(false);
    setMsTeamsData([]);
    setGoogleChannelList([]);
    if (success) onSuccess?.();
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

  // Same shape as autoPilotData but without a truthy id, so the config forms take
  // the "create new rule" path (a present id makes them issue an update instead).
  const autoOptimizeData = { ...autoPilotData, id: undefined };

  // ── Action buttons (footer) ──
  const ticketExists = !!recommendation?.ticket;
  const actionButtons = (
    <Box
      sx={{
        display: 'flex',
        height: '56px',
        justifyContent: 'space-between',
        alignItems: 'center',
        gap: ds.space.mul(0, 3),
        flexShrink: 0,
        paddingX: ds.space.mul(0, 5),
        '&& button': { minWidth: 'auto' },
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3) }}>
        <Button tone='secondary' size='sm' onClick={handleClose} disabled={deploying} id='resolve-modal-cancel'>
          Cancel
        </Button>
        <Tooltip title='Resize running pods without a restart on Kubernetes 1.35+. Older clusters automatically fall back to a rolling restart.'>
          <Box
            component='label'
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              boxSizing: 'border-box',
              px: 'var(--ds-space-3)',
              height: '28px',
              border: '1px solid',
              borderRadius: 'var(--ds-radius-md)',
              backgroundColor: inPlace ? 'var(--ds-brand-100)' : 'var(--ds-background-100)',
              borderColor: inPlace ? 'var(--ds-brand-300)' : 'var(--ds-brand-200)',
              cursor: deploying ? 'not-allowed' : 'pointer',
              transition: 'border-color var(--ds-motion-micro) var(--ds-motion-ease), background-color var(--ds-motion-micro) var(--ds-motion-ease)',
              '&:hover': {
                backgroundColor: 'var(--ds-brand-100)',
                borderColor: 'var(--ds-brand-300)',
              },
            }}
          >
            <Checkbox checked={inPlace} onChange={(checked) => setInPlace(checked)} disabled={deploying} label='No-restart (in-place)' />
          </Box>
        </Tooltip>
      </Box>
      <Box sx={{ display: 'flex', gap: ds.space.mul(0, 3), alignItems: 'center' }}>
        <Button tone='secondary' size='sm' onClick={openTicketForm} disabled={ticketExists} id='resolve-modal-ticket'>
          Create Ticket
        </Button>
        <Button tone='secondary' size='sm' icon={betaBadge} iconPlacement='end' onClick={openCreatePRModal} id='resolve-modal-pr'>
          Create PR
        </Button>
        <Button
          tone='secondary'
          size='sm'
          icon={recommendation?.continuousAutoPilotId ? externalLinkBadge : betaBadge}
          iconPlacement='end'
          tooltip={recommendation?.continuousAutoPilotId ? 'Open the active Continuous Auto Optimize in a new tab' : undefined}
          onClick={
            recommendation?.continuousAutoPilotId
              ? () => window.open(`/auto-pilot/task/${recommendation.continuousAutoPilotId}?accountId=${recommendation.account_id}`, '_blank')
              : handleContinuousAutoOptimize
          }
          disabled={!recommendation?.continuousAutoPilotId && !!recommendation?.scheduledAutoPilotId}
          id='resolve-modal-continuous'
        >
          Continuous Auto Optimize
        </Button>
        <Button
          tone='secondary'
          size='sm'
          icon={recommendation?.scheduledAutoPilotId ? externalLinkBadge : undefined}
          iconPlacement='end'
          tooltip={recommendation?.scheduledAutoPilotId ? 'Open the active Schedule Auto Optimize in a new tab' : undefined}
          onClick={
            recommendation?.scheduledAutoPilotId
              ? () => window.open(`/auto-pilot/task/${recommendation.scheduledAutoPilotId}?accountId=${recommendation.account_id}`, '_blank')
              : handleScheduleAutoOptimize
          }
          disabled={!recommendation?.scheduledAutoPilotId && !!recommendation?.continuousAutoPilotId}
          id='resolve-modal-schedule'
        >
          Schedule Auto Optimize
        </Button>
        <Button tone='secondary' size='sm' onClick={() => submitRecommendation()} disabled={deploying} id='resolve-modal-deploy'>
          {deploying ? 'Deploying...' : 'Deploy Fix'}
        </Button>
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
              padding: `${ds.space[4]} 40px`,
            },
          },
        }}
      >
        <Box sx={{ pb: ds.space.mul(0, 15) }}>
          <AutoPilotHeaderCard header='' data={autoPilotData} />
          {gitOpsInfo.manager && (
            <Box sx={{ backgroundColor: ds.amber[100], border: `0.5px solid ${ds.amber[300]}`, p: ds.space[4], mt: ds.space[4] }}>
              <Typography variant='body2' sx={{ color: ds.amber[700] }}>
                This workload is managed by <strong>{gitOpsInfo.manager}</strong>. “Deploy Fix” patches the running pods directly, so the change is
                reverted on the next sync.{' '}
                {gitOpsInfo.gitRepo ? '“Create PR” persists it in Git instead.' : 'Persist changes through your GitOps source instead.'}
              </Typography>
            </Box>
          )}
          {Object.keys(updatedData).length > 0
            ? Object.keys(updatedData).map((containerName) => (
                <Box key={containerName} sx={{ display: 'flex', gap: ds.space[4], marginTop: ds.space[4] }}>
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[5], width: '100%' }}>
                    <Heading value={`Container Name - ${containerName}`} borderColor={ds.blue[500]} borderWidth='sm' />
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
          <Box sx={{ display: 'flex', justifyContent: 'center', py: ds.space.mul(0, 10) }}>
            <CircularProgress size={24} />
          </Box>
        )}
        {isGitReposLoading || filteredGitIntegrations.length > 0 ? (
          <>
            {Object.keys(selectedWorkloadAnnotations).length > 0 ? (
              <>
                <Grid container gap={3}>
                  <Select
                    label='Git Integration'
                    value={selectedGitIntegration}
                    options={filteredGitIntegrations.map((i: any) => ({ value: i.key, label: i.label }))}
                    onChange={(v: string) => setSelectedGitIntegration(v)}
                    disabled={isGitReposLoading}
                  />
                </Grid>
                <Grid container gap={3} sx={{ mt: 2 }}>
                  <Select
                    label='In-place resize policy'
                    value={resizePolicyMode}
                    options={[
                      { value: 'in-place', label: 'In-place — resize without restart (CPU & memory)' },
                      { value: 'restart-memory', label: 'Restart container on memory change' },
                      { value: 'disabled', label: "Don't configure (apply on next rollout)" },
                    ]}
                    onChange={(v: string) => setResizePolicyMode(v)}
                    disabled={prLoading}
                  />
                </Grid>
                <Typography variant='body2' sx={{ mt: 1, color: ds.gray[500] }} data-testid='resize-policy-hint'>
                  Adds a <strong>resizePolicy</strong> to the workload so future pods resize without a restart on Kubernetes 1.35+. Older clusters
                  ignore it and apply on the next rollout.
                </Typography>
                <Typography sx={{ mt: 2, mb: 1, color: ds.green[600], fontWeight: ds.weight.medium }}>Source configuration detected</Typography>
                <ul>
                  {Object.entries(selectedWorkloadAnnotations).map(([key, value]) => (
                    <li key={key}>
                      <strong>{key}:</strong> {value}
                    </li>
                  ))}
                </ul>
                <Typography variant='body2' sx={{ mt: 1, color: ds.gray[500] }}>
                  The system will automatically detect the repository and values files to create the pull request.
                </Typography>
              </>
            ) : (
              <>
                <Typography sx={{ color: ds.amber[600], mb: 1 }}>No source configuration detected</Typography>
                <Typography variant='body2' sx={{ mb: 2 }}>
                  To enable pull request creation, configure one of the following on your workload:
                </Typography>
                <Typography variant='body2' sx={{ fontWeight: ds.weight.semibold, mb: 1 }}>
                  Option 1: Nudgebee Annotations
                </Typography>
                <ul>
                  <li>
                    <strong>{ANNOTATIONS.CI_GIT_REPO}</strong> - Git repository URL
                  </li>
                  <li>
                    <strong>{ANNOTATIONS.CI_GIT_HASH}</strong> - Commit hash (optional)
                  </li>
                  <li>
                    <strong>{ANNOTATIONS.CI_HELM_VALUES_PATH}</strong> - Path to Helm values file (optional)
                  </li>
                </ul>
                <Typography variant='body2' sx={{ fontWeight: ds.weight.semibold, mb: 1, mt: 2 }}>
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
                <Button tone='secondary' size='md' onClick={closePRModal} disabled={prLoading}>
                  Cancel
                </Button>
              </Grid>
              <Grid item>
                <Button
                  tone='primary'
                  size='md'
                  disabled={!selectedGitIntegration || !Object.keys(selectedWorkloadAnnotations).length || prLoading}
                  onClick={handleCreatePR}
                >
                  Create PR
                </Button>
              </Grid>
            </Grid>
          </>
        ) : (
          <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: ds.space[4], py: ds.space.mul(0, 15) }}>
            <Typography sx={{ color: ds.gray[700], fontSize: ds.text.bodyLg, textAlign: 'center' }}>
              No GitHub or GitLab integrations configured. Connect a repository to enable pull request creation.
            </Typography>
            <Button tone='primary' size='md' onClick={() => window.open('/accounts/account-form?cloudProvider=GITHUB', '_blank')}>
              Configure Git Integration
            </Button>
          </Box>
        )}
      </Modal>

      {/* ── Confirm large resource change ── */}
      <Modal width='sm' open={!!driftConfirm} handleClose={() => setDriftConfirm(null)} title='Confirm large resource change'>
        <Box sx={{ p: ds.space[4] }}>
          <Typography variant='body2' sx={{ color: ds.gray[700], mb: ds.space[4] }}>
            These values deviate sharply from the current allocation. Confirm this is intended:
          </Typography>
          <Box component='ul' sx={{ pl: ds.space[5], mb: ds.space[5] }}>
            {(driftConfirm || []).map((r) => (
              <li key={r.label}>
                <strong>{r.label}:</strong> {r.from} → {r.to} ({r.pct > 0 ? '+' : ''}
                {r.pct}%)
              </li>
            ))}
          </Box>
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3] }}>
            <Button tone='secondary' size='md' onClick={() => setDriftConfirm(null)}>
              Cancel
            </Button>
            <Button
              tone='primary'
              size='md'
              onClick={() => {
                setDriftConfirm(null);
                submitRecommendation(true);
              }}
            >
              Deploy anyway
            </Button>
          </Box>
        </Box>
      </Modal>

      {/* ── Schedule Auto Optimize Config Modal ── */}
      <Modal
        width='lg'
        open={isAutoPilotScheduledFormOpen}
        handleClose={() => closeAutoPilotScheduledConfigModal(false)}
        title='Scheduled Auto Optimize Configuration'
        loader={autoOptimizeLoading}
        sx={{
          '& .MuiPaper-root': {
            maxWidth: ds.space.mul(0, 505),
            '& .MuiDialogContent-root': {
              padding: 'var(--ds-space-4) var(--ds-space-6)',
            },
          },
        }}
      >
        <AutoOptimizeScheduledConfiguration
          autoOptimizeData={autoOptimizeData}
          closeAutoPilotSingleConfigModal={closeAutoPilotScheduledConfigModal}
          msTeamsData={msTeamsData}
          googleChannelList={googleChannelList}
          isMsTeamsLoading={isMsTeamsLoading}
          isGoogleChannelsLoading={isGoogleChannelsLoading}
          data={updatedData}
          currentData={allocatedData}
          additionalInfoCPUAndMem={{ cpuInfo: additionalCpuInfo, memInfo: additionalMemInfo }}
          setIsLoading={setAutoOptimizeLoading}
        />
      </Modal>

      {/* ── Continuous Auto Optimize Config Modal ── */}
      <Modal
        width='lg'
        open={isAutoPilotContinuousFormOpen}
        handleClose={() => closeAutoPilotContinuousConfigModal(false)}
        title='Continuous Auto Optimize Configuration'
        loader={autoOptimizeLoading}
        sx={{
          '& .MuiPaper-root': {
            maxWidth: ds.space.mul(0, 505),
            '& .MuiDialogContent-root': {
              padding: 'var(--ds-space-4) var(--ds-space-6)',
            },
          },
        }}
      >
        <AutoOptimizeContinuousConfiguration
          autoOptimizeData={autoOptimizeData}
          closeAutoPilotSingleConfigModal={closeAutoPilotContinuousConfigModal}
          msTeamsData={msTeamsData}
          googleChannelList={googleChannelList}
          isMsTeamsLoading={isMsTeamsLoading}
          isGoogleChannelsLoading={isGoogleChannelsLoading}
          setIsLoading={setAutoOptimizeLoading}
        />
      </Modal>

      {/* ── Ticket Create Form Modal ── */}
      <TicketCreatePopupForm
        open={isTicketFormOpen}
        handleClose={() => setIsTicketFormOpen(false)}
        onClose={() => setIsTicketFormOpen(false)}
        // TicketCreatePopupForm already shows the success toast (with the ticket link); only close here.
        onSuccess={() => setIsTicketFormOpen(false)}
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
