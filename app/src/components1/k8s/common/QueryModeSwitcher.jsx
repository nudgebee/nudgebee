import { Box, Typography } from '@mui/material';
import { useEffect, useRef, useState } from 'react';
import PropTypes from 'prop-types';
import LogQueryBuilderAutocomplete from './LogQueryBuilderAutocomplete';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import CodeMirror, { EditorView } from '@uiw/react-codemirror';
import { placeholder as cmPlaceholder } from '@codemirror/view';
import { PromQLExtension } from '@prometheus-io/codemirror-promql';
import { linter } from '@codemirror/lint';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import { Textarea } from './TextArea';
import { normalizeLegacyOperator } from './operatorCatalog';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { snackbar } from '@components1/common/snackbarService';
import CustomButton from '@components1/common/NewCustomButton';
import { ArrowRightWhiteIcon } from '@assets';
import observability from '@api1/observability';
import { parseHttpResponseBodyMessage, safeJSONParse, snakeToTitleCase } from 'src/utils/common';
import cache from '@lib/cache';
import ShimmerLoading from '@components1/common/ShimmerLoading';
import { v4 as uuidv4 } from 'uuid';
import { useLlmAsyncPolling, extractQueryResultFromConversation } from '@hooks/useLlmAsyncPolling';
import { useTenantBranding } from '@hooks/useTenantBranding';

export const chipsToBlockLabelMatchers = (block) => {
  const labelMatchers = [];
  (block?.queryItems || []).forEach((item) => {
    if (!item?.label || !item?.value) return;
    labelMatchers.push({
      label: item.label,
      operator: normalizeLegacyOperator(item.operator),
      value: item.value,
    });
  });
  return labelMatchers;
};

/**
 * @param {{
 *   accountId?: string,
 *   params?: object,
 *   logProvider?: any,
 *   onQueryChange?: (query: any) => void,
 *   queryItems?: any[],
 *   setQueryItems?: (items: any[]) => void,
 *   setLlmQueryResponse?: (response: any) => void,
 *   setConversationId?: (id: string) => void,
 *   qLEditor?: any,
 *   setQLEditor?: (editor: any) => void,
 *   allowMultipleQueries?: boolean,
 *   onAiLoadingChange?: (loading: boolean) => void
 *   deleteDataOnQueryBlockDeletion?: (query_key: string) => void
 *   providerType?: string,
 *   initialEsIndex?: string,
 * }} props
 */

