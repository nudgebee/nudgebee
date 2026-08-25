import apiAccount from '@api1/account';
import apiKubernetes1 from '@api1/kubernetes1';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import { useUpdateAllClusterOption } from '@shared/layout/UpdateDataContext';
import Datetime from '@shared/format/Datetime';
import { toast as snackbar } from '@ui/Toast';
import CustomTable from '@shared/tables/CustomTable';
import { Label } from '@ui/Label';
import { hasWriteAccess, fetchFeatureFlagsForAccount, canManage } from '@lib/auth';
import { Box, Grid, Stack, Typography } from '@mui/material';
import { Input } from '@ui/Input';
import { Link } from '@ui/Link';
import { useEffect, useRef, useState } from 'react';
import { Modal } from '@ui/Modal';
import Heading from '@components/common/Heading';
import { Divider } from '@ui/Divider';
import K8sAccountModal from '@components/integrations/modal/K8sAccountModal';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import SearchInput from '@ui/SearchInput';
import { Button as DsButton } from '@ui/Button';
import { TourLauncher } from '@components/common/tour';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import { action } from 'src/utils/actionStyles';
import { ds } from 'src/utils/colors';
import { getFeatures, updateFeatureFlagForAccount } from '@lib/UserService';
import { Checkbox } from '@ui/Checkbox';
import { parseHttpResponseBodyMessage, safeJSONParse } from 'src/utils/common';
import apiUser from '@api1/user';
import CopyButton from '@shared/buttons/CopyButton';

// Agents connect asynchronously minutes after an account is created, so the
// health columns are still empty on the fetch that follows install. Poll to
// pick them up instead of making the user reload the page (#34174).
const AGENT_POLL_MS = 30000;

