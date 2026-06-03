import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
import { Label } from '@components1/ds/Label';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import WarningAmberIcon from '@mui/icons-material/WarningAmber';
import AssignmentIcon from '@mui/icons-material/Assignment';
import UpgradeIcon from '@mui/icons-material/Upgrade';
import SyncIcon from '@mui/icons-material/Sync';
import { safeParseJSON } from '@components1/optimise-new/utils';

interface InfraUpgradeEvidenceProps {
  recommendation: any;
  ruleName: string;
  estimatedSavings?: number;
}

const InfraUpgradeEvidence = ({ recommendation, ruleName: _ruleName, estimatedSavings }: InfraUpgradeEvidenceProps) => {
  const rec = safeParseJSON(recommendation);

  // ─── API Deprecation format (k8s_api_deleted) ───
  if (rec.replacement_api || rec.deleted_items || rec.deprecated_version) {
    return (
      <Box sx={{ p: '14px' }}>
        <SectionTitle title='API Deprecation' muiIcon={<WarningAmberIcon sx={{ fontSize: '16px' }} />} />

        {/* Current → Replacement version arrow */}
        {(rec.version || rec.current_api_version) && (rec.replacement_api || rec.recommended_api_version) && (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[3],
              p: ds.space[3],
              backgroundColor: ds.amber[100],
              borderRadius: ds.radius.lg,
              border: `1px solid ${ds.amber[200]}`,
              mb: ds.space[3],
            }}
          >
            <VersionBadge label='Current' version={rec.version || rec.current_api_version} variant='current' />
            <ArrowForwardIcon sx={{ fontSize: '18px', color: ds.amber[700] }} />
            <VersionBadge label='Recommended' version={rec.replacement_api || rec.recommended_api_version} variant='recommended' />
          </Box>
        )}

        <Box
          sx={{
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            p: ds.space[3],
            border: `1px solid ${ds.gray[200]}`,
          }}
        >
          {rec.kind && <MetricRow label='Kind' value={rec.kind} />}
          {rec.name && <MetricRow label='Name' value={rec.name} />}
          {rec.group && <MetricRow label='API Group' value={rec.group} />}
          {rec.version && <MetricRow label='Current Version' value={rec.version} />}
          {rec.replacement_api && <MetricRow label='Replacement API' value={rec.replacement_api} highlight />}
          {rec.deprecated_version && <MetricRow label='Deprecated in K8s' value={`v${rec.deprecated_version}`} />}
          {rec.deleted_version && <MetricRow label='Deleted in K8s' value={`v${rec.deleted_version}`} highlight />}
        </Box>

        {/* Deleted items list */}
        {rec.deleted_items && Array.isArray(rec.deleted_items) && rec.deleted_items.length > 0 && (
          <>
            <SectionTitle title={`Affected Resources (${rec.deleted_items.length})`} muiIcon={<AssignmentIcon sx={{ fontSize: '16px' }} />} />
            <Box
              sx={{
                backgroundColor: ds.red[100],
                borderRadius: ds.radius.lg,
                p: '10px',
                border: `1px solid ${ds.red[200]}`,
                maxHeight: '200px',
                overflow: 'auto',
              }}
            >
              {rec.deleted_items.map((item: any, idx: number) => (
                <Box
                  key={typeof item === 'string' ? item : item.name || `del-${idx}`}
                  sx={{ py: ds.space[1], borderBottom: idx < rec.deleted_items.length - 1 ? `1px solid ${ds.red[100]}` : 'none' }}
                >
                  <Typography sx={{ fontSize: ds.text.small, color: ds.red[700], fontFamily: 'Roboto Mono, monospace' }}>
                    {typeof item === 'string' ? item : `${item.kind || ''}/${item.name || item}`}
                  </Typography>
                </Box>
              ))}
            </Box>
          </>
        )}

        {rec.description && (
          <Box sx={{ mt: ds.space[3] }}>
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.6 }}>
              {rec.description.replace(/\[b\]|\[\/b\]/g, '')}
            </Typography>
          </Box>
        )}

        {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
      </Box>
    );
  }

  // ─── Helm chart upgrade format ───
  // Handle both field naming conventions: {chartVersion, latestVersion} and {Installed: {version}, Latest: {version}}
  const helmChartName = rec.chartName || '';
  const helmInstalledVersion = rec.chartVersion || rec.Installed?.version || '';
  const helmLatestVersion = rec.latestVersion || rec.Latest?.version || '';
  const helmRelease = rec.releaseName || rec.release || '';
  const helmNamespace = rec.namespace || '';
  const helmOutdated = rec.outdated;
  const helmDeprecated = rec.deprecated;
  const helmOverridden = rec.overridden;

  if (helmChartName || helmInstalledVersion || helmLatestVersion) {
    return (
      <Box sx={{ p: '14px' }}>
        <SectionTitle title='Helm Chart Upgrade' muiIcon={<UpgradeIcon sx={{ fontSize: '16px' }} />} />

        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: ds.space[3],
            p: ds.space[3],
            backgroundColor: ds.blue[100],
            borderRadius: ds.radius.lg,
            border: `1px solid ${ds.blue[200]}`,
            mb: ds.space[3],
          }}
        >
          <VersionBadge label='Installed' version={helmInstalledVersion || '—'} variant='current' />
          <ArrowForwardIcon sx={{ fontSize: '18px', color: ds.blue[700] }} />
          <VersionBadge label='Latest' version={helmLatestVersion || '—'} variant='recommended' />
        </Box>

        {/* Status flags */}
        {(helmOutdated || helmDeprecated || helmOverridden) && (
          <Box sx={{ display: 'flex', gap: '6px', mb: ds.space[3], flexWrap: 'wrap' }}>
            {helmOutdated && (
              <Label size='sm' tone='warning'>
                Outdated
              </Label>
            )}
            {helmDeprecated && (
              <Label size='sm' tone='critical'>
                Deprecated
              </Label>
            )}
            {helmOverridden && (
              <Label size='sm' tone='info'>
                Overridden
              </Label>
            )}
          </Box>
        )}

        <Box
          sx={{
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            p: ds.space[3],
            border: `1px solid ${ds.gray[200]}`,
          }}
        >
          {helmChartName && <MetricRow label='Chart Name' value={helmChartName} />}
          {helmInstalledVersion && <MetricRow label='Installed Version' value={helmInstalledVersion} />}
          {helmLatestVersion && <MetricRow label='Latest Version' value={helmLatestVersion} highlight />}
          {helmRelease && <MetricRow label='Release Name' value={helmRelease} />}
          {helmNamespace && <MetricRow label='Namespace' value={helmNamespace} />}
          {rec.Installed?.date && <MetricRow label='Installed Date' value={new Date(rec.Installed.date).toLocaleDateString()} />}
          {rec.Latest?.date && <MetricRow label='Latest Release Date' value={new Date(rec.Latest.date).toLocaleDateString()} />}
        </Box>

        {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
      </Box>
    );
  }

  // ─── Generic version upgrade format ───
  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title='Upgrade Details' muiIcon={<SyncIcon sx={{ fontSize: '16px' }} />} />

      {(rec.current_version || rec.current_api_version) && (rec.recommended_version || rec.recommended_api_version) && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: ds.space[3],
            p: ds.space[3],
            backgroundColor: ds.blue[100],
            borderRadius: ds.radius.lg,
            border: `1px solid ${ds.blue[200]}`,
            mb: ds.space[3],
          }}
        >
          <VersionBadge label='Current' version={rec.current_version || rec.current_api_version} variant='current' />
          <ArrowForwardIcon sx={{ fontSize: '18px', color: ds.blue[700] }} />
          <VersionBadge label='Recommended' version={rec.recommended_version || rec.recommended_api_version} variant='recommended' />
        </Box>
      )}

      <Box
        sx={{
          backgroundColor: ds.gray[100],
          borderRadius: ds.radius.lg,
          p: ds.space[3],
          border: `1px solid ${ds.gray[200]}`,
        }}
      >
        {rec.description && (
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.5, mb: ds.space[2] }}>
            {rec.description.replace(/\[b\]|\[\/b\]/g, '')}
          </Typography>
        )}
        {rec.current_version && <MetricRow label='Current Version' value={rec.current_version} />}
        {rec.recommended_version && <MetricRow label='Recommended Version' value={rec.recommended_version} highlight />}
        {rec.current_api_version && <MetricRow label='Current API' value={rec.current_api_version} />}
        {rec.recommended_api_version && <MetricRow label='Recommended API' value={rec.recommended_api_version} highlight />}
        {rec.deleted_version && <MetricRow label='Deleted in K8s' value={`v${rec.deleted_version}`} />}
        {rec.deprecated_version && <MetricRow label='Deprecated in K8s' value={`v${rec.deprecated_version}`} />}
        {rec.reason && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.5, mt: ds.space[2] }}>{rec.reason}</Typography>}
      </Box>

      {/* Render any remaining flat fields */}
      {renderRemainingFields(rec)}
      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

