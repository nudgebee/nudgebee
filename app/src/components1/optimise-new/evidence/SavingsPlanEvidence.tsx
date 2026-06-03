import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import MonetizationOnIcon from '@mui/icons-material/MonetizationOn';
import TrendingDownIcon from '@mui/icons-material/TrendingDown';
import { safeParseJSON } from '@components1/optimise-new/utils';

// ─── Formatting helpers ───

const RESOURCE_TYPE_LABELS: Record<string, string> = {
  SageMakerSp: 'SageMaker Savings Plan',
  ComputeSp: 'Compute Savings Plan',
  Ec2InstanceSp: 'EC2 Instance Savings Plan',
  Ec2Instance: 'EC2 Instance',
  RdsInstance: 'RDS Instance',
  ElastiCacheNode: 'ElastiCache Node',
  RedshiftNode: 'Redshift Node',
  OpenSearchInstance: 'OpenSearch Instance',
};

const ENUM_REPLACEMENTS: Record<string, string> = {
  THREE_YEARS: '3 Years',
  ONE_YEAR: '1 Year',
  ALL_UPFRONT: 'All Upfront',
  PARTIAL_UPFRONT: 'Partial Upfront',
  NO_UPFRONT: 'No Upfront',
  SAGEMAKER_SP: 'SageMaker SP',
  COMPUTE_SP: 'Compute SP',
  EC2_INSTANCE_SP: 'EC2 Instance SP',
};

const formatResourceType = (type: string): string => RESOURCE_TYPE_LABELS[type] || type.replace(/([A-Z])/g, ' $1').trim();

const formatEnumString = (value: string): string => {
  let result = value;
  for (const [enumVal, label] of Object.entries(ENUM_REPLACEMENTS)) {
    result = result.replace(new RegExp(enumVal, 'g'), label);
  }
  return result.replace(/_/g, ' ').replace(/\s+/g, ' ').trim();
};

const formatCurrency = (value: number): string => '$' + Math.round(value).toLocaleString();

const formatCurrencyStr = (value: string): string => {
  const num = parseFloat(value);
  if (isNaN(num)) return value;
  return '$' + Math.round(num).toLocaleString();
};

const formatPercentage = (value: number | string): string => {
  const num = typeof value === 'string' ? parseFloat(value) : value;
  if (isNaN(num)) return String(value);
  return num.toFixed(1) + '%';
};

// Fields handled explicitly or hidden as redundant/technical
const HIDDEN_FIELDS = new Set([
  'source',
  'cloud_provider',
  'action_type',
  'account_id',
  'recommendation_id',
  'resource_arn',
  'resource_id',
  'restart_needed',
  'rollback_possible',
  'description',
  // COH fields
  'current_resource_type',
  'current_resource_summary',
  'recommended_resource_type',
  'recommended_resource_summary',
  'estimated_monthly_savings',
  'estimated_savings_percentage',
  'estimated_monthly_cost',
  'implementation_effort',
  'currency_code',
  // CE RI fields
  'service',
  'region',
  'instance_type',
  'instance_class',
  'instance_size',
  'node_type',
  'family',
  'platform',
  'database_engine',
  'current_generation',
  'upfront_cost',
  'recurring_monthly_cost',
  'average_utilization',
  'recommended_instance_count',
  // CE SP fields
  'savings_plan_type',
  'term',
  'payment_option',
  'hourly_commitment',
  'estimated_on_demand_cost',
  'estimated_total_cost',
]);

// ─── Component ───

interface SavingsPlanEvidenceProps {
  recommendation: any;
  ruleName: string;
  estimatedSavings?: number;
}

