import Datetime from '@shared/format/Datetime';
import { PrometheusIcon } from '@assets';
import KubernetesPrometheus from '@components/k8s/details/KubernetesPrometheus';
import { formatMetricValue, inferPromqlUnit } from '@lib/promqlUnit';
import { summarizeMetricGroups, summarizeSeries } from './cardSummaries';

class PrometheusCard {
  constructor(data, event) {
    this.id = 'PrometheusCard';
    this.sourceData = data;
    this.event = event;
    this.icon = PrometheusIcon;
    // Fall back to the PromQL query as the title, but collapse whitespace and
    // truncate so long/multi-line expressions don't overflow the card header.
    const queryTitle = typeof data?.metadata?.query === 'string' ? data.metadata.query.replace(/\s+/g, ' ').trim() : '';
    const compactQueryTitle = queryTitle.length > 100 ? `${queryTitle.slice(0, 100)}…` : queryTitle;
    this.text = data?.additional_info?.title || compactQueryTitle || 'Prometheus Query Result';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.resultType = 'series';

    // When metric_groups is present, split into separate evidences for per-metric charts
    if (data?.data?.metric_groups && Object.keys(data.data.metric_groups).length > 1) {
      this.prometheusTypeData = Object.entries(data.data.metric_groups).map(([name, group]) => ({
        metadata: { query: name },
        data: group,
        additional_info: { ...data.additional_info, title: name },
      }));
    } else {
      this.prometheusTypeData = [data];
    }
  }

  canRenderContent = async () => {
    this.renderContent = false;
    if (this.prometheusTypeData) {
      const allSeriesEmpty = this.prometheusTypeData.every((item) => !item?.data?.series_list_result?.length);
      const vectorEmpty = this.prometheusTypeData.every((item) => !item?.data?.vector_result?.length);
      if (!allSeriesEmpty || !vectorEmpty) {
        this.renderContent = true;
      }
      if (!vectorEmpty && allSeriesEmpty) {
        this.resultType = 'vector';
      }
    }
    if (this.renderContent) this.insightData = this.summarize();
    return this.renderContent;
  };

  // The stored summary when there is one. Otherwise: for a cloud resource's metrics, the one the
  // alarm watches and its peak; for a query result, how many series and the range they cover.
  summarize = () => {
    const data = this.sourceData;
    if (data?.insight?.length > 0) return data.insight;
    if (data?.data?.metric_groups) return summarizeMetricGroups(data.data.metric_groups, this.event?.labels?.aws_event_metric_name);
    const unit = inferPromqlUnit(data?.metadata?.query);
    return summarizeSeries(data?.data?.series_list_result, unit ? (value) => formatMetricValue(value, unit) : undefined);
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderPrometheusData()];
  };

  renderPrometheusData = () => {
    return this.resultType == 'series' ? (
      <KubernetesPrometheus
        inferUnits
        showQueryBox={false}
        preparedEvidences={this.prometheusTypeData}
        showDateTime={false}
        showExtraOptions={false}
        queriesToExecute={[]}
        dateTime={{
          startTime: 0,
          endTime: 0,
        }}
      />
    ) : (
      <div>
        {this.prometheusTypeData.map((entry, index) => {
          const { metadata, data } = entry;
          const query = metadata?.query;
          const seriesList = data?.vector_result || [];
          const unit = inferPromqlUnit(query);

          if (seriesList.length === 0) {
            return null;
          }

          return (
            <div key={index} style={{ marginBottom: 'var(--ds-space-6)' }}>
              Query: <h4>{query}</h4>
              {seriesList.map((series, i) => {
                const { value, metric } = series;
                const [metricValue, timestamp] = [value?.value, value?.timestamp];
                const numericValue = parseFloat(metricValue);

                return (
                  <div key={i} style={{ marginBottom: 'var(--ds-space-4)' }}>
                    <div>
                      <strong>Metric:</strong> {metric.__name__}
                    </div>
                    <div>
                      {/* The exact value when its unit is unknown; a rounded one only once the unit says what it is. */}
                      <strong>Value:</strong> {unit && Number.isFinite(numericValue) ? formatMetricValue(numericValue, unit) : metricValue}
                    </div>

                    {timestamp ? (
                      <div>
                        <strong>Timestamp:</strong>
                        <Datetime value={new Date(timestamp * 1000)} />{' '}
                      </div>
                    ) : null}
                    <pre>{JSON.stringify(metric, null, 2)}</pre>
                  </div>
                );
              })}
            </div>
          );
        })}
      </div>
    );
  };
}

export default PrometheusCard;
