import { PrometheusIcon } from '@assets';
import KubernetesPrometheus from '@components1/k8s/details/KubernetesPrometheus';
import { titleCase } from '@lib/formatter';
import { formatDateForPlusMinusDuration } from 'src/utils/common';
import { v4 as uuidv4 } from 'uuid';

class ExecutePrometheus {
  constructor(data, event) {
    this.id = `ExecutePrometheus`;
    this.text = titleCase(data?.additional_info?.title || `Performance Metrics`);
    this.icon = PrometheusIcon;
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.enricherData = data;
    this.event = event;
    this.queries = [];
    this.disabled = data?.additional_info?.status == 'skipped';
  }

  canRenderContent = async () => {
    this.renderContent = false;
    const queries = this.enricherData.data;
    if (Array.isArray(queries) && queries.length) {
      this.queries = queries.map((q) => ({
        ...q,
        key: uuidv4(),
      }));
      this.renderContent = true;
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderPrometheusQueryResult()];
  };

  renderPrometheusQueryResult = () => {
    const startDate = `${this.event.starts_at}Z`;
    const plusMinus30MinDuration = formatDateForPlusMinusDuration(new Date(startDate).getTime(), 30);
    return (
      <KubernetesPrometheus
        showDrilldown={false}
        preparedEvidences={[]}
        showExtraOptions={false}
        showQueryBox={false}
        chartView={true}
        showDateTime={false}
        accountId={this.event.cloud_account_id}
        queriesToExecute={this.queries}
        dateTime={{
          startTime: plusMinus30MinDuration.dateMinusMinutes,
          endTime: plusMinus30MinDuration.datePlusMinutes,
        }}
      />
    );
  };
}

export default ExecutePrometheus;
