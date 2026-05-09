import { Box } from '@mui/material';
import React, { useEffect, useState, type SetStateAction } from 'react';
import apiCloudAccount from '@api1/cloud-account';
import BoxLayout2 from '@common/BoxLayout2';
import CloudAccountTable from './CloudAccountTable';
import Currency from '@components1/common/format/Currency';
import HelpBeeModal from '@components1/helpbee';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { TicketsIcon } from '@assets';
import { getBrandingAsset } from '@hooks/useTenantBranding';
import RecommendationJobDetails from '@components1/k8s/common/RecommendationJobDetails';
import { action } from 'src/utils/actionStyles';
import { getLast7Days } from '@lib/datetime';
import { CustomText, type ICustomTable2Row } from './ec2/Instances';
import OptimizeSummary from './ec2/Summary';
import { useMetricCloudFilter } from '@hooks/useCloudFilters';
import { snakeToTitleCase } from 'src/utils/common';

const CloudAccountMetrics = (props: { accountId: string | undefined; heading: string | undefined; serviceName: string }) => {
  const [ec2RightSizing, setEC2RightSizing] = useState([]);
  const [ec2RightSizingCount, setEC2RightSizingCount] = useState(0);
  const [selectedServiceName, setSelectedServiceName] = useState(null);
  const [selectedSeverity, setSelectedSeverity] = useState(null);
  const [_ticketData, setTicketData] = useState({} as any);
  const [isHelpBeeOpen, setHelpBeeOpen] = useState(false);
  const [recommendationStatus, _setRecommendationStatus] = useState('Open');
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);
  const [selectedDateRange, _setSelectedDateRange] = useState<any>({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });

  const rowsPerPage = 10;
  const ec2OptimizeRightSizingTable = 'ec2OptimizeRightSizingTable';
  const RIGHT_SIZING_HEADER = ['Instance Name', 'Service Name', 'Current type', 'Recommendation type', 'Savings', 'Updated at', 'Action'];

  const { serviceNamesFilter, severityFilterType } = useMetricCloudFilter(props.accountId as string);

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

  const onServiceNamesFilterChange = (e: { target: { value: SetStateAction<null> } }) => {
    setSelectedServiceName(e?.target?.value);
    setPage(0);
  };

  const onSeverityFilterChange = (e: { target: { value: SetStateAction<null> } }) => {
    setSelectedSeverity(e?.target?.value);
    setPage(0);
  };

  useEffect(() => {
    if (!props?.accountId && !props?.serviceName) {
      return;
    }
    setLoading(true);
    apiCloudAccount
      .getCloudResource({
        account_id: props?.accountId,
        serviceName: props?.serviceName,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
      })
      .then((res: any) => {
        setLoading(false);
        const ec2ResourceData = res.data?.data?.cloud_resourses?.map((item: any) => {
          const data: ICustomTable2Row[] = [];
          const MENU_ITEMS = [
            {
              icon: TicketsIcon,
              label: 'Create Ticket',
              id: 0,
            },
            {
              icon: getBrandingAsset('helpbeeIcon'),
              label: 'HelpBee',
              id: 1,
            },
          ];
          data.push({
            component: <CustomText text1={item.name} />,
            drilldownQuery: {
              resourceId: item.resourceId,
            },
          });
          data.push({
            component: <CustomText text1={snakeToTitleCase(item.type)} />,
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
        setEC2RightSizing(ec2ResourceData);

        setEC2RightSizingCount(0);
      })
      .catch(() => {
        setLoading(false);
      });
  }, [props?.accountId, page, selectedServiceName, selectedSeverity, recommendationStatus]);

  const handleChange = () => {
    return true;
  };

  return (
    <>
      <HelpBeeModal isModalVisible={isHelpBeeOpen} onClose={() => setHelpBeeOpen(false)} />
      <BoxLayout2
        heading={props.heading ?? ''}
        id='right-sizing'
        filterOptions={[
          {
            type: 'dropdown',
            enabled: true,
            options: serviceNamesFilter,
            onSelect: onServiceNamesFilterChange,
            minWidth: '150px',
            label: 'Service Name',
            value: selectedServiceName,
          },
          {
            type: 'dropdown',
            enabled: true,
            options: severityFilterType,
            onSelect: onSeverityFilterChange,
            minWidth: '150px',
            label: 'Severity',
            value: selectedSeverity,
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
          enabled: false,
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
                  return <OptimizeSummary accountId={props.accountId} resourceId={drilldownQuery.resourceId} serviceName={'AmazonEC2'} />;
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
export default CloudAccountMetrics;
