/* eslint-disable prefer-const */
import { Box } from '@mui/material';
import React, { useEffect, useState, type SetStateAction } from 'react';
import apiCloudAccount from '@api1/cloud-account';
import BoxLayout2 from '@common/BoxLayout2';
import CloudAccountTable from './CloudAccountTable';
import HelpBeeModal from '@components1/helpbee';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { TicketsIcon } from '@assets';
import { getBrandingAsset } from '@hooks/useTenantBranding';
import { action } from 'src/utils/actionStyles';
import { getLast7Days } from '@lib/datetime';
import type { ICustomTable2Row } from './ec2/Instances';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import Text from '@components1/common/format/Text';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import Datetime from '@components1/common/format/Datetime';
import { useCloudFilter } from '@hooks/useCloudFilters';
import CustomTicketLink from '@components1/common/CustomTicketLink';

const TABLE_COLUMNS = ['Message', 'Subject Name', 'Event', 'Principal', 'Severity', { name: 'Occurred time', sortEnabled: true }, ''];

const CloudAccountTools = (props: { accountId: string | undefined; serviceName: string | undefined }) => {
  const [events, setEvents] = useState([]);
  const [eventsCount, setEventsCount] = useState(0);
  const [eventNamesFilter] = useState([]);
  const [selectedEventName, setSelectedEventName] = useState(null);
  const [selectedServiceName, setSelectedServiceName] = useState(null);
  const [selectedSeverity, setSelectedSeverity] = useState(null);
  const [selectedDateRange, _setSelectedDateRange] = useState<any>({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });

  const [_ticketData, setTicketData] = useState({} as any);
  const [isHelpBeeOpen, setHelpBeeOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);

  const rowsPerPage = 10;
  const cloudAccountEventsTable = 'cloudaccount-events';
  const _showEllipsis = true;

  const { serviceNamesFilter, severityFilterType } = useCloudFilter(props.accountId as string);

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

  const onEventNamesFilterChange = (e: { target: { value: SetStateAction<null> } }) => {
    setSelectedEventName(e?.target?.value);
    setPage(0);
  };
  const onServiceNamesFilterChange = (e: { target: { value: SetStateAction<null> } }) => {
    setSelectedServiceName(e?.target?.value);
    setPage(0);
  };

  const onSeverityFilterChange = (e: { target: { value: SetStateAction<null> } }) => {
    setSelectedSeverity(e?.target?.value);
    setPage(0);
  };

  //api call
  useEffect(() => {
    if (!props?.accountId) {
      return;
    }
    setLoading(true);
    apiCloudAccount
      .listEvents(
        {
          accountId: props?.accountId,
          subjectNamespace: props?.serviceName,
        },
        rowsPerPage,
        page * rowsPerPage
      )
      .then((res: any) => {
        setLoading(false);
        let ticketReferenceMap = new Map();
        const ec2ResourceData = res.data?.events?.map((item: any) => {
          let data: ICustomTable2Row[] = [];
          let MENU_ITEMS = [
            {
              icon: TicketsIcon,
              label: 'Create Ticket',
              id: 0,
              // disabled: resourcesItem.type !== undefined,
            },
            {
              icon: getBrandingAsset('helpbeeIcon'),
              label: 'HelpBee',
              id: 1,
            },
          ];
          data.push({
            component: ClusterNameWithRegion({
              name: item.title,
              hideIcon: true,
              region: ticketReferenceMap.has(item.fingerprint) ? (
                <CustomTicketLink
                  ticketURL={ticketReferenceMap.get(item.fingerprint)?.url}
                  ticketID={ticketReferenceMap.get(item.fingerprint)?.ticket_id}
                />
              ) : (
                <></>
              ),
            }),
            drilldownQuery: { event: item },
            data: item.title,
          });

          data.push({
            component: (
              <Box sx={{ minWidth: _showEllipsis && '200px' }}>
                <Text showAutoEllipsis value={item.subject_name} />
                {item.subject_namespace && <Text value={`ns: ${item.subject_namespace}`} secondaryText />}
              </Box>
            ),
          });

          data.push({
            component: <Text showAutoEllipsis value={item.aggregation_key} />,
            data: item.aggregation_key,
          });

          data.push({
            component: <Text showAutoEllipsis value={item.principal} />,
            data: item.aggregation_key,
          });

          data.push({ component: <SeverityIcon severityType={item.priority} />, data: item.priority });

          data.push({ component: <Datetime value={item.starts_at} />, data: item.starts_at });

          data.push({
            component: (
              <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'4px'}>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
              </Box>
            ),
          });

          return data;
        });
        setEvents(ec2ResourceData as any);
        setEventsCount(res.data?.events_aggregate?.aggregate?.count ?? 0);
      })
      .catch(() => {
        setLoading(false);
      });
  }, [props?.accountId, page, selectedEventName, selectedServiceName, selectedSeverity]);

  const handleChange = () => {
    return true;
  };
  return (
    <>
      <HelpBeeModal isModalVisible={isHelpBeeOpen} onClose={() => setHelpBeeOpen(false)} />
      <BoxLayout2
        heading={'Events'}
        id='cloudaccount-events'
        filterOptions={[
          {
            type: 'dropdown',
            enabled: true,
            options: eventNamesFilter,
            onSelect: onEventNamesFilterChange,
            minWidth: '150px',
            label: 'Event Names',
          },
          {
            type: 'dropdown',
            enabled: true,
            options: serviceNamesFilter,
            onSelect: onServiceNamesFilterChange,
            minWidth: '150px',
            label: 'Service Name',
          },
          {
            type: 'dropdown',
            enabled: true,
            options: severityFilterType,
            onSelect: onSeverityFilterChange,
            minWidth: '150px',
            label: 'Severity',
          },
        ]}
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: cloudAccountEventsTable,
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
          id={cloudAccountEventsTable}
          headers={TABLE_COLUMNS}
          data={events}
          rowsPerPage={rowsPerPage}
          onPageChange={changePage}
          totalRows={eventsCount}
          expandable={{
            tabs: [
              {
                componentFn: function (accountId: any, drilldownQuery: any) {
                  let evidences = drilldownQuery.event.evidences;
                  let evidencesData = [];
                  if (typeof evidences === 'string') {
                    evidencesData = JSON.parse(evidences);
                  }
                  if (evidencesData?.length > 0 && evidencesData[0].type == 'json') {
                    evidencesData = JSON.parse(evidencesData[0].data);
                  }
                  return (
                    <div>
                      <pre>{JSON.stringify(evidencesData, null, 2)}</pre>
                    </div>
                  );
                },
                text: 'EventDetails',
              },
            ],
          }}
          loading={loading}
          showExpandable={true}
          pageNumber={page + 1}
          tableHeadingCenter={'Severity'}
        />
      </BoxLayout2>
    </>
  );
};
export default CloudAccountTools;
