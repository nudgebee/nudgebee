import React from 'react';
import { Chart as ChartJS, CategoryScale, LinearScale, BarElement, Title, Tooltip, Legend } from 'chart.js';
import { Bar } from 'react-chartjs-2';
import { titleCase } from '@lib/formatter';
import LogsIcon from '@assets/investigation/logs-blue.svg';

ChartJS.register(CategoryScale, LinearScale, BarElement, Title, Tooltip, Legend);

class SpendBreakdownChartCard {
  constructor(data, index) {
    this.id = `SpendBreakdownChart_${index}`;
    this.text = titleCase(data?.data?.table_name?.replaceAll(':', '')?.replaceAll('*', '') || '') || 'Spend Breakdown';
    this.icon = LogsIcon;
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.enricherData = data;
    this.labels = [];
    this.values = [];
    this.colors = [];
  }

  canRenderContent = async () => {
    if (this.enricherData?.data?.rows?.length > 0) {
      this.renderContent = true;
      const rows = this.enricherData.data.rows;
      const headers = this.enricherData.data.headers || [];
      const changeIdx = headers.indexOf('Change');

      this.labels = rows.map((row) => row[0]);
      this.values = rows.map((row) => {
        const raw = changeIdx >= 0 ? row[changeIdx] : row[1];
        return parseFloat(String(raw).replace(/[$,+]/g, '') || 0);
      });
      this.colors = this.values.map((v) => (v >= 0 ? '#EF4444' : '#22C55E'));

      if (this.enricherData?.insight?.length > 0) {
        this.insightData = this.enricherData.insight;
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderChart()];
  };

  renderChart = () => {
    const rowCount = this.labels.length;
    const chartHeight = Math.max(200, rowCount * 32);

    const options = {
      indexAxis: 'y',
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
        tooltip: {
          callbacks: {
            label: (ctx) => {
              const val = ctx.raw;
              const prefix = val >= 0 ? '+' : '';
              return `${prefix}$${Math.abs(val).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
            },
          },
        },
      },
      scales: {
        x: {
          ticks: {
            callback: (val) => `$${val.toLocaleString()}`,
          },
          grid: { display: false },
        },
        y: {
          ticks: {
            callback: function (val) {
              const label = this.getLabelForValue(val);
              return label.length > 40 ? label.substring(0, 37) + '...' : label;
            },
            font: { size: 11 },
          },
          grid: { display: false },
        },
      },
    };

    const chartData = {
      labels: this.labels,
      datasets: [
        {
          data: this.values,
          backgroundColor: this.colors,
          borderRadius: 4,
          barPercentage: 0.6,
        },
      ],
    };

    return (
      <div style={{ height: chartHeight }}>
        <Bar options={options} data={chartData} />
      </div>
    );
  };
}

export default SpendBreakdownChartCard;
