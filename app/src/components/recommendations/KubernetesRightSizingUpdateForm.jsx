import { Grid, ToggleButton, ToggleButtonGroup, Typography } from '@mui/material';
import React, { useEffect, useState, useMemo } from 'react';
import Box from '@mui/material/Box';
import apiTickets from '@api1/tickets';
import apiRecommendations from '@api1/recommendation';
import { Modal } from '@ui/Modal';
import { Select } from '@ui/Select';
import { Label } from '@ui/Label';
import { MemoryHeadroomCheck, MemoryUsageFacts } from './MemoryUsageFacts';
import { toast as snackbar } from '@ui/Toast';
import { ANNOTATIONS, CI_PREFIX, CI_REQUEST_ANNOTATIONS } from '@lib/annotationKeys';
import { detectGitOpsManager } from '@utils/workloadManager';
import k8sApi from '@api1/kubernetes';
import { Link } from '@ui/Link';
import AutoOptimizeForm from '@components/autopilot/form/AutoOptimizeVerticalRightSizingForm';

import PropTypes from 'prop-types';
import { Button } from '@ui/Button';
import { PrOpenIcon, UpdateIcon } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import { SummaryBlock } from '@components/k8s/KubernetesClusterSummary';
import MarkDowns from '@shared/viewers/MarkDowns';
import { ds } from 'src/utils/colors';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import { detectGitProvider } from '@components/optimise-new/gitProvider';

// Kubernetes resource quantity ("128Mi", "1.5Gi", "100m", "512M", "2", "1e3") ->
// a plain number: bytes for memory, cores for CPU. Returns null for anything it
// can't parse so callers can tell "not set" apart from zero.
const K8S_QUANTITY_MULTIPLIERS = {
  Ki: 1024,
  Mi: 1024 ** 2,
  Gi: 1024 ** 3,
  Ti: 1024 ** 4,
  Pi: 1024 ** 5,
  k: 1e3,
  K: 1e3,
  M: 1e6,
  G: 1e9,
  T: 1e12,
  P: 1e15,
  m: 1e-3,
};
const parseK8sQuantity = (raw) => {
  if (raw === null || raw === undefined) return null;
  const s = String(raw).trim();
  if (!s) return null;
  // The exponent has to be part of the number capture: Kubernetes accepts
  // scientific notation, and a suffix group of [A-Za-z]* can never hold "e3" or
  // "e-3", so without it the whole quantity fails to match and reads as unset.
  const match = s.match(/^(-?[0-9.]+(?:[eE][-+]?\d+)?)([A-Za-z]*)$/);
  if (!match) return null;
  const num = parseFloat(match[1]);
  if (!Number.isFinite(num)) return null;
  const suffix = match[2];
  if (!suffix) return num;
  const multiplier = K8S_QUANTITY_MULTIPLIERS[suffix];
  return multiplier === undefined ? null : num * multiplier;
};

// Current requests/limits read off the workload's own container spec — the
// authoritative values collected from the K8s API. Needed because the
// event-evidence path reports 0 for both whenever the
// kube_pod_container_resource_* series are missing (pod already gone, KSM scrape
// gap), which leaves the "Current" column blank on a workload that does have
// requests and limits set.
const containerSpecAllocation = (workload, containerName) => {
  const containers = workload?.meta?.config?.containers;
  if (!Array.isArray(containers) || containers.length === 0) return null;
  // Only guess the container when there is nothing to guess between — a wrong
  // container's spec is worse than no spec.
  const container = containers.find((c) => c?.name === containerName) || (containers.length === 1 ? containers[0] : null);
  if (!container) return null;
  const requests = container?.resources?.requests || {};
  const limits = container?.resources?.limits || {};
  return {
    cpu: { request: parseK8sQuantity(requests.cpu), limit: parseK8sQuantity(limits.cpu) },
    memory: { request: parseK8sQuantity(requests.memory), limit: parseK8sQuantity(limits.memory) },
  };
};

// A value the producer actually knows. Neither source expresses "unset" as
// anything but 0 (the evidence path defaults missing requests/limits to 0), so
// 0 means "fall back to the workload spec", not "zero cores".
const knownValue = (v) => {
  const num = typeof v === 'string' ? parseFloat(v) : v;
  return Number.isFinite(num) && num > 0 ? num : null;
};

// Beyond this many containers the segmented control wraps badly; use a dropdown.
const MAX_CONTAINER_BUTTONS = 4;

// CPU may be a bare-cores number ("0.5") or a millicores string ("500m"); parse
// both to cores (500m must read as 0.5, not 500).
const parseCpuCores = (val) => {
  const s = String(val ?? '').trim();
  if (!s) return NaN;
  return s.endsWith('m') ? parseFloat(s.slice(0, -1)) / 1000 : parseFloat(s);
};

