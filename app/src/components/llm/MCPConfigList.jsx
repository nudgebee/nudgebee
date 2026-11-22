import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { Stack } from '@mui/material';
import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime';
import { ListingLayout } from '@ui/ListingLayout';
import { Chip } from '@ui/Chip';
import { Label } from '@ui/Label';
import { Button } from '@ui/Button';
import Tooltip from '@ui/Tooltip';
import Text from '@shared/format/Text';
import FilterDropdown from '@ui/FilterDropdown';
import SearchInput from '@ui/SearchInput';
import CustomTable from '@shared/tables/CustomTable';
import { toast as snackbar } from '@ui/Toast';
import apiIntegrations from '@api1/integrations';
import { parseIntegrationItem } from '@api1/integrations/helpers';
import { Modal } from '@ui/Modal';
import { canManage, isTenantAdmin } from '@lib/auth';
import McpWriteToolsField, { parseToolNames } from '@components/common/forms/McpWriteToolsField';
import { summarizeMCPHealth } from './mcpHealthSummary';

dayjs.extend(relativeTime);

const STATUS_OPTIONS = [
  { label: 'Enabled', value: 'enabled' },
  { label: 'Disabled', value: 'disabled' },
];

// Status keeps its column position; the newer columns follow it.
const HEADERS = ['Name', 'Connection', 'Account', 'Created By', 'Status', 'Health', 'Needs approval'];

const configValue = (item, key) =>
  (Array.isArray(item?.integration_config_values) ? item.integration_config_values : []).find((v) => v?.name === key)?.value || '';

/**
 * MCP Servers listing inside the Nubi Settings modal. Server management
 * (add / edit / enable / disable / delete) lives on the Admin → Integrations
 * page; this view shows each enabled server's health as Nubi last saw it, with
 * Check now for integrations:Execute holders (#40994), and lets tenant admins
 * choose which of a server's tools need approval (#40399).
 */
