import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useRouter } from 'next/router';
import { Badge, Box, CircularProgress, Typography } from '@mui/material';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import SettingsIcon from '@mui/icons-material/Settings';
import AddIcon from '@mui/icons-material/Add';
import { Button } from '@ui/Button';
import { DropdownMenu, type DropdownMenuItem } from '@ui/DropdownMenu';
import apiWorkflow from '@api1/workflow';
import { snackbar } from '@shared/snackbarService';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import { colors, ds } from 'src/utils/colors';
import Datetime from '@shared/format/Datetime';
import TriggerWorkflowModal from './TriggerWorkflowModal';
import { getDefaultTriggerInputs, getPrimaryTriggerType, getWorkflowInputSchema } from '../utils/workflowTriggerHelpers';

export interface TriggeredExecution {
  workflow_id: string;
  workflow_name?: string;
  id: string;
  status: string;
  start_time?: string;
  close_time?: string;
}

interface RunAutomationMenuProps {
  accountId: string;
  // Event this menu lists triggered executions for. The list is fetched here
  // (one mount fetch for the badge count) and smart-polled every 5s while the
  // dropdown is open, so newly triggered runs and their live status stay current.
  eventId: string;
  disabled?: boolean;
  // Gates the run/create/configure actions. When false the menu still shows the
  // read-only "Triggered for this event" list, but offers no way to run, create,
  // or configure automations. Defaults to false so the gate fails closed.
  canRun?: boolean;
  onCreateAutomation?: () => void;
}

// Poll the triggered-executions list every 5s while the dropdown is open. Closed
// dropdown = no polling, so API usage is bounded to when the user is looking.
const EXECUTIONS_POLL_INTERVAL_MS = 5000;

interface WorkflowListItem {
  id: string;
  name: string;
  status?: string;
  definition?: any;
  tags?: any;
  created_at?: string;
  created_by_user?: { id?: string; display_name?: string } | null;
  last_execution_status?: string;
  last_execution_time?: string;
}

type LoadState = 'idle' | 'loading' | 'loaded' | 'error';

const StatusDot: React.FC<{ color: string }> = ({ color }) => (
  <Box sx={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: color, flexShrink: 0 }} />
);

const WorkflowRow: React.FC<{ workflow: WorkflowListItem }> = ({ workflow }) => {
  const firstName = workflow.created_by_user?.display_name?.split(' ')[0];
  const hasRun = !!workflow.last_execution_time;
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1.5, minWidth: 0, py: 0.25, width: '100%' }}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.25, minWidth: 0, flex: 1 }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: colors.text.primary,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {workflow.name}
        </Typography>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, whiteSpace: 'nowrap', overflow: 'hidden' }}>
          {firstName && (
            <>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondaryDark, lineHeight: 1.4 }}>by {firstName}</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary }}>·</Typography>
            </>
          )}
          {hasRun ? (
            <Datetime
              baseDate={new Date()}
              value={workflow.last_execution_time as string}
              sxSuffix={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary }}
              sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondary }}
            />
          ) : (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary }}>never run</Typography>
          )}
        </Box>
      </Box>

      <Box sx={{ display: 'flex', alignItems: 'center', flexShrink: 0, whiteSpace: 'nowrap' }}>
        {hasRun ? (
          <Datetime
            baseDate={new Date()}
            value={workflow.last_execution_time as string}
            sxSuffix={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary }}
            sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondary }}
          />
        ) : (
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary }}>never run</Typography>
        )}
      </Box>
    </Box>
  );
};

const triggeredStatusDotColor = (status: string): string => {
  const s = (status || '').toUpperCase();
  if (s === 'COMPLETED' || s === 'SUCCESS') return ds.green[500];
  if (s === 'RUNNING' || s === 'IN_PROGRESS' || s === 'INPROGRESS') return ds.amber[500];
  return ds.red[500];
};

const TriggeredExecutionRow: React.FC<{ ex: TriggeredExecution }> = ({ ex }) => {
  const name = ex.workflow_name || `${ex.workflow_id.slice(0, 8)}…`;
  const time = ex.close_time || ex.start_time;
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1.5, minWidth: 0, py: 0.25, width: '100%' }}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.25, minWidth: 0, flex: 1 }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: colors.text.primary,
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {name}
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondaryDark, lineHeight: 1.4 }}>
          triggered for this event
        </Typography>
      </Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5, flexShrink: 0, whiteSpace: 'nowrap' }}>
        <StatusDot color={triggeredStatusDotColor(ex.status)} />
        {time && (
          <Datetime
            baseDate={new Date()}
            value={time}
            sxSuffix={{ fontSize: 'var(--ds-text-caption)', color: colors.text.tertiary }}
            sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondary }}
          />
        )}
      </Box>
    </Box>
  );
};

