import React, { useEffect, useState } from 'react';
import { Box, FormControlLabel, Grid, InputAdornment, Switch, TextField, Typography } from '@mui/material';
import VerticalAutopPilotForm from './AutoOptimizeVerticalRightSizingForm';
import ButtonTabs from '@components1/common/ButtonTabs';
import CustomDropdown from '@components1/common/CustomDropdown';
import { formatMemory } from '@lib/formatter';
import apiAutoPlaybook from '@api1/autoPlaybook';
import apiAccount from '@api1/account';
import DateTimeRangePicker from '@components1/k8s/common/DateTimeRangePicker';
import dayjs, { type Dayjs } from 'dayjs';
import apiIntegrations from '@api1/integrations';
import k8sApi from '@api1/kubernetes';
import TextWithBorder from '@components1/common/TextWithBorder';
import { inputSx } from '@data/themes/inputField';
import PropTypes from 'prop-types';
import buttonConfiguration from '@lib/buttonConfiguration';
import { useData } from '@context/DataContext';
import ActionButtons from './AutoOptimizeActionButtons';
import NotificationForm from './AutoOptimizeNotificationForm';
import apiAutoPilot from '@api1/autoPilot';
import { colors } from 'src/utils/colors';
import RunbookTargetResource from '@components1/runbooks/RunbookTargetResource';
import { snackbar } from '@components1/common/snackbarService';
import CustomTooltip from '@components1/common/CustomTooltip';
import { infoIcon } from '@assets';
import TicketFormSection from '@components1/tickets/TicketFormSection';
import SafeIcon from '@components1/common/SafeIcon';

interface TimeHeaderProps {
  title: string;
  subtitle: string;
}

