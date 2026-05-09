import apiAccount from '@api1/account';
import apiKubernetes1 from '@api1/kubernetes1';
import { ThreeDotsMenu } from '@components1/common';
import { useUpdateAllClusterOption } from '@components1/common/UpdateDataContext';
import Datetime from '@components1/common/format/Datetime';
import { snackbar } from '@components1/common/snackbarService';
import CustomTable from '@components1/common/tables/CustomTable2';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { hasWriteAccess, fetchFeatureFlagsForAccount } from '@lib/auth';
import { Box, Grid, Stack, TextField, Typography } from '@mui/material';
import { useRouter } from 'next/navigation';
import { useEffect, useState } from 'react';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
import TextWithBorder from '@components1/common/TextWithBorder';
import CustomDivider from '@components1/common/CustomDivider';
import NDialog from '@components1/common/modal/NDialog';
import K8sAccountModal from '@components1/common/K8sAccountModal';
import BoxLayout2 from '@components1/common/BoxLayout2';
import { colors } from 'src/utils/colors';
import CloudProviderIcon from '@components1/common/CloudIcon';
import { action } from 'src/utils/actionStyles';
import { getFeatures, updateFeatureFlagForAccount } from '@lib/UserService';
import CustomCheckBox from '@components1/common/CustomCheckbox';
import { parseHttpResponseBodyMessage, safeJSONParse } from 'src/utils/common';
import apiUser from '@api1/user';
import CopyableText from '@components1/common/CopyableText';

