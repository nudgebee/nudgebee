import { LineChart } from '@components1/common';
import { useEffect, useState } from 'react';
import { plusMinus5TimeRangePrometheusOfDate } from 'src/utils/common';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import apiKubernetes1 from '@api1/kubernetes1';

export const getDataFromProemtheus = async (event) => {
  const startsAt = event.starts_at.endsWith('Z') ? event.starts_at : event.starts_at + 'Z';
  const timeRange = plusMinus5TimeRangePrometheusOfDate(startsAt);

  try {
    const result = await apiKubernetes1.utilisationApi({
      accountId: event.cloud_account_id,
      metrics: ['cpu_usage_pod', 'cpu_request_pod', 'cpu_limit_pod'],
      startDate: timeRange?.startTime?.getTime(),
      endDate: timeRange?.endTime?.getTime(),
      namespaceName: event.subject_namespace,
      workloadName: event.subject_name,
    });

    if (result.length) {
      const cpuUsageData = result.find((data) => data.query_key === 'cpu_usage_pod')?.payload || [];
      const cpuRequestData = result.find((data) => data.query_key === 'cpu_request_pod')?.payload || [];
      const cpuLimitData = result.find((data) => data.query_key === 'cpu_limit_pod')?.payload || [];
      let seriesData = [];
      let cpuUsageLabels = [];
      if (cpuUsageData.length > 0) {
        cpuUsageLabels = cpuUsageData[0].timestamps.map((timestamp) => new Date(timestamp * 1000).toLocaleTimeString());
        seriesData.push(cpuUsageData[0].values?.map((val) => parseFloat(val)));
      }
      if (cpuRequestData.length > 0) {
        seriesData.push(cpuRequestData[0].values?.map((val) => parseFloat(val)));
      }
      if (cpuLimitData.length > 0) {
        seriesData.push(cpuLimitData[0].values?.map((val) => parseFloat(val)));
      }
      return { labels: cpuUsageLabels, data: seriesData };
    }
  } catch (e) {
    console.error('Error fetching data from Prometheus:', e);
  }

  return null;
};

function PrometheusCPUUsageCardComponent(props) {
  const [cpuUsageData, setCpuUsageData] = useState(props.data || {});

  useEffect(() => {
    if (!props.event || props.data) {
      return;
    }

    const fetchData = async () => {
      const result = await getDataFromProemtheus(props.event);

      if (result) {
        setCpuUsageData(result);
      }
    };

    fetchData();
  }, [props.event.cloud_account_id, props.event.subject_namespace, props.event.subject_name, props.event.starts_at]);

  return (
    <LineChart
      labels={cpuUsageData.labels}
      dataset={[
        {
          borderColor: colors.text.cpuUsage,
          data: cpuUsageData?.data?.[0] ?? [],
          label: 'Usage',
        },
        {
          borderColor: colors.text.cpuRequested,
          data: cpuUsageData.data?.[1] ?? [],
          label: 'Request',
        },
        {
          borderColor: colors.text.cpuLimit,
          data: cpuUsageData.data?.[2] ?? [],
          label: 'Limit',
          borderDash: [8, 2],
        },
      ]}
    />
  );
}

PrometheusCPUUsageCardComponent.propTypes = {
  event: PropTypes.object,
  data: PropTypes.object,
};

export default PrometheusCPUUsageCardComponent;
