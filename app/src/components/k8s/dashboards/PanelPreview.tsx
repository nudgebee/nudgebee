import React from 'react';
import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { ds } from '@utils/colors';
import type { AccountOption, Panel } from '@api1/dashboards';
import DashboardPanel from './DashboardPanel';
import { panelMinHeight } from './panelDefaults';
import { resolvePanelAccounts } from './panelAccounts';
import {
  discreteSignature,
  fetchSignature,
  isRunnable,
  previewRangeLabel,
  PREVIEW_DEBOUNCE_MS,
  PREVIEW_FALLBACK_RANGE_MS,
  sampleData,
  sampleSeriesLabels,
  sampleTableData,
  usesManualRun,
} from './panelPreviewRules';
import type { VariableValues } from './templating';

/** Share of a modal body the preview rail takes, in the editor and the library alike. */
export const PREVIEW_RAIL_WIDTH = '44%';

/** The window a preview runs over when its host has no range of its own. */
export function usePreviewRange(open: boolean) {
  const [range, setRange] = React.useState(freshRange);
  React.useEffect(() => {
    if (open) setRange(freshRange());
  }, [open]);
  return range;
}

function freshRange() {
  const now = Date.now();
  return { start: now - PREVIEW_FALLBACK_RANGE_MS, end: now };
}

function accountScopeNote(preview: Panel, scoped: AccountOption[]): string | undefined {
  if (preview.datasource === 'nudgebee') {
    // The query engine takes every account in one call.
    if (scoped.length === 0) return undefined;
    return scoped.length === 1 ? scoped[0].label : `${scoped.length} accounts`;
  }
  if (scoped.length === 1) return scoped[0].label;
  if (scoped.length > 1) {
    // Every other datasource queries the first account and offers the rest behind its filter.
    return `${scoped[0].label} — first of ${scoped.length}`;
  }
  return undefined;
}

interface Props {
  /** The draft exactly as the editor would save it, account scope already collapsed. */
  panel: Panel;
  /** Every account the author may point the panel at; the scope resolves against it. */
  accountOptions: AccountOption[];
  variables: VariableValues;
  startTime: number;
  endTime: number;
  forceSample?: boolean;
}

/** Runs the panel being authored and shows the result. */
const PanelPreview: React.FC<Props> = ({ panel, accountOptions, variables, startTime, endTime, forceSample }) => {
  /** The draft as last submitted, held apart so typing does not queue a request per character. */
  const [settled, setSettled] = React.useState<Panel>(panel);
  /** Bumped by Run/Refresh, to re-run a query that has not otherwise changed. */
  const [runToken, setRunToken] = React.useState(0);

  const signature = fetchSignature(panel);
  const manual = usesManualRun(panel.datasource);

  React.useEffect(() => {
    if (manual) return undefined;
    // A datasource, visualisation or account change is one deliberate click, so it
    // applies at once; only the query text waits for the typing to stop.
    if (discreteSignature(panel) !== discreteSignature(settled)) {
      setSettled(panel);
      return undefined;
    }
    const timer = setTimeout(() => setSettled(panel), PREVIEW_DEBOUNCE_MS);
    return () => clearTimeout(timer);
    // `settled` is deliberately not a dependency — re-running on it would restart
    // the debounce against itself.
  }, [signature, manual]);

  const run = () => {
    setSettled(panel);
    setRunToken((n) => n + 1);
  };

  /** Presentation fields track the form live — they change nothing about the request. */
  const preview = React.useMemo<Panel>(
    () => ({
      ...settled,
      title: panel.title || 'Untitled panel',
      description: panel.description,
      content: panel.content,
      unit: panel.unit,
    }),
    [settled, panel.title, panel.description, panel.content, panel.unit]
  );

  const liveRunnable = !forceSample && isRunnable(panel);
  const show = liveRunnable && isRunnable(settled) && settled.datasource === panel.datasource;
  const stale = show && signature !== fetchSignature(settled);
  /** Real accounts the query would reach, whether or not it is running yet — used to name the sample too. */
  const scoped = resolvePanelAccounts(preview, accountOptions);
  const scopeNote = accountScopeNote(preview, scoped);

  const sample = React.useMemo(() => {
    if (show || preview.type === 'text') return undefined;
    const table = sampleTableData(panel, startTime, endTime);
    return table ? { labels: [], series: [], table } : sampleData(startTime, endTime, sampleSeriesLabels(panel, scoped[0]?.label));
  }, [show, preview.type, panel, scoped, startTime, endTime]);

  const notes: string[] = [];
  if (sample) {
    notes.push(
      forceSample ? 'Configure your account to see real data' : manual ? 'Run the command to see real data' : 'Add a query to see real data'
    );
    if (scopeNote) notes.push(scopeNote);
  } else if (preview.type !== 'text') {
    notes.push(previewRangeLabel(startTime, endTime));
    if (scopeNote) notes.push(scopeNote);
  }

  return (
    <Box data-testid='panel-preview'>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[1] }}>
        <Typography
          sx={{
            flex: 1,
            minWidth: 0,
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: 'var(--ds-gray-700)',
            fontFamily: 'var(--ds-font-display)',
            lineHeight: 1,
          }}
        >
          Preview
        </Typography>
        {sample && (
          <Chip size='2xs' tone='info' data-testid='panel-preview-sample-badge'>
            Sample data
          </Chip>
        )}
        {manual && !forceSample && (
          <Button tone='secondary' size='sm' onClick={run} disabled={!liveRunnable} id='panel-preview-run-btn' data-testid='panel-preview-run-btn'>
            Run preview
          </Button>
        )}
      </Box>
      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: ds.space[2], mb: ds.space[3], minHeight: '18px' }}>
        <Typography variant='caption' sx={{ color: ds.gray[500], flex: 1, minWidth: 0 }}>
          {notes.join(' · ')}
        </Typography>
        {stale && (
          <Typography variant='caption' sx={{ color: ds.amber[600], flexShrink: 0 }} data-testid='panel-preview-stale'>
            {manual ? 'Not run yet' : 'Updating…'}
          </Typography>
        )}
      </Box>

      <Box
        sx={{
          display: 'flex',
          minHeight: panelMinHeight(panel),
          opacity: sample ? 0.72 : 1,
          p: '4px',
          borderRadius: '12px',
          border: `1px solid ${ds.purple[300]}`,
          background: ds.purple[100],
          boxShadow: '0 4px 18px rgba(0, 0, 0, 0.15)',
        }}
      >
        <Box sx={{ flex: 1, minWidth: 0 }}>
          {/* `editing` hides the view-time controls, which have nowhere to go inside a
              modal; `forceLoad` skips the scroll gate, unreliable inside a portal. */}
          <DashboardPanel
            panel={preview}
            accounts={accountOptions}
            variables={variables}
            startTime={startTime}
            endTime={endTime}
            refreshToken={runToken}
            forceLoad
            editing
            sampleData={sample}
          />
        </Box>
      </Box>
    </Box>
  );
};

export default PanelPreview;