const K8sIntegrationTile = () => {
  const router = useRouter();
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

  useEffect(() => {
    listK8sCloudAccount();
  }, [selectedNameFilter, selectedStatusFilter, recordsPerPage, currentPage, refreshKey]);

  const handleNameFilterChange = (e) => {
    setNameInput(e.target.value);
  };

  const handleStatusFilterChange = (e) => {
    setSelectedStatusFilter(e.target.value);
    setCurrentPage(0);
  };

  const onEnterPress = () => {
    setSelectedNameFilter(nameInput);
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
            id: 0,
          },
          {
            label: 'Re-new Token',
            id: 1,
          },
          {
            label: item.status == 'disabled' ? 'Enable' : 'Disable',
            id: 2,
          },
        ]
      : [];
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setAccountSettings(true);
      setSelectedAccountName(data.account_name);
      setAccountName(data.account_name);
      setSelectedAccountId(data.id);
    } else if (menuItem.id === 1) {
      apiAccount.generateAgentToken(data.id).then((res) => {
        if (res?.data?.data?.agent_token_create?.access_secret) {
          const k8CurlCmd = `wget https://raw.githubusercontent.com/nudgebee/k8s-agent/main/installation.sh && bash installation.sh -a "${res?.data?.data?.agent_token_create?.access_key}:${res?.data?.data?.agent_token_create?.access_secret}"`;
          setK8sCurlCommand(k8CurlCmd);
        }
      });
    } else if (menuItem.id === 2) {
      setUpdateAccountStatus({ name: data.account_name, id: data.id, status: data.status == 'disabled' ? 'active' : 'disabled' });
    }
  };

  const listK8sCloudAccount = () => {
    setLoading(true);
    setTableData([]);
    const accountAttr = {};
    setCloudAccountAttributes({});
    apiKubernetes1
      .listAcc({
        nameSearch: selectedNameFilter || undefined,
        statusSearch: selectedStatusFilter || undefined,
        limit: recordsPerPage,
        offset: currentPage * recordsPerPage,
      })
      .then((res) => {
        const cloudAccounts = res?.data?.data?.get_cloud_accounts_v2?.rows || [];
        if (cloudAccounts && cloudAccounts.length > 0) {
          const data = cloudAccounts.map((item) => {
            accountAttr[item.id] = safeJSONParse(item.cloud_account_attrs) || [];
            return [
              {
                drilldownQuery: { id: item.id },
                component: (
                  <Typography
                    variant='body2'
                    onClick={() => router.push(`/kubernetes/details/${item.id}`)}
                    sx={{
                      color: 'link',
                      cursor: 'pointer',
                      textDecoration: 'none',
                      '&:hover': {
                        textDecoration: 'underline',
                      },
                    }}
                  >
                    {item.account_name}
                  </Typography>
                ),
              },
              {
                component: <CustomLabels text={item.status} />,
              },
              {
                component: <Datetime value={item.created_at} />,
              },
              {
                text: '-',
              },
              {
                text: item?.created_by_name || '-',
              },
              {
                text: '-',
              },
              {
                text: '-',
              },
              {
                component: <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(item)} data={item} onMenuClick={onMenuClick} />,
              },
            ];
          });
          setTableData(data);
        }
        setTotalCount(res?.data?.data?.get_cloud_accounts_grouping_v2?.rows?.[0]?.count || 0);
        setCloudAccountAttributes(accountAttr);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    if (Object.keys(cloudAccountAttributes).length === 0) {
      return;
    }
    const cloudAccountIds = Object.keys(cloudAccountAttributes);
    apiKubernetes1.listK8sAccAgentHealth(cloudAccountIds).then((res) => {
      if (res && res?.data?.data?.agent.length > 0) {
        setTableData((prevData) =>
          prevData.map((itemData) => {
            const item = res?.data?.data?.agent.find((i) => i.cloud_account_id === itemData[0].drilldownQuery.id);
            if (!item) {
              return itemData;
            }

            const updatedItemData = [...itemData];
            updatedItemData[3] = { component: <Datetime value={item.last_connected_at} /> };
            updatedItemData[5] = { text: item.k8s_version || '-' };
            updatedItemData[6] = { text: item.version || '-' };
            return updatedItemData;
          })
        );
      }
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
            const logLabelValues = JSON.parse(logLabelStr);
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
          const abandonedResourceValue = JSON.parse(abandonedResourceConfig[0].value);
          setNetworkThreshold(abandonedResourceValue.network_threshold);
          setObservationDays(abandonedResourceValue.observation_days);
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
      padding: '0px 2px',
      mb: '4px',
      fontSize: '14px',
      fontWeight: 400,
      color: colors.text.secondary,
    },
    inputField: {
      fontSize: '14px',
      '& .MuiOutlinedInput-root': {
        borderRadius: '6px',
        backgroundColor: 'white',
        '&.Mui-focused fieldset': {
          borderColor: colors.border.primary,
        },
      },
      '& .MuiInputBase-input': {
        padding: '8px 12px',
      },
    },
    errorText: {
      color: colors.highest,
      fontSize: '12px',
      fontWeight: 500,
      mt: 1,
    },
    requiredStar: {
      color: colors.highest,
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
        borderRadius={2}
        p={2}
        sx={{
          margin: '15px 0 30px 0px',
          display: 'flex',
          flexDirection: 'row',
          justifyContent: 'space-between',
          border: `1px solid ${colors.border.secondary}`,
        }}
      >
        <Grid
          item
          xs={11}
          sx={{
            overflowY: 'auto',
            maxHeight: '100px',
          }}
        >
          <Typography sx={{ color: colors.text.secondaryDark, fontSize: '14px' }} variant='body1' id='k8sCurlCommand'>
            {k8sCurlCommand}
          </Typography>
        </Grid>
        <Grid item xs={1}>
          <CopyableText copyableText={k8sCurlCommand} iconOnly={true} iconSize={16} snackbarMessage='Command copied to clipboard' />
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
              gap: '8px',
              p: '6px',
              button: {
                minWidth: '140px',
              },
            }}
          >
            <CustomButton
              variant='secondary'
              size='Medium'
              id='cancel'
              text='Cancel'
              type='button'
              onClick={handleCloseAccountSettings}
              disabled={updating}
            />
            <CustomButton
              id='add-selected-button'
              text='Submit'
              type='button'
              size='Medium'
              onClick={handleSubmitAccountSetting}
              loading={updating}
            />
          </Box>
        }
      >
        <Box sx={{ padding: '12px 24px 12px 24px' }}>
          <TextWithBorder
            value='Account Name'
            borderColor={colors.border.primary}
            borderWidth='3px'
            sx={{ '& p': { fontSize: '16px', fontWeight: 500, color: colors.text.secondary, marginBottom: '18px' } }}
          />
          <Box display='grid' gridTemplateColumns='1fr 1fr' gap='16px'>
            <Box display='flex' flexDirection='column'>
              <TextField
                fullWidth
                value={accountName}
                label='Account Name'
                placeholder='Account Name'
                onChange={(e) => handleK8sAccountNameChange(e.target.value)}
                variant='outlined'
                sx={styles.inputField}
                disabled={!hasWriteAccess()}
              />
            </Box>
          </Box>

          <CustomDivider borderColor={colors.background.tertiaryLightestest} margin={'24px 0px'} />

          <TextWithBorder
            value='Log Label Mapper'
            borderColor={colors.border.primary}
            borderWidth='3px'
            sx={{ '& p': { fontSize: '16px', fontWeight: 500, color: colors.text.secondary, marginBottom: '18px' } }}
          />
          <Box display='grid' gridTemplateColumns='1fr 1fr' gap='16px'>
            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>Pod</Typography>
              <TextField
                fullWidth
                value={logPodLabel}
                placeholder='Log Pod label'
                onChange={(e) => setLogPodLabel(e.target.value)}
                variant='outlined'
                sx={styles.inputField}
              />
            </Box>

            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>Namespace</Typography>
              <TextField
                fullWidth
                value={logNamespaceLabel}
                placeholder='Log Namespace label'
                onChange={(e) => setLogNamespaceLabel(e.target.value)}
                variant='outlined'
                sx={styles.inputField}
              />
            </Box>

            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>App</Typography>
              <TextField
                fullWidth
                value={logAppLabel}
                placeholder='Log App label'
                onChange={(e) => setLogAppLabel(e.target.value)}
                variant='outlined'
                sx={styles.inputField}
              />
            </Box>

            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>Default query</Typography>
              <TextField
                fullWidth
                value={logDefaultQuery}
                placeholder='Default Query'
                onChange={(e) => setLogDefaultQuery(e.target.value)}
                variant='outlined'
                sx={styles.inputField}
              />
            </Box>
          </Box>
          <CustomDivider borderColor={colors.background.tertiaryLightestest} margin={'24px 0px'} />

          <TextWithBorder
            value='Certificate Expiry'
            borderColor={colors.border.primary}
            borderWidth='3px'
            sx={{ '& p': { fontSize: '16px', fontWeight: 500, color: colors.text.secondary, marginBottom: '16px' } }}
          />
          <Box display='grid' gridTemplateColumns='1fr 1fr' gap='16px'>
            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>Certificate Expiry</Typography>
              <TextField
                fullWidth
                value={certificateExpiry}
                type='number'
                onChange={(e) => setCertificateExpiry(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === '-') {
                    e.preventDefault();
                  }
                }}
                InputProps={{
                  inputProps: { min: 0 },
                }}
                variant='outlined'
                sx={styles.inputField}
              />
            </Box>
          </Box>

          <CustomDivider borderColor={colors.background.tertiaryLightestest} margin={'24px 0px'} />

          <TextWithBorder
            value='Abandoned App Configuration'
            borderColor={colors.border.primary}
            borderWidth='3px'
            sx={{ '& p': { fontSize: '16px', fontWeight: 500, color: colors.text.secondary, marginBottom: '16px' } }}
          />
          <Box display='grid' gridTemplateColumns='1fr 1fr' gap='16px'>
            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>Network Threshold</Typography>
              <TextField
                fullWidth
                value={networkThreshold}
                type='number'
                defaultValue={1000}
                onChange={(e) => setNetworkThreshold(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === '-') {
                    e.preventDefault();
                  }
                }}
                InputProps={{
                  inputProps: { min: 0 },
                }}
                variant='outlined'
                sx={styles.inputField}
              />
            </Box>
            <Box display='flex' flexDirection='column'>
              <Typography sx={styles.label}>Observation Days</Typography>
              <TextField
                fullWidth
                value={observationDays}
                defaultValue={7}
                type='number'
                onChange={(e) => setObservationDays(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === '-') {
                    e.preventDefault();
                  }
                }}
                InputProps={{
                  inputProps: { min: 0 },
                }}
                variant='outlined'
                sx={styles.inputField}
              />
            </Box>
          </Box>

          <CustomDivider borderColor={colors.background.tertiaryLightestest} margin={'24px 0px'} />

          {selectedAnomalyConfigs.length > 0 && (
            <TextWithBorder
              value='Anomaly Configuration'
              borderColor={colors.border.primary}
              borderWidth='3px'
              sx={{ '& p': { fontSize: '16px', fontWeight: 500, color: colors.text.secondary, marginBottom: '16px' } }}
            />
          )}
          {selectedAnomalyConfigs.length > 0
            ? selectedAnomalyConfigs.map((ac) => (
                <Box key={ac.title} display='flex' alignItems='center' gap={2} sx={{ mt: 2 }}>
                  <Box display='flex' flexDirection='column'>
                    <Typography sx={styles.label}>Type</Typography>
                    <TextField fullWidth value={ac.anomaly_type} disabled={true} variant='outlined' sx={styles.inputField} />
                  </Box>
                  <Box display='flex' flexDirection='column'>
                    <Typography sx={styles.label}>Operator</Typography>
                    <TextField fullWidth value={ac.change_operator} disabled={true} variant='outlined' sx={styles.inputField} />
                  </Box>
                  <Box display='flex' flexDirection='column'>
                    <Typography sx={styles.label}>Title</Typography>
                    <TextField fullWidth value={ac.title} disabled={true} variant='outlined' sx={styles.inputField} />
                  </Box>
                  <Box display='flex' flexDirection='column'>
                    <Typography sx={styles.label}>Buffer Percentage</Typography>
                    <TextField fullWidth value={ac.buffer_percentage * 100} type='number' disabled={true} variant='outlined' sx={styles.inputField} />
                  </Box>
                </Box>
              ))
            : null}
          <CustomDivider borderColor={colors.background.tertiaryLightestest} margin={'24px 0px'} />
          <TextWithBorder
            value='Feature Flag'
            borderColor={colors.border.primary}
            borderWidth='3px'
            sx={{ '& p': { fontSize: '16px', fontWeight: 500, color: colors.text.secondary, marginBottom: '16px' } }}
          />
          <Box
            display='grid'
            gridTemplateColumns='repeat(3, 1fr)'
            sx={{
              ml: '12px',
              width: '100%',
              '& > *': {
                borderRight: '1px solid #EBEBEB',
                borderBottom: '1px solid #EBEBEB',
                padding: '12px 16px',
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
              <CustomCheckBox
                key={f.value}
                checked={selectedFeatures.includes(f.value)}
                text={f.description || f.value}
                onChange={() => {
                  handleCheckBoxChange(f.value);
                }}
                checkboxStyle={{ fontSize: '12px' }}
              />
            ))}
          </Box>
        </Box>
      </Modal>

      <NDialog
        buttonText='Confirm'
        handleClose={() => setUpdateAccountStatus({})}
        dialogTitle={
          <Typography component='h2' variant='h6' fontWeight={600}>
            {updateAccountStatus.status == 'active' ? 'Enable' : 'Disable'} Kubernetes Account
          </Typography>
        }
        dialogContent={`Are you sure you want to ${updateAccountStatus.status == 'active' ? 'enable' : 'disable'} "${
          updateAccountStatus.name
        }" the configured Kubernetes Account?`}
        handleSubmit={handleUpdateAccountStatus}
        open={updateAccountStatus && Object.keys(updateAccountStatus).length > 0}
        loading={isStatusUpdating}
      />
      <NDialog
        handleClose={() => setK8sCurlCommand('')}
        dialogTitle={`Update the Agent`}
        open={k8sCurlCommand && k8sCurlCommand.length > 0}
        isSubmitRequired={false}
        additionalComponent={additionalShowcaseAgentUpdateCmd()}
      />
      <K8sAccountModal openModal={openModal} handleClose={closeModal} handleOnAccountCreate={handleOnAccountCreate} />
      <Grid container padding='5px' mt={2}>
        <Grid item xs={12}>
          <Stack direction='row' alignItems='center' justifyContent='space-between'>
            <Stack direction='row' alignItems='center' spacing={1}>
              <Typography color={colors.text.secondary} fontSize='16px' fontWeight={600}>
                {'Kubernetes'}
              </Typography>
              <CloudProviderIcon cloud_provider={'K8S'} />
            </Stack>
            {hasWriteAccess() && (
              <CustomButton id='add-k8s-account' onClick={() => setOpenModal(true)} aria-label='Add K8s Account' text='Add K8s Account' />
            )}
          </Stack>
        </Grid>
      </Grid>
      <BoxLayout2
        id={'k8s-integrations'}
        sharingOptions={false}
        filterOptions={[
          {
            type: 'dropdown',
            enabled: true,
            options: statusOptions,
            onSelect: handleStatusFilterChange,
            minWidth: '150px',
            label: 'By Status',
            value: selectedStatusFilter,
          },
          {
            type: 'search',
            enabled: true,
            onSelect: handleNameFilterChange,
            minWidth: '150px',
            label: 'Enter Name',
            onEnter: onEnterPress,
            value: nameInput,
          },
        ]}
      >
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
      </BoxLayout2>
    </>
  );
};

export default K8sIntegrationTile;
