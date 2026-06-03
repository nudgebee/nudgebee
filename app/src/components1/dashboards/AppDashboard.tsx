import PropTypes from 'prop-types';
import { LineChart } from '@components1/common';
import { Grid, Typography } from '@mui/material';
import ListingLayout from '@components1/ds/ListingLayout';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import { useEffect, useState } from 'react';
import CustomTable from '@common-new/tables/CustomTable2';
import { convertNumberToTimestamp } from 'src/utils/common';
import { getLast24Hrs } from '@lib/datetime';
import Title from '@components1/common/Title';
import EmptyData from '@components1/common/EmptyData';
import { DataNotAvailable } from '@assets';
import Loader from '@components1/common/Loader';
import observability from '@api1/observability';
import apiKubernetes1 from '@api1/kubernetes1';

export const getDashboardData = async (app: string, dashboardName = ''): Promise<DashboardModel | null> => {
  app = app.toLowerCase();
  let mod = null;
  if (app === 'golang' || app === 'go') {
    mod = await import('./apps/otel_golang.json');
  } else if (app === 'jvm' || app === 'java') {
    if (dashboardName === 'nb-jvm') {
      mod = await import('./apps/nb_jvm.json');
    } else {
      mod = await import('./apps/otel_jvm.json');
    }
  } else if (app === 'nodejs') {
    mod = await import('./apps/otel_nodejs.json');
  } else if (app === 'redis') {
    if (dashboardName === 'redis') {
      mod = await import('./apps/nb_redis.json');
    } else {
      mod = await import('./apps/prom_redis.json');
    }
  } else if (app === 'rabbitmq') {
    if (dashboardName === 'rabbitmq') {
      mod = await import('./apps/nb_rabbitmq.json');
    } else {
      mod = await import('./apps/prom_rabbitmq.json');
    }
  } else if (app === 'python') {
    if (dashboardName === 'nb-python') {
      mod = await import('./apps/nb_python.json');
    } else {
      mod = await import('./apps/otel_python.json');
    }
  } else if (app === 'nginx') {
    mod = await import('./apps/nginx.json');
  } else if (app === 'postgres-exporter') {
    mod = await import('./apps/postgres.json');
  } else if (app === 'postgres') {
    mod = await import('./apps/nb_postgres.json');
  } else if (app === 'mysql') {
    mod = await import('./apps/nb_mysql.json');
  } else if (app === 'clickhouse') {
    mod = await import('./apps/nb_clickhouse.json');
  } else if (app === 'kafka') {
    mod = await import('./apps/nb_kafka.json');
  } else if (app === 'mongodb') {
    if (dashboardName === 'mongodb') {
      mod = await import('./apps/nb_mongodb.json');
    } else {
      mod = await import('./apps/prom_mongo_metrics.json');
    }
  } else if (app === 'mongodb-hw') {
    mod = await import('./apps/prom_mongo_hw_metrics.json');
  }

  if (mod != null) {
    return JSON.parse(JSON.stringify(mod.default));
  }
  return null;
};

interface DashboardPanelModel {
  aliasColors?: any;
  bars?: boolean;
  dashLength?: number;
  dashes?: boolean;
  fill?: number;
  fillGradient?: number;
  gridPos: { h: number; w: number; x: number; y: number };
  id: number;
  legend?: { avg?: boolean; current?: boolean; max?: boolean; min?: boolean; show?: boolean; total?: boolean; values?: boolean };
  lines?: boolean;
  linewidth?: number;
  links?: any[];
  nullPointMode?: string;
  percentage?: boolean;
  pointradius?: number;
  points?: boolean;
  renderer?: string;
  seriesOverrides?: any[];
  spaceLength?: number;
  stack?: boolean;
  steppedLine?: boolean;
  targets?: {
    expr: string;
    format?: string;
    intervalFactor?: number;
    legendFormat?: string;
    refId: string;
    step?: number;
  }[];
  thresholds?: string | any[];
  timeFrom?: any;
  timeShift?: any;
  title: string;
  tooltip?: { shared: boolean; sort: number; value_type: string };
  type?: string;
  xaxis?: { buckets?: null; mode?: string; name?: null; show?: boolean; values?: any[] };
  yaxes?: {
    format?: string | null;
    label?: string | null;
    logBase?: number | null;
    max?: number | null | string;
    min?: number | null | string;
    show?: boolean;
  }[];
  yaxis?: { align: boolean; alignLevel: null };
  datasource?: string;
}

