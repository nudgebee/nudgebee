import React, { useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Refresh, InfoOutlined } from '@mui/icons-material';
import dayjs from 'dayjs';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import CustomTable from '@shared/tables/CustomTable';
import Datetime from '@shared/format/Datetime';
import { Label } from '@ui/Label';
import { Button } from '@ui/Button';
import Tooltip from '@ui/Tooltip';
import { ds } from '@utils/colors';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import type { AccountExecutionItem } from '@api1/workflow/types';
import { getDuration, getStatusTone } from '../utils/executionStatus';
import useExecutionDashboard from './useExecutionDashboard';
import ExecutionSummaryCards from './ExecutionSummaryCards';
import MostFailedAutomations from './MostFailedAutomations';
import ExecutionDetailDrawer from './ExecutionDetailDrawer';
import { EXECUTION_STATUS_OPTIONS, TABLE_HEADERS, executionUserLabel } from './constants';

const TABLE_ID = 'execution-dashboard-table';

/** Two lines of error text, then ellipsis — the rest lives in the tooltip. */
const ERROR_CELL_CLAMP = {
  fontSize: 'var(--ds-text-small)',
  display: '-webkit-box',
  WebkitBoxOrient: 'vertical' as const,
  WebkitLineClamp: 2,
  overflow: 'hidden',
  wordBreak: 'break-word' as const,
};

const renderAccountGroupIcon = (provider: string) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

/**
 * Executions across every automation in every account the caller can read,
 * narrowed by the Account filter.
 *
 * Two behaviours here are consequences of the Temporal visibility store and
 * are deliberate, not omissions:
 *   - There are no sort controls. The SQL visibility store cannot ORDER BY, so
 *     rows are always newest-first.
 *   - Counts render with a "≈" prefix and the date range is clamped to the
 *     namespace retention, because executions older than that do not exist.
 */
