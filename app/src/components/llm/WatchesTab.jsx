import { useEffect, useMemo, useState, useCallback, useRef } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography, Chip, Tooltip, IconButton, LinearProgress } from '@mui/material';
import CancelOutlinedIcon from '@mui/icons-material/CancelOutlined';
import RefreshIcon from '@mui/icons-material/Refresh';
import api from '@api1/ask-nudgebee';
import Loader from '@shared/Loader';
import { snackbar } from '@shared/snackbarService';
import { useWatchFeatureEnabled } from '@hooks/useTenantBranding';

// Watches tab — lists every watch_resource row registered against the current
// conversation. Polls every 5s for live updates (Hasura subscriptions would be
// ideal but the existing service layer doesn't have a subscription helper, and
// 5s polling is cheap for the tab-volume we expect).
//
// Status pill colors use explicit status hexes — green/red/blue/gray/orange to
// match the rest of the app's tabular conventions.

const STATUS_STYLES = {
  PENDING: { bg: '#FEF3C7', fg: '#B45309', border: '#F59E0B' },
  ACTIVE: { bg: '#DBEAFE', fg: '#1D4ED8', border: '#3B82F6' },
  COMPLETED: { bg: '#DCFCE7', fg: '#15803D', border: '#16A34A' },
  EXPIRED: { bg: '#F3F4F6', fg: '#4B5563', border: '#9CA3AF' },
  FAILED: { bg: '#FEE2E2', fg: '#B91C1C', border: '#EF4444' },
  CANCELLED: { bg: '#F3F4F6', fg: '#4B5563', border: '#9CA3AF' },
};

const TERMINAL_STATUSES = new Set(['COMPLETED', 'EXPIRED', 'FAILED', 'CANCELLED']);

function StatusPill({ status }) {
  const s = STATUS_STYLES[status] || STATUS_STYLES.PENDING;
  return (
    <Chip
      label={status}
      size='small'
      sx={{
        bgcolor: s.bg,
        color: s.fg,
        border: `1px solid ${s.border}`,
        fontWeight: 600,
        fontSize: '11px',
        height: '22px',
      }}
      data-testid={`watch-status-${status.toLowerCase()}`}
    />
  );
}

StatusPill.propTypes = { status: PropTypes.string.isRequired };

function formatRelative(dateString) {
  if (!dateString) {
    return '-';
  }
  const then = new Date(dateString).getTime();
  const now = Date.now();
  const deltaSec = Math.round((then - now) / 1000);
  const abs = Math.abs(deltaSec);
  let unit;
  let value;
  if (abs < 60) {
    unit = 's';
    value = abs;
  } else if (abs < 3600) {
    unit = 'm';
    value = Math.round(abs / 60);
  } else if (abs < 86400) {
    unit = 'h';
    value = Math.round(abs / 3600);
  } else {
    unit = 'd';
    value = Math.round(abs / 86400);
  }
  return deltaSec >= 0 ? `in ${value}${unit}` : `${value}${unit} ago`;
}

const WATCH_FIELDS_TO_SHOW_ON_HOVER = ['source_kind', 'predicate_kind', 'predicate_expr', 'predicate_negate'];

