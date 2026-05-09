import {
  Box,
  Typography,
  IconButton,
  Tabs,
  Tab,
  Divider,
  Chip,
  Drawer,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  CircularProgress,
} from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import LinkIcon from '@mui/icons-material/Link';
import { useState, useEffect } from 'react';
import { colors } from 'src/utils/colors';
import SeverityBadge, { type SeverityLevel } from './SeverityBadge';
import EvidencePanel from './EvidencePanel';
import DetailsPanel from './DetailsPanel';
import ActionBar from './ActionBar';
import Currency from '@components1/common/format/Currency';
import recommendationApi from '@api1/recommendation';
import { daysSinceLong, getResourceDisplayName } from './utils';

const getResolutionStatusStyle = (status: string) => {
  if (status === 'Completed') {
    return { backgroundColor: colors.background.costBlock, color: '#16A34A' };
  }
  if (status === 'Failed') {
    return { backgroundColor: colors.background.accordionSummay, color: '#DC2626' };
  }
  return { backgroundColor: colors.background.tertiaryLight, color: colors.text.tertiary };
};

interface RecommendationDetailPanelProps {
  open: boolean;
  onClose: () => void;
  recommendation: any;
  accounts?: Record<string, { name: string; cloud_provider: string }>;
  initialTab?: number;
  onCreateTicket?: (rec: any) => void;
  onResolve?: (rec: any) => void;
  onCopyCli?: (rec: any) => void;
  onAskNubi?: (rec: any) => void;
  onDismiss?: (rec: any) => void;
}

const formatDate = (dateStr: string | null) => {
  if (!dateStr) return '—';
  const d = new Date(dateStr);
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
};

const formatDateShort = (dateStr: string | null) => {
  if (!dateStr) return '—';
  const d = new Date(dateStr);
  return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
};

