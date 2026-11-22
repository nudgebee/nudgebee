import { PrometheusIcon } from '@assets';
import apiKubernetes1 from '@api1/kubernetes1';
import KubernetesPrometheus from '@components/k8s/details/KubernetesPrometheus';
import { titleCase } from '@lib/formatter';
import { formatDateForPlusMinusDuration } from 'src/utils/common';
import { v4 as uuidv4 } from 'uuid';

class ExecutePrometheus {
  // requireData: the evidence holds queries, not their results, so a card built from it can open
  // onto nothing. With this set the card is shown only when a query returns data for the time
  // around the alert, and it charts only the queries that do.
  constructor(data, event, { requireData = false } = {}) {
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
    this.requireData = requireData;
  }

  canRenderContent = async () => {
    this.renderContent = false;
    const queries = this.enricherData.data;
    if (Array.isArray(queries) && queries.length) {
      this.queries = queries.map((q) => ({
        ...q,
        key: uuidv4(),
      }));
      if (this.requireData) {
        // The page asks each card twice while it builds its list; the queries are run once.
        this.withData ??= this.queriesWithData();
        this.queries = await this.withData;
      }
      this.renderContent = this.queries.length > 0;
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderPrometheusQueryResult()];
  };

  // The time the card charts: 30 minutes either side of the alert.
  chartRange = () => {
    // The event's start is UTC and may be written with or without a zone. Adding "Z" to one that
    // has a zone gives an invalid date, and the chart then falls back to the last hour.
    const startsAt = String(this.event.starts_at ?? '');
    const startDate = /([Zz]|[+-]\d{2}:?\d{2})$/.test(startsAt) ? startsAt : `${startsAt}Z`;
    const plusMinus30MinDuration = formatDateForPlusMinusDuration(new Date(startDate).getTime(), 30);
    return {
      startTime: plusMinus30MinDuration.dateMinusMinutes,
      endTime: plusMinus30MinDuration.datePlusMinutes,
    };
  };

  // The queries that return at least one series for that time. A request that fails counts as
  // no data: the chart would fail the same way when the card is opened.
  queriesWithData = async () => {
    const { startTime, endTime } = this.chartRange();
    try {
      const response = await apiKubernetes1.consumePrometheusQueries({
        account_id: this.event.cloud_account_id,
        queries: Object.fromEntries(this.queries.map((q) => [q.key, q.query])),
        start_time: startTime,
        end_time: endTime,
        instant: false,
      });
      const results = response?.data?.data?.metrics_list?.results;
      const keysWithData = new Set((Array.isArray(results) ? results : []).filter((r) => r?.payload?.length > 0).map((r) => r.query_key));
      return this.queries.filter((q) => keysWithData.has(q.key));
    } catch {
      return [];
    }
  };

  renderPrometheusQueryResult = () => {
    return (
      <KubernetesPrometheus
        inferUnits
        showDrilldown={false}
        preparedEvidences={[]}
        showExtraOptions={false}
        showQueryBox={false}
        chartView={true}
        showDateTime={false}
        accountId={this.event.cloud_account_id}
        queriesToExecute={this.queries}
        dateTime={this.chartRange()}
      />
    );
  };
}

export default ExecutePrometheus;
