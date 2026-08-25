import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { Stack } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import { ListingLayout } from '@ui/ListingLayout';
import Tooltip from '@ui/Tooltip';
import { Chip } from '@ui/Chip';
import { ds } from '@utils/colors';
import { Label } from '@ui/Label';
import FilterDropdown from '@ui/FilterDropdown';
import SearchInput from '@ui/SearchInput';
import CustomTable from '@shared/tables/CustomTable';
import { toast as snackbar } from '@ui/Toast';
import apiIntegrations from '@api1/integrations';
import { parseIntegrationItem } from '@api1/integrations/helpers';

const STATUS_OPTIONS = [
  { label: 'Enabled', value: 'enabled' },
  { label: 'Disabled', value: 'disabled' },
];

const HEADERS = ['Name', 'Account', 'Created By', 'Updated By', 'Status'];

/**
 * Read-only LLM Provider listing inside the Nubi Settings modal. All
 * management actions (add / edit / enable / disable / delete / test) now
 * live on the Admin → Integrations page; this view is purely a quick
 * what's-configured glance with a Banner pointing to the canonical
 * management surface.
 */
const LLMConfigList = ({ stickyTable = false }) => {
  const [integrations, setIntegrations] = useState([]);
  const [loading, setLoading] = useState(false);

  const [nameInput, setNameInput] = useState('');
  const [selectedNameFilter, setSelectedNameFilter] = useState('');
  // Default to no status filter — a tenant whose only LLM provider is
  // currently disabled would otherwise land on an empty tab and assume
  // nothing is configured. The status chip on each row already
  // communicates state per-row.
  const [selectedStatusFilter, setSelectedStatusFilter] = useState('');
  const [currentPage, setCurrentPage] = useState(0);
  const [recordsPerPage, setRecordsPerPage] = useState(10);
  const [totalCount, setTotalCount] = useState(0);

  const fetchIntegrations = useCallback(async () => {
    setLoading(true);
    try {
      const response = await apiIntegrations.listIntegrations({
        type: 'llm',
        limit: recordsPerPage,
        offset: currentPage * recordsPerPage,
        name: selectedNameFilter || undefined,
        status: selectedStatusFilter || undefined,
      });
      // Fail-closed on GraphQL errors so a partial-data response doesn't
      // show an empty list and mislead the operator into thinking nothing
      // is configured.
      const gqlErrors = response?.data?.errors;
      if (Array.isArray(gqlErrors) && gqlErrors.length > 0) {
        const msg = gqlErrors[0]?.message || 'Failed to load LLM configurations';
        snackbar.error(msg);
        return;
      }
      const rawRows = response?.data?.data?.integrations_list?.rows || [];
      setIntegrations(rawRows.map(parseIntegrationItem));
      setTotalCount(response?.data?.data?.integrations_aggregate?.rows?.[0]?.count || 0);
    } catch (err) {
      // eslint-disable-next-line no-console
      console.error('LLMConfigList: fetch threw', err);
      snackbar.error('Failed to load LLM configurations');
    } finally {
      setLoading(false);
    }
  }, [currentPage, recordsPerPage, selectedNameFilter, selectedStatusFilter]);

  useEffect(() => {
    fetchIntegrations();
  }, [fetchIntegrations]);

  // integrations_cloud_accounts comes back as array, object, or null depending
  // on the response shape — normalize before mapping. Each account renders as
  // its own chip in the Account column.
  const linkedAccounts = (item) => (Array.isArray(item?.integrations_cloud_accounts) ? item.integrations_cloud_accounts : []);

  // The marker sits on the config's own name rather than in a column, because
  // it is a property of this config, not of any one account it serves.
  //
  // The UI applies one default to every account a config is linked to, so the
  // normal state is all-or-nothing and a bare chip says it. A mixed state can
  // only arrive through the API (the write path accepts a per-account map); it
  // shows the count rather than being rounded to a plain "Default", which would
  // misstate the accounts it excludes.
  const nameCell = (item) => {
    const linked = linkedAccounts(item);
    const marked = linked.filter((a) => a?.default_llm_provider === true).length;
    if (marked === 0) {
      return { text: item.name || '-' };
    }
    return {
      component: (
        <Stack direction='row' spacing={0.5} alignItems='center' useFlexGap flexWrap='wrap'>
          <span>{item.name || '-'}</span>
          <Chip size='sm' variant='tag' tone='success'>
            {marked === linked.length ? 'Default' : `Default (${marked} of ${linked.length})`}
          </Chip>
          <Tooltip title='Accounts added here use this provider by default.'>
            <InfoOutlinedIcon sx={{ fontSize: ds.text.body, color: ds.gray[500], cursor: 'help' }} />
          </Tooltip>
        </Stack>
      ),
    };
  };

  const accountChips = (item) => {
    const linked = linkedAccounts(item).filter((d) => d?.cloud_account_name);
    if (linked.length === 0) {
      return <span>-</span>;
    }
    return (
      <Stack direction='row' spacing={0.5} useFlexGap flexWrap='wrap'>
        {linked.map((acc, i) => (
          <Chip key={`${acc.cloud_account_name}-${i}`} size='sm' variant='tag' tone='neutral'>
            {acc.cloud_account_name}
          </Chip>
        ))}
      </Stack>
    );
  };

  const tableData = useMemo(
    () =>
      integrations.map((item) => [
        nameCell(item),
        { component: accountChips(item) },
        { text: item?.created_by_display_name || '-' },
        { text: item?.updated_by_display_name || '-' },
        { component: <Label text={item.status || '-'} /> },
      ]),
    [integrations]
  );

  const selectedStatusOption = STATUS_OPTIONS.find((o) => o.value === selectedStatusFilter) ?? null;

  return (
    <>
      <ListingLayout id='llm-config-list'>
        <ListingLayout.Toolbar>
          <Stack direction='row' alignItems='center' spacing={1}>
            <FilterDropdown
              id='llm-config-status-filter'
              label='Status'
              options={STATUS_OPTIONS}
              value={selectedStatusOption}
              onSelect={(_e, item) => {
                setSelectedStatusFilter(item?.value || '');
                setCurrentPage(0);
              }}
            />
            <SearchInput
              id='llm-config-name-search'
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
            id='llm-config'
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

export default LLMConfigList;
