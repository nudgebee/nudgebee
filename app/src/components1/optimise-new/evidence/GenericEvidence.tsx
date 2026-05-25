import { Box, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import { SavingsFooter, SectionTitle, MetricRow } from '@components1/optimise-new/EvidencePanel';
import recommendationApi from '@api1/recommendation';
import AssignmentIcon from '@mui/icons-material/Assignment';

const getGridColumns = (count: number): string => {
  if (count > 4) return 'repeat(3, 1fr)';
  if (count > 2) return 'repeat(2, 1fr)';
  return '1fr';
};
import { safeParseJSON } from '@components1/optimise-new/utils';

interface GenericEvidenceProps {
  recommendation: any;
  category: string;
  ruleName: string;
  estimatedSavings?: number;
}

// Replicates the parseJsonToKeyValue pattern from cloudaccount/common.tsx
const parseToKeyValue = (obj: any): { key: string; label: string; value: any; type: string }[] => {
  if (!obj || typeof obj !== 'object') return [];
  return Object.entries(obj)
    .filter(([, v]) => v != null)
    .map(([key, value]) => {
      const label = key
        .replace(/_/g, ' ')
        .replace(/([a-z])([A-Z])/g, '$1 $2')
        .replace(/\b\w/g, (c) => c.toUpperCase());
      return { key, label, value, type: typeof value };
    });
};

const GenericEvidence = ({ recommendation, category, ruleName, estimatedSavings }: GenericEvidenceProps) => {
  const rec = safeParseJSON(recommendation);
  const details = recommendationApi.getRecommendationDetails(category, ruleName);

  // Separate flat fields from nested objects and arrays
  const flatFields = parseToKeyValue(rec).filter((f) => f.type === 'string' || f.type === 'number' || f.type === 'boolean');
  const objectFields = parseToKeyValue(rec).filter((f) => f.type === 'object' && !Array.isArray(f.value));
  const arrayFields = parseToKeyValue(rec).filter((f) => Array.isArray(f.value));

  const hasContent = flatFields.length > 0 || objectFields.length > 0 || arrayFields.length > 0;

  return (
    <Box sx={{ p: '14px' }}>
      <SectionTitle title={details?.title || 'Recommendation Details'} muiIcon={<AssignmentIcon sx={{ fontSize: '16px' }} />} />

      {/* Main reason/description from data */}
      {rec.reason && (
        <Box
          sx={{
            backgroundColor: colors.background.tertiaryLightestestest,
            borderRadius: '8px',
            p: '12px',
            border: `1px solid ${colors.border.secondaryLight}`,
            mb: '12px',
          }}
        >
          <Typography sx={{ fontSize: '12px', color: colors.text.secondary, lineHeight: 1.6 }}>{rec.reason}</Typography>
        </Box>
      )}

      {/* Flat key-value pairs in 3-column grid */}
      {flatFields.length > 0 && (
        <Box
          sx={{
            display: 'grid',
            gridTemplateColumns: getGridColumns(flatFields.length),
            columnGap: '24px',
            rowGap: '12px',
            backgroundColor: colors.background.tertiaryLightestestest,
            borderRadius: '8px',
            p: '12px',
            border: `1px solid ${colors.border.secondaryLight}`,
            mb: '12px',
          }}
        >
          {flatFields
            .filter((f) => f.key !== 'reason')
            .slice(0, 15)
            .map((field) => (
              <Box key={field.key} sx={{ overflow: 'hidden', wordBreak: 'break-word' }}>
                <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontWeight: 600, mb: '2px' }}>{field.label}</Typography>
                <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>
                  {String(field.value).length > 100 ? String(field.value).substring(0, 100) + '...' : String(field.value)}
                </Typography>
              </Box>
            ))}
        </Box>
      )}

      {/* Nested objects */}
      {objectFields.slice(0, 4).map((field) => (
        <Box key={field.key}>
          <SectionTitle title={field.label} />
          <Box
            sx={{
              backgroundColor: colors.background.tertiaryLightestestest,
              borderRadius: '8px',
              p: '10px',
              border: `1px solid ${colors.border.secondaryLight}`,
              mb: '8px',
            }}
          >
            {Object.entries(field.value as Record<string, any>)
              .filter(([, v]) => v != null && typeof v !== 'object')
              .slice(0, 8)
              .map(([k, v]) => (
                <MetricRow key={k} label={k.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())} value={String(v)} />
              ))}
          </Box>
        </Box>
      ))}

      {/* Arrays */}
      {arrayFields.slice(0, 3).map((field) => (
        <Box key={field.key}>
          <SectionTitle title={`${field.label} (${(field.value as any[]).length})`} />
          <Box
            sx={{
              backgroundColor: colors.background.tertiaryLightestestest,
              borderRadius: '8px',
              p: '10px',
              border: `1px solid ${colors.border.secondaryLight}`,
              mb: '8px',
              maxHeight: '200px',
              overflow: 'auto',
            }}
          >
            {(field.value as any[]).slice(0, 10).map((item: any, idx: number) => (
              <Box
                key={typeof item === 'string' ? item.substring(0, 60) : item?.name || item?.message || `item-${idx}`}
                sx={{ py: '4px', borderBottom: idx < Math.min((field.value as any[]).length, 10) - 1 ? '1px solid #F3F4F6' : 'none' }}
              >
                <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>
                  {typeof item === 'string' && item}
                  {typeof item === 'object' && (item.message || item.name || JSON.stringify(item).substring(0, 100))}
                  {typeof item !== 'string' && typeof item !== 'object' && String(item)}
                </Typography>
              </Box>
            ))}
            {(field.value as any[]).length > 10 && (
              <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, fontStyle: 'italic', pt: '4px' }}>
                +{(field.value as any[]).length - 10} more items
              </Typography>
            )}
          </Box>
        </Box>
      ))}

      {!hasContent && !rec.reason && (
        <Typography sx={{ fontSize: '13px', color: colors.text.tertiary, fontStyle: 'italic' }}>
          No detailed evidence data available for this recommendation.
        </Typography>
      )}

      {estimatedSavings != null && estimatedSavings !== 0 && <SavingsFooter savings={estimatedSavings} />}
    </Box>
  );
};

export default GenericEvidence;
