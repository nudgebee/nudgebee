// CloudWatch Alarm Configuration Types

export interface MetricDimension {
  name: string;
  value: string;
}

export interface MetricStat {
  metric: {
    namespace: string;
    metric_name: string;
    dimensions: MetricDimension[];
  };
  period: number;
  stat: string;
}

export interface Metric {
  id: string;
  expression?: string;
  return_data?: boolean;
  label?: string;
  metric_stat?: MetricStat;
}

export interface AlarmConfig {
  metrics?: Metric[];
  metric_name?: string;
  threshold: number;
  comparison_operator: string;
  evaluation_periods: number;
  datapoints_to_alarm: number;
  period: number;
  statistic?: string;
  namespace?: string;
  dimensions?: MetricDimension[];
  treat_missing_data: string;
  alarm_name?: string;
}

export interface Recommendation {
  id: string;
  resource_name?: string;
  resource_id?: string;
  recommendation?: {
    alarm_config?: AlarmConfig;
    service_name?: string;
  };
}