interface DashboardModel {
  __inputs: any[];
  __requires: any[];
  annotations: { list: any[] };
  editable: boolean;
  gnetId: number;
  graphTooltip: number;
  id: number | null;
  iteration: number;
  links: any[];
  panels: DashboardPanelModel[];
  refresh?: string;
  schemaVersion: number;
  style: string;
  tags: string[];
  templating: { list: any[] };
  time: { from: string; to: string };
  timepicker: { refresh_intervals: string[]; time_options: string[] };
  timezone: string;
  title: string;
  uid: string;
  version: number;
  description: string;
}

function evaluateTemplate(template: any, data: any) {
  try {
    let result = template;
    if (typeof template === 'object') {
      result = template.type;
    }
    for (const key in data) {
      result = result.replaceAll(key, data[key]);
    }
    return result;
  } catch (error) {
    console.error('evaluate error', template, data, error);
    return template;
  }
}

function DashboardPanel({
  config,
  accountId,
  namespaceName,
  workloadName,
  podName,
  templateConfig,
  dateRange,
}: {
  config: DashboardPanelModel;
  accountId: string;
  namespaceName: string;
  workloadName: string;
  podName?: string;
  templateConfig: any;
  dateRange: any;
}) {
  const [data, setData] = useState<any>(null);

  const fetchAndBuildData = async (
    target: {
      expr: string;
      format?: string;
      intervalFactor?: number;
      legendFormat?: string;
      refId: string;
      step?: number;
    }[],
    datasource: any
  ): Promise<any[]> => {
    if (datasource === 'prometheus' || datasource?.type == 'prometheus') {
      const refIdMap: any = {};
      const queries = target.map((t) => {
        refIdMap[t.refId] = t;
        return {
          query: t.expr,
          key: t.refId,
        };
      });

      const requestBody1 = {
        account_id: accountId,
        queries: queries.reduce((acc, q) => {
          return {
            ...acc,
            [q.key]: q.query,
          };
        }, {}),
        start_time: dateRange.startDate,
        end_time: dateRange.endDate,
      };

      const res = await observability.metricsQuery(requestBody1);

      const results = res?.data?.data?.metrics_list?.results || [];

      const chartResult: any = {};
      chartResult.type = 'time_series';
      chartResult.data = [];
      chartResult.labels = [];
      chartResult.chartLabel = [];

      let isLabelAssigned = false;
      for (const result of results) {
        const key = result.query_key;
        if (!isLabelAssigned) {
          chartResult.labels = (result?.payload?.[0]?.timestamps ?? []).map((t: any) => convertNumberToTimestamp(t * 1000));
          isLabelAssigned = true;
        }
        try {
          for (const series of result?.payload ?? []) {
            chartResult.data.push(series?.values ?? []);
            const legendKey = refIdMap[key]?.legendFormat;
            chartResult.chartLabel.push(legendKey ? series?.metric?.[legendKey] ?? key : key);
          }
        } catch (err) {
          console.error('unable to build chart', err);
        }
      }
      return chartResult;
    }
    return [];
  };

  useEffect(() => {
    if (!(accountId && namespaceName && (podName ?? workloadName))) {
      return;
    }

    async function pullData() {
      const datasource = evaluateTemplate(config.datasource ?? 'prometheus', templateConfig);
      const targets =
        config.targets?.map((target) => {
          return {
            ...target,
            expr: evaluateTemplate(target.expr, templateConfig),
          };
        }) ?? [];
      const targetsData: any[] = await fetchAndBuildData(targets, datasource);
      setData(targetsData);
    }

    pullData();
  }, [accountId, namespaceName, workloadName, podName, dateRange.startDate, dateRange.endDate]);

  return (
    <div>
      <Title title={config.title} />
      {data === null ? (
        <div className='shimmer' style={{ height: 300, width: '98%' }} />
      ) : (
        <div>
          {data.type === 'table' && <CustomTable headers={data.headers} tableData={data.data} />}
          {data.type === 'time_series' && (
            <LineChart data={data.data} labels={data.labels} chartLabel={data.chartLabel} legendOptions={{ renderer: 'html' }} />
          )}
          {data.type === 'json' && <div>{JSON.stringify(data)}</div>}
        </div>
      )}
    </div>
  );
}

