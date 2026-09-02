import React from 'react';
import { Typography } from '@mui/material';
import DSCard from '@ui/Card';
import Chart from '@ui/Chart';
import { ds } from '@utils/colors';
import { formatMetricName } from '@utils/common';
import { buildScaleOptions, getUnitLabel } from './metricUnits';
import type { LiveMetricChart } from './useLiveResourceMetrics';

// Renders the live per-metric charts shared by the account Summary tabs and the
// Instances drilldown. One card per metric; the Y-axis is unit-formatted via
// buildScaleOptions so bytes/count/percent each render correctly.
export function LiveMetricCharts({ dataByMetric, loading }: { dataByMetric: Record<string, LiveMetricChart>; loading: boolean }) {
  const metricNames = Object.keys(dataByMetric);

  if (metricNames.length > 0) {
    return (
      <>
        {metricNames.map((g) => {
          const chart = dataByMetric[g];
          const showUnit = chart.unit === 'Percent' || chart.unit === 'Count';
          const title = showUnit ? `${formatMetricName(g)} (${getUnitLabel(chart.unit)})` : formatMetricName(g);
          return (
            <DSCard size='md' elevation='flat' key={g} sx={{ mb: ds.space[4], padding: ds.space[5] }}>
              <Chart.Line
                chartTitle={title}
                dataset={chart.datasets}
                labels={chart.labels}
                data={[]}
                loading={loading}
                scaleOptions={buildScaleOptions(chart.unit)}
              />
            </DSCard>
          );
        })}
      </>
    );
  }

  if (loading) return <Chart.Line dataset={[]} labels={[]} data={[]} loading={true} />;
  return (
    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], textAlign: 'center', py: ds.space[5] }}>
      No metrics data available for the selected time range.
    </Typography>
  );
}
