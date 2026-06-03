import Datetime from '@common-new/format/Datetime';
import { PrometheusIcon } from '@assets';
import KubernetesPrometheus from '@components1/k8s/details/KubernetesPrometheus';

class PrometheusCard {
  constructor(data, _event) {
    this.id = 'PrometheusCard';
    this.icon = PrometheusIcon;
    this.text = data?.additional_info?.title || 'Prometheus Query Result';
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
    return this.renderContent;
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

          if (seriesList.length === 0) {
            return null;
          }

          return (
            <div key={index} style={{ marginBottom: 'var(--ds-space-6)' }}>
              Query: <h4>{query}</h4>
              {seriesList.map((series, i) => {
                const { value, metric } = series;
                const [metricValue, timestamp] = [value?.value, value?.timestamp];

                return (
                  <div key={i} style={{ marginBottom: 'var(--ds-space-4)' }}>
                    <div>
                      <strong>Metric:</strong> {metric.__name__}
                    </div>
                    <div>
                      <strong>Value:</strong> {metricValue}
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
