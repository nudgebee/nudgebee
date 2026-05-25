import { Box, Typography, Divider } from '@mui/material';
import { colors } from 'src/utils/colors';
import Currency from '@components1/common/format/Currency';
import RightSizingEvidence from './evidence/RightSizingEvidence';
import ReplicaRightSizingEvidence from './evidence/ReplicaRightSizingEvidence';
import PVRightSizingEvidence from './evidence/PVRightSizingEvidence';
import UnusedPVCEvidence from './evidence/UnusedPVCEvidence';
import AbandonedResourceEvidence from './evidence/AbandonedResourceEvidence';
import CloudRightSizingEvidence from './evidence/CloudRightSizingEvidence';
import SavingsPlanEvidence from './evidence/SavingsPlanEvidence';
import ConfigurationEvidence from './evidence/ConfigurationEvidence';
import CertificateExpiryEvidence from './evidence/CertificateExpiryEvidence';
import InfraUpgradeEvidence from './evidence/InfraUpgradeEvidence';
import SecurityEvidence from './evidence/SecurityEvidence';
import ImageScanEvidence from './evidence/ImageScanEvidence';
import CISSecurityEvidence from './evidence/CISSecurityEvidence';
import SpotRecommendationEvidence from './evidence/SpotRecommendationEvidence';
import GenericEvidence from './evidence/GenericEvidence';
import { safeParseJSON } from './utils';

// ─── Shared helper components (exported for reuse by evidence sub-components) ───

const formatMetricValue = (value: any, unit: string): string => {
  if (value == null || value === '') return '—';
  let formatted: string;
  if (typeof value === 'number') {
    formatted = Number.isInteger(value) ? String(value) : value.toFixed(3);
  } else {
    formatted = String(value);
  }
  return unit ? formatted + ' ' + unit : formatted;
};

export const MetricRow = ({ label, value, unit = '', highlight = false }: { label: string; value: any; unit?: string; highlight?: boolean }) => (
  <Box sx={{ display: 'flex', justifyContent: 'space-between', py: '6px', borderBottom: `1px solid ${colors.border.secondaryLight}` }}>
    <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, fontWeight: 500 }}>{label}</Typography>
    <Typography
      sx={{
        fontSize: '12px',
        color: highlight ? colors.text.currency : colors.text.secondary,
        fontWeight: highlight ? 700 : 600,
        maxWidth: '60%',
        textAlign: 'right',
        wordBreak: 'break-word',
      }}
    >
      {formatMetricValue(value, unit)}
    </Typography>
  </Box>
);

export const SectionTitle = ({ title, icon: _icon, muiIcon }: { title: string; icon?: string; muiIcon?: React.ReactNode }) => (
  <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', mt: '16px', mb: '8px' }}>
    {muiIcon && <Box sx={{ display: 'flex', color: colors.text.tertiary, fontSize: '16px' }}>{muiIcon}</Box>}
    <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>{title}</Typography>
  </Box>
);

export const SavingsFooter = ({ savings }: { savings: number }) => {
  const displayValue = Math.abs(savings);
  let savingsColor = colors.text.secondary;
  if (savings > 0) savingsColor = colors.text.currency;
  else if (savings < 0) savingsColor = colors.error;

  return (
    <>
      <Divider sx={{ my: '14px' }} />
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          py: '4px',
          px: '2px',
        }}
      >
        <Box>
          <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.tertiary }}>Projected Monthly Savings</Typography>
          <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, fontStyle: 'italic' }}>Based on observed usage data</Typography>
        </Box>
        <Currency
          value={displayValue}
          precison={2}
          withTooltip={false}
          sx={{
            fontSize: '16px',
            fontWeight: 700,
            color: savingsColor,
          }}
        />
      </Box>
    </>
  );
};

// ─── Main EvidencePanel (rule-aware router) ───

interface EvidencePanelProps {
  recommendation: any;
  category: string;
  ruleName: string;
  estimatedSavings?: number;
  cloudResource?: any;
  fullRecommendation?: any;
}

const K8S_RESOURCE_TYPES = new Set(['Pod', 'Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob']);

const RIGHTSIZING_RULE_MAP: Record<string, string> = {
  replica_right_sizing: 'replica',
  pv_rightsize: 'pv',
  unused_pvc: 'pvc',
  abandoned_resource: 'abandoned',
};

const SAVINGS_PLAN_RULES = new Set([
  'aws_native_purchase_savings_plans',
  'aws_native_purchase_reserved_instances',
  'aws_native_ce_ri_recommendation',
  'aws_native_ce_savings_plan_recommendation',
]);

