import React from 'react';
import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';
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

interface Props {
  /** The draft exactly as the editor would save it, account scope already collapsed. */
  panel: Panel;
  /** Every account the author may point the panel at; the scope resolves against it. */
  accountOptions: AccountOption[];
  variables: VariableValues;
  startTime: number;
  endTime: number;
}

/** Runs the panel being authored and shows the result. */
const PanelPreview: React.FC<Props> = ({ panel, accountOptions, variables, startTime, endTime }) => {
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

  const liveRunnable = isRunnable(panel);
  // Across a datasource change the last result answers a different question, and a
  // PromQL chart under a Redis form is worse than no preview.
  const show = liveRunnable && isRunnable(settled) && settled.datasource === panel.datasource;
  const stale = show && signature !== fetchSignature(settled);

  /** Stand-in numbers until there is something real. A text panel writes its own content. */
  const sample = React.useMemo(
    () => (show || preview.type === 'text' ? undefined : sampleData(startTime, endTime)),
    [show, preview.type, startTime, endTime]
  );

  const notes: string[] = [];
  if (sample) {
    // First and unqualified — everything else here looks like a working panel.
    notes.push(manual ? 'Sample data — run the command to see real data' : 'Sample data — add a query to see real data');
  } else if (preview.type !== 'text') {
    notes.push(previewRangeLabel(startTime, endTime));
    const scoped = resolvePanelAccounts(preview, accountOptions);
    if (preview.datasource === 'nudgebee') {
      // The query engine takes every account in one call.
      if (scoped.length > 0) notes.push(scoped.length === 1 ? scoped[0].label : `${scoped.length} accounts`);
    } else if (scoped.length === 1) {
      notes.push(scoped[0].label);
    } else if (scoped.length > 1) {
      // The panel queries the first account and offers the rest behind its filter.
      notes.push(`${scoped[0].label} — first of ${scoped.length}`);
    }
  }

  return (
    <Box data-testid='panel-preview'>
      {/* Same type treatment a Form.Section title gets. */}
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[1] }}>
        <Typography
          sx={{
            flex: 1,
            minWidth: 0,
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: 'var(--ds-gray-700)',
            fontFamily: 'var(--ds-font-display)',
            lineHeight: 1.3,
          }}
        >
          Preview
        </Typography>
        {/* Only the datasources that hold back get a button; everywhere else the
            preview is never out of date. */}
        {manual && (
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

      {/* Tinted while it is a sample, so it does not read as your data. */}
      <Box sx={{ minHeight: panelMinHeight(panel), opacity: sample ? 0.72 : 1 }}>
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
  );
};

export default PanelPreview;
