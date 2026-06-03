import CustomTable from '@common-new/tables/CustomTable2';
import { useEffect, useState } from 'react';
import { ListingLayout } from '@components1/ds/ListingLayout';
import DownloadButton from '@common-new/DownloadButton';
import Text from '@common-new/format/Text';
import { buildContainerLabel, extractNamespaceAndApplication } from 'src/utils/common';
import { toast as snackbar } from '@components1/ds/Toast';
import PropTypes from 'prop-types';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import { TicketsIcon } from '@assets';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { Box } from '@mui/material';
import { action } from 'src/utils/actionStyles';
import { Link } from '@components1/ds/Link';
import ticketsApi from '@api1/tickets';
import apiKubernetes1 from '@api1/kubernetes1';

const KubernetesLogSensitiveInfo = ({ accountId }) => {
  const [data, setData] = useState([]);
  const selectedDateRange = {
    startDate: new Date(new Date().getTime() - 60 * 60 * 1000).getTime(),
    endDate: new Date().getTime(),
  };
  const [loading, setLoading] = useState(false);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});

  const errorMessage = 'Failed to fetch the sensitive log information';

  const onMenuClick = (menuItems, data) => {
    if (menuItems.id === 0) {
      setTicketData(data?.metric);
      setIsTicketCreateFormOpen(true);
    }
  };

  const getTicketDescription = (data) => {
    let description = '';
    description += '**Namespace**: ' + extractNamespaceAndApplication(data?.container_id, 'namespace') + '\n';
    description += '**Application**: ' + extractNamespaceAndApplication(data?.container_id, 'application') + '\n';
    return description;
  };

  const handleDataFetch = () => {
    const fetchData = async () => {
      setLoading(true);
      const requestBody = {
        accountId: accountId,
        metrics: ['sensitive_log_messages'],
        startDate: selectedDateRange.startDate,
        endDate: selectedDateRange.endDate,
        kind: 'workload',
      };

      try {
        const relayResponse = await apiKubernetes1.utilisationApi(requestBody);
        if (!relayResponse?.length) {
          snackbar.error(errorMessage);
          setLoading(false);
          return;
        }
        if (relayResponse?.[0]?.payload?.length) {
          const seriesListResult = relayResponse[0].payload;
          const uniqueReferenceIds = Array.from(new Set(seriesListResult.map((item) => item.metric.pattern_hash)));
          const ticketResponse = await ticketsApi.listTicketsSummary({
            reference_id: uniqueReferenceIds,
          });
          const ticketReferenceMap = {};
          ticketResponse?.data?.tickets?.forEach((ticket) => {
            ticketReferenceMap[ticket.reference_id] = ticket;
          });
          const groupData = seriesListResult.map((item) => {
            const result = buildContainerLabel(item.metric.container_id);
            const referenceId = item.metric.pattern_hash;
            return [
              {
                component: (
                  <Box>
                    <Text value={result?.podname ? 'Pod: ' + result?.podname || '-' : result} showAutoEllipsis />
                    {result.namespace && <Text secondaryText value={`NS: ${result.namespace}`} showAutoEllipsis />}
                    {result.container && <Text secondaryText value={`Container: ${result.container}`} showAutoEllipsis />}
                  </Box>
                ),
              },
              {
                component: (
                  <Box sx={{ overflowWrap: 'anywhere' }}>
                    <Text value={item.metric.name} showAutoEllipsis sx={{ whiteSpace: 'pre-line' }} />{' '}
                  </Box>
                ),
              },
              {
                component: (
                  <Box sx={{ overflowWrap: 'anywhere' }}>
                    <Text value={item.metric.pattern} showAutoEllipsis sx={{ whiteSpace: 'pre-line' }} />
                  </Box>
                ),
              },
              {
                component: (
                  <Box sx={{ overflowWrap: 'anywhere' }}>
                    <Text value={item.metric.regex} showAutoEllipsis sx={{ whiteSpace: 'pre-line' }} />{' '}
                  </Box>
                ),
              },
              {
                component: ticketReferenceMap[referenceId]?.ticket_id ? (
                  <Text
                    value={
                      <Link openInNew href={`${ticketReferenceMap[referenceId]?.url}`}>
                        {ticketReferenceMap[referenceId]?.ticket_id}
                      </Link>
                    }
                  />
                ) : (
                  '--'
                ),
              },
              {
                component: (
                  <Box display={'flex'} justifyContent={'flex-end'}>
                    <ThreeDotsMenu
                      sx={{ ...action.primary }}
                      menuItems={[
                        {
                          icon: TicketsIcon,
                          label: 'Create Ticket',
                          id: 0,
                          disabled: referenceId in ticketReferenceMap,
                        },
                      ]}
                      data={item}
                      onMenuClick={onMenuClick}
                    />
                  </Box>
                ),
              },
            ];
          });
          setData(groupData);
        }

        setLoading(false);
      } catch {
        snackbar.error(errorMessage);
        setLoading(false);
      }
    };
    setData([]);
    fetchData();
  };

  useEffect(() => {
    handleDataFetch();
  }, [accountId]);

  const getTicketId = (data) => {
    let id = '';
    id += data?.pattern_hash;
    return id;
  };

  return (
    <>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={() => {
          setIsTicketCreateFormOpen(false);
        }}
        onClose={() => {
          setIsTicketCreateFormOpen(false);
        }}
        onSuccess={() => {
          handleDataFetch();
          setIsTicketCreateFormOpen(false);
          snackbar.success('Ticket Created Successfully');
        }}
        onFailure={() => {
          setIsTicketCreateFormOpen(false);
          snackbar.error('Failed to Create');
        }}
        ticketData={{
          subject: 'Sensitive Message in Log',
          description: getTicketDescription(ticketData),
          accountId: accountId,
        }}
        ticketUrl={{}}
        reference={{
          id: getTicketId(ticketData),
          type: 'kubernetes',
        }}
      />
      <ListingLayout id='sensitive-log'>
        <ListingLayout.Toolbar actions={<DownloadButton onClick={() => ({ tableId: 'sensitive-log-data' })} />} />
        <ListingLayout.Body>
          <CustomTable
            loading={loading}
            id={'sensitive-log-data'}
            headers={[
              {
                name: 'Application',
                width: '25%',
              },
              {
                name: 'Name',
                width: '10%',
              },
              {
                name: 'Pattern',
                width: '25%',
              },
              {
                name: 'regex',
                width: '30%',
              },
              {
                name: 'Ticket',
                width: '15%',
              },
              {
                name: '',
                width: '',
              },
            ]}
            tableData={data}
            rowsPerPage={data.length}
            totalRows={data.length}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

KubernetesLogSensitiveInfo.propTypes = {
  accountId: PropTypes.string,
};

export default KubernetesLogSensitiveInfo;