const isCloudRightSizing = (rec: any, ruleName: string, cloudResource: any, fullRecommendation: any): boolean => {
  if (/^(aws_|azure_|gcp_|cloud_)/i.test(ruleName || '')) return true;
  if (rec.cloud_provider || rec.source === 'cloud_provider') return true;
  if (rec.current_instance_type || rec.recommended_instance_type || rec.cpu_utilization || rec.instance_type) return true;
  const resourceType = cloudResource?.type || fullRecommendation?.resource_type || '';
  const isK8s = K8S_RESOURCE_TYPES.has(resourceType);
  const hasK8sData = rec.notifications || Object.values(rec).some((v: any) => Array.isArray(v) && v.length > 0 && v[0]?.resource);
  return !isK8s && !hasK8sData;
};

const renderRightSizing = (rec: any, ruleName: string, estimatedSavings?: number, cloudResource?: any, fullRecommendation?: any) => {
  const specialRule = RIGHTSIZING_RULE_MAP[ruleName];
  if (specialRule === 'replica') {
    return <ReplicaRightSizingEvidence recommendation={rec} estimatedSavings={estimatedSavings} />;
  }
  if (specialRule === 'pv') {
    return <PVRightSizingEvidence recommendation={rec} estimatedSavings={estimatedSavings} cloudResource={cloudResource} />;
  }
  if (specialRule === 'pvc') {
    return <UnusedPVCEvidence recommendation={rec} estimatedSavings={estimatedSavings} cloudResource={cloudResource} />;
  }
  if (specialRule === 'abandoned') {
    return <AbandonedResourceEvidence recommendation={rec} estimatedSavings={estimatedSavings} cloudResource={cloudResource} />;
  }
  if (SAVINGS_PLAN_RULES.has(ruleName)) {
    return <SavingsPlanEvidence recommendation={rec} ruleName={ruleName} estimatedSavings={estimatedSavings} />;
  }
  if (isCloudRightSizing(rec, ruleName, cloudResource, fullRecommendation)) {
    return (
      <CloudRightSizingEvidence
        recommendation={rec}
        ruleName={ruleName}
        estimatedSavings={estimatedSavings}
        fullRecommendation={fullRecommendation}
      />
    );
  }
  return <RightSizingEvidence recommendation={rec} estimatedSavings={estimatedSavings} fullRecommendation={fullRecommendation} />;
};

const isCisBenchmark = (rec: any, ruleName: string): boolean => Boolean(rec.rule_id || rec.rule_description || ruleName?.includes('cis'));

const renderSecurity = (rec: any, ruleName: string, estimatedSavings?: number) => {
  if (ruleName === 'image_scan') {
    return <ImageScanEvidence recommendation={rec} ruleName={ruleName} estimatedSavings={estimatedSavings} />;
  }
  if (isCisBenchmark(rec, ruleName)) {
    return <CISSecurityEvidence recommendation={rec} ruleName={ruleName} estimatedSavings={estimatedSavings} />;
  }
  return <SecurityEvidence recommendation={rec} ruleName={ruleName} estimatedSavings={estimatedSavings} />;
};

const EvidencePanel = ({ recommendation, category, ruleName, estimatedSavings, cloudResource, fullRecommendation }: EvidencePanelProps) => {
  if (!recommendation) {
    return (
      <Box sx={{ p: '20px', textAlign: 'center' }}>
        <Typography sx={{ fontSize: '13px', color: colors.text.tertiary, fontStyle: 'italic' }}>
          No detailed evidence data available for this recommendation.
        </Typography>
        {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
      </Box>
    );
  }

  const rec = safeParseJSON(recommendation);

  switch (category) {
    case 'RightSizing':
      return renderRightSizing(rec, ruleName, estimatedSavings, cloudResource, fullRecommendation);

    case 'Configuration': {
      if (ruleName === 'certificate_expiry') {
        return <CertificateExpiryEvidence recommendation={rec} estimatedSavings={estimatedSavings} />;
      }
      return <ConfigurationEvidence recommendation={rec} ruleName={ruleName} estimatedSavings={estimatedSavings} />;
    }

    case 'InfraUpgrade':
    case 'K8sVersionUpgrade':
      return <InfraUpgradeEvidence recommendation={rec} ruleName={ruleName} estimatedSavings={estimatedSavings} />;

    case 'Security':
      return renderSecurity(rec, ruleName, estimatedSavings);

    case 'K8sSpotRecommendation':
      return <SpotRecommendationEvidence recommendation={rec} estimatedSavings={estimatedSavings} />;

    default:
      return <GenericEvidence recommendation={rec} category={category} ruleName={ruleName} estimatedSavings={estimatedSavings} />;
  }
};

export default EvidencePanel;
