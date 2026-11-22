import { escapeLikePattern } from '@lib/HttpService';

// Columns the events search box matches. The title alone misses most cloud
// alerts: a CloudWatch title is "AmazonEC2::<instance id>::Threshold Crossed…",
// while the alarm name that carries the host or app ("<instance id>-web-01-CPUUtilization") is the
// aggregation_key. aggregation_key holds the alert name for every source
// (CloudWatch alarm, Prometheus alertname, Azure and GCP rule), and
// subject_name is the resource. All three are plain text columns on events, so
// matching them costs the same as matching the title (#41158).
const EVENT_SEARCH_COLUMNS = ['title', 'subject_name', 'aggregation_key'];

// Where-clause fragment for the events search box, or null for blank input.
// Push it into an `_and` list so it composes with an exact `title` filter set by
// a drill-down instead of replacing it.
export function eventSearchClause(text?: string | null): Record<string, any> | null {
  const trimmed = text?.trim();
  if (!trimmed) {
    return null;
  }
  const pattern = '%' + escapeLikePattern(trimmed) + '%';
  return { _or: EVENT_SEARCH_COLUMNS.map((column) => ({ [column]: { _ilike: pattern } })) };
}
