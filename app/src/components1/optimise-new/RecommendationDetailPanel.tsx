import {
  Box,
  Typography,
  Tabs,
  Tab,
  Divider,
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
import { ds } from 'src/utils/colors';
import { Label, type LabelTone } from '@components1/ds/Label';
import { Button } from '@components1/ds/Button';
import { type SeverityLevel } from './SeverityBadge';
import EvidencePanel from './EvidencePanel';
import DetailsPanel from './DetailsPanel';
import ActionBar from './ActionBar';
import Currency from '@components1/common/format/Currency';
import recommendationApi from '@api1/recommendation';
import { daysSinceLong, getResourceDisplayName } from './utils';

// Severity → DS Label tone (mirrors the summary list mapping).
const SEVERITY_TONE: Record<string, LabelTone> = {
  Critical: 'critical',
  High: 'critical',
  Medium: 'warning',
  Low: 'info',
  Info: 'neutral',
};

// Resolution lifecycle status → DS Label tone.
const resolutionTone = (status: string): LabelTone => {
  if (status === 'Completed') return 'success';
  if (status === 'Failed') return 'critical';
  return 'neutral';
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
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontStyle: 'italic', py: ds.space[2] }}>
        No resolution history found.
      </Typography>
    );
  }

  return (
    <TableContainer
      sx={{
        borderRadius: ds.radius.lg,
        border: `1px solid ${ds.gray[200]}`,
        '& .MuiTableCell-root': { px: ds.space[2], py: '6px', fontSize: ds.text.caption, borderColor: ds.gray[200] },
      }}
    >
      <Table size='small'>
        <TableHead>
          <TableRow sx={{ backgroundColor: ds.blue[100] }}>
            <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Type</TableCell>
            <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Reference</TableCell>
            <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Resolver</TableCell>
            <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Status</TableCell>
            <TableCell sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Updated</TableCell>
          </TableRow>
        </TableHead>
        <TableBody>
          {resolutions.map((r: any, idx: number) => {
            const isLink = r.type_reference_id && (r.type_reference_id.startsWith('http') || r.type_reference_id.startsWith('/'));
            return (
              <TableRow key={r.type_reference_id || r.type || `resolution-${idx}`} sx={{ '&:last-child td': { borderBottom: 'none' } }}>
                <TableCell>
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[700] }}>{r.type || '—'}</Typography>
                </TableCell>
                <TableCell>
                  {isLink ? (
                    <Box
                      component='a'
                      href={r.type_reference_id}
                      target='_blank'
                      rel='noopener'
                      sx={{ fontSize: ds.text.caption, color: ds.blue[600], display: 'flex', alignItems: 'center', gap: '2px' }}
                    >
                      <LinkIcon sx={{ fontSize: '12px' }} />
                      Link
                    </Box>
                  ) : (
                    <Typography
                      sx={{
                        fontSize: ds.text.caption,
                        color: ds.gray[700],
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
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[700] }}>{r.resolver_type || '—'}</Typography>
                </TableCell>
                <TableCell>
                  <Label size='sm' tone={resolutionTone(r.status)}>
                    {r.status || '—'}
                  </Label>
                </TableCell>
                <TableCell>
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{formatDateShort(r.updated_at)}</Typography>
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
          borderLeft: `1px solid ${ds.gray[200]}`,
        },
      }}
    >
      <Box sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
        {/* Header */}
        <Box
          sx={{
            p: '16px 20px',
            borderBottom: `1px solid ${ds.gray[200]}`,
            display: 'flex',
            alignItems: 'flex-start',
            gap: ds.space[3],
          }}
        >
          <Box sx={{ flex: 1 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: '6px', flexWrap: 'wrap' }}>
              <Label size='md' tone={SEVERITY_TONE[severity] ?? 'neutral'}>
                {severity}
              </Label>
              <Label size='sm' tone={status === 'Open' ? 'info' : 'neutral'}>
                {status}
              </Label>
              <Label size='sm' tone='neutral'>
                {category.replace(/([A-Z])/g, ' $1').trim()}
              </Label>
            </Box>
            <Typography
              sx={{
                fontSize: ds.text.title,
                fontWeight: ds.weight.semibold,
                color: ds.gray[700],
                wordBreak: 'break-word',
                lineHeight: 1.3,
              }}
            >
              {resourceName}
            </Typography>
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], mt: '2px' }}>
              {resourceType}
              {namespace ? ` · ${namespace}` : ''}
              {accountName ? ` · ${accountName}` : ''}
            </Typography>
          </Box>
          <Button tone='ghost' composition='icon-only' size='sm' icon={<CloseIcon />} aria-label='Close' onClick={onClose} id='detail-panel-close' />
        </Box>

        {/* Savings banner */}
        {savings !== 0 && (
          <Box
            sx={{
              px: '20px',
              py: ds.space[2],
              borderBottom: `1px solid ${ds.gray[200]}`,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
            }}
          >
            <Box>
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontWeight: ds.weight.medium }}>Projected Monthly Savings</Typography>
              <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], fontStyle: 'italic' }}>Based on observed usage data</Typography>
            </Box>
            <Currency
              value={Math.abs(savings)}
              precison={2}
              withTooltip={false}
              sx={{
                fontSize: ds.text.title,
                fontWeight: ds.weight.semibold,
                color: savings > 0 ? ds.green[600] : ds.red[600],
              }}
            />
          </Box>
        )}

        {/* Tabs */}
        <Box sx={{ borderBottom: `1px solid ${ds.gray[200]}` }}>
          <Tabs
            value={activeTab}
            onChange={(_, newVal) => setActiveTab(newVal)}
            sx={{
              minHeight: '40px',
              '& .MuiTab-root': {
                textTransform: 'none',
                fontSize: ds.text.body,
                fontWeight: ds.weight.medium,
                minHeight: '40px',
                py: ds.space[2],
              },
              '& .Mui-selected': {
                color: `${ds.blue[600]} !important`,
                fontWeight: ds.weight.semibold,
              },
              '& .MuiTabs-indicator': {
                backgroundColor: ds.blue[500],
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
            <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[3] }}>Timeline</Typography>

            <Box sx={{ display: 'flex', flexDirection: 'column', gap: '0px' }}>
              {/* Created */}
              <Box sx={{ display: 'flex', gap: ds.space[3], alignItems: 'flex-start' }}>
                <Box
                  sx={{
                    width: '8px',
                    height: '8px',
                    borderRadius: '50%',
                    backgroundColor: ds.green[600],
                    mt: '5px',
                    flexShrink: 0,
                  }}
                />
                <Box sx={{ pb: ds.space[4], borderLeft: 'none' }}>
                  <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[700] }}>Recommendation created</Typography>
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
                    {formatDate(rec.created_at)} {daysSinceLong(rec.created_at) ? `(${daysSinceLong(rec.created_at)})` : ''}
                  </Typography>
                </Box>
              </Box>

              {/* Updated (if different from created) */}
              {rec.updated_at && rec.updated_at !== rec.created_at && (
                <Box sx={{ display: 'flex', gap: ds.space[3], alignItems: 'flex-start' }}>
                  <Box
                    sx={{
                      width: '8px',
                      height: '8px',
                      borderRadius: '50%',
                      backgroundColor: ds.blue[600],
                      mt: '5px',
                      flexShrink: 0,
                    }}
                  />
                  <Box sx={{ pb: ds.space[4] }}>
                    <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[700] }}>Last updated</Typography>
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
                      {formatDate(rec.updated_at)} {daysSinceLong(rec.updated_at) ? `(${daysSinceLong(rec.updated_at)})` : ''}
                    </Typography>
                  </Box>
                </Box>
              )}

              {/* Resolution info */}
              {rec.resolution && (
                <Box sx={{ display: 'flex', gap: ds.space[3], alignItems: 'flex-start' }}>
                  <Box
                    sx={{
                      width: '8px',
                      height: '8px',
                      borderRadius: '50%',
                      backgroundColor: ds.amber[500],
                      mt: '5px',
                      flexShrink: 0,
                    }}
                  />
                  <Box>
                    <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[700] }}>Resolution in progress</Typography>
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>PR: {rec.resolution.pr_url || 'Pending'}</Typography>
                  </Box>
                </Box>
              )}
            </Box>

            {/* Resolution History — inline lightweight table */}
            {rec.id && (
              <>
                <Divider sx={{ my: ds.space[4] }} />
                <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
                  Resolution History
                </Typography>
                <InlineResolutionHistory recommendationId={rec.id} />
              </>
            )}
          </Box>
        </Box>

        <ActionBar fullRecommendation={rec} onCreateTicket={onCreateTicket} onResolve={onResolve} onCopyCli={onCopyCli} onAskNubi={onAskNubi} />
      </Box>
    </Drawer>
  );
};

export default RecommendationDetailPanel;
