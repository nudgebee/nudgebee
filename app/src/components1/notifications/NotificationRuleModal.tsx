import { Box, Button, Divider, FormHelperText, InputLabel, styled, TextField, Typography } from '@mui/material';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import CloseIcon from '@mui/icons-material/Close';
import { Modal } from '@components1/common/modal';
import React, { useEffect, useState } from 'react';
import SafeIcon from '@components1/common/SafeIcon';
import apiUser from '@api1/user';
import {
  SlackIcon,
  MSTeamsIcon,
  troubleshootIcon1,
  OptimizeIcon,
  troubleshootIconBlack,
  OptimizeIconBlack,
  SLOInspectionWhiteIcon,
  SLOInspectionBlackIcon,
  EmailIconBlack,
  EmaiIconWhite,
  GChatIcon,
  EmailIcon,
  CloudAccountIcon,
  CloudIconBlackOutline,
} from '@assets';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import apiKubernetes from '@api1/kubernetes';
import apiAutoPlaybook from '@api1/autoPlaybook';
import apiDashboard from '@api1/home';
import apiNotifications from '@api1/notification';
import CustomSwitch from '@components1/common/CustomSwitch';
import { styles } from './NotificationPopupStyles';
import apiAccount from '@api1/account';
import CustomButton from '@components1/common/NewCustomButton';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';
import { DateTimePicker } from '@mui/x-date-pickers/DateTimePicker';
import { AdapterDayjs } from '@mui/x-date-pickers/AdapterDayjs';
import dayjs from 'dayjs';
import { LocalizationProvider } from '@mui/x-date-pickers/LocalizationProvider';
import k8sApi from '@api1/kubernetes1';
import formatter from '@lib/formatter';
import { safeJSONParse, parseHttpResponseBodyMessage } from 'src/utils/common';

interface NotificationRuleModalProps {
  open: boolean;
  handleClose: () => void;
  listNotificationRules: () => void;
  notificationRuleObject: any;
  editingSource?: string;
}

const StyledTextField = styled(TextField)({
  maxWidth: '237px',
  margin: 0,
  '& .MuiInputBase-root': {
    height: '40px',
    fontSize: '14px',
  },
  '& input': {
    padding: '5.5px 14px',
  },
  '& label': {
    lineHeight: '10px !important',
    overflow: 'visible !important',
  },
  '& .MuiInputLabel-root': {
    transform: 'translate(14px, 10px) scale(1)',
  },
  '& .MuiInputLabel-root.MuiInputLabel-shrink': {
    transform: 'translate(14px, -9px) scale(0.75)',
  },
});

const isValidString = (s: string) => {
  const pattern = /^[A-Za-z0-9][\w\s_-]*$/;
  return pattern.test(s);
};

const isValidEmail = (email: string) => {
  const emailPattern = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;
  return emailPattern.test(email);
};

const CONFIGURED_COLOR = '#22C55E';

const getBadgeBorderColor = (isActive: boolean, isConfigured: boolean): string => {
  if (isActive) return colors.primary;
  if (isConfigured) return CONFIGURED_COLOR;
  return colors.border.secondary;
};

const getBadgeHoverBorderColor = (isActive: boolean, isConfigured: boolean): string => {
  if (isActive) return colors.primary;
  if (isConfigured) return CONFIGURED_COLOR;
  return colors.primary;
};

interface Option {
  value: string;
  label: string;
}

