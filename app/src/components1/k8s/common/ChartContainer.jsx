import React from 'react';
import { LineChart } from '@components1/common';
import { colors } from 'src/utils/colors';

const ChartContainer = ({ jsonData }) => {
  const generateColors = (count) => {
    const colorPool = [colors.text.cpuUsage, colors.text.cpuRequested, colors.text.cpuLimit];
    return Array.from({ length: count }, (_, i) => colorPool[i % colorPool.length]);
  };

  const transformData = (entry) => {
    const { metadata, data } = entry;
    const query = metadata?.query;
    const seriesList = data?.series_list_result || [];

    if (seriesList.length === 0) {
      return null;
    }
    const timestamps = seriesList[0]?.timestamps || [];
    const labels = timestamps.map((t) => new Date(t * 1000).toLocaleTimeString());

    const colors = generateColors(seriesList.length);
    const datasets = seriesList.map((series, index) => ({
      label: series.metric.pod || series.metric.container_id || `Series ${index + 1}`,
      data: series.values,
      borderColor: colors[index],
      backgroundColor: colors[index],
      borderWidth: 2,
      pointRadius: 0,
    }));

    return {
      labels,
      datasets,
      chartTitle: query,
    };
  };

  return (
    <div>
      {jsonData?.map((entry, index) => {
        const chartData = transformData(entry);
        if (!chartData) {
          return null;
        }

        return (
          <div key={index} style={{ marginBottom: 'var(--ds-space-6)' }}>
            <LineChart
              labels={chartData.labels}
              dataset={chartData.datasets}
              chartTitle={chartData.chartTitle}
              colors={generateColors(chartData.datasets.length)}
            />
          </div>
        );
      })}
    </div>
  );
};

export default ChartContainer;
