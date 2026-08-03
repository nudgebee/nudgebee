export interface ChartDataPointContext {
  dataValue: string | number;
  labelValue: string; // series label (e.g. "CPU Usage", "Memory Usage")
  label: string; // display timestamp string (e.g. "22:10:00")
  epochTimestamp?: number | null; // epoch ms for precise timestamp
  chartTitle?: string; // e.g. "CPU Utilization (Core)"
  metrics?: { min?: string; max?: string; p99?: string; avg?: string };
  metricQuery?: string; // actual PromQL query e.g. "sum(rate(container_cpu_usage_seconds_total{...}[5m]))"
  podName?: string;
  namespaceName?: string;
  workloadName?: string;
  workloadKind?: string; // e.g. "Deployment", "StatefulSet"
}

function formatTimestamp(ctx: ChartDataPointContext): string {
  if (ctx.epochTimestamp) {
    const d = new Date(ctx.epochTimestamp);
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    const local = d.toLocaleString('en-US', {
      timeZone: tz,
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    });
    return `${local} (${tz}) — epoch: ${Math.floor(ctx.epochTimestamp / 1000)}`;
  }
  return ctx.label;
}

export function buildNubiChartPrompt(ctx: ChartDataPointContext): string {
  const lines: string[] = [];

  lines.push('## Investigate Metric Spike');
  lines.push('');

  // Spike summary
  lines.push(`**Metric:** ${ctx.labelValue || 'Unknown'}`);
  if (ctx.metricQuery) {
    lines.push(`**Query:** \`${ctx.metricQuery}\``);
  }
  lines.push(`**Observed Value:** ${ctx.dataValue}`);
  lines.push(`**Timestamp:** ${formatTimestamp(ctx)}`);
  if (ctx.chartTitle) {
    lines.push(`**Chart:** ${ctx.chartTitle}`);
  }

  // Resource context
  const hasResourceInfo = ctx.podName || ctx.namespaceName || ctx.workloadName;
  if (hasResourceInfo) {
    lines.push('');
    lines.push('### Resource Context');
    if (ctx.podName) {
      lines.push(`- **Pod:** \`${ctx.podName}\``);
    }
    if (ctx.namespaceName) {
      lines.push(`- **Namespace:** \`${ctx.namespaceName}\``);
    }
    if (ctx.workloadName) {
      const kindLabel = ctx.workloadKind ? ` (${ctx.workloadKind})` : '';
      lines.push(`- **Workload:** \`${ctx.workloadName}\`${kindLabel}`);
    }
  }

  // Series statistics
  if (ctx.metrics) {
    const entries = Object.entries(ctx.metrics).filter(([, v]) => v != null && v !== '-');
    if (entries.length > 0) {
      lines.push('');
      lines.push('### Series Statistics');
      for (const [key, value] of entries) {
        lines.push(`- **${key}:** ${value}`);
      }
    }
  }

  // Investigation request
  lines.push('');
  lines.push('Please investigate what caused this spike and suggest next steps.');

  return lines.join('\n');
}

// Subset of the recommendation's alarm_config (providers.AlarmCreationConfig JSON)
// relevant for grounding assistant answers. All providers share these keys.
export interface AlarmConfigPromptContext {
  metric_name?: string;
  statistic?: string;
  threshold?: number;
  comparison_operator?: string;
  period?: number;
  evaluation_periods?: number;
  datapoints_to_alarm?: number;
  metrics?: { label?: string; expression?: string; return_data?: boolean }[];
}

export interface OptimizationRecommendationContext {
  ruleName: string;
  category: string;
  severity: string;
  resourceName: string;
  resourceType?: string;
  namespace?: string;
  accountName?: string;
  estimatedSavings?: number;
  brief?: string;
  alarmConfig?: AlarmConfigPromptContext;
}

const COMPARISON_OPERATOR_SYMBOLS: Record<string, string> = {
  GreaterThanThreshold: '>',
  COMPARISON_GT: '>',
  GreaterThanOrEqualToThreshold: '>=',
  COMPARISON_GE: '>=',
  LessThanThreshold: '<',
  COMPARISON_LT: '<',
  LessThanOrEqualToThreshold: '<=',
  COMPARISON_LE: '<=',
};

