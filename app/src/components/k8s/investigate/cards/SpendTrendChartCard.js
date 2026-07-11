import Chart from '@ui/Chart';
import { titleCase } from '@lib/formatter';
import LogsIcon from '@assets/investigation/logs-blue.svg';
import { ds, resolveColor } from '@utils/colors';

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
    return <Chart.Bar labels={this.labels} data={this.amounts} chartLabel='Daily Spend' colors={resolveColor(ds.blue[500])} />;
  };
}

export default SpendTrendChartCard;
