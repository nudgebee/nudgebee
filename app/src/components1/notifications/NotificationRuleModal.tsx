import { Box, Button, FormHelperText, InputLabel, Typography } from '@mui/material';
import { Input } from '@components1/ds/Input';
import { Select } from '@components1/ds/Select';
import { Divider } from '@components1/ds/Divider';
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutline';
import CloseIcon from '@mui/icons-material/Close';
import { Modal } from '@components1/ds/Modal';
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
import apiKubernetes from '@api1/kubernetes';
import apiAutoPlaybook from '@api1/autoPlaybook';
import apiDashboard from '@api1/home';
import apiNotifications from '@api1/notification';
import CustomSwitch from '@components1/common/CustomSwitch';
import { styles } from './NotificationPopupStyles';
import apiAccount from '@api1/account';
import { Button as DsButton } from '@components1/ds/Button';
import { ds } from 'src/utils/colors';
import { toast as snackbar } from '@components1/ds/Toast';
import dayjs from 'dayjs';
import CustomDateTimePicker from '@common-new/widgets/CustomDateTimePicker';
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

const isValidString = (s: string) => {
  const pattern = /^[A-Za-z0-9][\w\s_-]*$/;
  return pattern.test(s);
};

const isValidEmail = (email: string) => {
  const emailPattern = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;
  return emailPattern.test(email);
};

const CONFIGURED_COLOR = ds.green[500];

const getBadgeBorderColor = (isActive: boolean, isConfigured: boolean): string => {
  if (isActive) return ds.blue[500];
  if (isConfigured) return CONFIGURED_COLOR;
  return ds.gray[300];
};

