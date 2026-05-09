import React, { useEffect, useState, useCallback } from 'react';
import { useRouter } from 'next/router';
import { v4 as uuidv4 } from 'uuid';
import { md5 } from '@lib/encode';
import SafeIcon from '@components1/common/SafeIcon';
import { Box, ToggleButton, ToggleButtonGroup } from '@mui/material';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import CustomDropdown from '@components1/common/CustomDropdown';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { Text, BoxLayout2, ThreeDotsMenu } from '@components1/common';
import { RefreshSubmitButton } from '@components1/k8s/common/RefreshSubmitButton';
import UserHistoryButton from '@components1/common/UserHistory';
import CustomIconButton from '@components1/CustomIconButton';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import QueryModeSwitcher from '@components1/k8s/common/QueryModeSwitcher';
import { OperatorDescriptor } from '@components1/k8s/common/operatorCatalog';
import { LogDate } from '@components1/k8s/common/LogDate';
import ConfigureWarning from '@components1/k8s/common/ConfigureWarning';
import CloudProviderIcon from '@components1/common/CloudIcon';
import { isAtMost70PercentDifferent, parseHttpResponseBodyMessage, safeJSONParse, snakeToTitleCase } from 'src/utils/common';
import { action } from 'src/utils/actionStyles';
import { useData } from '@context/DataContext';
import useTicketFliter from '@hooks/useTicketFliter';
import apiAskNudgebee from '@api1/ask-nudgebee';
import observability from '@api1/observability';
import apiAccount from '@api1/account';
import ticketsApi from '@api1/tickets';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import { snackbar } from '@components1/common/snackbarService';
import cache from '@lib/cache';
import { colors } from 'src/utils/colors';
import { infoIcon, TicketsIcon } from '@assets';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import CustomTooltip from '@components1/common/CustomTooltip';

interface TimeRange {
  [x: string]: number;
  startTime: number;
  endTime: number;
  shortcutClickTime: number;
}

const k8sLogs = 'k8sLogs';

interface KubernetesLogProps {
  accountId: string;
  showTrend: boolean;
  showQueryTextBox: boolean;
  dateTime: any;
  queryFromProps: string;
  showPolling?: boolean;
  showDateFilter?: boolean;
  showPlusMinusTab?: boolean;
}

