import React, { useEffect, useState, useCallback } from 'react';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import { Box } from '@mui/material';
import CloudProviderIcon from '@components1/common/CloudIcon';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import DownloadButton from '@common-new/DownloadButton';
import CustomTable2 from '@common-new/tables/CustomTable2';
import EmptyData from '@components1/common/EmptyData';
import { DataNotAvailable } from '@assets';
import Text from '@common-new/format/Text';
import { Label } from '@components1/ds/Label';
import apiUser from '@api1/user';
import apiTriage, { type ThresholdSuggestionItem } from '@api1/triage';
import { ds } from 'src/utils/colors';
import useKubernetesEventFilters from '@hooks/useKubernetesEventFilters';
import ThresholdEvidence, { RecentEventsTab } from './ThresholdEvidence';

const renderEvidence = (_opt: any, drilldownQuery: any): React.ReactElement => <ThresholdEvidence data={drilldownQuery} />;
const renderRecentEvents = (_opt: any, drilldownQuery: any): React.ReactElement => <RecentEventsTab data={drilldownQuery} />;

interface ThresholdSuggestionsManagerProps {
  accountId?: string;
}

interface AccountOption {
  id?: string;
  value?: string;
  label?: string;
  account_name?: string;
  cloud_provider?: string;
}

const SOURCE_OPTIONS = [
  { label: 'AWS CloudWatch', value: 'AWS_CloudWatch_Alarm' },
  { label: 'Azure Monitor', value: 'azure_monitor_webhook' },
  { label: 'Prometheus', value: 'prometheus' },
  { label: 'GCP Metric Alert', value: 'GCP_Metric_Alert' },
  { label: 'PagerDuty', value: 'pagerduty_webhook' },
];

const CONFIDENCE_OPTIONS = [
  { label: 'High', value: 'high' },
  { label: 'Medium', value: 'medium' },
  { label: 'Low', value: 'low' },
];

const CONFIDENCE_VARIANTS: Record<string, string> = {
  high: 'green',
  medium: 'yellow',
  low: 'grey',
};

const RECOMMENDATION_LABELS: Record<string, { label: string; variant: string }> = {
  tune_threshold: { label: 'Tune Threshold', variant: 'blue' },
  increase_duration: { label: 'Increase Duration', variant: 'orange' },
  tune_both: { label: 'Tune Both', variant: 'blue' },
  disable: { label: 'Disable Alert', variant: 'red' },
  none: { label: 'No Change', variant: 'green' },
  review_alert: { label: 'Review Alert', variant: 'orange' },
  not_eligible: { label: 'Not Eligible', variant: 'grey' },
};

const RISK_COLORS: Record<string, { bg: string; text: string; icon: string }> = {
  dangerous: { bg: ds.red[100], text: ds.red[700], icon: '' },
  review: { bg: ds.amber[100], text: ds.amber[700], icon: '' },
  safe: { bg: ds.green[100], text: ds.green[700], icon: '' },
};

const getRecommendationType = (item: ThresholdSuggestionItem): string => {
  return item.metric_stats?.recommendation_type || item.recommendation_type || 'tune_threshold';
};

const getRiskLevel = (item: ThresholdSuggestionItem): string => {
  return item.metric_stats?.risk_level || 'safe';
};

const formatNoiseReduction = (item: ThresholdSuggestionItem): string => {
  if (item.estimated_reduction == null) {
    return '-';
  }
  const reduction = Math.round(item.estimated_reduction);
  if (reduction === 100) {
    return 'Suppresses all';
  }
  return `${reduction}%`;
};