function WatchRow({ watch: w, onCancel }) {
  const isTerminal = TERMINAL_STATUSES.has(w.status);
  const hoverDetails = WATCH_FIELDS_TO_SHOW_ON_HOVER.map((k) => `${k}: ${String(w[k])}`).join('\n');
  // For terminal watches, prioritize final_result (the canonical "what won")
  // and fall back to error/last_poll_result. last_poll_result for terminal watches
  // is sometimes the in-flight poll that fired right after the matching one — useful
  // as a diagnostic but misleading as the headline (e.g. shows "timed out" even
  // when the watch COMPLETED on the previous poll).
  const lastObsRaw = isTerminal ? w.final_result || w.error || w.last_poll_result : w.last_poll_result;
  // Coerce to string before measuring/slicing: the backend fields
  // (final_result / last_poll_result / error) have no client-side type
  // guarantee. If a structured object ever arrives, `.length`/`.slice` would
  // be undefined and React would try to render an object child ("Objects are
  // not valid as a React child" crash).
  const lastObs = lastObsRaw == null ? '' : typeof lastObsRaw === 'string' ? lastObsRaw : JSON.stringify(lastObsRaw);

  return (
    <Box
      sx={{
        p: 1.5,
        border: '1px solid var(--ds-gray-200)',
        borderRadius: 1.5,
        bgcolor: 'var(--ds-background-100)',
        display: 'flex',
        flexDirection: 'column',
        gap: 0.75,
      }}
      data-testid={`watch-row-${w.id}`}
    >
      {/* top row: status pill, watch_id (short), cancel button */}
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <StatusPill status={w.status} />
          <Tooltip title={hoverDetails} placement='right'>
            <Typography sx={{ fontFamily: 'monospace', fontSize: '12px', color: 'var(--ds-gray-500)' }}>{w.id.slice(0, 8)}…</Typography>
          </Tooltip>
        </Box>
        {!isTerminal && (
          <Tooltip title='Cancel watch'>
            <IconButton
              size='small'
              onClick={() => onCancel(w)}
              data-testid={`watch-cancel-btn-${w.id}`}
              sx={{ color: 'var(--ds-gray-500)', '&:hover': { color: '#B91C1C' } }}
            >
              <CancelOutlinedIcon sx={{ fontSize: 18 }} />
            </IconButton>
          </Tooltip>
        )}
      </Box>

      {/* mid row: poll progress + interval + duration cap */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 2, flexWrap: 'wrap' }}>
        <Typography sx={{ fontSize: '12px', color: 'var(--ds-gray-500)' }}>
          <strong>{w.poll_count}</strong> polls
          {w.failure_count > 0 && <span style={{ color: '#B91C1C', marginLeft: 6 }}>· {w.failure_count} failed</span>}
        </Typography>
        <Typography sx={{ fontSize: '12px', color: 'var(--ds-gray-500)' }}>
          every <strong>{w.poll_interval_sec}s</strong>
        </Typography>
        {!isTerminal && w.next_poll_at && (
          <Typography sx={{ fontSize: '12px', color: 'var(--ds-gray-500)' }}>next {formatRelative(w.next_poll_at)}</Typography>
        )}
        {isTerminal ? (
          <Typography sx={{ fontSize: '12px', color: 'var(--ds-gray-500)' }}>terminated {formatRelative(w.updated_at)}</Typography>
        ) : (
          <Typography sx={{ fontSize: '12px', color: 'var(--ds-gray-500)' }}>expires {formatRelative(w.expires_at)}</Typography>
        )}
      </Box>

      {/* bottom row: last observation / final result snippet (only if present) */}
      {lastObs && (
        <Box
          sx={{
            mt: 0.5,
            p: 1,
            bgcolor: 'var(--ds-gray-100)',
            borderRadius: 1,
            fontFamily: 'monospace',
            fontSize: '11px',
            color: 'var(--ds-gray-500)',
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-word',
            maxHeight: 120,
            overflow: 'auto',
          }}
        >
          {lastObs.length > 600 ? `${lastObs.slice(0, 600)}…` : lastObs}
        </Box>
      )}
    </Box>
  );
}

WatchRow.propTypes = {
  watch: PropTypes.object.isRequired,
  onCancel: PropTypes.func.isRequired,
};