export async function getDashboardStats({
  accountId,
  namespaceName,
  workloadName,
  podName,
}: {
  accountId: string;
  namespaceName: string;
  workloadName?: string;
  podName?: string;
}) {
  const d = new Date();
  const twelveHoursAgo = new Date(d.getTime() - 1000 * 60 * 60 * 24);

  const requestBody = {
    accountId: accountId,
    metrics: [podName ? 'container_application_type_with_pod' : 'container_application_type_with_workload'],
    startDate: twelveHoursAgo.getTime(),
    endDate: d.getTime(),
    namespaceName: namespaceName,
    workloadName: podName || workloadName,
  };

  const res = await apiKubernetes1.utilisationApi(requestBody);
  const series_list_result = res?.[0]?.payload || [];
  let applicationTypes = series_list_result
    .filter(
      (containerApplicationType: any) =>
        !(
          containerApplicationType?.metric?.container_id.includes('/metrics') ||
          containerApplicationType?.metric?.container_id.includes('/otc-container')
        )
    )
    .map((containerApplicationType: any) => {
      if (
        containerApplicationType?.metric?.container_id?.includes('prometheus-postgres-exporter') ||
        (containerApplicationType?.metric?.container_id?.includes('/metrics') &&
          containerApplicationType?.metric?.container_id?.includes('postgresql'))
      ) {
        return 'postgres-exporter';
      }
      return containerApplicationType?.metric?.application_type;
    })
    .filter(Boolean);

  applicationTypes = [...new Set(applicationTypes)];

  if (applicationTypes.length > 0) {
    let lang = applicationTypes[0];
    if (applicationTypes.length > 1) {
      //try to detect actual framework
      if (applicationTypes.includes('nginx')) {
        lang = 'nginx';
      }
    }

    if (lang) {
      let dashboardName = lang;
      if (lang == 'jvm' || lang == 'java') {
        dashboardName = 'nb-jvm';
        const res2 = await apiKubernetes1.utilisationApi({
          accountId: accountId,
          metrics: ['jvm_memory_metric_count'],
          namespaceName: namespaceName,
          startDate: twelveHoursAgo.getTime(),
          endDate: d.getTime(),
        });
        const series_list_result = res2?.[0]?.payload || [];
        for (const appDetails of series_list_result) {
          if (
            appDetails.metric['namespace'] == namespaceName &&
            ((workloadName && appDetails.metric['pod'].startsWith(workloadName)) || (podName && appDetails.metric['pod'].startsWith(podName)))
          ) {
            dashboardName = 'otel-jvm';
            break;
          }
        }
      } else if (lang == 'python') {
        dashboardName = 'nb-python';
        const res2 = await apiKubernetes1.utilisationApi({
          accountId: accountId,
          metrics: ['cpython_memory_metric_count'],
          namespaceName: namespaceName,
          startDate: twelveHoursAgo.getTime(),
          endDate: d.getTime(),
        });
        const series_list_result = res2?.[0]?.payload || [];
        for (const appDetails of series_list_result) {
          if (
            appDetails.metric['namespace'] == namespaceName &&
            ((workloadName && appDetails.metric['pod'].startsWith(workloadName)) || (podName && appDetails.metric['pod'].startsWith(podName)))
          ) {
            dashboardName = 'otel-python';
            break;
          }
        }
      } else if (lang == 'go' || lang == 'golang') {
        dashboardName = 'nb-go';
        const res2 = await apiKubernetes1.utilisationApi({
          accountId: accountId,
          metrics: ['go_heap_memory_metric_count'],
          namespaceName: namespaceName,
          startDate: twelveHoursAgo.getTime(),
          endDate: d.getTime(),
        });
        const series_list_result = res2?.[0]?.payload || [];
        for (const appDetails of series_list_result) {
          if (
            appDetails.metric['namespace'] == namespaceName &&
            ((workloadName && appDetails.metric['pod'].startsWith(workloadName)) || (podName && appDetails.metric['pod'].startsWith(podName)))
          ) {
            dashboardName = 'otel-go';
            break;
          }
        }
      } else if (lang == 'postgres') {
        dashboardName = 'nb_postgres';
      } else if (lang == 'redis') {
        dashboardName = 'redis';
      } else if (lang == 'rabbitmq') {
        dashboardName = 'rabbitmq';
      } else if (lang == 'nginx') {
        dashboardName = 'nginx';
      }

      return {
        lang: lang,
        available: !!dashboardName,
        dashboardName: dashboardName,
      };
    }
  }
  return {
    available: false,
  };
}

