import apiBilling from '@api1/billing';
import { BoxLayout2 } from '@components1/common';
import CustomTable from '@components1/common/tables/CustomTable2';
import { useEffect, useState, useCallback } from 'react';
import { snakeToTitleCase } from '@utils/common';
import apiUser from '@api1/user';

const MonthlyCostSummary = ({ query }: { query: any }) => {
  const [tableData, setTableData] = useState<{ text: any }[][]>([]);
  const [recordsPerPage, setRecordsPerPage] = useState<number>(apiUser.getUserPreferencesTablePageSize());
  const [totalCount, setTotalCount] = useState<number>(0);
  const [currentPage, setCurrentPage] = useState<number>(0);
  const [loading, setLoading] = useState(false);

  const onPageChange = (page: number, limit: number) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const changeOperationText = (text: string) => {
    switch (text) {
      case 'active_clusters':
        return 'Active clusters';
      case 'active_nodes':
        return 'Additional nodes';
      case 'auto_optimize_runs':
        return 'Auto optimize runs';
      case 'auto_runbook_runs':
        return 'Auto runbook runs';
      default:
        return text;
    }
  };

  // Memoize the data grouping function
  const groupDataByClusterServiceAndOperation = useCallback((data: any[]) => {
    if (!Array.isArray(data)) {
      return [];
    }

    const grouped = data.reduce((acc, item) => {
      const key = `${item.cloud_account?.account_name}_${item.service_name}_${item.name}_${item.id}`;
      if (!acc[key]) {
        acc[key] = {
          cluster: item.cloud_account?.account_name || '-',
          service: item.service_name,
          operation: item.name,
          units: 0,
          costPerUnit: 0,
          totalCost: 0,
        };
      }

      const units = item.name === 'active_clusters' ? Math.max(acc[key].units, item.units) : acc[key].units + item.units;

      acc[key] = {
        ...acc[key],
        units,
        costPerUnit: Math.max(acc[key].costPerUnit, item.cost_per_unit),
        totalCost: acc[key].totalCost + item.total_cost,
      };

      return acc;
    }, {});

    const groupedArray = Object.values(grouped).map((item: any) => {
      const isAutomation = item.operation.includes('auto');
      const costPerUnitLabel = isAutomation ? `100 per 1000 runs` : `${item.costPerUnit}`;
      const units = isAutomation ? `${item.units} run(s)` : `${item.units}`;

      return [
        { text: `${snakeToTitleCase(item.service)} - ${snakeToTitleCase(changeOperationText(item.operation))}` },
        { text: item.cluster },
        { text: units },
        { text: costPerUnitLabel },
        { text: item.totalCost },
      ];
    });

    // Sort the grouped data by total cost in descending order
    groupedArray.sort((a, b) => {
      return b[4].text - a[4].text;
    });

    return groupedArray;
  }, []);

  useEffect(() => {
    setLoading(true);
    setTableData([]);

    const data = {
      date: query.date,
      limit: recordsPerPage,
      offset: currentPage * recordsPerPage,
    };

    apiBilling
      .getBillingDetailsByMonth(data)
      .then((res) => {
        setTotalCount(res?.data?.billing_usage_cost_aggregate?.aggregate?.count);
        const groupedData = groupDataByClusterServiceAndOperation(res?.data?.billing_usage_cost || []);
        setTableData(groupedData);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [currentPage, recordsPerPage, query.date, groupDataByClusterServiceAndOperation]);

  return (
    <BoxLayout2
      id={'monthly-box'}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: 'monthly-billing-index',
            };
          },
        },
        sharing: { enabled: true, onClick: null },
      }}
    >
      <CustomTable
        tableData={tableData}
        headers={['Description', 'Cluster', 'Usage Quantity', 'Per unit cost in USD', 'Total Cost in USD']}
        rowsPerPage={recordsPerPage}
        onPageChange={onPageChange}
        totalRows={totalCount}
        id={'monthly-billing-index'}
        pageNumber={currentPage + 1}
        loading={loading}
      />
    </BoxLayout2>
  );
};

export default MonthlyCostSummary;
