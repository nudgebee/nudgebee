import { Box } from '@mui/material';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import React, { useEffect, useState, type SetStateAction } from 'react';
import apiCloudAccount from '@api1/cloud-account';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import DownloadButton from '@common-new/DownloadButton';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import { Button as DsButton } from '@components1/ds/Button';
import { SeverityIcon as DsSeverityIcon } from '@components1/ds/SeverityIcon';
import CloudAccountTable from './CloudAccountTable';
import HelpBeeModal from '@components1/helpbee';
import { TicketsIcon } from '@assets';
import { getBrandingAsset } from '@hooks/useTenantBranding';
import SafeIcon from '@components1/common/SafeIcon';
import type { ICustomTable2Row } from './ec2/Instances';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import Text from '@common-new/format/Text';
import { ds } from '@utils/colors';
import Datetime from '@common-new/format/Datetime';
import { useCloudFilter } from '@hooks/useCloudFilters';
import { toSeverityLevel } from '@utils/common';

const TABLE_COLUMNS = ['Message', 'Subject Name', 'Event', 'Principal', 'Severity', { name: 'Occurred time', sortEnabled: true }, ''];
const TABLE_ID = 'cloudaccount-events';
const ROWS_PER_PAGE = 10;

const CloudAccountSecurity = (props: { accountId: string | undefined; serviceName: string | undefined }) => {
  const [events, setEvents] = useState([]);
  const [eventsCount, setEventsCount] = useState(0);
  const [eventNamesFilter] = useState([]);
  const [selectedEventName, setSelectedEventName] = useState<string | null>(null);
  const [selectedServiceName, setSelectedServiceName] = useState<string | null>(null);
  const [selectedSeverity, setSelectedSeverity] = useState<string | null>(null);
  const [isHelpBeeOpen, setIsHelpBeeOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);

  const { serviceNamesFilter, severityFilterType } = useCloudFilter(props.accountId as string);

  const serviceNameOptions = (serviceNamesFilter || []).map((s: string) => ({ label: s, value: s }));
  const severityOptions = (severityFilterType || []).map((s: string) => ({ label: s, value: s }));

  const changePage = (page: number) => {
    setPage(page - 1);
  };

  const onMenuClick = (menuItem: { id: number }) => {
    if (menuItem.id === 1) {
      setIsHelpBeeOpen(true);
    }
  };

  const onEventNamesFilterChange = (e: { target: { value: SetStateAction<string | null> } }) => {
    setSelectedEventName(e?.target?.value ?? null);
    setPage(0);
  };
  const onServiceNamesFilterChange = (e: { target: { value: SetStateAction<string | null> } }) => {
    setSelectedServiceName(e?.target?.value ?? null);
    setPage(0);
  };
  const onSeverityFilterChange = (e: { target: { value: SetStateAction<string | null> } }) => {
    setSelectedSeverity(e?.target?.value ?? null);
    setPage(0);
  };

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
        ROWS_PER_PAGE,
        page * ROWS_PER_PAGE
      )
      .then((res: any) => {
        setLoading(false);
        const eventsData = res.data?.events?.map((item: any) => {
          const data: ICustomTable2Row[] = [];
          const MENU_ITEMS = [
            {
              id: `${TABLE_ID}-action-${item.id}-create-ticket`,
              icon: <SafeIcon src={TicketsIcon} alt='' width={14} height={14} />,
              label: 'Create Ticket',
              onSelect: () => onMenuClick({ id: 0 }),
            },
            {
              id: `${TABLE_ID}-action-${item.id}-helpbee`,
              icon: <SafeIcon src={getBrandingAsset('helpbeeIcon')} alt='' width={14} height={14} />,
              label: 'HelpBee',
              onSelect: () => onMenuClick({ id: 1 }),
            },
          ];

          data.push({
            component: ClusterNameWithRegion({
              name: item.title,
              hideIcon: true,
            }),
            drilldownQuery: { event: item },
            data: item.title,
          });

          data.push({
            component: (
              <Box sx={{ minWidth: '200px' }}>
                <Text showAutoEllipsis value={item.subject_name} />
                {item.subject_namespace && <Text secondaryText value={`ns: ${item.subject_namespace}`} />}
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

          data.push({
            component: <DsSeverityIcon level={toSeverityLevel(item.priority)} aria-label={`Severity: ${item.priority || 'unknown'}`} />,
            data: item.priority,
          });

          data.push({ component: <Datetime value={item.starts_at} />, data: item.starts_at });

          data.push({
            component: (
              <Box display='flex' justifyContent='flex-end' flexDirection='row' alignItems='center' gap={ds.space[1]}>
                <DsDropdownMenu
                  align='end'
                  size='sm'
                  items={MENU_ITEMS}
                  trigger={<DsButton tone='ghost' size='xs' composition='icon-only' aria-label='More actions' icon={<MoreVertIcon />} />}
                />
              </Box>
            ),
          });

          return data;
        });
        setEvents(eventsData);
        setEventsCount(res.data?.events_aggregate?.aggregate?.count ?? 0);
      })
      .catch(() => {
        setLoading(false);
      });
  }, [props?.accountId, page, selectedEventName, selectedServiceName, selectedSeverity]);

  return (
    <>
      <HelpBeeModal isModalVisible={isHelpBeeOpen} onClose={() => setIsHelpBeeOpen(false)} />
      <ListingLayout id={TABLE_ID}>
        <ListingLayout.Toolbar
          title='Events'
          data-testid={`${TABLE_ID}-filter-toolbar`}
          actions={<DownloadButton id={`${TABLE_ID}-download`} onClick={() => ({ tableId: TABLE_ID })} />}
        >
          <FilterDropdown
            id={`${TABLE_ID}-filter-event-names`}
            label='Event Names'
            options={eventNamesFilter}
            value={selectedEventName}
            onSelect={onEventNamesFilterChange}
          />
          <FilterDropdown
            id={`${TABLE_ID}-filter-service-name`}
            label='Service Name'
            options={serviceNameOptions}
            value={selectedServiceName}
            onSelect={onServiceNamesFilterChange}
          />
          <FilterDropdown
            id={`${TABLE_ID}-filter-severity`}
            label='Severity'
            options={severityOptions}
            value={selectedSeverity}
            onSelect={onSeverityFilterChange}
          />
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          <CloudAccountTable
            id={TABLE_ID}
            headers={TABLE_COLUMNS}
            data={events}
            rowsPerPage={ROWS_PER_PAGE}
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
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};
export default CloudAccountSecurity;
