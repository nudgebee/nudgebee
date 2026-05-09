import React, { useEffect, useState } from 'react';
import AutoPilotHeaderCard from '@components1/autopilot/card/AutoOptimizeHeaderCard';
import { Box, Divider, Typography } from '@mui/material';
import { formatMemory } from '@lib/formatter';
import apiAutoPlaybook from '@api1/autoPlaybook';
import apiAccount from '@api1/account';
import k8sApi from '@api1/kubernetes';
import PropTypes from 'prop-types';
import { useData } from '@context/DataContext';
import CustomButtonsGroup from '@components1/common/CustomButtonsGroup';
import CustomHeatMap from '@components1/common/charts/CustomHeatMap';
import dayjs from 'dayjs';
import ActionButtons from './AutoOptimizeActionButtons';
import NotificationForm from './AutoOptimizeNotificationForm';
import { Textarea } from '@components1/k8s/common/TextArea';
import apiAutoPilot from '@api1/autoPilot';
import { snackbar } from '@components1/common/snackbarService';
const heatMapColors = ['#FFFFFF', '#EFF6FF', '#DBEAFE', '#BFDBFE', '#93C5FD', '#60A5FA'];

function convertCPUToExpotentialValue(value) {
  if (value) {
    const shiftedNumber = value * 100;
    return shiftedNumber.toExponential(2);
  }
  return '-';
}

function convertMemoryToFormat(value) {
  if (value) {
    return formatMemory(value, 'mb', 'gb');
  }
  return '-';
}

const convertDataFormat = (data) => {
  const convertedData = data?.map((entry) => {
    const d = dayjs(entry.timestamp * 1000);
    return {
      day: d.format('D MMM (ddd)'),
      hour: d.format('HH:00'),
      value: parseFloat(entry.replicas),
      cpu: convertCPUToExpotentialValue(entry.cpu),
      memory: convertMemoryToFormat(entry.memory),
      latency: entry.latency,
      rps: convertRPSToK(entry.rps),
    };
  });

  return convertedData;
};

function convertRPSToK(value) {
  if (value && value >= 1000) {
    const roundedNumber = Math.round(value);
    return `${Math.floor(roundedNumber / 1000)}k`;
  }
  return value;
}

const organizeDataByMetrics = (data) => {
  const cpuData = [];
  const memoryData = [];
  const rpsData = [];

  data?.forEach((entry) => {
    const { day, hour, value, cpu, memory, latency, rps } = entry;
    const cpuEntry = { day, hour, value, cpu };
    const memoryEntry = { day, hour, value, memory };
    const rpsEntry = { day, hour, value, latency, rps };

    cpuData.push(cpuEntry);
    memoryData.push(memoryEntry);
    rpsData.push(rpsEntry);
  });

  return { cpuData, memoryData, rpsData };
};

