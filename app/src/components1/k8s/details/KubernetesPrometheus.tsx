import React, { useEffect, useState } from 'react';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import { Box, Typography, ToggleButtonGroup, ToggleButton } from '@mui/material';
import { Switch } from '@components1/ds/Switch';
import { Divider } from '@components1/ds/Divider';
import { Checkbox } from '@components1/ds/Checkbox';
import ListingLayout from '@components1/ds/ListingLayout';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@common-new/DownloadButton';
import { convertNumberToTimestamp, isAtMost70PercentDifferent } from 'src/utils/common';
import { useRouter } from 'next/router';
import LineChart from '@common/charts/LineCharts';
import Text from '@common-new/format/Text';
import Title from '@components1/common/Title';
import Grid from '@mui/material/Grid';
import UserHistoryButton from '@components1/common/UserHistory';
import apiKubernetes1 from '@api1/kubernetes1';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { v4 as uuidv4 } from 'uuid';
import { Skeleton } from '@components1/ds/Skeleton';
import { toast as snackbar } from '@components1/ds/Toast';
import Tooltip from '@components1/ds/Tooltip';
import QueryModeSwitcher from '@components1/k8s/common/QueryModeSwitcher';
import { Button as DsButton } from '@components1/ds/Button';
import { ds } from '@utils/colors';

interface KubernetesPrometheusProps {
  accountId: string;
  showDrilldown: boolean;
  chartView: boolean;
  showExtraOptions: boolean;
  showQueryBox: boolean;
  preparedEvidences?: any[];
  showDateTime?: boolean;
  queriesToExecute?: Array<{ key: string; query: string; title?: string }>;
  dateTime?: {
    startTime: number;
    endTime: number;
  };
}

interface Header {
  name: string;
  width: string;
  component?: any;
}

