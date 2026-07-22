import { Box, Typography } from '@mui/material';
import { InfoOutlined } from '@mui/icons-material';
import Tooltip from '@ui/Tooltip';
import { ds } from '@utils/colors';

/** Friendly headings for the metric keys returned alongside K8s utilisation queries. */
export const K8S_METRIC_QUERY_LABELS: Record<string, string> = {
  cpu_usage: 'Usage',
  cpu_request: 'Requested',
  cpu_limit: 'Limit',
  memory_usage: 'Usage',
  memory_request: 'Requested',
  memory_limit: 'Limit',
};

/**
 * Build a `metric key -> executed query` map from a utilisation results array.
 * Each result from `metrics_aggregate_utilisation` carries `query_key` + `query`.
 * Returns {} for non-array input (e.g. an error object), so callers can pass it through safely.
 */
export const buildPromQueries = (results: unknown): Record<string, string> => {
  const map: Record<string, string> = {};
  if (Array.isArray(results)) {
    results.forEach((r: any) => {
      if (r?.query_key && r?.query) map[r.query_key] = r.query;
    });
  }
  return map;
};

/** snake_case / kebab metric key -> Title Case heading, e.g. `network_receive_packet` -> `Network Receive Packet`. */
const humanizeKey = (key: string): string =>
  key
    .replace(/[_-]+/g, ' ')
    .trim()
    .replace(/\b\w/g, (c) => c.toUpperCase());

interface MetricQueryInfoProps {
  /** Map of key -> executed query string (PromQL / NRQL / CloudWatch / etc.). */
  queries?: Record<string, string> | null;
  /** Optional map to render a friendly heading per key; falls back to a humanised key. */
  labelMap?: Record<string, string>;
}

/**
 * Small info icon that reveals the executed metric query(ies) on hover.
 *
 * Renders nothing when no queries are available — e.g. datasources that do not
 * return a query string (the historical `nb`/SQL utilisation path) — so callers
 * can pass it unconditionally next to a chart title.
 */
const MetricQueryInfo = ({ queries, labelMap }: MetricQueryInfoProps) => {
  const entries = queries ? Object.entries(queries).filter(([, q]) => Boolean(q)) : [];
  if (entries.length === 0) return null;

  const content = (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3], p: ds.space[1] }}>
      {entries.map(([key, query]) => (
        <Box key={key}>
          <Typography sx={{ fontSize: ds.text.caption, fontWeight: 600, color: ds.brand[500], mb: ds.space[1] }}>
            {labelMap?.[key] || humanizeKey(key)}
          </Typography>
          <Box
            component='pre'
            sx={{
              margin: 0,
              fontFamily: ds.font.mono,
              fontSize: ds.text.caption,
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              color: 'var(--ds-foreground)',
            }}
          >
            {query}
          </Box>
        </Box>
      ))}
    </Box>
  );

  return (
    <Tooltip variant='default' placement='top' title={content} tooltipStyle={{ maxWidth: '560px' }}>
      <InfoOutlined sx={{ fontSize: ds.text.body, color: ds.gray[400], cursor: 'help' }} />
    </Tooltip>
  );
};

export default MetricQueryInfo;