const KubernetesRightSizingPopupForm = ({
  open,
  title = 'Update Resource Limits',
  onClose,
  onSuccess,
  onFailure,
  data = {},
  updateResourceType = '',
  recommendationSource,
  containerOptions = [],
  onContainerChange,
  alertContainer,
}) => {
  const requestAnnotations = CI_REQUEST_ANNOTATIONS;
  const [updatedData, setUpdatedData] = useState({ ...data });
  const [updateType, setUpdateType] = useState(updateResourceType);
  const [allGitIntegrations, setAllGitIntegrations] = useState([]); // Combined GitHub + GitLab integrations
  const [selectedGitIntegration, setSelectedGitIntegration] = useState(''); // Format: "github:name" or "gitlab:name"
  const [isGitIntegrationsLoading, setIsGitIntegrationsLoading] = useState(false);
  const [loading, setLoading] = useState(false);
  const [selectedWorkloadAnnotations, setSelectedWorkloadAnnotations] = useState({});
  const [selectedButtons, setSelectedButtons] = useState({
    algo: 0,
    buffer: 0,
    memory: 0,
    memBuffer: 3,
  });
  const [algo, setAlgo] = useState('NBALGO');
  // Declarative-management detection (GitOps/Helm/Argo/Flux) so a manual "Update"
  // can warn it will be reverted on the next sync. driftConfirm holds the pending
  // large-change confirmation rows (null = no confirmation open).
  const [gitOpsInfo, setGitOpsInfo] = useState({ manager: null, gitRepo: null });
  const [driftConfirm, setDriftConfirm] = useState(null);
  // The workload being tuned. Its container spec (requests/limits) is the
  // fallback current allocation when the caller's data carries none, and is read
  // per container so switching containers switches the Current column too.
  const [workloadSpec, setWorkloadSpec] = useState(null);
  const tunedContainer = data?.container_name || data?.cloud_resourse?.meta?.container;
  const specAllocation = useMemo(() => containerSpecAllocation(workloadSpec, tunedContainer), [workloadSpec, tunedContainer]);
  // The caller lists the containers its evidence knows about; the workload spec
  // adds any the metrics never returned, so every container is reachable.
  const containerChoices = useMemo(() => {
    if (containerOptions.length === 0) return containerOptions;
    const specNames = (workloadSpec?.meta?.config?.containers || []).map((c) => c?.name).filter(Boolean);
    return [...new Set([...containerOptions, ...specNames])];
  }, [containerOptions, workloadSpec]);

  const parsedOldRequest =
    data?.memory?.oldRequest && typeof data?.memory?.oldRequest === 'string'
      ? parseFloat(data.memory.oldRequest.split(' ')[0])
      : data?.memory?.oldRequest;
  const parsedOldLimit =
    data?.memory?.oldLimit && typeof data?.memory?.oldLimit === 'string' ? parseFloat(data.memory.oldLimit.split(' ')[0]) : data?.memory?.oldLimit;

  // Memory is displayed in MB for event-sourced tuning and GB elsewhere; the
  // submit path converts back with the same divisor.
  const memBytesToDisplay = (bytes) => {
    if (bytes == null) return null;
    const divisor = recommendationSource === 'event' ? 1024 ** 2 : 1024 ** 3;
    return Math.round((bytes / divisor) * 100) / 100;
  };

  // The current allocation the "Current" column shows and the toggles compute
  // against: what the caller passed, else the workload's container spec.
  const currentCpuRequest = knownValue(data?.cpu?.oldRequest) ?? knownValue(specAllocation?.cpu?.request);
  const currentCpuLimit = knownValue(data?.cpu?.oldLimit) ?? knownValue(specAllocation?.cpu?.limit);
  const currentMemRequest = knownValue(parsedOldRequest) ?? memBytesToDisplay(knownValue(specAllocation?.memory?.request));
  const currentMemLimit = knownValue(parsedOldLimit) ?? memBytesToDisplay(knownValue(specAllocation?.memory?.limit));

  // The unbuffered base the algo/buffer toggles multiply against: the caller's
  // observed-usage base (nbalgoBase), but never below what the workload already
  // has allocated.
  //
  // Taking nbalgoBase on its own recommends a shrink on the workload that just
  // ran out of room: the usage series behind it is truncated precisely because
  // the container died, so an OOMKilled pod's samples stop at a few MB. A
  // 128Mi/192Mi pod whose last four samples read 2.74MB got "+10% buffer" =
  // 2.87MB for both request and limit. These modals only open on a
  // resource-pressure event (OOM, memory reaching limit, CPU throttling), so the
  // current allocation is a floor. Usage alone still drives the math when the
  // current allocation is unknown — the case nbalgoBase was added for.
  const higherOf = (...candidates) => {
    const values = candidates.map((v) => (typeof v === 'string' ? parseFloat(v) : v)).filter((v) => Number.isFinite(v));
    return values.length ? Math.max(...values) : null;
  };
  // What the container actually used — only the event path carries it.
  const memoryUsage = recommendationSource === 'event' ? data?.memory?.usage : undefined;
  // Kubernetes admits no request above its limit, so a current request above the
  // current limit is a miscount in the evidence (a second exporter publishing the
  // same series doubled it). The limit is then the request's real floor.
  const memRequestFloor =
    Number.isFinite(parseFloat(currentMemRequest)) && Number.isFinite(parseFloat(currentMemLimit))
      ? Math.min(parseFloat(currentMemRequest), parseFloat(currentMemLimit))
      : currentMemRequest;
  const memNbalgoBase = higherOf(data?.memory?.nbalgoBase, memRequestFloor);
  // The limit's base is the current LIMIT, not the request — sharing one base
  // collapses a 192Mi limit onto the request's value. Never below the request's
  // base, though: with no limit set, the usage peak can sit under a generous
  // request, and both are multiplied by the same buffer.
  const memNbalgoLimit = higherOf(data?.memory?.nbalgoBase, currentMemLimit, memNbalgoBase);
  const cpuNbalgoBase = higherOf(data?.cpu?.nbalgoBase, currentCpuRequest);

  // `|| undefined` (not `|| ''`) so `'' * bufferMultiplier === 0` can't sneak a
  // zero recommendation into the input. With undefined, `undefined * mult` is
  // NaN, which `parseMemValue` returns as null and the box stays empty.
  const additionalMemInfo = {
    req: currentMemRequest ?? '',
    limit: currentMemLimit ?? '',
    nbalgoReq: memNbalgoBase || undefined,
    nbalgoLimit: memNbalgoLimit || undefined,
  };

  const additionalCpuInfo = {
    p99: cpuNbalgoBase,
    p97: cpuNbalgoBase,
    p95: cpuNbalgoBase,
    nbalgo: cpuNbalgoBase,
  };

  const allocatedData = {
    cpu: {
      request: currentCpuRequest ?? '',
      limit: currentCpuLimit ?? '',
    },
    memory: {
      request: currentMemRequest ?? '',
      limit: currentMemLimit ?? '',
    },
  };

  const listGitConfigurations = () => {
    setIsGitIntegrationsLoading(true);
    // Single fetch: listTicketConfigsForCreate returns all tenant configs and filters
    // client-side, so fetch once and split by tool to avoid a redundant request.
    apiTickets
      .listTicketConfigsForCreate({ status: 'enabled' })
      .then((res) => {
        const configs = res?.data || [];
        return [{ data: configs.filter((c) => c?.tool === 'github') }, { data: configs.filter((c) => c?.tool === 'gitlab') }];
      })
      .then(([githubRes, gitlabRes]) => {
        const githubData =
          githubRes?.data?.length > 0
            ? githubRes.data.map((g) => ({ name: g.name, type: 'github', key: `github:${g.name}`, label: `GitHub: ${g.name}` }))
            : [];
        const gitlabData =
          gitlabRes?.data?.length > 0
            ? gitlabRes.data.map((g) => ({ name: g.name, type: 'gitlab', key: `gitlab:${g.name}`, label: `GitLab: ${g.name}` }))
            : [];
        const combined = [...githubData, ...gitlabData];
        setAllGitIntegrations(combined);
      })
      .catch(() => {
        setAllGitIntegrations([]);
      })
      .finally(() => {
        setIsGitIntegrationsLoading(false);
      });
  };

  useEffect(() => {
    if (open) {
      // Both flows need these on open: listGitConfigurations for the PR provider
      // choice (event code-fix PRs and rightsizing PRs alike), and
      // getWorkloadDeploymentForSelectedRightSize for GitOps detection + the
      // rightsizing annotations.
      listGitConfigurations();
      getWorkloadDeploymentForSelectedRightSize();
    } else {
      // Clean up state when modal closes
      setAllGitIntegrations([]);
      setSelectedGitIntegration('');
      setSelectedWorkloadAnnotations({});
      setGitOpsInfo({ manager: null, gitRepo: null });
      setDriftConfirm(null);
      setWorkloadSpec(null);
    }
  }, [open]);

  // Filter integrations based on the repo URL in annotations
  const filteredGitIntegrations = useMemo(() => {
    const repoUrl = selectedWorkloadAnnotations[ANNOTATIONS.CI_GIT_REPO] || selectedWorkloadAnnotations[ANNOTATIONS.WORKLOAD_GIT_REPO];
    const detectedProvider = detectGitProvider(repoUrl);
    if (!detectedProvider) return allGitIntegrations;
    return allGitIntegrations.filter((i) => i.type === detectedProvider);
  }, [selectedWorkloadAnnotations, allGitIntegrations]);

  // Auto-select first filtered integration when available
  useEffect(() => {
    if (filteredGitIntegrations.length > 0 && !selectedGitIntegration) {
      setSelectedGitIntegration(filteredGitIntegrations[0].key);
    }
  }, [filteredGitIntegrations, selectedGitIntegration]);

  useEffect(() => {
    let data2 = { ...data };
    if (data2?.memory?.request && typeof data2?.memory?.request === 'string') {
      let mem = data2?.memory?.request.split(' ')[0];
      mem = parseFloat(mem);
      data2.memory.request = mem;
    }
    if (data2?.memory?.limit && typeof data2?.memory?.limit === 'string') {
      let mem = data2?.memory?.limit.split(' ')[0];
      mem = parseFloat(mem);
      data2.memory.limit = mem;
    }
    if (data2?.cpu?.request && typeof data2?.cpu?.request === 'string') {
      let cpu = data2?.cpu?.request.split(' ')[0];
      cpu = parseFloat(cpu);
      data2.cpu.request = cpu;
    }
    if (data2?.cpu?.limit && typeof data2?.cpu?.limit === 'string') {
      let cpu = data2?.cpu?.limit.split(' ')[0];
      cpu = parseFloat(cpu);
      data2.cpu.limit = cpu;
    }
    setUpdatedData(data2);
  }, [data]);

  const handleUpdateData = (data) => {
    const data1 = data;
    setUpdatedData(data1);
  };

  const showError = (message) => {
    snackbar.error(message);
    setLoading(false);
  };

  // Rows describing resource changes that deviate sharply (>4x up or >75% down)
  // from the current allocation. The raw form value and the current allocation
  // share a display unit, so the ratio is unit-agnostic. Empty when the current
  // value is unknown or nothing is large — those are handled by the backend
  // sanity validator, not this confirmation.
  const computeDriftRows = () => {
    const memUnit = recommendationSource === 'event' ? 'Mi' : 'GB';
    const rows = [];
    const add = (label, oldRaw, newRaw, unit, isCpu = false) => {
      const from = isCpu ? parseCpuCores(oldRaw) : parseFloat(oldRaw);
      const to = isCpu ? parseCpuCores(newRaw) : parseFloat(newRaw);
      if (!Number.isFinite(from) || from <= 0 || !Number.isFinite(to) || to <= 0) return;
      const ratio = to / from;
      if (ratio >= 0.25 && ratio <= 4) return;
      rows.push({ label, from: `${from}${unit}`, to: `${to}${unit}`, pct: Math.round((ratio - 1) * 100) });
    };
    add('Memory request', allocatedData.memory.request, updatedData?.memory?.request, memUnit);
    add('Memory limit', allocatedData.memory.limit, updatedData?.memory?.limit, memUnit);
    add('CPU request', allocatedData.cpu.request, updatedData?.cpu?.request, '', true);
    add('CPU limit', allocatedData.cpu.limit, updatedData?.cpu?.limit, '', true);
    return rows;
  };

  const submitRecommendation = (skipConfirm = false) => {
    // Kubernetes refuses a request above its limit. Say so here, before the apply
    // marks the recommendation in progress and hands the agent a patch that fails.
    // Request and limit share a unit within each section, so they compare as-is.
    const memRequest = parseFloat(updatedData?.memory?.request);
    const memLimit = parseFloat(updatedData?.memory?.limit);
    if (Number.isFinite(memRequest) && Number.isFinite(memLimit) && memRequest > memLimit) {
      showError('Memory request is above the memory limit, which Kubernetes refuses. Lower the request or raise the limit.');
      return;
    }
    const cpuRequest = parseCpuCores(updatedData?.cpu?.request);
    const cpuLimit = parseCpuCores(updatedData?.cpu?.limit);
    if (Number.isFinite(cpuRequest) && Number.isFinite(cpuLimit) && cpuRequest > cpuLimit) {
      showError('CPU request is above the CPU limit, which Kubernetes refuses. Lower the request or raise the limit.');
      return;
    }
    // Warn before a large deviation from the current allocation (a likely mistake
    // or a fat-fingered value) — the user confirms once, then we proceed.
    if (!skipConfirm) {
      const rows = computeDriftRows();
      if (rows.length > 0) {
        setDriftConfirm(rows);
        return;
      }
    }
    setLoading(true);
    let dataToSubmit = {
      ...updatedData,
      memory: { ...(updatedData.memory || {}) },
      cpu: { ...(updatedData.cpu || {}) },
    };
    // For recommendation source, values are in GB and need * 1024 to convert to MiB.
    // For event source, values are already in MiB so no conversion needed.
    const memoryMultiplier = recommendationSource === 'event' ? 1 : 1024;
    if (dataToSubmit.memory.request) {
      if (isNaN(dataToSubmit.memory.request)) {
        showError('Memory Request should be a number');
        return;
      }
      dataToSubmit.memory.request = parseFloat(dataToSubmit.memory.request) * memoryMultiplier;
    } else {
      dataToSubmit.memory.request = null;
    }

    if (dataToSubmit.memory.limit) {
      if (isNaN(dataToSubmit.memory.limit)) {
        showError('Memory Limit should be a number');
        return;
      }
      dataToSubmit.memory.limit = parseFloat(dataToSubmit.memory.limit) * memoryMultiplier;
    } else {
      dataToSubmit.memory.limit = null;
    }

    if (dataToSubmit.cpu.request) {
      if (isNaN(dataToSubmit.cpu.request)) {
        showError('CPU Request should be a number');
        return;
      }
      dataToSubmit.cpu.request = parseFloat(dataToSubmit.cpu.request);
    } else {
      dataToSubmit.cpu.request = null;
    }

    if (dataToSubmit.cpu.limit) {
      if (isNaN(dataToSubmit.cpu.limit)) {
        showError('CPU Limit should be a number');
        return;
      }
      dataToSubmit.cpu.limit = parseFloat(dataToSubmit.cpu.limit);
    } else {
      dataToSubmit.cpu.limit = null;
    }

    // Memory is numeric MiB here; serialize it as an explicit Kubernetes quantity
    // ("160.79Mi"), matching the Raise-PR path. Sent as a bare number the apply
    // pipeline patches it verbatim and Kubernetes reads it as *bytes* (160.79 ->
    // "160790m", ~160 bytes), OOM-killing the pod. CPU stays a bare number (cores).
    if (dataToSubmit.memory.request) {
      dataToSubmit.memory.request += 'Mi';
    }
    if (dataToSubmit.memory.limit) {
      dataToSubmit.memory.limit += 'Mi';
    }

    const { memory, cpu, _, container_name, ...rest } = dataToSubmit;
    const result = {
      ...rest,
      container_name,
      [container_name]: { memory, cpu },
    };
    apiRecommendations
      .applyRecommendation(data.accountId, data.id, result, null, null, recommendationSource)
      .then((res) => {
        setLoading(false);
        if (res?.errors) {
          snackbar.error(`Failed to apply recommendation: ${parseHttpResponseBodyMessage(res)}`);
          onFailure(res?.errors);
        } else {
          snackbar.success('Recommendation applied successfully!');
          onSuccess(res?.data);
        }
      })
      .catch((error) => {
        setLoading(false);
        snackbar.error('Failed to apply recommendation. Please try again.');
        console.error('Error applying recommendation:', error);
      });
  };

  const handleGithubPRSuccessOrFail = (message, severity, prURL) => {
    const fullMessage = prURL ? `${message} Link to PR: ${prURL}` : message;

    if (severity === 'success') {
      snackbar.success(fullMessage);
    } else {
      snackbar.error(fullMessage);
    }
  };

  const getWorkloadDeploymentForSelectedRightSize = () => {
    if ('aiData' in data) {
      if ('source_details' in data.aiData) {
        if (Object.keys(data.aiData.source_details).length > 0) {
          setSelectedWorkloadAnnotations(data.aiData.source_details);
        }
      }
      return;
    }
    k8sApi
      .getK8sWorkload(1, 0, {
        accountId: data.accountId,
        namespaceName: data?.cloud_resourse?.meta?.namespace,
        workloadName: data?.cloud_resourse?.meta?.controller,
        // Exact: a partial match with limit 1 can hand back another workload
        // ("api" → "api-gateway") and show its spec as Current.
        exactNameMatch: true,
        workloadType: data?.cloud_resourse?.meta?.controllerKind || 'Deployment',
      })
      .then(async (res) => {
        const workloads = res?.data?.k8s_workloads || [];
        if (workloads && workloads.length == 1) {
          const workload = workloads[0];
          const annotations = workload.meta?.config?.annotations || {};
          const labels = workload.meta?.config?.labels || {};
          setGitOpsInfo(detectGitOpsManager(annotations, labels));
          // Set before the annotation branches below, each of which returns early.
          setWorkloadSpec(workload);

          // Check k8s annotations first
          const filteredKeys = Object.keys(annotations).filter((key) => key.startsWith(CI_PREFIX));
          if (filteredKeys && filteredKeys.length > 0) {
            const filteredObject = {};
            filteredKeys.forEach((key) => {
              filteredObject[key] = annotations[key];
            });
            setSelectedWorkloadAnnotations(filteredObject);
            return;
          }

          // Fallback to cloud_resource_attributes for manual CI configuration
          if (workload.cloud_resource_id) {
            try {
              const attributes = await k8sApi.getResourceAttributes(workload.cloud_resource_id);
              const manualConfig = {};
              attributes.forEach((attr) => {
                if (attr.name.startsWith(CI_PREFIX)) {
                  manualConfig[attr.name] = attr.value;
                }
              });
              if (Object.keys(manualConfig).length > 0) {
                setSelectedWorkloadAnnotations(manualConfig);
                return;
              }
            } catch (error) {
              console.error('Error fetching resource attributes:', error);
            }
          }

          setSelectedWorkloadAnnotations({});
        }
      });
  };

  const handleCreatePR = () => {
    if (!selectedGitIntegration) return;
    // Extract type and name from key (format: "github:name" or "gitlab:name")
    const [integrationType, ...nameParts] = selectedGitIntegration.split(':');
    const integrationName = nameParts.join(':'); // Handle names with colons
    setLoading(true);
    const data = JSON.parse(JSON.stringify(updatedData));
    data.raisePR = updateType == 'raise-pr';
    if (data?.memory?.request) {
      data.memory.request += 'Mi';
    }
    if (data?.memory?.limit) {
      data.memory.limit += 'Mi';
    }
    if (data?.cpu?.request) {
      data.cpu.request += '';
    }
    if (data?.cpu?.limit) {
      data.cpu.limit += '';
    }
    apiRecommendations
      .applyRecommendation(
        data.accountId,
        data.id,
        data,
        integrationType,
        {
          name: integrationName,
        },
        recommendationSource
      )
      .then((res) => {
        if (res?.errors && res?.errors.length > 0) {
          handleGithubPRSuccessOrFail(`Failed to create Pull request ${parseHttpResponseBodyMessage(res)}`, 'error', '');
        } else if (res?.data && res?.data.length > 0) {
          // The PR is raised asynchronously by the code agent — the initial
          // response's `pr` field is only populated once that finishes, so it's
          // normal for it to be absent here. Only attempt to parse it, and treat
          // its absence as "still in progress" rather than a failure.
          let prURL = '';
          const prRaw = res.data[0]?.pr;
          try {
            const parsedPr = typeof prRaw === 'string' ? JSON.parse(prRaw) : prRaw;
            prURL = parsedPr?.html_url || '';
          } catch {
            prURL = '';
          }
          handleGithubPRSuccessOrFail(
            prURL ? 'Pull request created successfully.' : 'Raising pull request — it will appear shortly.',
            'success',
            prURL
          );
          if (onClose) {
            onClose();
          }
        }
        setLoading(false);
      })
      .catch((error) => {
        handleGithubPRSuccessOrFail('failed to raise pull request', 'error', '');
        console.error(error);
        setLoading(false);
        if (onClose) {
          onClose();
        }
      });
  };

  const handleSelectedCpuAlgo = (buttonId, buttonValue) => {
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      algo: buttonId,
    }));
    setAlgo(buttonValue);
    updateDataBasedOnButtonValueForCpu(buttonValue);
  };

  const handleSelectedCpuBuffer = (buttonId, buttonValue) => {
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      buffer: buttonId,
    }));
    updateDataBasedOnButtonValueForCpu(buttonValue);
  };

  // (x || '').toFixed(2) throws when x is 0/falsy because ''.toFixed is undefined.
  // Return null instead so the cpu input renders as empty rather than crashing.
  const formatCpuRequest = (raw, multiplier = 1) => {
    const num = parseFloat(raw) * multiplier;
    return Number.isFinite(num) && num > 0 ? num.toFixed(2) : null;
  };

  const updateDataBasedOnButtonValueForCpu = (value) => {
    let selectedKey = algo;
    selectedKey = selectedKey?.toLowerCase();
    const multipliers = { 5: 1.05, 10: 1.1, 15: 1.15 };
    let nextRequest = null;
    switch (value) {
      case 'NBALGO':
        nextRequest = formatCpuRequest(additionalCpuInfo.nbalgo);
        break;
      case 'P99':
        nextRequest = formatCpuRequest(additionalCpuInfo.p99);
        break;
      case 'P97':
        nextRequest = formatCpuRequest(additionalCpuInfo.p97);
        break;
      case 'P95':
        nextRequest = formatCpuRequest(additionalCpuInfo.p95);
        break;
      case 5:
      case 10:
      case 15:
        nextRequest = formatCpuRequest(additionalCpuInfo[selectedKey], multipliers[value]);
        break;
      default:
        return;
    }
    setUpdatedData((prevData) => ({
      ...prevData,
      cpu: {
        ...prevData.cpu,
        request: nextRequest,
        limit: null,
      },
    }));
  };

  const parseMemValue = (val) => {
    const num = parseFloat(val);
    return isFinite(num) ? Math.round(num * 100) / 100 : null;
  };

  const updateDataBasedOnButtonValueForMemory = (value) => {
    const bufferMultiplier = { 0: 1, 5: 1.05, 10: 1.1, 15: 1.15, 20: 1.2 }[value];
    if (bufferMultiplier === undefined) return;

    setUpdatedData((prevData) => ({
      ...prevData,
      memory: {
        ...prevData.memory,
        request: parseMemValue(additionalMemInfo.nbalgoReq * bufferMultiplier),
        limit: parseMemValue(additionalMemInfo.nbalgoLimit * bufferMultiplier),
      },
    }));
  };

  const handleSelectedMemoryBuffer = (buttonId, buttonValue) => {
    setSelectedButtons(buttonId);
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      memBuffer: buttonId,
    }));
    updateDataBasedOnButtonValueForMemory(buttonValue);
  };

  const handleSelectedMemoryAlgo = (buttonId, buttonValue) => {
    setSelectedButtons(buttonId);
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      memory: buttonId,
    }));
    updateDataBasedOnButtonValueForMemory(buttonValue);
  };

  // Prefill Recommended from the spec-derived base when the caller had no signal
  // to prefill with (event evidence reports 0 for a workload whose
  // kube_pod_container_resource_* series are missing). The numbers match what the
  // toggles rendered next to the inputs already claim — memory +10% buffer, CPU
  // no buffer — so the form opens consistent with its own selection instead of
  // empty.
  useEffect(() => {
    if (!specAllocation) return;
    setUpdatedData((prevData) => {
      if (!prevData) return prevData;
      const next = { ...prevData };
      let changed = false;
      if (prevData.memory) {
        const memory = { ...prevData.memory };
        if (!knownValue(memory.request) && memNbalgoBase) {
          memory.request = parseMemValue(memNbalgoBase * 1.1);
          changed = true;
        }
        if (!knownValue(memory.limit) && memNbalgoLimit) {
          memory.limit = parseMemValue(memNbalgoLimit * 1.1);
          changed = true;
        }
        next.memory = memory;
      }
      if (prevData.cpu && !knownValue(prevData.cpu.request) && cpuNbalgoBase) {
        next.cpu = { ...prevData.cpu, request: formatCpuRequest(cpuNbalgoBase) };
        changed = true;
      }
      return changed ? next : prevData;
    });
    // `data` is a dependency because the effect above it re-seeds updatedData from
    // `data` whenever the caller re-renders; without it the prefill would be lost.
  }, [specAllocation, data]);

  const handleInputChange = (value, type, type1) => {
    if (type == 'cpu' && type1 == 'request') {
      setUpdatedData((prevData) => ({
        ...prevData,
        cpu: {
          ...prevData.cpu,
          request: value,
        },
      }));
    } else if (type == 'cpu' && type1 == 'limit') {
      setUpdatedData((prevData) => ({
        ...prevData,
        cpu: {
          ...prevData.cpu,
          limit: value,
        },
      }));
    } else if (type == 'mem' && type1 == 'request') {
      setUpdatedData((prevData) => ({
        ...prevData,
        memory: {
          ...prevData.memory,
          request: value,
        },
      }));
    } else if (type == 'mem' && type1 == 'limit') {
      setUpdatedData((prevData) => ({
        ...prevData,
        memory: {
          ...prevData.memory,
          limit: value,
        },
      }));
    }
  };

  const renderDiffData = () => {
    if (data?.aiData?.source_updates?.gitDiff) {
      const analysis = '```\n' + data.aiData.source_updates.gitDiff + '\n```';
      return <MarkDowns data={analysis} />;
    }
  };

  // The code-fix Raise PR replays the fix stored by the event's log analysis
  // (aiData.source_updates.gitDiff). Until that analysis is COMPLETED and
  // actually stored a diff, the backend rejects the request — so block
  // submission instead of letting the user fire a doomed call.
  //
  // Resource tuning is the other event-sourced Raise PR and has nothing to do
  // with log analysis: it edits requests/limits in the GitOps source through the
  // code agent. Gating every event-sourced Raise PR on the analysis left resource
  // tuning permanently disabled — it carries no aiData, so the status read as
  // "still running" forever.
  //
  // The split is card_id, mirroring how the backend routes the two apart
  // (ApplyEventResolution excludes MemoryAllocationCard from the code-fix
  // branch). Keyed the same way on both sides, a card can't be routed to the
  // code-fix path by the backend while the UI treats it as resource tuning.
  const isCodeFixPR = recommendationSource === 'event' && data?.card_id !== 'MemoryAllocationCard';
  // The investigation as a whole can still read IN_PROGRESS while a later stage runs, long after the
  // log-analysis stage produced the diff this action replays — so the overall status blocks a PR
  // that is ready to raise. Gate on that one stage, falling back to the overall status only for
  // responses old enough to carry no task_statuses.
  const logAnalysisStatus = data?.aiData?.task_statuses?.log_analysis;
  const eventAnalysisStatus = typeof logAnalysisStatus === 'string' ? logAnalysisStatus.toLowerCase() : data?.aiData?.status?.toLowerCase();
  const eventAnalysisCompleted = eventAnalysisStatus === 'completed';
  const eventAnalysisFailed = eventAnalysisStatus === 'failed';
  const eventAnalysisHasDiff = Boolean(data?.aiData?.source_updates?.gitDiff);
  const eventAnalysisReady = !isCodeFixPR || (eventAnalysisCompleted && eventAnalysisHasDiff);

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title || 'Update Resources'}
      width='lg'
      sx={{
        '& .MuiPaper-root': {
          maxWidth: '1010px',
          '& .MuiDialogContent-root': {
            padding: 'var(--ds-space-4) var(--ds-space-6)',
          },
        },
      }}
    >
      {/* One row: which container, then how to apply — the modal has no height to spare. */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: containerChoices.length > 0 ? 'space-between' : 'center',
          flexWrap: 'wrap',
          gap: ds.space[4],
          my: 2,
        }}
      >
        {containerChoices.length > 0 && (
          <Box sx={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: ds.space[3] }}>
            <Typography sx={{ fontSize: ds.text.bodyLg, color: ds.gray[700] }}>Container</Typography>
            {containerChoices.length > 1 && onContainerChange && containerChoices.length <= MAX_CONTAINER_BUTTONS ? (
              // Every container visible at once, the one that was killed marked.
              <ToggleButtonGroup
                size='small'
                exclusive
                value={tunedContainer}
                onChange={(_, value) => value && onContainerChange(value)}
                aria-label='Container'
                sx={{ button: { textTransform: 'none', gap: ds.space[2], height: '32px' } }}
              >
                {containerChoices.map((name) => (
                  <ToggleButton key={name} value={name}>
                    {name}
                    {name === alertContainer && (
                      <Label tone='critical' size='sm' textTransform='none'>
                        Out of memory
                      </Label>
                    )}
                  </ToggleButton>
                ))}
              </ToggleButtonGroup>
            ) : containerChoices.length > 1 && onContainerChange ? (
              <Select
                label='Container'
                value={tunedContainer || ''}
                minWidth='250px'
                clearable={false}
                options={containerChoices.map((name) => ({ value: name, label: name === alertContainer ? `${name} (out of memory)` : name }))}
                onChange={(v) => onContainerChange(v)}
              />
            ) : (
              <>
                <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{tunedContainer}</Typography>
                {tunedContainer === alertContainer && (
                  <Label tone='critical' size='sm' textTransform='none'>
                    Out of memory
                  </Label>
                )}
              </>
            )}
          </Box>
        )}
        <ToggleButtonGroup
          size='small'
          color='primary'
          value={updateType}
          exclusive
          onChange={(e) => {
            if (e.target.value) {
              setUpdateType(e.target.value);
            }
          }}
          aria-label='Platform'
          sx={{
            button: {
              minWidth: '120px',
              height: '36px',
              textTransform: 'inherit',
            },
            img: {
              filter: 'brightness(0) saturate(100%) invert(74%) sepia(24%) saturate(1%) hue-rotate(314deg) brightness(82%) contrast(88%)',
            },
            '& .Mui-selected': {
              color: `${ds.blue[500]} !important`,
              backgroundColor: ds.blue[100],
              img: {
                filter: 'brightness(0) saturate(100%) invert(45%) sepia(23%) saturate(3237%) hue-rotate(195deg) brightness(98%) contrast(98%)',
              },
            },
          }}
        >
          {updateResourceType != 'raise-pr' ? (
            <ToggleButton value='resourceChange' sx={{ gap: 'var(--ds-space-2)', color: ds.gray[400] }}>
              <SafeIcon src={UpdateIcon} alt='Update' />
              Update
            </ToggleButton>
          ) : null}
          <ToggleButton value='raise-pr' sx={{ gap: 'var(--ds-space-2)', color: ds.gray[400] }}>
            <SafeIcon src={PrOpenIcon} alt='Raise PR' />
            Raise PR
          </ToggleButton>
        </ToggleButtonGroup>
      </Box>

      {updateType === 'raise-pr' &&
        (isGitIntegrationsLoading || filteredGitIntegrations.length > 0 ? (
          <>
            <Grid container xs={12} gap={3}>
              <Select
                label={'Git Integration'}
                value={selectedGitIntegration || ''}
                minWidth={'250px'}
                options={filteredGitIntegrations.map((i) => ({ value: i.key, label: i.label }))}
                onChange={(v) => setSelectedGitIntegration(v)}
                loading={isGitIntegrationsLoading}
                disabled={!isCodeFixPR && Object.keys(selectedWorkloadAnnotations).length == 0}
              />
            </Grid>
            {isCodeFixPR ? (
              eventAnalysisReady ? (
                <Typography sx={{ mt: 'var(--ds-space-4)', mb: 'var(--ds-space-5)', fontSize: 'var(--ds-text-body-lg)', color: ds.gray[700] }}>
                  A pull request will be raised against the mapped source repository with the generated fix.
                </Typography>
              ) : (
                <Typography
                  data-testid='raise-pr-analysis-gate'
                  sx={{ mt: 'var(--ds-space-4)', mb: 'var(--ds-space-5)', fontSize: 'var(--ds-text-body-lg)', color: ds.amber[700] }}
                >
                  {eventAnalysisFailed
                    ? 'Log analysis failed — please re-run the analysis to generate a fix.'
                    : !eventAnalysisCompleted
                    ? 'Log analysis is still running — Raise PR will be available once it completes.'
                    : 'No code fix was generated by the analysis; re-run the analysis to raise a PR.'}
                </Typography>
              )
            ) : selectedWorkloadAnnotations && Object.keys(selectedWorkloadAnnotations).length > 0 ? (
              <ul>
                {Object.entries(selectedWorkloadAnnotations).map(([key, value]) => (
                  <li key={key}>
                    <strong>{key}:</strong> {value}
                  </li>
                ))}
              </ul>
            ) : (
              <SummaryBlock
                hideTitle
                sx={{
                  backgroundColor: ds.background[200],
                  border: `0.5px solid ${ds.brand[300]} !important`,
                  mt: 'var(--ds-space-4)',
                  mb: 'var(--ds-space-7)',
                  ul: {
                    pl: 'var(--ds-space-5)',
                  },
                  'ul li': {
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    fontSize: 'var(--ds-text-body-lg)',
                  },
                }}
              >
                <Typography sx={{ fontWeight: 'var(--ds-font-weight-regular)', fontSize: 'var(--ds-text-body-lg)', color: ds.gray[700] }}>
                  There are no annotations configured at Deployment. To make this functionality work, the following annotations are required at
                  Deployment:
                </Typography>
                <ul>
                  {requestAnnotations.map((value) => (
                    <li key={value}>
                      <strong>{value}</strong>
                    </li>
                  ))}
                </ul>
              </SummaryBlock>
            )}
            {renderDiffData()}
            <Box
              sx={{
                borderTop: `0.5px solid ${ds.gray[200]}`,
                button: {
                  minWidth: '140px',
                },
              }}
            >
              <Grid
                container
                sx={{
                  justifyContent: 'end',
                  mb: 2,
                  mt: 2,
                  button: {
                    minWidth: '140px',
                  },
                }}
                gap={1}
              >
                <Grid item>
                  <Button size='md' tone='secondary' onClick={onClose}>
                    Cancel
                  </Button>
                </Grid>
                <Grid item>
                  <Button
                    size='md'
                    disabled={
                      !selectedGitIntegration || (!isCodeFixPR && !Object.keys(selectedWorkloadAnnotations).length) || !eventAnalysisReady || loading
                    }
                    onClick={handleCreatePR}
                  >
                    Save
                  </Button>
                </Grid>
              </Grid>
            </Box>
          </>
        ) : (
          <Typography sx={{ marginBottom: 'var(--ds-space-4)' }}>
            No Git integration is configured.{' '}
            <Link href={'/accounts/account-form?cloudProvider=GITHUB'}>Please configure a GitHub or GitLab integration</Link>
          </Typography>
        ))}
      {updateType === 'resourceChange' && (
        <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='left' my='5px' py='1px'>
          {gitOpsInfo.manager && (
            <Box
              sx={{
                backgroundColor: ds.amber[100],
                border: `0.5px solid ${ds.amber[300]}`,
                p: 'var(--ds-space-4)',
                mb: 'var(--ds-space-5)',
              }}
            >
              <Typography sx={{ fontSize: 'var(--ds-text-body-md)', color: ds.amber[700] }}>
                This workload is managed by <strong>{gitOpsInfo.manager}</strong>. “Update” patches the running pods directly, so the change is
                reverted on the next sync.{' '}
                {gitOpsInfo.gitRepo ? '“Raise PR” persists it in Git instead.' : 'Persist changes through your GitOps source instead.'}
              </Typography>
            </Box>
          )}
          <Box sx={{ pb: 'var(--ds-space-6)' }}>
            <Box sx={{ display: 'flex', gap: 'var(--ds-space-4)', marginTop: 'var(--ds-space-4)' }}>
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-5)' }}>
                <AutoOptimizeForm
                  handleUpdateData={handleUpdateData}
                  handleSelectedAlgo={handleSelectedCpuAlgo}
                  handleSelectedBuffer={handleSelectedCpuBuffer}
                  handleSelectedMemoryBuffer={handleSelectedMemoryBuffer}
                  handleSelectedMemoryAlgo={handleSelectedMemoryAlgo}
                  data={updatedData}
                  currentData={allocatedData}
                  activeButton={selectedButtons}
                  handleInputChange={handleInputChange}
                  currentMemoryExtra={memoryUsage && <MemoryUsageFacts usage={memoryUsage} limitMb={currentMemLimit} />}
                  recommendedMemoryExtra={
                    memoryUsage && (
                      <MemoryHeadroomCheck usage={memoryUsage} request={updatedData?.memory?.request} limit={updatedData?.memory?.limit} />
                    )
                  }
                />
              </Box>
            </Box>
          </Box>

          <Box sx={{ borderTop: `0.5px solid ${ds.gray[200]}` }}>
            <Box
              display={'flex'}
              alignItems='center'
              justifyContent={'flex-end'}
              gap={ds.space[3]}
              my={2}
              mr='0px'
              sx={{
                button: {
                  minWidth: '140px',
                },
              }}
            >
              <Button size='md' tone='secondary' onClick={onClose}>
                Cancel
              </Button>
              <Button size='md' type='submit' onClick={() => submitRecommendation()} disabled={loading} loading={loading}>
                Update
              </Button>
            </Box>
          </Box>
        </Box>
      )}
      <Modal open={!!driftConfirm} onClose={() => setDriftConfirm(null)} title='Confirm large resource change' width='sm'>
        <Box sx={{ p: 'var(--ds-space-4)' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-body-md)', color: ds.gray[700], mb: 'var(--ds-space-4)' }}>
            These values deviate sharply from the current allocation. Confirm this is intended:
          </Typography>
          <Box component='ul' sx={{ pl: 'var(--ds-space-5)', mb: 'var(--ds-space-5)' }}>
            {(driftConfirm || []).map((r) => (
              <li key={r.label}>
                <strong>{r.label}:</strong> {r.from} → {r.to} ({r.pct > 0 ? '+' : ''}
                {r.pct}%)
              </li>
            ))}
          </Box>
          <Box display='flex' justifyContent='flex-end' gap={ds.space[3]}>
            <Button size='md' tone='secondary' onClick={() => setDriftConfirm(null)}>
              Cancel
            </Button>
            <Button
              size='md'
              onClick={() => {
                setDriftConfirm(null);
                submitRecommendation(true);
              }}
            >
              Apply anyway
            </Button>
          </Box>
        </Box>
      </Modal>
    </Modal>
  );
};

KubernetesRightSizingPopupForm.propTypes = {
  open: PropTypes.bool,
  title: PropTypes.string,
  onClose: PropTypes.func,
  onSuccess: PropTypes.func,
  onFailure: PropTypes.func,
  data: PropTypes.object,
  recommendationSource: PropTypes.string,
  containerOptions: PropTypes.arrayOf(PropTypes.string),
  onContainerChange: PropTypes.func,
  alertContainer: PropTypes.string,
};
export default KubernetesRightSizingPopupForm;
