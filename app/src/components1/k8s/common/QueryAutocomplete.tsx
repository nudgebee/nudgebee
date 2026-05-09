import React, { useCallback, useEffect, useState } from 'react';
import k8sApi from '@api1/kubernetes';
import CodeMirror from '@uiw/react-codemirror';
import { PromQLExtension } from '@prometheus-io/codemirror-promql';
import { Box, Grid, ToggleButton, ToggleButtonGroup, Typography } from '@mui/material';
import { linter } from '@codemirror/lint';
import QueryBuilder, { OperationBuilder, getLineOperators, IndexBuilder } from './QueryBuilder';
import { OperatorDescriptor } from './operatorCatalog';
import { Textarea } from './TextArea';
import apiAskNudgebee from '@api1/ask-nudgebee';
import CustomButton from '@components1/common/NewCustomButton';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';
import cache from '@lib/cache';
import { getYesterday } from '@lib/datetime';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import { ArrowRightWhiteIcon } from '@assets';
import apiKubernetes1 from '@api1/kubernetes1';
import { parseHttpResponseBodyMessage, safeJSONParse } from 'src/utils/common';
import { useLlmAsyncPolling, extractQueryResultFromConversation } from '@hooks/useLlmAsyncPolling';
import { useTenantBranding } from '@hooks/useTenantBranding';

type Keyable = { [key: string]: any };
interface QueryAutocompleteProps {
  accountId: string;
  query: string;
  qLBuilderOption: Keyable[];
  handleQLBuilder: any;
  callback: any;
  logProvider: string;
  operatorDescriptors?: OperatorDescriptor[];
  sendGenerateQuestionToParent?: any;
  setConversationId?: any;
  handleSubmit?: any;
  loading?: boolean;
  selectedDateRange?: any;
  fullWidth?: boolean;
}

