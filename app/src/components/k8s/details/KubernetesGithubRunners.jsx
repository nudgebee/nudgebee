/**
 * KubernetesGithubRunners — GitHub Actions self-hosted runner dashboard.
 *
 * Cluster-scoped view of the actions-runner-controller (ARC) install running in
 * the selected cluster. Panel layout follows the community dashboard
 * https://grafana.com/grafana/dashboards/25015.
 *
 * ARC ships in two incompatible flavours and this tab covers both, because a
 * cluster may run either (or, mid-migration, both at once):
 *
 *  - Summerwind (`RunnerDeployment` + `HorizontalRunnerAutoscaler`) exports
 *    `horizontalrunnerautoscaler_*`, `runnerdeployment_*`, `github_rate_limit*`.
 *    Sections: Runner Pool, Job Throughput, Controller Health.
 *  - ARC v2 (`gha-runner-scale-set`) exports `gha_*` from the AutoscalingListener
 *    and the v2 controller — this is the flavour 25015 was written against, and
 *    the only one carrying real job-level timings.
 *    Sections: Runner Scale Sets, Job Analytics.
 *
 * Sections with no series are hidden, so each cluster shows only what it runs.
 *
 * Both flavours require the controller to be scraped by the cluster's metrics
 * provider. Summerwind serves on port name `metrics-port` (needs
 * `metrics.proxy.enabled=false`); ARC v2 serves on port name `metrics` and is
 * OFF unless the chart's `metrics:` block is set — there is no `enabled` flag,
 * and omitting the block passes empty --metrics-addr flags that silently
 * disable it.
 */
import React, { useState, useEffect } from 'react';
import PropTypes from 'prop-types';
import { Box, Grid } from '@mui/material';
import { ListingLayout } from '@ui/ListingLayout';
import { Card } from '@ui/Card';
import { Stat } from '@ui/Stat';
import Chart from '@ui/Chart';
import Skeleton from '@ui/Skeleton';
import Banner from '@ui/Banner';
import Heading from '@components/common/Heading';
import EmptyData from '@shared/EmptyData';
import { DataNotAvailable } from '@assets';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import { determineAndFormatTime, getLast24Hrs, isWithinTimeFrame } from '@lib/datetime';
import observability from '@api1/observability';

// The relay rewrites `__CLUSTER__` into the account's Prometheus cluster matcher
// (`cluster="…" , `) before the query reaches the agent, and into an empty string
// for accounts with a dedicated Prometheus — so these selectors stay correct on a
// shared multi-cluster backend. Same idiom as KubernetesNodesTrends.
const CLUSTER = '__CLUSTER__';

// controller_runtime_* / workqueue_* are generic controller-runtime metrics that
// every operator in the cluster exports, so they have to be pinned to the ARC
// controller's scrape job. PromQL anchors regexes at both ends, so the pattern
// matches `<namespace>/actions-runner-controller` but not the sibling
// `…-actions-metrics-server` job.
const ARC_JOB = `${CLUSTER} job=~".*actions-runner-controller"`;