const TimeHeader = ({ title, subtitle }: TimeHeaderProps) => (
  <Box sx={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
    <Typography sx={{ color: colors.text.secondary, fontSize: '14px', fontWeight: 400 }}>{title}</Typography>
    <Typography sx={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}>{subtitle}</Typography>
  </Box>
);

TimeHeader.propTypes = {
  title: PropTypes.string.isRequired,
  subtitle: PropTypes.string.isRequired,
};

interface UpdatedDataType {
  cpu: {
    request: string | null;
    limit: string | null;
  };
  memory: {
    request: string | null;
    limit: string | null;
  };
}

const VerticalAutoOptimizeSingleConfiguration = ({
  autoOptimizeData,
  closeAutoPilotSingleConfigModal,
  msTeamsData,
  isMsTeamsLoading,
  googleChannelList,
  isGoogleChannelsLoading,
  listAutoPilot,
  data,
  currentData,
  additionalInfoCPUAndMem = {},
  _isLoading,
  setIsLoading,
  reviewAutoOptimize = false,
  approvalData = {},
}: {
  autoOptimizeData: any;
  closeAutoPilotSingleConfigModal: (success: boolean, status?: string) => void;
  msTeamsData: { label: string; value: string; channels?: { name: string; id: string }[] }[];
  isMsTeamsLoading: boolean;
  googleChannelList: { label: string; value: string }[];
  isGoogleChannelsLoading: boolean;
  listAutoPilot?: () => void;
  data?: any;
  currentData: any;
  additionalInfoCPUAndMem?: any;
  _isLoading?: boolean;
  setIsLoading: (loading: boolean) => void;
  reviewAutoOptimize?: boolean;
  approvalData?: any;
}) => {
  const [updatedData, setUpdatedData] = useState(data || { cpu: {}, memory: {} });
  const [allocatedData, setAllocatedData] = useState(
    currentData ?? {
      cpu: {
        request: '',
        limit: '',
      },
      memory: {
        request: '',
        limit: '',
      },
    }
  );
  const [isDailyTimeFrameOpen, setIsDailyTimeFrameOpen] = useState(false);
  const [isWeeklyTimeFrameOpen, setIsWeeklyTimeFrameOpen] = useState(false);
  const [cronExpression, setCronExpression] = useState(autoOptimizeData?.schedule_time || '');
  const [algo, setAlgo] = useState('NBALGO');
  const [buffer, setBuffer] = useState(0);
  const [memBuffer, setMemBuffer] = useState(0);
  const [activeButton, setActiveButton] = useState<string | number>('');
  const [selectedButtons, setSelectedButtons] = useState<{
    algo: string | number | undefined;
    buffer: string | number | undefined;
    memory: string | number | undefined;
    memBuffer: string | number | undefined;
  }>({
    algo: buttonConfiguration?.buttonConfigs?.buttonsAlgo.find((button) => button.value === autoOptimizeData?.rule?.cpu?.algo)?.id || 0,
    buffer: buttonConfiguration?.buttonConfigs?.buttonsBuffer.find((button) => button.value === autoOptimizeData?.rule?.cpu?.buffer_pct)?.id || 0,
    memory: buttonConfiguration?.buttonConfigs?.buttonMemoryAlgo.find((button) => button.value === autoOptimizeData?.rule?.memory?.algo)?.id || 0,
    memBuffer:
      buttonConfiguration?.buttonConfigs?.buttonMemoryBuffer.find((button) => button.value === autoOptimizeData?.rule?.memory?.buffer_pct)?.id || 0,
  });
  const [selectedDate, setSelectedDate] = useState<{
    startDate: Dayjs | null;
    endDate: Dayjs | null;
  }>({
    startDate: autoOptimizeData?.start_at ? dayjs(autoOptimizeData?.start_at) : dayjs(),
    endDate: autoOptimizeData?.end_at ? dayjs(autoOptimizeData?.end_at) : null,
  });
  const [dryRunData, setDryRunData] = useState(autoOptimizeData?.status === 'Dryrun');
  const [githubRepoName, setGithubRepoName] = useState(autoOptimizeData?.attributes?.git_ops_config?.repository_name ?? '');
  const [notificationData, setNotificationData] = useState({
    email: autoOptimizeData?.notification?.email?.enabled || false,
    slack: autoOptimizeData?.notification?.slack?.enabled || false,
    teams: autoOptimizeData?.notification?.ms_teams?.enabled || false,
    google_chat: autoOptimizeData?.notification?.google_chat?.enabled || false,
    channelId: autoOptimizeData?.notification?.slack?.channel_id || '',
    teamsId: autoOptimizeData?.notification?.ms_teams?.team_id || '',
    msChannelId: autoOptimizeData?.notification?.ms_teams?.channel_id || '',
    gChatChannelId: autoOptimizeData?.notification?.google_chat?.channel_id || '',
    gChatChannelName: autoOptimizeData?.notification?.google_chat?.channel_name || '',
  });
  const [slackChannelList, setSlackChannelList] = useState([]);
  const [slackChannelName, setSlackChannelName] = useState(autoOptimizeData?.notification?.slack?.channel_id || '');
  const [isLoadingSlackChannels, setIsLoadingSlackChannels] = useState(false);
  const [additionalCpuInfo, setAdditionalCpuInfo] = useState(additionalInfoCPUAndMem?.cpuInfo ?? {});
  const [additionalMemInfo, setAdditionalMemInfo] = useState(additionalInfoCPUAndMem?.memInfo ?? {});
  const [autoPilotData, setAutoPilotData] = useState(autoOptimizeData);
  const [githubRepos, setGithubRepos] = useState([]);
  const [msTeamName, setMsTeamName] = useState(autoOptimizeData?.notification?.ms_teams?.team_id || '');
  const [msChannelListOption, setMsChannelListOption] = useState<{ label: string; value: string }[]>([]);
  const [msChannelName, setMSChannelName] = useState(autoOptimizeData?.notification?.ms_teams?.channel_id || '');
  const [googleChatChannelName, setGoogleChatChannelName] = useState(autoOptimizeData?.notification?.google_chat?.channel_id || '');
  const [displayErrorsDesc, setDisplayErrorsDesc] = useState({
    notification: {
      teams: '',
      slack: '',
      google_chat: '',
    },
    gitops: {
      message: '',
    },
    reviewComment: '',
    jira: {
      formErrors: {},
    },
  });
  const [selectedWorkloadAnnotations, setSelectedWorkloadAnnotations] = useState({});
  const [minCPU, setMinCPU] = useState(autoOptimizeData?.rule?.cpu?.trigger?.change_pct ?? 10);
  const [maxCPU, setMaxCPU] = useState(autoOptimizeData?.rule?.cpu?.trigger?.max_change_pct ?? 100);
  const [minMemory, setMinMemory] = useState(autoOptimizeData?.rule?.memory?.trigger?.change_pct ?? 10);
  const [maxMemory, setMaxMemory] = useState(autoOptimizeData?.rule?.memory?.trigger?.max_change_pct ?? 100);
  const [raisePullRequest, setRaisePullRequest] = useState(autoOptimizeData?.attributes?.git_ops_config?.enabled || false);
  const { selectedCluster } = useData();
  const requestAnnotations = ['ci.nudgebee.com/git.hash', 'ci.nudgebee.com/git.repo', 'ci.nudgebee.com/helm.values.filePath'];
  const [reviewComment, setReviewComment] = useState('');
  const [selectedApplications, setSelectedApplications] = useState<any[]>([]);
  const [selectedNamespaces, setSelectedNamespaces] = useState([]);
  const [createTicketData, setCreateTicketData] = useState<any>({});
  const [createTicket, setCreateTicket] = useState(false);

  useEffect(() => {
    const allAppsFromProps =
      autoOptimizeData?.auto_optimize_resource_maps?.map((m: any) => m.resource_identifier) || autoOptimizeData?.resource_filter || [];
    setSelectedApplications(allAppsFromProps);

    const distinctNamespaces = allAppsFromProps.reduce((acc: string[], app: any) => {
      if (!acc.includes(app.namespace)) {
        acc.push(app.namespace);
      }
      return acc;
    }, []);
    setSelectedNamespaces(distinctNamespaces);
    setCreateTicketData(autoOptimizeData?.attributes?.ticket_config || {});
    setCreateTicket(autoOptimizeData?.attributes?.ticket_config?.enabled);
  }, [autoOptimizeData]);

  useEffect(() => {
    if (approvalData?.reviewer_comments) {
      setReviewComment(approvalData.reviewer_comments);
    }

    if (msTeamName) {
      filterChannelsName(msTeamName);
    }
  }, [msTeamName, msChannelName, msTeamsData]);

  const filterChannelsName = (_value: any) => {
    const channelValue = _value;
    const selectedMsTeamsData = msTeamsData?.find((item) => item?.value === channelValue);
    if (selectedMsTeamsData) {
      setMsChannelListOption(
        selectedMsTeamsData?.channels?.map((channel: { name: string; id: string }) => ({ label: channel?.name, value: channel?.id })) || []
      );
    } else {
      setMsChannelListOption([]);
    }
  };

  useEffect(() => {
    if (!autoOptimizeData?.id) {
      return;
    }

    const item = autoOptimizeData.data;

    if (!item) {
      return;
    }

    let cpuRecommLimit = '-';
    let cpuRecommReq = '-';
    let memoryRecommReq = '-';
    let memoryRecommLimit = '-';
    let cpuAllocatedReq = '-';
    let cpuAllocatedLimit = '-';
    let memoAllocatedReq = '-';
    let memoAllocatedLimit = '-';
    const containerName = Object.keys(item.recommendation)[0];
    for (const r of item.recommendation[containerName]) {
      if (r.resource === 'cpu') {
        cpuRecommReq = r.recommended?.request;
        cpuRecommLimit = r.recommended?.limit;
        cpuAllocatedReq = r.allocated?.request;
        cpuAllocatedLimit = r.allocated?.limit;
        data['cpuDesc'] = r.description;
      } else if (r.resource === 'memory') {
        memoryRecommReq = formatMemory(r.recommended?.request, 'bytes', 'mb', false);
        memoryRecommLimit = formatMemory(r.recommended?.limit, 'bytes', 'mb', false);
        memoAllocatedReq = formatMemory(r.allocated?.request, 'bytes', 'mb', false);
        memoAllocatedLimit = formatMemory(r.allocated?.limit, 'bytes', 'mb', false);
        data['memoryDesc'] = r.description;
      }
    }
    const addinfoCpu = item?.recommendation[containerName][0]?.add_info;
    const nbalgocpu = item?.recommendation[containerName][0]?.recommended;
    const addinfoMemo = item?.recommendation[containerName][1]?.add_info;
    const nbalgoMemo = item?.recommendation[containerName][1]?.recommended;
    setAdditionalCpuInfo({
      p99: addinfoCpu?.cpu_percentile_99,
      p97: addinfoCpu?.cpu_percentile_97,
      p95: addinfoCpu?.cpu_percentile_95,
      nbalgo: nbalgocpu?.request,
    });
    setAdditionalMemInfo({
      limit: addinfoMemo?.actual_recommended_limit,
      req: addinfoMemo?.actual_recommended_request,
      nbalgoReq: nbalgoMemo?.request,
      nbalgoLimit: nbalgoMemo?.limit,
    });
    setAllocatedData({
      cpu: {
        request: cpuAllocatedReq,
        limit: cpuAllocatedLimit,
      },
      memory: {
        request: memoAllocatedReq,
        limit: memoAllocatedLimit,
      },
    });
    setUpdatedData({
      cpu: {
        request: cpuRecommReq,
        limit: cpuRecommLimit,
      },
      memory: {
        request: memoryRecommReq,
        limit: memoryRecommLimit,
      },
    });
    setAutoPilotData({
      ...autoOptimizeData,
    });
  }, [autoOptimizeData?.id]);

  useEffect(() => {
    const fetchSlackChannels = async () => {
      setIsLoadingSlackChannels(true);
      try {
        const platforms = 'slack';
        const res: any = await apiAccount.getNotificationChannelList(platforms);
        const channelOptions = res?.data?.data?.map((_item: any) => ({ label: _item.name, value: _item.id })) || [];
        setSlackChannelList(channelOptions);
      } finally {
        setIsLoadingSlackChannels(false);
      }
    };

    fetchSlackChannels();
    listGithubConfigurations();
  }, []);

  useEffect(() => {
    getWorkloadDeploymentForSelectedRightSize(autoPilotData);
  }, [autoPilotData]);

  // Also check when selectedApplications changes (for Auto Optimize page)
  useEffect(() => {
    if (selectedApplications && selectedApplications.length > 0) {
      const firstApp = selectedApplications[0];
      if (firstApp?.namespace && firstApp?.name) {
        fetchWorkloadAnnotations(firstApp.namespace, firstApp.name, firstApp.kind || 'Deployment');
      }
    }
  }, [selectedApplications]);

  const fetchWorkloadAnnotations = async (namespace: string, name: string, kind: string) => {
    try {
      const res = await k8sApi.getK8sWorkload(1, 0, {
        accountId: selectedCluster?.value,
        namespaceName: namespace,
        workloadName: name,
        workloadType: kind,
      });

      const workloads = res?.data?.k8s_workloads || [];
      if (workloads && workloads.length == 1) {
        const workload = workloads[0];
        const annotations = workload.meta?.config?.annotations || {};

        // Check k8s annotations first
        const filteredKeys = Object.keys(annotations).filter((_key) => _key.startsWith('ci.nudgebee.com'));
        if (filteredKeys && filteredKeys.length > 0) {
          const filteredObject: { [key: string]: any } = {};
          filteredKeys.forEach((_key) => {
            filteredObject[_key] = annotations[_key];
          });
          setSelectedWorkloadAnnotations(filteredObject);
          return;
        }

        // Fallback to cloud_resource_attributes for manual CI configuration
        if (workload.cloud_resource_id) {
          const attributes = await k8sApi.getResourceAttributes(workload.cloud_resource_id);
          const manualConfig: { [key: string]: any } = {};
          attributes.forEach((attr: { name: string; value: string }) => {
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
    }
  };

  const getWorkloadDeploymentForSelectedRightSize = (_data: any) => {
    if (_data?.data?.cloud_resourse?.meta?.namespace && _data?.data?.cloud_resourse?.meta?.controller) {
      k8sApi
        .getK8sWorkload(1, 0, {
          accountId: _data?.accountId ?? _data?.account_id ?? selectedCluster?.value,
          namespaceName: _data?.data?.cloud_resourse?.meta?.namespace,
          workloadName: _data?.data?.cloud_resourse?.meta?.controller,
          workloadType: 'Deployment',
        })
        .then(async (res) => {
          const workloads = res?.data?.k8s_workloads || [];
          if (workloads && workloads.length == 1) {
            const workload = workloads[0];
            const annotations = workload.meta?.config?.annotations || {};

            // Check k8s annotations first
            const filteredKeys = Object.keys(annotations).filter((_key) => _key.startsWith('ci.nudgebee.com'));
            if (filteredKeys && filteredKeys.length > 0) {
              const filteredObject: { [key: string]: any } = {};
              filteredKeys.forEach((_key) => {
                filteredObject[_key] = annotations[_key];
              });
              setSelectedWorkloadAnnotations(filteredObject);
              return;
            }

            // Fallback to cloud_resource_attributes for manual CI configuration
            if (workload.cloud_resource_id) {
              try {
                const attributes = await k8sApi.getResourceAttributes(workload.cloud_resource_id);
                const manualConfig: { [key: string]: any } = {};
                attributes.forEach((attr: { name: string; value: string }) => {
                  if (attr.name.startsWith('ci.nudgebee.com')) {
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

            setGithubRepoName('');
            setSelectedWorkloadAnnotations({});
          }
        })
        .catch((err) => {
          console.error(err);
        });
    }
  };

  const listGithubConfigurations = () => {
    apiIntegrations
      .listTicketConfigurationsByTool({
        status: 'enabled',
        tool: 'github',
      })
      .then((res: any) => {
        if (res && res?.data?.length > 0) {
          setGithubRepos(res?.data.map((g: { name: string }) => g.name));
        }
      })
      .catch((err) => {
        console.error(err);
      });
  };

  const handleSelectedAlgo = (buttonId: any, buttonValue: any) => {
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      algo: buttonId,
    }));
    setAlgo(buttonValue || algo);
    updateDataBasedOnButtonValueForCpu(buttonValue);
  };

  const handleSelectedBuffer = (buttonId: any, buttonValue: any) => {
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      buffer: buttonId,
    }));
    setBuffer(buttonValue || buffer);
    updateDataBasedOnButtonValueForCpu(buttonValue);
  };

  const handleSelectedMemoryBuffer = (buttonId: any, buttonValue: any) => {
    setSelectedButtons(buttonId);
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      memBuffer: buttonId,
    }));
    setMemBuffer(buttonValue);
    updateDataBasedOnButtonValueForMemory(buttonValue);
  };

  const handleSelectedMemoryAlgo = (buttonId: any, buttonValue: any) => {
    setSelectedButtons(buttonId);
    setSelectedButtons((prevSelectedButtons) => ({
      ...prevSelectedButtons,
      memory: buttonId,
    }));
    updateDataBasedOnButtonValueForMemory(buttonValue);
  };
  const updateDataBasedOnButtonValueForCpu = (_value: any) => {
    const selectedKey = algo?.toLowerCase();

    switch (_value) {
      case 'NBALGO': {
        const parsedNbalgo = parseFloat(additionalCpuInfo.nbalgo);
        setUpdatedData((prevData: UpdatedDataType) => ({
          ...prevData,
          cpu: {
            ...prevData.cpu,
            request: !isNaN(parsedNbalgo) ? parsedNbalgo.toFixed(2) : '0.00',
            limit: null,
          },
        }));
        break;
      }
      case 'P99': {
        const parsedP99 = parseFloat(additionalCpuInfo.p99);
        setUpdatedData((prevData: UpdatedDataType) => ({
          ...prevData,
          cpu: {
            ...prevData.cpu,
            request: !isNaN(parsedP99) ? parsedP99.toFixed(2) : '0.00',
            limit: null,
          },
        }));
        break;
      }
      case 'P97': {
        const parsedP97 = parseFloat(additionalCpuInfo.p97);
        setUpdatedData((prevData: UpdatedDataType) => ({
          ...prevData,
          cpu: {
            ...prevData.cpu,
            request: !isNaN(parsedP97) ? parsedP97.toFixed(2) : '0.00',
            limit: null,
          },
        }));
        break;
      }
      case 'P95': {
        const parsedP95 = parseFloat(additionalCpuInfo.p95);
        setUpdatedData((prevData: UpdatedDataType) => ({
          ...prevData,
          cpu: {
            ...prevData.cpu,
            request: !isNaN(parsedP95) ? parsedP95.toFixed(2) : '0.00',
            limit: null,
          },
        }));
        break;
      }
      case 5: {
        const parsedSelectedKey = parseFloat(additionalCpuInfo[selectedKey]);
        setUpdatedData((prevData: UpdatedDataType) => ({
          ...prevData,
          cpu: {
            ...prevData.cpu,
            request: !isNaN(parsedSelectedKey) ? (parsedSelectedKey * 1.05).toFixed(2) : '0.00',
            limit: null,
          },
        }));
        break;
      }
      case 10: {
        const parsedSelectedKey = parseFloat(additionalCpuInfo[selectedKey]);
        setUpdatedData((prevData: UpdatedDataType) => ({
          ...prevData,
          cpu: {
            ...prevData.cpu,
            request: !isNaN(parsedSelectedKey) ? (parsedSelectedKey * 1.1).toFixed(2) : '0.00',
            limit: null,
          },
        }));
        break;
      }
      case 15: {
        const parsedSelectedKey = parseFloat(additionalCpuInfo[selectedKey]);
        setUpdatedData((prevData: UpdatedDataType) => ({
          ...prevData,
          cpu: {
            ...prevData.cpu,
            request: !isNaN(parsedSelectedKey) ? (parsedSelectedKey * 1.15).toFixed(2) : '0.00',
            limit: null,
          },
        }));
        break;
      }
      default:
        break;
    }
  };

  const updateDataBasedOnButtonValueForMemory = (_value: any) => {
    switch (_value) {
      case 0:
        setUpdatedData((prevData: UpdatedDataType) => ({
          ...prevData,
          memory: {
            ...prevData.memory,
            request: formatMemory(parseInt(additionalMemInfo.nbalgoReq), 'bytes', 'mb', false),
            limit: formatMemory(parseInt(additionalMemInfo.nbalgoLimit), 'bytes', 'mb', false),
          },
        }));
        break;
      case 5:
        setUpdatedData((prevData: UpdatedDataType) => ({
          ...prevData,
          memory: {
            ...prevData.memory,
            request: formatMemory(parseInt(additionalMemInfo.nbalgoReq) * 1.05, 'bytes', 'mb', false),
            limit: formatMemory(parseInt(additionalMemInfo.nbalgoLimit) * 1.05, 'bytes', 'mb', false),
          },
        }));
        break;
      case 10:
        setUpdatedData((prevData: UpdatedDataType) => ({
          ...prevData,
          memory: {
            ...prevData.memory,
            request: formatMemory(parseInt(additionalMemInfo.nbalgoReq) * 1.1, 'bytes', 'mb', false),
            limit: formatMemory(parseInt(additionalMemInfo.nbalgoLimit) * 1.1, 'bytes', 'mb', false),
          },
        }));
        break;
      case 15:
        setUpdatedData((prevData: UpdatedDataType) => ({
          ...prevData,
          memory: {
            ...prevData.memory,
            request: formatMemory(parseInt(additionalMemInfo.nbalgoReq) * 1.15, 'bytes', 'mb', false),
            limit: formatMemory(parseInt(additionalMemInfo.nbalgoLimit) * 1.15, 'bytes', 'mb', false),
          },
        }));
        break;
      case 20:
        setUpdatedData((prevData: UpdatedDataType) => ({
          ...prevData,
          memory: {
            ...prevData.memory,
            request: formatMemory(parseInt(additionalMemInfo.nbalgoReq) * 1.2, 'bytes', 'mb', false),
            limit: formatMemory(parseInt(additionalMemInfo.nbalgoLimit) * 1.2, 'bytes', 'mb', false),
          },
        }));
        break;
      default:
        break;
    }
  };

  const handleUpdateData = (data: any) => {
    const data1 = data;
    setUpdatedData(data1);
  };

  const handleTimeFrame = (_buttonId: any, _buttonValue: any, clickedButton: any) => {
    if (clickedButton?.label === 'Daily') {
      setIsDailyTimeFrameOpen(true);
      setIsWeeklyTimeFrameOpen(false);
    } else if (clickedButton?.label === 'Weekly') {
      setIsWeeklyTimeFrameOpen(true);
      setIsDailyTimeFrameOpen(true);
    } else if (clickedButton?.label === 'Cron Expression') {
      setIsWeeklyTimeFrameOpen(false);
      setIsDailyTimeFrameOpen(false);
    }
  };

  const updateCronJob = (e: any) => {
    setCronExpression(e.target.value);
  };

  const handleCancel = () => {
    closeAutoPilotSingleConfigModal(false);
  };
  const handleReviewAction = (status: string) => {
    if (status == 'REJECTED') {
      if (!reviewComment) {
        setDisplayErrorsDesc({ ...displayErrorsDesc, reviewComment: 'Please add a review comment if you wish to reject the runbook' });
        return;
      }
      apiAutoPilot
        .updateAutoPilotApprovalStatus(
          approvalData?.id,
          autoOptimizeData?.accountId ?? autoOptimizeData?.account_id ?? selectedCluster?.value,
          reviewComment,
          'REJECTED'
        )
        .then((res) => {
          if (res?.data?.update_status_auto_pilot_approval?.id) {
            closeAutoPilotSingleConfigModal(true);
          } else {
            snackbar.error(`Failed to reject Auto Optimize !`);
          }
        });
    } else if (status == 'APPROVED') {
      apiAutoPilot
        .updateAutoPilotApprovalStatus(
          approvalData?.id,
          autoOptimizeData?.accountId ?? autoOptimizeData?.account_id ?? selectedCluster?.value,
          reviewComment,
          'APPROVED'
        )
        .then((res) => {
          if (res?.data?.update_status_auto_pilot_approval?.id) {
            closeAutoPilotSingleConfigModal(true);
          } else {
            snackbar.error(`Failed to approve Auto Optimize !`);
          }
        });
    }
  };

  const validateAutoPilotRequest = () => {
    let valid = true;
    const validate = {
      notification: {
        teams: '',
        slack: '',
        google_chat: '',
      },
      gitops: {
        message: '',
      },
      reviewComment: '',
      jira: {
        formErrors: {},
      },
    };

    if (notificationData?.slack && !slackChannelName) {
      validate.notification.slack = 'Select Slack Channel';
    }

    if (notificationData?.teams) {
      if (!msTeamName && !msChannelName) {
        validate.notification.teams = 'Select Team and Channel';
        valid = false;
      }
      if (!msTeamName) {
        validate.notification.teams = 'Select atleast one team';
        valid = false;
      }
      if (msTeamName && !msChannelName) {
        validate.notification.teams = 'Select one Channel';
        valid = false;
      }
    }

    if (raisePullRequest && githubRepoName == '') {
      validate.gitops.message = 'Please select Github Repo name';
      valid = false;
    } else if (githubRepoName && Object.keys(selectedWorkloadAnnotations).length == 0) {
      validate.gitops.message = 'Please configure the annotation at Deployment';
      valid = false;
    }

    if (notificationData?.google_chat && !googleChatChannelName) {
      validate.notification.google_chat = 'Select Google Chat Channel';
      valid = false;
    }

    setDisplayErrorsDesc(validate);
    return valid;
  };

  const cleanupDateFormat = (date: Date | string | null | undefined) => {
    if (date == null || date == undefined) {
      return null;
    }

    let s = date;
    if (s instanceof Date) {
      s = s.toISOString();
    }

    if (s.includes('.')) {
      if (s.endsWith('Z')) {
        return s;
      }
      return s + 'Z';
    }

    if (s.endsWith('Z')) {
      return s.replaceAll('Z', '.000Z');
    }
    return s + '.000Z';
  };

  const handleCreateAutoPilotRule = () => {
    if (!validateAutoPilotRequest()) {
      return;
    }

    if (createTicket) {
      if (Object.keys(displayErrorsDesc.jira.formErrors).length > 0) {
        snackbar.error(`Fill the required fields to create Jira ticket- ${Object.keys(displayErrorsDesc.jira.formErrors)}`);
        return;
      }
    }

    if (!minMemory) {
      snackbar.error('Minimum Memory cannot be empty!');
      return;
    }
    if (!minCPU) {
      snackbar.error('Minimum CPU cannot be empty!');
      return;
    }
    if (maxMemory < minMemory) {
      snackbar.error('Maximum Memory cannot be less than Minimum Memory');
      return;
    }
    if (maxCPU < minCPU) {
      snackbar.error('Maximum CPU cannot be less than Minimum CPU');
      return;
    }

    if (selectedApplications?.length == 0) {
      snackbar.error('Please select Namespace & Application');
      return;
    }

    if (cronExpression) {
      const cronExpressionArray = cronExpression.split(' ');
      if (cronExpressionArray.length !== 5) {
        snackbar.error('Invalid cron expression');
        return;
      }
    }

    if (setIsLoading) {
      setIsLoading(true);
    }
    const data: any = {
      account_id: autoPilotData?.accountId ?? autoPilotData?.account_id ?? selectedCluster?.value,
      category: 'vertical_rightsize',
      resource_filter: selectedApplications,
      auto_optimize_config: {
        cpu: {
          algo: autoOptimizeData?.rule?.rules?.cpu?.algo || algo,
          buffer_pct: autoOptimizeData?.rule?.rules?.cpu?.buffer_pct || buffer,
          trigger: {
            change_pct: minCPU,
            max_change_pct: maxCPU,
          },
        },
        memory: {
          algo: 'max',
          unit: 'GB',
          buffer_pct: autoOptimizeData?.rule?.rules?.memory?.buffer_pct || memBuffer,
          trigger: {
            change_pct: minMemory,
            max_change_pct: maxMemory,
          },
        },
      },
      schedule: {
        frequency: cronExpression == '' ? '0 * * * *' : cronExpression,
        start_date: cleanupDateFormat(selectedDate?.startDate?.toISOString() ?? new Date()),
        end_date: cleanupDateFormat(selectedDate?.endDate?.toISOString() ?? null),
      },
      notification: {
        slack: notificationData?.slack
          ? {
              enabled: notificationData?.slack,
              channel_id: notificationData?.channelId,
            }
          : {
              enabled: notificationData?.slack,
            },
        ms_teams: notificationData?.teams
          ? { enabled: notificationData?.teams, team_id: msTeamName, channel_id: msChannelName }
          : {
              enabled: notificationData?.teams,
            },
        email: {
          enabled: notificationData?.email,
        },
        google_chat: notificationData?.google_chat
          ? {
              enabled: notificationData?.google_chat,
              channel_id: notificationData?.gChatChannelId,
            }
          : {
              enabled: notificationData?.google_chat,
            },
      },
      dryrun: dryRunData,
      gitops: {
        enabled: githubRepoName != '',
        repository_name: githubRepoName,
      },
      ticket_config: createTicketData,
    };
    if (!autoOptimizeData.id) {
      k8sApi
        .singleConfigAuotPilot(data)
        .then((res) => {
          setIsLoading(false);
          if (res?.errors) {
            snackbar.error('Error - ' + res?.errors[0]?.message);
          } else {
            snackbar.success('Auto Optimize Rule Created Successfully');
            closeAutoPilotSingleConfigModal(true);
            setNotificationData({
              ...notificationData,
              slack: false,
              teams: false,
            });
            setDisplayErrorsDesc({
              notification: {
                teams: '',
                slack: '',
                google_chat: '',
              },
              gitops: {
                message: '',
              },
              reviewComment: '',
              jira: {
                formErrors: {},
              },
            });
            setMSChannelName('');
            setMsTeamName('');
            setSlackChannelName('');
            handleCancel();
          }
        })
        .catch((error) => {
          console.error('Error in singleConfigAuotPilot:', error);
        })
        .finally(() => {
          setIsLoading(false);
        });
    } else {
      data.id = autoOptimizeData.id;
      apiAutoPlaybook
        .singleConfigUpdateAuotPilot(data)
        .then((res) => {
          setIsLoading(false);
          if (res?.errors) {
            snackbar.error('Error - ' + res?.errors[0]?.message);
          } else {
            snackbar.success('Auto Optimize Rule Updated Successfully');
            closeAutoPilotSingleConfigModal(true);
            if (listAutoPilot) {
              listAutoPilot();
            }
          }
        })
        .catch((error) => {
          console.error('Error in singleConfigUpdateAuotPilot:', error);
        })
        .finally(() => {
          setIsLoading(false);
        });
    }
  };

  interface AutoPilotButton {
    label: string;
    backgroundColor: string;
    onClick: () => void;
    isDisabled?: boolean;
  }

  const autoPilotSingleConfigButton: AutoPilotButton[] = [
    {
      label: 'Cancel',
      backgroundColor: colors.background.primaryDark,
      onClick: handleCancel,
    },
  ];

  if (reviewAutoOptimize) {
    autoPilotSingleConfigButton.push({
      isDisabled: !Object.keys(approvalData).length || approvalData.status == 'REJECTED',
      label: 'Reject',
      backgroundColor: colors.background.primaryDark,
      onClick: () => handleReviewAction('REJECTED'),
    });
    autoPilotSingleConfigButton.push({
      isDisabled: !Object.keys(approvalData).length || approvalData.status == 'APPROVED',
      label: 'Approve',
      backgroundColor: colors.background.primaryDark,
      onClick: () => handleReviewAction('APPROVED'),
    });
  } else {
    autoPilotSingleConfigButton.push({
      label: !autoOptimizeData.id ? 'Create Auto Optimize Rule' : 'Update Auto Optimize Rule',
      backgroundColor: colors.background.primaryDark,
      onClick: handleCreateAutoPilotRule,
    });
  }

  const handleSlackButtonClick = () => {
    const slackButtonState = !notificationData.slack;
    setNotificationData({
      ...notificationData,
      slack: slackButtonState,
    });
    if (!slackButtonState) {
      setSlackChannelName('');
    }
  };

  const handleTeamsButtonClick = () => {
    const teamsButtonState = !notificationData.teams;
    setNotificationData({
      ...notificationData,
      teams: teamsButtonState,
    });
    if (!teamsButtonState) {
      setMsTeamName('');
      setMSChannelName('');
    }
  };

  const handleGoogleChatButtonClick = () => {
    const googleChatButtonState = !notificationData.google_chat;
    setNotificationData({
      ...notificationData,
      google_chat: googleChatButtonState,
    });
    if (!googleChatButtonState) {
      setGoogleChatChannelName('');
    }
  };

  const handleStartDateEndDate = (type: string, date: Dayjs | null) => {
    if (type == 'start') {
      setSelectedDate((prevState) => ({
        endDate: prevState.endDate,
        startDate: date,
      }));
    } else if (type == 'end') {
      setSelectedDate((prevState) => ({
        startDate: prevState.startDate,
        endDate: date,
      }));
    }
  };

  const handleChildComponentChange = (value: any, type: string) => {
    switch (type) {
      case 'applications':
        setSelectedApplications(JSON.parse(value));
        break;
      case 'all-applications-check':
        setSelectedApplications(JSON.parse(value));
        break;
      case 'all-applications-uncheck':
        setSelectedApplications([]);
        break;
      case 'namespace':
        setSelectedApplications(selectedApplications.filter((app: any) => value.includes(app.namespace)));
        setSelectedNamespaces(value);
        break;
    }
  };

  const filterDataFromTicketForm = (data: any) => {
    const cloneObj: any = JSON.parse(JSON.stringify(data.formData));
    delete cloneObj.assignee;

    const fields = data.selectedIssueTypeTicketMetadata?.[0]?.fields;

    if (fields) {
      Object.entries(fields).forEach(([key, value]: [string, any]) => {
        if (value && value.type === 'datepicker') {
          cloneObj[key] = cloneObj[key] ? new Date(cloneObj[key]).toISOString().split('T')[0] : new Date().toISOString().split('T')[0];
        } else if (value && value.type === 'datetime') {
          cloneObj[key] = cloneObj[key] ? new Date(cloneObj[key]).toISOString() : new Date().toISOString();
        }
      });
    }

    setCreateTicketData((prev: any) => ({
      ...prev, // Best practice: keep other fields safe

      // FIX 1: Add fallback for configuration_id
      configuration_id: data?.selectedConfig?.id ?? prev?.configuration_id,

      // FIX 2: Fix casing (project_key) and keep fallback
      project_key: data?.selectedProject?.key ?? prev?.project_key,

      source: 'kubernetes',

      // FIX 3: Ensure fallback uses the correct casing used in state
      ticket_type: data?.selectedIssueType ?? prev?.ticket_type,
      severity: data?.formData?.severity ?? prev?.severity,
      description: data?.ticketDetails?.description ?? prev?.description,
      assignee: data?.formData?.assignee ?? prev?.assignee,

      platform: 'jira',
      additional_fields: cloneObj,
      enabled: true,
    }));
    setDisplayErrorsDesc((prev: any) => ({
      ...prev,
      jira: {
        formErrors: data.formErrors,
      },
    }));
  };

  return (
    <Box>
      <RunbookTargetResource
        selectedCluster={selectedCluster}
        selectedApplications={selectedApplications}
        selectedNamespace={selectedNamespaces}
        multipleNamespace
        reviewRunbook={reviewAutoOptimize}
        handleChildComponentChange={handleChildComponentChange}
      />
      <Box sx={{ display: 'flex', gap: '16px', marginTop: '16px' }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
          <VerticalAutopPilotForm
            handleUpdateData={handleUpdateData}
            handleSelectedAlgo={handleSelectedAlgo}
            handleSelectedBuffer={handleSelectedBuffer}
            handleSelectedMemoryBuffer={handleSelectedMemoryBuffer}
            handleSelectedMemoryAlgo={handleSelectedMemoryAlgo}
            data={updatedData}
            currentData={allocatedData}
            activeButton={selectedButtons}
            additionalInfoCPUAndMem={{ cpuInfo: additionalCpuInfo, memInfo: additionalMemInfo }}
            isDisable={true}
            reviewAutoOptimize={reviewAutoOptimize}
            handleInputChange={() => {}}
          />
          <Box
            sx={{
              borderRadius: '4px',
              borderTop: `1px solid ${colors.switchIcon}`,
              background: colors.background.primaryLightest,
              padding: '8px 16px',
              marginTop: '30px',
            }}
          >
            <Box>
              <Typography sx={{ color: colors.text.secondary, fontSize: '16px', fontWeight: 600 }}>{'Trigger Thresholds'}</Typography>
              <Typography sx={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}>
                <b>Minimum Change - </b> Do not trigger Change if the percent difference is less than the Minimum Change
                <br />
              </Typography>
              <Typography sx={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}>
                <b>Maximum Change - </b> Do not trigger Change if the percent difference is more than the Maximum Change
                <br />
              </Typography>
            </Box>
          </Box>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '16px', my: '30px' }}>
            <Box sx={{ display: 'flex', gap: '16px', flexDirection: 'row' }}>
              <Box sx={{ flex: 1 }}>
                <Box sx={{ mb: '10px' }}>
                  <TextWithBorder
                    borderWidth='2px'
                    borderColor={colors.nudgebeeMain}
                    value='CPU'
                    sx={{
                      '& p': {
                        fontSize: '14px',
                        color: colors.text.secondary,
                        fontWeight: 600,
                      },
                    }}
                  />
                </Box>
                <Box
                  sx={{
                    borderRadius: '8px',
                    border: `1px solid ${colors.border.vertical}`,
                    padding: '16px',
                    gap: '10px',
                  }}
                >
                  <Grid container spacing={2}>
                    <Grid item xs={6}>
                      <Box>
                        <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 400, mb: '6px' }}>Minimum Change</Typography>
                        <TextField
                          InputProps={{
                            endAdornment: <InputAdornment position='end'>%</InputAdornment>,
                            inputProps: { min: 0 },
                          }}
                          sx={{
                            '&.MuiFormControl-root': {
                              maxWidth: '110px',
                            },
                            '& .MuiOutlinedInput-root': {
                              border: `0.5px solid ${colors.border.secondary}`,
                            },
                            '& .MuiInputBase-root': {
                              height: '36px',
                            },
                          }}
                          size='small'
                          value={minCPU}
                          fullWidth
                          type='number'
                          onChange={(e) => {
                            const value = e.target.value;
                            if (value != null && value != undefined && !isNaN(Number(value))) {
                              setMinCPU(parseInt(value));
                            }
                          }}
                          onKeyDown={(e) => {
                            if (e.key === '-') {
                              e.preventDefault();
                            }
                          }}
                          disabled={reviewAutoOptimize}
                        />
                      </Box>
                    </Grid>
                    <Grid item xs={6}>
                      <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 400, mb: '6px' }}>Maximum Change</Typography>
                      <TextField
                        InputProps={{
                          endAdornment: <InputAdornment position='end'>%</InputAdornment>,
                          inputProps: { min: 0 },
                        }}
                        sx={{
                          ...inputSx,
                          '&.MuiFormControl-root': {
                            maxWidth: '110px',
                          },
                          '& .MuiInputBase-root': {
                            height: '36px',
                          },
                        }}
                        size='small'
                        value={maxCPU}
                        fullWidth
                        type='number'
                        onChange={(e) => {
                          const value = e.target.value;
                          if (value != null && value != undefined && !isNaN(Number(value))) {
                            setMaxCPU(parseInt(value));
                          }
                        }}
                        onKeyDown={(e) => {
                          if (e.key === '-') {
                            e.preventDefault();
                          }
                        }}
                        disabled={reviewAutoOptimize}
                      />
                    </Grid>
                  </Grid>
                </Box>
              </Box>
              <Box sx={{ flex: 1 }}>
                <Box sx={{ mb: '10px' }}>
                  <TextWithBorder
                    borderWidth='2px'
                    borderColor={colors.nudgebeeMain}
                    value='Memory'
                    sx={{
                      '& p': {
                        fontSize: '14px',
                        color: colors.text.secondary,
                        fontWeight: 600,
                      },
                    }}
                  />
                </Box>
                <Box
                  sx={{
                    borderRadius: '8px',
                    border: `1px solid ${colors.border.vertical}`,
                    padding: '16px',
                    gap: '10px',
                  }}
                >
                  <Grid container spacing={2}>
                    <Grid item xs={6}>
                      <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 400, mb: '6px' }}>Minimum Change</Typography>
                      <TextField
                        InputProps={{
                          endAdornment: <InputAdornment position='end'>%</InputAdornment>,
                          inputProps: { min: 0 },
                        }}
                        sx={{
                          ...inputSx,
                          '&.MuiFormControl-root': {
                            maxWidth: '110px',
                          },
                          '& .MuiInputBase-root': {
                            height: '36px',
                          },
                        }}
                        size='small'
                        value={minMemory}
                        fullWidth
                        type='number'
                        onChange={(e) => {
                          const value = e.target.value;
                          if (value != null && value != undefined && !isNaN(Number(value))) {
                            setMinMemory(parseInt(value));
                          }
                        }}
                        onKeyDown={(e) => {
                          if (e.key === '-') {
                            e.preventDefault();
                          }
                        }}
                        disabled={reviewAutoOptimize}
                      />
                    </Grid>
                    <Grid item xs={6}>
                      <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 400, mb: '6px' }}>Maximum Change</Typography>
                      <TextField
                        InputProps={{
                          endAdornment: <InputAdornment position='end'>%</InputAdornment>,
                          inputProps: { min: 0 },
                        }}
                        sx={{
                          ...inputSx,
                          '&.MuiFormControl-root': {
                            maxWidth: '110px',
                          },
                          '& .MuiInputBase-root': {
                            height: '36px',
                          },
                        }}
                        size='small'
                        value={maxMemory}
                        fullWidth
                        type='number'
                        onChange={(e) => {
                          const value = e.target.value;
                          if (value != null && value != undefined) {
                            setMaxMemory(parseInt(value));
                          }
                        }}
                        onKeyDown={(e) => {
                          if (e.key === '-') {
                            e.preventDefault();
                          }
                        }}
                        disabled={reviewAutoOptimize}
                      />
                    </Grid>
                  </Grid>
                </Box>
              </Box>
            </Box>
          </Box>
          <Box
            sx={{
              borderRadius: '4px 4px 0px 0px',
              borderTop: `1px solid ${colors.switchIcon}`,
              background: colors.background.primaryLightest,
              padding: '8px 16px',
              marginTop: '20px',
            }}
          >
            <Typography sx={{ color: colors.text.secondary, fontSize: '16px', fontWeight: 600 }}>{'Schedule Optimization'}</Typography>
            <Typography sx={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}>Cron Schedule follow UTC timezone</Typography>
          </Box>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', marginTop: '10px' }}>
            <Box sx={{ display: 'flex', gap: '12px', alignItems: 'end' }}>
              <ButtonTabs
                disabled={reviewAutoOptimize}
                buttons={buttonConfiguration?.timeButtonConfigs?.timeFrame}
                callBack={handleTimeFrame}
                color={colors.text.white}
                fontSize='12px'
                borderColor={colors.border.primaryLight}
                background={colors.background.primaryDark}
                selectedButton={3}
              />
            </Box>
          </Box>
          <Box>
            <TimeHeader title='Cron Expression' subtitle='' />
            <TextField
              InputLabelProps={{ shrink: false }}
              sx={{ width: '150px' }}
              size='small'
              value={cronExpression}
              fullWidth
              name={'updateCron'}
              type='text'
              onChange={(e) => updateCronJob(e)}
              disabled={reviewAutoOptimize}
            />
          </Box>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '24px', marginTop: '10px' }}>
            {isDailyTimeFrameOpen && (
              <Box>
                <TimeHeader title='Daily' subtitle=' Actions can be scheduled for one or more hours each day' />
                <ButtonTabs
                  disabled={reviewAutoOptimize}
                  buttons={buttonConfiguration?.timeButtonConfigs?.daily}
                  callBack={() => {}}
                  color={colors.text.white}
                  fontSize='12px'
                  borderColor={colors.border.primaryLight}
                  background={colors.background.primaryDark}
                  height='22px'
                  selectedButton={null}
                />
              </Box>
            )}
            {isWeeklyTimeFrameOpen && (
              <Box>
                <TimeHeader title='Weekly' subtitle='Which day of the week' />
                <ButtonTabs
                  disabled={reviewAutoOptimize}
                  buttons={buttonConfiguration?.timeButtonConfigs?.weekly}
                  callBack={() => {}}
                  color={colors.text.white}
                  fontSize='12px'
                  borderColor={colors.border.primaryLight}
                  background={colors.background.primaryDark}
                  height='22px'
                  selectedButton={null}
                />
              </Box>
            )}
            <Box
              sx={{
                display: 'flex',
                gap: '12px',
                '& .MuiInputLabel-root': {
                  lineHeight: '12px !important',
                  overflow: 'visible',
                },
              }}
            >
              <DateTimeRangePicker
                handleStartDateEndDate={handleStartDateEndDate}
                startDate={selectedDate?.startDate || dayjs()}
                endDate={selectedDate?.endDate}
                views={['day', 'hours', 'minutes']}
                minDate={dayjs(new Date())}
                maxDateTime={null}
                disableStartDate={true}
                disableEndDate={reviewAutoOptimize}
              />
            </Box>
          </Box>
          <Box
            sx={{
              borderRadius: '4px 4px 0px 0px',
              borderTop: `1px solid ${colors.switchIcon}`,
              background: colors.background.primaryLightest,
              padding: '8px 16px',
            }}
          >
            <FormControlLabel
              control={<Switch checked={raisePullRequest} onChange={(event) => setRaisePullRequest(event.target.checked)} name='raisePullRequest' />}
              label={
                <Box sx={{ display: 'flex', gap: '8px' }}>
                  <Typography sx={{ color: colors.text.secondary, fontSize: '16px', fontWeight: 600 }}>{'Raise Git Pull Request'}</Typography>
                  {Object.keys(selectedWorkloadAnnotations).length == 0 && (
                    <CustomTooltip title='Cannot enable Raise Git Pull Request. Please configure the required annotations at Deployment.'>
                      <Box ml={1} sx={{ cursor: 'pointer', display: 'flex', alignItems: 'center' }}>
                        <SafeIcon src={infoIcon} alt='info' width={20} height={20} />
                      </Box>
                    </CustomTooltip>
                  )}
                </Box>
              }
              disabled={Object.keys(selectedWorkloadAnnotations).length === 0 || reviewAutoOptimize}
            />
            {Object.keys(selectedWorkloadAnnotations).length === 0 && (
              <Box sx={{ marginTop: '8px' }}>
                <Typography sx={{ color: colors.text.secondary, fontSize: '12px', fontWeight: 400 }}>
                  The following annotations are required at Deployment:
                </Typography>
                <ul>
                  {requestAnnotations.map((_value) => (
                    <li key={_value}>
                      <Typography sx={{ fontSize: '12px', fontWeight: 600 }}>{_value}</Typography>
                    </li>
                  ))}
                </ul>
              </Box>
            )}
          </Box>
          {raisePullRequest && selectedWorkloadAnnotations && Object.keys(selectedWorkloadAnnotations).length > 0 ? (
            <ul>
              {Object.entries(selectedWorkloadAnnotations).map(([_key, value]) => (
                <li key={_key}>
                  <span>{`${_key}: ${value}`}</span>
                </li>
              ))}
            </ul>
          ) : null}
          {raisePullRequest && Object.keys(selectedWorkloadAnnotations).length > 0 ? (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '15px' }}>
                <CustomDropdown
                  key={'github-action'}
                  label={'Repo Name'}
                  minWidth='150px'
                  onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
                    setGithubRepoName(e.target.value ?? '');
                    setDisplayErrorsDesc((prev) => ({
                      ...prev,
                      gitops: {
                        message: '',
                      },
                    }));
                  }}
                  value={githubRepoName}
                  options={githubRepos || []}
                  showDynamicPaper={true}
                  showNormalField={true}
                  isDisabled={reviewAutoOptimize}
                  error={displayErrorsDesc.gitops.message != ''}
                  helperText={displayErrorsDesc.gitops.message || ''}
                />
              </Box>
            </Box>
          ) : null}
          <Box
            sx={{
              borderRadius: '4px 4px 0px 0px',
              borderTop: `1px solid ${colors.switchIcon}`,
              background: colors.background.primaryLightest,
              padding: '8px 16px',
            }}
          >
            <FormControlLabel
              control={
                <Switch checked={dryRunData} onChange={(event) => setDryRunData(event.target.checked)} name='dryRun' disabled={reviewAutoOptimize} />
              }
              label={<Typography sx={{ color: colors.text.secondary, fontSize: '16px', fontWeight: 600 }}>{'Dry Run'}</Typography>}
            />
            <Typography sx={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}>
              Instead of applying Recommendations, Log changes
            </Typography>
          </Box>
          <Box
            sx={{
              borderRadius: '4px 4px 0px 0px',
              borderTop: `1px solid ${colors.switchIcon}`,
              background: colors.background.primaryLightest,
              padding: '8px 16px',
            }}
          >
            <FormControlLabel
              control={
                <Switch
                  checked={createTicket}
                  onChange={(event) => {
                    setCreateTicket(event.target.checked);
                    if (!event.target.checked) {
                      setCreateTicketData({});
                    }
                  }}
                  name='raisePullRequest'
                />
              }
              label={
                <Box sx={{ display: 'flex', gap: '8px' }}>
                  <Typography sx={{ color: colors.text.secondary, fontSize: '16px', fontWeight: 600 }}>{'Create Jira Ticket'}</Typography>
                </Box>
              }
              disabled={reviewAutoOptimize}
            />
            {createTicket && (
              <TicketFormSection
                ticketData={{
                  ...createTicketData,
                  // Helper to ensure child finds the ID if it looks for selectedConfig.id
                  selectedConfig: { id: createTicketData?.configuration_id },
                  projectKey: createTicketData?.project_key,
                  ticketType: createTicketData?.ticket_type,
                  // Pass the raw fields for the child to run reverseProcessing
                  additionalFields: createTicketData?.additional_fields,
                }}
                error={createTicketData?.error}
                onStateChange={(newState: any) => filterDataFromTicketForm(newState)}
                ignoreFields={['subject', 'description']}
                isEdit={true}
                forceValidate={true}
                viewOnlyMode={false}
                toolName={'jira'}
              />
            )}
          </Box>
          <NotificationForm
            msTeamsData={msTeamsData}
            msChannelListOption={msChannelListOption}
            slackChannelList={slackChannelList}
            notificationData={notificationData}
            slackChannelName={slackChannelName}
            setSlackChannelName={setSlackChannelName}
            displayErrorsDesc={displayErrorsDesc}
            googleChannelList={googleChannelList}
            googleChatChannelName={googleChatChannelName}
            handleGoogleChatButtonClick={handleGoogleChatButtonClick}
            handleSlackButtonClick={handleSlackButtonClick}
            handleTeamsButtonClick={handleTeamsButtonClick}
            msChannelName={msChannelName}
            msTeamName={msTeamName}
            setMSChannelName={setMSChannelName}
            setMsTeamName={setMsTeamName}
            setNotificationData={setNotificationData}
            setGoogleChatChannelName={setGoogleChatChannelName}
            isLoadingSlackChannels={isLoadingSlackChannels}
            isMsTeamsLoading={isMsTeamsLoading}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
          />
        </Box>
      </Box>
      <ActionButtons buttons={autoPilotSingleConfigButton} activeButton={activeButton} setActiveButton={setActiveButton} />
    </Box>
  );
};

VerticalAutoOptimizeSingleConfiguration.propTypes = {
  autoOptimizeData: PropTypes.object.isRequired,
  closeAutoPilotSingleConfigModal: PropTypes.func.isRequired,
  msTeamsData: PropTypes.array.isRequired,
  googleChannelList: PropTypes.array.isRequired,
  listAutoPilot: PropTypes.func,
  data: PropTypes.object,
  currentData: PropTypes.object.isRequired,
  additionalInfoCPUAndMem: PropTypes.object,
  isLoading: PropTypes.bool,
  setIsLoading: PropTypes.func,
  reviewAutoOptimize: PropTypes.bool,
  approvalData: PropTypes.object,
  isMsTeamsLoading: PropTypes.bool,
  isGoogleChannelsLoading: PropTypes.bool,
};

export default VerticalAutoOptimizeSingleConfiguration;