const NotificationRuleModal: React.FC<NotificationRuleModalProps> = ({
  open = false,
  handleClose,
  listNotificationRules,
  notificationRuleObject,
  editingSource,
}) => {
  const [basedOnValue, setBasedOnValue] = useState('');
  const [clusterOption, setClusterOption] = useState([]);
  const [namespaceOption, setNamespaceOption] = useState<Option[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState<string>('');
  const [msTeamsData, setMsTeamsData] = useState([]);
  const [slackChannelList, setSlackChannelList] = useState([]);
  const [selectedSlackChannel, setSelectedSlackChannel] = useState<string>('');
  const [gChatChannelList, setGChatChannelList] = useState([]);
  const [selectedGChatChannel, setSelectedGChatChannel] = useState<string>('');
  const [email, setEmail] = useState('');
  const [selectedExclusionEmails, setSelectedExclusionEmails] = useState<Option[]>([]);
  const [selectedCluster, setSelectedCluster] = useState('');
  const [msChannelId, setMSChannelId] = useState<string>('');
  const [msTeamId, setMSTeamId] = useState<string>('');
  const [nsWorkloadOptions, setNSWorkloadOptions] = useState<any>([]);
  const [selectedWorkload, setSelectedWorkload] = useState<string>('');
  const [installedPlatforms, setInstalledPlatforms] = useState<any[]>([]);
  const [notificationName, setNotificationName] = useState<string>('');
  const [aggregationKey, setAggregationKey] = useState<string>('');
  const [eventRulesOptions, setEventRulesOptions] = useState<Option[]>([]);
  const [description, setDescription] = useState<string>('');
  const [expiresAt, setExpiresAt] = useState<Date | null>(null);
  const [suppressed, setSuppressed] = useState<boolean>(false);
  const [slackToggle, setSlackToggle] = useState<boolean>(false);
  const [teamsToggle, setTeamsToggle] = useState<boolean>(false);
  const [gChatToggle, setGChatToggle] = useState<boolean>(false);
  const [emailToggle, setEmailToggle] = useState<boolean>(false);
  const [msChannelList, setMsChannelList] = useState([]);
  const [installationId, setInstallationId] = useState<any>({});
  const [loadingChannelList, setLoadingChannelList] = useState({
    slack: false,
    ms_teams: false,
    google_chat: false,
  });
  const [loadingDropdown, setLoadingDropdown] = useState({
    namespaces: false,
    clusters: false,
    applications: false,
    aggregationKey: false,
  });
  const [selectedSeverity, setSelectedSeverity] = useState<string[]>([]);
  const [selectedDelivery, setSelectedDelivery] = useState<string>('real_time');
  const [selectedFrequency, setSelectedFrequency] = useState<string>('');
  const [userEmailOptions, setUserEmailOptions] = useState<Option[]>([]);
  const [loadingUsers, setLoadingUsers] = useState<boolean>(false);
  const [activeChannel, setActiveChannel] = useState<string | null>(null);
  const showEmail = basedOnValue === 'daily_recap';

  const [errors, setErrors] = useState<any>({});
  const [isSubmitting, setIsSubmitting] = useState(false);

  const [oldState, setOldState] = useState<any>(null);

  const defaultFormState = {
    suppressed: false,
    slackToggle: false,
    selectedSlackChannel: '',
    errors: {},
    teamsToggle: false,
    msTeamId: '',
    msChannelId: '',
    gChatToggle: false,
    selectedGChatChannel: '',
    notificationName: '',
    expiresAt: null,
    description: '',
  };

  const resetToDefault = () => {
    setSuppressed(defaultFormState.suppressed);
    setSlackToggle(defaultFormState.slackToggle);
    setSelectedSlackChannel(defaultFormState.selectedSlackChannel);
    setErrors(defaultFormState.errors);
    setTeamsToggle(defaultFormState.teamsToggle);
    setMSTeamId(defaultFormState.msTeamId);
    setMSChannelId(defaultFormState.msChannelId);
    setGChatToggle(defaultFormState.gChatToggle);
    setSelectedGChatChannel(defaultFormState.selectedGChatChannel);
    setNotificationName(defaultFormState.notificationName);
    setExpiresAt(defaultFormState.expiresAt);
    setDescription(defaultFormState.description);
  };

  const storeOldStates = () => {
    setOldState(
      JSON.parse(
        JSON.stringify({
          suppressed,
          slackToggle,
          selectedSlackChannel,
          errors,
          teamsToggle,
          msTeamId,
          msChannelId,
          gChatToggle,
          selectedGChatChannel,
          notificationName,
          expiresAt,
          description,
        })
      )
    );
    resetToDefault();
  };

  const restoreOldStates = () => {
    if (!oldState) return;
    setSuppressed(oldState.suppressed);
    setErrors(oldState.errors);
    setSlackToggle(oldState.slackToggle);
    setSelectedSlackChannel(oldState.selectedSlackChannel);
    setTeamsToggle(oldState.teamsToggle);
    setMSTeamId(oldState.msTeamId);
    setMSChannelId(oldState.msChannelId);
    setGChatToggle(oldState.gChatToggle);
    setSelectedGChatChannel(oldState.selectedGChatChannel);
    setNotificationName(oldState.notificationName);
    setExpiresAt(oldState.expiresAt ? new Date(oldState.expiresAt) : null);
    setDescription(oldState.description);
  };

  const fetchDailyHighlightsData = () => {
    const query: any = {};
    query.isAccountNull = true;
    apiNotifications.getNotificationRules(query, 1, 0).then((res: any) => {
      const notificationRuleObj = res?.data?.admin_get_notification_rules_v2?.rows?.[0];
      if (notificationRuleObj) {
        notificationRuleObj.notification_rule_mappings = safeJSONParse(notificationRuleObj?.notification_rule_mappings) || [];
        fetchCluster(notificationRuleObj);
        fetchDataForEditing(open, notificationRuleObj);
      }
    });
  };
  useEffect(() => {
    if (editingSource === '') {
      if (basedOnValue === 'daily_recap') {
        fetchDailyHighlightsData();
      }
    }
  }, [basedOnValue]);

  const getClustersData = async () => {
    try {
      setLoadingDropdown((prev) => ({
        ...prev,
        clusters: true,
      }));
      const response = await apiDashboard.getCloudAccounts();
      if (response && response.length > 0) {
        let filteredResponse = response;

        if (basedOnValue === 'cloud') {
          filteredResponse = response.filter((item: any) => item.cloud_provider !== 'K8s');
        } else {
          filteredResponse = response;
        }

        const clusters = filteredResponse.map((item: any) => ({
          label: item.account_name,
          value: item.id,
        }));
        setClusterOption(clusters);
      } else {
        setClusterOption([]);
      }
    } finally {
      setLoadingDropdown((prev) => ({
        ...prev,
        clusters: false,
      }));
    }
  };

  const fetchNamespacesForCluster = async (clusterId: string) => {
    if (!clusterId) {
      setNamespaceOption([]);
      return;
    }

    try {
      setLoadingDropdown((prev) => ({
        ...prev,
        namespaces: true,
      }));
      const response: any = await apiKubernetes.getK8sNamespaces(1000, 0, { accountId: clusterId });
      const namespaces =
        response?.data?.k8s_namespaces?.map((item: any) => ({
          value: item.name,
          label: item.name,
        })) || [];

      setNamespaceOption(namespaces);
    } catch (error) {
      console.error('Error fetching namespaces:', error);
      setNamespaceOption([]);
    } finally {
      setLoadingDropdown((prev) => ({
        ...prev,
        namespaces: false,
      }));
    }
  };

  const getAllWorkloadsData = async () => {
    try {
      if (selectedNamespace) {
        const query: any = {};
        query['namespace'] = selectedNamespace;
        query['cloud_account_id'] = selectedCluster;
        query['workload_type'] = 'Deployment';
        query.isActive = true;
        setLoadingDropdown((prev) => ({
          ...prev,
          applications: true,
        }));
        apiKubernetes
          .getAllK8sWorkload(query)
          .then((response: any) => {
            const workloadNameArray: string[] = response?.data.map((item: any) => {
              return { label: item.name, value: item.name };
            });
            setNSWorkloadOptions(workloadNameArray);
          })
          .finally(() => {
            setLoadingDropdown((prev) => ({
              ...prev,
              applications: false,
            }));
          });
      }
    } finally {
      setLoadingDropdown((prev) => ({
        ...prev,
        applications: false,
      }));
    }
  };

  useEffect(() => {
    getAllWorkloadsData();
  }, [selectedNamespace]);

  const handleDateChange = (newValue: import('dayjs').Dayjs | null) => {
    if (newValue) {
      setExpiresAt(newValue.toDate());
    } else {
      setExpiresAt(null);
    }
  };

  const fetchEventRules = async (clusterId: string) => {
    try {
      setLoadingDropdown((prev) => ({
        ...prev,
        aggregationKey: true,
      }));
      const response = await k8sApi.getEventRules({ accountId: clusterId }, 500, 0);
      const eventRules =
        response.data.event_rules
          ?.filter((rule: any) => rule.alert && rule.alert.trim() !== '')
          ?.sort((a: any, b: any) => a.alert.localeCompare(b.alert))
          ?.map((rule: any) => ({
            value: rule.alert,
            label: rule.alert,
          })) || [];
      if (eventRules.length > 0) {
        setEventRulesOptions(eventRules);
      } else {
        setEventRulesOptions([]);
      }
    } finally {
      setLoadingDropdown((prev) => ({
        ...prev,
        aggregationKey: false,
      }));
    }
  };

  useEffect(() => {
    if (selectedCluster) {
      fetchEventRules(selectedCluster);
    }
  }, [selectedCluster]);

  const handleChildComponentChange = (value: string, type: string) => {
    switch (type) {
      case 'namespace':
        setSelectedNamespace(value);
        setSelectedWorkload('');
        break;
      case 'cluster':
        setSelectedCluster(value);
        setSelectedNamespace('');
        setSelectedWorkload('');
        break;
      case 'action-ms-channel-value':
        setMSChannelId(value);
        break;
      case 'action-ms-teams-value':
        setMSTeamId(value);
        break;
      case 'action-ms-teams-check': {
        const isTeamsChecked = value == 'true';
        if (!isTeamsChecked) {
          setMSChannelId('');
          setMSTeamId('');
        }
        break;
      }
      case 'action-slack-channel-value': {
        setSelectedSlackChannel(value);
        break;
      }
      case 'action-gchat-channel-value': {
        setSelectedGChatChannel(value);
        break;
      }
      case 'workload':
        setSelectedWorkload(value);
        break;

      default:
        break;
    }
  };

  useEffect(() => {
    if (selectedCluster) {
      fetchNamespacesForCluster(selectedCluster);
    } else {
      setNamespaceOption([]);
    }
  }, [selectedCluster]);

  useEffect(() => {
    let isAborted = false;

    if (open) {
      // Reset user options when modal opens to prevent stale data
      setUserEmailOptions([]);

      apiNotifications.getInstalledTools().then((res: any) => {
        if (isAborted) return;
        const platforms = res?.messaging_platforms || [];
        if (platforms && platforms.length > 0) {
          const installationId: any = {};
          for (const element of platforms) {
            if (element.platform == 'slack') {
              installationId.slack = element.id;
            } else if (element.platform == 'ms_teams') {
              installationId.ms_teams = element.id;
            } else if (element.platform == 'google_chat') {
              installationId.google_chat = element.id;
            }
          }
          setInstallationId(installationId);
          setInstalledPlatforms(platforms);
        } else {
          setInstallationId({});
          setInstalledPlatforms([]);
        }
      });
      // Fetch active users for exclusion email dropdown
      setLoadingUsers(true);
      apiUser
        .listUsers({ status: 'active' })
        .then((res: any) => {
          if (isAborted) return;
          const users = res?.data || [];
          const seenEmails = new Set<string>();
          const userOptions = users
            .filter((user: any) => user.username && isValidEmail(user.username))
            .map((user: any) => ({
              label: user.display_name || user.username,
              value: user.username,
            }))
            .filter((option: Option) => {
              if (seenEmails.has(option.value)) {
                return false;
              }
              seenEmails.add(option.value);
              return true;
            });
          setUserEmailOptions(userOptions);
        })
        .finally(() => {
          if (!isAborted) {
            setLoadingUsers(false);
          }
        });
    }

    return () => {
      isAborted = true;
    };
  }, [open]);

  useEffect(() => {
    const platforms = installedPlatforms.map((i: any) => i.platform);
    if (platforms.includes('slack')) {
      setLoadingChannelList((prev) => ({
        ...prev,
        slack: true,
      }));
      apiAutoPlaybook
        .listSlackChannels('slack')
        .then((res) => {
          const response = res?.data || [];
          const newArray = response?.map((item: any) => ({
            label: item?.name,
            value: item?.id,
          }));
          setSlackChannelList(newArray || []);
        })
        .finally(() => {
          setLoadingChannelList((prev) => ({
            ...prev,
            slack: false,
          }));
        });
    }
    if (platforms.includes('ms_teams')) {
      setLoadingChannelList((prev) => ({
        ...prev,
        ms_teams: true,
      }));
      apiAutoPlaybook
        .listMSTeamsChannels('ms_teams')
        .then((res) => {
          const response = res?.data || [];
          const newData = response?.map((item: any) => ({
            channels: item?.channels?.map((channel: any) => ({
              label: channel?.name,
              value: channel?.id,
            })),
            value: item?.id,
            label: item?.name,
          }));
          setMsTeamsData(newData || []);
        })
        .finally(() => {
          setLoadingChannelList((prev) => ({
            ...prev,
            ms_teams: false,
          }));
        });
    }
    if (platforms.includes('google_chat')) {
      setLoadingChannelList((prev) => ({
        ...prev,
        google_chat: true,
      }));
      apiAccount
        .getNotificationChannelList('google_chat')
        .then((res: any) => {
          const teamOptions = res?.data?.data?.map((item: any) => ({ label: item.name, value: item.id })) || [];
          setGChatChannelList(teamOptions || []);
        })
        .finally(() => {
          setLoadingChannelList((prev) => ({
            ...prev,
            google_chat: false,
          }));
        });
    }

    getClustersData();
  }, [installedPlatforms]);

  const fetchCluster = (notificationRuleObject: any) => {
    getClustersData();
    if (!notificationRuleObject || Object.keys(notificationRuleObject).length === 0) {
      setSelectedCluster('');
    }
  };

  const fetchDataForEditing = (open: boolean, notificationRuleObject: any) => {
    if (open && notificationRuleObject && Object.keys(notificationRuleObject).length > 0) {
      setBasedOnValue(notificationRuleObject.source);
      handleActiveChannels(notificationRuleObject.notification_rule_mappings);
      setSuppressed(notificationRuleObject.is_suppressed);
      setNotificationName(notificationRuleObject.name);
      if (notificationRuleObject.namespace) {
        setSelectedNamespace(notificationRuleObject.namespace);
      }
      if (notificationRuleObject.cluster) {
        setSelectedCluster(notificationRuleObject.account_id);
      }
      if (notificationRuleObject.workload) {
        setSelectedWorkload(notificationRuleObject.workload);
      }
      if (notificationRuleObject.description) {
        setDescription(notificationRuleObject.description);
      }
      if (notificationRuleObject.expires_at) {
        setExpiresAt(new Date(notificationRuleObject.expires_at));
      }
      if (notificationRuleObject.aggregation_key) {
        setAggregationKey(notificationRuleObject.aggregation_key);
      }
      if (notificationRuleObject.severity) {
        setSelectedSeverity(Array.isArray(notificationRuleObject.severity) ? notificationRuleObject.severity : [notificationRuleObject.severity]);
      }
      if (notificationRuleObject.delivery_mode) {
        setSelectedDelivery(notificationRuleObject.delivery_mode);
      }
      if (notificationRuleObject.frequency) {
        setSelectedFrequency(notificationRuleObject.frequency);
      }
    } else if (open) {
      setBasedOnValue('troubleshoot');
    }
  };
  useEffect(() => {
    if (basedOnValue) {
      fetchCluster(notificationRuleObject);
    }
  }, [basedOnValue, notificationRuleObject]);

  useEffect(() => {
    fetchDataForEditing(open, notificationRuleObject);
  }, [open, notificationRuleObject]);

  const clearAllAndClose = () => {
    setErrors({});
    setBasedOnValue('');
    setClusterOption([]);
    setNamespaceOption([]);
    setSelectedNamespace('');
    setSelectedWorkload('');
    setMsTeamsData([]);
    setSlackChannelList([]);
    setGChatChannelList([]);
    setSelectedGChatChannel('');
    setEmail('');
    setSelectedExclusionEmails([]);
    setUserEmailOptions([]);
    setSelectedCluster('');
    setMSChannelId('');
    setNSWorkloadOptions([]);
    setMSTeamId('');
    setSlackToggle(false);
    setTeamsToggle(false);
    setGChatToggle(false);
    setEmailToggle(false);
    setSelectedSlackChannel('');
    setAggregationKey('');
    setMsChannelList([]);
    setNotificationName('');
    setDescription('');
    setExpiresAt(null);
    setSelectedSeverity([]);
    setSelectedDelivery('real_time');
    setSelectedFrequency('');
    setLoadingDropdown({
      clusters: false,
      applications: false,
      aggregationKey: false,
      namespaces: false,
    });
    handleClose();
  };

  useEffect(() => {
    const channelList: any = msTeamsData.filter((item: any) => item.value == msTeamId);
    const allChannels: any = channelList[0];
    if (allChannels && 'channels' in allChannels) {
      setMsChannelList(allChannels?.channels || []);
    } else {
      setMsChannelList([]);
    }
  }, [msTeamId, msTeamsData]);

  const handleActiveChannels = (channelsMapping: any) => {
    channelsMapping.forEach((item: any) => {
      if (item.channels.id && item.platform == 'slack') {
        setSelectedSlackChannel(item.channels.id);
        setSlackToggle(true);
      } else if (item.channels.channels && item.platform == 'ms_teams') {
        setMSTeamId(item.channels.team_id);
        setMSChannelId(item.channels.channels[0].id);
        setTeamsToggle(true);
      }
      if (item.channels.id && item.platform == 'google_chat') {
        setSelectedGChatChannel(item.channels.id);
        setGChatToggle(true);
      } else if (item.platform == 'email') {
        // Handle new format: {"emails": [...], "exclusion_emails": [...]}
        if (item.channels.emails || item.channels.exclusion_emails) {
          if (item.channels.emails && item.channels.emails.length > 0) {
            setEmail(item.channels.emails[0]);
          }
          if (item.channels.exclusion_emails && item.channels.exclusion_emails.length > 0) {
            // Convert exclusion email strings to Option objects for multi-select
            const exclusionEmailOptions = item.channels.exclusion_emails.map((email: string) => ({
              label: email,
              value: email,
            }));
            setSelectedExclusionEmails(exclusionEmailOptions);
          }
        } else if (Array.isArray(item.channels)) {
          // Handle legacy format: array of emails
          setEmail(item.channels[0]);
        }
        setEmailToggle(true);
      }
    });
  };

  const handleSubmit = () => {
    const error: any = {};

    if (basedOnValue !== 'daily_recap' && !selectedCluster) {
      error.cluster = 'Account selection is required';
    }

    if (!notificationName) {
      error.notificationName = 'Notification rule name is required';
    }
    if (!isValidString(notificationName)) {
      error.notificationName = 'Rule name must start with letter or number and can include spaces, underscores, and hyphens.';
    }
    if (!suppressed && !slackToggle && !teamsToggle && !gChatToggle && !emailToggle) {
      error.general = 'Please enable at least one notification platform or email';
    }

    if (slackToggle && !selectedSlackChannel) {
      error.slack = 'Slack channel must be selected';
      snackbar.error('Slack channel must be selected');
    }

    if (teamsToggle && (!msTeamId || !msChannelId)) {
      error.msTeams = 'MS Teams team and channel must be selected';
      snackbar.error('MS Teams team and channel must be selected');
    }

    if (gChatToggle && !selectedGChatChannel) {
      error.gChat = 'Google Chat channel must be selected';
      snackbar.error('Google Chat channel must be selected');
    }

    if (emailToggle && email && !isValidEmail(email)) {
      error.email = 'Please enter a valid email address';
    }

    if (emailToggle && !email && selectedExclusionEmails.length === userEmailOptions.length) {
      error.noEmail = 'You must either enter an additional email address or leave at least one email unexcluded.';
    }

    if (Object.keys(error).length > 0) {
      setErrors(error);
      return;
    }

    const queryParams: any = {};
    if (selectedCluster) {
      const clusterName: any = clusterOption.filter((item: any) => {
        if (item.value == selectedCluster) {
          return item.label;
        }
      });
      queryParams.cluster = clusterName[0]?.label;
      queryParams.accountId = selectedCluster;
    }
    queryParams.ruleName = notificationName;
    queryParams.source = basedOnValue;
    queryParams.isSuppressed = suppressed;
    if (selectedNamespace) {
      queryParams.namespace = selectedNamespace;
    }
    if (selectedWorkload) {
      queryParams.workload = selectedWorkload;
    }
    if (description) {
      queryParams.description = description;
    }
    if (notificationRuleObject.id) {
      queryParams.id = notificationRuleObject.id;
    }
    if (expiresAt) {
      const expiresAtValue = formatter.formatDateWithTimezone(expiresAt);
      queryParams.expires_at = expiresAtValue;
    }
    if (aggregationKey) {
      queryParams.aggregation_key = aggregationKey;
    }
    if (basedOnValue === 'troubleshoot') {
      if (selectedSeverity.length > 0) {
        queryParams.severity = selectedSeverity[0];
      }
      if (selectedDelivery) {
        queryParams.delivery = selectedDelivery;
      }
      if (selectedDelivery === 'batch' && selectedFrequency) {
        queryParams.frequency = selectedFrequency;
      }
    }
    if (!suppressed) {
      queryParams.mappings = [];
    }

    // Prevent submission while channel lists are still loading
    const isChannelListLoading =
      (slackToggle && loadingChannelList.slack) || (teamsToggle && loadingChannelList.ms_teams) || (gChatToggle && loadingChannelList.google_chat);
    if (isChannelListLoading && !suppressed) {
      snackbar.warning('Channel data is still loading. Please wait and try again.');
      return;
    }

    // Slack mapping object
    if (selectedSlackChannel && slackToggle && !suppressed) {
      const channel = slackChannelList.find((item: any) => item.value == selectedSlackChannel);
      if (!channel) {
        snackbar.warning('Slack channel is no longer available and will be removed from this rule.');
        setSlackToggle(false);
        setSelectedSlackChannel('');
      } else {
        queryParams.mappings.push({
          channels: { name: channel['label'], id: channel['value'] },
          platform: 'slack',
          installation_id: installationId.slack,
        });
      }
    }
    // MS teams mapping object
    if (msTeamId && msChannelId && teamsToggle && !suppressed) {
      const team: any = msTeamsData.find((item: any) => item.value == msTeamId);
      if (!team || !Array.isArray(team.channels)) {
        snackbar.warning('MS Teams team is no longer available and will be removed from this rule.');
        setTeamsToggle(false);
        setMSTeamId('');
        setMSChannelId('');
      } else {
        const channel = team.channels.find((channel: any) => channel.value == msChannelId);
        if (!channel) {
          snackbar.warning('MS Teams channel no longer exists and will be removed from this rule.');
          setTeamsToggle(false);
          setMSTeamId('');
          setMSChannelId('');
        } else {
          queryParams.mappings.push({
            channels: {
              team_name: team.label,
              team_id: team.value,
              channels: [{ name: channel.label, id: channel.value }],
            },
            platform: 'ms_teams',
            installation_id: installationId.ms_teams,
          });
        }
      }
    }
    if (selectedGChatChannel && gChatToggle && !suppressed) {
      const channel = gChatChannelList.find((item: any) => item.value == selectedGChatChannel);
      if (!channel) {
        snackbar.warning('Google Chat channel is no longer available and will be removed from this rule.');
        setGChatToggle(false);
        setSelectedGChatChannel('');
      } else {
        queryParams.mappings.push({
          channels: { name: channel['label'], id: channel['value'] },
          platform: 'google_chat',
          installation_id: installationId.google_chat,
        });
      }
    }
    // email mapping object
    if ((email || selectedExclusionEmails.length > 0) && emailToggle && !suppressed) {
      const emailChannels = {
        emails: email ? [email] : [],
        exclusion_emails: selectedExclusionEmails.map((option) => option.value),
      };
      queryParams.mappings.push({
        channels: emailChannels,
        platform: 'email',
      });
    }
    if (!suppressed && queryParams.mappings.length === 0) {
      setErrors({ general: 'Please enable at least one notification platform or email' });
      return;
    }
    setIsSubmitting(true);
    try {
      apiNotifications
        .insertNotificationRule(queryParams)
        .then((res: any) => {
          if (res?.data?.data?.notification_rule_upsert_one?.error) {
            const errorMsg = res.data.data.notification_rule_upsert_one.error;
            if (typeof errorMsg === 'string' && errorMsg.toLowerCase().includes('unique constraint')) {
              snackbar.error('A notification rule with this name already exists');
            } else {
              snackbar.error(errorMsg);
            }
          } else if (res?.data?.errors) {
            const errorMsg = parseHttpResponseBodyMessage(res.data);
            if (errorMsg.toLowerCase().includes('unique constraint')) {
              snackbar.error('A notification rule with this name already exists');
            } else {
              snackbar.error(errorMsg || 'Something went wrong');
            }
          } else {
            snackbar.success(
              `Rule ${notificationRuleObject && Object.keys(notificationRuleObject).length > 0 ? 'Updated ' : 'Created '} Successfully`
            );
            clearAllAndClose();
            listNotificationRules();
          }
        })
        .finally(() => {
          setIsSubmitting(false);
        });
    } catch (err) {
      snackbar.error('Something went wrong');
      setIsSubmitting(false);
      return err;
    }
  };

  const handleNameChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value;
    setNotificationName(value);
    if (!value || !isValidString(value)) {
      setErrors((prevErrors: any) => ({
        ...prevErrors,
        notificationName: 'Rule name must start with letter or number and can include spaces, underscores, and hyphens.',
      }));
    }
  };

  const handleEmailChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value;
    setEmail(value);
    if (value && !isValidEmail(value)) {
      setErrors((prevErrors: any) => ({
        ...prevErrors,
        email: 'Please enter a valid email address',
      }));
    } else if (errors.email) {
      setErrors((prevErrors: any) => {
        const newErrors = { ...prevErrors };
        delete newErrors.email;
        return newErrors;
      });
    }
  };
  useEffect(() => {
    if (editingSource) {
      setBasedOnValue(editingSource);
    }
  }, [editingSource]);

  useEffect(() => {
    if (errors.cluster && (basedOnValue === 'daily_recap' || selectedCluster)) {
      setErrors((prev: any) => {
        const newErrors = { ...prev };
        delete newErrors.cluster;
        return newErrors;
      });
    }
  }, [selectedCluster, basedOnValue, errors.cluster]);

  useEffect(() => {
    if (errors.notificationName && notificationName && isValidString(notificationName)) {
      setErrors((prev: any) => {
        const newErrors = { ...prev };
        delete newErrors.notificationName;
        return newErrors;
      });
    }
  }, [notificationName, errors.notificationName]);

  useEffect(() => {
    const generalErrorConditionMet = !suppressed && !slackToggle && !teamsToggle && !gChatToggle && !emailToggle;
    if (errors.general && !generalErrorConditionMet) {
      setErrors((prev: any) => {
        const newErrors = { ...prev };
        delete newErrors.general;
        return newErrors;
      });
    }
  }, [suppressed, slackToggle, teamsToggle, gChatToggle, emailToggle, errors.general]);

  useEffect(() => {
    if (errors.slack && (!slackToggle || selectedSlackChannel)) {
      setErrors((prev: any) => {
        const newErrors = { ...prev };
        delete newErrors.slack;
        return newErrors;
      });
    }
  }, [selectedSlackChannel, slackToggle, errors.slack]);

  useEffect(() => {
    if (errors.msTeams && (!teamsToggle || (msTeamId && msChannelId))) {
      setErrors((prev: any) => {
        const newErrors = { ...prev };
        delete newErrors.msTeams;
        return newErrors;
      });
    }
  }, [msTeamId, msChannelId, teamsToggle, errors.msTeams]);

  useEffect(() => {
    if (errors.gChat && (!gChatToggle || selectedGChatChannel)) {
      setErrors((prev: any) => {
        const newErrors = { ...prev };
        delete newErrors.gChat;
        return newErrors;
      });
    }
  }, [selectedGChatChannel, gChatToggle, errors.gChat]);

  useEffect(() => {
    if (errors.email && (!emailToggle || (email && isValidEmail(email)))) {
      setErrors((prev: any) => {
        const newErrors = { ...prev };
        delete newErrors.email;
        return newErrors;
      });
    }
  }, [email, emailToggle, errors.email]);

  useEffect(() => {
    if (errors.noEmail && (!emailToggle || email || selectedExclusionEmails.length !== userEmailOptions.length)) {
      setErrors((prev: any) => {
        const newErrors = { ...prev };
        delete newErrors.noEmail;
        return newErrors;
      });
    }
  }, [errors.noEmail, email, selectedExclusionEmails.length, userEmailOptions.length]);

  const isEditing = !!editingSource;

  return (
    <Modal
      width='lg'
      open={open}
      handleClose={() => clearAllAndClose()}
      title={`${notificationRuleObject && Object.keys(notificationRuleObject).length > 0 ? 'Update' : 'Create'} Notification Rule`}
      contentStyles={{ padding: '0px' }}
      rightComponentOnTitle={undefined}
      loader={isSubmitting}
    >
      <Box
        sx={{
          p: '16px 24px',
          borderBottom: `1px solid ${colors.button.tertiaryBorder}`,
          boxShadow: '0px 2px 12px 2px #00000014',
          display: 'flex',
          alignItems: 'center',
          gap: '12px',
        }}
      >
        <Button
          className={basedOnValue === 'troubleshoot' ? 'active' : undefined}
          sx={styles?.tabButton}
          onClick={() => {
            if (!isEditing) {
              if (basedOnValue === 'daily_recap') {
                restoreOldStates();
              }
              setBasedOnValue('troubleshoot');
            }
          }}
          disabled={isEditing && basedOnValue !== 'troubleshoot'}
          id={'tab-troubleshoot'}
        >
          <SafeIcon src={basedOnValue === 'troubleshoot' ? troubleshootIcon1 : troubleshootIconBlack} alt='' width={20} height={20} />
          Troubleshooting
        </Button>
        <Button
          className={basedOnValue === 'optimize' ? 'active' : undefined}
          sx={styles?.tabButton}
          onClick={() => {
            if (!isEditing) {
              if (basedOnValue === 'daily_recap') {
                restoreOldStates();
              }
              setBasedOnValue('optimize');
            }
          }}
          disabled={isEditing && basedOnValue !== 'optimize'}
          id={'tab-optimize'}
        >
          <SafeIcon src={basedOnValue === 'optimize' ? OptimizeIcon : OptimizeIconBlack} alt='' width={20} height={20} />
          Optimization
        </Button>
        <Button
          className={basedOnValue === 'slo' ? 'active' : undefined}
          sx={styles?.tabButton}
          onClick={() => {
            if (!isEditing) {
              if (basedOnValue === 'daily_recap') {
                restoreOldStates();
              }
              setBasedOnValue('slo');
            }
          }}
          disabled={isEditing && basedOnValue !== 'slo'}
          id={'tab-slo'}
        >
          <SafeIcon src={basedOnValue === 'slo' ? SLOInspectionWhiteIcon : SLOInspectionBlackIcon} alt='' width={20} height={20} />
          SLO
        </Button>
        <Button
          className={basedOnValue === 'daily_recap' ? 'active' : undefined}
          sx={styles?.tabButton}
          onClick={() => {
            if (!isEditing) {
              storeOldStates();
              setBasedOnValue('daily_recap');
            }
          }}
          disabled={isEditing && basedOnValue !== 'daily_recap'}
          id={'tab-daily-recap'}
        >
          <SafeIcon src={basedOnValue === 'daily_recap' ? EmaiIconWhite : EmailIconBlack} alt='' width={20} height={20} />
          Daily Highlight
        </Button>
        <Button
          className={basedOnValue === 'cloud' ? 'active' : undefined}
          sx={styles?.tabButton}
          onClick={() => {
            if (!isEditing) {
              if (basedOnValue === 'daily_recap') {
                restoreOldStates();
              }
              setBasedOnValue('cloud');
            }
          }}
          disabled={isEditing && basedOnValue !== 'cloud'}
          id={'tab-cloud'}
        >
          <SafeIcon src={basedOnValue === 'cloud' ? CloudAccountIcon : CloudIconBlackOutline} alt='' width={20} height={20} />
          Cloud
        </Button>
      </Box>

      <Box sx={{ p: '12px 24px', display: 'flex', flexDirection: 'column', gap: '8px' }}>
        {/* Rule Name & Description */}
        <Box sx={{ display: 'flex', gap: '12px', alignItems: 'flex-start' }}>
          <Box sx={{ flex: '0 0 calc((100% - 12px) / 3)' }}>
            <InputLabel htmlFor='notification-name' sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary, mb: 0.5 }}>
              Rule Name *
            </InputLabel>
            <TextField
              sx={{
                width: '100%',
                margin: 0,
                '& input': {
                  padding: '6px 12px',
                  fontSize: '13px',
                },
              }}
              size='small'
              name='notificationName'
              value={notificationName}
              id='notificationName'
              placeholder='e.g., Prod Critical Alerts'
              onChange={handleNameChange}
              error={!!errors.notificationName}
            />
            {errors.notificationName && (
              <FormHelperText error sx={{ fontSize: '11px', mt: '2px' }}>
                {errors.notificationName}
              </FormHelperText>
            )}
          </Box>
          <Box sx={{ flex: 1 }}>
            <InputLabel sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary, mb: 0.5 }}>Description</InputLabel>
            <TextField
              sx={{
                margin: 0,
                width: '100%',
                '& input': {
                  padding: '6px 12px',
                  fontSize: '13px',
                },
              }}
              size='small'
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              id='notification-description'
              placeholder='Optional description'
            />
          </Box>
        </Box>

        {basedOnValue !== 'daily_recap' && <Divider sx={{ my: 0.5 }} />}

        {/* Scope Section */}
        {basedOnValue === 'cloud' && (
          <Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
              <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>Scope</Typography>
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>— Select cloud account</Typography>
            </Box>
            <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '12px' }}>
              <Box>
                <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Account *</InputLabel>
                <FilterDropdownButton
                  id='account-1'
                  label='Account'
                  onSelect={(event: any) => {
                    handleChildComponentChange(event.target.value, 'cluster');
                  }}
                  value={clusterOption.find((o: any) => o.value === selectedCluster) || null}
                  options={clusterOption}
                  isOptionsLoading={loadingDropdown.clusters}
                  sx={{ width: '60%' }}
                />
                {errors.cluster && <FormHelperText error>{errors.cluster}</FormHelperText>}
              </Box>
            </Box>
          </Box>
        )}
        {basedOnValue !== 'daily_recap' && basedOnValue !== 'cloud' && (
          <Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
              <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>Scope</Typography>
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>— Select account</Typography>
            </Box>
            <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '12px' }}>
              <Box>
                <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Account *</InputLabel>
                <FilterDropdownButton
                  disabled={basedOnValue == 'daily_recap'}
                  id='account-2'
                  label='Account'
                  onSelect={(event: any) => {
                    handleChildComponentChange(event.target.value, 'cluster');
                  }}
                  value={clusterOption.find((o: any) => o.value === selectedCluster) || null}
                  options={clusterOption}
                  isOptionsLoading={loadingDropdown.clusters}
                  sx={{ width: '60%' }}
                />
                {errors.cluster && <FormHelperText error>{errors.cluster}</FormHelperText>}
              </Box>
              {basedOnValue !== 'optimize' && (
                <>
                  <Box>
                    <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Namespace</InputLabel>
                    <FilterDropdownButton
                      disabled={basedOnValue == 'optimize'}
                      id='namespace'
                      label='Namespace'
                      onSelect={(event: any) => {
                        handleChildComponentChange(event.target.value, 'namespace');
                      }}
                      options={namespaceOption}
                      value={namespaceOption.find((o: any) => o.value === selectedNamespace) || null}
                      isOptionsLoading={loadingDropdown.namespaces}
                      sx={{ width: '60%' }}
                    />
                  </Box>
                  <Box>
                    <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Application</InputLabel>
                    <FilterDropdownButton
                      disabled={basedOnValue == 'optimize'}
                      id='application'
                      label='Application'
                      onSelect={(event: any) => {
                        handleChildComponentChange(event.target.value, 'workload');
                      }}
                      options={nsWorkloadOptions}
                      value={nsWorkloadOptions.find((o: any) => o.value === selectedWorkload) || null}
                      isOptionsLoading={loadingDropdown.applications}
                      sx={{ width: '60%' }}
                    />
                  </Box>
                </>
              )}
              {basedOnValue === 'troubleshoot' && (
                <Box>
                  <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Aggregation Key</InputLabel>
                  <FilterDropdownButton
                    id='aggregation-key'
                    label='Aggregation Key'
                    onSelect={(event: any) => {
                      setAggregationKey(event.target.value);
                    }}
                    value={eventRulesOptions.find((o: any) => o.value === aggregationKey) || null}
                    options={eventRulesOptions}
                    isOptionsLoading={loadingDropdown.aggregationKey}
                    sx={{ width: '60%' }}
                  />
                </Box>
              )}
              {basedOnValue === 'troubleshoot' && (
                <>
                  <Box>
                    <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Severity</InputLabel>
                    <FilterDropdownButton
                      id='severity'
                      label='Severity'
                      onSelect={(event: any) => {
                        setSelectedSeverity([event.target.value]);
                      }}
                      value={
                        [{ label: 'High', value: 'high' }].find((o) => o.value === (selectedSeverity.length > 0 ? selectedSeverity[0] : '')) || null
                      }
                      options={[{ label: 'High', value: 'high' }]}
                      sx={{ width: '60%' }}
                    />
                  </Box>
                  <Box>
                    <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Delivery</InputLabel>
                    <FilterDropdownButton
                      id='delivery'
                      label='Delivery'
                      onSelect={(event: any) => {
                        setSelectedDelivery(event.target.value);
                        if (event.target.value === 'real_time') {
                          setSelectedFrequency('');
                        }
                      }}
                      value={
                        [
                          { label: 'Real Time', value: 'real_time' },
                          { label: 'Batch', value: 'batch' },
                        ].find((o) => o.value === selectedDelivery) || null
                      }
                      options={[
                        { label: 'Real Time', value: 'real_time' },
                        { label: 'Batch', value: 'batch' },
                      ]}
                      sx={{ width: '60%' }}
                    />
                  </Box>
                </>
              )}
            </Box>
            {basedOnValue === 'troubleshoot' && selectedDelivery === 'batch' && (
              <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: '12px', mt: 1 }}>
                <Box>
                  <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Frequency</InputLabel>
                  <FilterDropdownButton
                    id='frequency'
                    label='Frequency'
                    onSelect={(event: any) => setSelectedFrequency(event.target.value)}
                    value={[{ label: 'Hourly', value: 'hourly' }].find((o) => o.value === selectedFrequency) || null}
                    options={[{ label: 'Hourly', value: 'hourly' }]}
                    sx={{ width: '60%' }}
                  />
                </Box>
              </Box>
            )}
          </Box>
        )}

        <Divider sx={{ my: 0.5 }} />

        {/* Notification Status Toggle */}
        <Box
          sx={{
            backgroundColor: suppressed ? colors.background.tertiaryLightest : colors.background.primaryLightest,
            border: `1px solid ${suppressed ? colors.border.secondary : colors.primary}`,
            padding: '10px 14px',
            borderRadius: '8px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
            <SafeIcon
              src={suppressed ? troubleshootIconBlack : troubleshootIcon1}
              alt='notification-icon'
              width={18}
              height={18}
              style={{ opacity: suppressed ? 0.5 : 1 }}
            />
            <Box>
              <Typography sx={{ fontSize: '13px', fontWeight: 600, color: suppressed ? colors.text.tertiary : colors.text.secondary }}>
                {suppressed ? 'Notifications Suppressed' : 'Notifications Active'}
              </Typography>
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>
                {suppressed ? 'Alerts for this scope are muted' : 'Alerts will be sent to selected channels'}
              </Typography>
            </Box>
          </Box>
          <CustomSwitch id={'enable-notification'} onChange={() => setSuppressed(!suppressed)} checked={!suppressed} />
        </Box>
        {/* Notification Channels - Badge Selection (Option 2B) */}
        <Box sx={{ opacity: suppressed ? 0.5 : 1, pointerEvents: suppressed ? 'none' : 'auto' }}>
          <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, mb: 1 }}>Notification Channels</Typography>

          {/* Channel Badges */}
          <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap', mb: 1.5 }}>
            {/* Slack Badge */}
            {installedPlatforms.filter((g: any) => g.platform == 'slack').length > 0 && (
              <Box
                id='slack-badge'
                onClick={() => setActiveChannel(activeChannel === 'slack' ? null : 'slack')}
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 0.5,
                  px: 1.25,
                  py: 0.5,
                  borderRadius: '16px',
                  border: `1.5px solid ${getBadgeBorderColor(activeChannel === 'slack', !!selectedSlackChannel)}`,
                  backgroundColor: activeChannel === 'slack' ? colors.background.primaryLightest : colors.background.white,
                  cursor: 'pointer',
                  transition: 'all 0.15s ease',
                  '&:hover': { borderColor: getBadgeHoverBorderColor(activeChannel === 'slack', !!selectedSlackChannel) },
                }}
              >
                <SafeIcon src={SlackIcon} alt='Slack' width={14} height={14} />
                <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary }}>Slack</Typography>
                {selectedSlackChannel && (
                  <>
                    <CheckCircleOutlineIcon sx={{ fontSize: 14, color: '#22C55E', ml: 0.25 }} />
                    <CloseIcon
                      sx={{ fontSize: 13, color: '#9CA3AF', ml: 0.25, '&:hover': { color: '#EF4444' } }}
                      onClick={(e) => {
                        e.stopPropagation();
                        setSelectedSlackChannel('');
                        setSlackToggle(false);
                        if (activeChannel === 'slack') setActiveChannel(null);
                      }}
                    />
                  </>
                )}
              </Box>
            )}

            {/* MS Teams Badge */}
            {installedPlatforms.filter((g: any) => g.platform == 'ms_teams').length > 0 && (
              <Box
                id='msteams-badge'
                onClick={() => setActiveChannel(activeChannel === 'ms_teams' ? null : 'ms_teams')}
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 0.5,
                  px: 1.25,
                  py: 0.5,
                  borderRadius: '16px',
                  border: `1.5px solid ${getBadgeBorderColor(activeChannel === 'ms_teams', !!(msTeamId && msChannelId))}`,
                  backgroundColor: activeChannel === 'ms_teams' ? colors.background.primaryLightest : colors.background.white,
                  cursor: 'pointer',
                  transition: 'all 0.15s ease',
                  '&:hover': { borderColor: getBadgeHoverBorderColor(activeChannel === 'ms_teams', !!(msTeamId && msChannelId)) },
                }}
              >
                <SafeIcon src={MSTeamsIcon} alt='MS Teams' width={14} height={14} />
                <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary }}>MS Teams</Typography>
                {msTeamId && msChannelId && (
                  <>
                    <CheckCircleOutlineIcon sx={{ fontSize: 14, color: '#22C55E', ml: 0.25 }} />
                    <CloseIcon
                      sx={{ fontSize: 13, color: '#9CA3AF', ml: 0.25, '&:hover': { color: '#EF4444' } }}
                      onClick={(e) => {
                        e.stopPropagation();
                        setMSTeamId('');
                        setMSChannelId('');
                        setTeamsToggle(false);
                        if (activeChannel === 'ms_teams') setActiveChannel(null);
                      }}
                    />
                  </>
                )}
              </Box>
            )}

            {/* Google Chat Badge */}
            {installedPlatforms.filter((g: any) => g.platform == 'google_chat').length > 0 && (
              <Box
                id='gchat-badge'
                onClick={() => setActiveChannel(activeChannel === 'google_chat' ? null : 'google_chat')}
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 0.5,
                  px: 1.25,
                  py: 0.5,
                  borderRadius: '16px',
                  border: `1.5px solid ${getBadgeBorderColor(activeChannel === 'google_chat', !!selectedGChatChannel)}`,
                  backgroundColor: activeChannel === 'google_chat' ? colors.background.primaryLightest : colors.background.white,
                  cursor: 'pointer',
                  transition: 'all 0.15s ease',
                  '&:hover': { borderColor: getBadgeHoverBorderColor(activeChannel === 'google_chat', !!selectedGChatChannel) },
                }}
              >
                <SafeIcon src={GChatIcon} alt='Google Chat' width={14} height={14} />
                <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary }}>Google Chat</Typography>
                {selectedGChatChannel && (
                  <>
                    <CheckCircleOutlineIcon sx={{ fontSize: 14, color: '#22C55E', ml: 0.25 }} />
                    <CloseIcon
                      sx={{ fontSize: 13, color: '#9CA3AF', ml: 0.25, '&:hover': { color: '#EF4444' } }}
                      onClick={(e) => {
                        e.stopPropagation();
                        setSelectedGChatChannel('');
                        setGChatToggle(false);
                        if (activeChannel === 'google_chat') setActiveChannel(null);
                      }}
                    />
                  </>
                )}
              </Box>
            )}

            {/* Email Badge - only for daily_recap */}
            {showEmail && (
              <Box
                id='email-badge'
                onClick={() => setActiveChannel(activeChannel === 'email' ? null : 'email')}
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 0.5,
                  px: 1.25,
                  py: 0.5,
                  borderRadius: '16px',
                  border: `1.5px solid ${getBadgeBorderColor(activeChannel === 'email', !!(email || selectedExclusionEmails.length > 0))}`,
                  backgroundColor: activeChannel === 'email' ? colors.background.primaryLightest : colors.background.white,
                  cursor: 'pointer',
                  transition: 'all 0.15s ease',
                  '&:hover': {
                    borderColor: getBadgeHoverBorderColor(activeChannel === 'email', !!(email || selectedExclusionEmails.length > 0)),
                  },
                }}
              >
                <SafeIcon src={EmailIcon} alt='Email' width={14} height={14} />
                <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary }}>Email</Typography>
                {(email || selectedExclusionEmails.length > 0) && (
                  <>
                    <CheckCircleOutlineIcon sx={{ fontSize: 14, color: '#22C55E', ml: 0.25 }} />
                    <CloseIcon
                      sx={{ fontSize: 13, color: '#9CA3AF', ml: 0.25, '&:hover': { color: '#EF4444' } }}
                      onClick={(e) => {
                        e.stopPropagation();
                        setEmail('');
                        setSelectedExclusionEmails([]);
                        setEmailToggle(false);
                        if (activeChannel === 'email') setActiveChannel(null);
                      }}
                    />
                  </>
                )}
              </Box>
            )}
          </Box>

          {/* Config Panel - Only for active badge */}
          {activeChannel && (
            <Box
              sx={{
                p: 2,
                backgroundColor: colors.background.tertiaryLightest,
                borderRadius: '8px',
                border: `1px solid ${colors.border.secondary}`,
              }}
            >
              {/* Slack Config */}
              {activeChannel === 'slack' && (
                <Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1.5 }}>
                    <SafeIcon src={SlackIcon} alt='Slack' width={16} height={16} />
                    <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>Slack Configuration</Typography>
                  </Box>
                  <Box sx={{ maxWidth: '320px' }}>
                    <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Channel</InputLabel>
                    <FilterDropdownButton
                      id='slack-channel'
                      label='Channel'
                      onSelect={(event: any) => {
                        handleChildComponentChange(event.target.value, 'action-slack-channel-value');
                        if (event.target.value) setSlackToggle(true);
                        else setSlackToggle(false);
                      }}
                      value={slackChannelList.find((o: any) => o.value === selectedSlackChannel) || null}
                      options={slackChannelList}
                      isOptionsLoading={loadingChannelList.slack}
                      sx={{ width: '60%' }}
                    />
                    {errors.slack && (
                      <FormHelperText error sx={{ mt: 0.5 }}>
                        {errors.slack}
                      </FormHelperText>
                    )}
                  </Box>
                </Box>
              )}

              {/* MS Teams Config */}
              {activeChannel === 'ms_teams' && (
                <Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1.5 }}>
                    <SafeIcon src={MSTeamsIcon} alt='MS Teams' width={16} height={16} />
                    <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>MS Teams Configuration</Typography>
                  </Box>
                  <Box sx={{ display: 'flex', gap: 2 }}>
                    <Box sx={{ flex: 1, maxWidth: '220px' }}>
                      <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Team</InputLabel>
                      <FilterDropdownButton
                        id='msteam-groups'
                        label='Team'
                        onSelect={(event: any) => {
                          handleChildComponentChange(event.target.value, 'action-ms-teams-value');
                          setMSChannelId('');
                          if (event.target.value) setTeamsToggle(true);
                          else setTeamsToggle(false);
                        }}
                        value={msTeamsData.find((o: any) => o.value === msTeamId) || null}
                        options={msTeamsData}
                        isOptionsLoading={loadingChannelList.ms_teams}
                        sx={{ width: '60%' }}
                      />
                    </Box>
                    <Box sx={{ flex: 1, maxWidth: '220px' }}>
                      <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Channel</InputLabel>
                      <FilterDropdownButton
                        id='msteam-channel'
                        label='Channel'
                        onSelect={(event: any) => handleChildComponentChange(event.target.value, 'action-ms-channel-value')}
                        value={msChannelList.find((o: any) => o.value === msChannelId) || null}
                        options={msChannelList}
                        disabled={!msTeamId}
                        isOptionsLoading={loadingChannelList.ms_teams}
                        sx={{ width: '60%' }}
                      />
                    </Box>
                  </Box>
                  {errors.msTeams && (
                    <FormHelperText error sx={{ mt: 0.5 }}>
                      {errors.msTeams}
                    </FormHelperText>
                  )}
                </Box>
              )}

              {/* Google Chat Config */}
              {activeChannel === 'google_chat' && (
                <Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1.5 }}>
                    <SafeIcon src={GChatIcon} alt='Google Chat' width={16} height={16} />
                    <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>Google Chat Configuration</Typography>
                  </Box>
                  <Box sx={{ maxWidth: '320px' }}>
                    <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Space</InputLabel>
                    <FilterDropdownButton
                      id='gchat-channel'
                      label='Space'
                      onSelect={(event: any) => {
                        handleChildComponentChange(event.target.value, 'action-gchat-channel-value');
                        if (event.target.value) setGChatToggle(true);
                        else setGChatToggle(false);
                      }}
                      value={gChatChannelList.find((o: any) => o.value === selectedGChatChannel) || null}
                      options={gChatChannelList}
                      isOptionsLoading={loadingChannelList.google_chat}
                      sx={{ width: '60%' }}
                    />
                    {errors.gChat && (
                      <FormHelperText error sx={{ mt: 0.5 }}>
                        {errors.gChat}
                      </FormHelperText>
                    )}
                  </Box>
                </Box>
              )}

              {/* Email Config */}
              {activeChannel === 'email' && (
                <Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1.5 }}>
                    <SafeIcon src={EmailIcon} alt='Email' width={16} height={16} />
                    <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>Email Configuration</Typography>
                  </Box>
                  <Box sx={{ display: 'flex', gap: 2 }}>
                    <Box sx={{ flex: 1, maxWidth: '240px' }}>
                      <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Additional Email</InputLabel>
                      <TextField
                        sx={{
                          width: '100%',
                          '& .MuiInputBase-root': { height: '38px' },
                          '& input': { padding: '8px 12px', fontSize: '13px' },
                        }}
                        size='small'
                        value={email || ''}
                        id='email-input'
                        placeholder='user@company.com'
                        onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
                          handleEmailChange(e);
                          if (e.target.value || selectedExclusionEmails.length > 0) setEmailToggle(true);
                          else setEmailToggle(false);
                        }}
                        error={!!errors.email}
                      />
                      {errors.email && (
                        <FormHelperText error sx={{ mt: 0.5 }}>
                          {errors.email}
                        </FormHelperText>
                      )}
                    </Box>
                    <Box sx={{ flex: 1, maxWidth: '240px' }}>
                      <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Exclude Users</InputLabel>
                      <FilterDropdownButton
                        multiple
                        value={selectedExclusionEmails}
                        options={userEmailOptions as any}
                        onSelect={(event: any) => {
                          const value = event.target.value;
                          setSelectedExclusionEmails(Array.isArray(value) ? value : []);
                          if (email || (Array.isArray(value) && value.length > 0)) setEmailToggle(true);
                          else setEmailToggle(false);
                        }}
                        label='Exclude Users'
                        limitTag={1}
                        isOptionsLoading={loadingUsers}
                      />
                      {errors.noEmail && (
                        <FormHelperText error sx={{ mt: 0.5 }}>
                          {errors.noEmail}
                        </FormHelperText>
                      )}
                    </Box>
                  </Box>
                </Box>
              )}
            </Box>
          )}

          {/* Summary when no badge is active */}
          {!activeChannel && (slackToggle || teamsToggle || gChatToggle || emailToggle) && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: 1,
                p: 1.5,
                backgroundColor: '#F0FDF4',
                borderRadius: '6px',
                border: '1px solid #BBF7D0',
              }}
            >
              <CheckCircleOutlineIcon sx={{ fontSize: 16, color: '#22C55E' }} />
              <Typography sx={{ fontSize: '12px', color: '#166534' }}>
                {[slackToggle && 'Slack', teamsToggle && 'MS Teams', gChatToggle && 'Google Chat', emailToggle && 'Email'].filter(Boolean).join(', ')}{' '}
                configured
              </Typography>
            </Box>
          )}

          {/* Empty state */}
          {!activeChannel && !slackToggle && !teamsToggle && !gChatToggle && !emailToggle && (
            <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, pl: 0.5 }}>Click a channel badge above to configure it</Typography>
          )}

          {errors.general && (
            <FormHelperText error sx={{ mt: 1 }}>
              {errors.general}
            </FormHelperText>
          )}
        </Box>

        <Divider sx={{ my: 0.5 }} />

        {/* Advanced Options */}
        <Box>
          <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, mb: 1 }}>Advanced Options</Typography>
          <Box sx={{ opacity: suppressed ? 0.5 : 1, maxWidth: 'calc(50% - 6px)' }}>
            <InputLabel sx={{ fontSize: '11px', color: colors.text.tertiary, mb: 0.5 }}>Snooze Until</InputLabel>
            <LocalizationProvider dateAdapter={AdapterDayjs}>
              <DateTimePicker
                disabled={suppressed}
                renderInput={(props) => (
                  <StyledTextField
                    {...props}
                    id='snooze-date'
                    sx={{ width: '100%', maxWidth: '100%' }}
                    onKeyDown={(e) => e.preventDefault()}
                    onPaste={(e) => e.preventDefault()}
                    onDrop={(e) => e.preventDefault()}
                  />
                )}
                value={expiresAt}
                minDate={dayjs()}
                maxDateTime={dayjs().add(1, 'year')}
                onChange={handleDateChange}
                componentsProps={{
                  actionBar: {
                    actions: ['clear', 'accept'],
                  },
                }}
              />
            </LocalizationProvider>
          </Box>
        </Box>
      </Box>
      <Box
        display='flex'
        alignItems='center'
        justifyContent='space-between'
        p='12px 24px'
        sx={{
          borderTop: `0.5px solid ${colors.border.vertical}`,
          backgroundColor: colors.background.tertiaryLightest,
        }}
      >
        <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>
          {suppressed
            ? 'Notifications will be suppressed for this scope'
            : `${[slackToggle, teamsToggle, gChatToggle, emailToggle].filter(Boolean).length} channel(s) selected`}
        </Typography>
        <Box display='flex' alignItems='center' gap='12px'>
          <CustomButton id={'cancel'} size='Medium' text='Cancel' variant='secondary' onClick={clearAllAndClose} disabled={isSubmitting} />
          <CustomButton
            text={`${notificationRuleObject && Object.keys(notificationRuleObject).length > 0 ? 'Update' : 'Create'}`}
            id={'submit'}
            onClick={handleSubmit}
            size='Medium'
            loading={isSubmitting}
          />
        </Box>
      </Box>
    </Modal>
  );
};

export default NotificationRuleModal;