// Instant queries — the "right now" strip above the charts.
const SUMMARY_STATS = [
  {
    key: 'registered',
    label: 'Registered Runners',
    expr: `sum(horizontalrunnerautoscaler_runners_registered{${CLUSTER}})`,
    info: 'Runners currently registered with GitHub across every runner deployment in this cluster.',
  },
  {
    key: 'busy',
    label: 'Busy Runners',
    expr: `sum(horizontalrunnerautoscaler_runners_busy{${CLUSTER}})`,
    info: 'Registered runners currently executing a job.',
  },
  {
    key: 'idle',
    label: 'Idle Runners',
    expr: `clamp_min(sum(horizontalrunnerautoscaler_runners_registered{${CLUSTER}}) - sum(horizontalrunnerautoscaler_runners_busy{${CLUSTER}}), 0)`,
    info: 'Registered runners waiting for work.',
  },
  {
    key: 'desired',
    label: 'Desired Runners',
    expr: `sum(horizontalrunnerautoscaler_replicas_desired{${CLUSTER}})`,
    info: 'Replica count the autoscaler is currently asking for.',
  },
  {
    key: 'minReplicas',
    label: 'Min Replicas',
    expr: `sum(horizontalrunnerautoscaler_spec_min_replicas{${CLUSTER}})`,
    info: 'Sum of the configured autoscaler floors.',
  },
  {
    key: 'maxReplicas',
    label: 'Max Replicas',
    expr: `sum(horizontalrunnerautoscaler_spec_max_replicas{${CLUSTER}})`,
    info: 'Sum of the configured autoscaler ceilings — the pool cannot grow past this.',
  },
  {
    key: 'runsQueued',
    label: 'Workflow Runs Queued',
    expr: `sum(horizontalrunnerautoscaler_workflow_runs_queued{${CLUSTER}})`,
    info: 'Workflow runs waiting for a runner. Sustained non-zero values with runners at Max Replicas means the pool is undersized.',
  },
  {
    key: 'runsInProgress',
    label: 'Workflow Runs In Progress',
    expr: `sum(horizontalrunnerautoscaler_workflow_runs_in_progress{${CLUSTER}})`,
    info: 'Workflow runs currently executing on this cluster’s runners.',
  },
  {
    key: 'rateLimitRemaining',
    label: 'GitHub API Calls Left',
    expr: `min(github_rate_limit_remaining{${ARC_JOB}})`,
    info: 'Remaining GitHub API budget for the controller’s token. Exhausting it stalls scaling decisions.',
  },
];

