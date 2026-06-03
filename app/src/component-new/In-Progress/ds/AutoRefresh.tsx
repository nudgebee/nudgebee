/**
 * AutoRefresh — DS V2 of legacy AutoRefreshControls.
 * Spec: app/design-system/primitives/feedback/auto-refresh.html
 *
 * Polling-interval picker with on/off and a manual-refresh affordance.
 * Standardises the interval set across every page that fetches live data.
 *
 * Variants per spec:
 *   interval    = 'off' | '15s' | '30s' | '1m' | '5m'   (the entire menu — fixed)
 *   composition = 'interval-only' | 'interval+manual' | 'interval+manual+last-updated'
 *                 (auto from `onManualRefresh` + `lastUpdatedAt` props)
 *   size        = 'sm' | 'md'
 *
 * Don't (per spec):
 *   - Don't introduce a new interval. The five values are the entire menu —
 *     any addition risks runaway polling.
 *   - Don't poll while the tab is hidden. The primitive honours the Page
 *     Visibility API automatically (timer pauses while document.hidden).
 *
 * Migration:
 *   `import AutoRefreshControls from '@components1/common/AutoRefreshControls'`
 * → `import { AutoRefresh } from '@components1/ds/AutoRefresh'`
 *   Interval list standardised to 5 values.
 */
import * as React from 'react';
import { Box, ButtonBase, Menu, MenuItem } from '@mui/material';
import RefreshIcon from '@mui/icons-material/Refresh';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';

export type AutoRefreshInterval = 'off' | '15s' | '30s' | '1m' | '5m';
export type AutoRefreshComposition = 'interval-only' | 'interval+manual' | 'interval+manual+last-updated';
export type AutoRefreshSize = 'sm' | 'md';

export interface AutoRefreshProps {
  /** Currently selected interval. */
  interval: AutoRefreshInterval;
  /** Fired when the user picks a new interval. */
  onChange: (next: AutoRefreshInterval) => void;
  /** Fired when the user clicks the manual-refresh button OR when the timer ticks. */
  onManualRefresh?: () => void;
  /** Last-updated timestamp (Date or epoch ms). Renders as relative ("12 s ago") in the
   *  `interval+manual+last-updated` composition. Updates every 5s. */
  lastUpdatedAt?: Date | number;
  /** Force a composition. If omitted, derived from `onManualRefresh` + `lastUpdatedAt` props. */
  composition?: AutoRefreshComposition;
  size?: AutoRefreshSize;
  className?: string;
  id?: string;
}

const INTERVALS: AutoRefreshInterval[] = ['off', '15s', '30s', '1m', '5m'];

const INTERVAL_LABEL: Record<AutoRefreshInterval, string> = {
  off: 'Off',
  '15s': '15s',
  '30s': '30s',
  '1m': '1m',
  '5m': '5m',
};

const INTERVAL_MS: Record<AutoRefreshInterval, number | null> = {
  off: null,
  '15s': 15_000,
  '30s': 30_000,
  '1m': 60_000,
  '5m': 300_000,
};

const SIZE_TOKENS: Record<AutoRefreshSize, { height: string; fontSize: string; iconSize: number; gap: string; padX: string }> = {
  sm: { height: '24px', fontSize: 'var(--ds-text-caption)', iconSize: 12, gap: '6px', padX: 'var(--ds-space-2)' },
  md: { height: '28px', fontSize: 'var(--ds-text-small)', iconSize: 14, gap: 'var(--ds-space-2)', padX: 'var(--ds-space-3)' },
};

function formatRelative(ts: Date | number): string {
  const t = ts instanceof Date ? ts.getTime() : ts;
  const diffSec = Math.max(0, Math.floor((Date.now() - t) / 1000));
  if (diffSec < 60) return `${diffSec} s ago`;
  const min = Math.floor(diffSec / 60);
  if (min < 60) return `${min} m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} h ago`;
  return `${Math.floor(hr / 24)} d ago`;
}

function deriveComposition(p: AutoRefreshProps): AutoRefreshComposition {
  if (p.composition) return p.composition;
  if (p.lastUpdatedAt !== undefined) return 'interval+manual+last-updated';
  if (p.onManualRefresh) return 'interval+manual';
  return 'interval-only';
}

