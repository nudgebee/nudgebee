import apiIntegrations from '@api1/integrations';
import apiUser from '@api1/user';
import { ListingLayout } from '@ui/ListingLayout';
import { EmptyState } from '@ui/EmptyState';
import FilterDropdown from '@ui/FilterDropdown';
import SearchInput from '@ui/SearchInput';
import { Button as DsButton } from '@ui/Button';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import Datetime from '@shared/format/Datetime';
import { Modal } from '@ui/Modal';
import { toast as snackbar } from '@ui/Toast';
import CustomTable from '@shared/tables/CustomTable';
import { Label } from '@ui/Label';
import { hasWriteAccess, canManage } from '@lib/auth';
import AddIcon from '@mui/icons-material/Add';
import LinkOutlinedIcon from '@mui/icons-material/LinkOutlined';
import { ds } from 'src/utils/colors';
import { useEffect, useMemo, useState, useRef } from 'react';
import { snakeToTitleCase, toKebabCase } from 'src/utils/common';
import { action } from 'src/utils/actionStyles';
import apiTicketIntegrations from '@api1/tickets';
import { getIntegrationDescription } from '@lib/integrationDescriptions';
// Note: Test Connection has moved into the Add/Edit modal. To re-verify a
// saved integration, open Edit and click Test Connection (the stored token is
// rehydrated server-side when the field is left as the masked placeholder).

const statusOptions = [
  { label: 'Active', value: 'enabled' },
  { label: 'Inactive', value: 'disabled' },
];

// The status each page opens on; relaxed to "all" once if it returns nothing.
const DEFAULT_STATUS_FILTER = 'enabled';