const KubernetesLogs: React.FC<KubernetesLogProps> = ({
  accountId,
  queryFromProps = '',
  showQueryTextBox = false,
  dateTime = {},
  showPolling = true,
  showDateFilter = true,
  showPlusMinusTab = true,
}) => {
  const router = useRouter();
  const { selectedCluster } = useData();
  const { assistantName } = useTenantBranding();

  const [data, setData] = useState<any[]>([]);
  const [logQuery, setLogQuery] = useState('');
  const [errorMsg, setErrorMsg] = useState('');
  const [loading, setLoading] = useState(false);
  const [logProvider, setLogProvider] = useState('');
  const [operatorDescriptors, setOperatorDescriptors] = useState<OperatorDescriptor[] | undefined>(undefined);
  const [runInitialQuery, setRunInitialQuery] = useState(false);
  const [time, setTime] = useState<any>(
    dateTime || {
      startTime: Number(router.query.startTime) || (router.query.start ? Number(router.query.start) / 1000000 : 0),
      endTime: Number(router.query.endTime) || (router.query.end ? Number(router.query.end) / 1000000 : 0),
      shortcutClickTime: 0,
    }
  );
  const [interval, setInterval] = useState(0);
  const [pollLogs, setPollLogs] = useState(false);
  const [llmQueryResponse, setLlmQueryResponse] = useState('');
  const [generateQuestionText, setGenerateQuestionText] = useState('');
  const [conversationId, setConversationId] = useState('');
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiSessionId, setNubiSessionId] = useState('');
  const [isAiLoading, setIsAiLoading] = useState(false);
  const [logQueryItems, setLogQueryItems] = useState<any[]>([]);
  const [logOperations, setLogOperations] = useState<any[]>([]);
  const [qLEditor, setQLEditor] = useState('code');
  const [esIndex, setEsIndex] = useState('');
  const [logLimit, setLogLimit] = useState(Number(router.query.limit) || 100);
  const [queryRequestFromProps, setQueryRequestFromProps] = useState<any[] | null>(null);
  const [checkMapper, setCheckMapper] = useState(false);
  const [tabs, setTabs] = useState([{ text: 'Log Details', value: 0, key: 'log-details' }]);

  // Ticket Hook
  const {
    ticketData,
    isTicketCreateFormOpen,
    onMenuClick,
    closeTicketCreateForm,
    getTicketDescription,
    getTicketReferenceId,
    handleTicketSuccess,
    handleTicketFailure,
  } = useTicketFliter();

  const [rawLogs, setRawLogs] = useState<any[]>([]);
  const [ticketReferenceMap, setTicketReferenceMap] = useState<Map<string, any>>(new Map());

  const fetchTicketsForLogs = useCallback(
    async (results: any[]) => {
      if (!results?.length) {
        setTicketReferenceMap(new Map());
        return;
      }
      const referenceIds = Array.from(new Set(results.map((res) => getTicketReferenceId({ stream: res, data: res.message })).filter(Boolean)));
      if (!referenceIds.length) {
        setTicketReferenceMap(new Map());
        return;
      }
      try {
        const res: any = await ticketsApi.listTicketsSummary({ reference_id: referenceIds });
        const map = new Map<string, any>();
        res?.data?.tickets?.forEach((t: any) => map.set(t.reference_id, t));
        setTicketReferenceMap(map);
      } catch {
        setTicketReferenceMap(new Map());
      }
    },
    [getTicketReferenceId]
  );

  const getDefaultLogQuery = (provider: string) => {
    switch (provider) {
      case 'loki':
        return '{stream="stdout"}';
      case 'loggly':
        return '*';
      case 'observe':
        return ' ';
      default:
        return '';
    }
  };

  const resetStates = () => {
    setData([]);
    setErrorMsg('');
    setLlmQueryResponse('');
    setGenerateQuestionText('');
    setConversationId('');
    setLogQuery('');
    setLogQueryItems([]);
    setNubiQuery('');
    setNubiSidebarVisible(false);
    setInterval(0);
    setPollLogs(false);
    setRunInitialQuery(false);
    setQueryRequestFromProps(null);
    setCheckMapper(false);
  };

  const handleLogQueryFromDrilldown = useCallback((item: any) => {
    setLogQueryItems((prev) => {
      const existingIndex = prev.findIndex((chip) => chip.id === item.id);
      if (existingIndex !== -1) {
        const updated = [...prev];
        updated[existingIndex] = { ...updated[existingIndex], operator: item.operator };
        return updated;
      }
      return [...prev, item];
    });
  }, []);

  const handleGenerateLogAnalysis = useCallback((stream: any, message: string) => {
    const analysisPrompt = `@loganalysis analyse the following log and provide the root cause and possible actions to resolve the issue \n\n ${JSON.stringify(
      stream
    )} message:${message}`;
    setNubiQuery(analysisPrompt);
    setNubiSessionId(md5([JSON.stringify(stream)]));
    setNubiSidebarVisible(true);
  }, []);

  const formatLogResults = useCallback(
    (allResults: any[]) => {
      const result: any[] = allResults.map((res: any) => {
        let drilldownQuery = '';
        if (res?.labels?.namespace && res?.labels?.pod) {
          drilldownQuery = `{"namespaceName": "${res.labels.namespace}", "podName": "${res.labels.pod}"}`;
        }
        const refId = getTicketReferenceId({ stream: res, data: res.message });
        const existingTicket = refId ? ticketReferenceMap.get(refId) : undefined;
        const menuItems = [
          {
            icon: TicketsIcon,
            label: existingTicket ? `Ticket created: ${existingTicket.ticket_id}` : 'Create Ticket',
            id: 0,
            disabled: !!existingTicket,
          },
        ];

        return [
          {
            component: <LogDate timestamp={res.timestamp} log={res.severity} />,
            drilldownQuery: {
              data: res,
              callback:
                showQueryTextBox && !['azure_app_insights', 'loggly', 'observe', 'ES', 'newrelic'].includes(logProvider)
                  ? handleLogQueryFromDrilldown
                  : undefined,
              logQuery: drilldownQuery,
            },
          },
          {
            component: (
              <Box>
                <Text value={res.message} sx={{ fontSize: '13px', lineHeight: '1.6', overflowWrap: 'anywhere', wordBreak: 'break-all' }} />
                {existingTicket && (
                  <Box sx={{ mt: '2px' }}>
                    <CustomTicketLink ticketURL={existingTicket.url} ticketID={existingTicket.ticket_id} />
                  </Box>
                )}
              </Box>
            ),
          },
          {
            component: (
              <Box display={'flex'} justifyContent={'flex-end'}>
                <CustomIconButton
                  onClick={(event) => {
                    event.stopPropagation();
                    handleGenerateLogAnalysis(res, res.message);
                  }}
                  variant={'secondary'}
                  size={'xsmall'}
                  sx={{ height: '28px', mr: '4px', width: '28px' }}
                >
                  <SafeIcon alt={`Ask ${assistantName}`} src={getNubiIconUrl()} width={24} height={24} />
                </CustomIconButton>
                <ThreeDotsMenu sx={{ ...action.primary }} menuItems={menuItems} data={{ stream: res, data: res.message }} onMenuClick={onMenuClick} />
              </Box>
            ),
          },
        ];
      });

      setData(result);
    },
    [
      logProvider,
      showQueryTextBox,
      onMenuClick,
      handleLogQueryFromDrilldown,
      handleGenerateLogAnalysis,
      getTicketReferenceId,
      ticketReferenceMap,
      assistantName,
    ]
  );

  const createUserHistory = async (query: string, status: string, duration: number) => {
    await observability.createUserHistory({
      account_id: accountId,
      data: query,
      duration: duration,
      module: `log_query_${logProvider?.toLowerCase()}`,
      status: status,
    });
  };

  const aiCreateFeedback = async (executedQuery: string) => {
    if (llmQueryResponse && llmQueryResponse !== executedQuery && isAtMost70PercentDifferent(llmQueryResponse, executedQuery)) {
      await apiAskNudgebee.createAiFeedback({
        session_id: uuidv4(),
        module: logProvider,
        question: generateQuestionText,
        llm_response: llmQueryResponse,
        user_corrected_response: executedQuery,
        additional_notes: 'User did correction to the response',
        conversation_id: conversationId,
        cloud_account_id: accountId,
        useful: true,
      });
    }
  };

  // -- Main Query Handler --

  // FIX: Removed the query argument. Always uses logQuery state.
  const handleSubmit = useCallback(
    async (fromOnSubmit = false) => {
      setErrorMsg('');
      if (!pollLogs) {
        setData([]);
        setLoading(true);
      }

      const now = new Date().getTime();
      const effectiveQuery = logQuery;

      try {
        if (accountId === 'demo') {
          setLoading(false);
          return;
        }

        // Structured query from props (traces drilldown) — unified path
        if (queryRequestFromProps && queryRequestFromProps.length > 0) {
          const requestBody: any = {
            account_id: accountId,
            end_time: time.endTime,
            start_time: time.startTime,
            query: '',
            limit: logLimit,
            offset: 0,
            query_request: {
              where: { _and: queryRequestFromProps },
            },
            request: {
              query_type: 'dsl',
              checkMapper,
            },
          };
          const response = await observability.fetchLogs(requestBody);
          const error = response?.error || response?.data?.errors || '';
          if (error) {
            throw new Error(parseHttpResponseBodyMessage(response.data));
          }
          const allResults = response?.data?.data?.logs_query || [];
          setRawLogs(allResults);
          fetchTicketsForLogs(allResults);
          formatLogResults(allResults);
          setLoading(false);
          fromOnSubmit && createUserHistory(JSON.stringify(queryRequestFromProps), 'SUCCESS', new Date().getTime() - now);
          return;
        }

        let payloadQuery = effectiveQuery;
        if (logProvider === 'signoz') {
          payloadQuery = effectiveQuery ? JSON.stringify(effectiveQuery) : JSON.stringify([]);
        } else if (logProvider === 'observe') {
          payloadQuery = effectiveQuery;
        }

        // Construct Request Body
        const requestBody: any = {
          account_id: accountId,
          end_time: time.endTime,
          start_time: time.startTime,
          query: payloadQuery,
          limit: logLimit,
          offset: 0,
        };

        if (logProvider !== 'ES' && qLEditor === 'build') {
          const trimmedQuery = typeof effectiveQuery === 'string' ? effectiveQuery.trim() : '';
          if (!trimmedQuery?.startsWith('[')) {
            // Empty or not a valid JSON array (e.g. stale query from a previous provider switch)
            setLoading(false);
            if (fromOnSubmit) {
              snackbar.warning('Please select at least one label filter');
            }
            return;
          }
          const parsedWhere = safeJSONParse(trimmedQuery);
          if (parsedWhere && Array.isArray(parsedWhere) && parsedWhere.length > 0) {
            delete requestBody.query;
            requestBody.query_request = {
              where: { _and: parsedWhere },
            };
            requestBody.query = '';
          } else {
            setLoading(false);
            if (fromOnSubmit) {
              snackbar.warning('Please select at least one label filter');
            }
            return;
          }
        } else if (logProvider == 'ES' && qLEditor == 'code') {
          requestBody['request'] = { query_type: 'dsl', ...(esIndex ? { index: esIndex } : {}) };
        } else if (logProvider == 'ES' && qLEditor == 'build') {
          if (logQueryItems && logQueryItems.length > 0) {
            const mapESOperatorToBackend = (uiOperator: string): string => {
              const operatorMap: Record<string, string> = {
                '=': '_eq',
                '!=': '_neq',
                is_one_of: '_in',
                is_not_one_of: '_not_in',
              };
              return operatorMap[uiOperator] || uiOperator;
            };

            const structuredQuery: any[] = [];
            logQueryItems.forEach((item) => {
              let backendOp = mapESOperatorToBackend(item.operator);
              let value: any = item.value;

              // exists/!exists → _is_null with boolean value
              if (item.operator === 'exists') {
                backendOp = '_is_null';
                value = false;
              } else if (item.operator === '!exists') {
                backendOp = '_is_null';
                value = true;
              }

              // is_one_of/is_not_one_of → split comma-separated string into array
              if (item.operator === 'is_one_of' || item.operator === 'is_not_one_of') {
                value = String(item.value)
                  .split(',')
                  .map((v: string) => v.trim())
                  .filter((v: string) => v !== '');
              }

              structuredQuery.push({
                _binary: {
                  [item.label]: {
                    [backendOp]: value,
                  },
                },
              });
            });

            requestBody.query_request = {
              where: { _and: structuredQuery },
            };
            requestBody.query = '';
          } else {
            setLoading(false);
            if (fromOnSubmit) {
              snackbar.warning('Please select at least one label filter');
            }
            return;
          }
          requestBody['request'] = { query_type: 'dsl', ...(esIndex ? { index: esIndex } : {}) };
        }

        // Execute Request
        const response = await observability.fetchLogs(requestBody);

        const error = response?.error || response?.data?.errors || '';
        if (error) {
          throw new Error(parseHttpResponseBodyMessage(response.data));
        }

        const allResults = response?.data?.data?.logs_query || [];
        setRawLogs(allResults);
        fetchTicketsForLogs(allResults);
        formatLogResults(allResults);
        setLoading(false);

        llmQueryResponse && aiCreateFeedback(effectiveQuery);
        fromOnSubmit && createUserHistory(effectiveQuery, 'SUCCESS', new Date().getTime() - now);
      } catch (err: any) {
        const errMsg = err.message || 'Error fetching logs';
        snackbar.error(`Error: ${errMsg}`);
        setLoading(false);
        fromOnSubmit && createUserHistory(effectiveQuery, 'FAILED', new Date().getTime() - now);
      }
    },
    [
      logQuery,
      logProvider,
      accountId,
      time,
      logLimit,
      pollLogs,
      llmQueryResponse,
      qLEditor,
      logQueryItems,
      logOperations,
      esIndex,
      formatLogResults,
      generateQuestionText,
      conversationId,
      queryRequestFromProps,
    ]
  );

  useEffect(() => {
    const initProvider = async () => {
      setLogProvider('');
      setOperatorDescriptors(undefined);
      resetStates();
      if (accountId === 'demo') {
        setLogProvider('loki');
        return;
      }

      const cacheKey = `${accountId}-log-v3`;
      const indexCacheKey = `${accountId}-log-index`;
      const cached = cache.get(cacheKey);

      if (cached && typeof cached === 'object' && cached.provider) {
        setLogProvider(cached.provider);
        setOperatorDescriptors(cached.operator_descriptors);
        setEsIndex(cache.get(indexCacheKey) || '');
        return;
      }

      try {
        const res = await apiAccount.getDefaultProvider({
          account_id: accountId,
          provider_type: 'logs',
        });

        if (res?.data?.errors) {
          snackbar.error(parseHttpResponseBodyMessage(res?.data));
          return;
        }

        const provider = res?.data?.data?.get_default_provider?.provider || selectedCluster?.agent?.connection_status?.logsConnectionProvider || '';
        const defaultIndex = res?.data?.data?.get_default_provider?.default_index || '';
        const descriptors = res?.data?.data?.get_default_provider?.capabilities?.supported_operator_descriptors;
        setLogProvider(provider);
        setOperatorDescriptors(descriptors);
        setEsIndex(defaultIndex);
        if (defaultIndex) {
          cache.set(indexCacheKey, defaultIndex, 60 * 60);
        }
        cache.set(cacheKey, { provider, operator_descriptors: descriptors }, 60 * 60);
      } catch (error: any) {
        snackbar.error(error.message || 'Failed to fetch default provider');
      }
    };

    if (accountId) {
      initProvider();
    }
  }, [accountId, selectedCluster]);

  // 2. Parse URL Params & Initialize Query
  useEffect(() => {
    if (!router.isReady || !logProvider) {
      return;
    }

    const initializeQuery = () => {
      let queryFilter: any = router.query?.filter || queryFromProps;

      let calculatedQuery = getDefaultLogQuery(logProvider);
      let hasStructuredQuery = false;

      if (queryFilter) {
        try {
          if (typeof queryFilter === 'string') {
            queryFilter = JSON.parse(queryFilter);
          }

          if (Object.keys(queryFilter).length > 0) {
            const namespaceLabel = 'namespace';
            const appLabel = 'app';
            const podLabel = 'pod';
            const traceIdLabelName = 'trace_id';

            const whereItems: any[] = [];

            if (queryFilter.namespaceName) {
              whereItems.push({ _binary: { [namespaceLabel]: { _eq: queryFilter.namespaceName } } });
            }
            if (queryFilter.workloadName) {
              whereItems.push({ _binary: { [appLabel]: { _eq: queryFilter.workloadName } } });
            }
            if (queryFilter.podName) {
              whereItems.push({ _binary: { [podLabel]: { _eq: queryFilter.podName } } });
            }
            if (queryFilter.traceId) {
              whereItems.push({ _binary: { [traceIdLabelName]: { _eq: queryFilter.traceId } } });
            }
            if (whereItems.length > 0) {
              setQueryRequestFromProps(whereItems);
              calculatedQuery = '';
              hasStructuredQuery = true;
              setCheckMapper(true);
            }
            setPollLogs(false);
          }
        } catch (e) {
          console.error('Error parsing query filter', e);
        }
      }
      setLogQuery(calculatedQuery);

      if (calculatedQuery || hasStructuredQuery) {
        setRunInitialQuery(true);
      }
    };

    initializeQuery();
  }, [router.isReady, router.query, queryFromProps, logProvider, selectedCluster]);

  // FIX: New Effect to handle the initial run when state is fresh
  useEffect(() => {
    if (runInitialQuery && (logQuery || queryRequestFromProps)) {
      handleSubmit();
      setRunInitialQuery(false);
    }
  }, [runInitialQuery, logQuery, queryRequestFromProps, handleSubmit]);

  // 3. Polling Effect
  useEffect(() => {
    let intervalId: number;
    if (interval > 0) {
      intervalId = window.setInterval(() => {
        setPollLogs(true);
        setTime((prevTime: TimeRange) => ({
          ...prevTime,
          startTime:
            prevTime.shortcutClickTime > 0
              ? new Date().getTime() - prevTime.shortcutClickTime - interval * 1000
              : new Date().getTime() - interval * 1000,
          endTime: new Date().getTime(),
        }));
      }, interval * 1000);
    } else {
      setPollLogs(false);
    }
    return () => clearInterval(intervalId);
  }, [interval]);

  // 4. Trigger Fetch when Time Changes (if polling or manual date change)
  useEffect(() => {
    if (logProvider && (pollLogs || time.startTime !== dateTime.startTime)) {
      if (logQuery || queryRequestFromProps) {
        handleSubmit();
      }
    }
  }, [time, pollLogs, logProvider]);

  const onDateTimeRangeChange = (selectedDateTime: any) => {
    if (selectedDateTime?.shortcutClickTime > 0) {
      setTime({
        ...selectedDateTime,
        startTime: new Date().getTime() - selectedDateTime.shortcutClickTime,
        endTime: new Date().getTime(),
      });
    } else {
      setTime(selectedDateTime);
    }
    setPollLogs(false);
  };

  const handleEditorChange = (_event: any, value: string | null) => {
    if (value !== null) {
      setQLEditor(value);
    }
  };

  useEffect(() => {
    if (llmQueryResponse) {
      const logData = safeJSONParse(llmQueryResponse);
      if (logData && logData.logs) {
        setRawLogs(logData.logs);
        fetchTicketsForLogs(logData.logs);
        formatLogResults(logData.logs);
      }
    }
  }, [llmQueryResponse, formatLogResults, fetchTicketsForLogs]);

  useEffect(() => {
    if (rawLogs.length > 0) {
      formatLogResults(rawLogs);
    }
  }, [ticketReferenceMap]);

  useEffect(() => {
    if (logProvider === 'loki') {
      setTabs((prevTabs) => {
        if (prevTabs.some((tab) => tab.key === 'log-plus-minus')) return prevTabs;
        return [...prevTabs, { text: '+/- Logs', value: 1, key: 'log-plus-minus' }];
      });
    }
  }, [logProvider]);

  if (!logProvider) {
    return <ConfigureWarning type={'logs'} />;
  }

  return (
    <div>
      <NubiChatSidebar
        isVisible={nubiSidebarVisible}
        onClose={() => setNubiSidebarVisible(false)}
        accountId={accountId}
        query={nubiQuery}
        context={{ type: 'cluster', data: { conversationId: nubiSessionId } }}
        apiMode='investigate'
        source='log_analysis'
        position='right'
        mode='overlay'
        width='500px'
      />

      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={(msg: any) => {
          handleTicketSuccess();
          fetchTicketsForLogs(rawLogs);
          return msg;
        }}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Investigate Log',
          description: getTicketDescription(ticketData),
          accountId: accountId,
        }}
        reference={{
          id: getTicketReferenceId(ticketData),
          type: 'kubernetes',
        }}
      />

      <BoxLayout2
        id='query-logs'
        heading=''
        dateTimeRange={{
          enabled: showDateFilter,
          onChange: onDateTimeRangeChange,
          passedSelectedDateTime: time,
        }}
        leftExtraOptions={[
          <Box
            key={'log-provider-info'}
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
              padding: '6px 12px',
              backgroundColor: 'rgba(0, 0, 0, 0.02)',
              borderRadius: '6px',
              border: '1px solid rgba(0, 0, 0, 0.08)',
              minWidth: 'fit-content',
            }}
          >
            <Text value='Log Provider:' sx={{ fontSize: '14px', fontWeight: 500, color: colors.text.greyDark, whiteSpace: 'nowrap' }} />
            <CloudProviderIcon cloud_provider={logProvider} width='20px' height='20px' />
            <Text
              value={snakeToTitleCase(logProvider)}
              sx={{ fontSize: '14px', fontWeight: 600, color: colors.text.secondary, whiteSpace: 'nowrap' }}
            />
          </Box>,

          showQueryTextBox && (
            <ToggleButtonGroup
              key='query-mode-toggle'
              color='primary'
              exclusive
              value={qLEditor}
              onChange={handleEditorChange}
              sx={{
                minHeight: 0,
                minWidth: 0,
                '& button': {
                  padding: '8px 16px',
                  lineHeight: '14px',
                  height: '34px',
                  fontSize: '12px',
                  color: colors.text.secondaryDark,
                  borderColor: colors.border.secondary,
                  borderWidth: 0.5,
                  '&:hover': { borderColor: colors.border.queryAutocomplete, borderWidth: 1 },
                  '&.Mui-selected': { color: '#3B82F6', borderColor: colors.border.quadrant },
                },
              }}
            >
              {logProvider !== 'datadog' && <ToggleButton value='build'>Builder</ToggleButton>}
              <ToggleButton value='code'>
                Code
                {logProvider === 'ES' && (
                  <CustomTooltip title='Uses Elasticsearch Query DSL to write and execute log queries.' placement='top'>
                    <Box sx={{ display: 'flex', alignItems: 'center', ml: 0.5 }}>
                      <SafeIcon src={infoIcon} alt='info' width={14} height={14} />
                    </Box>
                  </CustomTooltip>
                )}
              </ToggleButton>
              {(logProvider === 'loki' || logProvider === 'signoz') && <ToggleButton value='ai'>AI</ToggleButton>}
            </ToggleButtonGroup>
          ),
        ]}
        extraOptions={
          showPolling
            ? [
                <CustomDropdown
                  key={'log-limit-selector'}
                  options={[
                    { label: '50', value: 50 },
                    { label: '100', value: 100 },
                    { label: '200', value: 200 },
                    { label: '500', value: 500 },
                    { label: '1000', value: 1000 },
                  ]}
                  value={logLimit}
                  onChange={(e) => setLogLimit(Number(e.target.value))}
                  label='Limit'
                  inputVariant='outlined'
                  disableClearable={true}
                  customStyle={{ maxWidth: '90px', mb: 0.8 }}
                />,
                <Box key={'log-refresh-poll'} sx={{ width: 'fit-content' }}>
                  <RefreshSubmitButton
                    loading={pollLogs || loading}
                    onSubmit={() => {
                      handleSubmit(true); // FIX: Removed argument. Just passing fromOnSubmit flag.
                      setPollLogs(false);
                    }}
                    interval={interval}
                    setInterval={setInterval}
                    disabled={isAiLoading || (qLEditor === 'ai' && !logQuery)}
                  />
                </Box>,
                ...(logProvider !== 'ES' && qLEditor !== 'build'
                  ? [<UserHistoryButton key={'user-history-button'} accountId={accountId} module={`log_query_${logProvider?.toLowerCase()}`} />]
                  : []),
              ]
            : []
        }
        filterOptions={[]}
        sharingOptions={{
          sharing: { enabled: false, onClick: null },
          download: { enabled: true, onClick: () => ({ tableId: k8sLogs }) },
        }}
      >
        {showQueryTextBox && (
          <Box sx={{ display: 'flex', alignItems: 'flex-start', flexDirection: 'column', paddingTop: '6px' }}>
            <QueryModeSwitcher
              accountId={accountId}
              onQueryChange={(e: any) => {
                setLogQuery(e.query);
                if (e.index !== undefined) setEsIndex(e.index);
              }}
              logProvider={logProvider}
              operatorDescriptors={operatorDescriptors}
              params={{ ...time }}
              queryItems={logQueryItems}
              setQueryItems={setLogQueryItems}
              _queryOperations={logOperations}
              setQueryOperations={setLogOperations}
              setLlmQueryResponse={setLlmQueryResponse}
              setConversationId={setConversationId}
              qLEditor={qLEditor}
              setQLEditor={setQLEditor}
              allowMultipleQueries={false}
              onAiLoadingChange={setIsAiLoading}
              providerType={'logs'}
              initialEsIndex={esIndex}
            />
          </Box>
        )}
        <KubernetesTable2
          id={k8sLogs}
          totalRows={data.length}
          data={data}
          headers={['Date', { name: 'Message', width: '90%' }, '']}
          rowsPerPage={data.length}
          showExpandable={true}
          expandable={{ tabs: showPlusMinusTab ? tabs : tabs.filter((f) => f.value !== 1) }}
          loading={loading}
          errorMessage={errorMsg}
          onPageChange={undefined}
          onSortChange={undefined}
          stickyColumnIndex='3'
        />
      </BoxLayout2>
    </div>
  );
};

export default KubernetesLogs;
