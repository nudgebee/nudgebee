import { Box, Typography, Divider } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import LinkIcon from '@mui/icons-material/Link';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import QueryStatsOutlinedIcon from '@mui/icons-material/QueryStatsOutlined';
import HistoryOutlinedIcon from '@mui/icons-material/HistoryOutlined';
import { useState, useEffect } from 'react';
import { usePagination } from '@hooks/usePagination';
import CustomTable from '@shared/tables/CustomTable';
import Tabs from '@shared/navigation/Tabs';
import { ds } from 'src/utils/colors';
import { Label, type LabelTone } from '@ui/Label';
import { Button } from '@ui/Button';
import { type SeverityLevel } from './SeverityBadge';
import EvidencePanel from './EvidencePanel';
import DetailsPanel from './DetailsPanel';
import ActionBar from './ActionBar';
import CustomDrawer from '@shared/CustomDrawer';
import Currency from '@shared/format/Currency';
import Datetime from '@shared/format/Datetime';
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

// Terminal resolution states, keyed by status. A status absent from this map is
// still running, and falls back to the in-progress wording below. `Success`
// means no PR was raised — a PR that was raised keeps its resolution InProgress.
const RESOLUTION_SUMMARY: Record<string, { dot: string; title: string; fallback: string }> = {
  Failed: { dot: ds.red[500], title: 'Resolution failed', fallback: 'PR creation failed' },
  Success: { dot: ds.green[500], title: 'No PR needed', fallback: 'No change was required' },
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

const RESOLUTION_HEADERS = [
  { name: 'Type', width: '20%' },
  { name: 'Reference', width: '25%' },
  { name: 'Resolver', width: '15%' },
  { name: 'Status', width: '15%' },
  { name: 'Updated', width: '25%' },
];

/** Inline Resolution History — paginated table that fits the drawer */
const InlineResolutionHistory = ({ recommendationId, refreshKey }: { recommendationId: string; refreshKey?: number }) => {
  const [resolutions, setResolutions] = useState<any[]>([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(true);
  const { page, rowsPerPage, changePage } = usePagination(5);

  useEffect(() => {
    if (!recommendationId) return;
    let cancelled = false;
    setLoading(true);
    recommendationApi
      .listRecommendationResolution(recommendationId, rowsPerPage, page * rowsPerPage)
      .then((res: any) => {
        if (cancelled) return;
        setResolutions(res?.data?.recommendation_resolution || []);
        setTotalCount(res?.data?.recommendation_resolution_aggregate?.aggregate?.count || 0);
      })
      .catch(() => {
        if (cancelled) return;
        setResolutions([]);
        setTotalCount(0);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [recommendationId, page, rowsPerPage, refreshKey]);

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
        component: <Datetime value={r.updated_at} />,
      },
    ];
  });

  return (
    <CustomTable
      id={`resolution-history-${recommendationId}`}
      headers={RESOLUTION_HEADERS}
      tableData={tableData}
      rowsPerPage={rowsPerPage}
      onPageChange={changePage}
      totalRows={totalCount}
      loading={loading}
      pageNumber={page + 1}
      showEmptyStateText
      emptyStateText='No resolution history found.'
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

  const [historyRefreshKey, setHistoryRefreshKey] = useState(0);
  const [prevHistoryRefreshKey, setPrevHistoryRefreshKey] = useState(0);
  const [showHistory, setShowHistory] = useState(false);

  const refreshHistory = (tab: number = 0) => {
    if (tab !== 2) return;
    setShowHistory(true);
    if (prevHistoryRefreshKey !== historyRefreshKey) {
      setHistoryRefreshKey(prevHistoryRefreshKey);
    }
  };

  useEffect(() => {
    if (open) {
      setActiveTab(initialTab);
      refreshHistory(initialTab);
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
    <CustomDrawer open={open} onClose={onClose} bare nonModal variant='modern' width='720px' storageKey='nb.optimizeDrawer.width'>
      <Box data-testid='recommendation-detail-panel' sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
        {/* Header */}
        <Box
          sx={{
            p: `${ds.space[4]} ${ds.space[5]} 0 ${ds.space[5]}`,
            display: 'flex',
            alignItems: 'flex-start',
            gap: ds.space[3],
          }}
        >
          <Box sx={{ flex: 1 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[2], flexWrap: 'wrap' }}>
              <Label size='sm' tone={SEVERITY_TONE[severity] ?? 'neutral'}>
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
                letterSpacing: '-0.01em',
              }}
            >
              {resourceName}
            </Typography>
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], mt: ds.space[0] }}>
              {resourceType}
              {namespace ? ` · ${namespace}` : ''}
              {accountName ? ` · ${accountName}` : ''}
              {rec.created_at && daysSinceLong(rec.created_at) ? ` · detected ${daysSinceLong(rec.created_at)}` : ''}
            </Typography>
          </Box>
          <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: ds.space[2], flexShrink: 0 }}>
            <Button
              tone='ghost'
              composition='icon-only'
              size='sm'
              icon={<CloseIcon />}
              aria-label='Close'
              onClick={onClose}
              id='detail-panel-close'
            />
            {savings !== 0 && (
              <Box sx={{ textAlign: 'right' }}>
                <Typography
                  sx={{
                    fontSize: ds.text.caption,
                    color: ds.gray[500],
                    fontWeight: ds.weight.medium,
                    whiteSpace: 'nowrap',
                    textTransform: 'uppercase',
                    letterSpacing: '0.04em',
                  }}
                >
                  Projected Savings
                </Typography>
                <Box sx={{ display: 'flex', alignItems: 'baseline', justifyContent: 'flex-end', gap: ds.space[1], mt: ds.space[0] }}>
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
                  <Box component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[400], fontWeight: ds.weight.medium }}>
                    /mo
                  </Box>
                </Box>
              </Box>
            )}
          </Box>
        </Box>

        {/* Tabs */}
        <Box sx={{ px: ds.space[5], pt: ds.space[2], borderBottom: `1px solid ${ds.gray[200]}` }}>
          <Tabs
            value={activeTab}
            onChange={(newVal: number) => {
              setActiveTab(newVal);
              refreshHistory(newVal);
            }}
            behavior='filter'
            showSurface={false}
            variant='primary'
            ariaLabel='recommendation detail tabs'
            options={{
              tabOptions: [
                { value: 0, text: 'Details', id: 'detail-tab-details', icon: <InfoOutlinedIcon sx={{ fontSize: 16 }} /> },
                { value: 1, text: 'Evidence', id: 'detail-tab-evidence', icon: <QueryStatsOutlinedIcon sx={{ fontSize: 16 }} /> },
                { value: 2, text: 'History', id: 'detail-tab-history', icon: <HistoryOutlinedIcon sx={{ fontSize: 16 }} /> },
              ],
            }}
          />
        </Box>

        {/* Tab content — all tabs are always mounted so data fetches start immediately */}
        <Box sx={{ flex: 1, overflow: 'auto', display: activeTab === 0 ? 'block' : 'none' }}>
          <DetailsPanel
            fullRecommendation={rec}
            accounts={accounts}
            onViewEvidence={() => setActiveTab(1)}
            onMitigationExecuted={() => setPrevHistoryRefreshKey((k) => k + 1)}
          />
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
          <Box sx={{ p: ds.space[5] }}>
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[3] }}>Timeline</Typography>

            <Box sx={{ display: 'flex', flexDirection: 'column', gap: '0px' }}>
              {/* Created */}
              <Box sx={{ display: 'flex', gap: ds.space[3], alignItems: 'flex-start' }}>
                <Box
                  sx={{
                    width: ds.space[2],
                    height: ds.space[2],
                    borderRadius: '50%',
                    backgroundColor: ds.green[600],
                    mt: ds.space[1],
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
                      mt: ds.space[1],
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
                      backgroundColor: RESOLUTION_SUMMARY[rec.resolution.status]?.dot ?? ds.amber[500],
                      mt: ds.space[1],
                      flexShrink: 0,
                    }}
                  />
                  <Box>
                    <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[700] }}>
                      {RESOLUTION_SUMMARY[rec.resolution.status]?.title ?? 'Resolution in progress'}
                    </Typography>
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
                      {RESOLUTION_SUMMARY[rec.resolution.status]
                        ? rec.resolution.status_message || RESOLUTION_SUMMARY[rec.resolution.status].fallback
                        : `PR: ${rec.resolution.pr_url || 'Pending'}`}
                    </Typography>
                  </Box>
                </Box>
              )}
            </Box>

            {/* Resolution History — inline lightweight table */}
            {rec.id && showHistory && (
              <>
                <Divider sx={{ my: ds.space[4] }} />
                <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
                  Resolution History
                </Typography>
                <InlineResolutionHistory recommendationId={rec.id} refreshKey={historyRefreshKey} />
              </>
            )}

            {/* Command Execution History — CLI runs tied to this recommendation */}
            {rec.id && rec.account_id && showHistory && (
              <>
                <Divider sx={{ my: ds.space[4] }} />
                <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
                  Command Execution History
                </Typography>
                <CommandExecutionHistory accountId={rec.account_id} recommendationId={rec.id} refreshKey={historyRefreshKey} />
              </>
            )}
          </Box>
        </Box>

        <ActionBar fullRecommendation={rec} onCreateTicket={onCreateTicket} onResolve={onResolve} onCopyCli={onCopyCli} onAskNubi={onAskNubi} />
      </Box>
    </CustomDrawer>
  );
};

export default RecommendationDetailPanel;
