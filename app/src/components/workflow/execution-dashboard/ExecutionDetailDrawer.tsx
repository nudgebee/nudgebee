import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import CustomDrawer from '@shared/CustomDrawer';
import { Button } from '@ui/Button';
import { Label } from '@ui/Label';
import Datetime from '@shared/format/Datetime';
import { Skeleton } from '@ui/Skeleton';
import apiWorkflow from '@api1/workflow';
import type { AccountExecutionItem } from '@api1/workflow/types';
import { getDuration, getStatusTone } from '../utils/executionStatus';
import { executionUserLabel } from './constants';

interface ExecutionDetailDrawerProps {
  execution: AccountExecutionItem | null;
  onClose: () => void;
}

const Field: React.FC<{ label: string; children: React.ReactNode }> = ({ label, children }) => (
  <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'baseline' }}>
    <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', minWidth: '110px' }}>{label}</Typography>
    <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)', minWidth: 0 }}>{children}</Box>
  </Box>
);

/**
 * Row detail. The list only carries a one-line failure reason, so the full
 * error (with stack) is fetched lazily here via workflow_get_execution rather
 * than being loaded for every row of every page.
 */
const ExecutionDetailDrawer: React.FC<ExecutionDetailDrawerProps> = ({ execution, onClose }) => {
  // The drawer follows whichever run is selected, so the account comes off the
  // row rather than the page — the dashboard spans accounts now.
  const accountId = execution?.account_id;
  const [detail, setDetail] = useState<any>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!execution || !accountId) {
      setDetail(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setDetail(null);
    apiWorkflow
      .getWorkflowExecution(accountId, execution.workflow_id, execution.id)
      .then((response: any) => {
        if (cancelled) return;
        setDetail(response?.data?.workflow_get_execution || null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [execution, accountId]);

  if (!execution) return null;

  // Same caveat as the table's link: without an account there is no valid
  // builder URL, so offer the button only when the row carries one.
  const fullViewHref = accountId ? `/workflow/${execution.workflow_id}?accountId=${accountId}&executionId=${execution.id}#executions` : '';

  return (
    <CustomDrawer open onClose={onClose} variant='modern' width='640px' storageKey='nb.executionDashboardDrawer.width' title='Execution details'>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)', padding: 'var(--ds-space-4)' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
          <Label size='md' dot text={execution.status.toUpperCase()} tone={getStatusTone(execution.status)} />
          <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' }}>
            {execution.workflow_name || execution.workflow_id}
          </Typography>
        </Box>

        <Field label='Execution ID'>
          <Typography sx={{ fontFamily: 'var(--ds-font-mono)', fontSize: 'var(--ds-text-caption)' }}>{execution.id}</Typography>
        </Field>
        <Field label='Started'>
          <Datetime value={execution.start_time} />
        </Field>
        <Field label='Duration'>{getDuration(execution.start_time, execution.close_time)}</Field>
        <Field label='Trigger'>{execution.trigger_type || 'Manual'}</Field>
        <Field label='User'>{executionUserLabel(execution.user_name, execution.triggered_by)}</Field>

        {loading && <Skeleton shape='rect' width='100%' height={120} />}

        {!loading && detail?.error && (
          <Box>
            <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-red-600)' }}>
              Execution error
            </Typography>
            <Box
              sx={{
                mt: 'var(--ds-space-1)',
                fontFamily: 'monospace',
                fontSize: 'var(--ds-text-caption)',
                color: 'var(--ds-red-600)',
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
                backgroundColor: 'var(--ds-red-100)',
                padding: 'var(--ds-space-2)',
                borderRadius: 'var(--ds-radius-sm)',
                border: '1px solid var(--ds-red-200)',
                maxHeight: '260px',
                overflow: 'auto',
              }}
            >
              {detail.error}
            </Box>
          </Box>
        )}

        {fullViewHref && (
          <Button
            tone='secondary'
            size='sm'
            data-testid='execution-dashboard-open-full-view-btn'
            // New tab, same reasoning as the table's automation links: keep the
            // filtered dashboard behind you.
            onClick={() => window.open(fullViewHref, '_blank', 'noopener,noreferrer')}
          >
            Open full execution view
          </Button>
        )}
      </Box>
    </CustomDrawer>
  );
};

export default ExecutionDetailDrawer;