function AppDashboard({
  accountId,
  namespaceName,
  workloadName,
  podName,
  podIp,
  startDate,
  endDate,
  appType,
  dashboardName,
}: {
  accountId: string;
  namespaceName: string;
  workloadName: string;
  podName?: string;
  podIp?: string;
  startDate: Date;
  endDate: Date;
  appType?: string;
  dashboardName?: string;
}) {
  const [dashboardData, setDashboardData] = useState<DashboardModel | null>(null);
  const [templateData, setTemplateData] = useState<any>({});
  const [dateRange, setDateRange] = useState({
    startDate: startDate?.getTime() ?? getLast24Hrs().getTime(),
    endDate: endDate?.getTime() ?? new Date().getTime(),
  });
  const [workloadAppType, setWorkloadAppType] = useState(appType);
  const [workloadDashboardName, setWorkloadDashboardName] = useState(dashboardName ?? appType);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (workloadAppType && workloadDashboardName) {
      return;
    }
    setLoading(true);
    getDashboardStats({ accountId, namespaceName, workloadName, podName })
      .then((availabelData) => {
        if (availabelData.available) {
          setWorkloadAppType(availabelData.lang);
          setWorkloadDashboardName(availabelData?.dashboardName ?? '');
        }
      })
      .finally(() => {
        setLoading(false);
      });
  }, [workloadAppType, accountId, namespaceName, workloadName]);

  useEffect(() => {
    if (!workloadAppType) {
      return;
    }
    async function loadDashboard() {
      const data = await getDashboardData(workloadAppType!, workloadDashboardName);
      if (data != null) {
        data.__inputs = data.__inputs || [];
        if (podName) {
          data.__inputs.push({
            name: 'podNameFilter',
            type: 'constant',
            value: `pod="${podName}"`,
          });
        } else if (workloadName) {
          data.__inputs.push({
            name: 'podNameFilter',
            type: 'constant',
            value: `pod=~"${workloadName}-.*"`,
          });
        }
        data.__inputs.push({
          name: 'accountId',
          type: 'constant',
          value: accountId,
        });
        data.__inputs.push({
          name: 'workloadName',
          type: 'constant',
          value: workloadName,
        });
        data.__inputs.push({
          name: 'namespaceName',
          type: 'constant',
          value: namespaceName,
        });
        data.__inputs.push({
          name: 'namespace',
          type: 'constant',
          value: namespaceName,
        });
        data.__inputs.push({
          name: 'podName',
          type: 'constant',
          value: podName,
        });
        if (podName) {
          data.__inputs.push({
            name: 'sourceContainerFilter',
            type: 'constant',
            value: `container_id=~"/k8s/${namespaceName}/${podName}/.*"`,
          });
        } else {
          data.__inputs.push({
            name: 'sourceContainerFilter',
            type: 'constant',
            value: `container_id=~"/k8s/${namespaceName}/${workloadName}-.*/.*"`,
          });
        }
        if (podIp) {
          data.__inputs.push({
            name: 'destinationIpFilter',
            type: 'constant',
            value: `actual_destination=~"${podIp}.*"`,
          });
        } else {
          data.__inputs.push({
            name: 'destinationIpFilter',
            type: 'constant',
            value: '',
          });
        }
      }
      setDashboardData(data);
      const newTemplateData: any = {};
      if (data?.__inputs && data.__inputs.length > 0) {
        data.__inputs.forEach((input: any) => {
          if (input.type === 'constant') {
            newTemplateData['${' + input.name + '}'] = input.value;
            newTemplateData['$' + input.name] = input.value;
          } else if (input.type === 'datasource') {
            newTemplateData['${' + input.name + '}'] = input.pluginId;
            newTemplateData['$' + input.name] = input.pluginId;
          } else {
            console.error('Unknown input type: ', input);
          }
        });
      }
      if (data != null && data.templating != null && data.templating.list != null) {
        data.templating.list.forEach((template: any) => {
          if (template.type === 'query') {
            newTemplateData['${' + template.name + '}'] = evaluateTemplate(template.query, newTemplateData);
            newTemplateData['$' + template.name] = newTemplateData['${' + template.name + '}'];
          } else if (template.type === 'datasource') {
            newTemplateData['${' + template.name + '}'] = evaluateTemplate(template.query, newTemplateData);
            newTemplateData['$' + template.name] = newTemplateData['${' + template.name + '}'];
          } else if (template.type === 'constant') {
            newTemplateData['${' + template.name + '}'] = evaluateTemplate(template.query, newTemplateData);
            newTemplateData['$' + template.name] = newTemplateData['${' + template.name + '}'];
          } else if (template.type === 'interval') {
            newTemplateData['${' + template.name + '}'] = evaluateTemplate(template.query, newTemplateData);
            newTemplateData['$' + template.name] = newTemplateData['${' + template.name + '}'];
          } else {
            console.error('Unknown template type: ', template);
          }
        });
      }
      setTemplateData(newTemplateData);
    }
    loadDashboard();
  }, [workloadAppType]);

  const renderingContent = () => {
    if (loading) {
      return <Loader style={{ width: '100%' }} />;
    } else if (dashboardData) {
      return (
        <ListingLayout id='appDashboard'>
          <ListingLayout.Toolbar
            title={dashboardData.title}
            actions={
              <CustomDateTimeRangePicker
                onChange={(ranges: any) => {
                  setDateRange((prevDateRange) => ({
                    ...prevDateRange,
                    startDate: ranges.selection.startTime,
                    endDate: ranges.selection.endTime,
                  }));
                }}
                passedSelectedDateTime={{
                  startTime: dateRange.startDate,
                  endTime: dateRange.endDate,
                  shortcutClickTime: 0,
                }}
              />
            }
          />
          <ListingLayout.Body>
            {dashboardData.panels.map((panel) => (
              <Grid key={panel.id} container>
                <Grid item xs={12} mb={2}>
                  <DashboardPanel
                    config={panel}
                    accountId={accountId}
                    namespaceName={namespaceName}
                    workloadName={workloadName}
                    podName={podName}
                    templateConfig={templateData}
                    dateRange={dateRange}
                  />
                </Grid>
              </Grid>
            ))}
          </ListingLayout.Body>
        </ListingLayout>
      );
    }
    return (
      <ListingLayout id='appDashboardNoData'>
        <ListingLayout.Body>
          <EmptyData img={DataNotAvailable} heading='No Dashboard Available' id={'app-dashboard'}>
            <Typography>
              For Python, Java, NodeJs, Golang configure <a href='https://opentelemetry.io/docs/languages/'>OpenTelemetry exporter</a>.
            </Typography>
            <Typography>
              For Postgres, Mysql, MongoDB etc configure <a href='https://prometheus.io/docs/instrumenting/exporters/'>Prometheus exporter</a>.
            </Typography>
          </EmptyData>
        </ListingLayout.Body>
      </ListingLayout>
    );
  };

  //TODO based on width and height of the panel, calculate the number of panels that can be displayed in a row
  // and then display the panels in a grid layout
  return renderingContent();
}

AppDashboard.propTypes = {
  accountId: PropTypes.string.isRequired,
  namespaceName: PropTypes.string.isRequired,
  workloadName: PropTypes.string.isRequired,
};

export default AppDashboard;
