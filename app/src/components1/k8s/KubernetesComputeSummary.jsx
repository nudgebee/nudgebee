import React, { useEffect, useState } from 'react';
import LineChart from '@common/charts/LineCharts';
import k8sApi from '@api1/kubernetes';
import BarChart from '@components1/common/charts/BarChart';
import { getLastSixMonths, getDateStringFromDateUnit } from '@lib/datetime';
import BoxLayout2 from '@components1/common/BoxLayout2';
import ChartSwitcher from '@common/ChartSwitcher';
import PropTypes from 'prop-types';

const KuberneteComputeSummary = ({ accountId = '', heading = '', id = 'KuberneteComputeCostSummary' }) => {
  const [chartUnit, setChartUnit] = useState('Month');
  const [chartType, setChartType] = useState('Bar');
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getLastSixMonths().getTime(),
    endDate: new Date().getTime(),
  });
  const [chartData, setChartData] = useState({
    labels: [],
    data: [],
  });
  const [computeSummaryLoading, setComputeSummaryLoading] = useState(false);

  const chartId = 'KuberneteComputeCostSummaryChart';

  useEffect(() => {
    if (!accountId) {
      return;
    }
    setComputeSummaryLoading(true);
    const chartData = [];
    const chartLabels = [];
    k8sApi
      .getK8sClusterCostTrendData(accountId, new Date(selectedDateRange.startDate), new Date(selectedDateRange.endDate), chartUnit)
      .then((res) => {
        res?.data?.spend_groupings?.forEach((item) => {
          chartData.push(item?.spend_amount);
          chartLabels.push(getDateStringFromDateUnit(item?.spend_date, chartUnit));
        });
        setChartData({
          labels: chartLabels,
          data: chartData,
        });
      })
      .finally(() => {
        setComputeSummaryLoading(false);
      });
  }, [accountId, chartUnit, selectedDateRange.startDate, selectedDateRange.endDate]);

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  return (
    <BoxLayout2
      id={id}
      heading={heading}
      dateTimeRange={{
        enabled: true,
        onChange: handleDateRangeChange,
        passedSelectedDateTime: {
          startTime: selectedDateRange.startDate,
          endTime: selectedDateRange.endDate,
        },
      }}
      showFiltersOnRightSide={{
        enabled: true,
        label: 'Frequency',
        options: ['Day', 'Week', 'Month'],
        showAll: false,
        value: chartUnit,
        onSelect: function (e) {
          setChartUnit(e?.target?.value);
        },
      }}
      minDate={new Date(new Date().getFullYear(), new Date().getMonth() - 6, 1)}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: async () => {
            return {
              canvasId: chartId,
            };
          },
        },
        sharing: { enabled: true },
      }}
      extraOptions={[
        <ChartSwitcher
          key={0}
          isBarChart={chartType == 'Bar'}
          leftButtonClick={() => setChartType('Line')}
          rightButtonClick={() => setChartType('Bar')}
        />,
      ]}
    >
      {chartType == 'Bar' ? (
        <BarChart id={chartId} data={chartData.data} labels={chartData.labels} loading={computeSummaryLoading} />
      ) : (
        <LineChart id={chartId} data={chartData.data} labels={chartData.labels} loading={computeSummaryLoading} />
      )}
    </BoxLayout2>
  );
};

KuberneteComputeSummary.propTypes = {
  accountId: PropTypes.string,
  heading: PropTypes.string,
  id: PropTypes.string,
};

export default KuberneteComputeSummary;
