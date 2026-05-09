import apiAskNudgebee from '@api1/ask-nudgebee';
import { ShareIconBlue, SaveIconOutlineselect } from '@assets';
import { LineChart, Text } from '@components1/common';
import CopyableText from '@components1/common/CopyableText';
import CustomDivider from '@components1/common/CustomDivider';
import CustomLink from '@components1/common/CustomLink';
import ExpandableText from '@components1/common/ExpandableText';
import MarkDowns from '@components1/common/MarkDowns';
import CustomTable from '@components1/common/tables/CustomTable2';
import FeedbackComponent from '@components1/common/ThumpsUpAndDown';
import CustomIconButton from '@components1/CustomIconButton';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import Duration from '@components1/llm/common/Duration';
import LLMAnswerRenderer from './common/LLMAnswerRenderer';
import KubernetesSecurityDetails from '@components1/recommendations/security/KubernetesSecurityDetails';
import { Box, Grid, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';
import { useRouter } from 'next/router';
import React, { useEffect, useState } from 'react';
import { colors } from 'src/utils/colors';
import { convertToReadableFormat } from 'src/utils/common';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import { mapToTableData } from '@components1/k8s/details/KubernetesLogStash';
import { LogDate } from '@components1/k8s/common/LogDate';
import { AgentTokenUsage } from './common/TokenUsageDisplay';
import { detectWorkflowJson } from '@components1/workflow/utils/workflowDetection';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import ReferencesPopover from './common/ReferencesModal';
import FileDownloadIcon from '@mui/icons-material/FileDownload';
import CheckIcon from '@mui/icons-material/Check';

const KubernetesLLMRequestResponse = (props) => {
  const router = useRouter();
  const [sentFeedback, setSentFeedback] = useState({});
  const [recordsPerPage, setRecordsPerPage] = useState(5);
  const [currentPage, setCurrentPage] = useState(0);
  const [referencesAnchorEl, setReferencesAnchorEl] = useState(null);

  useEffect(() => {
    if (props.toolCall.type == 'response') {
      apiAskNudgebee
        .getFeedbackForSessionId({
          account_id: props.accountId,
          session_id: props.toolCall.id,
        })
        .then((res) => {
          const response = res?.data?.data?.llm_conversation_feedback_v2?.rows ?? [];
          if (response.length == 1) {
            setSentFeedback({
              submitted: true,
              isPositive: response[0].useful ?? null,
              message: response[0].additional_notes ?? '',
            });
          }
        });
    }
  }, [props.toolCall.id]);

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  let toolName = props.toolCall.tool;
  toolName = toolName?.replaceAll('_query', '');
  toolName = toolName?.replaceAll('query', '');
  toolName = toolName?.replaceAll('Query', '');
  toolName = toolName?.replaceAll('_command_executer', '');
  toolName = toolName?.replaceAll('_execute', '');
  toolName = toolName?.replaceAll('execute', '');
  toolName = toolName?.replaceAll('_Execute', '');
  toolName = toolName?.replaceAll('Execute', '');
  toolName = toolName?.replaceAll('_sql', '');
  toolName = toolName?.replaceAll('sql', '');
  toolName = toolName?.replaceAll('Sql', '');
  toolName = toolName?.replaceAll('_executor', '');
  toolName = toolName?.replaceAll('executor', '');
  toolName = toolName?.replaceAll('Executor', '');
  if (toolName == 'getResourceTraces' || toolName == 'traces_execute' || toolName == 'traces') {
    toolName = 'Traces';
  }
  if (toolName == 'GetSecurityIssues' || toolName == 'security' || toolName == 'security_execute') {
    toolName = 'Security';
  }
  if (toolName == 'docs' || toolName == 'search_docs' || toolName == 'docs_agent') {
    toolName = 'Docs';
  }

  const getTableData = (arrayData, checkAll) => {
    if (arrayData && arrayData.length > 0) {
      const headers = Object.keys(arrayData[0]);
      if (checkAll) {
        for (let i = 1; i < arrayData.length; i++) {
          let objectKeys = Object.keys(arrayData[i]);
          for (let j = 0; j < objectKeys.length; j++) {
            if (!headers.includes(objectKeys[j])) {
              headers.push(objectKeys[j]);
            }
          }
        }
      }

      let convertedJson = arrayData.map((row) => {
        const rowData = {};
        headers.forEach((header, _) => {
          rowData[header] = row[header];
        });
        return rowData;
      });
      const convertedJson2 = convertedJson.map((item) => {
        const components = Object.entries(item).map(([_, value]) => {
          let value1 = value;
          if (typeof value === 'object' || Array.isArray(value)) {
            value1 = JSON.stringify(value);
          }
          return {
            component: <Text value={value1} showAutoEllipsis sx={{ minWidth: '50px' }} />,
          };
        });
        if (item.tool === 'plan_update') {
          components.sx = { backgroundColor: colors.background.suggestionCardBG };
        }
        return components;
      });
      return { headers: headers.map((f) => convertToReadableFormat(f.replaceAll('_', ' '))), tableData: convertedJson2 };
    }
  };

  const getTableData1 = (objData) => {
    if (objData && Object.keys(objData).length > 0) {
      const keys = Object.keys(objData);
      const tableData = [
        keys.map((key) => {
          let k = objData[key];
          if (typeof k === 'object' || Array.isArray(k)) {
            k = JSON.stringify(k);
          }
          return { text: k };
        }),
      ];

      return {
        headers: keys.map((f) => convertToReadableFormat(f.replaceAll('_', ' '))),
        tableData: tableData,
      };
    }
  };
  const defaultLogComponent = (results) => {
    let headers = [
      { name: 'Date', width: '10%' },
      { name: 'Message', width: '90%' },
    ];
    let tableData = [];
    if (results?.logs?.length > 0) {
      let logsData = results?.logs;
      tableData = logsData.map((m) => {
        let dateTimestamp = Date.parse(m.timestamp);
        let rowData = [
          {
            component: <LogDate timestamp={dateTimestamp} log={m?.message} />,
          },

          {
            component: <ExpandableText text={m?.message} />,
          },
        ];
        return rowData;
      });
    }
    return (
      <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
        <Grid item md={1}>
          <b>Provider -</b>
        </Grid>
        <Grid item md={11}>
          <Text value={results?.metadata?.provider} />
        </Grid>
        <Grid item md={1}>
          <b>Query -</b>
        </Grid>
        <Grid item md={11}>
          <Text value={results?.metadata?.query} />
        </Grid>
        <CustomTable tableData={tableData} headers={headers} renderVertical={tableData?.length <= 1} />
      </Grid>
    );
  };

  // Detect if response contains workflow JSON
  const responseText = props.toolCall?.response?.text || props.toolCall?.response_text || props.toolCall?.text;
  const chainName = props.toolCall?.response?.chain_name || props.toolCall?.agentName;

  const workflowJson = detectWorkflowJson(responseText, chainName);
  const isWorkflowResponse = workflowJson !== null;

  const getToolResponseCard = function () {
    let toolName = props.toolCall.tool;
    if (toolName == 'TroubleshootPlanner' && props.toolCall?.log) {
      return (
        <Grid container sx={{ marginBottom: '8px' }}>
          <MarkDowns data={props.toolCall?.response?.log ?? props.toolCall?.log} />
        </Grid>
      );
    } else if (toolName == 'planner' && (props.toolCall?.log || props.toolCall?.response_text)) {
      if (props.toolCall?.response_text) {
        try {
          let data = JSON.parse(props.toolCall?.response_text);

          if (Array.isArray(data)) {
            data.sort((a, b) => {
              const iterA = a.iteration || 0;
              const iterB = b.iteration || 0;
              if (iterA !== iterB) {
                return iterA - iterB;
              }
              if (a.tool === 'plan_update' && b.tool !== 'plan_update') {
                return -1;
              }
              if (a.tool !== 'plan_update' && b.tool === 'plan_update') {
                return 1;
              }
              return 0;
            });
          }

          const objectInfo = getTableData(data, true);
          if (objectInfo) {
            objectInfo.headers = objectInfo.headers.map((f) => {
              f = f.toLocaleLowerCase();
              if (f == 'id') {
                return {
                  width: '10%',
                  name: 'ID',
                };
              } else if (f == 'tool') {
                return {
                  width: '10%',
                  name: 'Tool',
                };
              } else if (f == 'plan') {
                return {
                  width: '40%',
                  name: 'Plan',
                };
              } else if (f == 'query') {
                return {
                  width: '40%',
                  name: 'Query',
                };
              }
              return f;
            });
          }
          return (
            <Grid container sx={{ marginBottom: '8px' }}>
              <CustomTable
                tableData={objectInfo.tableData}
                headers={objectInfo.headers}
                totalRows={objectInfo.tableData.length}
                rowsPerPage={objectInfo.tableData.length}
              />
            </Grid>
          );
        } catch {
          return (
            <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
              <pre>{(props.toolCall?.response?.text || '').replace(/\\n/g, '\n')}</pre>{' '}
            </Grid>
          );
        }
      }
      return (
        <Grid
          container
          sx={{
            marginBottom: '8px',
            '& p,& li': {
              fontSize: '14px',
              maxWidth: '790px',
              color: colors.darkPrimary,
              m: '0px 0px 12px 0px !important',
            },
          }}
        >
          <MarkDowns data={props.toolCall?.response?.log ?? props.toolCall?.log} />
        </Grid>
      );
    } else if (
      (toolName == 'PostgresQueryExecutor' ||
        toolName == 'postgres-debug' ||
        toolName == 'postgres_debug' ||
        toolName == 'postgres' ||
        toolName == 'postgres_query_execute' ||
        toolName == 'mysql-debug' ||
        toolName == 'mysql_debug' ||
        toolName == 'mysql' ||
        toolName == 'mysql_query_execute' ||
        toolName == 'queryEvents' ||
        toolName == 'executeEventsSql' ||
        toolName == 'events_execute' ||
        toolName == 'events' ||
        toolName == 'Events' ||
        toolName.startsWith('clickhouse')) &&
      props.toolCall?.response?.text
    ) {
      try {
        let events = [];
        try {
          events = JSON.parse(props.toolCall?.response?.text);
        } catch {
          <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
            <pre>{props.toolCall?.response?.text.replace(/\\n/g, '\n')}</pre>
          </Grid>;
        }

        if (Array.isArray(events) && events.length > 0) {
          const tableInfo = getTableData(events);

          const startIndex = currentPage * recordsPerPage;
          const endIndex = startIndex + recordsPerPage;
          const paginatedTableData = tableInfo.tableData.slice(startIndex, endIndex);

          return (
            <Grid container sx={{ marginBottom: '8px' }}>
              {events?.every((e) => !e.cloud_account_id) ? (
                <Box
                  sx={{
                    maxWidth: '1024px',
                    overflowX: 'auto',
                    '@media (max-width: 1510px)': {
                      maxWidth: '880px',
                    },
                    '@media (max-width: 1230px)': {
                      maxWidth: '520px',
                    },
                  }}
                >
                  <CustomTable
                    tableData={paginatedTableData}
                    headers={tableInfo.headers}
                    totalRows={tableInfo.tableData.length}
                    rowsPerPage={recordsPerPage}
                    onPageChange={onPageChange}
                    pageNumber={currentPage + 1}
                    renderVertical={tableInfo?.tableData?.length <= 1}
                  />
                </Box>
              ) : (
                events?.map((e) => (
                  <React.Fragment key={e.id}>
                    <Grid item md={10} mb={2}>
                      <CustomLink
                        style={{ color: colors.darkPrimary }}
                        target={'_blank'}
                        href={`/investigate?id=${e.id}&accountId=${e.cloud_account_id}`}
                        passHref
                      >
                        {`${e.title},  Subject - ${e.subject_name},  Namespace - ${e.subject_namespace}`}
                      </CustomLink>
                    </Grid>
                  </React.Fragment>
                ))
              )}
            </Grid>
          );
        }
        const objectInfo = getTableData1(events);
        if (objectInfo) {
          const startIndex = currentPage * recordsPerPage;
          const endIndex = startIndex + recordsPerPage;
          const paginatedObjectData = objectInfo.tableData.slice(startIndex, endIndex);

          return (
            <Grid container sx={{ marginBottom: '8px' }}>
              <CustomTable
                tableData={paginatedObjectData}
                headers={objectInfo.headers}
                totalRows={objectInfo.tableData.length}
                rowsPerPage={recordsPerPage}
                onPageChange={onPageChange}
                pageNumber={currentPage + 1}
                renderVertical={objectInfo?.tableData?.length <= 1}
              />
            </Grid>
          );
        }
      } catch {
        // Handle JSON parsing error for events data
      }
    } else if ((toolName == 'queryPrometheus' || toolName == 'prometheus' || toolName == 'prometheus_execute') && props.toolCall?.response?.text) {
      try {
        let metrics = JSON.parse(props.toolCall?.response?.text);
        let metricsQueryObject = {};
        if (Array.isArray(metrics) && metrics.length > 0) {
          metricsQueryObject['PrometheusQuery'] = metrics;
        } else {
          metricsQueryObject = metrics;
          for (let key in metricsQueryObject) {
            let value = metricsQueryObject[key];
            if (typeof value === 'string') {
              metricsQueryObject[key] = JSON.parse(value);
            }
          }
        }

        return (
          <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
            {Object.keys(metricsQueryObject).map((key) => {
              return (
                <React.Fragment key={key}>
                  <Grid item md={12} mb={2}>
                    <Text value={key} sx={{ fontWeight: '500', wordBreak: 'break-word' }} />
                  </Grid>
                  {metricsQueryObject[key]?.map((e, i) => {
                    return (
                      <React.Fragment key={`${key}-${i}`}>
                        {e.metric && Object.keys(e.metric).length > 0 && (
                          <Grid item md={10} mb={2}>
                            <Typography sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary, wordBreak: 'break-word' }}>
                              {JSON.stringify(e.metric ?? {})}
                            </Typography>
                          </Grid>
                        )}
                        <Grid item md={3} mb={2}>
                          <Text value={`Min:${e.stats.min}`} sx={{ marginBottom: '8px' }} />
                        </Grid>
                        <Grid item md={3} mb={2}>
                          <Text value={`Max:${e.stats.max}`} sx={{ marginBottom: '8px' }} />
                        </Grid>
                        <Grid item md={3} mb={2}>
                          <Text value={`Avg:${e.stats.avg}`} sx={{ marginBottom: '8px' }} />
                        </Grid>
                        <Grid item md={3} mb={2}>
                          <Text value={`P99:${e.stats.p99}`} sx={{ marginBottom: '8px' }} />
                        </Grid>
                        {e.values?.length > 1 ? (
                          <Grid item md={12} mb={2}>
                            <LineChart data={e.values} labels={e.timestamps} />
                          </Grid>
                        ) : (
                          <>
                            {e.values?.[0] && (
                              <Grid item md={12}>
                                <Text value={`Value: ${e.values?.[0]}`} sx={{ fontWeight: '500' }} />
                              </Grid>
                            )}
                          </>
                        )}
                        {i != metricsQueryObject[key].length - 1 && (
                          <Grid item md={12} mb={2}>
                            <CustomDivider maxWidth={5} />
                          </Grid>
                        )}
                      </React.Fragment>
                    );
                  })}
                </React.Fragment>
              );
            })}
          </Grid>
        );
      } catch {
        <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
          <pre>{props.toolCall?.response?.text.replace(/\\n/g, '\n')}</pre>
        </Grid>;
      }
    } else if (
      (toolName == 'queryTraces' ||
        toolName == 'traces' ||
        toolName == 'recommendations' ||
        toolName == 'executeRecommendationSql' ||
        toolName == 'recommendation_execute' ||
        toolName == 'postgres_debug' ||
        toolName == 'postgres' ||
        toolName == 'postgres_execute' ||
        toolName == 'queryPostgres' ||
        toolName == 'PostgresQueryExecutor' ||
        toolName == 'postgres_query_execute' ||
        toolName == 'mysql_debug' ||
        toolName == 'mysql' ||
        toolName == 'mysql_execute' ||
        toolName == 'queryMysql' ||
        toolName == 'MysqlQueryExecutor' ||
        toolName == 'mysql_query_execute' ||
        toolName == 'getResourceTraces' ||
        toolName == 'traces_execute' ||
        toolName == 'security' ||
        toolName == 'security_execute') &&
      props.toolCall?.response?.text
    ) {
      try {
        let traces = JSON.parse(props.toolCall?.response?.text);
        let headers = [];
        let tableData = [];
        if (traces.length > 0) {
          headers = Object.keys(traces[0]);
        }
        tableData = traces.map((t) => {
          let rowData = [];
          for (let h of headers) {
            rowData.push({
              component: <Text value={t[h]} showAutoEllipsis sx={{ minWidth: '60px' }} />,
            });
          }
          return rowData;
        });

        const startIndex = currentPage * recordsPerPage;
        const endIndex = startIndex + recordsPerPage;
        const paginatedData = tableData.slice(startIndex, endIndex);

        return (
          <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
            <Box
              sx={{
                maxWidth: '1024px',
                overflowX: 'auto',
                '@media (max-width: 1510px)': {
                  maxWidth: '880px',
                },
                '@media (max-width: 1230px)': {
                  maxWidth: '520px',
                },
              }}
            >
              <CustomTable
                tableData={paginatedData}
                headers={Object.keys(traces[0] ?? {}).map((header) => header.replaceAll('_', ' '))}
                rowsPerPage={recordsPerPage}
                onPageChange={onPageChange}
                pageNumber={currentPage + 1}
                totalRows={tableData.length}
                renderVertical={tableData?.length <= 1}
              />
            </Box>
          </Grid>
        );
      } catch {
        // Handle JSON parsing error for traces/events data
        if (props.toolCall?.response?.text) {
          return (
            <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
              <pre>{props.toolCall?.response?.text.replace(/\\n/g, '\n')}</pre>
            </Grid>
          );
        }
      }
    } else if (toolName == 'GetSecurityIssues') {
      try {
        let traces = JSON.parse(props.toolCall?.response?.text);
        return <KubernetesSecurityDetails llmTableData={traces} disableInfographic={true} kubernetes={{ id: props?.accountId }} />;
      } catch {
        // Handle JSON parsing error for security issues data
        if (props.toolCall?.response?.text) {
          return (
            <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
              <pre>{props.toolCall?.response?.text.replace(/\\n/g, '\n')}</pre>
            </Grid>
          );
        }
      }
    } else if ((toolName == 'queryLoki' || toolName == 'loki' || toolName == 'loki_execute') && props.toolCall?.response?.text) {
      try {
        let results = JSON.parse(props.toolCall?.response?.text);
        if (results?.result?.length > 0) {
          let logsData = results?.result[0]?.values;
          let headers = [
            { name: 'Date', width: '10%' },
            { name: 'Message', width: '90%' },
          ];
          let tableData = [];
          tableData = logsData.map((m) => {
            let dateTimestamp = parseInt(m[0]) / 1000000;

            let rowData = [
              {
                component: <LogDate timestamp={dateTimestamp} log={m?.[1]} />,
              },

              {
                component: <ExpandableText text={m[1]} maxSize={150} />,
              },
            ];
            return rowData;
          });

          return (
            <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
              <CustomTable tableData={tableData} headers={headers} renderVertical={tableData?.length <= 1} />
            </Grid>
          );
        }
      } catch {
        return (
          <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
            <pre>{props.toolCall?.response?.text.replace(/\\n/g, '\n')}</pre>
          </Grid>
        );
      }
    } else if ((toolName == 'queryES' || toolName == 'es' || toolName == 'elastic_search_execute') && props.toolCall?.response?.text) {
      try {
        let results = JSON.parse(props.toolCall?.response?.text);
        if (results?.result?.length > 0) {
          let logsData = results?.result;
          let tableData = mapToTableData(logsData);
          return (
            <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
              <KubernetesTable2
                id={'k8s-logs'}
                totalRows={tableData.length}
                data={tableData}
                headers={[{ name: 'Date', width: '20%' }, { name: 'Message', width: '80%' }, '']}
                rowsPerPage={tableData.length}
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
                onPageChange={undefined}
                onSortChange={undefined}
              />
            </Grid>
          );
        }
      } catch {
        return (
          <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
            <pre>{props.toolCall?.response?.text.replace(/\\n/g, '\n')}</pre>
          </Grid>
        );
      }
    } else if (
      (toolName == 'KubectlExecutor' || toolName == 'k8s' || toolName == 'kubectl' || toolName == 'kubectl_execute') &&
      props.toolCall?.response?.text
    ) {
      try {
        let results = JSON.parse(props.toolCall?.response?.text);
        return (
          <>
            {results.stdout ? (
              <Grid
                container
                sx={{
                  marginBottom: '8px',
                  fontSize: '14px',
                  color: colors.darkPrimary,
                  wordBreak: 'break-word',
                  pre: {
                    textWrap: 'inherit',
                  },
                }}
              >
                <pre>{results.stdout.replace(/\\n/g, '\n') || results.stderr.replace(/\\n/g, '\n')}</pre>
              </Grid>
            ) : null}
          </>
        );
      } catch {
        return (
          <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
            <pre>{props.toolCall?.response?.text.replace(/\\n/g, '\n')}</pre>
          </Grid>
        );
      }
    } else if ((toolName == 'search_docs' || toolName == 'docs' || toolName == 'docs_agent') && props.toolCall?.response?.text) {
      try {
        let results = JSON.parse(props.toolCall?.response?.text) || [];
        results = results.map((r, i) => {
          r.index = i;
          return r;
        });

        return (
          <>
            {results
              ? results?.map((r) => {
                  return (
                    <React.Fragment key={r.index}>
                      <MarkDowns data={r.PageContent} sx={{ width: '100%' }} />
                      <CustomDivider borderType='dashed' borderWidth={'0.75px'} borderColor={colors.border.secondary} margin='0px 0px 12px 0px' />
                    </React.Fragment>
                  );
                })
              : null}
          </>
        );
      } catch {
        // Handle JSON parsing error for docs data
      }
    } else if (
      (toolName == 'aws' ||
        toolName == 'aws_execute' ||
        toolName == 'gcloud' ||
        toolName == 'gcloud_execute' ||
        toolName == 'azure' ||
        toolName == 'azure_execute') &&
      props.toolCall?.response?.text
    ) {
      try {
        return <pre>{props.toolCall?.response?.text}</pre>;
      } catch {
        // Handle error in parsing aws/gcloud/azure data
      }
    } else if (
      (toolName == 'rabbit_execute' || toolName == 'rabbitmq' || toolName == 'rabbitmq_execute' || toolName == 'redis_command_executer') &&
      props.toolCall?.response?.text
    ) {
      try {
        let data = props.toolCall?.response?.text;
        try {
          data = JSON.parse(data);
          data = data?.stdout || data?.stderr;
        } catch {
          return (
            <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
              <pre>{props.toolCall?.response?.text.replace(/\\n/g, '\n')}</pre>
            </Grid>
          );
        }

        return (
          <>
            <pre> {data}</pre>
          </>
        );
      } catch {
        // Handle error in parsing rabbitmq data
      }
    } else if (
      (toolName == 'redis_execute' ||
        toolName == 'redis' ||
        toolName == 'redis_executor' ||
        toolName == 'redis_command_executor' ||
        toolName == 'redis_command_executer') &&
      props.toolCall?.response?.text
    ) {
      try {
        let data = props.toolCall?.response?.text;
        try {
          data = JSON.parse(data);
          data = data?.stdout || data?.stderr;
        } catch {
          return (
            <Grid container sx={{ marginBottom: '8px', fontSize: '14px', color: colors.darkPrimary }}>
              <pre>{props.toolCall?.response?.text.replace(/\\n/g, '\n')}</pre>
            </Grid>
          );
        }

        return (
          <>
            <pre> {data}</pre>
          </>
        );
      } catch {
        // Handle error in parsing redis data
      }
    } else if (toolName == 'followup-question') {
      // Followup interactivity (textarea / option buttons / multi-select footer) lives in
      // the bottom-anchored FollowupSheet now. Inline only renders a compact, read-only
      // summary so the user can scroll back through the timeline and see what they were
      // asked and how they answered.
      let messageConfig = {};
      if (props.toolCall?.response?.message_config) {
        try {
          const mc = props.toolCall.response.message_config;
          messageConfig = typeof mc === 'string' ? JSON.parse(mc) : mc;
        } catch {
          // message_config may already be a parsed object from DB view JSON aggregation
        }
      }

      const status = props.toolCall?.response?.status;
      const isCompleted = status === 'COMPLETED';
      const isExpired = status === 'TERMINATED' || status === 'KILLED' || status === 'FAILED';
      const rawResponse = props.toolCall?.response?.text || '';
      // multi_select stores the answer as a JSON-stringified array; render as a friendly
      // comma-separated list so the user sees the actual selections rather than JSON syntax.
      let displayResponse = rawResponse;
      if (rawResponse && messageConfig.followupType === 'multi_select') {
        try {
          const parsed = JSON.parse(rawResponse);
          if (Array.isArray(parsed)) {
            displayResponse = parsed.join(', ');
          }
        } catch {
          // keep raw text
        }
      }

      return (
        <Box sx={{ pt: 0 }}>
          {/* Only show the full question text inline once it's answered (or terminated) — while
              the user is still being asked, the bottom FollowupSheet owns the question display
              so we don't duplicate (and potentially explode) huge prompts in two places. */}
          {(isCompleted || isExpired) && messageConfig.question && (
            <Box
              sx={{
                fontSize: '13px',
                color: colors.text.secondary,
                lineHeight: 1.45,
                mb: '6px',
                '& p': { margin: 0 },
                '& code': {
                  fontFamily: 'ui-monospace, "SF Mono", Menlo, monospace',
                  fontSize: '12px',
                  background: colors.background.shimmerBase,
                  padding: '1.5px 5px',
                  borderRadius: '4px',
                },
              }}
            >
              <MarkDowns data={messageConfig.question} />
            </Box>
          )}
          {isCompleted && displayResponse && (
            <Box
              sx={{
                mt: 0,
                mb: '8px',
                display: 'flex',
                alignItems: 'center',
                flexWrap: 'wrap',
                gap: '10px',
              }}
            >
              <Box
                component='span'
                sx={{
                  fontSize: '13px',
                  color: '#64748b',
                  fontWeight: 500,
                  flexShrink: 0,
                }}
              >
                You replied
              </Box>
              <Box
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: '5px',
                  paddingTop: '2px !important',
                  paddingBottom: '2px !important',
                  paddingLeft: '10px !important',
                  paddingRight: '10px !important',
                  minHeight: '20px',
                  lineHeight: 1.3,
                  background: colors.background.primaryLightest,
                  borderRadius: '999px',
                  fontSize: '12.5px',
                  fontWeight: 600,
                  color: colors.primary,
                  maxWidth: '100%',
                  wordBreak: 'break-word',
                  boxSizing: 'border-box',
                }}
              >
                <CheckIcon sx={{ fontSize: '12px', flexShrink: 0 }} />
                <Box component='span' sx={{ minWidth: 0 }}>
                  {displayResponse}
                </Box>
              </Box>
            </Box>
          )}
          {!isCompleted && !isExpired && (
            <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, fontStyle: 'italic' }}>
              ↓ Answer this question in the input below
            </Typography>
          )}
          {isExpired && (
            <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, fontStyle: 'italic' }}>
              Conversation was terminated. This question was not answered.
            </Typography>
          )}
        </Box>
      );
    } else if (toolName?.toLocaleLowerCase()?.includes('logs') && props.toolCall?.response?.text) {
      try {
        let results = JSON.parse(props.toolCall?.response?.text);
        return defaultLogComponent(results);
      } catch {
        return (
          <Grid container sx={{ marginBottom: '8px', fontSize: '14px' }}>
            <MarkDowns data={props.toolCall?.response?.text} />
          </Grid>
        );
      }
    } else if (toolName == 'LLM' || toolName == 'llm') {
      let data = props.toolCall?.response_text ?? props.toolCall?.response?.text;
      return (
        <SummaryBlock
          hideTitle
          sx={{
            display: 'flex',
            alignItems: 'flex-end',
            backgroundColor: colors.white,
            mt: '10px',
            padding: '10px 15px',
            bottom: '18px',
            borderColor: 'white',
          }}
        >
          {<MarkDowns data={data?.replace(/^:/, '')} sx={{ width: '100%' }} />}
        </SummaryBlock>
      );
    } else if (toolName == 'crawl_execute' || toolName == 'crawl') {
      let data = props.toolCall?.response_text ?? props.toolCall?.response?.text;
      return (
        <SummaryBlock
          hideTitle
          sx={{
            display: 'flex',
            alignItems: 'flex-end',
            backgroundColor: colors.white,
            mt: '10px',
            padding: '10px 15px',
            bottom: '18px',
            borderColor: 'white',
          }}
        >
          {<MarkDowns data={data?.replace(/^:/, '')} sx={{ width: '100%' }} />}
        </SummaryBlock>
      );
    } else if (toolName == 'service_dependency_graph' || toolName == 'service_dependency_graph_execute') {
      let data = props.toolCall?.response_text ?? props.toolCall?.response?.text;
      return (
        <SummaryBlock
          hideTitle
          sx={{
            display: 'flex',
            alignItems: 'flex-end',
            backgroundColor: colors.white,
            mt: '10px',
            padding: '10px 15px',
            bottom: '18px',
            borderColor: 'white',
          }}
        >
          {<MarkDowns data={data?.replace(/^:/, '')} sx={{ width: '100%' }} />}
        </SummaryBlock>
      );
    }

    let markdownResponse = props.toolCall?.response?.text?.replace(/^:/, '');
    try {
      let parsedObject = JSON.parse(markdownResponse);
      //valid json
      markdownResponse = JSON.stringify(parsedObject, null, 2);
      markdownResponse = '```json\n' + markdownResponse + '\n```';
    } catch {
      //ignore
    }
    return <MarkDowns data={markdownResponse} sx={{ width: '100%' }} />;
  };

  let agentRequest = props.toolCall.text;
  if (agentRequest?.includes('"command"')) {
    try {
      agentRequest = JSON.parse(agentRequest)?.command;
    } catch {
      // ignore
    }
  }

  // Helper function to get unique references count
  const parsedReferences = React.useMemo(() => {
    if (!props.toolCall.references) {
      return [];
    }
    if (typeof props.toolCall.references === 'string') {
      try {
        return JSON.parse(props.toolCall.references);
      } catch {
        return [];
      }
    }
    return Array.isArray(props.toolCall.references) ? props.toolCall.references : [];
  }, [props.toolCall.references]);

  const getUniqueReferencesCount = (references) => {
    if (!references || references.length === 0) {
      return 0;
    }
    const seenUrls = new Set();
    references.forEach((ref) => {
      seenUrls.add(ref.url);
    });
    return seenUrls.size;
  };

  const aiCreateFeedback = async (createFeedbackObject) => {
    if (props.toolCall.id) {
      await apiAskNudgebee.createAiFeedback({
        session_id: props.toolCall.id,
        module: 'new-investigation',
        question: props.generateQuestionText,
        llm_response: '',
        user_corrected_response: '',
        additional_notes: createFeedbackObject.type == 'thumbs_up' ? 'User liked the Response' : createFeedbackObject.message,
        conversation_id: props.toolCall.id,
        cloud_account_id: props.accountId,
        useful: createFeedbackObject.type == 'thumbs_up',
      });
    }
  };

  return (
    <Grid container sx={{ scrollBehavior: 'unset', padding: '0px' }}>
      {/* Duration for acknowledgment is now shown in the header right side */}
      {(props.toolCall.type == 'response' || props.toolCall.type == 'error') && (
        <Grid
          item
          md={12}
          sx={{
            fontFamily: '"Poppins", sans-serif',
            fontWeight: '500',
            lineHeight: '20px',
            color: colors.text.secondary,
            wordBreak: 'break-word',
          }}
        >
          {/* Per-message token-usage widget and duration are now rendered in the response meta-rail
              (top-right of the card). The "Apply to Editor" workflow CTA stays right-aligned here. */}
          {isWorkflowResponse && (
            <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: '12px' }}>
              <CustomButton
                text='Apply to Editor'
                variant='primary'
                size='Small'
                onClick={() => {
                  try {
                    // Format JSON nicely before applying
                    const formatted = JSON.stringify(JSON.parse(workflowJson), null, 2);

                    // Store workflow JSON and navigate to workflow editor
                    sessionStorage.setItem('aiGeneratedWorkflow', formatted);
                    router.push(`/workflow/new?accountId=${props.accountId}&loadFromAI=true`);
                  } catch (error) {
                    console.error('Failed to parse workflow JSON:', error);
                    snackbar.error('Failed to parse automation JSON');
                  }
                }}
                icon={<SafeIcon src={SaveIconOutlineselect} alt='apply' width={16} height={16} />}
              />
            </Box>
          )}
          <Box key={'index'} fontSize={'12px'} fontWeight={400} color={colors.text.secondary}>
            <LLMAnswerRenderer
              toolCall={props.toolCall}
              messages={props.messages}
              onNavigateToTask={props.onNavigateToTask}
              groupIndex={props.groupIndex}
            />
          </Box>
          <Box display={'flex'} alignItems={'center'} justifyContent={'space-between'} mt='12px'>
            {props.toolCall.type == 'response' && (
              <>
                <Box display={'flex'} alignItems={'center'} gap='10px'>
                  <CustomIconButton
                    onClick={props.handleShare}
                    variant={'no-border-white'}
                    sx={{
                      '&.custom_icon_button': {
                        padding: '7px !important',
                        borderRadius: '4px',
                        transition: 'all 0.2s ease',
                        '&:hover': {
                          backgroundColor: 'rgba(0, 0, 0, 0.04)',
                        },
                        img: {
                          filter: 'brightness(0) saturate(100%) invert(65%) sepia(3%) saturate(0%) hue-rotate(135deg) brightness(98%) contrast(85%)',
                        },
                      },
                    }}
                  >
                    <SafeIcon src={ShareIconBlue} alt='share icon' width={20} height={20} />
                  </CustomIconButton>

                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      height: '32px',
                      width: '32px',
                      borderRadius: '4px',
                      transition: 'all 0.2s ease',
                      '&:hover': {
                        backgroundColor: 'rgba(0, 0, 0, 0.04)',
                      },
                    }}
                  >
                    <CopyableText copyableText={props.toolCall.text} iconSize={15} iconOnly />
                  </Box>

                  {parsedReferences.length > 0 && (
                    <>
                      <Box sx={{ borderLeft: '1px solid #E5E7EB', height: '20px', mx: '4px' }} />
                      <Box
                        onMouseEnter={(e) => setReferencesAnchorEl(e.currentTarget)}
                        onClick={(e) => setReferencesAnchorEl(e.currentTarget)}
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: '6px',
                          cursor: 'pointer',
                          padding: '4px 8px',
                          borderRadius: '4px',
                          transition: 'all 0.2s ease',
                          '&:hover': {
                            backgroundColor: 'rgba(0, 0, 0, 0.04)',
                          },
                        }}
                      >
                        <Typography
                          sx={{
                            fontSize: '13px',
                            fontWeight: '500',
                            color: colors.text.primary,
                            fontFamily: '"Poppins", sans-serif',
                          }}
                        >
                          {getUniqueReferencesCount(parsedReferences)} source
                          {getUniqueReferencesCount(parsedReferences) !== 1 ? 's' : ''}
                        </Typography>
                        {parsedReferences.some((r) => r.type === 'file') && (
                          <FileDownloadIcon sx={{ fontSize: '16px', color: colors.primary, ml: '2px' }} />
                        )}
                      </Box>
                    </>
                  )}
                </Box>

                {/* Right side: Feedback */}
                <Box display={'flex'} alignItems={'center'} gap='8px'>
                  <Box
                    sx={{
                      '& button': {
                        minWidth: 'auto !important',
                        padding: '7px !important',
                        '& svg': {
                          fontSize: '20px !important',
                        },
                        '& .MuiButton-startIcon': {
                          margin: '0 !important',
                        },
                      },
                      '& .MuiButtonBase-root': {
                        fontSize: '0 !important',
                        '& span:not(.MuiButton-startIcon)': {
                          display: 'none !important',
                        },
                      },
                    }}
                  >
                    <FeedbackComponent onFeedbackSubmit={(feedbackObject) => aiCreateFeedback(feedbackObject)} sentFeedback={sentFeedback} />
                  </Box>
                </Box>
              </>
            )}
          </Box>
        </Grid>
      )}
      {props.toolCall.type != 'response' && props.toolCall.type != 'error' && toolName && (
        <>
          {agentRequest ? (
            <>
              <Grid item md={12}>
                {props.toolCall.type !== 'followup-question' && (
                  <CustomDivider
                    borderType='dashed'
                    borderWidth={'0.75px'}
                    borderColor={colors.border.secondary}
                    margin='4px 0px 20px 0px'
                    maxWidth={'855px'}
                  />
                )}
                <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                  {props.toolCall.type !== 'followup-question' && (
                    <Text
                      value={'Agent - ' + toolName + ''}
                      sx={{
                        fontWeight: '400',
                        mb: '4px',
                        fontFamily: 'Roboto',
                        fontSize: '13px',
                        borderBottom: `1px dashed ${colors.border.secondary}`,
                        borderImage: `repeating-linear-gradient(to right, ${colors.border.secondary} 0, ${colors.border.secondary} 10px, transparent 10px, transparent 20px) 10%`,
                      }}
                    />
                  )}
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                    {props.agentTokenData && <AgentTokenUsage agentData={props.agentTokenData} />}
                    {parsedReferences.length > 0 && (
                      <Box
                        onMouseEnter={(e) => setReferencesAnchorEl(e.currentTarget)}
                        onClick={(e) => setReferencesAnchorEl(e.currentTarget)}
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: '6px',
                          cursor: 'pointer',
                          padding: '4px 8px',
                          borderRadius: '4px',
                          transition: 'all 0.2s ease',
                          '&:hover': {
                            backgroundColor: 'rgba(0, 0, 0, 0.04)',
                          },
                        }}
                      >
                        <Typography
                          sx={{
                            fontSize: '13px',
                            fontWeight: '500',
                            color: colors.text.primary,
                            fontFamily: '"Poppins", sans-serif',
                          }}
                        >
                          {getUniqueReferencesCount(parsedReferences)} source
                          {getUniqueReferencesCount(parsedReferences) !== 1 ? 's' : ''}
                        </Typography>
                        {parsedReferences.some((r) => r.type === 'file') && (
                          <FileDownloadIcon sx={{ fontSize: '16px', color: colors.primary, ml: '2px' }} />
                        )}
                      </Box>
                    )}
                    <Duration createdAt={props.toolCall.created_at} updatedAt={props.toolCall.updated_at} />
                  </Box>
                </Box>
              </Grid>
              {props.toolCall.type !== 'followup-question' && (
                <Grid item md={10}>
                  <Typography
                    sx={{
                      marginBottom: '12px',
                      color: colors.darkPrimary,
                      fontSize: '12px',
                      wordBreak: 'break-all',
                    }}
                  >
                    {agentRequest}
                  </Typography>
                </Grid>
              )}

              {props.toolCall.toolParameters?.command && (
                <>
                  <Grid item md={12} mt={2}>
                    <Text value={'Command'} sx={{ fontWeight: '400', mb: '4px', fontFamily: 'Roboto', fontSize: '13px' }} />
                  </Grid>
                  <Grid item md={12}>
                    <Box
                      sx={{
                        '& *': {
                          color: `${colors.darkPrimary} !important`,
                          fontSize: '12px !important',
                          padding: '0px !important',
                        },
                        '& p, & div, & span, & code': {
                          color: `${colors.darkPrimary} !important`,
                          fontSize: '12px !important',
                          fontFamily: 'Roboto !important',
                          padding: '0px !important',
                        },
                      }}
                    >
                      <MarkDowns
                        data={
                          typeof props.toolCall.toolParameters?.command === 'object'
                            ? JSON.stringify(props.toolCall.toolParameters?.command)
                            : props.toolCall.toolParameters?.command
                        }
                        sx={{ wordBreak: 'break-all', width: '100%', padding: '0px' }}
                      />
                    </Box>
                  </Grid>
                </>
              )}

              {props.toolCall.type !== 'followup-question' && (
                <Grid item md={10} mt='12px'>
                  <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-start', width: '100%', gap: '8px' }}>
                    <Text value='Agent Response' sx={{ fontWeight: '400', mb: '4px', fontFamily: 'Roboto', fontSize: '13px' }} />
                    <CopyableText
                      copyableText={
                        props.toolCall?.response?.text || props.toolCall?.response_text || JSON.stringify(props.toolCall?.response || {}, null, 2)
                      }
                      sx={{
                        height: 'auto',
                        width: 'auto',
                        minWidth: '24px',
                        padding: '4px',
                        borderRadius: '4px',
                        '&:hover': {
                          backgroundColor: 'rgba(0, 0, 0, 0.04)',
                        },
                      }}
                    />
                  </Box>
                </Grid>
              )}
            </>
          ) : null}
          <Grid
            item
            md={12}
            sx={{
              '& p, & li': {
                fontSize: '12px',
                lineHeight: '22px',
                color: colors.darkPrimary,
                fontFamily: '"Poppins", sans-serif',
              },
            }}
          >
            <Box
              sx={{
                '& *': {
                  color: `${colors.darkPrimary} !important`,
                  fontSize: '12px !important',
                },
                '& p, & div, & span, & code': {
                  color: `${colors.darkPrimary} !important`,
                  fontSize: '12px !important',
                  fontFamily: 'Roboto !important',
                  padding: '0px',
                },
              }}
            >
              {getToolResponseCard()}
            </Box>
          </Grid>
        </>
      )}
      {/* References Popover */}
      {parsedReferences.length > 0 && (
        <ReferencesPopover
          anchorEl={referencesAnchorEl}
          open={Boolean(referencesAnchorEl)}
          onClose={() => setReferencesAnchorEl(null)}
          references={parsedReferences}
          accountId={props.accountId}
          conversationId={props.conversationId}
        />
      )}
    </Grid>
  );
};

KubernetesLLMRequestResponse.propTypes = {
  toolCall: PropTypes.object,
  generateQuestionText: PropTypes.string,
  accountId: PropTypes.string,
  handleShare: PropTypes.func,
  sessionId: PropTypes.string,
  conversationId: PropTypes.string,
  agentTokenData: PropTypes.object,
  selectedModel: PropTypes.object,
  followupReadOnlyKey: PropTypes.string,
};

export default KubernetesLLMRequestResponse;
