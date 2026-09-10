import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import apiWorkflow from '@api1/workflow';
import { ds } from 'src/utils/colors';
import Datetime from '@shared/format/Datetime';
import type { TriggeredExecution } from './RunAutomationMenu';

interface EventAutomationRunsListProps {
  accountId: string;
  eventId: string;
  // Bumped by the caller after a successful trigger so the new run appears
  // without waiting for a poll tick (a just-triggered run isn't in the list yet,
  // so the "any run unfinished" poll gate wouldn't be open).
  refreshKey?: number;
}

// Statuses a run can still move out of. Anything else is settled, so once no row
// is in this set the list stops refetching.
const RUNNING_STATUSES = new Set(['RUNNING', 'IN_PROGRESS', 'INPROGRESS', 'PENDING', 'QUEUED', 'SCHEDULED']);
const POLL_INTERVAL_MS = 5000;

const statusDotColor = (status: string): string => {
  const s = (status || '').toUpperCase();
  if (s === 'COMPLETED' || s === 'SUCCESS') return ds.green[500];
  if (RUNNING_STATUSES.has(s)) return ds.amber[500];
  return ds.red[500];
};

// Read-only list of the automations that ran for this event, rendered as rows in
// the body of the "Run an automation" card. Shares only the fetch
// (listExecutionsForEvent) with RunAutomationMenu, whose dropdown-button shape
// doesn't fit an inline card section.
const EventAutomationRunsList: React.FC<EventAutomationRunsListProps> = ({ accountId, eventId, refreshKey = 0 }) => {
  const [executions, setExecutions] = useState<TriggeredExecution[]>([]);

  // Drops async results that resolve after unmount or after the caller switched
  // event/account mid-flight.
  const isMountedRef = useRef(true);
  const currentKeyRef = useRef(`${accountId}:${eventId}`);
  useEffect(() => {
    currentKeyRef.current = `${accountId}:${eventId}`;
  }, [accountId, eventId]);
  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  const fetchExecutions = useCallback(async () => {
    if (!accountId || !eventId) return;
    const reqKey = `${accountId}:${eventId}`;
    try {
      const resp: any = await apiWorkflow.listExecutionsForEvent(accountId, eventId);
      if (!isMountedRef.current || currentKeyRef.current !== reqKey) return;
      setExecutions((resp?.data?.executions || []).filter((ex: TriggeredExecution) => ex?.workflow_id && ex?.id));
    } catch (err) {
      if (isMountedRef.current) console.error('Failed to load automation runs for event:', err);
    }
  }, [accountId, eventId]);

  useEffect(() => {
    if (!accountId || !eventId) {
      setExecutions([]);
      return undefined;
    }
    setExecutions([]);
    fetchExecutions();
    return undefined;
  }, [accountId, eventId, refreshKey, fetchExecutions]);

  // Recursive setTimeout (not setInterval) so requests never overlap, and only
  // while a run is unfinished — a settled list costs nothing.
  const hasActiveRun = executions.some((ex) => RUNNING_STATUSES.has((ex.status || '').toUpperCase()));
  useEffect(() => {
    if (!hasActiveRun) return undefined;
    let timeoutId: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;
    const tick = async () => {
      await fetchExecutions();
      if (cancelled) return;
      timeoutId = setTimeout(tick, POLL_INTERVAL_MS);
    };
    timeoutId = setTimeout(tick, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      if (timeoutId) clearTimeout(timeoutId);
    };
  }, [hasActiveRun, fetchExecutions]);

  if (executions.length === 0) return null;

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column' }}>
      {executions.map((ex) => {
        const name = ex.workflow_name || `${ex.workflow_id.slice(0, 8)}…`;
        const time = ex.close_time || ex.start_time;
        return (
          <Box
            key={ex.id}
            data-testid='event-automation-run-link'
            component='a'
            href={`/automation/${ex.workflow_id}?accountId=${accountId}&executionId=${ex.id}#executions`}
            target='_blank'
            rel='noopener noreferrer'
            sx={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: ds.space[3],
              textDecoration: 'none',
              borderTop: `1px solid ${ds.gray[200]}`,
              px: ds.space[4],
              py: ds.space[3],
              '&:hover': { backgroundColor: ds.gray[100] },
            }}
          >
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], minWidth: 0 }}>
              <Box sx={{ width: 8, height: 8, borderRadius: ds.radius.pill, backgroundColor: statusDotColor(ex.status), flexShrink: 0 }} />
              <Typography
                sx={{
                  fontSize: ds.text.body,
                  fontWeight: ds.weight.medium,
                  color: ds.blue[500],
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {name}
              </Typography>
            </Box>
            {time && (
              <Datetime
                baseDate={new Date()}
                value={time}
                sxSuffix={{ fontSize: ds.text.caption, color: ds.gray[600] }}
                sx={{ fontSize: ds.text.caption, color: ds.brand[500], flexShrink: 0 }}
              />
            )}
          </Box>
        );
      })}
    </Box>
  );
};

export default EventAutomationRunsList;
