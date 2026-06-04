import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { Stack, Box } from '@mui/material';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Banner } from '@components1/ds/Banner';
import { Chip } from '@components1/ds/Chip';
import { Label } from '@components1/ds/Label';
import FilterDropdown from '@components1/common/FilterDropdownButton';
import CustomSearch from '@common-new/CustomSearch';
import CustomTable from '@common-new/tables/CustomTable2';
import { toast as snackbar } from '@components1/ds/Toast';
import apiIntegrations from '@api1/integrations';
import { parseIntegrationItem } from '@api1/integrations/helpers';
import { useRouter } from 'next/router';

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
const LLMConfigList = () => {
  const router = useRouter();
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

  const tableData = useMemo(
    () =>
      integrations.map((item) => [
        { text: item.name || '-' },
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
      <Box sx={{ mb: 2 }}>
        <Banner
          tone='info'
          surface='section'
          actionsPlacement='inline'
          message={
            <>
              This view is read-only. Manage <strong>LLM Providers</strong> from <strong>Admin → Integrations → LLM</strong>.
            </>
          }
          actions={[
            {
              label: 'Manage in Admin',
              onClick: () => router.push('/accounts/account-form?cloudProvider=llm'),
            },
          ]}
        />
      </Box>

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
            <CustomSearch
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
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default LLMConfigList;
