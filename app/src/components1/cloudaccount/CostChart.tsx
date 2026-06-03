import BarChart from '@components1/common/charts/BarChart';
import LineChart from '@common/charts/LineCharts';
import ChartSwitcher from '@components1/common/ChartSwitcher';
import { getDateStringFromDateUnit, getLastSixMonths } from '@lib/datetime';
import React, { useEffect, useState } from 'react';
import apiCloudAccount from '@api1/cloud-account';
import dayjs from 'dayjs';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@common-new/DownloadButton';
import { ds } from '@utils/colors';

const FREQUENCY_OPTIONS = [
  { label: 'Day', value: 'Day' },
  { label: 'Week', value: 'Week' },
  { label: 'Month', value: 'Month' },
];

// `id` and `canvasId` are now props so the component can be safely instantiated
// multiple times on the same page (e.g. inside expandable Monitoring rows
// where several charts can be open simultaneously). Defaults preserve the
// historical ids used by older call sites.
const TotalCostChart = ({
  accountId = '',
  resourceServiceName = '',
  resourceId = '',
  heading = 'Cost Trend',
  id = 'graph-section',
  canvasId = 'cloudUtilizationCostChartId',
}) => {
  const [chartUnit, setChartUnit] = useState('Month');
  const [selectedDateRange, _setSelectedDateRange] = useState({
    startDate: getLastSixMonths().getTime(),
    endDate: new Date().getTime(),
  });

  const cloudUtilizationCostChartId = canvasId;
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

  // Backend retains 6 months of cost data — clamp the picker's earliest pickable
  // date to the first day of the month 6 months ago. Same constraint the legacy
  // file passed via BoxLayout2's `minDate` prop.
  const minDate = dayjs(new Date(new Date().getFullYear(), new Date().getMonth() - 6, 1));

  return (
    <ListingLayout id={id} sx={{ mb: ds.space[4] }}>
      <ListingLayout.Toolbar
        title={heading}
        actions={
          <>
            <ChartSwitcher
              isBarChart={displayBarChart}
              leftButtonClick={() => setDisplayBarChart(false)}
              rightButtonClick={() => setDisplayBarChart(true)}
            />
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{
                startTime: selectedDateRange.startDate,
                endTime: selectedDateRange.endDate,
                shortcutClickTime: 0,
              }}
              onChange={(result: any) => {
                const val = result?.selection ?? result;
                if (val) handleDateRangeChange(val);
              }}
              minDate={minDate}
            />
            <DownloadButton id={`${cloudUtilizationCostChartId}-download`} onClick={() => ({ canvasId: cloudUtilizationCostChartId })} />
          </>
        }
      >
        <FilterDropdown
          id={`${cloudUtilizationCostChartId}-frequency`}
          label='Frequency'
          options={FREQUENCY_OPTIONS}
          value={FREQUENCY_OPTIONS.find((o) => o.value === chartUnit) ?? null}
          onSelect={(_e: any, item: any) => setChartUnit(item?.value || 'Month')}
        />
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
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
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default TotalCostChart;