const HorizontalAutoOptimizeSingleConfiguration = ({
  autoOptimizeData,
  closeAutoPilotSingleConfigModal,
  msTeamsData,
  isMsTeamsLoading,
  googleChannelList,
  isGoogleChannelsLoading,
  listAutoPilot,
  isLoading,
  setIsLoading,
  reviewAutoOptimize = false,
  approvalData = {},
  // data,
  // currentData,
}) => {
  const [activeButton, setActiveButton] = useState('');

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
  const [slackChannelName, setSlackChannelName] = useState(autoOptimizeData?.notification?.slack?.channel_name || '');
  const [isLoadingSlackChannels, setIsLoadingSlackChannels] = useState(false);
  const [msTeamName, setMsTeamName] = useState(autoOptimizeData?.notification?.ms_teams?.team_id || '');
  const [msChannelListOption, setMsChannelListOption] = useState([]);
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
  });
  const [resourceFilter, setResourceFilter] = useState(
    autoOptimizeData?.auto_optimize_resource_maps?.map((m) => m.resource_identifier) || autoOptimizeData?.resource_filter || []
  );
  const { selectedCluster } = useData();
  const [selectedHeatMapOption, setSelectedHeatMapOption] = useState(0);
  const [heatMapData, setHeatMapData] = useState([]);
  const [metricsData, setMetricsData] = useState({});
  const [reviewComment, setReviewComment] = useState('');
  useEffect(() => {
    if (msTeamName) {
      filterChannelsName(msTeamName);
    }
  }, [msTeamName, msChannelName, msTeamsData]);

  const filterChannelsName = (value) => {
    const channelValue = value;
    const selectedMsTeamsData = msTeamsData?.find((item) => item?.value === channelValue);
    if (selectedMsTeamsData) {
      setMsChannelListOption(selectedMsTeamsData?.channels?.map((channel) => ({ label: channel?.name, value: channel?.id })));
    } else {
      setMsChannelListOption([]);
    }
  };

  useEffect(() => {
    if (approvalData?.reviewer_comments) {
      setReviewComment(approvalData.reviewer_comments);
    }

    const fetchSlackChannels = async () => {
      setIsLoadingSlackChannels(true);
      try {
        const platforms = 'slack';
        const res = await apiAccount.getNotificationChannelList(platforms);
        const channelOptions = res?.data?.data?.map((item) => ({ label: item.name, value: item.id })) || [];
        setSlackChannelList(channelOptions);
      } finally {
        setIsLoadingSlackChannels(false);
      }
    };

    fetchSlackChannels();
  }, []);

  const handleCancel = () => {
    closeAutoPilotSingleConfigModal(false);
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

    if (notificationData?.google_chat && !googleChatChannelName) {
      validate.notification.google_chat = 'Select Google Chat Channel';
      valid = false;
    }

    setDisplayErrorsDesc(validate);
    return valid;
  };

  const handleCreateAutoPilotRule = () => {
    if (!validateAutoPilotRequest()) {
      return;
    }

    if (resourceFilter?.length == 0) {
      snackbar.error('Please select Namespace & Application');
      return;
    }

    if (setIsLoading) {
      setIsLoading(true);
    }

    const data = {
      account_id: autoOptimizeData?.accountId ?? autoOptimizeData?.account_id ?? selectedCluster?.value,
      category: 'horizontal_rightsize',
      resource_filter: resourceFilter,
      auto_optimize_config: {
        horizontal_rightsize: {},
      },
      schedule: {},
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
      ticket_config: {},
      dryrun: false,
    };
    if (!autoOptimizeData.id) {
      k8sApi
        .singleConfigAuotPilot(data)
        .then((res) => {
          setIsLoading(false);
          if (res?.errors) {
            snackbar.error('Error - ' + res?.errors[0]?.message);
          } else {
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
            });
            setMSChannelName('');
            setMsTeamName('');
            setSlackChannelName('');
            snackbar.success('Auto Optimize Rule Created Successfully');
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
      data.id = autoOptimizeData?.id;
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

  const handleReviewAction = (status) => {
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
            closeAutoPilotSingleConfigModal(true, 'REJECTED');
            snackbar.success('Auto Optimize Rule Rejected Successfully');
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
            closeAutoPilotSingleConfigModal(true, 'APPROVED');
            snackbar.success('Auto Optimize Rule Approved Successfully');
          } else {
            snackbar.error(`Failed to approve Auto Optimize !`);
          }
        });
    }
  };

  const autoPilotSingleConfigButton = [
    {
      label: 'Cancel',
      backgroundColor: '#3B82F6',
      onClick: handleCancel,
    },
  ];

  if (reviewAutoOptimize) {
    autoPilotSingleConfigButton.push({
      isDisabled: !Object.keys(approvalData).length || approvalData.status == 'REJECTED',
      label: 'Reject',
      backgroundColor: '#3B82F6',
      onClick: () => handleReviewAction('REJECTED'),
    });
    autoPilotSingleConfigButton.push({
      isDisabled: !Object.keys(approvalData).length || approvalData.status == 'APPROVED',
      label: 'Approve',
      backgroundColor: '#3B82F6',
      onClick: () => handleReviewAction('APPROVED'),
    });
  } else {
    autoPilotSingleConfigButton.push({
      label: !autoOptimizeData.id ? 'Create Auto Optimize Rule' : 'Update Auto Optimize Rule',
      backgroundColor: '#3B82F6',
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

  const handleHeatMapOptionClick = (option) => {
    setSelectedHeatMapOption(option.value);
    switch (option.value) {
      case 0:
        setHeatMapData(metricsData.cpu);
        break;
      case 1:
        setHeatMapData(metricsData.memory);
        break;
      case 2:
        setHeatMapData(metricsData.rps);
        break;
      default:
        break;
    }
  };

  useEffect(() => {
    if (resourceFilter.length === 0) {
      return;
    }
    if (!resourceFilter[0].name) {
      return;
    }
    setIsLoading(true);
    let account = selectedCluster?.value;
    let deployment = resourceFilter[0].name;
    let namespace = resourceFilter[0]?.namespace;

    k8sApi
      .getReplicaRightSizingData(account, namespace, deployment)
      .then((res) => {
        const _result = res?.data?.data?.metrics;
        const formate = convertDataFormat(_result);
        const { cpuData, memoryData, rpsData } = organizeDataByMetrics(formate);
        setHeatMapData(cpuData);
        setIsLoading(false);
        setMetricsData({
          cpu: cpuData,
          memory: memoryData,
          rps: rpsData,
        });
      })
      .catch(() => {
        setIsLoading(false);
      });
  }, [JSON.stringify(resourceFilter)]);

  return (
    <Box>
      <Box sx={{ marginTop: '24px' }}>
        <AutoPilotHeaderCard
          header='Historical Data'
          data={autoOptimizeData}
          setResourceFilter={setResourceFilter}
          isMultiSelect={false}
          scalingType={'horizontal'}
          reviewAutoOptimize={reviewAutoOptimize}
          workloadRequired={false}
        />
      </Box>
      <Box sx={{ display: 'flex', gap: '16px', marginTop: '16px' }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: '24px', flex: 1 }}>
          <Box sx={{ margin: '20px 0' }}>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Box sx={{ marginBottom: '10px', marginLeft: '30px' }}>
                <Typography sx={{ color: '#374151', fontSize: '14px', fontWeight: 600 }}>{'Last 7 Day Usage'}</Typography>
                <Divider sx={{ background: '#60A5FA', padding: '1px', width: '28px' }} />
              </Box>
              <CustomButtonsGroup
                options={[
                  { value: 0, text: 'CPU' },
                  { value: 1, text: 'Memory' },
                  { value: 2, text: 'RPS' },
                ]}
                selected={selectedHeatMapOption}
                onClick={handleHeatMapOptionClick}
              />
            </Box>
            <CustomHeatMap data={heatMapData} customColors={heatMapColors} selectedOption={selectedHeatMapOption} loading={isLoading ?? false} />
          </Box>
          <NotificationForm
            msTeamsData={msTeamsData}
            msChannelListOption={msChannelListOption}
            slackChannelList={slackChannelList}
            notificationData={notificationData}
            setSlackChannelName={setSlackChannelName}
            slackChannelName={slackChannelName}
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
            reviewAutoOptimize={reviewAutoOptimize}
            isLoadingSlackChannels={isLoadingSlackChannels}
            isMsTeamsLoading={isMsTeamsLoading}
            isGoogleChannelsLoading={isGoogleChannelsLoading}
          />
        </Box>
      </Box>
      {reviewAutoOptimize && (
        <Box sx={{ display: 'flex', flexDirection: 'column', marginTop: '16px' }}>
          <Textarea
            value={reviewComment}
            placeholder='Please comment if you wish to reject this PR'
            onChange={(e) => setReviewComment(e.target.value)}
            minRows={2}
            maxRows={4}
            maxLength={250}
          />
          <Box>
            {displayErrorsDesc.reviewComment ? (
              <Typography sx={{ color: 'red', fontSize: '14px', marginTop: '4px' }}>{displayErrorsDesc.reviewComment}</Typography>
            ) : null}
          </Box>
        </Box>
      )}
      <ActionButtons buttons={autoPilotSingleConfigButton} activeButton={activeButton} setActiveButton={setActiveButton} />
    </Box>
  );
};

HorizontalAutoOptimizeSingleConfiguration.propTypes = {
  autoOptimizeData: PropTypes.object.isRequired,
  closeAutoPilotSingleConfigModal: PropTypes.func,
  msTeamsData: PropTypes.array.isRequired,
  googleChannelList: PropTypes.array.isRequired,
  listAutoPilot: PropTypes.func,
  isLoading: PropTypes.bool,
  setIsLoading: PropTypes.func,
  reviewAutoOptimize: PropTypes.bool,
  approvalData: PropTypes.object,
  isMsTeamsLoading: PropTypes.bool,
  isGoogleChannelsLoading: PropTypes.bool,
  // data: PropTypes.object,
  // currentData: PropTypes.object,
};

export default HorizontalAutoOptimizeSingleConfiguration;
