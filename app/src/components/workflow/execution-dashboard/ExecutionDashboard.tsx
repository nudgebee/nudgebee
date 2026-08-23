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
import { ds } from '@utils/colors';
import type { AccountExecutionItem } from '@api1/workflow/types';
import { getDuration, getStatusTone } from '../utils/executionStatus';
import useExecutionDashboard from './useExecutionDashboard';
import ExecutionSummaryCards from './ExecutionSummaryCards';
import MostFailedAutomations from './MostFailedAutomations';
import ExecutionDetailDrawer from './ExecutionDetailDrawer';
import { EXECUTION_STATUS_OPTIONS, MAX_PAGEABLE_ROWS, TABLE_HEADERS, executionUserLabel } from './constants';

interface ExecutionDashboardProps {
  accountId?: string;
}

const TABLE_ID = 'execution-dashboard-table';

/**
 * Executions across every automation in the account.
 *
 * Two behaviours here are consequences of the Temporal visibility store and
 * are deliberate, not omissions:
 *   - There are no sort controls. The SQL visibility store cannot ORDER BY, so
 *     rows are always newest-first.
 *   - Counts render with a "≈" prefix and the date range is clamped to the
 *     namespace retention, because executions older than that do not exist.
 */
const ExecutionDashboard: React.FC<ExecutionDashboardProps> = ({ accountId }) => {
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
    automationOptions,
    userOptions,
    refresh,
  } = useExecutionDashboard(accountId);

  const [selectedExecution, setSelectedExecution] = useState<AccountExecutionItem | null>(null);

  const retentionDays = aggregate?.retention_days || 0;

  const selectedOptions = (options: { value: string; label: string }[], selected: string[]) =>
    options.filter((option) => selected.includes(option.value));

  const tableData = useMemo(
    () =>
      executions.map((execution) => [
        { component: <Datetime value={execution.start_time} baseDate={new Date()} /> },
        {
          component: (
            <Typography
              component='a'
              href={`/workflow/${execution.workflow_id}?accountId=${accountId}#executions`}
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
          ),
        },
        {
          component: (
            <Typography
              title={execution.id}
              sx={{ fontFamily: 'var(--ds-font-mono)', fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}
            >
              {execution.id.slice(0, 8)}
            </Typography>
          ),
          // Merged into the object CustomTable hands to onRowClick.
          drilldownQuery: { executionId: execution.id },
        },
        { component: <Label text={execution.status.toUpperCase()} tone={getStatusTone(execution.status)} /> },
        {
          component: (
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
          component: (
            <Typography
              title={execution.failure_reason || execution.trigger_type || ''}
              sx={{
                fontSize: 'var(--ds-text-small)',
                color: execution.failure_reason ? 'var(--ds-red-600)' : 'var(--ds-gray-600)',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {execution.failure_reason || execution.trigger_type || 'Manual'}
            </Typography>
          ),
        },
      ]),
    [executions, accountId]
  );

  // The server refuses to page past MAX_PAGEABLE_ROWS, so don't render page
  // numbers the user would only get an error from.
  const pageableRows = Math.min(totalRows, MAX_PAGEABLE_ROWS);

  return (
    <Box>
      <ExecutionSummaryCards aggregate={aggregate} loading={loading} retentionDays={retentionDays} />

      <MostFailedAutomations
        entries={aggregate?.top_failed || []}
        approximate={!!aggregate?.top_failed_is_approximate}
        retentionDays={retentionDays}
        onSelectAutomation={(workflowId) => updateFilters({ workflowIds: [workflowId] })}
      />

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
            {`Filters apply to this table only; the summary above is not affected. Sorted newest first.${
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
            totalRows={pageableRows}
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

      <ExecutionDetailDrawer execution={selectedExecution} accountId={accountId} onClose={() => setSelectedExecution(null)} />
    </Box>
  );
};

export default ExecutionDashboard;