const K8sIntegrationTile = () => {
  const headers = [
    'Name',
    { name: 'Status', width: '10%' },
    { name: 'Installed At', width: '10%' },
    { name: 'Last Connected At', width: '10%' },
    { name: 'Created By', width: '15%' },
    { name: 'K8s Version', width: '10%' },
    { name: 'Installed Agent Version', width: '20%' },
    '',
  ];

  const [tableData, setTableData] = useState([]);
  const [openModal, setOpenModal] = useState(false);
  const [loading, setLoading] = useState(false);
  const [accountSettings, setAccountSettings] = useState(false);
  const [selectedAccountName, setSelectedAccountName] = useState('');
  const [selectedAccountId, setSelectedAccountId] = useState('');
  const [logPodLabel, setLogPodLabel] = useState('');
  const [logNamespaceLabel, setLogNamespaceLabel] = useState('');
  const [logAppLabel, setLogAppLabel] = useState('');
  const [cloudAccountAttributes, setCloudAccountAttributes] = useState({});
  const [logDefaultQuery, setLogDefaultQuery] = useState('');
  const [certificateExpiry, setCertificateExpiry] = useState(0);
  const [networkThreshold, setNetworkThreshold] = useState(0);
  const [observationDays, setObservationDays] = useState(0);
  const [updateAccountStatus, setUpdateAccountStatus] = useState({});
  const [isStatusUpdating, setIsStatusUpdating] = useState(false);
  const [k8sCurlCommand, setK8sCurlCommand] = useState('');
  const [selectedAnomalyConfigs, setSelectedAnomalyConfigs] = useState([]);
  const [accountName, setAccountName] = useState('');
  const [nameInput, setNameInput] = useState('');
  const [selectedNameFilter, setSelectedNameFilter] = useState('');
  const [selectedStatusFilter, setSelectedStatusFilter] = useState('active');
  const [updating, setUpdating] = useState(false);
  const [featureOptions, setFeatureOptions] = useState([]);
  const [selectedFeatures, setSelectedFeatures] = useState([]);
  const [initialFeatures, setInitialFeatures] = useState([]);
  const [currentPage, setCurrentPage] = useState(0);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [totalCount, setTotalCount] = useState(0);
  const [refreshKey, setRefreshKey] = useState(0);

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const updateAllClusters = useUpdateAllClusterOption();

  // Last-known agent health per cloud_account_id. A silent poll re-renders rows
  // from this cache so the health columns don't blink back to '-' on every tick
  // while the second (health) request is in flight.
  const agentHealthRef = useRef({});

  // Bumped per request so a slow response from a superseded fetch can't clobber
  // newer data — polls and filter changes overlap. Same guard as #34351.
  const listSeqRef = useRef(0);
  const healthSeqRef = useRef(0);

  useEffect(() => {
    listK8sCloudAccount();
  }, [selectedNameFilter, selectedStatusFilter, recordsPerPage, currentPage, refreshKey]);

  useEffect(() => {
    const intervalId = setInterval(() => {
      // Hidden tabs don't need the traffic; the next visible tick refreshes.
      if (document.visibilityState === 'visible') {
        listK8sCloudAccount(true);
      }
    }, AGENT_POLL_MS);
    return () => clearInterval(intervalId);
  }, [selectedNameFilter, selectedStatusFilter, recordsPerPage, currentPage]);

  const handleStatusFilterChange = (e) => {
    setSelectedStatusFilter(e.target.value);
    setCurrentPage(0);
  };

  const statusOptions = [
    { label: 'Active', value: 'active' },
    { label: 'Disabled', value: 'disabled' },
  ];

  const getMenuItems = (item) => {
    return hasWriteAccess()
      ? [
          {
            label: 'Settings',
            id: 'settings',
          },
          {
            label: 'Re-new Token',
            id: 'renew-token',
          },
          {
            label: item.status == 'disabled' ? 'Enable' : 'Disable',
            id: 'toggle-enabled',
          },
        ]
      : [];
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 'settings') {
      setAccountSettings(true);
      setSelectedAccountName(data.account_name);
      setAccountName(data.account_name);
      setSelectedAccountId(data.id);
    } else if (menuItem.id === 'renew-token') {
      apiAccount.generateAgentToken(data.id).then((res) => {
        if (res?.data?.data?.agents_create_token?.access_secret) {
          const k8CurlCmd = `wget https://raw.githubusercontent.com/nudgebee/k8s-agent/main/installation.sh && bash installation.sh -a "${res?.data?.data?.agents_create_token?.access_key}:${res?.data?.data?.agents_create_token?.access_secret}"`;
          setK8sCurlCommand(k8CurlCmd);
        }
      });
    } else if (menuItem.id === 'toggle-enabled') {
      setUpdateAccountStatus({ name: data.account_name, id: data.id, status: data.status == 'disabled' ? 'active' : 'disabled' });
    }
  };

  const listK8sCloudAccount = (silent = false) => {
    const seq = ++listSeqRef.current;
    // A silent poll leaves the rendered table alone until its data lands —
    // clearing here would blank the table and flash the spinner every tick.
    if (!silent) {
      setLoading(true);
      setTableData([]);
      setCloudAccountAttributes({});
    }
    const accountAttr = {};
    apiKubernetes1
      .listAcc({
        nameSearch: selectedNameFilter || undefined,
        statusSearch: selectedStatusFilter || undefined,
        limit: recordsPerPage,
        offset: currentPage * recordsPerPage,
      })
      .then((res) => {
        if (seq !== listSeqRef.current) {
          return; // a newer fetch superseded this one
        }
        const cloudAccounts = res?.data?.data?.accounts_list?.rows || [];
        if (cloudAccounts && cloudAccounts.length > 0) {
          const data = cloudAccounts.map((item) => {
            accountAttr[item.id] = safeJSONParse(item.cloud_account_attrs) || [];
            const health = agentHealthRef.current[item.id];
            return [
              {
                drilldownQuery: { id: item.id },
                component: <Link href={`/kubernetes/details/${item.id}`}>{item.account_name}</Link>,
              },
              {
                component: <Label text={item.status} />,
              },
              {
                component: <Datetime value={item.created_at} />,
              },
              health?.last_connected_at ? { component: <Datetime value={health.last_connected_at} /> } : { text: '-' },
              {
                text: item?.created_by_name || '-',
              },
              {
                text: health?.k8s_version || '-',
              },
              {
                text: health?.version || '-',
              },
              {
                component: <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(item)} data={item} onMenuClick={onMenuClick} />,
              },
            ];
          });
          setTableData(data);
        } else {
          setTableData([]);
        }
        setTotalCount(res?.data?.data?.accounts_aggregate?.rows?.[0]?.count || 0);
        setCloudAccountAttributes(accountAttr);
      })
      .finally(() => {
        // Only the newest request owns the spinner, otherwise a superseded
        // response could clear a spinner the live request still needs.
        if (seq === listSeqRef.current) {
          setLoading(false);
        }
      });
  };

  useEffect(() => {
    if (Object.keys(cloudAccountAttributes).length === 0) {
      return;
    }
    const cloudAccountIds = Object.keys(cloudAccountAttributes);
    const seq = ++healthSeqRef.current;
    apiKubernetes1.listK8sAccAgentHealth(cloudAccountIds).then((res) => {
      if (seq !== healthSeqRef.current) {
        return; // a newer health fetch superseded this one
      }
      const agents = res?.data?.data?.agent || [];
      const healthByAccountId = new Map(agents.map((item) => [item.cloud_account_id, item]));

      // Reconcile the cache against this response rather than merging into it:
      // an account whose agent is gone must drop back to '-', not keep serving
      // the last value we happened to see.
      cloudAccountIds.forEach((id) => {
        const health = healthByAccountId.get(id);
        if (health) {
          agentHealthRef.current[id] = health;
        } else {
          delete agentHealthRef.current[id];
        }
      });

      setTableData((prevData) =>
        prevData.map((itemData) => {
          const item = healthByAccountId.get(itemData[0].drilldownQuery.id);
          const updatedItemData = [...itemData];
          updatedItemData[3] = item?.last_connected_at ? { component: <Datetime value={item.last_connected_at} /> } : { text: '-' };
          updatedItemData[5] = { text: item?.k8s_version || '-' };
          updatedItemData[6] = { text: item?.version || '-' };
          return updatedItemData;
        })
      );
    });
  }, [cloudAccountAttributes]);

  const fetchFeatureFlags = async (accountId) => {
    try {
      const accountFeatureFlags = await fetchFeatureFlagsForAccount(accountId);
      if (accountFeatureFlags?.length > 0) {
        const enabled = accountFeatureFlags.filter((g) => g.status === 'enabled').map((g) => g.feature_id);
        setSelectedFeatures(enabled);
        setInitialFeatures(enabled); // <-- save original state
      }
    } catch (error) {
      console.log('Failed to fetch Account Feature Flags', error);
      snackbar.error('Failed to fetch Account Feature Flags');
    }
  };

  useEffect(() => {
    if (accountSettings) {
      if (
        selectedAccountId in cloudAccountAttributes &&
        cloudAccountAttributes[selectedAccountId] &&
        cloudAccountAttributes[selectedAccountId].length > 0
      ) {
        const logLabels = cloudAccountAttributes[selectedAccountId].filter((l) => l.name == 'log_labels');
        if (logLabels && logLabels.length == 1) {
          const logLabelStr = logLabels[0].value;
          if (logLabelStr) {
            const logLabelValues = safeJSONParse(logLabelStr) || {};
            if (logLabelValues.pod) {
              setLogPodLabel(logLabelValues.pod);
            }
            if (logLabelValues.app) {
              setLogAppLabel(logLabelValues.app);
            }
            if (logLabelValues.namespace) {
              setLogNamespaceLabel(logLabelValues.namespace);
            }
            if (logLabelValues.defaultQuery) {
              setLogDefaultQuery(logLabelValues.defaultQuery);
            }
          }
        }
        const certificateExpiryValue = cloudAccountAttributes[selectedAccountId].filter((l) => l.name == 'certificate_expiry_recommendation');
        if (certificateExpiryValue && certificateExpiryValue.length == 1) {
          setCertificateExpiry(certificateExpiryValue[0].value);
        }
        const abandonedResourceConfig = cloudAccountAttributes[selectedAccountId].filter((l) => l.name == 'abandoned_resource');
        if (abandonedResourceConfig && abandonedResourceConfig.length == 1) {
          const abandonedResourceValue = safeJSONParse(abandonedResourceConfig[0].value);
          if (abandonedResourceValue) {
            setNetworkThreshold(abandonedResourceValue.network_threshold);
            setObservationDays(abandonedResourceValue.observation_days);
          }
        }
      }
      apiKubernetes1.listAnomalyTemplate().then((res) => {
        const anomalyTemplates = res?.data?.data?.anomaly_template_list?.data || [];
        if (anomalyTemplates.length > 0) {
          setSelectedAnomalyConfigs(anomalyTemplates);
        }
      });
      fetchFeatureFlags(selectedAccountId);
    }
  }, [accountSettings, selectedAccountId]);

  useEffect(() => {
    const fetchFeatures = async () => {
      try {
        const features = await getFeatures();
        setFeatureOptions(features);
      } catch (error) {
        console.log('Failed to fetch available features', error);
        snackbar.error('Failed to fetch available features');
      }
    };
    fetchFeatures();
  }, []);

  const closeModal = () => {
    setOpenModal(false);
  };

  const handleCloseAccountSettings = () => {
    setAccountSettings(false);
    setLogAppLabel('');
    setLogNamespaceLabel('');
    setLogPodLabel('');
    setLogDefaultQuery('');
    setCertificateExpiry(0);
    setNetworkThreshold(0);
    setObservationDays(0);
    setUpdating(false);
  };

  const styles = {
    label: {
      padding: `0px ${ds.space[1]}`,
      mb: ds.space[1],
      fontSize: ds.text.bodyLg,
      fontWeight: ds.weight.regular,
      color: ds.gray[700],
    },
    inputField: {
      fontSize: ds.text.bodyLg,
      '& .MuiOutlinedInput-root': {
        borderRadius: ds.radius.md,
        backgroundColor: ds.background[100],
        '&.Mui-focused fieldset': {
          borderColor: ds.blue[500],
        },
      },
      '& .MuiInputBase-input': {
        padding: `${ds.space[2]} ${ds.space[3]}`,
      },
    },
    errorText: {
      color: ds.red[500],
      fontSize: ds.text.small,
      fontWeight: ds.weight.medium,
      mt: ds.space[2],
    },
    requiredStar: {
      color: ds.red[500],
    },
  };

  const updateFeatureFlags = async () => {
    try {
      const added = selectedFeatures.filter((f) => !initialFeatures.includes(f));
      const removed = initialFeatures.filter((f) => !selectedFeatures.includes(f));

      const updatePayload = [
        ...added.map((f) => ({ feature_id: f, status: 'enabled', account_id: selectedAccountId })),
        ...removed.map((f) => ({ feature_id: f, status: 'disabled', account_id: selectedAccountId })),
      ];

      if (updatePayload.length > 0 && selectedAccountId) {
        const updateFeatureFlagResponse = await updateFeatureFlagForAccount(updatePayload);
        if (updateFeatureFlagResponse?.data?.errors) {
          snackbar.error(`Failed to save feature configuration - ${parseHttpResponseBodyMessage(updateFeatureFlagResponse.data)}`);
          return;
        }
        snackbar.success('Feature configuration saved.');
        fetchFeatureFlagsForAccount(selectedAccountId, true); // refresh cache
      }
    } catch (error) {
      console.log('error', error);
      snackbar.error('Failed to Update Account Feature Flags');
    }
  };

  const handleSubmitAccountSetting = async () => {
    setUpdating(true);
    const data = [
      {
        name: 'log_labels',
        value: JSON.stringify({
          pod: logPodLabel,
          namespace: logNamespaceLabel,
          app: logAppLabel,
          defaultQuery: logDefaultQuery,
        }),
        cloud_account_id: selectedAccountId,
      },
    ];

    if (certificateExpiry && certificateExpiry > 0) {
      data.push({
        name: 'certificate_expiry_recommendation',
        value: certificateExpiry,
        cloud_account_id: selectedAccountId,
      });
    }

    if (networkThreshold > 0 && observationDays > 0) {
      data.push({
        name: 'abandoned_resource',
        value: JSON.stringify({
          network_threshold: networkThreshold,
          observation_days: observationDays,
        }),
        cloud_account_id: selectedAccountId,
      });
    }
    try {
      const res = await apiAccount.insertAccAttr(data);

      if (res?.data?.errors?.length > 0) {
        snackbar.error('Failed to Update Account Attributes');
      } else {
        snackbar.success('Account Attributes Updated successfully');
      }
    } catch (error) {
      console.log(error);
      snackbar.error('Failed to Update Account Attributes');
    }

    if (selectedAccountName !== accountName) {
      try {
        const res = await apiAccount.updateAccount({ id: selectedAccountId }, { account_name: accountName });

        if (res?.data?.errors?.length > 0) {
          snackbar.error('Failed to Update Account Name');
        } else {
          snackbar.success('Account Name Updated successfully');
        }
      } catch (error) {
        console.log('error', error);
        snackbar.error('Failed to Update Account Name');
      }
    }
    setUpdating(false);
    handleCloseAccountSettings();
    listK8sCloudAccount();
    updateFeatureFlags();
  };

  const handleUpdateAccountStatus = () => {
    setIsStatusUpdating(true);
    apiAccount
      .updateAccount(
        {
          id: updateAccountStatus.id,
        },
        {
          status: updateAccountStatus.status,
        }
      )
      .then((res) => {
        if (res?.data?.errors?.length > 0) {
          snackbar.error('Failed to Update Account');
        } else {
          snackbar.success('Account Updated successfully');
          listK8sCloudAccount();
          updateAllClusters(true);
        }
        setUpdateAccountStatus({});
      })
      .catch(() => {
        snackbar.error('Failed to Update Account');
      })
      .finally(() => {
        setIsStatusUpdating(false);
      });
  };

  const handleOnAccountCreate = () => {
    setSelectedNameFilter('');
    setSelectedStatusFilter('active');
    setCurrentPage(0);
    setRefreshKey((prev) => prev + 1);
  };

  const additionalShowcaseAgentUpdateCmd = () => {
    return (
      <Grid
        container
        borderRadius={ds.radius.lg}
        p={ds.space[4]}
        sx={{
          margin: `${ds.space[4]} 0 ${ds.space[6]} 0px`,
          display: 'flex',
          flexDirection: 'row',
          justifyContent: 'space-between',
          border: `1px solid ${ds.gray[300]}`,
        }}
      >
        <Grid
          item
          xs={11}
          sx={{
            overflowY: 'auto',
            maxHeight: ds.space.mul(1, 25),
          }}
        >
          <Typography sx={{ color: ds.gray[500], fontSize: ds.text.bodyLg }} variant='body1' id='k8sCurlCommand'>
            {k8sCurlCommand}
          </Typography>
        </Grid>
        <Grid item xs={1}>
          <CopyButton text={k8sCurlCommand} size='md' toastMessage='Command copied to clipboard' />
        </Grid>
      </Grid>
    );
  };

  const handleK8sAccountNameChange = (value) => {
    setAccountName(value);
  };

  const handleCheckBoxChange = (value) => {
    setSelectedFeatures((prev) => {
      if (prev.includes(value)) {
        return prev.filter((f) => f !== value);
      }
      return [...prev, value];
    });
  };

  return (
    <>
      <Modal
        width='md'
        open={accountSettings}
        handleClose={handleCloseAccountSettings}
        title={'Account Setting ' + '(' + selectedAccountName + ')'}
        loader={updating}
        actionButtons={
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'flex-end',
              gap: ds.space[2],
              p: ds.space[1],
              button: {
                minWidth: ds.space.mul(1, 35),
              },
            }}
          >
            <DsButton tone='secondary' size='md' id='cancel' onClick={handleCloseAccountSettings} disabled={updating}>
              Cancel
            </DsButton>
            <DsButton id='add-selected-button' tone='primary' size='md' onClick={handleSubmitAccountSetting} loading={updating}>
              Submit
            </DsButton>
          </Box>
        }
      >
        <Box sx={{ padding: `${ds.space[3]} ${ds.space[5]} ${ds.space[3]} ${ds.space[5]}` }}>
          <Heading value='Account Name' borderWidth='md' />
          <Box display='grid' gridTemplateColumns='1fr 1fr' gap={ds.space[4]}>
            <Box display='flex' flexDirection='column'>
              <Input
                value={accountName}
                label='Account Name'
                placeholder='Account Name'
                onChange={(value) => handleK8sAccountNameChange(value)}
                disabled={!hasWriteAccess()}
              />
            </Box>
          </Box>

          <Divider color={ds.background[200]} sx={{ marginTop: ds.space[5], marginBottom: ds.space[5] }} />

          <Heading value='Log Label Mapper' borderWidth='md' />
          <Box display='grid' gridTemplateColumns='1fr 1fr' gap={ds.space[4]}>
            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>Pod</Typography>
              <Input value={logPodLabel} placeholder='Log Pod label' onChange={(value) => setLogPodLabel(value)} />
            </Box>

            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>Namespace</Typography>
              <Input value={logNamespaceLabel} placeholder='Log Namespace label' onChange={(value) => setLogNamespaceLabel(value)} />
            </Box>

            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>App</Typography>
              <Input value={logAppLabel} placeholder='Log App label' onChange={(value) => setLogAppLabel(value)} />
            </Box>

            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>Default query</Typography>
              <Input value={logDefaultQuery} placeholder='Default Query' onChange={(value) => setLogDefaultQuery(value)} />
            </Box>
          </Box>
          <Divider color={ds.background[200]} sx={{ marginTop: ds.space[5], marginBottom: ds.space[5] }} />

          <Heading value='Certificate Expiry' borderWidth='md' />
          <Box display='grid' gridTemplateColumns='1fr 1fr' gap={ds.space[4]}>
            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>Certificate Expiry</Typography>
              <Input
                value={String(certificateExpiry ?? '')}
                type='number'
                inputMode='numeric'
                onChange={(value) => setCertificateExpiry(value)}
                onKeyDown={(e) => {
                  if (e.key === '-') {
                    e.preventDefault();
                  }
                }}
              />
            </Box>
          </Box>

          <Divider color={ds.background[200]} sx={{ marginTop: ds.space[5], marginBottom: ds.space[5] }} />

          <Heading value='Abandoned App Configuration' borderWidth='md' />
          <Box display='grid' gridTemplateColumns='1fr 1fr' gap={ds.space[4]}>
            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>Network Threshold</Typography>
              <Input
                value={String(networkThreshold ?? '')}
                type='number'
                inputMode='numeric'
                onChange={(value) => setNetworkThreshold(value)}
                onKeyDown={(e) => {
                  if (e.key === '-') {
                    e.preventDefault();
                  }
                }}
              />
            </Box>
            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>Observation Days</Typography>
              <Input
                value={String(observationDays ?? '')}
                type='number'
                inputMode='numeric'
                onChange={(value) => setObservationDays(value)}
                onKeyDown={(e) => {
                  if (e.key === '-') {
                    e.preventDefault();
                  }
                }}
              />
            </Box>
          </Box>

          <Divider color={ds.background[200]} sx={{ marginTop: ds.space[5], marginBottom: ds.space[5] }} />

          {selectedAnomalyConfigs.length > 0 && <Heading value='Anomaly Configuration' borderWidth='md' />}
          {selectedAnomalyConfigs.length > 0
            ? selectedAnomalyConfigs.map((ac) => (
                <Box key={ac.title} display='flex' alignItems='center' gap={ds.space[4]} sx={{ mt: ds.space[4] }}>
                  <Box display='flex' flexDirection='column'>
                    <Typography sx={styles.label}>Type</Typography>
                    <Input value={String(ac.anomaly_type ?? '')} disabled onChange={() => {}} />
                  </Box>
                  <Box display='flex' flexDirection='column'>
                    <Typography sx={styles.label}>Operator</Typography>
                    <Input value={String(ac.change_operator ?? '')} disabled onChange={() => {}} />
                  </Box>
                  <Box display='flex' flexDirection='column'>
                    <Typography sx={styles.label}>Title</Typography>
                    <Input value={String(ac.title ?? '')} disabled onChange={() => {}} />
                  </Box>
                  <Box display='flex' flexDirection='column'>
                    <Typography sx={styles.label}>Buffer Percentage</Typography>
                    <Input value={String(ac.buffer_percentage * 100)} type='number' disabled onChange={() => {}} />
                  </Box>
                </Box>
              ))
            : null}
          <Divider color={ds.background[200]} sx={{ marginTop: ds.space[5], marginBottom: ds.space[5] }} />
          <Heading value='Feature Flag' borderWidth='md' />
          <Box
            display='grid'
            gridTemplateColumns='repeat(3, 1fr)'
            sx={{
              ml: ds.space[3],
              width: '100%',
              '& > *': {
                borderRight: `1px solid ${ds.gray[200]}`,
                borderBottom: `1px solid ${ds.gray[200]}`,
                padding: `${ds.space[3]} ${ds.space[4]}`,
                '&:nth-of-type(3n)': {
                  borderRight: 'none',
                },
                '&:nth-last-of-type(-n+3)': {
                  borderBottom: 'none',
                },
              },
            }}
          >
            {featureOptions?.map((f) => (
              <Checkbox
                key={f.value}
                size='sm'
                checked={selectedFeatures.includes(f.value)}
                label={f.description || f.value}
                onChange={() => handleCheckBoxChange(f.value)}
              />
            ))}
          </Box>
        </Box>
      </Modal>

      <Modal
        handleClose={isStatusUpdating ? () => {} : () => setUpdateAccountStatus({})}
        open={updateAccountStatus && Object.keys(updateAccountStatus).length > 0}
        title={
          <Typography component='h2' variant='h6' fontWeight={600}>
            {updateAccountStatus.status == 'active' ? 'Enable' : 'Disable'} Kubernetes Account
          </Typography>
        }
        width='md'
        loader={isStatusUpdating}
        actionButtons={
          <>
            <DsButton id='k8s-account-status-cancel-btn' tone='secondary' onClick={() => setUpdateAccountStatus({})} disabled={isStatusUpdating}>
              Cancel
            </DsButton>
            <DsButton
              tone={updateAccountStatus.status == 'active' ? 'primary' : 'danger'}
              loading={isStatusUpdating}
              onClick={handleUpdateAccountStatus}
            >
              Confirm
            </DsButton>
          </>
        }
      >
        {`Are you sure you want to ${updateAccountStatus.status == 'active' ? 'enable' : 'disable'} "${
          updateAccountStatus.name
        }" the configured Kubernetes Account?`}
      </Modal>
      <Modal
        handleClose={() => setK8sCurlCommand('')}
        title='Update the Agent'
        open={k8sCurlCommand && k8sCurlCommand.length > 0}
        width='md'
        isConfirmRequired={false}
      >
        {additionalShowcaseAgentUpdateCmd()}
      </Modal>
      <K8sAccountModal openModal={openModal} handleClose={closeModal} handleOnAccountCreate={handleOnAccountCreate} />
      <ListingLayout id='k8s-integrations'>
        <ListingLayout.Toolbar
          title={
            <Stack direction='row' alignItems='center' spacing={1}>
              <Typography color={ds.gray[700]} fontSize={ds.text.title} fontWeight={600}>
                Kubernetes
              </Typography>
              <CloudProviderIcon cloud_provider='K8S' />
            </Stack>
          }
          actions={
            canManage('integrations', 'Write') ? (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                <TourLauncher tourId='connect-cluster' label='How to connect a cluster' />
                <DsButton id='add-k8s-account' tone='primary' size='md' onClick={() => setOpenModal(true)} aria-label='Add K8s Account'>
                  Add K8s Account
                </DsButton>
              </Box>
            ) : undefined
          }
        >
          <FilterDropdown
            id='k8s-status-filter'
            label='Status'
            options={statusOptions}
            value={statusOptions.find((o) => o.value === selectedStatusFilter) ?? null}
            onSelect={(_e, item) => handleStatusFilterChange({ target: { value: item?.value || '' } })}
          />
          <SearchInput
            id='k8s-name-search'
            value={nameInput}
            onChange={(next) => {
              setNameInput((prev) => {
                if (prev.trim() !== '' && next.trim() === '') {
                  setSelectedNameFilter('');
                  setCurrentPage(0);
                }
                return next;
              });
            }}
            onEnterPress={() => {
              setSelectedNameFilter(nameInput);
              setCurrentPage(0);
            }}
            onClear={() => {
              setNameInput('');
              setSelectedNameFilter('');
              setCurrentPage(0);
            }}
            label='Enter Name'
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            stickyColumnIndex={'8'}
            loading={loading}
            tableData={tableData}
            headers={headers}
            totalRows={totalCount || tableData.length}
            rowsPerPage={recordsPerPage}
            pageNumber={currentPage + 1}
            onPageChange={onPageChange}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default K8sIntegrationTile;
