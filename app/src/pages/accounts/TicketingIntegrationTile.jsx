import apiIntegrations from '@api1/integrations';
import apiUser from '@api1/user';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import { Button as DsButton } from '@components1/ds/Button';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import CloudProviderIcon from '@components1/common/CloudIcon';
import Datetime from '@common-new/format/Datetime';
import NDialog from '@common-new/modal/NDialog';
import { toast as snackbar } from '@components1/ds/Toast';
import CustomTable from '@common-new/tables/CustomTable2';
import { Label } from '@components1/ds/Label';
import { hasWriteAccess } from '@lib/auth';
import { Stack, Typography } from '@mui/material';
import { useEffect, useMemo, useState } from 'react';
import { snakeToTitleCase, toKebabCase } from 'src/utils/common';
import { action } from 'src/utils/actionStyles';
import apiTicketIntegrations from '@api1/tickets';
// Note: Test Connection has moved into the Add/Edit modal. To re-verify a
// saved integration, open Edit and click Test Connection (the stored token is
// rehydrated server-side when the field is left as the masked placeholder).

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
  const [nameInput, setNameInput] = useState('');
  const [selectedNameFilter, setSelectedNameFilter] = useState('');
  const [selectedStatusFilter, setSelectedStatusFilter] = useState('enabled');
  const [currentPage, setCurrentPage] = useState(0);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [totalCount, setTotalCount] = useState(0);

  useEffect(() => {
    listConfigurations();
  }, [selectedNameFilter, selectedStatusFilter, currentPage, recordsPerPage]);

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
    }
    return items;
  };

  const onMenuClick = (menuItem, item) => {
    if (menuItem.id === 0) {
      setDisableConfig({ id: item.id, active: !item.is_active, name: item.name });
    } else if (menuItem.id === 2) {
      setEditConfig(item);
      setOpenModal(true);
    }
  };

  const handleStatusFilterChange = (e) => {
    setSelectedStatusFilter(e.target.value);
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
        { component: <Label text={item.is_active ? 'active' : 'inactive'} /> },
        { text: snakeToTitleCase(item.auth_type) || '-' },
        { text: item.user?.display_name || '-' },
        { text: item?.url || '-' },
        {
          component: <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(item)} data={item} onMenuClick={onMenuClick} />,
        },
      ]),
    [rawData, tool, displayName]
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
          apiTicketIntegrations.listTicketConfigurations({}, true).catch((e) => {
            console.error('Failed to refresh ticket configurations cache', e);
          });
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
        submitTone={disableConfig.active ? 'primary' : 'danger'}
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
      <ListingLayout id={`${tool}-integrations`}>
        <ListingLayout.Toolbar
          title={
            <Stack direction='row' alignItems='center' spacing={1}>
              <Typography color={'var(--ds-gray-700)'} fontSize='16px' fontWeight={600}>
                {displayName}
              </Typography>
              <CloudProviderIcon cloud_provider={cloudProvider} />
            </Stack>
          }
          actions={
            hasWriteAccess() ? (
              <DsButton
                id={`add-${toKebabCase(displayName)}-account-btn`}
                tone='primary'
                size='md'
                onClick={openAddModal}
                aria-label={`Add ${displayName} Account`}
              >
                {`Add ${displayName} Account`}
              </DsButton>
            ) : undefined
          }
        >
          <FilterDropdown
            id={`${tool}-status-filter`}
            label='Status'
            options={statusOptions}
            value={statusOptions.find((o) => o.value === selectedStatusFilter) ?? null}
            onSelect={(_e, item) => handleStatusFilterChange({ target: { value: item?.value || '' } })}
          />
          <CustomSearch
            id={`${tool}-name-search`}
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
            id={tool}
            loading={loading}
            tableData={tableData}
            headers={headers}
            totalRows={totalCount}
            rowsPerPage={recordsPerPage}
            pageNumber={currentPage + 1}
            onPageChange={onPageChange}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default TicketingIntegrationTile;
