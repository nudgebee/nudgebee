import React, { useEffect, useState } from 'react';
import k8sApi from '@api1/kubernetes';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import BoxLayout2 from '@components1/common/BoxLayout2';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { v4 as uuidv4 } from 'uuid';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { Box } from '@mui/material';
import { RefreshSubmitButton } from '@components1/k8s/common/RefreshSubmitButton';
import UserHistoryButton from '@components1/common/UserHistory';
import useTicketFliter from '@hooks/useTicketFliter';
import QueryAutocomplete from '@components1/k8s/common/QueryAutocomplete';
import { Text } from '@components1/common';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { isAtMost70PercentDifferent } from 'src/utils/common';
import { snackbar } from '@components1/common/snackbarService';
import { LogDate } from '@components1/k8s/common/LogDate';

interface TimeRange {
  startTime: number;
  endTime: number;
  shortcutClickTime: number;
}

interface KubernetesLogStashProps {
  accountId: string;
  showQueryTextBox: boolean;
  query: string;
  timeRange: TimeRange;
  showPolling?: boolean;
}

export const mapToTableData = (data: any, getMenuItem: any = null, onMenuClick: any = null) => {
  const convertedJson = data.map((row: any) => row._source);
  const convertedJson2 = convertedJson.map((item: any) => {
    let message = item['message'] ?? item['log'] ?? item['msg'];
    if (!message) {
      const msg2 = item['kubernetes'] ?? item['_source'] ?? item;
      if (msg2) {
        message = JSON.stringify(msg2);
      }
    }
    let timestamp = item['@timestamp'];
    if (item['@timestamp']) {
      timestamp = new Date(item['@timestamp']).getTime();
    } else if (!timestamp && (item?.updated_at || item?.time)) {
      timestamp = new Date(item?.updated_at ?? item?.time).getTime();
    }
    const row = [
      {
        component: <LogDate timestamp={timestamp} log={message} />,
        drilldownQuery: item,
      },
      {
        component: <Text showAutoEllipsis value={message} />,
      },
    ];

    if (getMenuItem && onMenuClick) {
      row.push({
        component: <ThreeDotsMenu menuItems={getMenuItem()} data={item['message']} onMenuClick={onMenuClick} />,
      });
    }

    return row;
  });
  return convertedJson2;
};

