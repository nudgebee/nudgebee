import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
import { safeParseJSON } from './utils';
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
import { MetricRow, SectionTitle, SavingsFooter } from './evidence/evidencePrimitives';
export { MetricRow, SectionTitle, SavingsFooter };

// ─── Main EvidencePanel (rule-aware router) ───

interface EvidencePanelProps {
  recommendation: any;
  category: string;
  ruleName: string;
  estimatedSavings?: number;
  cloudResource?: any;
  fullRecommendation?: any;
}

const K8S_RESOURCE_TYPES = new Set(['Pod', 'Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob', 'Rollout']);

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
      <Box sx={{ p: ds.space.mul(0, 10), textAlign: 'center' }}>
        <Typography sx={{ fontSize: ds.text.body, color: ds.gray[500], fontStyle: 'italic' }}>
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
