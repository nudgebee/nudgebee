import { useCallback, useEffect, useState } from 'react';
import { useRouter } from 'next/router';
import { Box, Typography } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { Skeleton } from '@ui/Skeleton';
import Tooltip from '@ui/Tooltip';
import { ds } from 'src/utils/colors';
import { useTenantBranding } from '@hooks/useTenantBranding';
import BriefingHeader from './BriefingHeader';
import BriefingTile from './BriefingTile';
import BriefingCallout from './BriefingCallout';
import { useBriefingData } from './useBriefingData';
import type { ComparisonRow } from './resolveBriefing';

const COLLAPSE_STORAGE_KEY = 'troubleshoot:briefing:collapsed:v1';

const COLUMN_WIDTHS = '1fr 1.05fr 1.25fr 0.85fr';

const FLAGGED_MAX_HEIGHT = 196;

const ColumnShell = ({ title, meta, info, children }: { title: string; meta?: string; info?: string; children: React.ReactNode }) => (
  <Box
    sx={{
      display: 'flex',
      flexDirection: 'column',
      minWidth: 0,
      padding: 'var(--ds-space-3) var(--ds-space-4)',
      '&:not(:first-of-type)': { borderLeft: `1px solid ${ds.gray[200]}` },
      '@media (max-width: 899px)': {
        '&:not(:first-of-type)': { borderLeft: 'none', borderTop: `1px solid ${ds.gray[200]}` },
      },
    }}
  >
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)', marginBottom: '10px' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', minWidth: 0 }}>
        <Typography
          sx={{
            fontSize: ds.text.caption,
            fontWeight: ds.weight.semibold,
            letterSpacing: '0.06em',
            textTransform: 'uppercase',
            color: ds.brand[600],
          }}
        >
          {title}
        </Typography>
        {info && (
          <Tooltip title={info}>
            <InfoOutlinedIcon sx={{ fontSize: ds.text.caption, color: ds.gray[400], cursor: 'help', flexShrink: 0 }} />
          </Tooltip>
        )}
      </Box>
      {meta && <Typography sx={{ fontSize: '10px', color: ds.gray[400], letterSpacing: '0.03em', whiteSpace: 'nowrap' }}>{meta}</Typography>}
    </Box>
    {children}
  </Box>
);

const TileGrid = ({ children }: { children: React.ReactNode }) => (
  <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px 12px' }}>{children}</Box>
);

const ComparisonTable = ({ rows, onDrillDown }: { rows: ComparisonRow[]; onDrillDown?: (query: Record<string, string>) => void }) => (
  <Box sx={{ border: `1px solid ${ds.gray[100]}`, borderRadius: 'var(--ds-radius-sm)', overflow: 'hidden', marginBottom: '10px' }}>
    {rows.map((row, index) => {
      const clickable = Boolean(row.drill && onDrillDown);
      return (
        <Box
          key={row.key}
          sx={{
            display: 'grid',
            gridTemplateColumns: '1fr auto 46px',
            gap: 'var(--ds-space-2)',
            alignItems: 'center',
            padding: '5px 10px',
            borderBottom: index === rows.length - 1 ? 'none' : `1px solid ${ds.gray[100]}`,
            backgroundColor: row.emphasis ? ds.brand[100] : index % 2 === 1 ? ds.background[200] : 'transparent',
            ...(clickable ? { cursor: 'pointer', '&:hover': { backgroundColor: ds.blue[100] } } : {}),
          }}
          {...(clickable ? { role: 'button', tabIndex: 0, onClick: () => onDrillDown?.(row.drill!) } : {})}
          data-testid={`briefing-comparison-${row.key}`}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '3px', minWidth: 0 }}>
            <Typography
              sx={{
                fontSize: ds.text.caption,
                fontWeight: row.emphasis ? ds.weight.semibold : ds.weight.regular,
                color: row.emphasis ? ds.brand[600] : ds.gray[700],
              }}
            >
              {row.label}
            </Typography>
            {row.tooltip && (
              <Tooltip title={row.tooltip}>
                <InfoOutlinedIcon sx={{ fontSize: ds.text.caption, color: ds.gray[400], cursor: 'help', flexShrink: 0 }} />
              </Tooltip>
            )}
          </Box>
          <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.brand[600], fontVariantNumeric: 'tabular-nums' }}>
            {row.value}
          </Typography>
          <Typography
            sx={{
              fontSize: ds.text.caption,
              color: row.emphasis ? ds.gray[700] : ds.gray[600],
              fontWeight: row.emphasis ? ds.weight.medium : ds.weight.regular,
              textAlign: 'right',
              fontVariantNumeric: 'tabular-nums',
            }}
          >
            {row.secondary ?? ''}
          </Typography>
        </Box>
      );
    })}
  </Box>
);