const queryToObject = (str: string) => {
  const queryArr: Keyable = [];
  const qry = str.split(/[{}]+/);
  const regex = /(!=|=~|<|>|!~|=)|[\"*?\"]/g; // eslint-disable-line
  qry[1]?.split(/[,]+/).forEach((b) => {
    const d = b
      .trim()
      .split(regex)
      .reduce((a: any, x) => {
        if (x && x != '') {
          return typeof a === 'string' ? { label: a, operator: x } : { ...a, value: x };
        }
        return a;
      });
    d && d != '' && queryArr.push(d);
  });
  return queryArr;
};

const apilabelData: any = { labels: [] };
const QueryAutocomplete: React.FC<QueryAutocompleteProps> = ({
  query,
  qLBuilderOption,
  accountId,
  handleQLBuilder,
  callback,
  logProvider,
  operatorDescriptors,
  sendGenerateQuestionToParent,
  setConversationId,
  handleSubmit,
  loading = false,
  fullWidth = false,
  selectedDateRange = {
    startTime: getYesterday().getTime(),
    endTime: new Date().getTime(),
  },
}: QueryAutocompleteProps) => {
  const { assistantName } = useTenantBranding();
  const [qLOption, setQLOption] = useState<string[]>([]);
  const [qLEditor, setQLEditor] = useState<string>('code');
  const initialLineOperator = getLineOperators(operatorDescriptors)[0]?.value ?? '';
  const [lineContains, setLineContains] = useState<any[]>([{ operator: initialLineOperator, value: '' }]);
  const [index, setIndex] = useState('.*');
  const [generateQuestionText, setGenerateQuestionText] = useState('');
  const [isLoadingGenerateQuestionText, setIsLoadingGenerateQuestionText] = useState(false);
  const [helperTextForLLM, setHelperTextForLLM] = useState('');
  const [indices, setIndices] = useState<string[]>([]);
  const [query1, setQuery1] = useState<string | undefined>(query);
  const [llmQueryResponse, setLlmQueryResponse] = useState('');
  const { startPolling } = useLlmAsyncPolling({ accountId });

  useEffect(() => {
    setQuery1(query);
  }, [query]);

  useEffect(() => {
    let requestBody = null;
    if (logProvider === 'loki' && qLEditor == 'build') {
      requestBody = {
        no_sinks: true,
        body: {
          account_id: accountId,
          action_name: 'query_loki_labels',
          action_params: {
            query: `start=${selectedDateRange.startTime * 1000000}&end=${selectedDateRange.endTime * 1000000}`,
          },
        },
        cache: false,
      };
      k8sApi.relayForwardRequest(requestBody).then((res) => {
        if (res?.data?.success) {
          const labels = res?.data?.data?.data || [];
          if (labels && labels.length > 0) {
            setQLOption(labels);
            apilabelData.labels = labels;
          } else {
            setQLOption([]);
          }
        }
      });
    } else if (logProvider === 'ES' && qLEditor === 'build' && index) {
      requestBody = {
        no_sinks: true,
        body: {
          account_id: accountId,
          action_name: 'query_esindex_field',
          action_params: {
            index: index,
          },
        },
        cache: false,
      };
      k8sApi.relayForwardRequest(requestBody).then((res) => {
        if (res?.data?.success) {
          const response = res?.data?.data ?? {};
          if (response) {
            const indexedMappings = JSON.parse(response)?.fields ?? ['.*'];
            setQLOption(Object.keys(indexedMappings).sort((a, b) => a.localeCompare(b)));
            apilabelData.labels = Object.keys(indexedMappings).sort((a, b) => a.localeCompare(b));
          }
        }
      });
    } else if (logProvider == 'promql') {
      if (accountId == 'demo') {
        setQLOption([]);
        return;
      }
      const cachedPrometheusLabels = cache.getWithSuffix(`${accountId}.prometheus.labels`, null, {});
      if (!cachedPrometheusLabels) {
        setQLOption([]);
        apiKubernetes1.metricsList(accountId).then((res) => {
          if (res?.errors) {
            snackbar.error(`failed to fetch labels- ${parseHttpResponseBodyMessage(res)}`);
            return;
          }
          const metricsList = res?.data?.data?.metrics_list?.map((m: any) => m.metric) || [];
          if (metricsList.length) {
            cache.setWithSuffix(`${accountId}.prometheus.labels`, metricsList, {}, 60 * 60 * 6);
            setQLOption(metricsList);
          }
        });
      }
    } else {
      return;
    }
  }, [accountId, index, logProvider, qLEditor]);

  useEffect(() => {
    if (logProvider == 'ES') {
      const requestBody = {
        no_sinks: true,
        body: {
          account_id: accountId,
          action_name: 'query_es_indices',
          action_params: {},
        },
        cache: false,
      };
      k8sApi.relayForwardRequest(requestBody).then((res) => {
        if (res?.data?.success) {
          const labels = res?.data?.data || ['.*'];
          if (labels) {
            setIndices(Object.keys(JSON.parse(labels)).sort());
          } else {
            setIndices(['.*']);
          }
        }
      });
    }
  }, [accountId, logProvider, qLEditor]);

  const fetchValueByLabel = (label: string, callback: any) => {
    if (logProvider === 'ES') {
      return false;
    }
    const requestBody = {
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'query_grafana_loki_label_values',
        action_params: {
          query: `start=${selectedDateRange.startTime * 1000000}&end=${selectedDateRange.endTime * 1000000}`,
          label: label,
        },
      },
      cache: false,
    };
    k8sApi.relayForwardRequest(requestBody).then((res) => {
      if (res?.data?.success) {
        const valueSet = res?.data?.data?.data || [];
        if (valueSet && valueSet.length > 0) {
          callback(valueSet);
        } else {
          callback(['']);
        }
      }
    });
  };

  const handleChange = (e: any) => {
    const value = e.target.value;

    if (value === 'build' && logProvider === 'loki') {
      handleQLBuilder(queryToObject(query));
    } else if (value === 'ai') {
      setGenerateQuestionText('');
      sendGenerateQuestionToParent('');
    } else if (value === 'code') {
      setQuery1('');
    }

    if (logProvider === 'promql') {
      callback('', 'reset');
    } else if (logProvider === 'ES') {
      callback('');
      handleQLBuilder([{ label: '', operator: '=', value: undefined }]);
    }

    setHelperTextForLLM('');
    setQLEditor(value);
  };

  const getFilterOption = () => {
    if (logProvider == 'ES') {
      return false;
    }
    const arr: any = [...apilabelData.labels];
    qLBuilderOption.forEach((item) => arr.indexOf(item.label) > -1 && arr.splice(arr.indexOf(item.label), 1));
    setQLOption(arr);
  };

  const addFilterChange = (_e: any) => {
    const lastFilter = qLBuilderOption[qLBuilderOption.length - 1];
    if (lastFilter?.value && lastFilter?.label) {
      getFilterOption();
      handleQLBuilder([...qLBuilderOption, { label: '', operator: '=', value: undefined }]);
    }
  };

  const removeFilterChange = (index: number) => {
    const arr: any = qLBuilderOption;
    arr.splice(index, 1);
    getFilterOption();
    handleQLBuilder([...arr]);
  };

  const handleFilterValue = (value: string | null, index: number, type: string) => {
    const arr: any = qLBuilderOption;
    arr[index][type] = value;
    if (type == 'label') {
      handleFilterValue(null, index, 'value');
    }
    handleQLBuilder([...arr]);
  };

  useEffect(() => {
    const areAllLabelAndValueNull = qLBuilderOption.every((item) => item.label === null && item.value === null);
    if (!areAllLabelAndValueNull) {
      buildQuery();
    }
  }, [qLBuilderOption, lineContains]);

  const buildQuery = useCallback(() => {
    const b: string[] = [];
    if (logProvider === 'ES') {
      const esb = require('elastic-builder');
      const lc: any[] = [];
      qLBuilderOption?.forEach((item: any, _i) => {
        item.label && item?.value && lc.push(esb.matchQuery(item?.label, item?.value));
        item.label && b.push(`"${item.operator ?? 'match'}":{${item.label}:"${item.value ?? ''}"}`);
      });
      const requestBody = esb.boolQuery().should(lc);
      callback(JSON.stringify(requestBody.toJSON()));
      return requestBody.toJSON();
    }
    qLBuilderOption?.forEach((item: any, _i) => item.label && b.push(`${item.label}${item.operator ?? '='}"${item.value ?? ''}"`));
    const lc: string[] = [];
    lineContains.forEach((lcItem: any, _i) => lc.push(` ${lcItem.operator ?? ''}"${lcItem.value ?? ''}"`));
    callback(`{${b}}${lc.join('')}`);
    return b.join(',');
  }, [qLBuilderOption, lineContains]);

  const handleGenerateQuery = () => {
    setIsLoadingGenerateQuestionText(true);
    setQuery1('');
    setHelperTextForLLM('');
    sendGenerateQuestionToParent(generateQuestionText);
    const handleAsyncResult = (conv: any, processResult: (response: string, conversationId: string) => void) => {
      setIsLoadingGenerateQuestionText(false);
      if (conv.status === 'COMPLETED') {
        const result = extractQueryResultFromConversation(conv);
        if (result) {
          processResult(result.response, result.conversationId);
        }
      } else {
        snackbar.error('Query generation failed');
      }
    };

    if (logProvider == 'ES') {
      apiAskNudgebee
        .askNudgebeeAiGenerateESDsl({
          account_id: accountId,
          query: JSON.stringify({ query: generateQuestionText, index: index }),
        })
        .then((res) => {
          const data = res?.data?.data?.ai_generate_es_dsl_query?.data;
          const sessionId = data?.session_id;
          if (sessionId) {
            startPolling(sessionId, (conv: any) => {
              handleAsyncResult(conv, (response, conversationId) => {
                let isValidJson = false;
                try {
                  JSON.parse(response);
                  isValidJson = true;
                } catch (e) {
                  console.error('unable to parse query', e);
                }
                if (isValidJson) {
                  callback(response, 'ai');
                  setConversationId(conversationId);
                }
              });
            });
          } else {
            const query = data?.response[0] ?? '';
            let isValidJson = false;
            let parsedQuery: any = '';
            try {
              parsedQuery = JSON.parse(query);
              isValidJson = true;
            } catch {
              isValidJson = false;
            }
            if (!isValidJson) {
              setHelperTextForLLM(parsedQuery?.error);
            } else {
              callback(query, 'ai');
              setConversationId(data?.conversation_id ?? '');
            }
            setIsLoadingGenerateQuestionText(false);
          }
        })
        .catch(() => {
          setIsLoadingGenerateQuestionText(false);
        });
    } else if (logProvider == 'loki') {
      apiAskNudgebee
        .askAiGenerateLokiQuery({
          account_id: accountId,
          query: generateQuestionText,
        })
        .then((res) => {
          const data = res?.data?.data?.ai_generate_loki_query?.data;
          const sessionId = data?.session_id;
          if (sessionId) {
            startPolling(sessionId, (conv: any) => {
              handleAsyncResult(conv, (response, conversationId) => {
                let isValidJson = false;
                try {
                  JSON.parse(response);
                  isValidJson = true;
                } catch {
                  isValidJson = false;
                }
                if (isValidJson) {
                  const parsed = safeJSONParse(response);
                  setHelperTextForLLM(parsed?.error);
                } else {
                  callback(response, 'ai');
                  setConversationId(conversationId);
                  setQuery1(response);
                }
              });
            });
          } else {
            const query = data?.response[0] ?? '';
            let isValidJson = false;
            let parsedQuery: any = '';
            try {
              parsedQuery = JSON.parse(query);
              isValidJson = true;
            } catch {
              isValidJson = false;
            }
            if (isValidJson) {
              setHelperTextForLLM(parsedQuery?.error);
            } else {
              callback(query, 'ai');
              setConversationId(data?.conversation_id ?? '');
              setQuery1(query);
            }
            setIsLoadingGenerateQuestionText(false);
          }
        })
        .catch(() => {
          setIsLoadingGenerateQuestionText(false);
        });
    } else if (logProvider == 'promql') {
      apiAskNudgebee
        .askNudgebeeAiGeneratePrometheusQuery({
          account_id: accountId,
          query: generateQuestionText,
        })
        .then((res) => {
          const data = res?.data?.data?.ai_generate_prometheus_query?.data;
          const sessionId = data?.session_id;
          if (sessionId) {
            startPolling(sessionId, (conv: any) => {
              handleAsyncResult(conv, (response, conversationId) => {
                if (response.includes('error: ')) {
                  setHelperTextForLLM(response);
                } else {
                  setConversationId(conversationId);
                  setQuery1(response);
                  setLlmQueryResponse(response);
                }
              });
            });
          } else {
            const query = data?.response[0] ?? '';
            if (query.includes('error: ')) {
              setHelperTextForLLM(query);
            } else {
              setConversationId(data?.conversation_id ?? '');
              setQuery1(query);
              setLlmQueryResponse(query);
            }
            setIsLoadingGenerateQuestionText(false);
          }
        })
        .catch(() => {
          setIsLoadingGenerateQuestionText(false);
        });
    } else {
      snackbar.error(`${logProvider} is not supported`);
    }
  };

  const getExtension = (type: string) => {
    if (type == 'promql') {
      return [
        [
          new PromQLExtension()
            .setComplete({
              remote: {
                cache: {
                  initialMetricList: qLOption,
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
            .asExtension(),
        ],
        linter(null, {
          tooltipFilter: (diagnostics: readonly any[]) => {
            const uniqueMessages = new Map();
            const filtered: any[] = [];
            const addedKeys = new Set<string>();

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
        }),
      ];
    } else if (type == 'loki') {
      return [
        new PromQLExtension()
          .setComplete({
            remote: {
              httpMethod: 'GET',
              lookbackInterval: 12 * 60 * 60 * 1000,
              cache: {
                initialMetricList: qLOption,
              },
              fetchFn: (_input: RequestInfo, _init?: RequestInit) => {
                return new Promise(() => ({ data: [] }));
              },
            },
          })
          .activateCompletion(true)
          .asExtension(),
      ];
    }
    return [];
  };

  const getPlaceholder = (type: string) => {
    switch (type) {
      case 'ES':
        return 'Elasticsearch DSL';
      case 'loki':
        return 'Loki';
      default:
        return 'Prometheus';
    }
  };

  return (
    <Box sx={{ width: fullWidth ? '98%' : 'auto' }}>
      <ToggleButtonGroup
        color='primary'
        exclusive
        value={qLEditor}
        onChange={handleChange}
        sx={{
          minHeight: 0,
          minWidth: 0,
          marginBottom: '10px',
          '& button': {
            padding: '8px 16px',
            minHeight: 0,
            minWidth: 0,
            lineHeight: '14px',
            height: '34px',
            fontSize: '12px',
            color: colors.text.secondaryDark,
            fontWeight: 400,
            borderColor: colors.border.secondary,
            borderWidth: 0.5,
            backgroundColor: 'transparent',
            '&:hover': {
              borderColor: colors.border.queryAutocomplete,
              borderWidth: 1,
            },
            '&.Mui-selected': {
              backgroundColor: 'transparent !important',
              borderColor: colors.border.quadrant,
              borderWidth: '0.5px',
              color: '#3B82F6',
            },
            '&.selected': {
              fontWeight: 500,
              borderBottom: `2px solid ${colors.text.secondary}`,
              borderBottomLeftRadius: 0,
              borderBottomRightRadius: 0,
            },
          },
        }}
      >
        {logProvider != 'promql' ? <ToggleButton value='build'>Builder</ToggleButton> : null}
        <ToggleButton value='code'>Code</ToggleButton>
        <ToggleButton value='ai'>AI</ToggleButton>
      </ToggleButtonGroup>
      <br />
      <SummaryBlock
        hideTitle
        sx={{
          borderRadius: '4px',
          width: 'calc(100% - 8px)',
          padding: '16px',
          backgroundColor: 'transparent',
          border: '0.5px solid #D0D0D0 !important',
          mt: '5px',
        }}
      >
        {qLEditor === 'ai' && (
          <>
            <Box display={'flex'} sx={{ alignItems: !helperTextForLLM ? 'center' : '' }} gap={'12px'} mb='10px'>
              <Box display='flex' flexDirection='column' gap='4px' sx={{ width: 'calc(100% - 50px)' }}>
                {logProvider === 'ES' && (
                  <Box sx={{ marginBottom: '8px' }}>
                    <IndexBuilder
                      key={'es-index'}
                      indicesList={indices}
                      value={index}
                      callback={(e: any) => {
                        setIndex(e);
                        callback(e, 'es-index');
                      }}
                      showBorder={false}
                      showMargin={false}
                      showPadding={false}
                      width={292}
                      sx={{
                        paddingLeft: '0px',
                        '.MuiInputBase-root': {
                          '&::before,:hover': {
                            border: '0px !imoprtant',
                          },
                        },
                      }}
                    />
                  </Box>
                )}
                <SummaryBlock
                  hideTitle
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    backgroundColor: '#FFFFFF',
                    borderRadius: '8px',
                    border: '1px solid #3B82F6 !important',
                    boxShadow: '0px 2px 7px 0px #3B82F60F, 0px 4px 6px -1px #3B82F61F',
                    padding: '16px 24px',
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
                      onKeyDown={(e: React.KeyboardEvent<HTMLTextAreaElement>) => {
                        if (e.key === 'Enter' && e.shiftKey) {
                          e.preventDefault();
                          if (generateQuestionText && !(logProvider == 'ES' && !index)) {
                            handleGenerateQuery();
                          }
                        }
                      }}
                      maxRows={4}
                      disabled={logProvider == 'ES' && !index}
                      sx={{ width: '100%' }}
                    />
                    {helperTextForLLM && <Typography sx={{ color: 'red', fontSize: '14px' }}>{helperTextForLLM}</Typography>}
                  </Box>

                  <Box>
                    <CustomButton
                      loading={isLoadingGenerateQuestionText}
                      sx={{ marginTop: '2px' }}
                      size='Medium'
                      onClick={() => {
                        handleGenerateQuery();
                      }}
                      startIcon={ArrowRightWhiteIcon}
                      disabled={logProvider == 'ES' && !index}
                      showTooltip
                      toolTipTitle={logProvider == 'ES' && !index ? 'Please select the index first' : ''}
                      tooltipPlacement={'right'}
                    />
                  </Box>
                </SummaryBlock>
              </Box>
            </Box>
            <Box
              sx={{
                display: 'flex',
                flexDirection: 'column',
                gap: '12px',
              }}
            >
              <CodeMirror
                style={{
                  border: '1px solid black',
                  overflow: 'hidden',
                  padding: '0px',
                  borderRadius: '6px',
                  width: '100%',
                }}
                value={query1}
                width={fullWidth ? '100%' : '500px'}
                height='75px'
                theme='dark'
                editable={true}
                aria-expanded={true}
                extensions={getExtension(logProvider)}
                onChange={(e) => {
                  setQuery1(e);
                  if (logProvider != 'promql') {
                    callback(e, 'ai');
                  }
                }}
                key={'code-mirror-ai'}
              />
              {logProvider == 'promql' && (
                <CustomButton
                  id={'submit'}
                  text={'Submit'}
                  size='Small'
                  sx={{
                    width: 'fit-content',
                  }}
                  disabled={!query1 || loading}
                  onClick={() => {
                    handleSubmit(query1, llmQueryResponse, 'ai');
                  }}
                />
              )}
            </Box>
          </>
        )}
        {qLEditor === 'code' && (
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              gap: '12px',
            }}
          >
            {logProvider === 'ES' && (
              <IndexBuilder
                key={'es-index'}
                indicesList={indices}
                value={index}
                callback={(e: any) => {
                  callback(e, 'es-index');
                }}
                showBorder={false}
                showMargin={false}
                showPadding={false}
                width={292}
                sx={{
                  paddingLeft: '0px',
                  '.MuiInputBase-root': {
                    '&::before,:hover': {
                      border: '0px !imoprtant',
                    },
                  },
                }}
              />
            )}
            <CodeMirror
              style={{
                border: '1px solid black',
                overflow: 'hidden',
                padding: '0px',
                borderRadius: '6px',
                width: '100%',
              }}
              value={query}
              width={fullWidth ? '100%' : '500px'}
              height='75px'
              theme='dark'
              editable={true}
              aria-expanded={true}
              extensions={getExtension(logProvider)}
              onChange={(e) => {
                setQuery1(e);
                if (logProvider != 'promql') {
                  callback(e, 'code');
                }
              }}
            />
            {logProvider == 'promql' && (
              <CustomButton
                id={'submit'}
                text={'Submit'}
                disabled={!query1 || loading}
                size='Small'
                sx={{
                  width: 'fit-content',
                }}
                onClick={() => {
                  handleSubmit(query1, '', 'code');
                }}
              />
            )}
          </Box>
        )}
        {qLEditor == 'build' && logProvider != 'promql' && (
          <Box>
            {logProvider == 'ES' && (
              <IndexBuilder
                key={'es-index'}
                indicesList={indices}
                value={index}
                callback={(e: any) => {
                  setIndex(e);
                  callback(e, 'es-index');
                  handleQLBuilder([{ label: '', operator: '=', value: undefined }]);
                  callback('');
                }}
                showBorder={false}
                showMargin={false}
                showPadding={false}
                width={292}
                sx={{
                  paddingLeft: '0px',
                  '.MuiInputBase-root': {
                    '&::before,:hover': {
                      border: '0px !imoprtant',
                    },
                  },
                }}
              />
            )}

            <Box>
              {qLBuilderOption?.map((item: any, _i) => (
                <QueryBuilder
                  key={`query-builder-${item.label}`}
                  indexId={_i}
                  label={item?.label}
                  operator={item?.operator}
                  value={item?.value}
                  labelOption={qLOption}
                  removeFilter={qLBuilderOption.length <= 1}
                  logProvider={logProvider}
                  callback={{
                    addLabel: (e: any) => {
                      handleFilterValue(e.target.value, _i, 'label');
                    },
                    addOperator: (e: any) => {
                      handleFilterValue(e.target.value, _i, 'operator');
                    },
                    addValue: (e: any) => {
                      handleFilterValue(e.target.value, _i, 'value');
                    },
                    removeLabelFilter: removeFilterChange,
                    addLabelFilter: addFilterChange,
                    fetchValueByLabel: fetchValueByLabel,
                  }}
                />
              ))}
              <CustomButton
                variant={'tertiary'}
                text={'Add Filters'}
                onClick={(e) => {
                  addFilterChange(e);
                }}
                sx={{ mt: '10px', mr: '10px' }}
              />
            </Box>
            {(logProvider === 'loki' || logProvider === 'newrelic') && (
              <>
                <Box sx={{ mt: '5px', mb: '7px' }}>
                  <CustomButton
                    variant='tertiary'
                    text={'+ OPERATIONS'}
                    onClick={() => {
                      const lastLC = lineContains[lineContains.length - 1];
                      if (lastLC?.value != '') {
                        setLineContains([...lineContains, { operator: getLineOperators(operatorDescriptors)[0]?.value ?? '', value: '' }]);
                      }
                    }}
                  />
                </Box>
                <Box marginTop={'10px'}>
                  <Grid container>
                    <Grid item md={3} mb='12px'>
                      {lineContains.map((lc, index) => (
                        <OperationBuilder
                          showMargin={false}
                          showPadding={false}
                          key={`lineContains-${lc.operator}`}
                          index={index}
                          lineContains={lineContains}
                          removeFilter={lineContains.length <= 1}
                          operatorDescriptors={operatorDescriptors}
                          showBorder={false}
                          callback={{
                            addValue: (e: any, i: number) => {
                              const tmpArr = [...lineContains];
                              tmpArr[i].value = e.target?.value;
                              setLineContains(tmpArr);
                            },
                            addOperator: (e: any) => {
                              const tmpArr = [...lineContains];
                              tmpArr[index].operator = e.target?.value;
                              setLineContains(tmpArr);
                            },
                            removeLabelFilter: (i: number) => {
                              const arr = lineContains;
                              arr.splice(i, 1);
                              setLineContains([...arr]);
                            },
                          }}
                        />
                      ))}
                    </Grid>
                  </Grid>
                </Box>
              </>
            )}
            <CodeMirror
              value={query}
              width='100%'
              height='75px'
              theme='dark'
              style={{
                border: '1px solid black',
                overflow: 'hidden',
                padding: '0px',
                borderRadius: '6px',
                width: '100%',
                marginTop: logProvider === 'ES' ? '5px' : '',
              }}
              editable={false}
              aria-expanded={true}
            />
          </Box>
        )}
      </SummaryBlock>
    </Box>
  );
};

export default QueryAutocomplete;