const KubernetesLogStash: React.FC<KubernetesLogStashProps> = ({ accountId, showQueryTextBox = true, timeRange, query, showPolling = true }) => {
  const [data, setData] = useState<any>([]);
  const [logstashQuery, setLogstashQuery] = useState(query || `{"match_all": {}}`);
  const [loading, setLoading] = useState(false);
  const [time, setTime] = useState(timeRange);
  const [interval, setInterval] = useState(0);
  const [queryIndex, setQueryIndex] = useState('.*');
  const [pollLogs, setPollLogs] = useState(false);
  const [qLBuilderOption, setQLBuilderOption] = useState<any[]>([{ label: '', operator: '=', value: undefined }]);
  const [llmQueryResponse, setLlmQueryResponse] = useState('');
  const [generateQuestionText, setGenerateQuestionText] = useState('');
  const [conversationId, setConversationId] = useState('');

  const {
    ticketData,
    isTicketCreateFormOpen,
    getMenuItem,
    onMenuClick,
    closeTicketCreateForm,
    getTicketDescription,
    handleTicketSuccess,
    handleTicketFailure,
  } = useTicketFliter();

  const k8sLogs = 'k8sLogstash';

  useEffect(() => {
    if (logstashQuery && time.startTime > 0 && time.endTime > 0) {
      handleSubmit();
    }
  }, [time, accountId]);

  const handleSubmit = () => {
    if (!pollLogs) {
      setLoading(true);
    }
    let parsedQuery;
    try {
      parsedQuery = logstashQuery ? JSON.parse(logstashQuery) : '';
    } catch {
      snackbar.error('Invalid JSON format in query. Please check the syntax.');
      setLoading(false);
      return;
    }
    const requestBody = {
      no_sinks: true,
      track_history: true,
      body: {
        account_id: accountId,
        action_name: 'query_es',
        action_params: {
          query: parsedQuery,
          index: queryIndex,
        },
        origin: 'Nudgebee UI',
      },
      cache: false,
    };
    aiCreateFeedback(false);
    k8sApi
      .relayForwardRequest(requestBody)
      .then((res) => {
        if (res?.data?.success) {
          const parsedData = JSON.parse(res?.data?.data) || {};
          if (parsedData) {
            const hits = parsedData?.hits?.hits || [];
            if (hits && hits.length > 0) {
              const convertedJson2 = mapToTableData(hits, getMenuItem, onMenuClick);
              setData((prevData: any) => [...prevData, ...convertedJson2]);
            }
            aiCreateFeedback(true);
          }
        } else {
          snackbar.error('Failed to fetch the Logs');
        }
      })
      .catch(() => {
        snackbar.error('Failed to fetch the Logs');
      })
      .finally(() => {
        setLoading(false);
        setPollLogs(false);
      });
  };

  const onDateTimeRangeChange = (selectedDateTime: any) => {
    setTime(selectedDateTime);
    setData([]);
  };

  useEffect(() => {
    if (interval > 0) {
      const intervalId = window.setInterval(() => {
        setPollLogs(true);
        setTime({
          startTime: new Date().getTime() - 3600 * 1000,
          endTime: new Date().getTime(),
          shortcutClickTime: 0,
        });
      }, interval * 1000);
      return () => clearInterval(intervalId);
    }
  }, [interval]);

  const aiCreateFeedback = async (createFeedback: boolean) => {
    if ((llmQueryResponse && llmQueryResponse != logstashQuery && isAtMost70PercentDifferent(llmQueryResponse, logstashQuery)) || createFeedback) {
      await apiAskNudgebee.createAiFeedback({
        session_id: uuidv4(),
        module: 'es',
        question: generateQuestionText,
        llm_response: llmQueryResponse,
        user_corrected_response: logstashQuery,
        additional_notes: 'User did correction to the response',
        conversation_id: conversationId,
        cloud_account_id: accountId,
        useful: true,
      });
    }
  };

  return (
    <div>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Investigate Log',
          description: getTicketDescription(ticketData),
          accountId: accountId,
        }}
        ticketUrl={{}}
        reference={{
          id: uuidv4(),
          type: 'kubernetes',
        }}
      />
      <BoxLayout2
        id={'k8s-logs-stash'}
        heading=''
        dateTimeRange={{
          enabled: true,
          onChange: onDateTimeRangeChange,
          passedSelectedDateTime: time,
        }}
        extraOptions={
          showPolling
            ? [
                <Box key={'logstash-refresh-poll'} sx={{ width: 'fit-content' }}>
                  <RefreshSubmitButton
                    loading={pollLogs || loading}
                    onSubmit={() => {
                      handleSubmit();
                      setPollLogs(false);
                    }}
                    interval={interval}
                    setInterval={setInterval}
                  />
                </Box>,
                <UserHistoryButton key={'user-history-button'} accountId={accountId} module='log_query_es' onCopyClick={() => {}} />,
              ]
            : [<UserHistoryButton key={'user-history-button'} accountId={accountId} module='log_query_es' onCopyClick={() => {}} />]
        }
        filterOptions={[]}
        sharingOptions={{
          sharing: {
            enabled: false,
            onClick: null,
          },
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: k8sLogs,
              };
            },
          },
        }}
      >
        {showQueryTextBox ? (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: '10px',
              marginTop: '-34px',
              '& button': {
                marginTop: '',
              },
              '& .MuiOutlinedInput-root': {
                height: '32px',
              },
              width: '100%',
            }}
          >
            <QueryAutocomplete
              logProvider={'ES'}
              fullWidth
              accountId={accountId}
              query={logstashQuery}
              qLBuilderOption={qLBuilderOption}
              handleQLBuilder={setQLBuilderOption}
              callback={(e: any, type: string) => {
                if (type == 'es-index') {
                  setQueryIndex(e);
                  return;
                } else if (type == 'ai') {
                  setLlmQueryResponse(e);
                }
                setLogstashQuery(e);
              }}
              sendGenerateQuestionToParent={(e: string) => {
                setGenerateQuestionText(e);
              }}
              setConversationId={(e: string) => {
                setConversationId(e);
              }}
            />
            <br />
          </Box>
        ) : null}
        <KubernetesTable2
          id={k8sLogs}
          totalRows={data.length}
          data={data}
          headers={[{ name: 'Date', width: '20%' }, { name: 'Message', width: '80%' }, '']}
          rowsPerPage={data.length}
          showExpandable={true}
          expandable={{
            tabs: [
              {
                text: 'Log Details',
                value: 0,
                key: 'logstash-log',
              },
            ],
          }}
          loading={loading}
          onPageChange={undefined}
          onSortChange={undefined}
        />
      </BoxLayout2>
    </div>
  );
};

export default KubernetesLogStash;
