import { Grid, ToggleButton, ToggleButtonGroup, Typography } from '@mui/material';
import React, { useEffect, useState, useMemo } from 'react';
import Box from '@mui/material/Box';
import apiIntegrations from '@api1/integrations';
import apiRecommendations from '@api1/recommendation';
import { Modal } from '@components1/common/modal';
import CustomDropdown from '@components1/common/CustomDropdown';
import { snackbar } from '@components1/common/snackbarService';
import { ANNOTATIONS, CI_PREFIX, CI_REQUEST_ANNOTATIONS } from '@lib/annotationKeys';
import k8sApi from '@api1/kubernetes';
import Link from 'next/link';
import AutoOptimizeForm from '@components1/autopilot/form/AutoOptimizeVerticalRightSizingForm';

import PropTypes from 'prop-types';
import CustomButton from '@components1/common/NewCustomButton';
import { PrOpenIcon, UpdateIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import MarkDowns from '@components1/common/MarkDowns';
import { colors } from 'src/utils/colors';
import { parseHttpResponseBodyMessage } from 'src/utils/common';

// Helper to detect git provider from repo URL
const detectGitProvider = (repoUrl) => {
  if (!repoUrl) return null;
  const url = repoUrl.toLowerCase();
  if (url.includes('github.com')) return 'github';
  if (url.includes('gitlab')) return 'gitlab';
  return null;
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

  const parsedOldRequest =
    data?.memory?.oldRequest && typeof data?.memory?.oldRequest === 'string'
      ? parseFloat(data.memory.oldRequest.split(' ')[0])
      : data?.memory?.oldRequest;
  const parsedOldLimit =
    data?.memory?.oldLimit && typeof data?.memory?.oldLimit === 'string' ? parseFloat(data.memory.oldLimit.split(' ')[0]) : data?.memory?.oldLimit;

  // nbalgoBase is the unbuffered base for algo/buffer toggles. Prefer it over
  // oldRequest/oldLimit so a workload with zero current requests/limits but
  // real observed usage still drives the toggle math (without it,
  // `parsedOldRequest || ''` collapses 0 to '' and toggles clear the input).
  const memNbalgoBase = data?.memory?.nbalgoBase ?? parsedOldRequest;
  const memNbalgoLimit = data?.memory?.nbalgoBase ?? parsedOldLimit;
  const cpuNbalgoBase = data?.cpu?.nbalgoBase ?? data?.cpu?.oldRequest;

  // `|| undefined` (not `|| ''`) so `'' * bufferMultiplier === 0` can't sneak a
  // zero recommendation into the input. With undefined, `undefined * mult` is
  // NaN, which `parseMemValue` returns as null and the box stays empty.
  const additionalMemInfo = {
    req: parsedOldRequest || '',
    limit: parsedOldLimit || '',
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
      request: data?.cpu?.oldRequest || '',
      limit: data?.cpu?.oldLimit || '',
    },
    memory: {
      request: parsedOldRequest || '',
      limit: parsedOldLimit || '',
    },
  };

  const listGitConfigurations = () => {
    setIsGitIntegrationsLoading(true);
    Promise.all([
      apiIntegrations.listTicketConfigurationsByTool({ status: 'enabled', tool: 'github' }),
      apiIntegrations.listTicketConfigurationsByTool({ status: 'enabled', tool: 'gitlab' }),
    ])
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
    if (open && recommendationSource != 'event') {
      listGitConfigurations();
      getWorkloadDeploymentForSelectedRightSize();
    } else {
      // Clean up state when modal closes
      setAllGitIntegrations([]);
      setSelectedGitIntegration('');
      setSelectedWorkloadAnnotations({});
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

  const submitRecommendation = () => {
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
        workloadType: data?.cloud_resourse?.meta?.controllerKind || 'Deployment',
      })
      .then(async (res) => {
        const workloads = res?.data?.k8s_workloads || [];
        if (workloads && workloads.length == 1) {
          const workload = workloads[0];
          const annotations = workload.meta?.config?.annotations || {};

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
          const parsedJson = JSON.parse(res?.data[0].pr);
          const prURL = parsedJson.html_url;
          handleGithubPRSuccessOrFail('Pull request created successfully.', 'success', prURL);
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
        height: '700px',
      }}
    >
      <Box
        display='flex'
        flexDirection={'column'}
        alignItems={'center'}
        my={2}
        sx={{
          button: {
            minWidth: '120px',
          },
        }}
      >
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
              color: `${colors.text.primary} !important`,
              backgroundColor: colors.background.toggle,
              img: {
                filter: 'brightness(0) saturate(100%) invert(45%) sepia(23%) saturate(3237%) hue-rotate(195deg) brightness(98%) contrast(98%)',
              },
            },
          }}
        >
          {updateResourceType != 'raise-pr' ? (
            <ToggleButton value='resourceChange' sx={{ gap: 'var(--ds-space-2)', color: colors.text.secondaryDark }}>
              <SafeIcon src={UpdateIcon} alt='Update' />
              Update
            </ToggleButton>
          ) : null}
          <ToggleButton value='raise-pr' sx={{ gap: 'var(--ds-space-2)', color: colors.text.secondaryDark }}>
            <SafeIcon src={PrOpenIcon} alt='Raise PR' />
            Raise PR
          </ToggleButton>
        </ToggleButtonGroup>
      </Box>

      {updateType === 'raise-pr' &&
        (isGitIntegrationsLoading || filteredGitIntegrations.length > 0 ? (
          <>
            <Grid container xs={12} gap={3}>
              <CustomDropdown
                label={'Git Integration'}
                value={filteredGitIntegrations.find((i) => i.key === selectedGitIntegration)?.label || ''}
                minWidth={'250px'}
                options={filteredGitIntegrations.map((i) => i.label)}
                onChange={(e) => {
                  const selected = filteredGitIntegrations.find((i) => i.label === e.target.value);
                  if (selected) setSelectedGitIntegration(selected.key);
                }}
                showNormalField={true}
                isLoading={isGitIntegrationsLoading}
                isDisabled={Object.keys(selectedWorkloadAnnotations).length == 0}
              />
            </Grid>
            {selectedWorkloadAnnotations && Object.keys(selectedWorkloadAnnotations).length > 0 ? (
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
                  backgroundColor: colors.button.secondaryHover,
                  border: `0.5px solid ${colors.border.summaryBlock} !important`,
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
                <Typography sx={{ fontWeight: 'var(--ds-font-weight-regular)', fontSize: 'var(--ds-text-body-lg)', color: colors.text.secondary }}>
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
                borderTop: `0.5px solid ${colors.border.vertical}`,
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
                  <CustomButton text='Cancel' size='Medium' variant='secondary' onClick={onClose} />
                </Grid>
                <Grid item>
                  <CustomButton
                    size='Medium'
                    disabled={!selectedGitIntegration || !Object.keys(selectedWorkloadAnnotations).length || loading}
                    text='Save'
                    onClick={handleCreatePR}
                  />
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
                />
              </Box>
            </Box>
          </Box>

          <Box sx={{ borderTop: `0.5px solid ${colors.border.vertical}` }}>
            <Box
              display={'flex'}
              alignItems='center'
              justifyContent={'flex-end'}
              gap={'12px'}
              my={2}
              mr='0px'
              sx={{
                button: {
                  minWidth: '140px',
                },
              }}
            >
              <CustomButton size='Medium' text='Cancel' variant='secondary' onClick={onClose} />
              <CustomButton size='Medium' type='submit' text={'Update'} onClick={() => submitRecommendation()} disabled={loading} loading={loading} />
            </Box>
          </Box>
        </Box>
      )}
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
};
export default KubernetesRightSizingPopupForm;