const RunAutomationMenu: React.FC<RunAutomationMenuProps> = ({ accountId, eventId, disabled = false, canRun = false, onCreateAutomation }) => {
  const router = useRouter();
  const [workflows, setWorkflows] = useState<WorkflowListItem[]>([]);
  const [loadState, setLoadState] = useState<LoadState>('idle');
  const [errorMessage, setErrorMessage] = useState('');
  const [executions, setExecutions] = useState<TriggeredExecution[]>([]);

  const [selectedWorkflow, setSelectedWorkflow] = useState<WorkflowListItem | null>(null);
  const [modalOpen, setModalOpen] = useState(false);
  const [triggerLoading, setTriggerLoading] = useState(false);

  // Tracks the current accountId/eventId so an in-flight fetch can detect that
  // the caller switched account or event mid-flight and drop its stale response.
  const currentAccountIdRef = useRef<string>(accountId);
  useEffect(() => {
    currentAccountIdRef.current = accountId;
  }, [accountId]);
  const currentEventIdRef = useRef<string>(eventId);
  useEffect(() => {
    currentEventIdRef.current = eventId;
  }, [eventId]);

  // ── Triggered-executions list: mount fetch (badge) + open-gated 5s poll ──
  const menuOpenRef = useRef(false);
  const pollTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Drops async results that resolve after unmount so we never setState on an
  // unmounted component (the fetch/poll can outlive the menu).
  const isMountedRef = useRef(true);
  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  const fetchExecutions = useCallback(async () => {
    if (!accountId || !eventId) return;
    const reqAccountId = accountId;
    const reqEventId = eventId;
    try {
      const resp: any = await apiWorkflow.listExecutionsForEvent(reqAccountId, reqEventId);
      if (!isMountedRef.current) return;
      // Drop the response if the active event/account changed while in flight.
      if (currentAccountIdRef.current !== reqAccountId || currentEventIdRef.current !== reqEventId) return;
      setExecutions(resp?.data?.executions || []);
    } catch (err) {
      if (isMountedRef.current) console.error('Failed to load triggered executions:', err);
    }
  }, [accountId, eventId]);

  const stopExecutionsPoll = useCallback(() => {
    if (pollTimeoutRef.current) {
      clearTimeout(pollTimeoutRef.current);
      pollTimeoutRef.current = null;
    }
  }, []);

  // Recursive setTimeout (not setInterval) so requests never overlap: the next
  // tick is only scheduled after the current fetch settles, and only while open.
  const pollExecutions = useCallback(async () => {
    if (!menuOpenRef.current || !isMountedRef.current) return;
    await fetchExecutions();
    // Re-check after the await: the menu may have closed or the component
    // unmounted while the fetch was in flight — don't schedule another tick.
    if (!menuOpenRef.current || !isMountedRef.current) return;
    pollTimeoutRef.current = setTimeout(pollExecutions, EXECUTIONS_POLL_INTERVAL_MS);
  }, [fetchExecutions]);

  const handleMenuOpen = useCallback(() => {
    menuOpenRef.current = true;
    stopExecutionsPoll();
    // Immediate fetch on open, then the loop schedules itself every 5s.
    pollExecutions();
  }, [stopExecutionsPoll, pollExecutions]);

  const handleMenuClose = useCallback(() => {
    menuOpenRef.current = false;
    stopExecutionsPoll();
  }, [stopExecutionsPoll]);

  // One fetch per (account, event) so the closed-button badge count is correct
  // before the dropdown is ever opened. Resets the list when the event changes.
  const lastFetchedExecKeyRef = useRef<string | null>(null);
  useEffect(() => {
    if (!accountId || !eventId) return;
    const key = `${accountId}:${eventId}`;
    if (lastFetchedExecKeyRef.current === key) return;
    lastFetchedExecKeyRef.current = key;
    setExecutions([]);
    fetchExecutions();
  }, [accountId, eventId, fetchExecutions]);

  // Pause polling when the tab is hidden; resume on return if the menu is open.
  useEffect(() => {
    const handleVisibilityChange = () => {
      if (document.hidden) {
        stopExecutionsPoll();
      } else if (menuOpenRef.current) {
        pollExecutions();
      }
    };
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      document.removeEventListener('visibilitychange', handleVisibilityChange);
      // Flip the gate so any in-flight poll bails instead of rescheduling.
      menuOpenRef.current = false;
      stopExecutionsPoll();
    };
  }, [stopExecutionsPoll, pollExecutions]);

  const fetchWorkflows = useCallback(async () => {
    if (!accountId) return;
    const requestAccountId = accountId;
    setLoadState('loading');
    setErrorMessage('');
    try {
      const response: any = await apiWorkflow.listWorkflows(requestAccountId);
      // Drop the response if the active accountId changed while the request was in flight.
      if (currentAccountIdRef.current !== requestAccountId) return;
      const apiError = parseHttpResponseBodyMessage(response);
      if (apiError) {
        setErrorMessage(apiError);
        setLoadState('error');
        return;
      }
      const list: WorkflowListItem[] = response?.data?.workflow_list?.workflows || [];
      setWorkflows(list);
      setLoadState('loaded');
    } catch (err) {
      if (currentAccountIdRef.current !== requestAccountId) return;
      console.error('Failed to load automations:', err);
      setErrorMessage('Failed to load automations');
      setLoadState('error');
    }
  }, [accountId]);

  // Pre-fetch on mount so the dropdown opens without a loading flicker on
  // first click. `automationAccountId` upstream flips from router.query →
  // row.cloud_account_id once the event row loads; track the last fetched
  // value in a ref so we don't repeat the call for an unchanged accountId.
  const lastFetchedAccountIdRef = useRef<string | null>(null);
  useEffect(() => {
    // View-only users never see the run list, so skip the fetch entirely.
    if (!canRun) return;
    if (!accountId) return;
    if (lastFetchedAccountIdRef.current === accountId) return;
    lastFetchedAccountIdRef.current = accountId;
    fetchWorkflows();
  }, [canRun, accountId, fetchWorkflows]);

  const handleSelect = (workflow: WorkflowListItem) => {
    setSelectedWorkflow(workflow);
    setModalOpen(true);
  };

  const handleModalClose = () => {
    setModalOpen(false);
    setSelectedWorkflow(null);
    setTriggerLoading(false);
  };

  const handleTrigger = async (inputs: any) => {
    // Throw on every failure path. TriggerWorkflowModal calls `onClose()` only
    // after `await onTrigger(...)` resolves — returning a resolved promise on
    // error would close the modal and discard the user's input.
    if (!selectedWorkflow?.id || !accountId) {
      const msg = 'Invalid automation or account ID';
      snackbar.error(msg);
      throw new Error(msg);
    }
    setTriggerLoading(true);
    try {
      const response: any = await apiWorkflow.triggerWorkflow({
        account_id: accountId,
        id: selectedWorkflow.id,
        inputs,
      });
      const errorMsg = parseHttpResponseBodyMessage(response);
      if (errorMsg) throw new Error(errorMsg);
      const triggerData = response?.data?.workflow_execute;
      if (!triggerData?.execution_id) throw new Error('Failed to trigger automation');
      snackbar.success(`Automation "${selectedWorkflow.name}" triggered`);
      // Refresh so the just-triggered execution surfaces on the next open even
      // though the modal closed the dropdown (which stopped the poll loop).
      fetchExecutions();
    } catch (err) {
      console.error('Error triggering automation:', err);
      const msg = err instanceof Error && err.message ? err.message : `Failed to trigger automation "${selectedWorkflow.name}"`;
      snackbar.error(msg);
      throw err;
    } finally {
      setTriggerLoading(false);
    }
  };

  const goToWorkflowsPage = useCallback(() => {
    router.push(`/auto-pilot?accountId=${accountId}#workflow`);
  }, [router, accountId]);

  const goToExecution = useCallback(
    (ex: TriggeredExecution) => {
      const url = `/workflow/${ex.workflow_id}?accountId=${accountId}&executionId=${ex.id}#executions`;
      window.open(url, '_blank', 'noopener,noreferrer');
    },
    [accountId]
  );

  const validTriggered = useMemo(() => executions.filter((ex) => ex?.workflow_id && ex?.id), [executions]);
  const triggeredCount = validTriggered.length;

  const triggeredItems: DropdownMenuItem[] = useMemo(() => {
    const valid = validTriggered;
    if (valid.length === 0) return [];
    return [
      { type: 'section' as const, label: 'Triggered for this event' },
      ...valid.map((ex) => ({
        label: <TriggeredExecutionRow ex={ex} />,
        onSelect: () => goToExecution(ex),
        id: `triggered-execution-${ex.id}`,
        searchText: ex.workflow_name || ex.workflow_id,
      })),
    ];
  }, [validTriggered, goToExecution]);

  const items: DropdownMenuItem[] = useMemo(() => {
    // View-only users (no write access) see just the triggered list — no run,
    // create, or configure actions.
    if (!canRun) {
      if (triggeredItems.length === 0) {
        return [
          {
            label: (
              <Typography sx={{ fontSize: 'var(--ds-text-body)', color: colors.text.secondaryDark }}>
                No automations triggered for this event
              </Typography>
            ),
            disabled: true,
            onSelect: () => {},
          },
        ];
      }
      return triggeredItems;
    }
    // A separator divides the triggered list from the run section when both show.
    const runHeader: DropdownMenuItem[] =
      triggeredItems.length > 0
        ? [{ type: 'separator' as const }, { type: 'section' as const, label: 'Run an automation' }]
        : [{ type: 'section' as const, label: 'Run an automation' }];
    if (loadState === 'loading') {
      return [
        ...triggeredItems,
        ...runHeader,
        {
          label: (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
              <CircularProgress size={14} />
              <Typography sx={{ fontSize: 'var(--ds-text-body)', color: colors.text.secondaryDark }}>Loading automations…</Typography>
            </Box>
          ),
          disabled: true,
          onSelect: () => {},
        },
      ];
    }
    if (loadState === 'error') {
      return [
        ...triggeredItems,
        ...runHeader,
        {
          label: (
            <Typography sx={{ fontSize: 'var(--ds-text-body)', color: colors.error }}>{errorMessage || 'Failed to load automations'}</Typography>
          ),
          disabled: true,
          onSelect: () => {},
        },
      ];
    }
    if (loadState === 'loaded' && workflows.length === 0) {
      return [
        ...triggeredItems,
        ...runHeader,
        {
          label: <Typography sx={{ fontSize: 'var(--ds-text-body)', color: colors.text.secondaryDark }}>No automations configured</Typography>,
          disabled: true,
          onSelect: () => {},
        },
        { type: 'separator' as const },
        {
          label: 'Configure automations →',
          icon: <SettingsIcon fontSize='small' />,
          onSelect: goToWorkflowsPage,
          id: 'run-automation-configure',
        },
      ];
    }
    return [
      ...triggeredItems,
      ...runHeader,
      ...workflows.map((w) => ({
        label: <WorkflowRow workflow={w} />,
        onSelect: () => handleSelect(w),
        id: `run-automation-item-${w.id}`,
        searchText: w.name,
      })),
    ];
    // handleSelect is stable for the lifetime of the dropdown's open state —
    // recomputing items on each change of selected workflow would force the
    // menu to remount and close.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canRun, loadState, workflows, errorMessage, triggeredItems, goToWorkflowsPage]);

  const headerActions =
    canRun && onCreateAutomation ? (
      <Button
        tone='primary'
        size='sm'
        composition='icon+text'
        icon={<AddIcon sx={{ fontSize: 16 }} />}
        aria-label='Create automation'
        tooltip='Create automation'
        data-testid='run-automation-create'
        onClick={(e) => {
          e.stopPropagation();
          onCreateAutomation();
        }}
      >
        Create
      </Button>
    ) : undefined;

  return (
    <>
      <Box sx={{ mr: 'var(--ds-space-2)' }}>
        <DropdownMenu
          align='end'
          side='bottom'
          size='sm'
          minWidth={380}
          itemsMaxHeight='min(420px, calc(100vh - 260px))'
          searchable
          searchPlaceholder='Search automations…'
          onRefresh={() => {
            fetchWorkflows();
            fetchExecutions();
          }}
          refreshLabel='Refresh list'
          headerActions={headerActions}
          onClose={handleMenuClose}
          trigger={
            <Badge
              badgeContent={triggeredCount}
              overlap='rectangular'
              data-testid='run-automation-triggered-badge'
              onClick={handleMenuOpen}
              sx={{
                '& .MuiBadge-badge': {
                  top: 4,
                  right: 6,
                  height: 16,
                  minWidth: 16,
                  fontSize: 10,
                  padding: '0 4px',
                  backgroundColor: ds.brand[600],
                  color: ds.background[100],
                },
              }}
            >
              <Button
                id='run-automation-btn'
                data-testid='run-automation-btn'
                tone='secondary'
                size='sm'
                composition='text+icon'
                iconPlacement='end'
                icon={<KeyboardArrowDownIcon sx={{ fontSize: 16 }} />}
                tooltip={triggeredCount > 0 ? `${triggeredCount} triggered for this event` : 'Run an automation'}
                disabled={disabled || !accountId}
              >
                Automations
              </Button>
            </Badge>
          }
          items={items}
        />
      </Box>

      {selectedWorkflow && (
        <TriggerWorkflowModal
          open={modalOpen}
          onClose={handleModalClose}
          workflowName={selectedWorkflow.name}
          triggerType={getPrimaryTriggerType(selectedWorkflow)}
          defaultInputs={getDefaultTriggerInputs(selectedWorkflow)}
          inputSchema={getWorkflowInputSchema(selectedWorkflow)}
          onTrigger={handleTrigger}
          loading={triggerLoading}
        />
      )}
    </>
  );
};

export default RunAutomationMenu;
