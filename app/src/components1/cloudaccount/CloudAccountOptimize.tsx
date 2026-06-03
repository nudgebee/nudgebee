/* eslint-disable prefer-const */
import { Box } from '@mui/material';
import { useEffect, useState, type SetStateAction } from 'react';
import apiCloudAccount from '@api1/cloud-account';
import BoxLayout2 from '@common/BoxLayout2';
import CloudAccountTable from './CloudAccountTable';
import Currency from '@components1/common/format/Currency';
import HelpBeeModal from '@components1/helpbee';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import RecommendationJobDetails from '@components1/k8s/common/RecommendationJobDetails';
import { action } from 'src/utils/actionStyles';
import { getLast7Days } from '@lib/datetime';
import type { ICustomTable2Row } from './ec2/Instances';
import OptimizeUtilization from './OptimizeUnutilized';
import { MENU_ITEMS, CustomText } from './common';

const RIGHT_SIZING_HEADER = ['Instance Name', 'Service Name', 'Current type', 'Recommendation type', 'Savings', 'Updated at', 'Action'];

const CloudAccountOptimize = (props: { accountId: string | undefined; heading: string | undefined }) => {
  const [ec2RightSizing, setEC2RightSizing] = useState([]);
  const [ec2RightSizingCount, setEC2RightSizingCount] = useState(0);
  const [namespaceFilter] = useState([]);
  const [selectedNamespace, setSelectedNamespace] = useState(null);
  const [selectedWorkloadType] = useState(null);
  const [_ticketData, setTicketData] = useState({} as any);
  const [isHelpBeeOpen, setHelpBeeOpen] = useState(false);
  const [recommendationStatus, _setRecommendationStatus] = useState('Open');
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);
  const rowsPerPage = 10;
  const ec2OptimizeRightSizingTable = 'ec2OptimizeRightSizingTable';
  const [selectedDateRange, _setSelectedDateRange] = useState<any>({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });

  const changePage = (page: number) => {
    setPage(page - 1);
  };

  const onMenuClick = (menuItem: { id: number }, data: any) => {
    if (menuItem.id === 0) {
      setTicketData(data);
    }
    if (menuItem.id === 1) {
      setHelpBeeOpen(true);
    }
  };

  const onNamespaceFilterChange = (e: { target: { value: SetStateAction<null> } }) => {
    setSelectedNamespace(e?.target?.value);
    setPage(0);
  };

  //api call
  useEffect(() => {
    if (!props?.accountId) {
      return;
    }
    setLoading(true);
    apiCloudAccount
      .getCloudResource({
        account_id: props?.accountId,
        serviceName: 'AmazonEC2',
        type: 'compute-instance',
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
      })
      .then((res: any) => {
        setLoading(false);
        const ec2ResourceData = res.data?.data?.cloud_resourses?.map((item: any) => {
          let data: ICustomTable2Row[] = [];
          data.push({
            component: <CustomText text1={item.name} />,
            drilldownQuery: {
              podName: item.name,
              workloadName: item.name,
              namespaceName: item.meta?.namespace,
              cpuRecc: 'cpuRecommLimit',
              cpuReq: 'cpuRecommReq',
              memoryReq: 'memoryRecommReq',
              memoryRecc: 'memoryRecommLimit',
              resourceId: item.resourceId,
              memLimit: '2',
              cpuLimit: '2',
              recommendation: item,
            },
          });
          data.push({
            component: <CustomText text1={'EC2'} />,
            text: item.resourse_id,
          });
          data.push({
            component: <CustomText text1={'t3.xlarge'} />,
          });
          data.push({
            component: <CustomText text1={'8.5'} text2={'vCPU'} />,
          });
          data.push({ component: <Currency value={45} suffix={'/mo'} /> });
          data.push({
            component: <CustomText text1={'8h'} text2={'ago'} />,
          });
          data.push({
            component: (
              <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'4px'}>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
              </Box>
            ),
          });

          return data;
        });
        setEC2RightSizing(ec2ResourceData as any);

        setEC2RightSizingCount(0);
      })
      .catch(() => {
        setLoading(false);
      });
  }, [props?.accountId, page, selectedNamespace, selectedWorkloadType, recommendationStatus]);

  const handleChange = () => {
    return true;
  };
  return (
    <>
      <HelpBeeModal isModalVisible={isHelpBeeOpen} onClose={() => setHelpBeeOpen(false)} />
      <BoxLayout2
        heading={props.heading === undefined ? '' : props.heading}
        id='right-sizing'
        filterOptions={[
          {
            type: 'dropdown',
            enabled: true,
            options: namespaceFilter,
            onSelect: onNamespaceFilterChange,
            minWidth: '150px',
            label: 'Namespace',
          },
        ]}
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: ec2OptimizeRightSizingTable,
              };
            },
          },
          sharing: { enabled: false, onClick: null },
        }}
        dateTimeRange={{
          enabled: true,
          onChange: handleChange,
          passedSelectedDateTime: {
            startTime: selectedDateRange.startDate,
            endTime: selectedDateRange.endDate,
            shortcutClickTime: 0,
          },
        }}
      >
        <CloudAccountTable
          id={ec2OptimizeRightSizingTable}
          headers={RIGHT_SIZING_HEADER}
          data={ec2RightSizing}
          rowsPerPage={rowsPerPage}
          onPageChange={changePage}
          totalRows={ec2RightSizingCount}
          expandable={{
            tabs: [
              {
                componentFn: function (accountId: any, drilldownQuery: any) {
                  return (
                    <OptimizeUtilization
                      account={accountId}
                      namespaceName={drilldownQuery.namespace}
                      workloadName={drilldownQuery.workloadName}
                      recc={{
                        cpuRecc: drilldownQuery.cpuRecc,
                        memoryRecc: drilldownQuery.memoryRecc,
                        cpuLimit: drilldownQuery.cpuLimit,
                        memLimit: drilldownQuery.memLimit,
                      }}
                    />
                  );
                },
                text: 'CPU & Memory Utilization',
              },
            ],
          }}
          loading={loading}
          showExpandable={true}
          pageNumber={page + 1}
        />
        <RecommendationJobDetails jobName={'krr_scan'} />
      </BoxLayout2>
    </>
  );
};
export default CloudAccountOptimize;
