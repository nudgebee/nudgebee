import apiIntegrations from '@api1/integrations';
import apiUser from '@api1/user';
import { BoxLayout2, ThreeDotsMenu } from '@components1/common';
import CloudProviderIcon from '@components1/common/CloudIcon';
import CustomButton from '@components1/common/NewCustomButton';
import Datetime from '@components1/common/format/Datetime';
import NDialog from '@components1/common/modal/NDialog';
import { snackbar } from '@components1/common/snackbarService';
import CustomTable from '@components1/common/tables/CustomTable2';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { hasWriteAccess } from '@lib/auth';
import { Grid, Stack, Typography } from '@mui/material';
import { useEffect, useMemo, useState } from 'react';
import { colors } from 'src/utils/colors';
import { snakeToTitleCase, toKebabCase } from 'src/utils/common';
import { action } from 'src/utils/actionStyles';
import apiTicketIntegrations from '@api1/tickets';

const statusOptions = [
  { label: 'Active', value: 'enabled' },
  { label: 'Inactive', value: 'disabled' },
];

const TicketingIntegrationTile = ({ tool, displayName, cloudProvider, AccountModalComponent }) => {
  const headers = ['Name', 'Last Connected At', 'Status', 'Auth Type', 'Created By', 'Account URL', ''];

  const [rawData, setRawData] = useState([]);
  const [openModal, setOpenModal] = useState(false);
  const [editConfig, setEditConfig] = useState(null);
  const [loading, setLoading] = useState(false);
  const [disableConfig, setDisableConfig] = useState({});
  const [isChangingConfig, setIsChangingConfig] = useState(false);
  const [testingConnectionId, setTestingConnectionId] = useState(null);
  const [nameInput, setNameInput] = useState('');
  const [selectedNameFilter, setSelectedNameFilter] = useState('');
  const [selectedStatusFilter, setSelectedStatusFilter] = useState('enabled');
  const [currentPage, setCurrentPage] = useState(0);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [totalCount, setTotalCount] = useState(0);

  useEffect(() => {
    listConfigurations();
  }, [selectedNameFilter, selectedStatusFilter, currentPage, recordsPerPage]);

  const handleTestConnection = async (item) => {
    setTestingConnectionId(item.id);
    try {
      const result = await apiIntegrations.testTicketConnection(item.id);
      if (result?.success) {
        snackbar.success(`${displayName} connection successful (${result.projects_count} projects accessible)`);
      } else {
        snackbar.error(result?.error || `${displayName} connection test failed`);
      }
    } catch {
      snackbar.error(`Failed to test ${displayName} connection`);
    } finally {
      setTestingConnectionId(null);
    }
  };

  const getMenuItems = (item) => {
    if (!hasWriteAccess()) {
      return [];
    }
    const items = [
      {
        label: item.is_active ? 'Disable' : 'Enable',
        id: 0,
      },
    ];
    if (item.is_active) {
      items.push({
        label: 'Edit',
        id: 2,
      });
      items.push({
        label: testingConnectionId === item.id ? 'Testing...' : 'Test Connection',
        id: 1,
        disabled: testingConnectionId === item.id,
      });
    }
    return items;
  };

  const onMenuClick = (menuItem, item) => {
    if (menuItem.id === 0) {
      setDisableConfig({ id: item.id, active: !item.is_active, name: item.name });
    } else if (menuItem.id === 1) {
      handleTestConnection(item);
    } else if (menuItem.id === 2) {
      setEditConfig(item);
      setOpenModal(true);
    }
  };

  const handleNameFilterChange = (e) => {
    if (nameInput !== '' && e.target.value === '') {
      setSelectedNameFilter('');
      setCurrentPage(0);
    }
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

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const listConfigurations = () => {
    setLoading(true);
    apiIntegrations
      .listTicketConfigurationsByTool({
        tool,
        limit: recordsPerPage,
        offset: currentPage * recordsPerPage,
        name: selectedNameFilter || undefined,
        status: selectedStatusFilter || undefined,
      })
      .then((res) => {
        setRawData(res?.data?.length > 0 ? res.data : []);
        setTotalCount(res?.totalCount || 0);
      })
      .catch((err) => {
        console.error(`Failed to fetch ${displayName} configurations`, err);
      })
      .finally(() => setLoading(false));
  };

  const tableData = useMemo(
    () =>
      rawData.map((item) => [
        { text: item.name },
        { component: <Datetime value={item.last_connected} /> },
        { component: <CustomLabels text={item.is_active ? 'active' : 'inactive'} /> },
        { text: snakeToTitleCase(item.auth_type) || '-' },
        { text: item.user?.display_name || '-' },
        { text: item?.url || '-' },
        {
          component: <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(item)} data={item} onMenuClick={onMenuClick} />,
        },
      ]),
    [rawData, testingConnectionId, tool, displayName]
  );

  const closeModal = (shouldRefresh = false) => {
    setDisableConfig({});
    setOpenModal(false);
    setEditConfig(null);
    // Refresh the list if integration was successful
    if (shouldRefresh) {
      listConfigurations();
    }
  };

  const openAddModal = () => {
    setEditConfig(null);
    setOpenModal(true);
  };

  const handleDisableConfig = () => {
    setIsChangingConfig(true);
    apiIntegrations
      .disableTicketConfiguration(disableConfig)
      .then((res) => {
        if (res?.data?.data?.integration_update_status_by_pk?.id) {
          snackbar.success(`${displayName} configuration ${disableConfig.active ? 'enabled' : 'disabled'} successfully`);
          setDisableConfig({});
          listConfigurations();
          apiTicketIntegrations.listTicketConfigurations({}, true).catch(() => {});
        } else {
          snackbar.error(`Failed to ${disableConfig.active ? 'enable' : 'disable'} ${displayName} configuration`);
        }
      })
      .catch(() => {
        snackbar.error(`Failed to ${disableConfig.active ? 'enable' : 'disable'} ${displayName} configuration`);
      })
      .finally(() => {
        setIsChangingConfig(false);
      });
  };

  return (
    <>
      <NDialog
        buttonText='Confirm'
        handleClose={() => setDisableConfig({})}
        dialogContent={`Are you sure you want to ${disableConfig.active ? 'enable' : 'disable'} this "${
          disableConfig.name
        }" ${displayName} integration?`}
        handleSubmit={handleDisableConfig}
        loading={isChangingConfig}
        open={disableConfig && Object.keys(disableConfig).length > 0}
        dialogTitle={`${disableConfig.active ? 'Enable' : 'Disable'} ${displayName} Integration`}
      />
      <AccountModalComponent openModal={openModal} handleClose={closeModal} tool={tool} editConfig={editConfig} />
      <Grid container padding='5px' mt={2}>
        <Grid item xs={12}>
          <Stack direction='row' alignItems='center' justifyContent='space-between'>
            <Stack direction='row' alignItems='center' spacing={1}>
              <Typography color={colors.text.secondary} fontSize='16px' fontWeight={600}>
                {displayName}
              </Typography>
              <CloudProviderIcon cloud_provider={cloudProvider} />
            </Stack>
            {hasWriteAccess() && (
              <CustomButton
                id={`add-${toKebabCase(displayName)}-account-btn`}
                onClick={openAddModal}
                aria-label={`Add ${displayName} Account`}
                text={`Add ${displayName} Account`}
              />
            )}
          </Stack>
        </Grid>
      </Grid>
      <BoxLayout2
        id={`${tool}-integrations`}
        loading={loading}
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
          id={tool}
          loading={loading}
          tableData={tableData}
          headers={headers}
          totalRows={totalCount}
          rowsPerPage={recordsPerPage}
          pageNumber={currentPage + 1}
          onPageChange={onPageChange}
        />
      </BoxLayout2>
    </>
  );
};

export default TicketingIntegrationTile;
