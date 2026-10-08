import { useEffect, useState } from 'react';
import { Box } from '@mui/material';
import { Banner } from '@ui/Banner';
import { ProgressLinear } from '@ui/ProgressLinear';
import apiKubernetes1 from '@api1/kubernetes1';
import { safeJSONParse } from 'src/utils/common';
import { AGENT_CONNECTED } from './pendingAgentInstall';

/**
 * The cloud-account counterpart of the Kubernetes "waiting for the agent" step.
 *
 * A cloud account has no agent to install. Its agent row is written by
 * cloud-collector, and several of its jobs write it: events and resources always
 * say CONNECTED, even when they fail. Only the cost sync — the job account
 * creation kicks off — gives a real verdict, and only once its
 * `connection_status.spends` key is present do `status` and `status_message`
 * carry that verdict (see updateOrCreateAgentStatus in cloud-collector). So that
 * key, not the status column alone, is what "the first sync finished" means.
 */
const POLL_MS = 10000;
// The cost sync backfills history, so a large account can take far longer than
// this. Past it we stop the spinner and say so rather than hold the user.
const TIMEOUT_MS = 3 * 60 * 1000;

export type CloudFirstSyncPhase = 'idle' | 'waiting' | 'done' | 'attention' | 'timeout';

export interface CloudFirstSyncRow {
  cloud_account_id: string;
  status?: string | null;
  status_message?: string | null;
  connection_status?: unknown;
}

export interface CloudFirstSyncIssue {
  accountId: string;
  /** NOT_CONNECTED: the cost sync failed. Otherwise it finished with a warning. */
  failed: boolean;
  message: string;
}

type ConnectionStatus = { spends?: { err?: string } };

const parseConnectionStatus = (value: unknown): ConnectionStatus => {
  if (typeof value === 'string') {
    return safeJSONParse(value) || {};
  }
  return value && typeof value === 'object' ? (value as ConnectionStatus) : {};
};

interface CloudFirstSyncState {
  phase: CloudFirstSyncPhase;
  issues: CloudFirstSyncIssue[];
}

export const evaluateCloudFirstSync = (accountIds: string[], rows: CloudFirstSyncRow[]): { allReported: boolean; issues: CloudFirstSyncIssue[] } => {
  const issues: CloudFirstSyncIssue[] = [];
  let reportedCount = 0;
  accountIds.forEach((accountId) => {
    const row = rows.find((r) => r?.cloud_account_id === accountId);
    const spends = row ? parseConnectionStatus(row.connection_status)?.spends : undefined;
    if (!row || !spends) {
      return;
    }
    reportedCount += 1;
    const failed = row.status !== AGENT_CONNECTED;
    const message = row.status_message || spends?.err || '';
    if (failed || message) {
      issues.push({ accountId, failed, message: message || 'The cost sync did not complete.' });
    }
  });
  return { allReported: accountIds.length > 0 && reportedCount === accountIds.length, issues };
};

/**
 * Polls until every account's first cost sync has reported, or the timeout
 * passes. `provider` is the agent type cloud-collector writes: AWS, Azure, GCP.
 * An empty id list means nothing is being waited on.
 */
export const useCloudFirstSync = (accountIds: string[], provider: string): CloudFirstSyncState => {
  const idsKey = accountIds.join(',');
  const [state, setState] = useState<CloudFirstSyncState>({ phase: 'idle', issues: [] });

  useEffect(() => {
    if (!idsKey) {
      setState({ phase: 'idle', issues: [] });
      return undefined;
    }
    const ids = idsKey.split(',');
    const startedAt = Date.now();
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    setState({ phase: 'waiting', issues: [] });

    // Chained rather than setInterval so a slow response never overlaps the next.
    const check = async (): Promise<void> => {
      const rows = await apiKubernetes1.listCloudAccountFirstSync(ids, provider);
      if (cancelled) {
        return;
      }
      const { allReported, issues } = evaluateCloudFirstSync(ids, rows);
      if (allReported) {
        setState({ phase: issues.length > 0 ? 'attention' : 'done', issues });
        return;
      }
      if (Date.now() - startedAt >= TIMEOUT_MS) {
        setState({ phase: 'timeout', issues });
        return;
      }
      timer = setTimeout(check, POLL_MS);
    };
    check();
    return (): void => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [idsKey, provider]);

  return state;
};

interface CloudFirstSyncStatusProps {
  phase: CloudFirstSyncPhase;
  issues: CloudFirstSyncIssue[];
  /** Shown in copy, e.g. "AWS". */
  providerLabel: string;
  /** Display name per account id, for listing issues. */
  accountNames?: Record<string, string>;
}

export const CloudFirstSyncStatus = ({ phase, issues, providerLabel, accountNames = {} }: CloudFirstSyncStatusProps): JSX.Element | null => {
  if (phase === 'idle' || phase === 'done') {
    return null;
  }

  if (phase === 'waiting') {
    return (
      <Box mt='var(--ds-space-5)' data-testid='cloud-first-sync-waiting'>
        <ProgressLinear surface='section' tone='info' aria-label='Waiting for the first data sync' />
        <Box mt='var(--ds-space-2)'>
          <Banner
            tone='info'
            surface='section'
            title={`Waiting for the first data sync from your ${providerLabel} account`}
            message='This window closes on its own when the first sync finishes. You can also close it now; the sync keeps running.'
          />
        </Box>
      </Box>
    );
  }

  const issueLines = issues.map((issue) => (
    <Box component='div' key={issue.accountId}>
      {issues.length > 1 || accountNames[issue.accountId] ? `${accountNames[issue.accountId] || issue.accountId}: ` : ''}
      {issue.message}
    </Box>
  ));

  if (phase === 'timeout') {
    return (
      <Box mt='var(--ds-space-5)' data-testid='cloud-first-sync-timeout'>
        <Banner
          tone='info'
          surface='section'
          title='Still syncing'
          message={
            <>
              <Box component='div'>
                The first sync is taking longer than usual, which is normal for accounts with a lot of history. It keeps running in the background, so
                it is safe to close this window.
              </Box>
              {issueLines}
            </>
          }
        />
      </Box>
    );
  }

  const anyFailed = issues.some((issue) => issue.failed);
  return (
    <Box mt='var(--ds-space-5)' data-testid='cloud-first-sync-attention'>
      <Banner
        tone={anyFailed ? 'critical' : 'warning'}
        surface='section'
        title={anyFailed ? 'The first cost sync failed' : 'The first sync finished with a warning'}
        message={
          <>
            {issueLines}
            <Box component='div' sx={{ mt: 'var(--ds-space-1)' }}>
              The account was added. Resource and event collection runs separately and is not blocked by this.
            </Box>
          </>
        }
      />
    </Box>
  );
};