export default function WatchesTab({ conversationId }) {
  const [watches, setWatches] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [refreshTick, setRefreshTick] = useState(0);
  // Per-env feature flag (LLM_SERVER_WATCH_ENABLED via /api/public/app_config).
  // When off, the /v1/watches route is unmounted on llm-server, so skip fetching
  // rather than 404.
  const watchFeatureEnabled = useWatchFeatureEnabled();

  const refresh = useCallback(() => setRefreshTick((n) => n + 1), []);

  // Backoff state lives in refs, not effect-local `let`s: the auto-refresh
  // effect must NOT re-run on every `error` change (that resets the delay to
  // 5s and defeats the backoff entirely). The effect reads the latest error
  // via errorRef instead of closing over `error`.
  const delayRef = useRef(5000);
  const errorRef = useRef(null);

  // Poll every 5s while any watch is non-terminal so the user sees live
  // updates without needing to click refresh. Once all watches are terminal,
  // we stop the timer (still allow manual refresh).
  const hasNonTerminal = useMemo(() => watches.some((w) => !TERMINAL_STATUSES.has(w.status)), [watches]);

  useEffect(() => {
    if (!conversationId || !watchFeatureEnabled) {
      setLoading(false);
      return;
    }
    let aborted = false;
    setLoading(watches.length === 0); // only show full-screen loader on first load
    api
      .listWatchesByConversation({ conversationId })
      .then((rows) => {
        if (!aborted) {
          setWatches(rows);
          setError(null);
          errorRef.current = null;
          setLoading(false);
        }
      })
      .catch((err) => {
        if (!aborted) {
          const msg = err?.message || 'Failed to load watches';
          setError(msg);
          errorRef.current = msg;
          setLoading(false);
        }
      });
    return () => {
      aborted = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conversationId, refreshTick, watchFeatureEnabled]);

  // Auto-refresh ticker. Only runs while we have non-terminal rows; cleared
  // on tab unmount or once everything is terminal. Uses a setTimeout chain
  // (rather than setInterval) so the cadence can back off on consecutive
  // errors — without this, a broken backend yielded 720 failed requests
  // per hour per open conversation tab.
  useEffect(() => {
    if (!hasNonTerminal) {
      return undefined;
    }
    let cancelled = false;
    let timer = null;
    // Delay persists across renders via delayRef so a persistent backend
    // error actually backs off (5s → 10s → … → 30s) instead of resetting
    // to 5s every time the `error` state changes. Latest error is read from
    // errorRef so this effect does not need `error` in its deps.
    const MAX_DELAY_MS = 30000;
    const schedule = () => {
      timer = setTimeout(() => {
        if (cancelled) return;
        refresh();
        if (errorRef.current) {
          delayRef.current = Math.min(delayRef.current * 2, MAX_DELAY_MS);
        } else {
          delayRef.current = 5000;
        }
        schedule();
      }, delayRef.current);
    };
    schedule();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [hasNonTerminal, refresh]);

  const handleCancel = useCallback(
    async (w) => {
      try {
        const res = await api.cancelWatch({ watchId: w.id });
        if (res?.affected_rows > 0) {
          snackbar.success(`Watch ${w.id.slice(0, 8)} cancelled`);
          refresh();
        } else {
          // The row was already terminal by the time the click landed.
          snackbar.warning('Watch was already terminal — no change');
          refresh();
        }
      } catch (err) {
        snackbar.error(`Failed to cancel watch: ${err?.message || 'unknown error'}`);
      }
    },
    [refresh]
  );

  if (loading) {
    return (
      <Box sx={{ p: 3, display: 'flex', justifyContent: 'center' }} data-testid='watches-tab-loading'>
        <Loader />
      </Box>
    );
  }

  if (error) {
    return (
      <Box sx={{ p: 3 }} data-testid='watches-tab-error'>
        <Typography sx={{ color: '#B91C1C' }}>Could not load watches: {error}</Typography>
      </Box>
    );
  }

  if (watches.length === 0) {
    return (
      <Box sx={{ p: 4, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 1 }} data-testid='watches-tab-empty'>
        <Typography className='nb-text-h3-widget' sx={{ color: 'var(--ds-gray-500)' }}>
          No watches in this conversation yet
        </Typography>
        <Typography className='nb-text-subtext-primary' sx={{ textAlign: 'center', maxWidth: 420 }}>
          Ask the agent to monitor a long-running task — e.g. <em>“ping me when this CI run finishes”</em> — and it will appear here while it polls.
        </Typography>
      </Box>
    );
  }

  return (
    <Box sx={{ p: 1.5, display: 'flex', flexDirection: 'column', gap: 1 }} data-testid='watches-tab'>
      {/* header strip with active-poll progress hint */}
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Typography className='nb-text-h3-widget'>Watches ({watches.length})</Typography>
        <Tooltip title='Refresh'>
          <IconButton size='small' onClick={refresh} data-testid='watches-refresh-btn'>
            <RefreshIcon sx={{ fontSize: 18 }} />
          </IconButton>
        </Tooltip>
      </Box>
      {hasNonTerminal && <LinearProgress variant='indeterminate' sx={{ height: 2, bgcolor: 'var(--ds-gray-100)' }} />}
      {watches.map((w) => (
        <WatchRow key={w.id} watch={w} onCancel={handleCancel} />
      ))}
    </Box>
  );
}

WatchesTab.propTypes = {
  conversationId: PropTypes.string,
};
