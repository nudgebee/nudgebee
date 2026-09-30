/**
 * The per-account rows behind a stat or gauge panel's total, drawn on the card.
 *
 * The same rows used to live only in the hover over the number. That kept the
 * card quiet, but it also meant the one question a combined number raises —
 * which cluster is it? — needed a mouse over the right spot to answer, and
 * nothing on the card hinted that one account was carrying the figure.
 *
 * How many rows fit is decided from the panel's grid height rather than
 * measured. The dashboard grid sizes a row to its tallest panel, so a list that
 * grew with the account count would stretch every panel beside it; a budget
 * fixed by `grid_pos.h` keeps the card the size its author set, and is the same
 * answer on every refresh.
 *
 * Every account, or none. A list cut to the first few names in account order
 * says nothing the hover does not, and reads as if those were the ones that
 * matter — so a panel that cannot fit them all draws exactly as it did before:
 * the account count, and the breakdown on hover.
 *
 * A stat lists them under its number when they fit there, and otherwise beside
 * it, where the list gets the body's full height: a short stat — five grid rows
 * is a common size — has no room for a list under its number, and four rows
 * beside it.
 */
import React from 'react';
import { Box } from '@mui/material';
import { ds } from '@utils/colors';
import type { Panel, PanelThresholdStep } from '@api1/dashboards';
import { panelMinHeight } from './panelDefaults';
import type { StatRow } from './panelSeries';
import { thresholdTone } from './panelThresholds';

/** One row's height. Fixed, so the budget below is arithmetic rather than a guess. */
export const BREAKDOWN_ROW_PX = 18;

/**
 * What a panel spends before its body starts: the header band (title, and the
 * account picker a multi-account panel carries — 49px in Chromium) plus the
 * body's padding.
 */
const PANEL_CHROME_PX = 69;

/**
 * A stat is budgeted for a title that wraps to a second line (a 56px header):
 * stats are the panels laid out narrow, three or four to a row, where a title
 * plus the scope chips and the account picker do not fit on one line. A gauge
 * already runs past its grid height at the default size, so it is not.
 */
const TITLE_WRAP_PX = 7;

/**
 * A stat's figure. The account-count caption is not counted: it is drawn only
 * when the list is not.
 */
const STAT_FIGURE_PX = 42;

/**
 * The smallest a gauge's dial gets to make room for rows. Below this the dial's
 * value label crowds its arc and the dial stops reading as a dial.
 */
export const MIN_DIAL_PX = 112;

/**
 * Rows a gauge shows at most. The dial is the panel's figure; past three rows
 * the list, not the dial, would be what the panel is.
 */
const MAX_GAUGE_ROWS = 3;

/** Where a card lists its accounts: under its figure, or — a stat only — beside it. */
export type BreakdownPlacement = 'under' | 'beside';

/**
 * Where this panel lists `accounts` rows, or null when it cannot fit every one
 * of them — or when there is only one, which has nothing to break down.
 */
export function breakdownPlacement(panel: Panel, accounts: number): BreakdownPlacement | null {
  if (accounts < 2) return null;
  const body = panelMinHeight(panel) - PANEL_CHROME_PX;
  if (panel.type === 'gauge') return accounts <= MAX_GAUGE_ROWS && fitsRows(accounts, body - MIN_DIAL_PX) ? 'under' : null;
  const statBody = body - TITLE_WRAP_PX;
  if (fitsRows(accounts, statBody - STAT_FIGURE_PX)) return 'under';
  return fitsRows(accounts, statBody) ? 'beside' : null;
}

/**
 * Where a card that cannot list every account lists the ones over a threshold —
 * they are what the list is for — or null when even those do not fit. The
 * account count stays under a stat's figure, so they go beside it; a gauge's go
 * under its dial, within the gauge's usual cap.
 */
export function breachedPlacement(panel: Panel, accounts: number): BreakdownPlacement | null {
  if (accounts < 1) return null;
  const body = panelMinHeight(panel) - PANEL_CHROME_PX;
  if (panel.type === 'gauge') return accounts <= MAX_GAUGE_ROWS && fitsRows(accounts, body - MIN_DIAL_PX) ? 'under' : null;
  return fitsRows(accounts, body - TITLE_WRAP_PX) ? 'beside' : null;
}

function fitsRows(rows: number, room: number): boolean {
  return rows <= Math.floor(room / BREAKDOWN_ROW_PX);
}

interface RowsProps {
  rows: StatRow[];
  format: (value: number | undefined) => string;
  /** The threshold step each account crossed, keyed by account. Those rows take the step's colour. */
  flagged?: Map<string, PanelThresholdStep>;
}

/**
 * The rows themselves, name on the left and figure on the right. Shared by the
 * card and by the hovers, so the two cannot drift into different layouts.
 */
export const BreakdownRows: React.FC<RowsProps> = ({ rows, format, flagged }) => (
  <>
    {rows.map((row) => {
      const step = flagged?.get(row.account);
      const tone = step ? thresholdTone(step.color) : undefined;
      return (
        <Box
          key={row.account}
          data-testid='panel-breakdown-row'
          data-threshold={step?.color}
          sx={{
            display: 'flex',
            justifyContent: 'space-between',
            gap: ds.space[3],
            lineHeight: `${BREAKDOWN_ROW_PX}px`,
            minWidth: 0,
            // A tinted band, not a bigger font: the row stays the height the
            // panel budgeted for it. The band reaches past the text on both
            // sides so the name and figure stay in line with the rows around it.
            ...(tone
              ? {
                  background: tone.tint,
                  color: tone.text,
                  fontWeight: ds.weight.semibold,
                  px: ds.space[1],
                  mx: ds.space.mul(1, -1),
                  borderRadius: ds.radius.sm,
                }
              : {}),
          }}
        >
          <Box component='span' sx={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            {row.account}
          </Box>
          <Box component='span' sx={{ flexShrink: 0, fontVariantNumeric: 'tabular-nums', opacity: row.value === undefined ? 0.7 : 1 }}>
            {row.failed ? 'no answer' : row.value === undefined ? 'no data' : format(row.value)}
          </Box>
        </Box>
      );
    })}
  </>
);

interface Props extends RowsProps {
  placement: BreakdownPlacement;
  testId?: string;
}

const PanelAccountBreakdown: React.FC<Props> = ({ rows, placement, format, flagged, testId }) => (
  <Box
    data-testid={testId}
    data-placement={placement}
    // Beside the figure the list takes the rest of the width; under it, the
    // list keeps its own height so the panel above it does not squeeze it.
    sx={{ fontSize: ds.text.small, color: ds.gray[600], minWidth: 0, ...(placement === 'beside' ? { flex: 1 } : { flexShrink: 0 }) }}
  >
    <BreakdownRows rows={rows} format={format} flagged={flagged} />
  </Box>
);

export default PanelAccountBreakdown;