const TicketingIntegrationTile = ({ tool, displayName, cloudProvider, AccountModalComponent }) => {
  const headers = ['Name', 'Last Connected At', 'Status', 'Auth Type', 'Created By', 'Account URL', ''];

  const [rawData, setRawData] = useState([]);
  const [openModal, setOpenModal] = useState(false);
  const [editConfig, setEditConfig] = useState(null);
  const [loading, setLoading] = useState(false);
  const [disableConfig, setDisableConfig] = useState({});
  const [deleteConfig, setDeleteConfig] = useState({});
  const [isChangingConfig, setIsChangingConfig] = useState(false);
  const [nameInput, setNameInput] = useState('');
  const [selectedNameFilter, setSelectedNameFilter] = useState('');
  const [selectedStatusFilter, setSelectedStatusFilter] = useState(DEFAULT_STATUS_FILTER);
  const [currentPage, setCurrentPage] = useState(0);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [totalCount, setTotalCount] = useState(0);
  // Which tool the rows in state belong to. Tagged rather than a plain "have I
  // loaded" boolean because this component is reused across tools without
  // remounting — the account-form switch returns the same element type — so an
  // untagged flag stays true through the swap and paints the previous tool's
  // emptiness under the new tool's name.
  const [loadedFor, setLoadedFor] = useState(null);
  const hasLoaded = loadedFor === tool;

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
        id: 'toggle-enabled',
      },
    ];
    if (item.is_active) {
      items.push({
        label: 'Edit',
        id: 'edit',
      });
    }
    items.push({
      label: 'Delete',
      id: 'delete',
    });
    return items;
  };

  const onMenuClick = (menuItem, item) => {
    if (menuItem.id === 'toggle-enabled') {
      setDisableConfig({ id: item.id, active: !item.is_active, name: item.name });
    } else if (menuItem.id === 'edit') {
      setEditConfig(item);
      setOpenModal(true);
    } else if (menuItem.id === 'delete') {
      setDeleteConfig({ id: item.id, name: item.name });
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
        console.error('Failed to fetch ticketing configurations', { displayName, err });
      })
      .finally(() => {
        setLoading(false);
        setLoadedFor(tool);
      });
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

  const handleDeleteConfig = () => {
    setIsChangingConfig(true);
    apiIntegrations
      .deleteIntegrations({
        integration_config_name: deleteConfig.name,
        integration_name: tool,
        source: 'user',
      })
      .then((res) => {
        if (res?.data?.data?.integrations_delete_config?.status === 'success') {
          snackbar.success(`${displayName} configuration deleted successfully`);
          setDeleteConfig({});
          listConfigurations();
          apiTicketIntegrations.listTicketConfigurations({}, true).catch((e) => {
            console.error('Failed to refresh ticket configurations cache', e);
          });
        } else {
          snackbar.error(res?.data?.errors?.[0]?.message || `Failed to delete ${displayName} configuration`);
        }
      })
      .catch(() => {
        snackbar.error(`Failed to delete ${displayName} configuration`);
      })
      .finally(() => {
        setIsChangingConfig(false);
      });
  };

  // When the default status filter hides everything, fall back to "all statuses" once
  // before declaring the screen empty. Without this a tenant whose only configs are
  // disabled sees the connect prompt — with no filter left on screen to clear, its
  // disabled configs would be unreachable from the UI.
  // Tagged for the same reason as `loadedFor`: a plain boolean would stay true
  // after a swap and skip this fallback for every later tool in the session.
  const relaxedFor = useRef(null);
  useEffect(() => {
    if (!hasLoaded || loading || relaxedFor.current === tool) {
      return;
    }
    if (totalCount === 0 && !selectedNameFilter && selectedStatusFilter) {
      relaxedFor.current = tool;
      setSelectedStatusFilter('');
      setCurrentPage(0);
    }
  }, [hasLoaded, loading, totalCount, selectedNameFilter, selectedStatusFilter, tool]);

  const addAccountButton = (
    <DsButton
      id={`add-${toKebabCase(displayName)}-account-btn`}
      tone='primary'
      size='md'
      icon={<AddIcon sx={{ fontSize: 18 }} />}
      onClick={openAddModal}
      aria-label={`Add ${displayName} Account`}
    >
      {`Add ${displayName} Account`}
    </DsButton>
  );

  // Swap the filter bar + table for a "connect your first account" prompt only when
  // the list is empty with the filters untouched. A zero-row result the user filtered
  // their way into keeps the toolbar, otherwise clearing that filter is unreachable.
  // Known edge: the status filter defaults to Active, so a tenant whose only accounts
  // are disabled sees this prompt too.
  const showConnectPrompt = hasLoaded && !loading && totalCount === 0 && !selectedNameFilter && !selectedStatusFilter;

  return (
    <>
      <Modal
        handleClose={isChangingConfig ? () => {} : () => setDisableConfig({})}
        open={disableConfig && Object.keys(disableConfig).length > 0}
        title={`${disableConfig.active ? 'Enable' : 'Disable'} ${displayName} Integration`}
        width='md'
        loader={isChangingConfig}
        actionButtons={
          <>
            <DsButton id='ticketing-integration-cancel-btn' tone='secondary' onClick={() => setDisableConfig({})} disabled={isChangingConfig}>
              Cancel
            </DsButton>
            <DsButton tone={disableConfig.active ? 'primary' : 'danger'} loading={isChangingConfig} onClick={handleDisableConfig}>
              Confirm
            </DsButton>
          </>
        }
      >
        {`Are you sure you want to ${disableConfig.active ? 'enable' : 'disable'} this "${disableConfig.name}" ${displayName} integration?`}
      </Modal>
      <Modal
        handleClose={isChangingConfig ? () => {} : () => setDeleteConfig({})}
        open={deleteConfig && Object.keys(deleteConfig).length > 0}
        title={`Delete ${displayName} Integration`}
        width='md'
        loader={isChangingConfig}
        actionButtons={
          <>
            <DsButton id='ticketing-integration-delete-cancel-btn' tone='secondary' onClick={() => setDeleteConfig({})} disabled={isChangingConfig}>
              Cancel
            </DsButton>
            <DsButton id='ticketing-integration-delete-confirm-btn' tone='danger' loading={isChangingConfig} onClick={handleDeleteConfig}>
              Delete
            </DsButton>
          </>
        }
      >
        {`Are you sure you want to delete this "${deleteConfig.name}" ${displayName} integration? This action cannot be undone.`}
      </Modal>
      <AccountModalComponent openModal={openModal} handleClose={closeModal} tool={tool} editConfig={editConfig} />
      <ListingLayout id={`${tool}-integrations`}>
        <ListingLayout.Header
          icon={<CloudProviderIcon cloud_provider={cloudProvider} width={ds.space[5]} height={ds.space[5]} />}
          title={displayName}
          description={getIntegrationDescription(cloudProvider)}
          actions={canManage('integrations', 'Write') && hasLoaded && totalCount > 0 ? addAccountButton : undefined}
        />
        {showConnectPrompt ? (
          <ListingLayout.Body padding={ds.space[5]}>
            <EmptyState
              id={`${tool}-no-accounts`}
              icon={<LinkOutlinedIcon sx={{ fontSize: 20 }} />}
              title={`No ${displayName} accounts connected`}
              description={getIntegrationDescription(cloudProvider)}
              action={
                canManage('integrations', 'Write')
                  ? {
                      id: `add-${toKebabCase(displayName)}-account-btn`,
                      label: `Add ${displayName} Account`,
                      onClick: openAddModal,
                      icon: <AddIcon sx={{ fontSize: 16 }} />,
                    }
                  : undefined
              }
            />
          </ListingLayout.Body>
        ) : (
          <>
            <ListingLayout.Toolbar>
              <FilterDropdown
                id={`${tool}-status-filter`}
                label='Status'
                options={statusOptions}
                value={statusOptions.find((o) => o.value === selectedStatusFilter) ?? null}
                onSelect={(_e, item) => handleStatusFilterChange({ target: { value: item?.value || '' } })}
              />
              <SearchInput
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
          </>
        )}
      </ListingLayout>
    </>
  );
};

export default TicketingIntegrationTile;
