import BarChart from '@components1/common/charts/BarChart';
import { titleCase } from '@lib/formatter';
import LogsIcon from '@assets/investigation/logs-blue.svg';

class SpendTrendChartCard {
  constructor(data, index) {
    this.id = `SpendTrendChart_${index}`;
    this.text = titleCase(data?.data?.table_name?.replaceAll(':', '')?.replaceAll('*', '') || '') || 'Daily Spend Trend';
    this.icon = LogsIcon;
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.enricherData = data;
    this.labels = [];
    this.amounts = [];
  }

  canRenderContent = async () => {
    if (this.enricherData?.data?.rows?.length > 0) {
      this.renderContent = true;
      const rows = this.enricherData.data.rows;
      this.labels = rows.map((row) => row[0]);
      this.amounts = rows.map((row) => parseFloat(row[1]?.replace(/[$,]/g, '') || 0));

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
    return <BarChart labels={this.labels} data={this.amounts} chartLabel='Daily Spend' colors='#6366F1' />;
  };
}

export default SpendTrendChartCard;
