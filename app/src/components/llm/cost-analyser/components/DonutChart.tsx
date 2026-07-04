/**
 * DonutChart — pastel doughnut (react-chartjs-2) with the shared white tabular
 * tooltip. Used for the cost-by-model breakdown. The hole carries the total
 * (centerLabel/centerValue) so the donut answers "how much, in total?" on its
 * own; clicking a slice drills via `onSelectSlice`.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import { Doughnut } from 'react-chartjs-2';
import { makeExternalTooltip } from './chartKit';

interface DonutChartProps {
  values: number[];
  labels: string[];
  colors: string[];
  size?: number;
  /** Formats the raw slice value in the tooltip. */
  formatValue?: (raw: number, label: string) => string;
  /** Small caption in the hole (e.g. "Total"). */
  centerLabel?: string;
  /** Big figure in the hole (e.g. "$729"). */
  centerValue?: string;
  /** Click a slice to drill; receives the slice label. */
  onSelectSlice?: (label: string) => void;
}

export function DonutChart({ values, labels, colors, size = 132, formatValue, centerLabel, centerValue, onSelectSlice }: DonutChartProps) {
  // Center text plugin — draws centerLabel/centerValue stacked in the cutout.
  const centerText = React.useMemo(
    () => ({
      id: 'donutCenterText',
      afterDraw(chart: any) {
        if (!centerValue && !centerLabel) return;
        const { ctx, chartArea } = chart;
        const cx = (chartArea.left + chartArea.right) / 2;
        const cy = (chartArea.top + chartArea.bottom) / 2;
        ctx.save();
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        if (centerLabel) {
          ctx.font = '10px sans-serif';
          ctx.fillStyle = 'rgba(0,0,0,0.45)';
          ctx.fillText(centerLabel, cx, cy - 9);
        }
        if (centerValue) {
          ctx.font = '600 15px sans-serif';
          ctx.fillStyle = 'rgba(0,0,0,0.78)';
          ctx.fillText(centerValue, cx, cy + 6);
        }
        ctx.restore();
      },
    }),
    [centerLabel, centerValue]
  );

  const options = {
    responsive: true,
    maintainAspectRatio: false,
    cutout: '66%',
    animation: { animateRotate: true, duration: 500 } as const,
    onClick: (_e: unknown, els: { index: number }[]) => {
      if (onSelectSlice && els?.length) onSelectSlice(labels[els[0].index]);
    },
    onHover: (e: any, els: unknown[]) => {
      const t = e?.native?.target;
      if (t && t.style) t.style.cursor = onSelectSlice && els && els.length ? 'pointer' : 'default';
    },
    plugins: {
      legend: { display: false },
      tooltip: { enabled: false, external: makeExternalTooltip((raw, label) => (formatValue ? formatValue(Number(raw), label) : String(raw))) },
    },
  };
  return (
    <Box sx={{ width: size, height: size }}>
      <Doughnut
        data={{ labels, datasets: [{ data: values, backgroundColor: colors, borderColor: '#ffffff', borderWidth: 2, hoverOffset: 6 }] }}
        options={options as any}
        plugins={[centerText]}
      />
    </Box>
  );
}

export default DonutChart;
