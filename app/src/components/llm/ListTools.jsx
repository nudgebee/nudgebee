import React from 'react';
import PropTypes from 'prop-types';
import apiAskNudgebee from '@api1/ask-nudgebee';
import ListingLayout from '@ui/ListingLayout';
import SearchInput from '@ui/SearchInput';
import FilterDropdown from '@ui/FilterDropdown';
import DownloadButton from '@shared/buttons/DownloadButton';
import { Button } from '@ui/Button';
import CustomTable from '@shared/tables/CustomTable';
import { Label } from '@ui/Label';
import Text from '@shared/format/Text';
import CreateTool from './CreateTool';
import { Modal } from '@ui/Modal';
import { hasWriteAccess } from '@lib/auth';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { PlusIcon, EditIcon, ErrorIcon } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import { snakeToTitleCase } from 'src/utils/common';
import { Box, Stack } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import { ds } from 'src/utils/colors';
import { TOOL_CONFIGURATION_WARNING } from '@data/constants';
import { HybridScopeChips } from '@components/llm/ScopeChip';

// "Created By" is a binary distinction on tool.type — the backend only ever
// emits 'system' or 'custom', so the options are fixed (not derived from data).
const CREATED_BY_OPTIONS = [
  { label: 'System Generated', value: 'system' },
  { label: 'User Created', value: 'custom' },
];