// Range queries — one entry per chart. `legendLabel` names the PromQL label that
// distinguishes the series inside a target (from its `sum by (…)` clause).
const PANELS = [
  {
    id: 'runner-pool',
    section: 'Runner Pool',
    title: 'Registered / Busy / Idle by Runner Deployment',
    targets: [
      {
        refId: 'registered',
        name: 'Registered',
        legendLabel: 'name',
        expr: `sum by (name)(horizontalrunnerautoscaler_runners_registered{${CLUSTER}})`,
      },
      {
        refId: 'busy',
        name: 'Busy',
        legendLabel: 'name',
        expr: `sum by (name)(horizontalrunnerautoscaler_runners_busy{${CLUSTER}})`,
      },
      {
        refId: 'idle',
        name: 'Idle',
        legendLabel: 'name',
        expr: `clamp_min(sum by (name)(horizontalrunnerautoscaler_runners_registered{${CLUSTER}}) - sum by (name)(horizontalrunnerautoscaler_runners_busy{${CLUSTER}}), 0)`,
      },
    ],
  },
  {
    id: 'autoscaler',
    section: 'Runner Pool',
    title: 'Autoscaler — Desired vs Bounds',
    targets: [
      {
        refId: 'desired',
        name: 'Desired',
        legendLabel: 'name',
        expr: `sum by (name)(horizontalrunnerautoscaler_replicas_desired{${CLUSTER}})`,
      },
      {
        refId: 'necessary',
        name: 'Necessary',
        legendLabel: 'name',
        expr: `sum by (name)(horizontalrunnerautoscaler_necessary_replicas{${CLUSTER}})`,
      },
      {
        refId: 'min',
        name: 'Min',
        legendLabel: 'horizontalrunnerautoscaler',
        expr: `sum by (horizontalrunnerautoscaler)(horizontalrunnerautoscaler_spec_min_replicas{${CLUSTER}})`,
      },
      {
        refId: 'max',
        name: 'Max',
        legendLabel: 'horizontalrunnerautoscaler',
        expr: `sum by (horizontalrunnerautoscaler)(horizontalrunnerautoscaler_spec_max_replicas{${CLUSTER}})`,
      },
    ],
  },
  {
    id: 'replicas',
    section: 'Runner Pool',
    title: 'Runner Deployment Replicas & Terminating Busy Runners',
    targets: [
      {
        refId: 'replicas',
        name: 'Replicas',
        legendLabel: 'runnerdeployment',
        expr: `sum by (runnerdeployment)(runnerdeployment_spec_replicas{${CLUSTER}})`,
      },
      {
        refId: 'terminating',
        name: 'Terminating busy',
        legendLabel: 'name',
        expr: `sum by (name)(horizontalrunnerautoscaler_terminating_busy{${CLUSTER}})`,
      },
    ],
  },
  {
    id: 'workflow-runs',
    section: 'Job Throughput',
    title: 'Workflow Runs — Queued vs In Progress by Repository',
    targets: [
      {
        refId: 'queued',
        name: 'Queued',
        legendLabel: 'repository',
        expr: `sum by (repository)(horizontalrunnerautoscaler_workflow_runs_queued{${CLUSTER}})`,
      },
      {
        refId: 'inProgress',
        name: 'In progress',
        legendLabel: 'repository',
        expr: `sum by (repository)(horizontalrunnerautoscaler_workflow_runs_in_progress{${CLUSTER}})`,
      },
    ],
  },
  {
    id: 'workflow-completions',
    section: 'Job Throughput',
    title: 'Workflow Runs Completed (per minute)',
    targets: [
      {
        refId: 'completed',
        name: 'Completed/min',
        legendLabel: 'repository',
        expr: `sum by (repository)(rate(horizontalrunnerautoscaler_workflow_runs_completed{${CLUSTER}}[5m])) * 60`,
      },
    ],
  },
  {
    id: 'rate-limit',
    section: 'Job Throughput',
    title: 'GitHub API Rate Limit',
    targets: [
      { refId: 'remaining', name: 'Remaining', expr: `min(github_rate_limit_remaining{${ARC_JOB}})` },
      { refId: 'limit', name: 'Limit', expr: `max(github_rate_limit{${ARC_JOB}})` },
    ],
  },
  {
    id: 'reconcile-rate',
    section: 'Controller Health',
    title: 'Reconcile Rate by Controller',
    targets: [
      {
        refId: 'rate',
        name: 'Reconciles/s',
        legendLabel: 'controller',
        expr: `sum by (controller, result)(rate(controller_runtime_reconcile_total{${ARC_JOB}}[5m]))`,
      },
    ],
  },
  {
    id: 'reconcile-duration',
    section: 'Controller Health',
    title: 'Reconcile Duration (p50 / p95)',
    targets: [
      {
        refId: 'p50',
        name: 'p50 (s)',
        legendLabel: 'controller',
        expr: `histogram_quantile(0.50, sum by (le, controller)(rate(controller_runtime_reconcile_time_seconds_bucket{${ARC_JOB}}[5m])))`,
      },
      {
        refId: 'p95',
        name: 'p95 (s)',
        legendLabel: 'controller',
        expr: `histogram_quantile(0.95, sum by (le, controller)(rate(controller_runtime_reconcile_time_seconds_bucket{${ARC_JOB}}[5m])))`,
      },
    ],
  },
  {
    id: 'workqueue',
    section: 'Controller Health',
    title: 'Active Workers & Work Queue Depth',
    targets: [
      {
        refId: 'workers',
        name: 'Active workers',
        legendLabel: 'controller',
        expr: `sum by (controller)(controller_runtime_active_workers{${ARC_JOB}})`,
      },
      {
        refId: 'depth',
        name: 'Queue depth',
        legendLabel: 'name',
        expr: `sum by (name)(workqueue_depth{${ARC_JOB}})`,
      },
    ],
  },

  // ---------------------------------------------------------------------------
  // ARC v2 (gha-runner-scale-set). These come from the AutoscalingListener and
  // the v2 controller — components that do not exist in the Summerwind
  // (RunnerDeployment/HorizontalRunnerAutoscaler) flavour, so on a cluster
  // running only Summerwind these panels have no series and their sections are
  // hidden. Metric names are globally unique, so unlike controller_runtime_* /
  // workqueue_* they need no job selector and stay correct however the cluster
  // happens to name its scrape jobs.
  // ---------------------------------------------------------------------------
  {
    id: 'scaleset-pool',
    section: 'Runner Scale Sets',
    title: 'Scale Set Runners — Registered / Busy / Idle',
    targets: [
      { refId: 'registered', name: 'Registered', legendLabel: 'name', expr: `sum by (name)(gha_registered_runners{${CLUSTER}})` },
      { refId: 'busy', name: 'Busy', legendLabel: 'name', expr: `sum by (name)(gha_busy_runners{${CLUSTER}})` },
      { refId: 'idle', name: 'Idle', legendLabel: 'name', expr: `sum by (name)(gha_idle_runners{${CLUSTER}})` },
    ],
  },
  {
    id: 'scaleset-bounds',
    section: 'Runner Scale Sets',
    title: 'Scale Set Autoscaler — Desired vs Bounds',
    targets: [
      { refId: 'desired', name: 'Desired', legendLabel: 'name', expr: `sum by (name)(gha_desired_runners{${CLUSTER}})` },
      { refId: 'min', name: 'Min', legendLabel: 'name', expr: `sum by (name)(gha_min_runners{${CLUSTER}})` },
      { refId: 'max', name: 'Max', legendLabel: 'name', expr: `sum by (name)(gha_max_runners{${CLUSTER}})` },
    ],
  },
  {
    id: 'ephemeral-runners',
    section: 'Runner Scale Sets',
    title: 'Ephemeral Runner Lifecycle',
    targets: [
      { refId: 'pending', name: 'Pending', expr: `sum(gha_controller_pending_ephemeral_runners{${CLUSTER}})` },
      { refId: 'running', name: 'Running', expr: `sum(gha_controller_running_ephemeral_runners{${CLUSTER}})` },
      { refId: 'failed', name: 'Failed', expr: `sum(gha_controller_failed_ephemeral_runners{${CLUSTER}})` },
      { refId: 'listeners', name: 'Listeners', expr: `sum(gha_controller_running_listeners{${CLUSTER}})` },
    ],
  },
  {
    id: 'scaleset-jobs',
    section: 'Job Analytics',
    title: 'Jobs In Flight — Assigned vs Running',
    targets: [
      { refId: 'assigned', name: 'Assigned', legendLabel: 'repository', expr: `sum by (repository)(gha_assigned_jobs{${CLUSTER}})` },
      { refId: 'running', name: 'Running', legendLabel: 'repository', expr: `sum by (repository)(gha_running_jobs{${CLUSTER}})` },
    ],
  },
  {
    id: 'scaleset-job-rate',
    section: 'Job Analytics',
    title: 'Job Rate (per minute)',
    targets: [
      {
        refId: 'started',
        name: 'Started/min',
        legendLabel: 'repository',
        expr: `sum by (repository)(rate(gha_started_jobs_total{${CLUSTER}}[5m])) * 60`,
      },
      {
        refId: 'completed',
        name: 'Completed/min',
        legendLabel: 'repository',
        expr: `sum by (repository)(rate(gha_completed_jobs_total{${CLUSTER}}[5m])) * 60`,
      },
    ],
  },
  {
    id: 'job-duration',
    section: 'Job Analytics',
    title: 'Job Queue Wait & Execution Time (p50 / p95)',
    // `increase(…[1h])` rather than `rate(…[5m])` — deliberately, and matching the
    // source dashboard. Job events are sparse: with no job in the last 5 minutes
    // every bucket rate is 0, and histogram_quantile over an all-zero histogram is
    // NaN, which renders as an empty panel on an otherwise healthy cluster.
    targets: [
      {
        refId: 'startupP50',
        name: 'Queue wait p50 (s)',
        expr: `histogram_quantile(0.50, sum by (le)(increase(gha_job_startup_duration_seconds_bucket{${CLUSTER}}[1h])))`,
      },
      {
        refId: 'startupP95',
        name: 'Queue wait p95 (s)',
        expr: `histogram_quantile(0.95, sum by (le)(increase(gha_job_startup_duration_seconds_bucket{${CLUSTER}}[1h])))`,
      },
      {
        refId: 'execP50',
        name: 'Execution p50 (s)',
        expr: `histogram_quantile(0.50, sum by (le)(increase(gha_job_execution_duration_seconds_bucket{${CLUSTER}}[1h])))`,
      },
      {
        refId: 'execP95',
        name: 'Execution p95 (s)',
        expr: `histogram_quantile(0.95, sum by (le)(increase(gha_job_execution_duration_seconds_bucket{${CLUSTER}}[1h])))`,
      },
    ],
  },

  // ---------------------------------------------------------------------------
  // Per-CI-workflow breakdown. `job_workflow_name` is the workflow FILE name
  // without extension (ParseWorkflowRef), e.g. `app-dev-gke` — not the `name:`
  // field ("App-Dev-GKE CI").
  //
  // This label is NOT emitted by default: the listener builds it in jobLabels()
  // but defaultMetrics' per-metric allowlist drops it. It requires
  // `listenerMetrics` on the AutoscalingRunnerSet. Without it these panels are
  // empty AND the Job Analytics panels above are ambiguous — job_name alone is
  // not unique (in this repo 74 workflows share 23 job keys; `lint` belongs to
  // 13 workflows and `app-build` to 26, so they all collapse into one series).
  // ---------------------------------------------------------------------------
  {
    id: 'ci-executions',
    section: 'CI Workflows',
    title: 'Executions by CI Workflow',
    targets: [
      {
        refId: 'runs',
        name: 'Runs',
        legendLabel: 'job_workflow_name',
        expr: `sum by (job_workflow_name)(increase(gha_completed_jobs_total{${CLUSTER}}[1h]))`,
      },
    ],
  },
  {
    id: 'ci-outcomes',
    section: 'CI Workflows',
    title: 'Outcomes by CI Workflow',
    targets: [
      {
        refId: 'outcome',
        name: 'Runs',
        legendLabel: 'job_workflow_name',
        expr: `sum by (job_workflow_name, job_result)(increase(gha_completed_jobs_total{${CLUSTER}}[1h]))`,
      },
    ],
  },
  {
    id: 'ci-duration',
    section: 'CI Workflows',
    title: 'Execution Time by CI Workflow (p95)',
    targets: [
      {
        refId: 'p95',
        name: 'p95 (s)',
        legendLabel: 'job_workflow_name',
        expr: `histogram_quantile(0.95, sum by (le, job_workflow_name)(increase(gha_job_execution_duration_seconds_bucket{${CLUSTER}}[1h])))`,
      },
    ],
  },
];

