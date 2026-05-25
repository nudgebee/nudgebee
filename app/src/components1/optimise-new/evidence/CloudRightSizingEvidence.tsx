import { useState, useEffect } from 'react';
import { Box, Typography, Chip, CircularProgress } from '@mui/material';
import { colors } from 'src/utils/colors';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import recommendationApi from '@api1/recommendation';
import apiCloudAccount from '@api1/cloud-account';
import CloudIcon from '@mui/icons-material/Cloud';
import TrendingUpIcon from '@mui/icons-material/TrendingUp';
import SyncIcon from '@mui/icons-material/Sync';
import DescriptionIcon from '@mui/icons-material/Description';
import TimelineIcon from '@mui/icons-material/Timeline';
import LineChart from '@components1/common/charts/LineCharts';
import { formatMemory } from '@lib/formatter';
import { formatBytes } from 'src/utils/common';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface CloudRightSizingEvidenceProps {
  recommendation: any;
  ruleName: string;
  estimatedSavings?: number;
  fullRecommendation?: any;
}

const CloudRightSizingEvidence = ({ recommendation, ruleName, estimatedSavings, fullRecommendation }: CloudRightSizingEvidenceProps) => {
  const rec = safeParseJSON(recommendation);

  // Hooks must be called before any conditional returns (Rules of Hooks)
  const [metricsLoading, setMetricsLoading] = useState(false);
  const [cloudMetrics, setCloudMetrics] = useState<Record<string, any[]>>({});

  useEffect(() => {
    if (!fullRecommendation) return;
    const accountId = fullRecommendation.account_id;
    const resourceId = fullRecommendation.resource_id || fullRecommendation.cloud_resourse?.id;
    // Try multiple paths for service name
    const serviceName =
      rec?.service_name ||
      fullRecommendation.cloud_resourse?.meta?.config?.serviceName ||
      fullRecommendation.cloud_resourse?.meta?.serviceName ||
      fullRecommendation.service_name ||
      '';

    if (!accountId || !resourceId) return;

    setMetricsLoading(true);
    const startDate = new Date();
    startDate.setDate(startDate.getDate() - 7);

    apiCloudAccount
      .getCloudResourceMetrics({
        account_id: accountId,
        serviceName: serviceName || undefined,
        resourceId,
        startDate,
        endDate: new Date(),
      })
      .then((res: any) => {
        const metricsData = res?.data?.data?.cloud_metric_groupings_v2?.rows || [];
        if (metricsData.length > 0) {
          // Group by metric name
          const grouped = metricsData.reduce((acc: Record<string, any[]>, curr: any) => {
            const metric = curr.metric;
            if (!acc[metric]) acc[metric] = [];
            acc[metric].push(curr);
            return acc;
          }, {});
          setCloudMetrics(grouped);
        }
      })
      .catch((err: any) => {
        console.error('[CloudRightSizingEvidence] Failed to fetch cloud resource metrics:', err);
      })
      .finally(() => setMetricsLoading(false));
  }, [fullRecommendation]);

  if (!rec) return null;

  const details = recommendationApi.getRecommendationDetails('RightSizing', ruleName);
  // Also try Configuration category for rules like aws_rds_instance_reserved
  const detailsFallback = details || recommendationApi.getRecommendationDetails('Configuration', ruleName);

  const currentInstance = rec.current_instance_type || rec.instance_type || '';
  const recommendedInstance = rec.recommended_instance_type || '';
  const currentPrice = rec.current_price;
  const recommendedPrice = rec.recommended_price;
  const reason = rec.reason || rec.message || '';
  const serviceName = rec.service_name || detailsFallback?.serviceName || '';

  const { metricObjects, scalarFields, objectFields } = parseRecFields(rec);

  // Alternate instances (for aws_ec2_alternate_instances / aws_rds_alternate_instances)
  const alternateInstances = rec.alternate_instances || [];

  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='Cloud Resource Analysis' muiIcon={<CloudIcon sx={{ fontSize: '16px' }} />} />

      {/* Instance type comparison */}
      {currentInstance && recommendedInstance && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '16px',
            p: '14px',
            backgroundColor: colors.background.primaryLightest,
            borderRadius: '8px',
            border: '1px solid #BFDBFE',
            mb: '12px',
            justifyContent: 'center',
          }}
        >
          <Box sx={{ textAlign: 'center' }}>
            <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, mb: '2px' }}>Current</Typography>
            <Chip
              label={currentInstance}
              size='small'
              sx={{
                fontFamily: 'Roboto Mono, monospace',
                fontSize: '12px',
                fontWeight: 600,
                color: '#DC2626',
                backgroundColor: colors.background.accordionSummay,
                border: '1px solid #DC262630',
              }}
            />
          </Box>
          <ArrowForwardIcon sx={{ fontSize: '18px', color: '#1E40AF' }} />
          <Box sx={{ textAlign: 'center' }}>
            <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, mb: '2px' }}>Recommended</Typography>
            <Chip
              label={recommendedInstance}
              size='small'
              sx={{
                fontFamily: 'Roboto Mono, monospace',
                fontSize: '12px',
                fontWeight: 600,
                color: '#16A34A',
                backgroundColor: colors.background.costBlock,
                border: '1px solid #16A34A30',
              }}
            />
          </Box>
        </Box>
      )}

      {/* Instance type (single — no comparison) */}
      {currentInstance && !recommendedInstance && (
        <Box
          sx={{
            backgroundColor: colors.background.tertiaryLightestestest,
            borderRadius: '8px',
            p: '10px',
            border: `1px solid ${colors.border.secondaryLight}`,
            mb: '12px',
          }}
        >
          <MetricRow label='Instance Type' value={currentInstance} />
        </Box>
      )}

      {/* Price comparison */}
      {currentPrice != null && recommendedPrice != null && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '16px',
            p: '10px',
            backgroundColor: colors.background.costBlock,
            borderRadius: '8px',
            border: '1px solid #BBF7D0',
            mb: '12px',
            justifyContent: 'center',
          }}
        >
          <Box sx={{ textAlign: 'center' }}>
            <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Current Cost</Typography>
            <Typography sx={{ fontSize: '16px', fontWeight: 600, color: '#DC2626' }}>${Number(currentPrice).toFixed(2)}/hr</Typography>
          </Box>
          <ArrowForwardIcon sx={{ fontSize: '16px', color: '#16A34A' }} />
          <Box sx={{ textAlign: 'center' }}>
            <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Recommended</Typography>
            <Typography sx={{ fontSize: '16px', fontWeight: 600, color: '#16A34A' }}>${Number(recommendedPrice).toFixed(2)}/hr</Typography>
          </Box>
          <Box sx={{ textAlign: 'center', pl: '8px', borderLeft: '1px solid #BBF7D0' }}>
            <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Savings</Typography>
            <Typography sx={{ fontSize: '16px', fontWeight: 700, color: '#16A34A' }}>
              {((1 - recommendedPrice / currentPrice) * 100).toFixed(0)}%
            </Typography>
          </Box>
        </Box>
      )}

      {/* Reason / Message */}
      {reason && (
        <Box
          sx={{
            backgroundColor: colors.background.tertiaryLightestestest,
            borderRadius: '8px',
            p: '12px',
            border: `1px solid ${colors.border.secondaryLight}`,
            mb: '12px',
          }}
        >
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.6 }}>{reason}</Typography>
        </Box>
      )}

      {/* All resource details — show every available data field */}
      {(serviceName || scalarFields.length > 0) && (
        <>
          <SectionTitle title='Resource Details' muiIcon={<DescriptionIcon sx={{ fontSize: '16px' }} />} />
          <Box
            sx={{
              backgroundColor: colors.background.tertiaryLightestestest,
              borderRadius: '8px',
              p: '12px',
              border: `1px solid ${colors.border.secondaryLight}`,
              mb: '12px',
            }}
          >
            {serviceName && <MetricRow label='Service' value={serviceName} />}
            {scalarFields.map((f) => (
              <MetricRow
                key={f.key}
                label={f.label}
                value={formatScalarValue(f.key, f.value)}
                highlight={f.key.includes('recommend') || f.key.includes('saving')}
              />
            ))}
          </Box>
        </>
      )}

      {/* Nested object fields — render as additional detail sections */}
      {objectFields.map(({ key, data }) => (
        <Box key={key}>
          <SectionTitle
            title={key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())}
            muiIcon={<DescriptionIcon sx={{ fontSize: '16px' }} />}
          />
          <Box
            sx={{
              backgroundColor: colors.background.tertiaryLightestestest,
              borderRadius: '8px',
              p: '12px',
              border: `1px solid ${colors.border.secondaryLight}`,
              mb: '12px',
            }}
          >
            {Object.entries(data)
              .filter(([, v]) => v != null && typeof v !== 'object')
              .map(([k, v]) => (
                <MetricRow key={k} label={k.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())} value={formatScalarValue(k, v)} />
              ))}
          </Box>
        </Box>
      ))}

      {/* CloudWatch metrics from recommendation JSONB — rendered as line charts */}
      {metricObjects.length > 0 && (
        <>
          <SectionTitle title='Metrics (from recommendation)' muiIcon={<TrendingUpIcon sx={{ fontSize: '16px' }} />} />
          {metricObjects.map((metric, idx) => {
            const chartLabels = metric.timestamps.map((ts: string) => {
              const d = new Date(ts);
              return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
            });
            const metricChartColors = ['#3B82F6', '#16A34A', '#EAB308'];
            const chartColor = metricChartColors[idx] || metricChartColors[0];
            return (
              <Box
                key={metric.name || `metric-${idx}`}
                sx={{
                  mb: '12px',
                  backgroundColor: colors.background.tertiaryLightestestest,
                  borderRadius: '8px',
                  p: '10px',
                  border: `1px solid ${colors.border.secondaryLight}`,
                }}
              >
                <LineChart
                  chartTitle={`${metric.name} (${metric.statistics})`}
                  data={[metric.values]}
                  labels={chartLabels}
                  colors={[chartColor]}
                  chartLabel={[`${metric.name}`]}
                  minHeight={160}
                  dynamicHeight={false}
                />
              </Box>
            );
          })}
        </>
      )}

      {/* Cloud resource monitoring metrics (7-day trend from CloudWatch / Azure Monitor / GCP) */}
      {metricsLoading && (
        <Box sx={{ display: 'flex', justifyContent: 'center', py: '16px' }}>
          <CircularProgress size={24} />
        </Box>
      )}
      {/* Only show "no metrics" for utilization-based rules where metrics are expected */}
      {!metricsLoading &&
        metricObjects.length === 0 &&
        Object.keys(cloudMetrics).length === 0 &&
        fullRecommendation &&
        UTILIZATION_RULES.test(ruleName || '') && (
          <Box
            sx={{
              backgroundColor: colors.background.tertiaryLightestestest,
              borderRadius: '8px',
              p: '10px',
              mb: '12px',
              border: `1px solid ${colors.border.secondaryLight}`,
            }}
          >
            <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontStyle: 'italic', textAlign: 'center' }}>
              No monitoring metrics available for this resource
            </Typography>
          </Box>
        )}
      {Object.keys(cloudMetrics).length > 0 && (
        <>
          <SectionTitle title='Resource Monitoring (7d)' muiIcon={<TimelineIcon sx={{ fontSize: '16px' }} />} />
          {Object.entries(cloudMetrics).map(([metricName, dataPoints], idx) => {
            // Sort by timestamp
            const sorted = [...dataPoints].sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());
            const labels = sorted.map((d: any) => {
              const dt = new Date(d.timestamp);
              return dt.toLocaleDateString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
            });
            // For memory-related metrics (bytes), convert to GB for readability
            const isMemoryMetric = metricName.toLowerCase().includes('memory') || metricName.toLowerCase().includes('freeable');
            const isNetworkMetric = metricName.toLowerCase().includes('network') || metricName.toLowerCase().includes('bytes');
            const values = sorted.map((d: any) => {
              if (isMemoryMetric && !isNetworkMetric) {
                return d.avg_value != null ? Number(formatMemory(d.avg_value, 'bytes', 'gb', false)) : null;
              }
              return d.avg_value;
            });
            const getMetricUnit = (): string => {
              if (isMemoryMetric && !isNetworkMetric) return 'GB';
              if (metricName.includes('Utilization') || metricName.includes('Percent')) return '%';
              return '';
            };
            const unit = getMetricUnit();
            const chartColors = ['#3B82F6', '#16A34A', '#EAB308', '#8B5CF6', '#EC4899', '#F97316'];
            const displayName = metricName.replace(/([A-Z])/g, ' $1').trim();

            return (
              <Box
                key={metricName}
                sx={{
                  mb: '12px',
                  backgroundColor: colors.background.tertiaryLightestestest,
                  borderRadius: '8px',
                  p: '10px',
                  border: `1px solid ${colors.border.secondaryLight}`,
                }}
              >
                <LineChart
                  chartTitle={unit ? displayName + ' (' + unit + ')' : displayName}
                  data={[values]}
                  labels={labels}
                  colors={[chartColors[idx % chartColors.length]]}
                  chartLabel={[displayName]}
                  minHeight={160}
                  dynamicHeight={false}
                />
              </Box>
            );
          })}
        </>
      )}

      {/* Alternate instances */}
      {alternateInstances.length > 0 && (
        <>
          <SectionTitle title='Alternative Instances' muiIcon={<SyncIcon sx={{ fontSize: '16px' }} />} />
          <Box
            sx={{
              backgroundColor: colors.background.tertiaryLightestestest,
              borderRadius: '8px',
              p: '10px',
              border: `1px solid ${colors.border.secondaryLight}`,
              maxHeight: '150px',
              overflow: 'auto',
            }}
          >
            {alternateInstances.slice(0, 5).map((alt: any, idx: number) => {
              const instanceType = alt.instanceType || alt.product?.attributes?.instanceType || 'Unknown';
              const price = alt.price || extractPrice(alt);
              return (
                <Box
                  key={instanceType + '-' + idx}
                  sx={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    py: '6px',
                    borderBottom: idx < alternateInstances.length - 1 ? `1px solid ${colors.border.secondaryLight}` : 'none',
                  }}
                >
                  <Typography sx={{ fontSize: '12px', color: colors.text.secondary, fontFamily: 'monospace' }}>{instanceType}</Typography>
                  {price != null && (
                    <Typography sx={{ fontSize: '12px', fontWeight: 600, color: '#16A34A' }}>${Number(price).toFixed(4)}/hr</Typography>
                  )}
                </Box>
              );
            })}
          </Box>
        </>
      )}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

