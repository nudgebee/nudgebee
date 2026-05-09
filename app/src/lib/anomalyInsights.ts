export interface AnomalyInsight {
  timestamp: string;
  value: number;
  baseline_value: number;
  deviation_absolute: number;
  deviation_percent: number;
  severity: 'low' | 'medium' | 'high' | 'critical';
  anomaly_score: number;
  comparison_window: string;
  insight?: string; // pre-generated sentence from backend
}

export function formatInsight(insight: AnomalyInsight, anomalyType: string): string {
  // Use pre-generated sentence from backend when available
  if (insight.insight != null) {
    return insight.insight;
  }

  // Fallback: construct sentence from stats (for older stored data without insight field)
  const { value, baseline_value, deviation_percent, comparison_window } = insight;
  const formattedValue = formatMetricValue(value, anomalyType);
  const formattedBaseline = formatMetricValue(baseline_value, anomalyType);
  const direction = deviation_percent > 0 ? 'above' : 'below';
  const percent = Math.abs(deviation_percent).toFixed(1);
  const metricName = prettifyMetricName(anomalyType);

  if (comparison_window === 'first detection') {
    return `${metricName} reached ${formattedValue} (first detection, no historical baseline)`;
  }

  return `${metricName} reached ${formattedValue}, which is ${percent}% ${direction} the ${comparison_window} of ${formattedBaseline}`;
}

function formatMetricValue(value: number, type: string): string {
  switch (type.toLowerCase()) {
    case 'memory': {
      const mb = value / (1024 * 1024);
      return mb > 1024 ? `${(mb / 1024).toFixed(2)}GB` : `${mb.toFixed(0)}MB`;
    }

    case 'cpu':
      // If value < 1, it's already in cores, convert to millicores
      return value < 1 ? `${(value * 1000).toFixed(0)}m` : `${value.toFixed(2)} cores`;

    case 'latency':
      return value < 1 ? `${(value * 1000).toFixed(0)}ms` : `${value.toFixed(2)}s`;

    case 'errorrate':
      return `${(value * 100).toFixed(2)}%`;

    case 'replicas':
      return value.toFixed(0);

    default:
      return value.toFixed(2);
  }
}

function prettifyMetricName(type: string): string {
  const names: Record<string, string> = {
    memory: 'Memory',
    cpu: 'CPU',
    latency: 'Latency',
    errorrate: 'Error Rate',
    replicas: 'Replicas',
  };
  return names[type.toLowerCase()] || type;
}

export function getSeverityColor(severity: string): 'error' | 'warning' | 'info' | 'success' {
  switch (severity) {
    case 'critical':
    case 'high':
      return 'error';
    case 'medium':
      return 'warning';
    case 'low':
      return 'info';
    default:
      return 'info';
  }
}