const MCPConfigList = ({ stickyTable = false }) => {
  const [integrations, setIntegrations] = useState([]);
  const [loading, setLoading] = useState(false);

  const [nameInput, setNameInput] = useState('');
  const [selectedNameFilter, setSelectedNameFilter] = useState('');
  const [selectedStatusFilter, setSelectedStatusFilter] = useState('enabled');
  const [currentPage, setCurrentPage] = useState(0);
  const [recordsPerPage, setRecordsPerPage] = useState(10);
  const [totalCount, setTotalCount] = useState(0);
  const [checkingId, setCheckingId] = useState(null);
  // Check now is integrations:Execute; account-scoped grants can't reach it (v1).
  const canCheck = canManage('integrations', 'Execute');
  // The server whose tools are being marked as needing approval (#40399).
  const [approvalTarget, setApprovalTarget] = useState(null);
  const [approvalDraft, setApprovalDraft] = useState('');
  const [savingApproval, setSavingApproval] = useState(false);
  const canManageApproval = isTenantAdmin();

  const openApproval = (item) => {
    setApprovalTarget(item);
    setApprovalDraft(configValue(item, 'write_tools'));
  };

  const saveApproval = async () => {
    setSavingApproval(true);
    try {
      const response = await apiIntegrations.updateMcpWriteTools(approvalTarget.id, approvalDraft);
      const error = response?.data?.errors?.[0]?.message;
      if (error) {
        snackbar.error(error);
        return;
      }
      snackbar.success('Saved which tools need approval');
      setApprovalTarget(null);
      fetchIntegrations();
    } catch {
      snackbar.error('Failed to save which tools need approval');
    } finally {
      setSavingApproval(false);
    }
  };

  const fetchIntegrations = useCallback(async () => {
    setLoading(true);
    try {
      const response = await apiIntegrations.listIntegrations({
        type: 'mcp',
        limit: recordsPerPage,
        offset: currentPage * recordsPerPage,
        name: selectedNameFilter || undefined,
        status: selectedStatusFilter || undefined,
        withHealth: true,
      });
      // Fail-closed on GraphQL errors — same rationale as LLMConfigList.
      const gqlErrors = response?.data?.errors;
      if (Array.isArray(gqlErrors) && gqlErrors.length > 0) {
        const msg = gqlErrors[0]?.message || 'Failed to load MCP servers';
        snackbar.error(msg);
        return;
      }
      const rawRows = response?.data?.data?.integrations_list?.rows || [];
      setIntegrations(rawRows.map(parseIntegrationItem));
      setTotalCount(response?.data?.data?.integrations_aggregate?.rows?.[0]?.count || 0);
    } catch (err) {
      // eslint-disable-next-line no-console
      console.error('MCPConfigList: fetch threw', err);
      snackbar.error('Failed to load MCP servers');
    } finally {
      setLoading(false);
    }
  }, [currentPage, recordsPerPage, selectedNameFilter, selectedStatusFilter]);

  useEffect(() => {
    fetchIntegrations();
  }, [fetchIntegrations]);

  // Surface the most useful connection-shape value (connection_mode wins
  // over transport / mcp_url) for the Connection column.
  const connectionLabel = (item) => {
    const values = Array.isArray(item?.integration_config_values) ? item.integration_config_values : [];
    const cfg = values.reduce((acc, { name, value }) => {
      acc[name] = value;
      return acc;
    }, {});
    return cfg.connection_mode || cfg.transport || cfg.mcp_url || '-';
  };

  const accountChips = (item) => {
    const accs = Array.isArray(item?.integrations_cloud_accounts) ? item.integrations_cloud_accounts : [];
    const names = accs.map((d) => d?.cloud_account_name).filter(Boolean);
    if (names.length === 0) {
      return <span>-</span>;
    }
    return (
      <Stack direction='row' spacing={0.5} useFlexGap flexWrap='wrap'>
        {names.map((name, i) => (
          <Chip key={`${name}-${i}`} size='sm' variant='tag' tone='neutral'>
            {name}
          </Chip>
        ))}
      </Stack>
    );
  };

  const checkNow = useCallback(async (item) => {
    setCheckingId(item.id);
    try {
      const result = await apiIntegrations.checkMCPIntegrationHealth(item.id);
      if (result?.error) {
        snackbar.error(result.error);
        return;
      }
      const updated = { ...item, health: result.health };
      setIntegrations((rows) => rows.map((row) => (row.id === item.id ? updated : row)));
      const summary = summarizeMCPHealth(updated);
      const message = `${item.name}: ${summary.label}${summary.scopeNote ? ` (${summary.scopeNote})` : ''}`;
      if (summary.tone === 'critical') {
        snackbar.error(message);
      } else {
        snackbar.success(message);
      }
    } finally {
      setCheckingId(null);
    }
  }, []);

  const healthCell = (item) => {
    // Disabling resets the health, and a disabled server is never checked.
    if (item.status !== 'enabled') {
      return <span>-</span>;
    }
    const summary = summarizeMCPHealth(item);
    const pill = <Label tone={summary.tone}>{summary.label}</Label>;
    const details = [
      summary.scopeNote,
      summary.toolCount !== null ? `${summary.toolCount} ${summary.toolCount === 1 ? 'tool' : 'tools'}` : '',
      summary.checkedAt ? `checked ${dayjs(summary.checkedAt).fromNow()}` : '',
    ].filter(Boolean);
    return (
      <Stack direction='row' alignItems='center' spacing={1} useFlexGap flexWrap='wrap'>
        {summary.reason ? (
          <Tooltip title={summary.reason}>
            <span>{pill}</span>
          </Tooltip>
        ) : (
          pill
        )}
        {details.length > 0 && <Text value={details.join(' · ')} secondaryText />}
        {canCheck && (
          <Button
            id={`mcp-config-check-now-${item.id}`}
            tone='secondary'
            size='xs'
            loading={checkingId === item.id}
            disabled={checkingId !== null}
            onClick={() => checkNow(item)}
          >
            Check now
          </Button>
        )}
      </Stack>
    );
  };

  const approvalCell = (item) => {
    const marked = parseToolNames(configValue(item, 'write_tools'));
    return (
      <Stack direction='row' spacing={1} alignItems='center'>
        <Chip size='sm' variant='tag' tone={marked.length ? 'warning' : 'neutral'}>
          {marked.length ? `${marked.length} tool${marked.length === 1 ? '' : 's'}` : 'None'}
        </Chip>
        {canManageApproval && (
          <Button tone='secondary' size='xs' onClick={() => openApproval(item)} data-testid={`mcp-approval-manage-${item.id}`}>
            Manage
          </Button>
        )}
      </Stack>
    );
  };

  const tableData = useMemo(
    () =>
      integrations.map((item) => [
        { text: item.name || '-' },
        { text: connectionLabel(item) },
        { component: accountChips(item) },
        { text: item?.created_by_display_name || '-' },
        { component: <Label text={item.status || '-'} /> },
        { component: healthCell(item) },
        { component: approvalCell(item) },
      ]),
    [integrations, checkingId, canCheck, canManageApproval]
  );

  const selectedStatusOption = STATUS_OPTIONS.find((o) => o.value === selectedStatusFilter) ?? null;

  return (
    <>
      <ListingLayout id='mcp-config-list'>
        <ListingLayout.Toolbar>
          <Stack direction='row' alignItems='center' spacing={1}>
            <FilterDropdown
              id='mcp-config-status-filter'
              label='Status'
              options={STATUS_OPTIONS}
              value={selectedStatusOption}
              onSelect={(_e, item) => {
                setSelectedStatusFilter(item?.value || '');
                setCurrentPage(0);
              }}
            />
            <SearchInput
              id='mcp-config-name-search'
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
          </Stack>
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            id='mcp-config'
            loading={loading}
            tableData={tableData}
            headers={HEADERS}
            totalRows={totalCount}
            rowsPerPage={recordsPerPage}
            pageNumber={currentPage + 1}
            onPageChange={(page, limit) => {
              setCurrentPage(page - 1);
              setRecordsPerPage(limit);
            }}
            stickyHeader={stickyTable}
            sx={stickyTable ? { maxHeight: 'calc(90vh - 340px)', overflowY: 'auto' } : undefined}
          />
        </ListingLayout.Body>
      </ListingLayout>
      <Modal
        width='sm'
        open={!!approvalTarget}
        handleClose={() => setApprovalTarget(null)}
        title={approvalTarget ? `Tools that need approval · ${approvalTarget.name}` : ''}
      >
        {approvalTarget && (
          <Stack spacing={2}>
            <McpWriteToolsField
              value={approvalDraft}
              onChange={setApprovalDraft}
              integrationId={approvalTarget.id}
              accountId={approvalTarget.integrations_cloud_accounts?.[0]?.cloud_account_id}
              disabled={savingApproval}
            />
            <Stack direction='row' spacing={1} justifyContent='flex-end'>
              <Button tone='secondary' size='md' onClick={() => setApprovalTarget(null)} disabled={savingApproval}>
                Cancel
              </Button>
              <Button tone='primary' size='md' onClick={saveApproval} loading={savingApproval} data-testid='mcp-approval-save'>
                Save
              </Button>
            </Stack>
          </Stack>
        )}
      </Modal>
    </>
  );
};

export default MCPConfigList;