// Rules where CloudWatch/Azure Monitor metrics are expected (utilization-based)
const UTILIZATION_RULES = /underutilized|idle|overprovisioned|unused|utilization|cpu_|memory_/i;

const KNOWN_FIELDS = new Set([
  'current_instance_type',
  'recommended_instance_type',
  'instance_type',
  'current_price',
  'recommended_price',
  'reason',
  'message',
  'service_name',
  'alternate_instances',
]);

type MetricObj = { name: string; values: number[]; timestamps: string[]; statistics: string };
type ScalarField = { key: string; label: string; value: any };
type ObjectField = { key: string; data: any };

const formatFieldLabel = (key: string): string => key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());

// Keys whose values are already in a unit other than bytes (e.g. `recommendedMemoryGb`
// is GB, `cpuUtilization` is percent). Checked BEFORE BYTES_KEY_RE to prevent a
// 16-GB value from being rendered as "16 B".
const NON_BYTES_SUFFIX_RE = /(Gb|Mb|Kb|Tb|GiB|MiB|KiB|TiB|Pct|Percent|Utilization|Iops|Ops|Count)$/i;

// Keys whose numeric values are bytes coming from cloud-collector recommendations.
// `^(min|max)value$` is intentionally anchored — we don't want to catch unrelated
// keys like `minCpuPercent`.
const BYTES_KEY_RE = /storage|memory|bytes|freeable|disk|^(min|max)value$/i;

