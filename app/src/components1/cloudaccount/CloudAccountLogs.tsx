/* eslint-disable prefer-const */
import { Box, Typography } from '@mui/material';
import { useEffect, useState } from 'react';
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
import Text from '@components1/common/format/Text';
import CustomButton from '@components1/common/NewCustomButton';

const TABLE_COLUMNS = ['Date', { name: 'Message', width: '90%' }, ''];

const CloudAccountLogs = (props: { accountId: string | undefined; serviceName: string | undefined }) => {
  const [logs, setLogs] = useState([]);
  const [logsCount, setLogsCount] = useState(0);
  const [selectedDateRange] = useState<any>({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });

  const [logstashQuery, setlogstashQuery] = useState('{"match_all": {}}');
  const [index, setIndex] = useState('logs-*-*');
  const [queryWrong, setQueryWrong] = useState(false);
  const [helperText, setHelperText] = useState('');
  const [_errorMsg, setErrorMsg] = useState('');
  const [time] = useState({ startTime: new Date().getTime() - 3600 * 1000, endTime: new Date().getTime() });
  const showQueryTextBox = true;

  const [_ticketData, setTicketData] = useState({} as any);
  const [isHelpBeeOpen, setHelpBeeOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);

  const rowsPerPage = 10;
  const cloudAccountLogsTable = 'cloudaccount-logs';
  const _showEllipsis = true;
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

  const handleKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') {
      handleSubmit();
    }
  };
  const handleSubmit = () => {
    setErrorMsg('');
    setHelperText('');
    setQueryWrong(false);
    let _startTime = time?.startTime * 1000000;
    let _endTime = time?.endTime * 1000000;
    if (selectedDateRange) {
      _startTime = selectedDateRange.startTime * 1000000;
      _endTime = selectedDateRange.endTime * 1000000;
    }
    try {
      JSON.parse(logstashQuery);
    } catch {
      setQueryWrong(true);
      setHelperText('Invalid JSON');
      return;
    }
    setLoading(true);

    setLoading(false);
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
        let _ticketReferenceMap = new Map();
        const ec2ResourceData = res.data?.events?.map((item: any) => {
          let data: ICustomTable2Row[] = [];
          let MENU_ITEMS = [
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
            component: (
              <Typography fontSize={'14px'} fontWeight={400}>
                {new Date(item.starts_at).toLocaleString()}
              </Typography>
            ),
            drilldownQuery: { event: item },
            data: item.title,
          });

          data.push({
            component: <Text showAutoEllipsis value={item.aggregation_key} />,
            data: item.aggregation_key,
          });

          data.push({
            component: (
              <Box display={'flex'} flexDirection={'row'} alignItems={'right'} gap={'4px'}>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
              </Box>
            ),
          });

          return data;
        });
        setLogs(ec2ResourceData as any);
        setLogsCount(res.data?.events_aggregate?.aggregate?.count ?? 0);
      })
      .catch(() => {
        setLoading(false);
      });
  }, [props?.accountId, page]);

  const handleChange = () => {
    return true;
  };
  return (
    <>
      <HelpBeeModal isModalVisible={isHelpBeeOpen} onClose={() => setHelpBeeOpen(false)} />
      <BoxLayout2
        heading={'Logs'}
        id='cloudaccount-events'
        filterOptions={[
          ...(showQueryTextBox
            ? [
                {
                  type: 'textfield',
                  enabled: true,
                  onChange: (e: any) => {
                    setlogstashQuery(e.target.value);
                    setQueryWrong(false);
                    setHelperText('');
                  },
                  label: 'Logstash Query (ES/QL)',
                  value: logstashQuery,
                  id: 'logstashQuery',
                  onKeyDown: handleKeyDown,
                  error: queryWrong,
                  helperText: helperText,
                },
                {
                  type: 'textfield',
                  enabled: true,
                  label: 'Index',
                  onChange: (e: any) => {
                    setIndex(e.target.value);
                    setQueryWrong(false);
                    setHelperText('');
                  },
                  value: index,
                  id: 'index',
                  onKeyDown: handleKeyDown,
                  error: queryWrong,
                  helperText: helperText,
                },
              ]
            : []),
          {
            type: 'custom',
            enabled: true,
            component: (
              <CustomButton
                text={'Submit'}
                onClick={() => {
                  handleSubmit();
                }}
                variant='blueButton'
              />
            ),
          },
        ]}
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: cloudAccountLogsTable,
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
          id={cloudAccountLogsTable}
          headers={TABLE_COLUMNS}
          data={logs}
          rowsPerPage={rowsPerPage}
          onPageChange={changePage}
          totalRows={logsCount}
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
        />
      </BoxLayout2>
    </>
  );
};
export default CloudAccountLogs;
