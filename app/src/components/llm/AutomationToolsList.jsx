import React, { useCallback, useEffect, useMemo, useState } from 'react';
import PropTypes from 'prop-types';
import { Stack } from '@mui/material';
import { ListingLayout } from '@ui/ListingLayout';
import { Chip } from '@ui/Chip';
import { Link } from '@ui/Link';
import Tooltip from '@ui/Tooltip';
import SearchInput from '@ui/SearchInput';
import CustomTable from '@shared/tables/CustomTable';
import { toast as snackbar } from '@ui/Toast';
import { queryGraphQL } from '@lib/HttpService';
import { listToolApprovalAccounts } from '@api1/tool-approval';

// Automations Nubi can run as tools, read tenant-wide, with the rules Nubi's own
// list applies: active, offered to the AI, published (a live version), not an
// agent's, and described for the AI. The listing returns the draft, so the
// description is the draft's; a live version without one is still not offered.
const LIST_AI_AUTOMATIONS = `
query ListAIAutomations($limit: Int, $next_page_token: String) {
  workflow_list(request: {ai_invocable: true, status: "ACTIVE", limit: $limit, next_page_token: $next_page_token}) {
    next_page_token
    workflows {
      id
      name
      account_id
      status
      ai_invocable
      ai_auto_approve
      live_version_id
      bee
      definition {
        llm_description
      }
      created_by_user {
        display_name
        username
      }
    }
  }
}`;
const PAGE_SIZE = 200;
const MAX_PAGES = 10;

const HEADERS = ['Name', 'Account', 'Approval', 'Created By'];

const offeredToNubi = (w) =>
  Boolean(w?.ai_invocable && w?.status === 'ACTIVE' && w?.live_version_id && !w?.bee && w?.definition?.llm_description?.trim());

export async function listAIAutomations() {
  const automations = [];
  let token;
  for (let page = 0; page < MAX_PAGES; page += 1) {
    const response = await queryGraphQL(LIST_AI_AUTOMATIONS, 'ListAIAutomations', { limit: PAGE_SIZE, ...(token ? { next_page_token: token } : {}) });
    const error = response?.data?.errors?.[0]?.message;
    if (error) throw new Error(error);
    const result = response?.data?.data?.workflow_list;
    automations.push(...(result?.workflows || []).filter(offeredToNubi));
    token = result?.next_page_token;
    if (!token) break;
  }
  return automations;
}

/**
 * Automations Nubi can run as tools (#40399), and whether they ask first: an
 * automation always asks unless its author allowed it to run without asking.
 * Read-only: both settings live in each automation, which the name opens.
 */
const AutomationToolsList = ({ stickyTable = false }) => {
  const [automations, setAutomations] = useState([]);
  const [accounts, setAccounts] = useState([]);
  const [loading, setLoading] = useState(false);
  const [search, setSearch] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [rows, accountRows] = await Promise.all([listAIAutomations(), listToolApprovalAccounts()]);
      setAutomations(rows);
      setAccounts(accountRows);
    } catch (err) {
      snackbar.error(err?.message || 'Failed to load automations');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const accountName = useCallback((id) => accounts.find((a) => a.id === id)?.account_name || id || '-', [accounts]);

  const tableData = useMemo(
    () =>
      automations
        .filter((w) => !search.trim() || w.name?.toLowerCase().includes(search.trim().toLowerCase()))
        .map((w) => [
          {
            component: (
              <Link href={`/automation/${w.id}?accountId=${w.account_id}`} openInNew>
                {w.name}
              </Link>
            ),
          },
          { text: accountName(w.account_id) },
          {
            component: (
              <Tooltip
                title={
                  w.ai_auto_approve
                    ? 'Its author allowed Nubi to run it without asking. Permission checks still apply.'
                    : 'Nubi asks for approval before every run.'
                }
                placement='top'
              >
                <span>
                  <Chip size='sm' variant='tag' tone={w.ai_auto_approve ? 'neutral' : 'warning'}>
                    {w.ai_auto_approve ? 'Never asks' : 'Always asks'}
                  </Chip>
                </span>
              </Tooltip>
            ),
          },
          { text: w.created_by_user?.display_name || w.created_by_user?.username || '-' },
        ]),
    [automations, search, accountName]
  );

  return (
    <ListingLayout id='automation-tools-list'>
      <ListingLayout.Toolbar>
        <Stack direction='row' alignItems='center' spacing={1}>
          <SearchInput id='automation-tools-search' value={search} onChange={setSearch} onClear={() => setSearch('')} label='Enter Name' />
        </Stack>
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <CustomTable
          id='automation-tools'
          loading={loading}
          tableData={tableData}
          headers={HEADERS}
          totalRows={tableData.length}
          rowsPerPage={tableData.length || 10}
          stickyHeader={stickyTable}
          sx={stickyTable ? { maxHeight: 'calc(90vh - 340px)', overflowY: 'auto' } : undefined}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

AutomationToolsList.propTypes = { stickyTable: PropTypes.bool };

export default AutomationToolsList;