// Explicit overrides for backend fields whose names misleadingly end in a non-byte
// suffix but whose values are actually bytes (e.g. `allcatedStorage10Pct` ends in
// `Pct` but holds a byte count).
const FORCE_BYTES = new Set(['allcatedStorage10Pct']);

const isByteKey = (key: string): boolean => {
  if (FORCE_BYTES.has(key)) return true;
  if (NON_BYTES_SUFFIX_RE.test(key)) return false;
  return BYTES_KEY_RE.test(key);
};

const formatScalarValue = (key: string, value: any): string => {
  if (value === null || value === undefined) return '—';
  if (typeof value === 'boolean') return value ? 'Yes' : 'No';
  if (typeof value === 'number') {
    // Non-finite (NaN/Infinity) and the `math.MaxInt` sentinel (≈ 9.22e18 after
    // float64 cast) both mean "no real value" — show a dash either way.
    if (!Number.isFinite(value) || value > Number.MAX_SAFE_INTEGER) return '—';
    if (isByteKey(key)) return formatBytes(value);
    return value.toLocaleString('en-US', { maximumFractionDigits: 2 });
  }
  return String(value);
};

const isMetricObject = (obj: any): boolean => Array.isArray(obj.values) && Array.isArray(obj.timestamps) && obj.values.length > 0;