/** Inline Resolution History — lightweight table that fits the drawer */
const InlineResolutionHistory = ({ recommendationId }: { recommendationId: string }) => {
  const [resolutions, setResolutions] = useState<any[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!recommendationId) return;
    setLoading(true);
    recommendationApi
      .listRecommendationResolution(recommendationId, 10, 0)
      .then((res: any) => {
        setResolutions(res?.data?.recommendation_resolution || []);
      })
      .catch(() => setResolutions([]))
      .finally(() => setLoading(false));
  }, [recommendationId]);

  if (loading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', py: '20px' }}>
        <CircularProgress size={20} />
      </Box>
    );
  }

  if (!resolutions || resolutions.length === 0) {
    return (
      <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, fontStyle: 'italic', py: '8px' }}>No resolution history found.</Typography>
    );
  }

  return (
    <TableContainer
      sx={{
        borderRadius: '8px',
        border: `1px solid ${colors.border.secondaryLight}`,
        '& .MuiTableCell-root': { px: '8px', py: '6px', fontSize: '11px', borderColor: colors.background.tertiaryLight },
      }}
    >
      <Table size='small'>
        <TableHead>
          <TableRow sx={{ backgroundColor: colors.background.tableHeader }}>
            <TableCell sx={{ fontWeight: 600, color: colors.text.secondary }}>Type</TableCell>
            <TableCell sx={{ fontWeight: 600, color: colors.text.secondary }}>Reference</TableCell>
            <TableCell sx={{ fontWeight: 600, color: colors.text.secondary }}>Resolver</TableCell>
            <TableCell sx={{ fontWeight: 600, color: colors.text.secondary }}>Status</TableCell>
            <TableCell sx={{ fontWeight: 600, color: colors.text.secondary }}>Updated</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {resolutions.map((r: any, idx: number) => {
            const isLink = r.type_reference_id && (r.type_reference_id.startsWith('http') || r.type_reference_id.startsWith('/'));
            return (
              <TableRow key={r.type_reference_id || r.type || `resolution-${idx}`} sx={{ '&:last-child td': { borderBottom: 'none' } }}>
                <TableCell>
                  <Typography sx={{ fontSize: '11px', color: colors.text.secondary }}>{r.type || '—'}</Typography>
                </TableCell>
                <TableCell>
                  {isLink ? (
                    <Box
                      component='a'
                      href={r.type_reference_id}
                      target='_blank'
                      rel='noopener'
                      sx={{ fontSize: '11px', color: colors.primary, display: 'flex', alignItems: 'center', gap: '2px' }}
                    >
                      <LinkIcon sx={{ fontSize: '12px' }} />
                      Link
                    </Box>
                  ) : (
                    <Typography
                      sx={{
                        fontSize: '11px',
                        color: colors.text.secondary,
                        maxWidth: '100px',
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {r.type_reference_id || '—'}
                    </Typography>
                  )}
                </TableCell>
                <TableCell>
                  <Typography sx={{ fontSize: '11px', color: colors.text.secondary }}>{r.resolver_type || '—'}</Typography>
                </TableCell>
                <TableCell>
                  <Chip
                    label={r.status || '—'}
                    size='small'
                    sx={{
                      fontSize: '10px',
                      height: '18px',
                      ...getResolutionStatusStyle(r.status),
                    }}
                  />
                </TableCell>
                <TableCell>
                  <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>{formatDateShort(r.updated_at)}</Typography>
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </TableContainer>
  );
};

const RecommendationDetailPanel = ({
  open,
  onClose,
  recommendation,
  accounts = {},
  initialTab = 0,
  onCreateTicket,
  onResolve,
  onCopyCli,
  onAskNubi,
  onDismiss,
}: RecommendationDetailPanelProps) => {
  const [activeTab, setActiveTab] = useState(initialTab);

  useEffect(() => {
    if (open) {
      setActiveTab(initialTab);
    }
  }, [open, initialTab]);

  if (!recommendation) return null;

  const rec = recommendation;
  const resourceName = getResourceDisplayName(rec);
  const resourceType = rec.resource_type || rec.cloud_resourse?.type || '';
  const severity = (rec.severity || 'Info') as SeverityLevel;
  const category = rec.category || '';
  const ruleName = rec.rule_name || '';
  const savings = rec.estimated_savings || 0;
  const status = rec.status || 'Open';
  const namespace = rec.resource_k8s_namespace || '';
  const accountName = accounts[rec.account_id]?.name || '';

  return (
    <Drawer
      anchor='right'
      open={open}
      onClose={onClose}
      data-testid='recommendation-detail-panel'
      sx={{
        '& .MuiDrawer-paper': {
          width: { xs: '100%', md: '720px' },
          boxShadow: '0px 4px 20px -1px rgba(229, 229, 229, 0.4), -4px 0 20px rgba(0,0,0,0.08)',
          borderLeft: `1px solid ${colors.border.secondaryLight}`,
        },
      }}
    >
      <Box sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
        {/* Header */}
        <Box
          sx={{
            p: '16px 20px',
            borderBottom: `1px solid ${colors.border.secondaryLightest}`,
            display: 'flex',
            alignItems: 'flex-start',
            gap: '12px',
          }}
        >
          <Box sx={{ flex: 1 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px', mb: '6px', flexWrap: 'wrap' }}>
              <SeverityBadge severity={severity} size='medium' />
              <Chip
                label={status}
                size='small'
                sx={{
                  fontSize: '11px',
                  fontWeight: 500,
                  height: '22px',
                  backgroundColor: status === 'Open' ? colors.background.primaryLightest : colors.background.tertiaryLight,
                  color: status === 'Open' ? colors.primary : colors.text.tertiary,
                  border: `1px solid ${status === 'Open' ? colors.border.primaryLight : colors.border.secondaryLightest}`,
                }}
              />
              <Chip
                label={category.replace(/([A-Z])/g, ' $1').trim()}
                size='small'
                sx={{
                  fontSize: '11px',
                  fontWeight: 500,
                  height: '22px',
                  backgroundColor: colors.background.tertiaryLight,
                  color: colors.text.tertiary,
                }}
              />
            </Box>
            <Typography
              sx={{
                fontSize: '16px',
                fontWeight: 600,
                color: colors.text.secondary,
                wordBreak: 'break-word',
                lineHeight: 1.3,
              }}
            >
              {resourceName}
            </Typography>
            <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, mt: '2px' }}>
              {resourceType}
              {namespace ? ` · ${namespace}` : ''}
              {accountName ? ` · ${accountName}` : ''}
            </Typography>
          </Box>
          <IconButton onClick={onClose} data-testid='detail-panel-close' sx={{ p: '6px', color: colors.text.tertiary }}>
            <CloseIcon sx={{ fontSize: '20px' }} />
          </IconButton>
        </Box>

        {/* Savings banner */}
        {savings !== 0 && (
          <Box
            sx={{
              px: '20px',
              py: '8px',
              borderBottom: `1px solid ${colors.border.secondaryLightest}`,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
            }}
          >
            <Box>
              <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, fontWeight: 500 }}>Projected Monthly Savings</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, fontStyle: 'italic' }}>Based on observed usage data</Typography>
            </Box>
            <Currency
              value={Math.abs(savings)}
              precison={2}
              withTooltip={false}
              sx={{
                fontSize: '18px',
                fontWeight: 700,
                color: savings > 0 ? colors.text.currency : colors.error,
              }}
            />
          </Box>
        )}

        {/* Tabs */}
        <Box sx={{ borderBottom: `1px solid ${colors.border.secondaryLightest}` }}>
          <Tabs
            value={activeTab}
            onChange={(_, newVal) => setActiveTab(newVal)}
            sx={{
              minHeight: '40px',
              '& .MuiTab-root': {
                textTransform: 'none',
                fontSize: '13px',
                fontWeight: 500,
                minHeight: '40px',
                py: '8px',
              },
              '& .Mui-selected': {
                color: `${colors.primary} !important`,
                fontWeight: 600,
              },
              '& .MuiTabs-indicator': {
                backgroundColor: colors.primary,
              },
            }}
          >
            <Tab label='Details' data-testid='detail-tab-details' />
            <Tab label='Evidence' data-testid='detail-tab-evidence' />
            <Tab label='History' data-testid='detail-tab-history' />
          </Tabs>
        </Box>

        {/* Tab content — all tabs are always mounted so data fetches start immediately */}
        <Box sx={{ flex: 1, overflow: 'auto', display: activeTab === 0 ? 'block' : 'none' }}>
          <DetailsPanel fullRecommendation={rec} accounts={accounts} />
        </Box>

        <Box sx={{ flex: 1, overflow: 'auto', display: activeTab === 1 ? 'block' : 'none' }}>
          <EvidencePanel
            recommendation={rec.recommendation}
            category={category}
            ruleName={ruleName}
            estimatedSavings={savings}
            cloudResource={rec.cloud_resourse}
            fullRecommendation={rec}
          />
        </Box>

        <Box sx={{ flex: 1, overflow: 'auto', display: activeTab === 2 ? 'block' : 'none' }}>
          <Box sx={{ p: '16px 20px' }}>
            <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, mb: '12px' }}>Timeline</Typography>

            <Box sx={{ display: 'flex', flexDirection: 'column', gap: '0px' }}>
              {/* Created */}
              <Box sx={{ display: 'flex', gap: '12px', alignItems: 'flex-start' }}>
                <Box
                  sx={{
                    width: '8px',
                    height: '8px',
                    borderRadius: '50%',
                    backgroundColor: '#16A34A',
                    mt: '5px',
                    flexShrink: 0,
                  }}
                />
                <Box sx={{ pb: '16px', borderLeft: 'none' }}>
                  <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary }}>Recommendation created</Typography>
                  <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>
                    {formatDate(rec.created_at)} {daysSinceLong(rec.created_at) ? `(${daysSinceLong(rec.created_at)})` : ''}
                  </Typography>
                </Box>
              </Box>

              {/* Updated (if different from created) */}
              {rec.updated_at && rec.updated_at !== rec.created_at && (
                <Box sx={{ display: 'flex', gap: '12px', alignItems: 'flex-start' }}>
                  <Box
                    sx={{
                      width: '8px',
                      height: '8px',
                      borderRadius: '50%',
                      backgroundColor: colors.primary,
                      mt: '5px',
                      flexShrink: 0,
                    }}
                  />
                  <Box sx={{ pb: '16px' }}>
                    <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary }}>Last updated</Typography>
                    <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>
                      {formatDate(rec.updated_at)} {daysSinceLong(rec.updated_at) ? `(${daysSinceLong(rec.updated_at)})` : ''}
                    </Typography>
                  </Box>
                </Box>
              )}

              {/* Resolution info */}
              {rec.resolution && (
                <Box sx={{ display: 'flex', gap: '12px', alignItems: 'flex-start' }}>
                  <Box
                    sx={{
                      width: '8px',
                      height: '8px',
                      borderRadius: '50%',
                      backgroundColor: '#EAB308',
                      mt: '5px',
                      flexShrink: 0,
                    }}
                  />
                  <Box>
                    <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary }}>Resolution in progress</Typography>
                    <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>PR: {rec.resolution.pr_url || 'Pending'}</Typography>
                  </Box>
                </Box>
              )}
            </Box>

            {/* Resolution History — inline lightweight table */}
            {rec.id && (
              <>
                <Divider sx={{ my: '16px' }} />
                <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary, mb: '8px' }}>Resolution History</Typography>
                <InlineResolutionHistory recommendationId={rec.id} />
              </>
            )}
          </Box>
        </Box>

        <ActionBar
          fullRecommendation={rec}
          onCreateTicket={onCreateTicket}
          onResolve={onResolve}
          onCopyCli={onCopyCli}
          onAskNubi={onAskNubi}
          onDismiss={onDismiss}
        />
      </Box>
    </Drawer>
  );
};

export default RecommendationDetailPanel;
