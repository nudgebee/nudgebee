import React, { useEffect, useState } from 'react';
import Chart from '@components1/ds/Chart';
import k8sApi from '@api1/kubernetes';
import { getLastSixMonths, getDateStringFromDateUnit } from '@lib/datetime';
import ChartSwitcher from '@common/ChartSwitcher';
import PropTypes from 'prop-types';
import ListingLayout from '@components1/ds/ListingLayout';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import FilterDropdown from '@components1/ds/FilterDropdown';
import DownloadButton from '@common-new/DownloadButton';
import HeadingWithBorder from '@common-new/HeadingWithBorder';

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
    <ListingLayout id={id}>
      <ListingLayout.Toolbar
        actions={
          <>
            <FilterDropdown
              label='Frequency'
              options={['Day', 'Week', 'Month'].map((o) => ({ value: o, label: o }))}
              value={chartUnit}
              onSelect={(e) => setChartUnit(e?.target?.value)}
            />
            <ChartSwitcher
              isBarChart={chartType == 'Bar'}
              leftButtonClick={() => setChartType('Line')}
              rightButtonClick={() => setChartType('Bar')}
            />
            <CustomDateTimeRangePicker
              passedSelectedDateTime={{
                startTime: selectedDateRange.startDate,
                endTime: selectedDateRange.endDate,
              }}
              onChange={({ selection }) => handleDateRangeChange(selection)}
              minDate={new Date(new Date().getFullYear(), new Date().getMonth() - 6, 1)}
            />
            <DownloadButton id={`${id}-download`} onClick={async () => ({ canvasId: chartId })} />
          </>
        }
      >
        <HeadingWithBorder
          value={heading}
          borderColor='var(--ds-blue-500)'
          borderWidth='3px'
          sx={{ '& p': { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)' } }}
        />
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        {chartType == 'Bar' ? (
          <Chart.Bar id={chartId} data={chartData.data} labels={chartData.labels} loading={computeSummaryLoading} />
        ) : (
          <Chart.Line id={chartId} data={chartData.data} labels={chartData.labels} loading={computeSummaryLoading} />
        )}
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KuberneteComputeSummary.propTypes = {
  accountId: PropTypes.string,
  heading: PropTypes.string,
  id: PropTypes.string,
};

export default KuberneteComputeSummary;