const getBadgeHoverBorderColor = (isActive: boolean, isConfigured: boolean): string => {
  if (isActive) return ds.blue[500];
  if (isConfigured) return CONFIGURED_COLOR;
  return ds.blue[500];
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

  const visibleConfigured = [
    basedOnValue !== 'daily_recap' && slackToggle && 'Slack',
    basedOnValue !== 'daily_recap' && teamsToggle && 'MS Teams',
    basedOnValue !== 'daily_recap' && gChatToggle && 'Google Chat',
    basedOnValue === 'daily_recap' && emailToggle && 'Email',
  ].filter(Boolean) as string[];

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
    query.source = 'daily_recap';
    apiNotifications.getNotificationRules(query, 1, 0).then((res: any) => {
      const notificationRuleObj = res?.data?.notifications_list_rules?.rows?.[0];
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
    setActiveChannel(basedOnValue === 'daily_recap' ? 'email' : null);
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

    // "daily_recap" and "optimize" are tenant-wide (no account scope) — see #28130.
    if (basedOnValue !== 'daily_recap' && basedOnValue !== 'optimize' && !selectedCluster) {
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
    // "optimize" is tenant-wide (no account scope) — never send account_id/cluster,
    // even if a stale selection lingers from another tab, or its rule can never
    // match the tenant-wide optimize digest (#28130).
    if (selectedCluster && basedOnValue !== 'optimize') {
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
          if (res?.data?.data?.notifications_upsert_rule?.error) {
            const errorMsg = res.data.data.notifications_upsert_rule.error;
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

  const handleNameChange = (value: string) => {
    setNotificationName(value);
    if (!value || !isValidString(value)) {
      setErrors((prevErrors: any) => ({
        ...prevErrors,
        notificationName: 'Rule name must start with letter or number and can include spaces, underscores, and hyphens.',
      }));
    }
  };

  const handleEmailChange = (value: string) => {
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
      contentStyles={{ padding: 'var(--ds-space-1)' }}
      rightComponentOnTitle={undefined}
      loader={isSubmitting}
    >
      <Box
        sx={{
          p: 'var(--ds-space-4) var(--ds-space-5)',
          borderBottom: `1px solid ${ds.blue[400]}`,
          boxShadow: '0px 2px 12px 2px #00000014',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-3)',
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

      <Box sx={{ p: 'var(--ds-space-3) var(--ds-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
        {/* Rule Name & Description */}
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', alignItems: 'flex-start' }}>
          <Box sx={{ flex: '0 0 calc((100% - 12px) / 3)' }}>
            <InputLabel
              htmlFor='notification-name'
              sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700], mb: ds.space[1] }}
            >
              Rule Name *
            </InputLabel>
            <Input
              size='sm'
              name='notificationName'
              value={notificationName}
              id='notificationName'
              placeholder='e.g., Prod Critical Alerts'
              onChange={handleNameChange}
              error={errors.notificationName || undefined}
            />
            {/* DS Input renders its own error message; external FormHelperText removed */}
          </Box>
          <Box sx={{ flex: 1 }}>
            <InputLabel sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700], mb: ds.space[1] }}>
              Description
            </InputLabel>
            <Input size='sm' value={description} onChange={setDescription} id='notification-description' placeholder='Optional description' />
          </Box>
        </Box>

        {basedOnValue !== 'daily_recap' && basedOnValue !== 'optimize' && <Divider sx={{ my: ds.space[1] }} />}

        {/* Scope Section */}
        {basedOnValue === 'cloud' && (
          <Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[2] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                Scope
              </Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600] }}>— Select cloud account</Typography>
            </Box>
            <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 'var(--ds-space-3)' }}>
              <Box>
                <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Account *</InputLabel>
                <Box sx={{ width: '60%' }}>
                  <Select
                    required
                    id='account-1'
                    placeholder='Account'
                    onChange={(value) => {
                      handleChildComponentChange(value, 'cluster');
                    }}
                    value={selectedCluster || null}
                    options={clusterOption}
                    loading={loadingDropdown.clusters}
                    size='sm'
                  />
                </Box>
                {errors.cluster && <FormHelperText error>{errors.cluster}</FormHelperText>}
              </Box>
            </Box>
          </Box>
        )}
        {basedOnValue !== 'daily_recap' && basedOnValue !== 'cloud' && basedOnValue !== 'optimize' && (
          <Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[2] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                Scope
              </Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600] }}>— Select account</Typography>
            </Box>
            <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 'var(--ds-space-3)' }}>
              <Box>
                <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Account *</InputLabel>
                <Box sx={{ width: '60%' }}>
                  <Select
                    required
                    disabled={basedOnValue == 'daily_recap'}
                    id='account-2'
                    placeholder='Account'
                    onChange={(value) => {
                      handleChildComponentChange(value, 'cluster');
                    }}
                    value={selectedCluster || null}
                    options={clusterOption}
                    loading={loadingDropdown.clusters}
                    size='sm'
                  />
                </Box>
                {errors.cluster && <FormHelperText error>{errors.cluster}</FormHelperText>}
              </Box>
              {basedOnValue !== 'optimize' && (
                <>
                  <Box>
                    <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Namespace</InputLabel>
                    <Box sx={{ width: '60%' }}>
                      <Select
                        disabled={basedOnValue == 'optimize'}
                        id='namespace'
                        placeholder='Namespace'
                        onChange={(value) => {
                          handleChildComponentChange(value, 'namespace');
                        }}
                        options={namespaceOption}
                        value={selectedNamespace || null}
                        loading={loadingDropdown.namespaces}
                        size='sm'
                      />
                    </Box>
                  </Box>
                  <Box>
                    <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Application</InputLabel>
                    <Box sx={{ width: '60%' }}>
                      <Select
                        disabled={basedOnValue == 'optimize'}
                        id='application'
                        placeholder='Application'
                        onChange={(value) => {
                          handleChildComponentChange(value, 'workload');
                        }}
                        options={nsWorkloadOptions}
                        value={selectedWorkload || null}
                        loading={loadingDropdown.applications}
                        size='sm'
                      />
                    </Box>
                  </Box>
                </>
              )}
              {basedOnValue === 'troubleshoot' && (
                <Box>
                  <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Aggregation Key</InputLabel>
                  <Box sx={{ width: '60%' }}>
                    <Select
                      id='aggregation-key'
                      placeholder='Aggregation Key'
                      onChange={(value) => {
                        setAggregationKey(value);
                      }}
                      value={aggregationKey || null}
                      options={eventRulesOptions}
                      loading={loadingDropdown.aggregationKey}
                      size='sm'
                    />
                  </Box>
                </Box>
              )}
              {basedOnValue === 'troubleshoot' && (
                <>
                  <Box>
                    <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Severity</InputLabel>
                    <Box sx={{ width: '60%' }}>
                      <Select
                        id='severity'
                        placeholder='Severity'
                        onChange={(value) => {
                          setSelectedSeverity([value]);
                        }}
                        value={selectedSeverity.length > 0 ? selectedSeverity[0] : null}
                        options={[{ label: 'High', value: 'high' }]}
                        size='sm'
                      />
                    </Box>
                  </Box>
                  <Box>
                    <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Delivery</InputLabel>
                    <Box sx={{ width: '60%' }}>
                      <Select
                        id='delivery'
                        placeholder='Delivery'
                        onChange={(value) => {
                          setSelectedDelivery(value);
                          if (value === 'real_time') {
                            setSelectedFrequency('');
                          }
                        }}
                        value={selectedDelivery || null}
                        options={[
                          { label: 'Real Time', value: 'real_time' },
                          { label: 'Batch', value: 'batch' },
                        ]}
                        size='sm'
                      />
                    </Box>
                  </Box>
                </>
              )}
            </Box>
            {basedOnValue === 'troubleshoot' && selectedDelivery === 'batch' && (
              <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 'var(--ds-space-3)', mt: ds.space[2] }}>
                <Box>
                  <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Frequency</InputLabel>
                  <Box sx={{ width: '60%' }}>
                    <Select
                      id='frequency'
                      placeholder='Frequency'
                      onChange={(value) => setSelectedFrequency(value)}
                      value={selectedFrequency || null}
                      options={[{ label: 'Hourly', value: 'hourly' }]}
                      size='sm'
                    />
                  </Box>
                </Box>
              </Box>
            )}
          </Box>
        )}

        <Divider sx={{ my: ds.space[1] }} />

        {/* Notification Status Toggle */}
        <Box
          sx={{
            backgroundColor: suppressed ? ds.background[300] : ds.blue[100],
            border: `1px solid ${suppressed ? ds.gray[300] : ds.blue[500]}`,
            padding: 'var(--ds-space-2) var(--ds-space-3)',
            borderRadius: 'var(--ds-radius-lg)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
            <SafeIcon src={troubleshootIconBlack} alt='notification-icon' width={18} height={18} style={{ opacity: suppressed ? 0.5 : 1 }} />
            <Box>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: suppressed ? ds.gray[600] : ds.gray[700],
                }}
              >
                {suppressed ? 'Notifications Suppressed' : 'Notifications Active'}
              </Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600] }}>
                {suppressed ? 'Alerts for this scope are muted' : 'Alerts will be sent to selected channels'}
              </Typography>
            </Box>
          </Box>
          <CustomSwitch id={'enable-notification'} onChange={() => setSuppressed(!suppressed)} checked={!suppressed} />
        </Box>
        {/* Notification Channels - Badge Selection (Option 2B) */}
        <Box sx={{ opacity: suppressed ? 0.5 : 1, pointerEvents: suppressed ? 'none' : 'auto' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700], mb: ds.space[2] }}>
            Notification Channels
          </Typography>

          {/* Channel Badges */}
          <Box sx={{ display: 'flex', gap: ds.space[2], flexWrap: 'wrap', mb: ds.space[3] }}>
            {/* Slack Badge — hidden for daily_recap (email-only) */}
            {basedOnValue !== 'daily_recap' && installedPlatforms.filter((g: any) => g.platform == 'slack').length > 0 && (
              <Box
                id='slack-badge'
                onClick={() => setActiveChannel(activeChannel === 'slack' ? null : 'slack')}
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: ds.space[1],
                  px: ds.space.mul(0, 5),
                  py: ds.space[1],
                  borderRadius: 'var(--ds-radius-xl)',
                  border: `1.5px solid ${getBadgeBorderColor(activeChannel === 'slack', !!selectedSlackChannel)}`,
                  backgroundColor: activeChannel === 'slack' ? ds.blue[100] : ds.background[100],
                  cursor: 'pointer',
                  transition: 'all 0.15s ease',
                  '&:hover': { borderColor: getBadgeHoverBorderColor(activeChannel === 'slack', !!selectedSlackChannel) },
                }}
              >
                <SafeIcon src={SlackIcon} alt='Slack' width={14} height={14} />
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] }}>
                  Slack
                </Typography>
                {selectedSlackChannel && (
                  <>
                    <CheckCircleOutlineIcon sx={{ fontSize: 14, color: 'var(--ds-green-500)', ml: ds.space[0] }} />
                    <CloseIcon
                      sx={{ fontSize: 13, color: 'var(--ds-gray-400)', ml: ds.space[0], '&:hover': { color: 'var(--ds-red-500)' } }}
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

            {/* MS Teams Badge — hidden for daily_recap (email-only) */}
            {basedOnValue !== 'daily_recap' && installedPlatforms.filter((g: any) => g.platform == 'ms_teams').length > 0 && (
              <Box
                id='msteams-badge'
                onClick={() => setActiveChannel(activeChannel === 'ms_teams' ? null : 'ms_teams')}
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: ds.space[1],
                  px: ds.space.mul(0, 5),
                  py: ds.space[1],
                  borderRadius: 'var(--ds-radius-xl)',
                  border: `1.5px solid ${getBadgeBorderColor(activeChannel === 'ms_teams', !!(msTeamId && msChannelId))}`,
                  backgroundColor: activeChannel === 'ms_teams' ? ds.blue[100] : ds.background[100],
                  cursor: 'pointer',
                  transition: 'all 0.15s ease',
                  '&:hover': { borderColor: getBadgeHoverBorderColor(activeChannel === 'ms_teams', !!(msTeamId && msChannelId)) },
                }}
              >
                <SafeIcon src={MSTeamsIcon} alt='MS Teams' width={14} height={14} />
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] }}>
                  MS Teams
                </Typography>
                {msTeamId && msChannelId && (
                  <>
                    <CheckCircleOutlineIcon sx={{ fontSize: 14, color: 'var(--ds-green-500)', ml: ds.space[0] }} />
                    <CloseIcon
                      sx={{ fontSize: 13, color: 'var(--ds-gray-400)', ml: ds.space[0], '&:hover': { color: 'var(--ds-red-500)' } }}
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

            {/* Google Chat Badge — hidden for daily_recap (email-only) */}
            {basedOnValue !== 'daily_recap' && installedPlatforms.filter((g: any) => g.platform == 'google_chat').length > 0 && (
              <Box
                id='gchat-badge'
                onClick={() => setActiveChannel(activeChannel === 'google_chat' ? null : 'google_chat')}
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: ds.space[1],
                  px: ds.space.mul(0, 5),
                  py: ds.space[1],
                  borderRadius: 'var(--ds-radius-xl)',
                  border: `1.5px solid ${getBadgeBorderColor(activeChannel === 'google_chat', !!selectedGChatChannel)}`,
                  backgroundColor: activeChannel === 'google_chat' ? ds.blue[100] : ds.background[100],
                  cursor: 'pointer',
                  transition: 'all 0.15s ease',
                  '&:hover': { borderColor: getBadgeHoverBorderColor(activeChannel === 'google_chat', !!selectedGChatChannel) },
                }}
              >
                <SafeIcon src={GChatIcon} alt='Google Chat' width={14} height={14} />
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] }}>
                  Google Chat
                </Typography>
                {selectedGChatChannel && (
                  <>
                    <CheckCircleOutlineIcon sx={{ fontSize: 14, color: 'var(--ds-green-500)', ml: ds.space[0] }} />
                    <CloseIcon
                      sx={{ fontSize: 13, color: 'var(--ds-gray-400)', ml: ds.space[0], '&:hover': { color: 'var(--ds-red-500)' } }}
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
                  gap: ds.space[1],
                  px: ds.space.mul(0, 5),
                  py: ds.space[1],
                  borderRadius: 'var(--ds-radius-xl)',
                  border: `1.5px solid ${getBadgeBorderColor(activeChannel === 'email', !!(email || selectedExclusionEmails.length > 0))}`,
                  backgroundColor: activeChannel === 'email' ? ds.blue[100] : ds.background[100],
                  cursor: 'pointer',
                  transition: 'all 0.15s ease',
                  '&:hover': {
                    borderColor: getBadgeHoverBorderColor(activeChannel === 'email', !!(email || selectedExclusionEmails.length > 0)),
                  },
                }}
              >
                <SafeIcon src={EmailIcon} alt='Email' width={14} height={14} />
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] }}>
                  Email
                </Typography>
                {(email || selectedExclusionEmails.length > 0) && (
                  <>
                    <CheckCircleOutlineIcon sx={{ fontSize: 14, color: 'var(--ds-green-500)', ml: ds.space[0] }} />
                    <CloseIcon
                      sx={{ fontSize: 13, color: 'var(--ds-gray-400)', ml: ds.space[0], '&:hover': { color: 'var(--ds-red-500)' } }}
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
                p: ds.space[4],
                backgroundColor: ds.background[300],
                borderRadius: 'var(--ds-radius-lg)',
                border: `1px solid ${ds.gray[300]}`,
              }}
            >
              {/* Slack Config */}
              {activeChannel === 'slack' && (
                <Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[3] }}>
                    <SafeIcon src={SlackIcon} alt='Slack' width={16} height={16} />
                    <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                      Slack Configuration
                    </Typography>
                  </Box>
                  <Box sx={{ maxWidth: ds.space.mul(1, 80) }}>
                    <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Channel</InputLabel>
                    <Box sx={{ width: '60%' }}>
                      <Select
                        id='slack-channel'
                        placeholder='Channel'
                        onChange={(value) => {
                          handleChildComponentChange(value, 'action-slack-channel-value');
                          if (value) setSlackToggle(true);
                          else setSlackToggle(false);
                        }}
                        value={selectedSlackChannel || null}
                        options={slackChannelList}
                        loading={loadingChannelList.slack}
                        size='sm'
                      />
                    </Box>
                    {errors.slack && (
                      <FormHelperText error sx={{ mt: ds.space[1] }}>
                        {errors.slack}
                      </FormHelperText>
                    )}
                  </Box>
                </Box>
              )}

              {/* MS Teams Config */}
              {activeChannel === 'ms_teams' && (
                <Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[3] }}>
                    <SafeIcon src={MSTeamsIcon} alt='MS Teams' width={16} height={16} />
                    <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                      MS Teams Configuration
                    </Typography>
                  </Box>
                  <Box sx={{ display: 'flex', gap: ds.space[4] }}>
                    <Box sx={{ flex: 1, maxWidth: ds.space.mul(1, 55) }}>
                      <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Team</InputLabel>
                      <Box sx={{ width: '60%' }}>
                        <Select
                          id='msteam-groups'
                          placeholder='Team'
                          onChange={(value) => {
                            handleChildComponentChange(value, 'action-ms-teams-value');
                            setMSChannelId('');
                            if (value) setTeamsToggle(true);
                            else setTeamsToggle(false);
                          }}
                          value={msTeamId || null}
                          options={msTeamsData}
                          loading={loadingChannelList.ms_teams}
                          size='sm'
                        />
                      </Box>
                    </Box>
                    <Box sx={{ flex: 1, maxWidth: ds.space.mul(1, 55) }}>
                      <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Channel</InputLabel>
                      <Box sx={{ width: '60%' }}>
                        <Select
                          id='msteam-channel'
                          placeholder='Channel'
                          onChange={(value) => handleChildComponentChange(value, 'action-ms-channel-value')}
                          value={msChannelId || null}
                          options={msChannelList}
                          disabled={!msTeamId}
                          loading={loadingChannelList.ms_teams}
                          size='sm'
                        />
                      </Box>
                    </Box>
                  </Box>
                  {errors.msTeams && (
                    <FormHelperText error sx={{ mt: ds.space[1] }}>
                      {errors.msTeams}
                    </FormHelperText>
                  )}
                </Box>
              )}

              {/* Google Chat Config */}
              {activeChannel === 'google_chat' && (
                <Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[3] }}>
                    <SafeIcon src={GChatIcon} alt='Google Chat' width={16} height={16} />
                    <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                      Google Chat Configuration
                    </Typography>
                  </Box>
                  <Box sx={{ maxWidth: ds.space.mul(1, 80) }}>
                    <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Space</InputLabel>
                    <Box sx={{ width: '60%' }}>
                      <Select
                        id='gchat-channel'
                        placeholder='Space'
                        onChange={(value) => {
                          handleChildComponentChange(value, 'action-gchat-channel-value');
                          if (value) setGChatToggle(true);
                          else setGChatToggle(false);
                        }}
                        value={selectedGChatChannel || null}
                        options={gChatChannelList}
                        loading={loadingChannelList.google_chat}
                        size='sm'
                      />
                    </Box>
                    {errors.gChat && (
                      <FormHelperText error sx={{ mt: ds.space[1] }}>
                        {errors.gChat}
                      </FormHelperText>
                    )}
                  </Box>
                </Box>
              )}

              {/* Email Config */}
              {activeChannel === 'email' && (
                <Box>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[3] }}>
                    <SafeIcon src={EmailIcon} alt='Email' width={16} height={16} />
                    <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700] }}>
                      Email Configuration
                    </Typography>
                  </Box>
                  <Box sx={{ display: 'flex', gap: ds.space[4] }}>
                    <Box sx={{ flex: 1, maxWidth: ds.space.mul(1, 60) }}>
                      <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Additional Email</InputLabel>
                      <Input
                        size='sm'
                        value={email || ''}
                        id='email-input'
                        placeholder='user@company.com'
                        onChange={(value: string) => {
                          handleEmailChange(value);
                          if (value || selectedExclusionEmails.length > 0) setEmailToggle(true);
                          else setEmailToggle(false);
                        }}
                        error={errors.email || undefined}
                      />
                      {/* DS Input renders its own error message; external FormHelperText removed */}
                    </Box>
                    <Box sx={{ flex: 1, maxWidth: ds.space.mul(1, 60) }}>
                      <InputLabel sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], mb: ds.space[1] }}>Exclude Users</InputLabel>
                      <Select
                        multiple
                        value={selectedExclusionEmails.map((o) => o.value)}
                        options={userEmailOptions as any}
                        onChange={(value) => {
                          const selected = userEmailOptions.filter((o) => value.includes(o.value));
                          setSelectedExclusionEmails(selected);
                          if (email || selected.length > 0) setEmailToggle(true);
                          else setEmailToggle(false);
                        }}
                        placeholder='Exclude Users'
                        maxChips={1}
                        loading={loadingUsers}
                        size='sm'
                      />
                      {errors.noEmail && (
                        <FormHelperText error sx={{ mt: ds.space[1] }}>
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
          {!activeChannel && visibleConfigured.length > 0 && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: ds.space[2],
                p: ds.space[3],
                backgroundColor: 'var(--ds-green-100)',
                borderRadius: 'var(--ds-radius-md)',
                border: '1px solid var(--ds-green-200)',
              }}
            >
              <CheckCircleOutlineIcon sx={{ fontSize: 16, color: 'var(--ds-green-500)' }} />
              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-green-700)' }}>
                {visibleConfigured.join(', ')} configured
              </Typography>
            </Box>
          )}

          {/* Empty state */}
          {!activeChannel && visibleConfigured.length === 0 && (
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], pl: ds.space[1] }}>
              Click a channel badge above to configure it
            </Typography>
          )}

          {errors.general && (
            <FormHelperText error sx={{ mt: ds.space[2] }}>
              {errors.general}
            </FormHelperText>
          )}
        </Box>

        <Divider sx={{ my: ds.space[1] }} />

        {/* Advanced Options */}
        <Box>
          <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.gray[700], mb: ds.space[2] }}>
            Advanced Options
          </Typography>
          <Box sx={{ opacity: suppressed ? 0.5 : 1, maxWidth: 'calc(50% - 6px)' }}>
            <CustomDateTimePicker
              id='snooze-date'
              label='Snooze Until'
              disabled={suppressed}
              value={expiresAt}
              onChange={handleDateChange}
              minDate={dayjs()}
              maxDateTime={dayjs().add(1, 'year')}
              preventDirectInput
              componentsProps={{
                actionBar: {
                  actions: ['clear', 'accept'],
                },
              }}
            />
          </Box>
        </Box>
      </Box>
      <Box
        display='flex'
        alignItems='center'
        justifyContent='space-between'
        p={`${ds.space[3]} ${ds.space[5]}`}
        sx={{
          borderTop: `0.5px solid ${ds.gray[200]}`,
          backgroundColor: ds.background[300],
        }}
      >
        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }}>
          {suppressed ? 'Notifications will be suppressed for this scope' : `${visibleConfigured.length} channel(s) selected`}
        </Typography>
        <Box display='flex' alignItems='center' gap={ds.space[3]}>
          <DsButton id='cancel' size='md' tone='secondary' onClick={clearAllAndClose} disabled={isSubmitting}>
            Cancel
          </DsButton>
          <DsButton id='submit' size='md' onClick={handleSubmit} loading={isSubmitting}>
            {notificationRuleObject && Object.keys(notificationRuleObject).length > 0 ? 'Update' : 'Create'}
          </DsButton>
        </Box>
      </Box>
    </Modal>
  );
};

export default NotificationRuleModal;