const toMetricObj = (key: string, obj: any): MetricObj => ({
  name: obj.name || formatFieldLabel(key),
  values: obj.values,
  timestamps: obj.timestamps,
  statistics: obj.statistics || 'Average',
});

const classifyObjectField = (key: string, value: any, result: { metricObjects: MetricObj[]; objectFields: ObjectField[] }) => {
  if (isMetricObject(value)) {
    result.metricObjects.push(toMetricObj(key, value));
  } else {
    result.objectFields.push({ key, data: value });
  }
};

const classifyArrayField = (key: string, value: any[], scalarFields: ScalarField[]) => {
  if (key !== 'alternate_instances' && value.length > 0 && typeof value[0] !== 'object') {
    scalarFields.push({ key, label: formatFieldLabel(key), value: value.join(', ') });
  }
};

const parseRecFields = (rec: any): { metricObjects: MetricObj[]; scalarFields: ScalarField[]; objectFields: ObjectField[] } => {
  const metricObjects: MetricObj[] = [];
  const scalarFields: ScalarField[] = [];
  const objectFields: ObjectField[] = [];

  for (const [key, value] of Object.entries(rec)) {
    if (KNOWN_FIELDS.has(key) || value == null) {
      continue;
    }
    if (typeof value === 'object' && !Array.isArray(value)) {
      classifyObjectField(key, value, { metricObjects, objectFields });
    } else if (Array.isArray(value)) {
      classifyArrayField(key, value, scalarFields);
    } else {
      scalarFields.push({ key, label: formatFieldLabel(key), value });
    }
  }
  return { metricObjects, scalarFields, objectFields };
};

const _MetricSummaryBox = ({ label, value, color }: { label: string; value: string; color: string }) => (
  <Box sx={{ textAlign: 'center', p: '4px', borderRadius: '4px', backgroundColor: 'white' }}>
    <Typography sx={{ fontSize: '9px', color: colors.text.tertiary }}>{label}</Typography>
    <Typography sx={{ fontSize: '12px', fontWeight: 600, color, fontFamily: 'monospace' }}>{value}</Typography>
  </Box>
);

function extractPrice(alt: any): number | null {
  try {
    if (alt.terms?.OnDemand) {
      const term = Object.values(alt.terms.OnDemand)[0] as any;
      const dim = Object.values(term?.priceDimensions || {})[0] as any;
      return dim?.pricePerUnit?.USD ? parseFloat(dim.pricePerUnit.USD) : null;
    }
  } catch {
    // Fallback
  }
  return null;
}

export default CloudRightSizingEvidence;
