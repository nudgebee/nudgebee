import BarChart from '@components1/common/charts/BarChart';
import LineChart from '@common/charts/LineCharts';
import BoxLayout2 from '@components1/common/BoxLayout2';
import ChartSwitcher from '@components1/common/ChartSwitcher';
import { getDateStringFromDateUnit, getLastSixMonths } from '@lib/datetime';
import React, { useEffect, useState } from 'react';
import apiCloudAccount from '@api1/cloud-account';
import { Box } from '@mui/material';

const TotalCostChart = ({ accountId = '', resourceServiceName = '', resourceId = '', heading = 'Cost Trend' }) => {
  const [chartUnit, setChartUnit] = useState('Month');
  const [selectedDateRange, _setSelectedDateRange] = useState({
    startDate: getLastSixMonths().getTime(),
    endDate: new Date().getTime(),
  });

  const cloudUtilizationCostChartId = 'cloudUtilizationCostChartId';
  const [displayBarChart, setDisplayBarChart] = useState(true);
  const [costLinechartData, setCostLinechartData] = useState({ data: [], labels: [] });
  const [computeSummaryLoading, setComputeSummaryLoading] = useState(false);

  useEffect(() => {
    if (!accountId) {
      return;
    }
    setComputeSummaryLoading(true);
    const chartData: any = [];
    const chartLabels: any = [];
    apiCloudAccount
      .listCloudAccountTrend(
        { accountId: accountId, resourceServiceName: resourceServiceName, resourceId: resourceId },
        new Date(selectedDateRange.startDate),
        new Date(selectedDateRange.endDate),
        chartUnit
      )
      .then((res: any) => {
        res?.data?.spend_groupings?.forEach((item: any) => {
          chartData.push(item?.spend_amount);
          chartLabels.push(getDateStringFromDateUnit(item?.spend_date, chartUnit));
        });
        setCostLinechartData({
          labels: chartLabels,
          data: chartData,
        });
      })
      .finally(() => {
        setComputeSummaryLoading(false);
      });
  }, [accountId, chartUnit, selectedDateRange.startDate, selectedDateRange.endDate, resourceServiceName, resourceId]);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    _setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };
  return (
    <BoxLayout2
      id='graph-section'
      heading={heading}
      dateTimeRange={{
        enabled: true,
        onChange: handleDateRangeChange,
        passedSelectedDateTime: {
          startTime: selectedDateRange.startDate,
          endTime: selectedDateRange.endDate,
          shortcutClickTime: 0,
        },
      }}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              canvasId: cloudUtilizationCostChartId,
            };
          },
        },
        sharing: { enabled: false, onClick: null },
      }}
      minDate={new Date(new Date().getFullYear(), new Date().getMonth() - 6, 1)}
      filterOptions={[
        {
          type: 'dropdown',
          label: 'Frequency',
          options: ['Day', 'Week', 'Month'],
          showAll: false,
          value: chartUnit,
          onSelect: function (e: any, _rule: string) {
            setChartUnit(e?.target?.value);
          },
        },
        {
          type: 'custom',
          component: (
            <ChartSwitcher
              isBarChart={displayBarChart}
              leftButtonClick={() => setDisplayBarChart(false)}
              rightButtonClick={() => setDisplayBarChart(true)}
            />
          ),
        },
      ]}
    >
      <Box mt={2} />
      {displayBarChart ? (
        <BarChart
          id={cloudUtilizationCostChartId}
          chartLabel='Cost'
          data={costLinechartData.data}
          labels={costLinechartData.labels}
          loading={computeSummaryLoading}
        />
      ) : (
        <LineChart
          id={cloudUtilizationCostChartId}
          chartLabel='Cost'
          data={costLinechartData.data}
          labels={costLinechartData.labels}
          loading={computeSummaryLoading}
        />
      )}
    </BoxLayout2>
  );
};

export default TotalCostChart;