const QueryModeSwitcher = ({
  accountId,
  params,
  logProvider,
  operatorDescriptors,
  onQueryChange,
  queryItems = [],
  setQueryItems,
  _queryOperations = [],
  setQueryOperations,
  setLlmQueryResponse,
  setConversationId,
  qLEditor,
  setQLEditor,
  allowMultipleQueries = true,
  onAiLoadingChange,
  deleteDataOnQueryBlockDeletion = (_query_key) => {},
  providerType = '',
  initialQuery = '',
  initialEsIndex = '',
}) => {
  const { assistantName } = useTenantBranding();
  const [mounted, setMounted] = useState(false);
  const [internalQLEditor, setInternalQLEditor] = useState('code');
  const currentQLEditor = qLEditor !== undefined ? qLEditor : internalQLEditor;
  const setCurrentQLEditor = setQLEditor || setInternalQLEditor;
  const [query, setQuery] = useState('');
  const codeQueryRef = useRef('');
  const [generateQuestionText, setGenerateQuestionText] = useState('');
  const [isLoadingGenerateQuestionText, setIsLoadingGenerateQuestionText] = useState(false);
  const [helperTextForLLM, setHelperTextForLLM] = useState('');
  const { startPolling } = useLlmAsyncPolling({ accountId });
  const [metricsList, setMetricsList] = useState([]);
  const [esIndexList, setEsIndexList] = useState([]);
  const [isEsIndexLoading, setIsEsIndexLoading] = useState(false);

  useEffect(() => {
    setMounted(true);
  }, []);
  const [prebuildQueryBlocks, setPrebuildQueryBlocks] = useState([
    {
      id: 0,
      query_key: uuidv4(),
      selectedMetric: '',
      queryItems: [],
      operations: [],
      aggregator: 'avg',
      aggregatorBy: [],
    },
  ]);

  const getSampleQuery = (provider, type) => {
    const isMetric = type === 'metric' || type === 'metrics';
    switch (provider) {
      case 'loki':
        return 'Example: {job="api-server"} |= "error" | json | status >= 500';
      case 'prometheus':
      case 'chronosphere':
      case 'victoria-metrics':
        return 'Example: rate(http_requests_total{status="500", job="api-server"}[5m])';
      case 'signoz':
        return isMetric
          ? 'Example: sum(rate(signoz_calls_total{service_name="api-server",http_status_code="500"}[5m])) by (service_name)'
          : 'Example: service.name = my-api AND status >= 500';
      case 'datadog':
        return isMetric
          ? 'Example: avg:system.cpu.user{service:web-app,env:prod} by {host}.rollup(avg, 60)'
          : 'Example: service:web-app status:error @http.status_code:5*';
      case 'newrelic':
        return isMetric
          ? "Example: SELECT average(cpuPercent), max(memoryUsedPercent) FROM SystemSample WHERE appName = 'api-server' TIMESERIES SINCE 1 hour ago"
          : "Example: SELECT count(*) FROM Log WHERE service = 'api' AND level = 'error' SINCE 1 hour ago";
      case 'dynatrace':
        return isMetric
          ? 'Example: builtin:service.response.time:avg:auto:sort(value(avg,descending)):limit(10):names'
          : 'Example: fetch logs | filter loglevel == "ERROR" AND dt.entity.service == "my-service"';
      case 'ES':
        return 'Example: {"query":{"bool":{"must":[{"match":{"level":"error"}},{"range":{"@timestamp":{"gte":"now-1h"}}}]}}}';
      case 'loggly':
        return 'Example: tag:production AND level:error AND service:"api-server"';
      case 'observe':
        return isMetric
          ? 'Example: metric cpu_usage_seconds_total | filter host = "web-01" | aggregate sum(value), group_by(job)'
          : 'Example: filter service = "api-server" | filter level = "error"';
      case 'azure_app_insights':
        return isMetric
          ? 'Example: customMetrics | where name == "RequestDuration" | summarize avg(value) by bin(timestamp, 5m), cloud_RoleName'
          : 'Example: traces | where severityLevel >= 3 | where cloud_RoleName == "my-service"';
      default:
        return 'Type your query here...';
    }
  };

  const extensions = [EditorView.lineWrapping, cmPlaceholder(getSampleQuery(logProvider, providerType))];

  const resetStates = () => {
    setQuery('');
    setHelperTextForLLM('');
    setGenerateQuestionText('');
    setIsLoadingGenerateQuestionText(false);
    setQueryItems([]);
    if (onQueryChange) {
      onQueryChange({ query: '', queryKeys: [''] });
    }
  };

  useEffect(() => {
    if (initialQuery) {
      codeQueryRef.current = initialQuery;
      setQuery(initialQuery);
    }
  }, [initialQuery]);

  useEffect(() => {
    const whichDefaultMode = logProvider == 'signoz' || logProvider == 'loggly' || logProvider == 'loki' ? 'build' : 'code';
    setCurrentQLEditor(whichDefaultMode);
  }, [logProvider]);

  useEffect(() => {
    resetStates();
  }, [accountId]);

  const handleFetchMetricsFormattedQuery = async (blocks) => {
    const blocksWithMetrics = blocks.filter((b) => b.selectedMetric);
    if (!blocksWithMetrics.length) {
      setQuery(codeQueryRef.current || '');
      return;
    }

    setQuery('');

    try {
      const queryItems = {};
      blocksWithMetrics.forEach((block) => {
        queryItems[block.query_key] = {
          metric: block.selectedMetric,
          label_matchers: chipsToBlockLabelMatchers(block),
        };
      });

      const response = await observability.getMetricsQuery({
        account_id: accountId,
        query_items: queryItems,
        start_time: params?.startDate,
        end_time: params?.endDate,
        instant: false,
      });
      if (response?.data?.errors) {
        snackbar.error(`Failed to format query - ${parseHttpResponseBodyMessage(response?.data)}`);
        return;
      }

      const resultsMap = response?.data?.data?.metrics_get_query?.results || {};
      const queryKeys = blocksWithMetrics.map((b) => b.query_key);
      const formattedQuery = queryKeys
        .map((key) => resultsMap[key])
        .filter(Boolean)
        .join(';');
      if (formattedQuery) {
        setQuery(formattedQuery);
        if (onQueryChange) {
          onQueryChange({ query: formattedQuery, queryKeys });
        }
      }
    } catch (err) {
      snackbar.error(`Failed to format query - ${err.message}`);
    }
  };

  useEffect(() => {
    if (currentQLEditor === 'ai') {
      setQuery('');
    } else if (currentQLEditor === 'code') {
      if (providerType === 'metrics') {
        handleFetchMetricsFormattedQuery(prebuildQueryBlocks);
      } else {
        const allQueryItems = prebuildQueryBlocks.flatMap((block) => block.queryItems || []);
        const allOperations = prebuildQueryBlocks.flatMap((block) => block.operations || []);
        handleFetchFormattedQuery(allQueryItems, allOperations);
      }
    }
  }, [currentQLEditor]);

  // Sync operations back to parent when prebuildQueryBlocks changes
  useEffect(() => {
    if (setQueryOperations && prebuildQueryBlocks.length > 0) {
      const allOperations = prebuildQueryBlocks.flatMap((block) => block.operations || []);
      setQueryOperations(allOperations);
    }
  }, [prebuildQueryBlocks, setQueryOperations]);

  const operatorMap = {
    '=': '_eq',
    '!=': '_neq',
    CONTAINS: '_contains',
    'NOT CONTAINS': '_not_contains',
    LIKE: '_like',
    'NOT LIKE': '_nlike',
    ILIKE: '_ilike',
    '>': '_gt',
    '>=': '_gte',
    '<': '_lt',
    '<=': '_lte',
    IN: '_in',
    'NOT IN': '_nin',
    EXISTS: '_exists',
    'NOT EXISTS': '_not_exists',
    REGEXP: '_regexp',
    'NOT REGEXP': '_not_regexp',
    BETWEEN: '_between',
    'NOT BETWEEN': '_not_between',
    // LogQueryBuilderAutocomplete normalizes chip.operator via operatorCatalog
    // before the chip reaches this component, so item.operator can already be a
    // backend token. Accept those identity-style to avoid collapsing to _eq.
    _eq: '_eq',
    _neq: '_neq',
    _lt: '_lt',
    _lte: '_lte',
    _gt: '_gt',
    _gte: '_gte',
    _regex: '_regex',
    _nregex: '_nregex',
    _contains: '_contains',
    _nlike: '_nlike',
    _icontains: '_icontains',
    _like: '_like',
    _ilike: '_ilike',
    _in: '_in',
    _not_in: '_not_in',
    _has_key: '_has_key',
    _is_null: '_is_null',
    _between: '_between',
  };

  const lineOperatorMap = {
    CONTAINS: '_contains',
    'NOT CONTAINS': '_nlike',
    ICONTAINS: '_icontains',
    'NOT ICONTAINS': '_nlike',
    LIKE: '_like',
    ILIKE: '_ilike',
    'NOT LIKE': '_nlike',
    REGEX: '_regex',
    'NOT REGEX': '_nregex',
    _contains: '_contains',
    _icontains: '_icontains',
    _nicontains: '_nlike',
    _like: '_like',
    _ilike: '_ilike',
    _nlike: '_nlike',
    _regex: '_regex',
    _nregex: '_nregex',
  };

  const handleFetchFormattedQuery = async (itemsToUse, operationsToUse = []) => {
    const validOperations = (operationsToUse || []).filter((op) => op?.value && op.value.trim() !== '');
    const hasItems = Array.isArray(itemsToUse) && itemsToUse.length > 0;
    const hasOperations = validOperations.length > 0;

    if (!hasItems && !hasOperations) {
      setQuery(codeQueryRef.current || '');
      return;
    }

    setQuery('');

    try {
      const itemClauses = (itemsToUse || []).map((item) => {
        const apiOperator = operatorMap[item.operator] || '_eq';
        return {
          _binary: {
            [item.label]: {
              [apiOperator]: item.value,
            },
          },
        };
      });

      const operationClauses = [];
      validOperations.forEach((op) => {
        const apiOperator = lineOperatorMap[op.op];
        if (apiOperator) {
          operationClauses.push({
            _binary: {
              content: {
                [apiOperator]: op.value,
              },
            },
          });
        } else {
          console.warn(`Unsupported line operation operator: ${op.op}`);
        }
      });

      const whereClause = {
        _and: [...itemClauses, ...operationClauses],
      };

      const response = await observability.getFormattedQuery({
        account_id: accountId,
        query_request: {
          where: whereClause,
        },
      });

      if (response?.data?.errors) {
        snackbar.error(`Failed to format query - ${parseHttpResponseBodyMessage(response?.data)}`);
        return;
      }

      const formattedQuery = response?.data?.data?.logs_get_query?.query;

      if (formattedQuery) {
        const key = uuidv4();
        setQuery(formattedQuery);
        if (onQueryChange) {
          onQueryChange({ query: formattedQuery, queryKeys: [key] });
        }
      }
    } catch (err) {
      snackbar.error(`Failed to format query - ${err.message}`);
    }
  };

  useEffect(() => {
    if (logProvider == 'prometheus' || logProvider == 'chronosphere' || logProvider == 'victoria-metrics') {
      const fetchMetrics = async () => {
        try {
          const cachedPrometheusLabels = cache.getWithSuffix(`${accountId}.prometheus.labels`, null, {});
          if (cachedPrometheusLabels) {
            setMetricsList(cachedPrometheusLabels);
          } else {
            setMetricsList([]);
            const res = await observability.metricsList(accountId);
            if (res?.errors) {
              snackbar.error(`failed to fetch labels- ${parseHttpResponseBodyMessage(res)}`);
              return;
            }
            const metricsList = res?.data?.data?.metrics_list?.map((m) => m.metric) || [];
            if (metricsList.length) {
              cache.setWithSuffix(`${accountId}.prometheus.labels`, metricsList, {}, 60 * 60 * 6);
              setMetricsList(metricsList);
            }
          }
        } catch (err) {
          snackbar.error(`Unexpected error fetching metrics: ${String(err)}`);
        }
      };
      fetchMetrics();
    } else if (logProvider == 'ES') {
      const fetchEsIndexes = async () => {
        setIsEsIndexLoading(true);
        try {
          const cachedESIndexes = cache.getWithSuffix(`${accountId}.es.indexes`, null, {});
          if (cachedESIndexes) {
            setEsIndexList(cachedESIndexes);
          } else {
            setEsIndexList([]);
            const res = await observability.fetchLogLabels({
              account_id: accountId,
            });
            if (res?.errors) {
              snackbar.error(`failed to fetch indexes - ${parseHttpResponseBodyMessage(res)}`);
              return;
            }
            const indexList = res?.data?.data?.logs_list_labels?.map((m) => m.label) || [];
            if (indexList.length) {
              cache.setWithSuffix(`${accountId}.es.indexes`, indexList, {}, 60 * 60 * 6);
              setEsIndexList(indexList);
            }
          }
        } catch (err) {
          snackbar.error(`Unexpected error fetching indexes: ${String(err)}`);
        } finally {
          setIsEsIndexLoading(false);
        }
      };
      fetchEsIndexes();
    }
  }, [logProvider, accountId]);

  // Derive selected ES index from the first query block (shared between Build and Code tabs)
  const selectedEsIndex = prebuildQueryBlocks[0]?.selectedMetric || '';

  // Reset selected ES index when provider changes away from ES
  useEffect(() => {
    if (logProvider !== 'ES' && prebuildQueryBlocks[0]?.selectedMetric) {
      setPrebuildQueryBlocks((prev) => prev.map((b, i) => (i === 0 ? { ...b, selectedMetric: '' } : b)));
    }
  }, [logProvider]);

  // Sync selected ES index to the externally-controlled value (integration-
  // configured default from the parent, or the user's last pick round-tripped
  // through onQueryChange). Keeps the dropdown honest on account switch.
  useEffect(() => {
    if (logProvider !== 'ES') {
      return;
    }
    if (prebuildQueryBlocks[0]?.selectedMetric === initialEsIndex) {
      return;
    }
    setPrebuildQueryBlocks((prev) => prev.map((b, i) => (i === 0 ? { ...b, selectedMetric: initialEsIndex } : b)));
  }, [logProvider, initialEsIndex]);

  const getExtension = () => {
    if (logProvider == 'prometheus' || logProvider == 'chronosphere' || logProvider == 'victoria-metrics') {
      extensions.push(
        new PromQLExtension()
          .setComplete({
            remote: {
              cache: {
                initialMetricList: metricsList,
              },
              fetchFn: (url) => {
                const requestUrl = typeof url === 'string' ? url : url.url;
                if (
                  requestUrl.includes('api/v1/metadata') ||
                  requestUrl.includes('api/v1/series') ||
                  requestUrl.includes('api/v1/label/__name__/values')
                ) {
                  const mockResponse = new Response(JSON.stringify({}));
                  return Promise.resolve(mockResponse);
                }
                return fetch(url);
              },
            },
          })
          .activateCompletion(true)
          .asExtension()
      );
      extensions.push(
        linter(null, {
          tooltipFilter: (diagnostics) => {
            const uniqueMessages = new Map();
            const filtered = [];
            const addedKeys = new Set();

            for (const diagnostic of diagnostics) {
              const key = `${diagnostic.message}-${diagnostic.from}-${diagnostic.to}`;
              if (!uniqueMessages.has(diagnostic.message)) {
                uniqueMessages.set(diagnostic.message, true);
                filtered.push(diagnostic);
                addedKeys.add(key);
              } else if (!addedKeys.has(key)) {
                const existing = filtered.find((d) => d.message === diagnostic.message);
                if (!existing || existing.to < diagnostic.from || existing.from > diagnostic.to) {
                  filtered.push(diagnostic);
                  addedKeys.add(key);
                }
              }
            }
            return filtered;
          },
        })
      );
    }
    return extensions;
  };

  const sendConversationIdAndLLMResponseToParent = (conversationId, llmResponse) => {
    if (conversationId && setConversationId) {
      setConversationId(conversationId);
    }
    if (llmResponse && setLlmQueryResponse) {
      setLlmQueryResponse(llmResponse);
    }
  };

  const handleGenerateQuery = () => {
    setIsLoadingGenerateQuestionText(true);
    if (onAiLoadingChange) {
      onAiLoadingChange(true);
    }
    setQuery('');
    setHelperTextForLLM('');
    if (logProvider == 'loki') {
      apiAskNudgebee
        .askAiGenerateLokiQuery({
          account_id: accountId,
          query: generateQuestionText,
        })
        .then((res) => {
          const errors = res?.data?.errors || [];
          if (errors.length) {
            snackbar.error(`failed to get response ${parseHttpResponseBodyMessage(res?.data)}`);
            setIsLoadingGenerateQuestionText(false);
            if (onAiLoadingChange) {
              onAiLoadingChange(false);
            }
            return;
          }
          const data = res?.data?.data?.ai_generate_loki_query?.data;
          const sessionId = data?.session_id;
          if (sessionId) {
            startPolling(sessionId, (conv) => {
              setIsLoadingGenerateQuestionText(false);
              if (onAiLoadingChange) {
                onAiLoadingChange(false);
              }
              if (conv.status === 'COMPLETED') {
                const result = extractQueryResultFromConversation(conv);
                if (result) {
                  const queryData = safeJSONParse(result.response);
                  if (queryData) {
                    const queries = Object.keys(queryData);
                    if (queries.length > 0) {
                      const key = uuidv4();
                      setQuery(queries[0]);
                      if (onQueryChange) {
                        onQueryChange({ query: queries[0], queryKeys: [key] });
                      }
                      sendConversationIdAndLLMResponseToParent(result.conversationId, queryData[queries[0]]);
                    }
                  }
                }
              } else {
                snackbar.error('Query generation failed');
              }
            });
          } else {
            const query = data?.response[0] ?? '{}';
            const queryData = safeJSONParse(query);
            if (queryData) {
              const queries = Object.keys(queryData);
              if (queries.length > 0) {
                const key = uuidv4();
                setQuery(queries[0]);
                if (onQueryChange) {
                  onQueryChange({ query: queries[0], queryKeys: [key] });
                }
                sendConversationIdAndLLMResponseToParent(data?.conversation_id ?? '', queryData[queries[0]]);
              }
            }
            setIsLoadingGenerateQuestionText(false);
            if (onAiLoadingChange) {
              onAiLoadingChange(false);
            }
          }
        })
        .catch(() => {
          setIsLoadingGenerateQuestionText(false);
          if (onAiLoadingChange) {
            onAiLoadingChange(false);
          }
        });
    } else if (logProvider == 'prometheus' || logProvider == 'chronosphere' || logProvider == 'victoria-metrics') {
      apiAskNudgebee
        .askNudgebeeAiGeneratePrometheusQuery({
          account_id: accountId,
          query: generateQuestionText,
        })
        .then((res) => {
          const errors = res?.data?.errors || [];
          if (errors.length) {
            snackbar.error(`failed to get response ${parseHttpResponseBodyMessage(res?.data)}`);
            setIsLoadingGenerateQuestionText(false);
            if (onAiLoadingChange) {
              onAiLoadingChange(false);
            }
            return;
          }
          const data = res?.data?.data?.ai_generate_prometheus_query?.data;
          const sessionId = data?.session_id;
          if (sessionId) {
            startPolling(sessionId, (conv) => {
              setIsLoadingGenerateQuestionText(false);
              if (onAiLoadingChange) {
                onAiLoadingChange(false);
              }
              if (conv.status === 'COMPLETED') {
                const result = extractQueryResultFromConversation(conv);
                if (result) {
                  const queryData = safeJSONParse(result.response);
                  if (queryData) {
                    const queries = Object.keys(queryData);
                    if (queries.length > 0) {
                      const key = uuidv4();
                      setQuery(queries[0]);
                      if (onQueryChange) {
                        onQueryChange({ query: queries[0], queryKeys: [key] });
                      }
                      sendConversationIdAndLLMResponseToParent(result.conversationId, queryData[queries[0]]);
                    }
                  }
                }
              } else {
                snackbar.error('Query generation failed');
              }
            });
          } else {
            const query = data?.response[0] ?? '{}';
            const queryData = safeJSONParse(query);
            if (queryData) {
              const queries = Object.keys(queryData);
              if (queries.length > 0) {
                const key = uuidv4();
                setQuery(queries[0]);
                if (onQueryChange) {
                  onQueryChange({ query: queries[0], queryKeys: [key] });
                }
                sendConversationIdAndLLMResponseToParent(data?.conversation_id ?? '', queryData[queries[0]]);
              }
            }
            setIsLoadingGenerateQuestionText(false);
            if (onAiLoadingChange) {
              onAiLoadingChange(false);
            }
          }
        })
        .catch(() => {
          setIsLoadingGenerateQuestionText(false);
          if (onAiLoadingChange) {
            onAiLoadingChange(false);
          }
        });
    } else {
      snackbar.error(`${logProvider} is not supported`);
      setIsLoadingGenerateQuestionText(false);
      if (onAiLoadingChange) {
        onAiLoadingChange(false);
      }
    }
  };

  const getPlaceholder = (type) => {
    switch (type) {
      case 'ES':
        return 'Elasticsearch DSL';
      case 'loki':
        return 'Loki';
      default:
        return snakeToTitleCase(type);
    }
  };

  const renderSwitchMode = () => {
    if (currentQLEditor === 'build') {
      return (
        <LogQueryBuilderAutocomplete
          accountId={accountId}
          logProvider={logProvider}
          operatorDescriptors={operatorDescriptors}
          queryItems={queryItems}
          params={params}
          onQueryChange={(e) => {
            setQuery(e.query);
            if (onQueryChange) {
              onQueryChange(e);
            }
          }}
          onQueryItemsChange={setQueryItems}
          allowMultipleQueries={allowMultipleQueries}
          deleteDataOnQueryBlockDeletion={deleteDataOnQueryBlockDeletion}
          prebuildQueryBlocks={prebuildQueryBlocks}
          setPrebuildQueryBlocks={setPrebuildQueryBlocks}
          providerType={providerType}
        />
      );
    } else if (currentQLEditor == 'code') {
      if (!mounted) return null;
      return (
        <Box sx={{ width: '100%', display: 'flex', flexDirection: 'column', gap: '8px', marginTop: '16px' }}>
          {logProvider === 'ES' && (
            <Box sx={{ width: '260px' }}>
              <FilterDropdownButton
                label='Select an Index'
                value={selectedEsIndex || null}
                options={esIndexList ?? []}
                disabled={esIndexList?.length === 0}
                onSelect={(_event, value) => {
                  setPrebuildQueryBlocks((prev) => prev.map((b, i) => (i === 0 ? { ...b, selectedMetric: value || '' } : b)));
                  if (onQueryChange) {
                    onQueryChange({ query: codeQueryRef.current || query, queryKeys: [''], index: value || '' });
                  }
                }}
                isOptionsLoading={isEsIndexLoading}
              />
            </Box>
          )}
          <CodeMirror
            style={{
              border: '1px solid black',
              background: '#282C34',
              overflow: 'auto',
              padding: '0px',
              borderRadius: '6px',
              width: '100%',
              height: '200px',
            }}
            value={query}
            width={'100%'}
            height='auto'
            theme='dark'
            editable={true}
            aria-expanded={true}
            extensions={getExtension()}
            onChange={(e) => {
              codeQueryRef.current = e;
              setQuery(e);
              if (onQueryChange) {
                onQueryChange({
                  query: e,
                  queryKeys: [''],
                  ...(logProvider === 'ES' ? { index: selectedEsIndex } : {}),
                });
              }
            }}
          />
        </Box>
      );
    } else if (currentQLEditor == 'ai') {
      if (!mounted) return null;
      return (
        <>
          <Box display={'flex'} sx={{ alignItems: !helperTextForLLM ? 'center' : '' }} gap={'12px'} mb='10px' mt='16px' width='96%'>
            <Box display='flex' flexDirection='column' gap='4px' sx={{ width: '100%' }}>
              <SummaryBlock
                hideTitle
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  backgroundColor: '#FFFFFF',
                  borderRadius: '8px',
                  border: '1px solid #3B82F6 !important',
                  boxShadow: '0px 2px 7px 0px #3B82F60F, 0px 4px 6px -1px #3B82F61F',
                  padding: '12px 24px',
                  width: '100%',
                  justifyContent: 'space-between',
                  gap: '12px',
                  '& textarea': {
                    width: '100%',
                    border: '0px',
                    resize: 'none',
                    boxShadow: 'none',
                    padding: '0px',
                    '&:focus': {
                      boxShadow: 'none',
                    },
                    '&::placeholder': {
                      color: '#B9B9B9',
                      fontSize: '14px',
                      fontWeight: 400,
                    },
                    '&::-webkit-scrollbar': {
                      display: 'none',
                    },
                  },
                  '& .MuiOutlinedInput-notchedOutline': {
                    border: '0px !important',
                  },
                  '& button': {
                    padding: '0px 10px !important',
                  },
                }}
              >
                <Box sx={{ width: '100%' }}>
                  <Textarea
                    value={generateQuestionText}
                    placeholder={`Ask ${assistantName} to Generate ${getPlaceholder(logProvider)} Query`}
                    onChange={(e) => {
                      setGenerateQuestionText(e.target.value);
                    }}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' && e.shiftKey) {
                        e.preventDefault();
                        if (generateQuestionText) {
                          handleGenerateQuery();
                        }
                      }
                    }}
                    maxRows={4}
                    sx={{ width: '100%' }}
                    disabled={isLoadingGenerateQuestionText}
                  />
                  {helperTextForLLM && <Typography sx={{ color: 'red', fontSize: '14px' }}>{helperTextForLLM}</Typography>}
                </Box>
                <CustomButton
                  sx={{ marginTop: '2px', width: '50px' }}
                  startIcon={ArrowRightWhiteIcon}
                  size='Medium'
                  onClick={() => {
                    handleGenerateQuery();
                  }}
                  disabled={!generateQuestionText || isLoadingGenerateQuestionText}
                />
              </SummaryBlock>
            </Box>
          </Box>
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              gap: '12px',
              width: '100%',
            }}
          >
            <ShimmerLoading isLoading={isLoadingGenerateQuestionText} height={'180px'} width={'99%'}>
              <CodeMirror
                style={{
                  border: '1px solid black',
                  overflow: 'hidden',
                  padding: '0px',
                  borderRadius: '6px',
                  width: '100%',
                  background: '#282C34',
                  minHeight: '180px',
                  maxHeight: '260px',
                }}
                value={query}
                width={'100%'}
                height='auto'
                theme='dark'
                editable={true}
                aria-expanded={true}
                extensions={getExtension()}
                onChange={(e) => {
                  setQuery(e);
                  if (onQueryChange) {
                    onQueryChange({ query: e, queryKeys: [''] });
                  }
                }}
                key={'code-mirror-ai'}
              />
            </ShimmerLoading>
          </Box>
        </>
      );
    }
  };

  return <>{renderSwitchMode()}</>;
};

QueryModeSwitcher.propTypes = {
  accountId: PropTypes.string,
  params: PropTypes.object,
  logProvider: PropTypes.any,
  operatorDescriptors: PropTypes.arrayOf(
    PropTypes.shape({
      token: PropTypes.string.isRequired,
      chip_label: PropTypes.string,
      line_label: PropTypes.string,
      kinds: PropTypes.arrayOf(PropTypes.string).isRequired,
    })
  ),
  onQueryChange: PropTypes.func,
  queryItems: PropTypes.array,
  setQueryItems: PropTypes.func,
  _queryOperations: PropTypes.array,
  setQueryOperations: PropTypes.func,
  setLlmQueryResponse: PropTypes.func,
  setConversationId: PropTypes.func,
  qLEditor: PropTypes.string,
  setQLEditor: PropTypes.func,
  allowMultipleQueries: PropTypes.bool,
  onAiLoadingChange: PropTypes.func,
  deleteDataOnQueryBlockDeletion: PropTypes.func,
  providerType: PropTypes.string,
  initialQuery: PropTypes.string,
};

export default QueryModeSwitcher;