export function AutoRefresh(props: AutoRefreshProps) {
  const { interval, onChange, onManualRefresh, lastUpdatedAt, size = 'sm', className, id } = props;
  const composition = deriveComposition(props);
  const tokens = SIZE_TOKENS[size];
  const [anchorEl, setAnchorEl] = React.useState<HTMLElement | null>(null);
  const [, forceTick] = React.useState(0);

  // Polling timer with Page Visibility API integration
  React.useEffect(() => {
    const ms = INTERVAL_MS[interval];
    if (ms == null) return;
    let timerId: ReturnType<typeof setInterval> | null = null;

    const start = () => {
      if (timerId) return;
      timerId = setInterval(() => {
        if (typeof document !== 'undefined' && !document.hidden) {
          onManualRefresh?.();
        }
      }, ms);
    };
    const stop = () => {
      if (timerId) {
        clearInterval(timerId);
        timerId = null;
      }
    };
    const handleVisibility = () => {
      if (typeof document !== 'undefined' && document.hidden) stop();
      else start();
    };

    if (typeof document !== 'undefined' && !document.hidden) start();
    if (typeof document !== 'undefined') document.addEventListener('visibilitychange', handleVisibility);
    return () => {
      stop();
      if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', handleVisibility);
    };
  }, [interval, onManualRefresh]);

  // Tick relative-time display every 5s when last-updated is shown
  React.useEffect(() => {
    if (composition !== 'interval+manual+last-updated' || lastUpdatedAt === undefined) return;
    const tickId = setInterval(() => forceTick((n) => n + 1), 5_000);
    return () => clearInterval(tickId);
  }, [composition, lastUpdatedAt]);

  const intervalButton = (
    <ButtonBase
      onClick={(e) => setAnchorEl(e.currentTarget)}
      aria-haspopup='menu'
      aria-expanded={Boolean(anchorEl)}
      sx={{
        height: tokens.height,
        paddingLeft: tokens.padX,
        paddingRight: tokens.padX,
        fontSize: tokens.fontSize,
        fontWeight: 'var(--ds-font-weight-medium)',
        color: 'var(--ds-gray-700)',
        backgroundColor: 'var(--ds-background-100)',
        border: '1px solid var(--ds-gray-300)',
        borderRadius: 'var(--ds-radius-sm)',
        display: 'inline-flex',
        alignItems: 'center',
        gap: '4px',
        cursor: 'pointer',
        '&:hover': { borderColor: 'var(--ds-gray-400)', backgroundColor: 'var(--ds-gray-100)' },
        '&.Mui-focusVisible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '1px' },
      }}
    >
      <RefreshIcon sx={{ fontSize: tokens.iconSize }} />
      <Box component='span'>Auto · {INTERVAL_LABEL[interval]}</Box>
      <KeyboardArrowDownIcon sx={{ fontSize: tokens.iconSize }} />
    </ButtonBase>
  );

  const manualButton =
    composition !== 'interval-only' ? (
      <ButtonBase
        onClick={() => onManualRefresh?.()}
        aria-label='Refresh now'
        sx={{
          width: tokens.height,
          height: tokens.height,
          borderRadius: 'var(--ds-radius-sm)',
          color: 'var(--ds-gray-600)',
          '&:hover': { backgroundColor: 'var(--ds-gray-100)', color: 'var(--ds-gray-700)' },
          '&.Mui-focusVisible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '1px' },
        }}
      >
        <RefreshIcon sx={{ fontSize: tokens.iconSize + 2 }} />
      </ButtonBase>
    ) : null;

  const lastUpdatedText =
    composition === 'interval+manual+last-updated' && lastUpdatedAt !== undefined ? `Updated ${formatRelative(lastUpdatedAt)}` : null;

  return (
    <Box id={id} className={className} sx={{ display: 'inline-flex', alignItems: 'center', gap: tokens.gap }}>
      {intervalButton}
      {manualButton}
      {lastUpdatedText && (
        <Box
          component='span'
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: 'var(--ds-gray-500)',
          }}
        >
          {lastUpdatedText}
        </Box>
      )}
      <Menu
        anchorEl={anchorEl}
        open={Boolean(anchorEl)}
        onClose={() => setAnchorEl(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
        transformOrigin={{ vertical: 'top', horizontal: 'left' }}
        slotProps={{
          paper: {
            sx: {
              minWidth: '120px',
              borderRadius: 'var(--ds-radius-md)',
              border: '1px solid var(--ds-gray-200)',
              boxShadow: '0px 4px 20px var(--ds-gray-alpha-200)',
              marginTop: '4px',
            },
          },
        }}
      >
        {INTERVALS.map((iv) => (
          <MenuItem
            key={iv}
            selected={iv === interval}
            onClick={() => {
              onChange(iv);
              setAnchorEl(null);
            }}
            sx={{
              fontSize: tokens.fontSize,
              color: 'var(--ds-gray-700)',
              minHeight: 0,
              padding: 'var(--ds-space-2) var(--ds-space-3)',
              '&.Mui-selected': {
                backgroundColor: 'var(--ds-blue-100)',
                color: 'var(--ds-blue-700)',
                fontWeight: 'var(--ds-font-weight-semibold)',
              },
            }}
          >
            {INTERVAL_LABEL[iv]}
          </MenuItem>
        ))}
      </Menu>
    </Box>
  );
}

export default AutoRefresh;