const renderAccountGroupIcon = (provider: string) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const ThresholdSuggestionsManager: React.FC<ThresholdSuggestionsManagerProps> = ({ accountId }) => {
  const router = useRouter();
  const tableId = 'thresholdSuggestionsManager';
  const isMultiAccountView = !accountId;

  const { accounts } = useKubernetesEventFilters({
    selectedAccountId: accountId,
    isTroubleshootPage: isMultiAccountView,
    enableFilters: isMultiAccountView,
    disabledFilters: ['workload', 'namespace', 'subjectType', 'aggregationKey', 'source'],
    resource_ids: [],
  }) as { accounts: AccountOption[] };

  const [suggestions, setSuggestions] = useState<ThresholdSuggestionItem[]>([]);
  const [tableData, setTableData] = useState<any[]>([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);

  const [currentPage, setCurrentPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  const [selectedSource, setSelectedSource] = useState<string>('');
  const [selectedConfidence, setSelectedConfidence] = useState<string>('');
  const [selectedAccountFilter, setSelectedAccountFilter] = useState<string[]>(() => {
    const raw = router.query.accountId as string;
    return raw ? raw.split(',').filter(Boolean) : [];
  });

  useEffect(() => {
    const raw = router.query.accountId as string;
    setSelectedAccountFilter(raw ? raw.split(',').filter(Boolean) : []);
  }, [router.query.accountId]);

  const fetchSuggestions = useCallback(async () => {
    setLoading(true);
    try {
      const result = await apiTriage.listThresholdSuggestions({
        cloud_account_id: accountId || undefined,
        cloud_account_ids: !accountId && selectedAccountFilter.length ? selectedAccountFilter : undefined,
        source: selectedSource || undefined,
        confidence: selectedConfidence || undefined,
        limit: rowsPerPage,
        offset: currentPage * rowsPerPage,
      });
      if (result) {
        setSuggestions(result.suggestions || []);
        setTotalCount(result.total || 0);
      }
    } catch (error) {
      console.error('Failed to fetch threshold suggestions:', error);
    } finally {
      setLoading(false);
    }
  }, [accountId, selectedSource, selectedConfidence, selectedAccountFilter, currentPage, rowsPerPage]);

  useEffect(() => {
    fetchSuggestions();
  }, [fetchSuggestions]);

  useEffect(() => {
    const data = suggestions.map((item) => {
      const confidenceVariant = CONFIDENCE_VARIANTS[item.confidence || ''] || CONFIDENCE_VARIANTS.low;
      const sourceLabel = SOURCE_OPTIONS.find((s) => s.value === item.source)?.label || item.source;

      const accountCell: any[] = [];
      if (isMultiAccountView) {
        const account = accounts.find((acc) => (acc.id || acc.value) === item.cloud_account_id);
        accountCell.push({
          component: <Text showAutoEllipsis value={account?.label || account?.account_name || item.cloud_account_id} />,
        });
      }

      const recType = getRecommendationType(item);
      const recStyle = RECOMMENDATION_LABELS[recType] || RECOMMENDATION_LABELS.tune_threshold;
      const riskLevel = getRiskLevel(item);
      const riskStyle = RISK_COLORS[riskLevel] || RISK_COLORS.safe;
      const noiseText = formatNoiseReduction(item);

      return [
        ...accountCell,
        {
          drilldownQuery: item,
          component: (
            <Box>
              <Text showAutoEllipsis value={item.alert_name || '-'} />
              <Text showAutoEllipsis value={sourceLabel} sx={{ fontSize: ds.text.caption, color: ds.gray[600] }} />
            </Box>
          ),
        },
        {
          component: (
            <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
              <Label text={recStyle.label} variant={recStyle.variant} dot />
              {riskLevel !== 'safe' && (
                <Text
                  value='Needs review'
                  sx={{ fontSize: ds.text.small, color: riskStyle.text, mt: 'var(--ds-space-1)', fontWeight: ds.weight.medium }}
                />
              )}
            </Box>
          ),
        },
        {
          component: (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
              <Text value={formatThreshold(item.current_threshold, item.operator)} />
              {item.current_threshold !== item.suggested_threshold &&
                recType !== 'disable' &&
                recType !== 'none' &&
                recType !== 'increase_duration' &&
                recType !== 'review_alert' &&
                recType !== 'not_eligible' && (
                  <>
                    <Text value='→' sx={{ color: ds.gray[600] }} />
                    <Text value={formatThreshold(item.suggested_threshold, item.operator)} sx={{ fontWeight: ds.weight.semibold }} />
                  </>
                )}
            </Box>
          ),
        },
        {
          component: (
            <Box sx={{ display: 'flex', justifyContent: 'center' }}>
              <Label text={item.confidence || '-'} variant={confidenceVariant} />
            </Box>
          ),
        },
        {
          component: (
            <Box sx={{ display: 'flex', justifyContent: 'center' }}>
              <Text
                value={noiseText}
                sx={{
                  fontWeight: ds.weight.semibold,
                  color: noiseText === 'Suppresses all' ? ds.red[700] : 'inherit',
                  fontSize: noiseText === 'Suppresses all' ? ds.text.small : ds.text.body,
                }}
              />
            </Box>
          ),
        },
      ];
    });

    setTableData(data);
  }, [suggestions, isMultiAccountView, accounts]);

  const formatNumber = (value: number): string => {
    const abs = Math.abs(value);
    if (abs >= 1e9) {
      return `${(value / 1e9).toFixed(1)}B`;
    }
    if (abs >= 1e6) {
      return `${(value / 1e6).toFixed(1)}M`;
    }
    if (abs >= 1e4) {
      return `${(value / 1e3).toFixed(1)}K`;
    }
    return value.toFixed(2);
  };

  const operatorSymbol = (operator?: string): string => {
    const op = (operator || '').toLowerCase();
    if (op.includes('greaterthanorequalto') || op === '>=') {
      return '>=';
    }
    if (op.includes('greaterthan') || op === '>') {
      return '>';
    }
    if (op.includes('lessthanorequalto') || op === '<=') {
      return '<=';
    }
    if (op.includes('lessthan') || op === '<') {
      return '<';
    }
    return '>';
  };

  const formatThreshold = (value?: number, operator?: string): string => {
    if (value == null) {
      return '-';
    }
    return `${operatorSymbol(operator)} ${formatNumber(value)}`;
  };

  const onPageChange = (page: number, newRowsPerPage: number) => {
    setCurrentPage(page - 1);
    if (newRowsPerPage !== rowsPerPage) {
      setRowsPerPage(newRowsPerPage);
      setCurrentPage(0);
    }
  };

  return (
    <div>
      <ListingLayout id='threshold-suggestions-list-box'>
        <ListingLayout.Toolbar
          data-testid='threshold-suggestions-filter-toolbar'
          actions={<DownloadButton id={`${tableId}-download`} onClick={() => ({ tableId })} />}
        >
          {isMultiAccountView && (
            <FilterDropdown
              id='threshold-suggestions-filter-account'
              label='Account'
              multiple
              grouped
              groupIcon={renderAccountGroupIcon}
              options={accounts.map((acc: AccountOption) => ({
                label: acc.label || acc.account_name || acc.id,
                value: acc.id || acc.value,
                group: acc.cloud_provider || 'Other',
              }))}
              value={accounts
                .filter((acc: AccountOption) => selectedAccountFilter.includes((acc.id || acc.value) as string))
                .map((acc: AccountOption) => ({
                  label: acc.label || acc.account_name || acc.id,
                  value: acc.id || acc.value,
                  group: acc.cloud_provider || 'Other',
                }))}
              onSelect={(_e: any, items: any) => {
                const ids = (Array.isArray(items) ? items : []).map((v: any) => v.value);
                setSelectedAccountFilter(ids);
                setCurrentPage(0);
                applyFiltersOnRouter(router, { accountId: ids.join(',') });
              }}
            />
          )}
          <FilterDropdown
            id='threshold-suggestions-filter-source'
            label='Source'
            options={SOURCE_OPTIONS}
            value={SOURCE_OPTIONS.find((o) => o.value === selectedSource) ?? null}
            onSelect={(_e: any, item: any) => {
              setSelectedSource(item?.value || '');
              setCurrentPage(0);
            }}
          />
          <FilterDropdown
            id='threshold-suggestions-filter-confidence'
            label='Confidence'
            options={CONFIDENCE_OPTIONS}
            value={CONFIDENCE_OPTIONS.find((o) => o.value === selectedConfidence) ?? null}
            onSelect={(_e: any, item: any) => {
              setSelectedConfidence(item?.value || '');
              setCurrentPage(0);
            }}
          />
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          {!loading && suggestions.length === 0 ? (
            <EmptyData
              id='threshold-suggestions-empty'
              img={DataNotAvailable}
              heading='No Data Available'
              subHeading='Alert tuning suggestions are automatically generated for noisy alerts with 5+ firings in the last 30 days.'
            />
          ) : (
            <CustomTable2
              id={tableId}
              totalRows={totalCount}
              tableData={tableData}
              headers={[
                ...(isMultiAccountView ? [{ name: 'Account', width: '10%' }] : []),
                { name: 'Alert', width: isMultiAccountView ? '20%' : '25%' },
                { name: 'Recommendation', width: '16%' },
                { name: 'Threshold', width: isMultiAccountView ? '20%' : '25%' },
                { name: 'Confidence', width: '14%' },
                { name: 'Noise Reduction', width: '20%' },
              ]}
              rowsPerPage={rowsPerPage}
              loading={loading}
              onPageChange={onPageChange}
              onSortChange={undefined}
              pageNumber={currentPage + 1}
              tableHeadingCenter={['Recommendation', 'Confidence', 'Noise Reduction']}
              showExpandable
              expandable={{
                tabs: [
                  {
                    text: 'Evidence',
                    value: 0,
                    key: 'evidence',
                    componentFn: renderEvidence,
                  },
                  {
                    text: 'Recent Events',
                    value: 1,
                    key: 'recent-events',
                    componentFn: renderRecentEvents,
                  },
                ],
              }}
            />
          )}
        </ListingLayout.Body>
      </ListingLayout>
    </div>
  );
};

export default ThresholdSuggestionsManager;