const KubernetesPrometheus: React.FC<KubernetesPrometheusProps> = ({
  accountId,
  showDrilldown = true,
  chartView = true,
  showExtraOptions = true,
  showQueryBox = true,
  preparedEvidences = [],
  showDateTime = true,
  queriesToExecute = [],
  dateTime = {
    startTime: 0,
    endTime: 0,
  },
}) => {
  const router = useRouter();
  const k8sProm = 'k8sProm';
  const startDate = new Date(new Date().getTime() - 60 * 60 * 1000);

  const [data, setData] = useState([]);
  const [errorMsg, setErrorMsg] = useState('');
  const [loading, setLoading] = useState(false);
  const [chartData, setChartData] = useState<any>([]);
  const [selectedDateRange, setSelectedDateRange] = useState<any>({
    startDate: dateTime.startTime > 0 ? dateTime.startTime : startDate.getTime(),
    endDate: dateTime.endTime > 0 ? dateTime.endTime : new Date().getTime(),
  });
  const [showChartView, setShowChartView] = useState<boolean>(chartView);
  const [conversationId, setConversationId] = useState('');
  const [query, setQuery] = useState('');
  const [llmQueryResponse, setLlmQueryResponse] = useState('');
  const [instant, setInstant] = useState(false);
  const [promqlItems, setPromqlItems] = useState<Array<{ key: string; query: string; title?: string }>>([]);
  const [generateQuestionText, _setGenerateQuestionText] = useState('');
  const [qLEditor, setQLEditor] = useState('code');
  const [isAiLoading, setIsAiLoading] = useState(false);

  useEffect(() => {
    if (router?.query?.startDate && router?.query?.endDate) {
      setSelectedDateRange({
        startDate: router?.query?.startDate,
        endDate: router?.query?.endDate,
      });
    }
  }, [router.query.startDate, router.query.endDate]);

  useEffect(() => {
    if (selectedDateRange.startDate && selectedDateRange.endDate) {
      handleSubmit(query, llmQueryResponse, '', queriesToExecute);
    }
  }, [selectedDateRange.startDate, selectedDateRange.endDate, instant]);

  const getObjectWithMaxKeys = (data: any) => {
    const metricsObjects = data?.filter((obj: any) => 'metric' in obj).map((j: any) => j.metric);
    const objectWithMaxKeys = metricsObjects.reduce((maxObj: any, currentObj: any) => {
      const maxObjKeys = Object.keys(maxObj).length;
      const currentObjKeys = Object.keys(currentObj).length;

      if (currentObjKeys > maxObjKeys) {
        return currentObj;
      }
      return maxObj;
    }, {});
    return objectWithMaxKeys;
  };

  const aiCreateFeedback = async (createFeedback: boolean, promqlQuery: string, llmQueryResponse: string) => {
    if ((llmQueryResponse != promqlQuery && isAtMost70PercentDifferent(llmQueryResponse, promqlQuery)) || createFeedback) {
      await apiAskNudgebee.createAiFeedback({
        session_id: uuidv4(),
        module: 'prometheus',
        question: generateQuestionText,
        llm_response: llmQueryResponse,
        user_corrected_response: promqlQuery,
        additional_notes: 'User did correction to the response',
        conversation_id: conversationId,
        cloud_account_id: accountId,
        useful: true,
      });
    }
  };

  useEffect(() => {
    if (preparedEvidences && preparedEvidences.length) {
      setLoading(true);
      setErrorMsg('');
      setData([]);
      setChartData([]);
      const queries: { [key: string]: string } = {};
      const evidences: { [key: number]: any } = {};
      preparedEvidences.forEach((evidence, index) => {
        const evidenceData = evidence?.data;
        if (evidenceData) {
          queries[index] = evidence?.metadata?.query || '';
          evidences[index] = evidenceData;
        }
      });
      const getQueryByKey = (
        key: string
      ): {
        query: string;
        title: string;
      } => {
        const value = queries[key];
        if (value === undefined || value === null) {
          return {
            query: '',
            title: '',
          };
        }
        return typeof value === 'string'
          ? { query: value, title: '' }
          : {
              query: JSON.stringify(value),
              title: '',
            };
      };

      if (Object.keys(evidences).length > 0) {
        const { tableData, graphData } = processEvidenceDataKeys(evidences, getQueryByKey);
        setData(tableData);
        setChartData(graphData);
      } else {
        snackbar.error('No evidence data found');
      }

      setLoading(false);
    }
  }, [preparedEvidences]);

  const processEvidenceDataKeys = (
    evidenceData: any,
    getQueryByKey: (key: string) => {
      query: string;
      title: string;
    } | null
  ) => {
    const evidenceDataKeys = Object.keys(evidenceData);
    const tableData: any = [];
    const graphData: any = [];

    evidenceDataKeys.forEach((g) => {
      if (evidenceData[g]?.series_list_result && evidenceData[g]?.series_list_result.length > 0) {
        const maxKeysObject = getObjectWithMaxKeys(evidenceData[g]?.series_list_result);
        const metricKeys: string[] = Object.keys(maxKeysObject);
        let fromMetric = true;
        let headers: any[] = [];

        if (metricKeys && metricKeys.length > 0) {
          const headersWithWidth: Header[] = metricKeys.map((key) => {
            let width = '';
            if (key === 'Count') {
              width = '10%';
            }
            return { name: key, width: width, component: <Text value={key} /> };
          });
          headers = [...headersWithWidth, { name: '', width: '' }];
          metricKeys.push('Count');
        } else if (evidenceData[g]?.series_list_result[0].timestamps.length > 0) {
          fromMetric = false;
          headers = [
            { name: 'timestamps', width: '', component: <Text value='timestamp' /> },
            { name: 'values', width: '', component: <Text value='values' /> },
          ];
        }

        const labels = [...new Set(evidenceData[g]?.series_list_result?.flatMap((e: any) => e.timestamps) ?? [])];
        labels.sort();
        const chartDataDataset: any[] = [];

        const groupData = fromMetric
          ? evidenceData[g]?.series_list_result?.map((item: any) => {
              // O(1) lookup via Map instead of O(n) indexOf per label
              const tsIndex = new Map<any, number>();
              item.timestamps.forEach((ts: any, i: number) => tsIndex.set(ts, i));
              const values: any[] = [];
              labels.forEach((label) => {
                const index = tsIndex.get(label);
                if (index !== undefined) {
                  values.push(parseFloat(item.values[index]));
                } else {
                  values.push(0);
                }
              });
              chartDataDataset.push({
                label: Object.entries(item.metric)
                  .map(([key, value]) => `${key}=${value}`)
                  .join('\n'),
                data: values,
              });
              const metricData = metricKeys.map((h, i) => {
                if (item.metric[h]) {
                  if (i == 0) {
                    return {
                      text: <Text value={item.metric[h]} showAutoEllipsis />,
                      drilldownQuery: item,
                    };
                  }
                  return {
                    text: <Text value={item.metric[h]} showAutoEllipsis />,
                  };
                } else if (h == 'Count') {
                  return {
                    text: item?.values ? (
                      <Text value={item?.values?.reduce((accumulator: number, currentValue: string) => accumulator + parseFloat(currentValue), 0)} />
                    ) : (
                      '-'
                    ),
                  };
                }
                return {
                  text: '-',
                };
              });
              return metricData;
            })
          : evidenceData[g]?.series_list_result[0]?.timestamps?.map((item: any, indx: any) => {
              return [
                {
                  text: new Date(item * 1000).toString(),
                },
                {
                  text: evidenceData[g]?.series_list_result[0]?.values[indx] || '-',
                },
              ];
            });

        tableData.push({
          ...getQueryByKey(g),
          data: groupData,
          headers: headers,
        });

        graphData.push({
          ...getQueryByKey(g),
          data: {
            labels: labels.map((e: any) => convertNumberToTimestamp(e * 1000)),
            data: fromMetric
              ? chartDataDataset
              : [{ label: 'Value', data: evidenceData[g]?.series_list_result[0]?.values?.map((e: string) => parseFloat(e)) }],
          },
        });
      } else if (evidenceData[g]) {
        const dataItems = evidenceData[g];

        if (Array.isArray(dataItems) && dataItems[0]?.value && Array.isArray(dataItems[0].value)) {
          // Collect all unique metric keys from the array
          const metricKeys = Array.from(new Set(dataItems.flatMap((item) => (item.metric ? Object.keys(item.metric) : []))));

          // Define dynamic headers
          const headers = [
            { name: 'Timestamp', width: '', component: <Text value='Timestamp' /> },
            ...metricKeys.map((key) => ({
              name: key,
              width: '',
              component: <Text value={key} />,
            })),
            { name: 'Value', width: '', component: <Text value='Value' /> },
          ];

          // Build table data
          const data = dataItems.map((item) => {
            const timestamp = new Date(item.value[0] * 1000).toLocaleString();
            const value = item.value[1];

            // Build each row starting with timestamp
            const row = [
              { text: timestamp, drilldownQuery: item },
              ...metricKeys.map((key) => ({
                text: item.metric?.[key] ?? '-', // fallback if key not in metric
              })),
              { text: value },
            ];

            return row;
          });

          tableData.push({
            ...getQueryByKey(g),
            data,
            headers,
          });

          // For graph: plot total value over time
          graphData.push({
            ...getQueryByKey(g),
            data: {
              labels: dataItems.map((item) => convertNumberToTimestamp(item.value[0] * 1000)),
              data: [
                {
                  label: 'Total',
                  data: dataItems.map((item) => parseFloat(item.value[1])),
                },
              ],
            },
          });
        } else {
          // fallback for string_result or unknown structure
          tableData.push({
            ...getQueryByKey(g),
            data: [],
            headers: [],
            helperText: evidenceData[g].string_result ?? '',
          });

          graphData.push({
            ...getQueryByKey(g),
            data: {
              labels: [],
              data: [],
            },
            helperText: evidenceData[g].string_result ?? '',
          });
        }
      } else {
        tableData.push({
          ...getQueryByKey(g),
          data: [],
          headers: [],
          helperText: evidenceData[g].string_result ?? '',
        });
        graphData.push({
          ...getQueryByKey(g),
          data: {
            labels: [],
            data: [],
          },
          helperText: evidenceData[g].string_result ?? '',
        });
      }
    });

    return { tableData, graphData };
  };

  const handleSubmit = (
    query = '',
    llmQueryResponse = '',
    type = '',
    queriesToExecute: Array<{ key: string; query: string; title?: string }> = []
  ) => {
    if (!query && (!queriesToExecute || queriesToExecute.length === 0)) {
      return;
    }
    setLoading(true);
    setErrorMsg('');
    setData([]);
    setChartData([]);

    let promqls = query
      .replace(/^;+|;+$/g, '')
      .split(';')
      .map((g) => ({
        key: uuidv4(),
        query: g.trim(),
      }));

    if (queriesToExecute.length) {
      promqls = queriesToExecute;
    }

    setQuery(query);
    setLlmQueryResponse(llmQueryResponse);

    const getQueryByKey = (key: string) => {
      const entry: any = promqls.find((item: any) => item.key === key);
      return entry
        ? entry
        : {
            query: '',
            title: '',
          };
    };

    const convertFormattedQuery = (promqls: any) => {
      return promqls.reduce((res: any, val: any) => {
        return {
          ...res,
          [val.key]: val.query,
        };
      }, {});
    };
    const requestBody = {
      account_id: accountId,
      queries: convertFormattedQuery(promqls),
      start_time: selectedDateRange.startDate,
      end_time: selectedDateRange.endDate,
      instant: instant,
    };
    apiKubernetes1
      .consumePrometheusQueries(requestBody)
      .then((res) => {
        if (res?.data?.data?.metrics_list?.results) {
          const evidence = res?.data?.data?.metrics_list?.results;
          const evidenceData = evidence.reduce((res: any, data: any) => {
            return {
              ...res,
              [data.query_key]: {
                series_list_result: data.payload,
              },
            };
          }, {});

          const { tableData, graphData } = processEvidenceDataKeys(evidenceData, getQueryByKey);

          setData(tableData);
          setChartData(graphData);
          setLoading(false);
        } else {
          snackbar.error('Failed execute Query');
          setLoading(false);
        }
        if (type == 'ai') {
          aiCreateFeedback(true, query, llmQueryResponse);
        }
      })
      .catch(() => {
        setLoading(false);
        snackbar.error('Failed to fetch the Data');
      });
  };

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  const handleChange = (_event: any, value: string | null) => {
    if (value !== null) {
      setQLEditor(value);
    }
  };

  return (
    <div>
      <ListingLayout id='query-logs' sx={{ mb: 'var(--ds-space-2)' }}>
        <ListingLayout.Toolbar
          actions={
            <>
              <DownloadButton
                onClick={() =>
                  showChartView
                    ? { canvasId: chartData.map((cd: any, index: number) => `k8sPromChart-${cd.id || index}`), tableId: '' }
                    : { tableId: k8sProm }
                }
              />
              {showDateTime && (
                <CustomDateTimeRangePicker
                  passedSelectedDateTime={{
                    startTime: selectedDateRange.startDate,
                    endTime: selectedDateRange.endDate,
                    shortcutClickTime: 0,
                  }}
                  onChange={(result: any) => {
                    const val = result?.selection ?? result;
                    if (val) handleDateRangeChange(val);
                  }}
                />
              )}
              {showExtraOptions && (
                <>
                  <Tooltip placement='top' title={'Evaluated at one point in time'} arrow tooltipStyle={{ maxHeight: 'unset', overflowY: 'visible' }}>
                    <Box>
                      <Checkbox checked={instant} onChange={(next) => setInstant(next)} label='Instant' />
                    </Box>
                  </Tooltip>
                  <Switch checked={showChartView} onChange={(_e, checked) => setShowChartView(checked)} label='Chart' />
                  <DsButton
                    tone='primary'
                    size='md'
                    onClick={() => handleSubmit(query)}
                    disabled={loading || isAiLoading || (qLEditor === 'ai' && !query)}
                  >
                    Submit
                  </DsButton>
                  <UserHistoryButton accountId={accountId} module='metrics_query_prometheus' />
                </>
              )}
            </>
          }
        >
          {showQueryBox && (
            <ToggleButtonGroup
              color='primary'
              exclusive
              value={qLEditor}
              onChange={handleChange}
              sx={{
                minHeight: 0,
                minWidth: 0,
                '& button': {
                  padding: 'var(--ds-space-2) var(--ds-space-4)',
                  minHeight: 0,
                  minWidth: 0,
                  lineHeight: '14px',
                  height: ds.space.mul(0, 17),
                  fontSize: 'var(--ds-text-small)',
                  color: 'var(--ds-gray-600)',
                  fontWeight: 'var(--ds-font-weight-regular)',
                  borderColor: 'var(--ds-gray-300)',
                  borderWidth: 0.5,
                  backgroundColor: 'transparent',
                  '&:hover': {
                    borderColor: 'var(--ds-gray-400)',
                    borderWidth: 1,
                  },
                  '&.Mui-selected': {
                    backgroundColor: 'transparent !important',
                    borderColor: 'var(--ds-blue-300)',
                    borderWidth: '0.5px',
                    color: 'var(--ds-blue-500)',
                  },
                  '&.selected': {
                    fontWeight: 'var(--ds-font-weight-medium)',
                    borderBottom: '2px solid var(--ds-gray-700)',
                    borderBottomLeftRadius: 0,
                    borderBottomRightRadius: 0,
                  },
                },
              }}
            >
              <ToggleButton value='build'>Builder</ToggleButton>
              <ToggleButton value='code'>Code</ToggleButton>
              <ToggleButton value='ai'>AI</ToggleButton>
            </ToggleButtonGroup>
          )}
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          {showQueryBox && (
            <QueryModeSwitcher
              accountId={accountId}
              params={{ ...selectedDateRange }}
              logProvider={'promql'}
              onQueryChange={(e: any) => {
                setQuery(e);
              }}
              queryItems={promqlItems as any}
              setQueryItems={setPromqlItems}
              setLlmQueryResponse={setLlmQueryResponse}
              setConversationId={setConversationId}
              qLEditor={qLEditor}
              setQLEditor={setQLEditor}
              onAiLoadingChange={(loading: boolean) => {
                setIsAiLoading(loading);
              }}
              providerType={'metrics'}
            />
          )}
          {loading ? (
            <Skeleton shape='rect' width='98%' height={ds.space.mul(0, 200)} />
          ) : showChartView ? (
            chartData.map((cd: any, index: number) => (
              <Box sx={{ mb: ds.space[2] }} key={`chart-${cd.id || index}`}>
                <Divider sx={{ mt: ds.space[6] }} />
                {cd.title && (
                  <Typography variant='body1' sx={{ mb: ds.space[4], fontWeight: 'var(--ds-font-weight-semibold)' }}>
                    {cd.title}
                  </Typography>
                )}
                <Typography variant='body1' sx={{ mb: ds.space[4] }}>
                  Query: {cd.query}
                  {cd.helperText && <Typography sx={{ color: 'var(--ds-red-500)' }}>{cd.helperText}</Typography>}
                </Typography>
                <LineChart
                  id={`k8sPromChart-${cd.id || index}`}
                  dataset={cd.data.data}
                  labels={cd.data.labels}
                  legendOptions={{
                    renderer: 'html',
                  }}
                  interactionOptions={{
                    enabled: false,
                  }}
                />
              </Box>
            ))
          ) : (
            data.map((cd: any, index: number) => (
              <Box sx={{ mb: ds.space[6] }} key={`${uuidv4()}`}>
                <Divider sx={{ mt: ds.space[6] }} />
                {cd.title && (
                  <Typography variant='body1' sx={{ mb: ds.space[4], fontWeight: 'var(--ds-font-weight-semibold)' }}>
                    {cd.title}
                  </Typography>
                )}
                <Typography variant='body1' sx={{ mb: ds.space[4] }}>
                  Query: {cd.query}
                  {cd.helperText && <Typography sx={{ color: 'var(--ds-red-500)' }}>{cd.helperText}</Typography>}
                </Typography>
                <KubernetesTable2
                  id={k8sProm}
                  totalRows={cd.data.length}
                  data={cd.data}
                  rounded={'0px'}
                  headers={cd.headers}
                  rowsPerPage={cd.data.length}
                  showExpandable={showDrilldown}
                  expandable={{
                    tabs: [
                      {
                        text: 'Row Details',
                        value: 0,
                        key: 'prometheus-query-log',
                        componentFn: (_option: any, query: any, _row: any) => {
                          return (
                            <div>
                              {Object.keys(query).length > 0 ? (
                                <>
                                  <Title title={'Labels'} />
                                  <Grid container spacing={2}>
                                    {Object.keys(query?.metric).map((key) => {
                                      return (
                                        <Grid key={key} item xs={6}>
                                          <Typography>{key + '=' + query.metric[key]}</Typography>
                                        </Grid>
                                      );
                                    })}
                                  </Grid>
                                  <br />
                                  <Title title={'Trend'} />
                                  <LineChart
                                    data={instant ? [query.value[1]] : query?.values.map((e: string) => parseFloat(e))}
                                    labels={
                                      instant
                                        ? [convertNumberToTimestamp(query.value[0] * 1000)]
                                        : query?.timestamps?.map((e: number) => convertNumberToTimestamp(e * 1000))
                                    }
                                    chartLabel={'Count'}
                                  />
                                </>
                              ) : (
                                <Typography>No Data</Typography>
                              )}
                            </div>
                          );
                        },
                      },
                      {
                        text: '+/- Logs',
                        value: 1,
                        key: 'loki-plus-minus-log-from-prometheus',
                      },
                    ],
                  }}
                  errorMessage={errorMsg}
                  onPageChange={undefined}
                  onSortChange={undefined}
                />
                {index < data.length - 1 && <Divider sx={{ mt: ds.space[6] }} />}
              </Box>
            ))
          )}
        </ListingLayout.Body>
      </ListingLayout>
    </div>
  );
};

export default KubernetesPrometheus;