type VersionVariant = 'current' | 'recommended';
const VERSION_BADGE_TONE: Record<VersionVariant, { text: string; bg: string; border: string }> = {
  current: { text: ds.red[600], bg: ds.red[100], border: ds.red[200] },
  recommended: { text: ds.green[600], bg: ds.green[100], border: ds.green[200] },
};

const VersionBadge = ({ label, version, variant }: { label: string; version: string; variant: VersionVariant }) => {
  const tone = VERSION_BADGE_TONE[variant];
  return (
    <Box sx={{ textAlign: 'center' }}>
      <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mb: '2px' }}>{label}</Typography>
      <Box
        component='span'
        sx={{
          display: 'inline-block',
          fontFamily: ds.font.mono,
          fontSize: ds.text.small,
          fontWeight: ds.weight.semibold,
          color: tone.text,
          backgroundColor: tone.bg,
          border: `1px solid ${tone.border}`,
          borderRadius: ds.radius.md,
          px: ds.space[2],
          py: '2px',
        }}
      >
        {version}
      </Box>
    </Box>
  );
};

const KNOWN_VERSION_FIELDS = new Set([
  'description',
  'current_version',
  'recommended_version',
  'current_api_version',
  'recommended_api_version',
  'deleted_version',
  'deprecated_version',
  'reason',
  'kind',
  'name',
  'group',
  'version',
  'replacement_api',
  'deleted_items',
  'chartName',
  'chartVersion',
  'latestVersion',
  'releaseName',
  'namespace',
]);

function renderRemainingFields(rec: any) {
  const remaining = Object.entries(rec).filter(([key, value]) => !KNOWN_VERSION_FIELDS.has(key) && value != null && typeof value !== 'object');
  if (remaining.length === 0) return null;

  return (
    <Box
      sx={{
        mt: ds.space[2],
        backgroundColor: ds.gray[100],
        borderRadius: ds.radius.lg,
        p: '10px',
        border: `1px solid ${ds.gray[200]}`,
      }}
    >
      {remaining.slice(0, 6).map(([key, value]) => (
        <MetricRow key={key} label={key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())} value={String(value)} />
      ))}
    </Box>
  );
}

export default InfraUpgradeEvidence;