const SECTIONS = ['Runner Pool', 'Job Throughput', 'Controller Health', 'Runner Scale Sets', 'Job Analytics', 'CI Workflows'];

const rangeQueryKey = (panelId, refId) => `${panelId}::${refId}`;

const seriesLabel = (target, metric) => {
  const distinguisher = target.legendLabel ? metric?.[target.legendLabel] : undefined;
  // Two panels group by a second label they don't legend on: `reconcile-rate` by
  // (controller, result) and `ci-outcomes` by (job_workflow_name, job_result).
  // Append whichever is present so success/failure lines stay tellable apart.
  const outcome = metric?.result ?? metric?.job_result;
  const suffix = outcome ? ` (${outcome})` : '';
  return distinguisher ? `${target.name} · ${distinguisher}${suffix}` : `${target.name}${suffix}`;
};

/** Collapse the results of one panel's targets into Chart.Line's {data, labels, chartLabel}. */
const buildPanelChart = (panel, resultsByKey, shortRange) => {
  const series = [];
  panel.targets.forEach((target) => {
    const payload = resultsByKey[rangeQueryKey(panel.id, target.refId)] ?? [];
    payload.forEach((row) => {
      series.push({ target, row });
    });
  });

  if (series.length === 0) {
    return { data: [], labels: [], chartLabel: [] };
  }

  // Targets are evaluated over the same range and step, but a series that only
  // exists for part of the window has fewer points — align every series onto the
  // union timeline so the x-axis stays honest.
  const timeline = [...new Set(series.flatMap(({ row }) => row.timestamps ?? []))].sort((a, b) => a - b);

  const data = series.map(({ row }) => {
    const byTimestamp = new Map((row.timestamps ?? []).map((ts, idx) => [ts, row.values?.[idx]]));
    return timeline.map((ts) => byTimestamp.get(ts) ?? null);
  });

  return {
    data,
    labels: timeline.map((ts) => determineAndFormatTime(ts, shortRange)),
    chartLabel: series.map(({ target, row }) => seriesLabel(target, row.metric)),
  };
};