const ExecutionDashboard: React.FC = () => {
  const {
    filters,
    updateFilters,
    page,
    pageSize,
    changePage,
    executions,
    totalRows,
    loading,
    error,
    aggregate,
    aggregateLoading,
    automationOptions,
    userOptions,
    accounts,
    accountOptions,
    refresh,
  } = useExecutionDashboard();

  const [selectedExecution, setSelectedExecution] = useState<AccountExecutionItem | null>(null);

  const retentionDays = aggregate?.retention_days || 0;

  const selectedOptions = (options: { value: string; label: string }[], selected: string[]) =>
    options.filter((option) => selected.includes(option.value));

  const tableData = useMemo(
    () =>
      executions.map((execution) => [
        {
          component: <Datetime value={execution.start_time} baseDate={new Date()} />,
          // Merged into the object CustomTable hands to onRowClick. Lives on
          // the first cell now that the Execution ID column is gone — the id
          // itself is drawer-only detail, not something to scan a column of.
          drilldownQuery: { executionId: execution.id },
        },
        {
          // The builder needs an account, and a visibility record written
          // before nb_account_id existed carries none — link only when the row
          // can actually say where it ran, rather than sending the user to
          // `?accountId=undefined`.
          component: execution.account_id ? (
            <Typography
              component='a'
              href={`/automation/${execution.workflow_id}?accountId=${execution.account_id}#executions`}
              // New tab: the dashboard is a triage surface, and losing the
              // filtered table to inspect one automation is the wrong trade.
              target='_blank'
              rel='noopener noreferrer'
              id={`execution-dashboard-automation-${execution.id}`}
              title={execution.workflow_name || execution.workflow_id}
              sx={{
                fontSize: 'var(--ds-text-body)',
                color: 'var(--ds-blue-500)',
                textDecoration: 'none',
                display: 'block',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
                '&:hover': { textDecoration: 'underline' },
              }}
              onClick={(event: React.MouseEvent) => event.stopPropagation()}
            >
              {execution.workflow_name || execution.workflow_id}
            </Typography>
          ) : (
            <Typography
              title={execution.workflow_name || execution.workflow_id}
              sx={{
                fontSize: 'var(--ds-text-body)',
                color: 'var(--ds-gray-700)',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {execution.workflow_name || execution.workflow_id}
            </Typography>
          ),
        },
        {
          component: (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, overflow: 'hidden' }}>
              {execution.account_id && accounts[execution.account_id]?.cloud_provider && (
                <CloudProviderIcon cloud_provider={accounts[execution.account_id].cloud_provider} width='14px' height='14px' />
              )}
              <Typography
                title={(execution.account_id && accounts[execution.account_id]?.name) || execution.account_id || ''}
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  color: 'var(--ds-gray-700)',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {(execution.account_id && accounts[execution.account_id]?.name) || execution.account_id || '-'}
              </Typography>
            </Box>
          ),
        },
        { component: <Label text={execution.status.toUpperCase()} tone={getStatusTone(execution.status)} /> },
        {
          component: (
            <Box sx={{ minWidth: 0 }}>
              <Typography
                title={executionUserLabel(execution.user_name, execution.triggered_by)}
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  color: 'var(--ds-gray-700)',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {executionUserLabel(execution.user_name, execution.triggered_by)}
              </Typography>
              {/* How the run started. Used to sit in the Details column, where
                  it read as an error's peer on rows that had no error. */}
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  color: 'var(--ds-gray-500)',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {execution.trigger_type || 'Manual'}
              </Typography>
            </Box>
          ),
        },
        {
          component: (
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)' }}>
              {getDuration(execution.start_time, execution.close_time)}
            </Typography>
          ),
        },
        {
          // Failures only. Errors run long — clamp to two lines, put the whole
          // message in a tooltip, and leave the full text (with stack) to the
          // drawer rather than letting one row stretch the table.
          component: execution.failure_reason ? (
            <Tooltip title={execution.failure_reason} variant='explainer' desc='Open the row for the full error.'>
              <Typography sx={{ ...ERROR_CELL_CLAMP, color: 'var(--ds-red-600)' }}>{execution.failure_reason}</Typography>
            </Tooltip>
          ) : (
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>—</Typography>
          ),
        },
      ]),
    [executions, accounts]
  );

  return (
    <Box>
      {/* Headline left, breakdown right. The failure count and the automations
          it came from are one question, so they sit on one row; the right panel
          drops away when there are no failures to attribute. */}
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: { xs: '1fr', md: '360px minmax(0, 1fr)' },
          gap: 'var(--ds-space-3)',
          padding: 'var(--ds-space-4) 0',
          // Default `stretch`: the two cards are one row of information and a
          // ragged bottom edge reads as a rendering bug.
        }}
      >
        <ExecutionSummaryCards aggregate={aggregate} loading={aggregateLoading} retentionDays={retentionDays} />

        <MostFailedAutomations
          entries={aggregate?.top_failed || []}
          loading={aggregateLoading}
          approximate={!!aggregate?.top_failed_is_approximate}
          totalFailures={aggregate?.failed || 0}
          onSelectAutomation={(workflowId) => updateFilters({ workflowIds: [workflowId] })}
        />
      </Box>

      <ListingLayout id='execution-dashboard'>
        <ListingLayout.Toolbar
          actions={
            <>
              <CustomDateTimeRangePicker
                passedSelectedDateTime={{
                  startTime: filters.startDate.getTime(),
                  endTime: filters.endDate.getTime(),
                  shortcutClickTime: 0,
                }}
                // Executions older than the namespace retention do not exist,
                // so don't let the user ask for them.
                minDate={dayjs().subtract(retentionDays > 0 ? retentionDays : 60, 'day')}
                onChange={(passed: any) => {
                  const range = passed?.selection || passed;
                  updateFilters({ startDate: new Date(range.startTime), endDate: new Date(range.endTime) });
                }}
                sx={{ height: ds.space[6] }}
              />
              <Button
                composition='icon-only'
                tone='secondary'
                size='sm'
                aria-label='Refresh executions'
                data-testid='execution-dashboard-refresh-btn'
                icon={<Refresh sx={{ fontSize: 'var(--ds-text-body-lg)' }} />}
                onClick={refresh}
              />
            </>
          }
        >
          <FilterDropdown
            id='execution-dashboard-filter-account'
            label='Account'
            multiple
            grouped
            groupIcon={renderAccountGroupIcon}
            options={accountOptions}
            value={accountOptions.filter((option) => filters.accountIds.includes(option.value))}
            onSelect={(_event: any, items: any) => updateFilters({ accountIds: (items || []).map((item: any) => item.value) })}
          />
          <FilterDropdown
            id='execution-dashboard-filter-automation'
            label='Automation'
            multiple
            options={automationOptions}
            value={selectedOptions(automationOptions, filters.workflowIds)}
            onSelect={(_event: any, items: any) => updateFilters({ workflowIds: (items || []).map((item: any) => item.value) })}
          />
          <FilterDropdown
            id='execution-dashboard-filter-user'
            label='User'
            multiple
            options={userOptions}
            value={selectedOptions(userOptions, filters.triggeredBy)}
            onSelect={(_event: any, items: any) => updateFilters({ triggeredBy: (items || []).map((item: any) => item.value) })}
          />
          <FilterDropdown
            id='execution-dashboard-filter-status'
            label='Status'
            multiple
            options={EXECUTION_STATUS_OPTIONS}
            value={selectedOptions(EXECUTION_STATUS_OPTIONS, filters.statuses)}
            onSelect={(_event: any, items: any) => updateFilters({ statuses: (items || []).map((item: any) => item.value) })}
          />
        </ListingLayout.Toolbar>

        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', padding: `0 ${ds.space[5]}` }}>
          <InfoOutlined sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)' }} />
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
            {/* Account is a scope, not a table filter — it replaced the page-level
                account the summary used to be pinned to, so it moves the cards
                too. The rest still narrow only the table. */}
            {`Account applies to the whole page; the other filters apply to this table only. Sorted newest first.${
              retentionDays > 0 ? ` Execution history is retained for ${retentionDays} days.` : ''
            }`}
          </Typography>
        </Box>

        <ListingLayout.Body>
          <CustomTable
            id={TABLE_ID}
            headers={TABLE_HEADERS}
            tableData={tableData}
            loading={loading}
            rowsPerPage={pageSize}
            pageNumber={page}
            totalRows={totalRows}
            onPageChange={(nextPage: number, nextPageSize: number) => changePage(nextPage, nextPageSize)}
            onRowClick={(query: any) => {
              const match = executions.find((execution) => execution.id === query?.executionId);
              if (match) setSelectedExecution(match);
            }}
            showEmptyStateText
            emptyStateText={error || 'No executions in this range.'}
          />
        </ListingLayout.Body>
      </ListingLayout>

      <ExecutionDetailDrawer execution={selectedExecution} onClose={() => setSelectedExecution(null)} />
    </Box>
  );
};

export default ExecutionDashboard;
