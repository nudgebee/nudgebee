import React, { useCallback, useEffect, useRef, useState } from 'react';
import { nodeExceptionHeader } from '@lib/kubernetesData';
import apiKubernetes from '@api1/kubernetes';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import { Typography } from '@mui/material';
import ReactLink from 'next/link';
import BoxLayout2 from '@components1/common/BoxLayout2';
import Datetime from '@components1/common/format/Datetime';
import CustomTabs from '@components1/common/CustomTabsForDrilldown';
import CustomTable from '@components1/common/tables/CustomTable2';

interface KubernetesTable2Props {
  id: string;
  allClusters: any[];
  clusterOption: [
    {
      label: string;
      value: string;
    }
  ];
}

interface FilterRequest {
  account_id?: string;
}

const KubernetesDashboardNodeExceptions: React.FC<KubernetesTable2Props> = ({ id, allClusters, clusterOption }) => {
  const filterOptions = [
    {
      name: 'Node Exception',
      value: 1,
      tabOptions: [{ id: 'node-failures', text: 'Node Failures', value: 0 }],
    },
  ];
  const currentDate = new Date();
  const startDate = new Date(currentDate);
  startDate.setDate(currentDate.getDate() - 1);

  const [nodeExceptionData, setNodeExceptionData] = useState([]);
  const [nodeSubTab, setNodeSubTab] = useState(0);
  const [filterObj, setFilterObj] = useState<FilterRequest>({});
  const [selectedDates, setSelectedDates] = useState([
    {
      startDate: startDate,
      endDate: currentDate,
      key: 'selection',
    },
  ]);
  const [loading, setLoading] = useState(false);
  const [isElementVisible, setIsElementVisible] = useState(false);
  const [shouldFetch, setShouldFetch] = useState(true);

  const tableRef = useRef<HTMLDivElement>(null);
  const tableId = `${id}-table`;

  useEffect(() => {
    const observerCallback = (entries: any) => {
      const entry = entries[0];
      if (entry.isIntersecting) {
        setIsElementVisible(entry.isIntersecting);
      }
    };
    const observerOptions = {
      root: null,
      rootMargin: '0px',
      threshold: 0.5,
    };
    const observer = new IntersectionObserver(observerCallback, observerOptions);
    if (tableRef.current) {
      observer.observe(tableRef.current);
    }
    return () => {
      if (tableRef.current) {
        observer.unobserve(tableRef.current);
      }
    };
  }, []);

  const getKubernetesNodeExceptionData = useCallback(
    (filters: any) => {
      if (!shouldFetch) {
        return;
      }
      const limit = 5;
      const subject_type = ['node'];
      const start_date = selectedDates[0].startDate;
      const end_date = selectedDates[0].endDate;
      setLoading(true);
      apiKubernetes
        .getK8sEvents(limit, 0, {
          subject_type,
          start_date,
          end_date,
          ...filters,
        })
        .then((response: any) => {
          setLoading(false);
          const clusterIdNameMap: { [accountId: string]: string } = {};
          allClusters.forEach((e) => {
            clusterIdNameMap[e.account_id] = e.account_name;
          });
          response?.data?.events.forEach((e: any) => {
            e.cluster = clusterIdNameMap[e.account_id];
          });

          const nodeExceptionData = response?.data?.events.map((e: any) => {
            const data = [];
            data.push({
              component: (
                <ClusterNameWithRegion
                  name={e?.subject_name}
                  nameOnClick={undefined}
                  additionalContent={makeAccountClicklable(e?.account_id, e?.cluster)}
                  hideIcon={true}
                  cursorPointer
                  font={undefined}
                  region={undefined}
                  namespace={undefined}
                  namespaceFont={undefined}
                  maxWidth={'350px'}
                />
              ),
              drilldownQuery: { workloadName: e?.workload_name, namespaceName: e?.namespace_name },
            });
            data.push({
              component: (
                <ClusterNameWithRegion
                  name={e?.title}
                  nameOnClick={undefined}
                  additionalContent={undefined}
                  hideIcon={true}
                  cursorPointer={false}
                  font={undefined}
                  region={undefined}
                  namespace={undefined}
                  namespaceFont={undefined}
                  maxWidth={'400px'}
                  showTruncatedString
                />
              ),
            });
            data.push({ text: e?.finding_type });
            data.push({ component: <CustomLabels margin='auto' text={e?.status} /> });
            data.push({ component: <Datetime baseDate={new Date()} value={e?.starts_at} /> });
            data.push({ component: <SeverityIcon severityType={e?.priority} /> });
            return data;
          });
          setNodeExceptionData(nodeExceptionData);
        })
        .finally(() => {
          setLoading(false);
          setShouldFetch(false);
        });
    },
    [shouldFetch]
  );

  useEffect(() => {
    if (isElementVisible) {
      getKubernetesNodeExceptionData(filterObj);
    }
  }, [isElementVisible, getKubernetesNodeExceptionData]);

  useEffect(() => {
    setShouldFetch(true);
  }, [filterObj, selectedDates]);

  const handleChangeNodeSubTab = (e: any, value: number) => {
    setNodeSubTab(value);
  };

  const makeAccountClicklable = (account_id: string, account_name: string) => {
    return (
      <Typography style={{ fontSize: 12 }}>
        Cluster:{' '}
        <ReactLink
          href={'/kubernetes/details/' + account_id + '#summary'}
          onClick={(event) => {
            event.stopPropagation();
          }}
        >
          {account_name}
        </ReactLink>
      </Typography>
    );
  };

  const filterByCluster = (e: any) => {
    let accountId = '';
    if (e) {
      accountId = e.target.value;
    }
    setFilterObj((prevFilterObj) => ({
      ...prevFilterObj,
      account_id: accountId,
    }));
  };

  const handleDateRangeChange = (selectedRange: any) => {
    const startTime = new Date(selectedRange.startTime);
    const endTime = new Date(selectedRange.endTime);

    const updatedDates = [
      {
        startDate: startTime,
        endDate: endTime,
        key: 'selection',
      },
    ];

    setSelectedDates(updatedDates);
  };

  return (
    <BoxLayout2
      dateTimeRange={{
        enabled: true,
        onChange: handleDateRangeChange,
        passedSelectedDateTime: {
          startTime: startDate.getTime(),
          endTime: currentDate.getTime(),
          shortcutClickTime: 0,
        },
      }}
      heading={'Node Exception'}
      id={id}
      sharingOptions={{
        sharing: {
          enabled: false,
          onClick: null,
        },
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: tableId,
            };
          },
        },
      }}
      filterOptions={[
        {
          type: 'dropdown',
          enabled: true,
          options: clusterOption,
          onSelect: filterByCluster,
          minWidth: '150px',
          label: 'Cluster',
          value: filterObj.account_id,
        },
      ]}
      extraOptions={[]}
    >
      <CustomTabs options={filterOptions[0].tabOptions} value={nodeSubTab} onChange={handleChangeNodeSubTab} />
      <div ref={tableRef}>
        <CustomTable
          id={tableId}
          headers={nodeExceptionHeader}
          tableData={nodeExceptionData}
          rowsPerPage={5}
          onPageChange={undefined}
          totalRows={nodeExceptionData.length}
          loading={loading}
          showExpandable={false}
          onSortChange={undefined}
          tableHeadingCenter={['Status', 'Severity']}
        />
      </div>
    </BoxLayout2>
  );
};

export default KubernetesDashboardNodeExceptions;