const ListTools = ({ accountId, stickyTable = false }) => {
  // Tenant-wide read-only mode: when the Settings modal is opened from the
  // global sidebar (no current account in scope), the backend's
  // toolListTool routes empty account_id to ListToolsForTenant — system
  // tool catalog + custom tools across every account the caller can
  // read. The UI replaces NB-Tool-Type with an Account column, drops the
  // per-account is_configured warning, and hides Create / Edit.
  const isTenantWide = !accountId;
  const { baseTitle } = useTenantBranding();
  const [data, setData] = React.useState([]);
  const [originalData, setOriginalData] = React.useState([]);
  const [loading, setLoading] = React.useState(false);
  const [createToolModal, setCreateToolModal] = React.useState(false);
  const [allTools, setAllTools] = React.useState([]);
  const [editMode, setEditMode] = React.useState(false);
  const [selectedTool, setSelectedTool] = React.useState(null);
  const [searchToolByName, setSearchToolByName] = React.useState('');
  const [createdByFilter, setCreatedByFilter] = React.useState(null);
  const [statusFilter, setStatusFilter] = React.useState(null);

  React.useEffect(() => {
    listTools();
  }, [accountId]);

  // Status options are derived from the loaded tools (not hard-coded) so the
  // filter always mirrors the exact set of statuses the Status column shows —
  // enabled/disabled/draft/error today, plus any future statuses for free.
  const statusOptions = React.useMemo(() => {
    const distinct = [...new Set(allTools.map((tool) => tool?.status).filter(Boolean))];
    return distinct.map((status) => ({ label: snakeToTitleCase(status), value: status })).sort((a, b) => a.label.localeCompare(b.label));
  }, [allTools]);

  // Drop a selected status that no longer exists after a refetch, otherwise the
  // table would silently show zero rows with no obvious way to recover.
  React.useEffect(() => {
    if (statusFilter && statusOptions.length > 0 && !statusOptions.some((opt) => opt.value === statusFilter)) {
      setStatusFilter(null);
    }
  }, [statusOptions, statusFilter]);

  React.useEffect(() => {
    const filteredData = originalData.filter((item) => {
      const raw = item[0]?.rawData || {};

      // Name: match both original (snake_case) and formatted (Title Case) forms.
      let nameMatches = true;
      if (searchToolByName !== '') {
        const originalToolName = raw.name || '';
        const formattedToolName = snakeToTitleCase(originalToolName);
        const searchLower = searchToolByName.toLowerCase();
        nameMatches = originalToolName.toLowerCase().includes(searchLower) || formattedToolName.toLowerCase().includes(searchLower);
      }

      const createdByMatches = !createdByFilter || raw.type === createdByFilter;
      const statusMatches = !statusFilter || raw.status === statusFilter;

      return nameMatches && createdByMatches && statusMatches;
    });
    setData(filteredData);
  }, [searchToolByName, createdByFilter, statusFilter, originalData]);

  const handleSearchEnter = () => {
    listTools();
  };

  const handleEditTool = (tool) => {
    setSelectedTool(tool);
    setEditMode(true);
    setCreateToolModal(true);
  };

  const listTools = () => {
    setLoading(true);
    apiAskNudgebee
      .listTools({ accountId })
      .then((res) => {
        const listToolsResponse = res?.data?.data?.ai_list_tools?.data ?? [];
        const allTools = listToolsResponse.map((tool) => tool);
        setAllTools(allTools);
        if (listToolsResponse.length > 0) {
          const tools = listToolsResponse.map((tool) => {
            return [
              {
                component: (
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
                    <Box sx={{ fontWeight: ds.weight.medium, display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                      {snakeToTitleCase(tool.name)}
                      {/* is_configured is per-account; the warning makes no
                          sense in the tenant-wide read. */}
                      {!isTenantWide && tool.needs_config && !tool.is_configured && (
                        <Tooltip title={TOOL_CONFIGURATION_WARNING}>
                          <SafeIcon src={ErrorIcon} alt='warning' height={18} width={18} />
                        </Tooltip>
                      )}
                    </Box>
                    <Box
                      sx={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        backgroundColor: tool.type === 'system' ? ds.blue[100] : ds.gray[100],
                        color: tool.type === 'system' ? ds.blue[700] : ds.gray[600],
                        fontSize: ds.text.caption,
                        fontWeight: ds.weight.semibold,
                        padding: '2px 6px',
                        borderRadius: ds.radius.pill,
                        border: `1px solid ${tool.type === 'system' ? ds.blue[200] : ds.gray[200]}`,
                        textTransform: 'uppercase',
                        letterSpacing: '0.5px',
                        width: 'fit-content',
                      }}
                    >
                      {tool.type === 'system' ? `${baseTitle} System` : 'User Created'}
                    </Box>
                  </Box>
                ),
                rawData: { name: tool.name, type: tool.type, status: tool.status },
              },
              {
                component: <Text value={tool.description || '-'} showAutoEllipsis requiredToolTip lineClamp={2} />,
              },
              {
                component: <Label text={tool.status} />,
              },
              // Tenant-wide swaps NB Tool Type + Actions for an Account
              // column. Edit is per-account, so it's hidden alongside
              // Create. System tools never have an account; show '—'.
              ...(isTenantWide
                ? [
                    {
                      component: <Text value={tool.account_name || (tool.type === 'system' ? '—' : tool.account_id || '—')} />,
                    },
                  ]
                : [
                    {
                      text: <Label text={snakeToTitleCase(tool.nb_tool_type)} />,
                    },
                    {
                      component:
                        tool.type === 'custom' && tool.nb_tool_type == 'tool' && hasWriteAccess(accountId) ? (
                          <Button
                            tone='secondary'
                            size='xs'
                            composition='icon-only'
                            icon={<SafeIcon src={EditIcon} alt='edit' height={20} width={20} />}
                            aria-label='Edit tool'
                            onClick={() => handleEditTool(tool)}
                          />
                        ) : null,
                    },
                  ]),
            ];
          });
          setData(tools);
          setOriginalData(tools);
        } else {
          setData([]);
          setOriginalData([]);
        }
      })
      .finally(() => {
        setLoading(false);
      });
  };

  return (
    <>
      <Modal
        width={'md'}
        open={createToolModal}
        handleClose={() => {
          setCreateToolModal(false);
          setEditMode(false);
          setSelectedTool(null);
        }}
        title={editMode ? 'Edit Tool' : 'Add Tool'}
      >
        <CreateTool
          accountId={accountId}
          handleClose={(value) => {
            if (value == 'success') {
              listTools();
            }
            setCreateToolModal(false);
            setEditMode(false);
            setSelectedTool(null);
          }}
          allTools={allTools}
          editMode={editMode}
          toolData={selectedTool}
        />
      </Modal>
      <ListingLayout id='all-tools'>
        <ListingLayout.Toolbar
          title={
            <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[2] }}>
              Tools
              {/* Hybrid: system catalog + custom tools per account. */}
              <HybridScopeChips accountId={accountId} />
            </Box>
          }
          actions={
            <>
              <DownloadButton onClick={() => ({ tableId: 'tools' })} size='sm' />
              <Button
                tone='secondary'
                size='sm'
                id='integration'
                onClick={() => {
                  window.open('/user-management#integrations', '_blank', 'noopener,noreferrer');
                }}
              >
                Integration
              </Button>
              {!isTenantWide && hasWriteAccess(accountId) && (
                <Button
                  tone='primary'
                  size='sm'
                  id='create-tool'
                  onClick={() => {
                    setEditMode(false);
                    setSelectedTool(null);
                    setCreateToolModal(true);
                  }}
                >
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: ds.space[2],
                      fontFamily: ds.font.sans,
                      fontSize: ds.text.small,
                      fontWeight: ds.weight.medium,
                    }}
                  >
                    <SafeIcon src={PlusIcon} alt='plus' />
                    Create Tool
                  </Box>
                </Button>
              )}
            </>
          }
        >
          <Stack direction='row' alignItems='center' spacing={1}>
            <FilterDropdown
              id='tool-created-by-filter'
              label='Created By'
              options={CREATED_BY_OPTIONS}
              value={createdByFilter}
              onSelect={(_e, item) => setCreatedByFilter(item?.value ?? null)}
            />
            <FilterDropdown
              id='tool-status-filter'
              label='Status'
              options={statusOptions}
              value={statusFilter}
              onSelect={(_e, item) => setStatusFilter(item?.value ?? null)}
            />
            <SearchInput
              id='tool-search'
              label='Search Tool'
              value={searchToolByName}
              onChange={(value) => setSearchToolByName(value)}
              onEnterPress={handleSearchEnter}
            />
          </Stack>
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            headers={
              isTenantWide
                ? [
                    { name: 'Name', width: '25%' },
                    { name: 'Description', width: '45%' },
                    { name: 'Status', width: '15%' },
                    { name: 'Account', width: '15%' },
                  ]
                : [
                    { name: 'Name', width: '20%' },
                    { name: 'Description', width: '40%' },
                    { name: 'Status', width: '15%' },
                    { name: 'NB Tool Type', width: '15%' },
                    { name: 'Actions', width: '10%' },
                  ]
            }
            tableData={data}
            rowsPerPage={data.length}
            totalRows={data.length}
            loading={loading}
            id='tools'
            stickyHeader={stickyTable}
            sx={stickyTable ? { maxHeight: 'calc(90vh - 300px)', overflowY: 'auto' } : undefined}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

ListTools.propTypes = {
  accountId: PropTypes.string,
  stickyTable: PropTypes.bool,
};

export default ListTools;