const SavingsPlanEvidence = ({ recommendation, ruleName, estimatedSavings }: SavingsPlanEvidenceProps) => {
  const rec = safeParseJSON(recommendation);
  if (!rec || typeof rec !== 'object') return null;

  const isCERI = ruleName === 'aws_native_ce_ri_recommendation';
  const isCESP = ruleName === 'aws_native_ce_savings_plan_recommendation';
  const isCOHRI = ruleName === 'aws_native_purchase_reserved_instances';

  const sectionTitle = isCERI || isCOHRI ? 'Reserved Instance Recommendation' : 'Savings Plan Recommendation';

  // Shared fields
  const savingsPercentage = rec.estimated_savings_percentage;
  const effort = rec.implementation_effort;

  // Collect remaining unknown fields
  const remainingFields = Object.entries(rec)
    .filter(([key, value]) => !HIDDEN_FIELDS.has(key) && value != null && typeof value !== 'object')
    .slice(0, 6);

  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title={sectionTitle} muiIcon={<MonetizationOnIcon sx={{ fontSize: '16px' }} />} />

      {/* Cost Explorer RI: instance/service details */}
      {isCERI && <CERIDetails rec={rec} />}

      {/* Cost Explorer SP: plan type, term, payment */}
      {isCESP && <CESPDetails rec={rec} />}

      {/* COH: resource type and summaries */}
      {!isCERI && !isCESP && <COHDetails rec={rec} />}

      {/* Cost Analysis */}
      <SectionTitle title='Cost Analysis' muiIcon={<TrendingDownIcon sx={{ fontSize: '16px' }} />} />

      <Box
        sx={{
          backgroundColor: ds.gray[100],
          borderRadius: ds.radius.lg,
          p: ds.space[3],
          border: `1px solid ${ds.gray[200]}`,
          mb: ds.space[3],
        }}
      >
        {/* COH cost fields */}
        {rec.estimated_monthly_cost != null && <MetricRow label='Estimated Monthly Cost' value={formatCurrency(rec.estimated_monthly_cost)} />}

        {/* CE RI cost fields */}
        {rec.upfront_cost != null && <MetricRow label='Upfront Cost' value={formatCurrencyStr(rec.upfront_cost)} />}
        {rec.recurring_monthly_cost != null && <MetricRow label='Recurring Monthly Cost' value={formatCurrencyStr(rec.recurring_monthly_cost)} />}
        {rec.estimated_monthly_savings != null && (
          <MetricRow label='Estimated Monthly Savings' value={formatCurrency(rec.estimated_monthly_savings)} highlight />
        )}

        {/* CE SP cost fields */}
        {rec.hourly_commitment != null && <MetricRow label='Hourly Commitment' value={formatCurrencyStr(rec.hourly_commitment)} />}
        {rec.estimated_on_demand_cost != null && <MetricRow label='On-Demand Cost' value={formatCurrencyStr(rec.estimated_on_demand_cost)} />}
        {rec.estimated_total_cost != null && <MetricRow label='Estimated Total Cost' value={formatCurrencyStr(rec.estimated_total_cost)} />}

        {/* Common */}
        {savingsPercentage != null && <MetricRow label='Savings Percentage' value={formatPercentage(savingsPercentage)} highlight />}
        {effort && <MetricRow label='Implementation Effort' value={effort} />}
      </Box>

      {/* Remaining fields (safety net) */}
      {remainingFields.length > 0 && (
        <Box
          sx={{
            backgroundColor: ds.gray[100],
            borderRadius: ds.radius.lg,
            p: '10px',
            border: `1px solid ${ds.gray[200]}`,
            mb: ds.space[3],
          }}
        >
          {remainingFields.map(([key, value]) => (
            <MetricRow
              key={key}
              label={key.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())}
              value={typeof value === 'number' ? formatCurrency(value) : formatEnumString(String(value))}
            />
          ))}
        </Box>
      )}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

// ─── Sub-sections for different data shapes ───

