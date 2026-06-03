import { Box } from '@mui/material';
import React, { useEffect, useState, type SetStateAction } from 'react';
import apiCloudAccount from '@api1/cloud-account';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import DownloadButton from '@common-new/DownloadButton';
import CloudAccountTable from './CloudAccountTable';
import Currency from '@common-new/format/Currency';
import HelpBeeModal from '@components1/helpbee';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { TicketsIcon } from '@assets';
import { getBrandingAsset } from '@hooks/useTenantBranding';
import RecommendationJobDetails from '@components1/k8s/common/RecommendationJobDetails';
import { action } from 'src/utils/actionStyles';
import { ds } from '@utils/colors';
import type { ICustomTable2Row } from './ec2/Instances';
import { CustomText } from './common';
import OptimizeSummary from './ec2/Summary';
import { useMetricCloudFilter } from '@hooks/useCloudFilters';
import { snakeToTitleCase } from 'src/utils/common';

const findOption = (options: any[], value: any) => (value ? options?.find((o: any) => o.value === value) ?? null : null);

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
              <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={ds.space[1]}>
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

  return (
    <>
      <HelpBeeModal isModalVisible={isHelpBeeOpen} onClose={() => setHelpBeeOpen(false)} />
      <ListingLayout id='right-sizing'>
        <ListingLayout.Toolbar
          title={props.heading ?? ''}
          actions={<DownloadButton id='right-sizing-download' onClick={() => ({ tableId: ec2OptimizeRightSizingTable })} />}
        >
          <FilterDropdown
            id='right-sizing-service-name'
            label='Service Name'
            options={serviceNamesFilter}
            value={findOption(serviceNamesFilter as any[], selectedServiceName)}
            onSelect={onServiceNamesFilterChange}
          />
          <FilterDropdown
            id='right-sizing-severity'
            label='Severity'
            options={severityFilterType}
            value={findOption(severityFilterType as any[], selectedSeverity)}
            onSelect={onSeverityFilterChange}
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
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
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};
export default CloudAccountMetrics;