const LoadingColumns = () => (
  <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '1fr 1fr', lg: COLUMN_WIDTHS } }}>
    {['a', 'b', 'c', 'd'].map((column) => (
      <Box key={column} sx={{ padding: 'var(--ds-space-3) var(--ds-space-4)', display: 'flex', flexDirection: 'column', gap: '10px' }}>
        <Skeleton shape='text' size='caption' width='60%' />
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px 12px' }}>
          {[0, 1, 2, 3].map((tile) => (
            <Skeleton key={tile} shape='rect' height={52} />
          ))}
        </Box>
      </Box>
    ))}
  </Box>
);

interface Props {
  onDrillDown?: (query: Record<string, string>) => void;
}

const NubiBriefing = ({ onDrillDown }: Props) => {
  const { assistantName, nubiIconUrl } = useTenantBranding();
  const router = useRouter();
  // briefingWindow still pins the drill-down to the window the numbers were
  // counted over, even though the filters themselves now live on the page.
  const { loading, error, model, window: briefingWindow } = useBriefingData();

  const isActive = useCallback(
    (drill?: Record<string, string>) => {
      if (!drill) return false;
      const keys = Object.keys(drill).filter((key) => key !== 'status');
      return keys.length > 0 && keys.every((key) => router.query[key] === drill[key]);
    },
    [router.query]
  );

  const drillDown = useCallback(
    (query: Record<string, string>) =>
      onDrillDown?.({ ...query, start_time: String(briefingWindow.startMs), end_time: String(briefingWindow.endMs) }),
    [onDrillDown, briefingWindow]
  );

  const [expanded, setExpanded] = useState(true);
  useEffect(() => {
    try {
      setExpanded(localStorage.getItem(COLLAPSE_STORAGE_KEY) !== 'true');
    } catch {}
  }, []);

  const toggle = () => {
    setExpanded((previous) => {
      const next = !previous;
      try {
        localStorage.setItem(COLLAPSE_STORAGE_KEY, String(!next));
      } catch {}
      return next;
    });
  };

  if (error) {
    return (
      <Box sx={{ width: '100%', padding: 'var(--ds-space-4) 0' }}>
        <Banner
          tone='critical'
          surface='section'
          title='Briefing unavailable'
          message="Couldn't load the triage aggregates for this window. The events list below is unaffected."
        />
      </Box>
    );
  }

  return (
    <Box sx={{ width: '100%', padding: 'var(--ds-space-4) 0' }} data-testid='nubi-briefing'>
      <Card variant='elevated' size='sm' sx={{ padding: 0, overflow: 'hidden' }}>
        {loading || !model ? (
          <LoadingColumns />
        ) : (
          <>
            <BriefingHeader model={model} expanded={expanded} onToggle={toggle} assistantName={assistantName} iconUrl={nubiIconUrl} />

            {expanded && (
              <Box
                sx={{
                  display: 'grid',
                  gridTemplateColumns: { xs: '1fr', md: '1fr 1fr', lg: COLUMN_WIDTHS },
                  alignItems: 'stretch',
                }}
              >
                <ColumnShell title='What came in' meta='all always-on'>
                  <TileGrid>
                    {model.intake.map((tile) => (
                      <BriefingTile key={tile.key} tile={tile} active={isActive(tile.drill)} onDrillDown={drillDown} />
                    ))}
                  </TileGrid>
                </ColumnShell>

                <ColumnShell title={`How ${assistantName} ranked it`} meta='always-on'>
                  <TileGrid>
                    {model.ranking.map((tile) => (
                      <BriefingTile key={tile.key} tile={tile} active={isActive(tile.drill)} onDrillDown={drillDown} />
                    ))}
                  </TileGrid>
                </ColumnShell>

                <ColumnShell title={`Where ${assistantName} disagreed`} meta={model.comparisonMeta} info={model.mappingCaveat}>
                  <ComparisonTable rows={model.comparison} onDrillDown={drillDown} />
                  <TileGrid>
                    {model.disagreementTiles.map((tile) => (
                      <BriefingTile key={tile.key} tile={tile} active={isActive(tile.drill)} onDrillDown={drillDown} />
                    ))}
                  </TileGrid>
                </ColumnShell>

                <ColumnShell title='Flagged' meta={model.flaggedMeta}>
                  <Box
                    sx={{
                      display: 'flex',
                      flexDirection: 'column',
                      gap: 'var(--ds-space-2)',
                      maxHeight: `${FLAGGED_MAX_HEIGHT}px`,
                      overflowY: 'auto',
                    }}
                  >
                    {model.callouts.map((callout) => (
                      <BriefingCallout key={callout.key} callout={callout} />
                    ))}
                    {model.flaggedNote && (
                      <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], lineHeight: 1.5 }} data-testid='briefing-flagged-note'>
                        {model.flaggedNote}
                      </Typography>
                    )}
                  </Box>
                </ColumnShell>
              </Box>
            )}
          </>
        )}
      </Card>
    </Box>
  );
};

export default NubiBriefing;
