import { MetricsInvestigateIcon } from '@assets';
import QueryMetrics from '@components1/k8s/details/QueryMetrics';
import { formatDateForPlusMinusDuration, safeJSONParse } from 'src/utils/common';

let queryMetricsCardIdx = 0;

class QueryMetricsCard {
  constructor(query, evidenceData, event) {
    this.id = `QueryMetricsCard_${queryMetricsCardIdx++}`;
    this.text = evidenceData?.additional_info?.title || `Metrics Explorer`;
    this.icon = MetricsInvestigateIcon;
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.query = query;
    this.event = event;
    this.evidenceData = evidenceData;
    this.preparedEvidences = [];
  }

  canRenderContent = async () => {
    this.renderContent = false;
    if (this.query && Array.isArray(this.query) && this.query.length) {
      this.renderContent = true;
    } else if (this.evidenceData) {
      const parsedJson = safeJSONParse(this.evidenceData?.data);
      if (parsedJson) {
        const hasData = (parsedJson?.data || parsedJson).results?.some((result) =>
          result.payload?.some((item) => item.values?.some((v) => v !== null && v !== undefined))
        );
        if (hasData) {
          this.preparedEvidences = (parsedJson?.data || parsedJson).results;
          this.renderContent = true;
        }
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderMetricsQueryResult()];
  };

  renderMetricsQueryResult = () => {
    const startDate = `${this.event.starts_at}Z`;
    const plusMinus30MinDuration = formatDateForPlusMinusDuration(new Date(startDate).getTime(), 30);
    return (
      <QueryMetrics
        showDrilldown={false}
        preparedEvidences={this.preparedEvidences}
        showExtraOptions={false}
        showQueryBox={false}
        chartView={true}
        showDateTime={false}
        accountId={this.event.cloud_account_id}
        queriesToExecute={this.query}
        dateTime={{
          startTime: plusMinus30MinDuration.dateMinusMinutes,
          endTime: plusMinus30MinDuration.datePlusMinutes,
        }}
      />
    );
  };
}

export default QueryMetricsCard;