function formatSeconds(seconds: number): string {
  if (seconds > 0 && seconds % 60 === 0) {
    return `${seconds / 60} min`;
  }
  return `${seconds}s`;
}

// Renders the alarm parameters the Create button will actually apply, so the
// assistant's explanations and any policy JSON/CLI it emits match the product
// behavior instead of documentation defaults.
function buildAlarmConfigSection(cfg: AlarmConfigPromptContext): string[] {
  const lines: string[] = [];

  lines.push('');
  lines.push('### Alert Configuration (authoritative)');
  lines.push('These are the exact parameters used when this alert is created from the recommendation:');

  const expressionMetric = cfg.metrics?.find((m) => m.return_data && m.expression);
  const metricLabel = expressionMetric ? expressionMetric.label || 'metric-math expression' : cfg.metric_name;
  if (metricLabel) {
    lines.push(`- **Metric:** ${metricLabel}`);
  }
  if (cfg.statistic) {
    lines.push(`- **Statistic:** ${cfg.statistic}`);
  }
  if (cfg.comparison_operator && cfg.threshold != null) {
    const operator = COMPARISON_OPERATOR_SYMBOLS[cfg.comparison_operator] || cfg.comparison_operator;
    lines.push(`- **Condition:** value ${operator} ${cfg.threshold}`);
  }

  const period = cfg.period || 0;
  const evalPeriods = cfg.evaluation_periods || 0;
  if (period > 0 && evalPeriods > 0) {
    lines.push(`- **Evaluation period:** ${formatSeconds(period)}`);
    const datapoints = cfg.datapoints_to_alarm || 0;
    if (datapoints > 0 && datapoints < evalPeriods) {
      lines.push(
        `- **Effective trigger:** at least ${datapoints} of ${evalPeriods} evaluation periods (${formatSeconds(
          period
        )} each) must breach within ${formatSeconds(period * evalPeriods)} before the alert fires`
      );
    } else if (evalPeriods === 1) {
      lines.push(`- **Effective trigger:** the condition must hold for ${formatSeconds(period)} (a single evaluation period) before the alert fires`);
    } else {
      lines.push(
        `- **Effective trigger:** the condition must hold for ${formatSeconds(
          period * evalPeriods
        )} total (${evalPeriods} consecutive ${formatSeconds(
          period
        )} periods) before the alert fires — e.g. a GCP alert policy encodes this as duration (retest window) = ${
          period * evalPeriods
        }s, not ${period}s`
      );
    }
  }

  lines.push('');
  lines.push(
    'If you output any alert configuration (JSON, CLI, Terraform), derive every field from the values above — do not substitute typical documentation defaults.'
  );

  return lines;
}

export function buildNubiOptimizePrompt(ctx: OptimizationRecommendationContext): string {
  const lines: string[] = [];

  lines.push('## Analyze Optimization Recommendation');
  lines.push('');

  lines.push(`**Rule:** ${ctx.ruleName}`);
  lines.push(`**Category:** ${ctx.category.replace(/([A-Z])/g, ' $1').trim()}`);
  lines.push(`**Severity:** ${ctx.severity}`);
  lines.push(`**Resource:** ${ctx.resourceName}`);
  if (ctx.resourceType) {
    lines.push(`**Type:** ${ctx.resourceType}`);
  }
  if (ctx.namespace) {
    lines.push(`**Namespace:** \`${ctx.namespace}\``);
  }
  if (ctx.accountName) {
    lines.push(`**Account:** ${ctx.accountName}`);
  }
  if (ctx.estimatedSavings) {
    lines.push(`**Estimated Savings:** $${ctx.estimatedSavings.toFixed(2)}/mo`);
  }

  if (ctx.brief) {
    lines.push('');
    lines.push('### Summary');
    lines.push(ctx.brief);
  }

  if (ctx.alarmConfig) {
    lines.push(...buildAlarmConfigSection(ctx.alarmConfig));
  }

  lines.push('');
  lines.push('Please analyze this optimization recommendation, explain the impact, and suggest the best approach to implement it.');

  return lines.join('\n');
}
