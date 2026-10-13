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
import { canManage } from '@lib/auth';
import { summarizeMCPHealth } from './mcpHealthSummary';

dayjs.extend(relativeTime);

const STATUS_OPTIONS = [
  { label: 'Enabled', value: 'enabled' },
  { label: 'Disabled', value: 'disabled' },
];

const HEADERS = ['Name', 'Connection', 'Account', 'Created By', 'Status', 'Health'];

/**
 * MCP Servers listing inside the Nubi Settings modal. All management actions
 * (add / edit / enable / disable / delete) live on the Admin → Integrations
 * page; this view is a what's-configured glance plus each enabled server's
 * health as Nubi last saw it, with Check now for integrations:Execute holders
 * (#40994).
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

  const tableData = useMemo(
    () =>
      integrations.map((item) => [
        { text: item.name || '-' },
        { text: connectionLabel(item) },
        { component: accountChips(item) },
        { text: item?.created_by_display_name || '-' },
        { component: <Label text={item.status || '-'} /> },
        { component: healthCell(item) },
      ]),
    [integrations, checkingId, canCheck]
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
    </>
  );
};

export default MCPConfigList;
