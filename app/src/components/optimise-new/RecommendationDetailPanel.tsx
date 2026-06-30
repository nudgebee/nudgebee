import { Box, Typography, Tabs, Tab, Divider, Drawer } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import LinkIcon from '@mui/icons-material/Link';
import { useState, useEffect } from 'react';
import { usePagination } from '@hooks/usePagination';
import CustomTable2 from '@shared/tables/CustomTable2';
import { ds } from 'src/utils/colors';
import { Label, type LabelTone } from '@ui/Label';
import { Button } from '@ui/Button';
import { type SeverityLevel } from './SeverityBadge';
import EvidencePanel from './EvidencePanel';
import DetailsPanel from './DetailsPanel';
import ActionBar from './ActionBar';
import Currency from '@shared/format/Currency';
import recommendationApi from '@api1/recommendation';
import { daysSinceLong, getResourceDisplayName } from './utils';
import CommandExecutionHistory from '@components/cloudaccount/CommandExecutionHistory';

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
  accounts?: Record<string, { name: string; cloud_provider: string; account_access?: string }>;
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

const RESOLUTION_HEADERS = [
  { name: 'Type', width: '20%' },
  { name: 'Reference', width: '25%' },
  { name: 'Resolver', width: '15%' },
  { name: 'Status', width: '15%' },
  { name: 'Updated', width: '25%' },
];

/** Inline Resolution History — paginated table that fits the drawer */
const InlineResolutionHistory = ({ recommendationId }: { recommendationId: string }) => {
  const [resolutions, setResolutions] = useState<any[]>([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(true);
  const { page, rowsPerPage, changePage } = usePagination(5);

  useEffect(() => {
    if (!recommendationId) return;
    setLoading(true);
    recommendationApi
      .listRecommendationResolution(recommendationId, rowsPerPage, page * rowsPerPage)
      .then((res: any) => {
        setResolutions(res?.data?.recommendation_resolution || []);
        setTotalCount(res?.data?.recommendation_resolution_aggregate?.aggregate?.count || 0);
      })
      .catch(() => {
        setResolutions([]);
        setTotalCount(0);
      })
      .finally(() => setLoading(false));
  }, [recommendationId, page, rowsPerPage]);

  if (!loading && resolutions.length === 0) {
    return (
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'center',
          py: ds.space[3],
          borderRadius: ds.radius.lg,
          border: `1px solid ${ds.gray[200]}`,
          backgroundColor: ds.background[100],
        }}
      >
        <Typography sx={{ fontSize: ds.text.body, color: ds.gray[600] }}>No resolution history found.</Typography>
      </Box>
    );
  }

  const tableData = resolutions.map((r: any) => {
    const isLink = r.type_reference_id && (r.type_reference_id.startsWith('http') || r.type_reference_id.startsWith('/'));
    return [
      {
        component: <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[700] }}>{r.type || '—'}</Typography>,
      },
      {
        component: isLink ? (
          <Box
            component='a'
            href={r.type_reference_id}
            target='_blank'
            rel='noopener'
            sx={{ fontSize: ds.text.caption, color: ds.blue[600], display: 'flex', alignItems: 'center', gap: ds.space[0] }}
          >
            <LinkIcon sx={{ fontSize: ds.text.caption }} />
            Link
          </Box>
        ) : (
          <Typography
            sx={{
              fontSize: ds.text.caption,
              color: ds.gray[700],
              maxWidth: ds.space.mul(1, 25),
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {r.type_reference_id || '—'}
          </Typography>
        ),
      },
      {
        component: <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[700] }}>{r.resolver_type || '—'}</Typography>,
      },
      {
        component: (
          <Label size='sm' tone={resolutionTone(r.status)}>
            {r.status || '—'}
          </Label>
        ),
      },
      {
        component: <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>{formatDateShort(r.updated_at)}</Typography>,
      },
    ];
  });

  return (
    <CustomTable2
      id={`resolution-history-${recommendationId}`}
      headers={RESOLUTION_HEADERS}
      tableData={tableData}
      rowsPerPage={rowsPerPage}
      onPageChange={changePage}
      totalRows={totalCount}
      loading={loading}
      pageNumber={page + 1}
    />
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
          boxShadow: `0px ${ds.space[1]} 20px -1px color-mix(in srgb, ${ds.gray[300]} 40%, transparent), ${ds.space.mul(1, -1)} 0px 20px ${
            ds.gray.alpha[200]
          }`,
          borderLeft: `1px solid ${ds.gray[200]}`,
        },
      }}
    >
      <Box sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
        {/* Header */}
        <Box
          sx={{
            p: `${ds.space[4]} 20px`,
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
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], mt: ds.space[0] }}>
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
          <Box sx={{ p: `${ds.space[4]} 20px` }}>
            <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[3] }}>Timeline</Typography>

            <Box sx={{ display: 'flex', flexDirection: 'column', gap: '0px' }}>
              {/* Created */}
              <Box sx={{ display: 'flex', gap: ds.space[3], alignItems: 'flex-start' }}>
                <Box
                  sx={{
                    width: ds.space[2],
                    height: ds.space[2],
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
                      width: ds.space[2],
                      height: ds.space[2],
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
                      width: ds.space[2],
                      height: ds.space[2],
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

            {/* Command Execution History — CLI runs tied to this recommendation */}
            {rec.id && rec.account_id && (
              <>
                <Divider sx={{ my: ds.space[4] }} />
                <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
                  Command Execution History
                </Typography>
                <CommandExecutionHistory accountId={rec.account_id} recommendationId={rec.id} />
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