const KubernetesGithubRunners = ({ accountId }) => {
  const [dateRange, setDateRange] = useState({
    startDate: getLast24Hrs().getTime(),
    endDate: new Date().getTime(),
  });
  const [stats, setStats] = useState(null);
  const [charts, setCharts] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  useEffect(() => {
    if (!accountId) {
      return undefined;
    }

    // Guards the stale-response race when the date range changes mid-flight.
    let cancelled = false;
    setLoading(true);
    setError(null);

    const statQueries = SUMMARY_STATS.reduce((acc, stat) => ({ ...acc, [stat.key]: stat.expr }), {});
    const rangeQueries = PANELS.reduce(
      (acc, panel) => panel.targets.reduce((inner, target) => ({ ...inner, [rangeQueryKey(panel.id, target.refId)]: target.expr }), acc),
      {}
    );

    const toResultsByKey = (res) =>
      (res?.data?.data?.metrics_list?.results ?? []).reduce((acc, result) => ({ ...acc, [result.query_key]: result.payload ?? [] }), {});

    // A failed query does NOT reject: the gateway answers 200 with either a
    // top-level `errors` array or a per-query `error` on the result. Without
    // this both shapes look identical to "the cluster runs no runners", so the
    // empty state would claim ARC is absent when Prometheus is simply down.
    const collectErrors = (res) => [
      ...(res?.data?.errors ?? []).map((e) => e?.message).filter(Boolean),
      ...(res?.data?.data?.metrics_list?.results ?? []).map((result) => result?.error).filter(Boolean),
    ];

    Promise.all([
      observability.metricsQuery({
        account_id: accountId,
        queries: statQueries,
        instant: true,
        start_time: dateRange.startDate,
        end_time: dateRange.endDate,
      }),
      observability.metricsQuery({
        account_id: accountId,
        queries: rangeQueries,
        start_time: dateRange.startDate,
        end_time: dateRange.endDate,
      }),
    ])
      .then(([statRes, rangeRes]) => {
        if (cancelled) {
          return;
        }
        // Surfaced alongside whatever did come back — a partial failure still
        // renders its healthy panels rather than blanking the whole tab.
        const errors = [...new Set([...collectErrors(statRes), ...collectErrors(rangeRes)])];
        if (errors.length > 0) {
          setError(errors.join('\n'));
        }

        const statResults = toResultsByKey(statRes);
        setStats(
          SUMMARY_STATS.reduce((acc, stat) => {
            const value = statResults[stat.key]?.[0]?.values?.[0];
            return { ...acc, [stat.key]: value === undefined ? null : value };
          }, {})
        );

        const rangeResults = toResultsByKey(rangeRes);
        const shortRange = isWithinTimeFrame(dateRange.startDate, dateRange.endDate, 24);
        setCharts(PANELS.map((panel) => ({ panel, chart: buildPanelChart(panel, rangeResults, shortRange) })));
      })
      .catch(() => {
        if (!cancelled) {
          setError('Failed to fetch GitHub runner metrics. The cluster may be unreachable or its metrics provider may be down.');
          setStats({});
          setCharts([]);
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [accountId, dateRange.startDate, dateRange.endDate]);

  const hasRunnerData = charts?.some(({ chart }) => chart.data.length > 0) || Object.values(stats ?? {}).some((value) => value !== null);

  const renderBody = () => {
    if (loading) {
      return (
        <Grid container spacing={2} mt={0}>
          {[0, 1, 2, 3].map((key) => (
            <Grid item xs={12} md={6} key={key}>
              <Skeleton shape='rect' height={300} width='100%' />
            </Grid>
          ))}
        </Grid>
      );
    }

    // "No runners" is only claimed when the queries actually succeeded and came
    // back empty. On failure the banner below carries the real reason instead.
    if (!hasRunnerData && !error) {
      return (
        <EmptyData img={DataNotAvailable} heading='No GitHub Actions Runners Found' id='github-runners-empty'>
          <p>
            This cluster is not reporting actions-runner-controller metrics. Enable metrics on the ARC controller and scrape its
            <code> metrics-port</code> with the cluster&apos;s metrics provider.
          </p>
        </EmptyData>
      );
    }

    return (
      <>
        {error && (
          <Box mb={'var(--ds-space-3)'}>
            <Banner id='github-runners-error' tone='critical' surface='section' title='Could not load runner metrics' message={error} />
          </Box>
        )}
        {hasRunnerData && (
          <>
            <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-3)' }} id='github-runners-kpi-row'>
              {SUMMARY_STATS.map((stat) => (
                <Card key={stat.key} sx={{ flex: '1 1 180px', minWidth: 160 }}>
                  <Stat
                    id={`github-runners-stat-${stat.key}`}
                    label={stat.label}
                    value={stats?.[stat.key] ?? '—'}
                    size='md'
                    info={{ tooltip: stat.info }}
                  />
                </Card>
              ))}
            </Box>
            {SECTIONS.map((section) => {
              const sectionCharts = charts.filter(({ panel }) => panel.section === section);
              // A cluster runs Summerwind ARC or the v2 scale-set flavour, rarely
              // both, so roughly half these sections are structurally empty on any
              // given cluster. Drop a section with no series entirely rather than
              // render a wall of blank charts that reads as a broken dashboard.
              if (!sectionCharts.some(({ chart }) => chart.data.length > 0)) {
                return null;
              }
              return (
                <Box key={section} mt={'var(--ds-space-5)'}>
                  <Heading value={section} borderWidth='md' />
                  <Grid container spacing={2}>
                    {sectionCharts.map(({ panel, chart }) => (
                      <Grid item xs={12} lg={6} key={panel.id}>
                        <Heading value={panel.title} borderWidth='sm' />
                        <Chart.Line
                          id={`github-runners-chart-${panel.id}`}
                          data={chart.data}
                          labels={chart.labels}
                          chartLabel={chart.chartLabel}
                          legendOptions={{ renderer: 'html' }}
                        />
                      </Grid>
                    ))}
                  </Grid>
                </Box>
              );
            })}
          </>
        )}
      </>
    );
  };

  return (
    <ListingLayout id='github-runners'>
      <ListingLayout.Toolbar
        actions={
          <CustomDateTimeRangePicker
            onChange={(ranges) =>
              setDateRange({
                startDate: ranges.selection.startTime,
                endDate: ranges.selection.endTime,
              })
            }
            passedSelectedDateTime={{
              startTime: dateRange.startDate,
              endTime: dateRange.endDate,
              shortcutClickTime: 0,
            }}
          />
        }
      />
      <ListingLayout.Body>{renderBody()}</ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesGithubRunners.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default KubernetesGithubRunners;