/** Cost Explorer Reserved Instance details */
const CERIDetails = ({ rec }: { rec: Record<string, any> }) => {
  const service = rec.service || '';
  const region = rec.region || '';
  const instanceIdentifier = rec.instance_type || rec.instance_class || rec.instance_size || rec.node_type || '';
  const family = rec.family || '';
  const platform = rec.platform || '';
  const dbEngine = rec.database_engine || '';
  const currentGen = rec.current_generation;
  const utilization = rec.average_utilization;
  const count = rec.recommended_instance_count;

  return (
    <Box
      sx={{
        backgroundColor: ds.gray[100],
        borderRadius: ds.radius.lg,
        p: ds.space[3],
        border: `1px solid ${ds.gray[200]}`,
        mb: ds.space[3],
      }}
    >
      {service && <MetricRow label='Service' value={service} />}
      {region && <MetricRow label='Region' value={region} />}
      {instanceIdentifier && <MetricRow label='Instance' value={instanceIdentifier} />}
      {family && <MetricRow label='Family' value={family} />}
      {platform && <MetricRow label='Platform' value={platform} />}
      {dbEngine && <MetricRow label='Database Engine' value={dbEngine} />}
      {currentGen != null && <MetricRow label='Current Generation' value={currentGen ? 'Yes' : 'No'} />}
      {utilization != null && <MetricRow label='Average Utilization' value={formatPercentage(utilization)} />}
      {count && <MetricRow label='Recommended Count' value={count} highlight />}
    </Box>
  );
};

/** Cost Explorer Savings Plan details */
const CESPDetails = ({ rec }: { rec: Record<string, any> }) => {
  const spType = rec.savings_plan_type ? formatEnumString(rec.savings_plan_type) : '';
  const term = rec.term ? formatEnumString(rec.term) : '';
  const paymentOption = rec.payment_option ? formatEnumString(rec.payment_option) : '';

  return (
    <Box
      sx={{
        backgroundColor: ds.blue[100],
        borderRadius: ds.radius.lg,
        p: ds.space[3],
        border: `1px solid ${ds.blue[200]}`,
        mb: ds.space[3],
      }}
    >
      {spType && <MetricRow label='Plan Type' value={spType} />}
      {term && <MetricRow label='Term' value={term} />}
      {paymentOption && <MetricRow label='Payment Option' value={paymentOption} />}
    </Box>
  );
};

/** Cost Optimization Hub details */
const COHDetails = ({ rec }: { rec: Record<string, any> }) => {
  const currentType = rec.current_resource_type ? formatResourceType(rec.current_resource_type) : '';
  const recommendedType = rec.recommended_resource_type ? formatResourceType(rec.recommended_resource_type) : '';
  const currentSummary = rec.current_resource_summary ? formatEnumString(rec.current_resource_summary) : '';
  const recommendedSummary = rec.recommended_resource_summary ? formatEnumString(rec.recommended_resource_summary) : '';
  const description = rec.recommended_resource_summary ? formatEnumString(rec.recommended_resource_summary) : '';

  return (
    <>
      {description && (
        <Box
          sx={{
            backgroundColor: ds.blue[100],
            borderRadius: ds.radius.lg,
            p: ds.space[3],
            border: `1px solid ${ds.blue[200]}`,
            mb: ds.space[3],
          }}
        >
          <Typography sx={{ fontSize: ds.text.small, color: ds.blue[700], lineHeight: 1.6 }}>{description}</Typography>
        </Box>
      )}

      <Box
        sx={{
          backgroundColor: ds.gray[100],
          borderRadius: ds.radius.lg,
          p: ds.space[3],
          border: `1px solid ${ds.gray[200]}`,
          mb: ds.space[3],
        }}
      >
        {currentType && <MetricRow label='Resource Type' value={currentType} />}
        {currentSummary && <MetricRow label='Current Plan' value={currentSummary} />}
        {recommendedType && recommendedType !== currentType && <MetricRow label='Recommended Type' value={recommendedType} />}
        {recommendedSummary && <MetricRow label='Recommended Plan' value={recommendedSummary} highlight />}
      </Box>
    </>
  );
};

export default SavingsPlanEvidence;
