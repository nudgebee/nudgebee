/**
 * What a time-series panel's thresholds look like ON the chart: a dashed line
 * at each step's value in the step's colour, and a band over the stretch of a
 * line that held past a step for the whole hold. Drawn as a chart.js plugin so
 * it rides along in the PNG export, which copies the canvas.
 *
 * The axis is a category axis of sample indexes (see LineCharts), so a band is
 * positioned by index, not by time.
 */
import { resolveColor } from '@utils/colors';
import { withAlpha } from '@shared/charts/chartPlugins';
import type { PanelThresholdStep } from '@api1/dashboards';
import { forOf, holdLabel, OP_SYMBOL, opOf, thresholdTone, type HoldSpan } from './panelThresholds';

/** The band's opacity over the chart body. Light: the lines underneath are the point. */
const BAND_ALPHA = 0.14;

/** The label's font size in px — also how much room it needs above its line. */
const LABEL_PX = 10;

/**
 * The bands to draw: each visible line's held run, merged per step so several
 * lines holding the same step at once shade that stretch once. Stacking one
 * translucent band per line turned a busy fleet's chart solid.
 */
export function mergedBands(chart: any, spans: HoldSpan[]): { step: PanelThresholdStep; from: number; to: number }[] {
  const byStep = new Map<string, { step: PanelThresholdStep; runs: [number, number][] }>();
  for (const span of spans) {
    if (!chart.isDatasetVisible(span.series)) continue;
    const key = `${span.step.value}:${span.step.color}:${opOf(span.step)}`;
    if (!byStep.has(key)) byStep.set(key, { step: span.step, runs: [] });
    byStep.get(key)!.runs.push([span.from, span.to]);
  }
  const out: { step: PanelThresholdStep; from: number; to: number }[] = [];
  for (const { step, runs } of byStep.values()) {
    runs.sort((a, b) => a[0] - b[0]);
    let [from, to] = runs[0];
    for (const [f, t] of runs.slice(1)) {
      if (f <= to + 1) to = Math.max(to, t);
      else {
        out.push({ step, from, to });
        [from, to] = [f, t];
      }
    }
    out.push({ step, from, to });
  }
  return out;
}

/** One drawn threshold line: the step, de-duplicated by value and colour so two steps on one line draw once. */
function lines(steps: PanelThresholdStep[]): PanelThresholdStep[] {
  const seen = new Set<string>();
  return steps.filter((step) => {
    const key = `${step.value}:${step.color}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

/** What the plugin draws, read fresh on every draw. */
export interface ThresholdDrawing {
  steps: PanelThresholdStep[];
  spans: HoldSpan[];
}

/**
 * The plugin. react-chartjs-2 hands plugins to chart.js once, when the chart is
 * created, and never again — so the plugin must not close over the steps and
 * spans of its first render. It asks `current` for them on every draw instead,
 * and the chart's own update (new datasets on every panel render) redraws it.
 */
export function thresholdChartPlugin(current: () => ThresholdDrawing) {
  return {
    id: 'panelThresholds',
    /**
     * A threshold at 70 on a chart whose data sits at 0–10 would otherwise be
     * drawn off-canvas and never seen. The y scale's computed limits are widened
     * to reach every line — on the scale itself, for this pass only, never on the
     * chart's options, which outlive the pass and would keep a removed step's
     * limit around.
     */
    afterDataLimits(chart: any, args: any) {
      const scale = args?.scale;
      if (!scale || scale.id !== 'y') return;
      const drawn = lines(current().steps);
      if (drawn.length === 0) return;
      const values = drawn.map((s) => s.value);
      const max = Math.max(...values);
      const min = Math.min(...values);
      if (scale.max < max) scale.max = max;
      if (scale.min > min) scale.min = min;
    },
    afterDatasetsDraw(chart: any) {
      const { ctx, chartArea, scales } = chart;
      const yScale = scales?.y;
      const xScale = scales?.x;
      if (!ctx || !chartArea || !yScale || !xScale) return;
      const { steps, spans } = current();
      const drawn = lines(steps);
      if (drawn.length === 0 && spans.length === 0) return;
      ctx.save();
      // `clip: false` on the chart lets datasets spill; the band must not.
      ctx.beginPath();
      ctx.rect(chartArea.left, chartArea.top, chartArea.right - chartArea.left, chartArea.bottom - chartArea.top);
      ctx.clip();

      for (const band of mergedBands(chart, spans)) {
        const colour = resolveColor(thresholdTone(band.step.color).border);
        const left = xScale.getPixelForValue(band.from);
        const right = xScale.getPixelForValue(band.to);
        ctx.fillStyle = withAlpha(colour, BAND_ALPHA);
        ctx.fillRect(Math.min(left, right), chartArea.top, Math.max(2, Math.abs(right - left)), chartArea.bottom - chartArea.top);
      }

      for (const step of drawn) {
        const y = yScale.getPixelForValue(step.value);
        if (!Number.isFinite(y) || y < chartArea.top || y > chartArea.bottom) continue;
        const colour = resolveColor(thresholdTone(step.color).border);
        ctx.strokeStyle = colour;
        ctx.setLineDash([4, 4]);
        ctx.lineWidth = 1;
        ctx.beginPath();
        ctx.moveTo(chartArea.left, y);
        ctx.lineTo(chartArea.right, y);
        ctx.stroke();
        ctx.setLineDash([]);
        ctx.font = `${LABEL_PX}px sans-serif`;
        ctx.fillStyle = colour;
        ctx.textAlign = 'right';
        // Above the line, unless that is outside the plot — a step that sets the
        // top of the axis would lose its label to the clip — then below it.
        const below = y - LABEL_PX - 2 < chartArea.top;
        ctx.textBaseline = below ? 'top' : 'bottom';
        const hold = forOf(step);
        ctx.fillText(
          `${OP_SYMBOL[opOf(step)]} ${step.value}${hold !== undefined ? ` for ${holdLabel(hold)}` : ''}`,
          chartArea.right - 2,
          below ? y + 2 : y - 2
        );
      }
      ctx.restore();
    },
  };
}
