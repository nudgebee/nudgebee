/**
 * PanelGauge — the gauge panel's body: the account-overview dial
 * (react-gauge-component, K8sMemoryCpuIndicator's arc geometry and palette)
 * reading one 0–100 value.
 *
 * The dial IS a percentage — the query is expected to return 0–100, and an
 * out-of-range value pins to the nearer end rather than leaving the arc. A
 * value with no natural maximum has no place on a gauge; that panel is a stat.
 */
import React from 'react';
import dynamic from 'next/dynamic';
import { Box, Typography } from '@mui/material';
import { ds } from '@utils/colors';
import { BREAKDOWN_ROW_PX } from './PanelAccountBreakdown';

// SSR off for the same reason the overview turns it off: the gauge measures
// its container to lay out the SVG, which needs a real DOM.
const GaugeComponent = dynamic(() => import('react-gauge-component'), { ssr: false });

interface Props {
  /** Newest reported point of the panel's first series; undefined = nothing came back. */
  value: number | undefined;
  /** Names the series the value came from, when the query kept labels. */
  caption?: string;
  /**
   * Breakdown rows the panel draws under the dial. The dial gives up their
   * height, so the rows fit in the same panel rather than stretching it.
   */
  rowsBelow?: number;
}

/** The dial's width at a default-height panel with nothing under it. */
const DIAL_MAX_PX = 340;

/**
 * Height over width of the drawn dial — a half circle plus its value label and
 * the library's own margins — as measured in Chromium. Converts the height the
 * rows need into the width the dial gives up.
 */
const DIAL_ASPECT = 0.555;

const PanelGauge: React.FC<Props> = ({ value, caption, rowsBelow = 0 }) => {
  const clamped = Math.min(100, Math.max(0, value ?? 0));
  const maxWidth = DIAL_MAX_PX - Math.round((rowsBelow * BREAKDOWN_ROW_PX) / DIAL_ASPECT);
  // The value label is drawn in SVG pixels, so it does not shrink with the dial on
  // its own — left at full size it all but covers a smaller arc.
  const labelScale = maxWidth / DIAL_MAX_PX;
  return (
    // Centered in the panel body rather than top-left: the dial is the panel's
    // one figure, and a fixed corner-sized dial leaves the rest reading as empty.
    <Box sx={{ height: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center' }}>
      {value === undefined || Number.isNaN(value) ? (
        <Typography sx={{ fontSize: 28, fontWeight: 650, letterSpacing: '-0.02em', color: ds.gray[700] }}>—</Typography>
      ) : (
        <Box
          sx={{
            width: '100%',
            // Sized against the default panel body (grid h=8 → ~190px): a half
            // dial's height is ~width/2, so 340 fills it without spilling — less
            // whatever the breakdown rows under it take.
            maxWidth,
            // The overview's two visual overrides, without which the dial reads as
            // three fixed colour zones instead of "covered so far vs remaining":
            // the outer tick arc goes, and the uncovered remainder paints neutral.
            '.doughnut .outerSubArc': { display: 'none !important' },
            '.doughnut .subArc:last-child path': { fill: `${ds.gray[200]} !important` },
          }}
        >
          <GaugeComponent
            labels={{
              tickLabels: { hideMinMax: true },
              valueLabel: {
                style: {
                  // SVG-space, sized to the 340px dial — the text tokens are page
                  // typography and top out well below what the dial needs.
                  fontSize: `${Math.round(48 * labelScale)}px`,
                  fontWeight: 'var(--ds-font-weight-medium)',
                  fill: 'var(--ds-brand-500)',
                  textShadow: 'none',
                  transform: `translateY(${Math.round(-20 * labelScale)}px)`,
                },
              },
            }}
            value={Math.round(clamped)}
            arc={{
              colorArray: [ds.red[500], ds.green[400], ds.red[500]],
              subArcs: [
                { length: 0.3, showTick: false },
                { length: 0.7, showTick: false },
                { length: 0.1, showTick: false },
              ],
              padding: 0.03,
              width: 0.3,
            }}
            pointer={{ elastic: true, animationDelay: 0 }}
          />
        </Box>
      )}
      {caption && (
        <Typography variant='caption' sx={{ color: ds.gray[500] }}>
          {caption}
        </Typography>
      )}
    </Box>
  );
};

export default PanelGauge;
