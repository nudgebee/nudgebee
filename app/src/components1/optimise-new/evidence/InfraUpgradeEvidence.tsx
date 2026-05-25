import { Box, Typography, Chip } from '@mui/material';
import { colors } from 'src/utils/colors';
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
              gap: '12px',
              p: '12px',
              backgroundColor: '#FEF3C7',
              borderRadius: '8px',
              border: '1px solid #FDE68A',
              mb: '12px',
            }}
          >
            <VersionBadge label='Current' version={rec.version || rec.current_api_version} color='#DC2626' />
            <ArrowForwardIcon sx={{ fontSize: '18px', color: '#92400E' }} />
            <VersionBadge label='Recommended' version={rec.replacement_api || rec.recommended_api_version} color='#16A34A' />
          </Box>
        )}

        <Box
          sx={{
            backgroundColor: colors.background.tertiaryLightestestest,
            borderRadius: '8px',
            p: '12px',
            border: `1px solid ${colors.border.secondaryLight}`,
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
                backgroundColor: colors.background.accordionSummay,
                borderRadius: '8px',
                p: '10px',
                border: '1px solid #FECACA',
                maxHeight: '200px',
                overflow: 'auto',
              }}
            >
              {rec.deleted_items.map((item: any, idx: number) => (
                <Box
                  key={typeof item === 'string' ? item : item.name || `del-${idx}`}
                  sx={{ py: '4px', borderBottom: idx < rec.deleted_items.length - 1 ? '1px solid #FEE2E2' : 'none' }}
                >
                  <Typography sx={{ fontSize: '12px', color: '#991B1B', fontFamily: 'Roboto Mono, monospace' }}>
                    {typeof item === 'string' ? item : `${item.kind || ''}/${item.name || item}`}
                  </Typography>
                </Box>
              ))}
            </Box>
          </>
        )}

        {rec.description && (
          <Box sx={{ mt: '12px' }}>
            <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.6 }}>
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
            gap: '12px',
            p: '12px',
            backgroundColor: colors.background.primaryLightest,
            borderRadius: '8px',
            border: '1px solid #BFDBFE',
            mb: '12px',
          }}
        >
          <VersionBadge label='Installed' version={helmInstalledVersion || '—'} color='#DC2626' />
          <ArrowForwardIcon sx={{ fontSize: '18px', color: '#1E40AF' }} />
          <VersionBadge label='Latest' version={helmLatestVersion || '—'} color='#16A34A' />
        </Box>

        {/* Status flags */}
        {(helmOutdated || helmDeprecated || helmOverridden) && (
          <Box sx={{ display: 'flex', gap: '6px', mb: '12px', flexWrap: 'wrap' }}>
            {helmOutdated && (
              <Chip
                label='Outdated'
                size='small'
                sx={{ fontSize: '10px', height: '20px', backgroundColor: '#FEF3C7', color: '#92400E', border: '1px solid #FDE68A' }}
              />
            )}
            {helmDeprecated && (
              <Chip
                label='Deprecated'
                size='small'
                sx={{
                  fontSize: '10px',
                  height: '20px',
                  backgroundColor: colors.background.accordionSummay,
                  color: '#991B1B',
                  border: '1px solid #FECACA',
                }}
              />
            )}
            {helmOverridden && (
              <Chip
                label='Overridden'
                size='small'
                sx={{
                  fontSize: '10px',
                  height: '20px',
                  backgroundColor: colors.background.primaryLightest,
                  color: '#1E40AF',
                  border: '1px solid #BFDBFE',
                }}
              />
            )}
          </Box>
        )}

        <Box
          sx={{
            backgroundColor: colors.background.tertiaryLightestestest,
            borderRadius: '8px',
            p: '12px',
            border: `1px solid ${colors.border.secondaryLight}`,
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
            gap: '12px',
            p: '12px',
            backgroundColor: colors.background.primaryLightest,
            borderRadius: '8px',
            border: '1px solid #BFDBFE',
            mb: '12px',
          }}
        >
          <VersionBadge label='Current' version={rec.current_version || rec.current_api_version} color='#DC2626' />
          <ArrowForwardIcon sx={{ fontSize: '18px', color: '#1E40AF' }} />
          <VersionBadge label='Recommended' version={rec.recommended_version || rec.recommended_api_version} color='#16A34A' />
        </Box>
      )}

      <Box
        sx={{
          backgroundColor: colors.background.tertiaryLightestestest,
          borderRadius: '8px',
          p: '12px',
          border: `1px solid ${colors.border.secondaryLight}`,
        }}
      >
        {rec.description && (
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.5, mb: '8px' }}>
            {rec.description.replace(/\[b\]|\[\/b\]/g, '')}
          </Typography>
        )}
        {rec.current_version && <MetricRow label='Current Version' value={rec.current_version} />}
        {rec.recommended_version && <MetricRow label='Recommended Version' value={rec.recommended_version} highlight />}
        {rec.current_api_version && <MetricRow label='Current API' value={rec.current_api_version} />}
        {rec.recommended_api_version && <MetricRow label='Recommended API' value={rec.recommended_api_version} highlight />}
        {rec.deleted_version && <MetricRow label='Deleted in K8s' value={`v${rec.deleted_version}`} />}
        {rec.deprecated_version && <MetricRow label='Deprecated in K8s' value={`v${rec.deprecated_version}`} />}
        {rec.reason && <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.5, mt: '8px' }}>{rec.reason}</Typography>}
      </Box>

      {/* Render any remaining flat fields */}
      {renderRemainingFields(rec)}
      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

const getVersionBadgeBg = (color: string): string => {
  if (color === '#16A34A') return colors.background.costBlock;
  if (color === '#DC2626') return colors.background.accordionSummay;
  return colors.background.tertiaryLightestestest;
};

const VersionBadge = ({ label, version, color }: { label: string; version: string; color: string }) => (
  <Box sx={{ textAlign: 'center' }}>
    <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, mb: '2px' }}>{label}</Typography>
    <Chip
      label={version}
      size='small'
      sx={{
        fontFamily: 'Roboto Mono, monospace',
        fontSize: '12px',
        fontWeight: 600,
        color,
        backgroundColor: getVersionBadgeBg(color),
        border: '1px solid ' + color + '30',
      }}
    />
  </Box>
);

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
        mt: '8px',
        backgroundColor: colors.background.tertiaryLightestestest,
        borderRadius: '8px',
        p: '10px',
        border: `1px solid ${colors.border.secondaryLight}`,
      }}
    >
      {remaining.slice(0, 6).map(([key, value]) => (
        <MetricRow key={key} label={key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())} value={String(value)} />
      ))}
    </Box>
  );
}

export default InfraUpgradeEvidence;
