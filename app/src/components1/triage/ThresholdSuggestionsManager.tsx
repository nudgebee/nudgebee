import React, { useEffect, useState, useCallback } from 'react';
import { Box, Chip } from '@mui/material';
import BoxLayout2 from '@components1/common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import EmptyData from '@components1/common/EmptyData';
import { DataNotAvailable } from '@assets';
import { Text } from '@components1/common';
import apiUser from '@api1/user';
import apiTriage, { type ThresholdSuggestionItem } from '@api1/triage';
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

const CONFIDENCE_COLORS: Record<string, { bg: string; text: string; variant: string }> = {
  high: { bg: '#e8f5e9', text: '#2e7d32', variant: 'green' },
  medium: { bg: '#fff3e0', text: '#e65100', variant: 'yellow' },
  low: { bg: '#fce4ec', text: '#c62828', variant: 'grey' },
};

const RECOMMENDATION_LABELS: Record<string, { label: string; color: string; bg: string }> = {
  tune_threshold: { label: 'Tune Threshold', color: '#1565c0', bg: '#e3f2fd' },
  increase_duration: { label: 'Increase Duration', color: '#e65100', bg: '#fff3e0' },
  tune_both: { label: 'Tune Both', color: '#6a1b9a', bg: '#f3e5f5' },
  disable: { label: 'Disable Alert', color: '#c62828', bg: '#fce4ec' },
  none: { label: 'No Change', color: '#2e7d32', bg: '#e8f5e9' },
  review_alert: { label: 'Review Alert', color: '#bf360c', bg: '#fbe9e7' },
  not_eligible: { label: 'Not Eligible', color: '#546e7a', bg: '#eceff1' },
};

const RISK_COLORS: Record<string, { bg: string; text: string; icon: string }> = {
  dangerous: { bg: '#fce4ec', text: '#c62828', icon: '' },
  review: { bg: '#fff8e1', text: '#f57f17', icon: '' },
  safe: { bg: '#e8f5e9', text: '#2e7d32', icon: '' },
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

const ThresholdSuggestionsManager: React.FC<ThresholdSuggestionsManagerProps> = ({ accountId }) => {
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
  const [selectedAccountFilter, setSelectedAccountFilter] = useState<string>('');

  const fetchSuggestions = useCallback(async () => {
    setLoading(true);
    try {
      const result = await apiTriage.listThresholdSuggestions({
        cloud_account_id: accountId || selectedAccountFilter || undefined,
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
      const confidenceColor = CONFIDENCE_COLORS[item.confidence || ''] || CONFIDENCE_COLORS.low;
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
              <Text showAutoEllipsis value={sourceLabel} sx={{ fontSize: '12px', color: '#616161' }} />
            </Box>
          ),
        },
        {
          component: (
            <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
              <Chip
                label={recStyle.label}
                size='small'
                sx={{ backgroundColor: recStyle.bg, color: recStyle.color, fontWeight: 600, fontSize: '11px', height: '22px', mb: '2px' }}
              />
              {riskLevel !== 'safe' && <Text value='Needs review' sx={{ fontSize: '11px', color: riskStyle.text, mt: '2px', fontWeight: 500 }} />}
            </Box>
          ),
        },
        {
          component: (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
              <Text value={formatThreshold(item.current_threshold, item.operator)} />
              {item.current_threshold !== item.suggested_threshold &&
                recType !== 'disable' &&
                recType !== 'none' &&
                recType !== 'increase_duration' &&
                recType !== 'review_alert' &&
                recType !== 'not_eligible' && (
                  <>
                    <Text value='→' sx={{ color: '#616161' }} />
                    <Text value={formatThreshold(item.suggested_threshold, item.operator)} sx={{ fontWeight: 600 }} />
                  </>
                )}
            </Box>
          ),
        },
        {
          component: (
            <Box sx={{ display: 'flex', justifyContent: 'center' }}>
              <Chip
                label={item.confidence || '-'}
                size='small'
                sx={{
                  backgroundColor: confidenceColor.bg,
                  color: confidenceColor.text,
                  fontWeight: 600,
                  textTransform: 'capitalize',
                  fontSize: '12px',
                }}
              />
            </Box>
          ),
        },
        {
          component: (
            <Box sx={{ display: 'flex', justifyContent: 'center' }}>
              <Text
                value={noiseText}
                sx={{
                  fontWeight: 600,
                  color: noiseText === 'Suppresses all' ? '#c62828' : 'inherit',
                  fontSize: noiseText === 'Suppresses all' ? '11px' : '13px',
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
      <BoxLayout2
        id='threshold-suggestions-list-box'
        heading=''
        sharingOptions={{
          sharing: { enabled: false, onClick: null },
          download: {
            enabled: true,
            onClick: () => ({ tableId }),
          },
        }}
        filterOptions={[
          ...(isMultiAccountView
            ? [
                {
                  type: 'dropdown' as const,
                  enabled: true,
                  options: accounts.map((acc) => ({
                    label: acc.label || acc.account_name || acc.id,
                    value: acc.id || acc.value,
                  })),
                  onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                    setSelectedAccountFilter(e?.target?.value);
                    setCurrentPage(0);
                  },
                  minWidth: '200px',
                  label: 'Account',
                  value: selectedAccountFilter,
                },
              ]
            : []),
          {
            type: 'dropdown' as const,
            enabled: true,
            options: SOURCE_OPTIONS,
            onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
              setSelectedSource(e?.target?.value);
              setCurrentPage(0);
            },
            minWidth: '150px',
            label: 'Source',
            value: selectedSource,
          },
          {
            type: 'dropdown' as const,
            enabled: true,
            options: CONFIDENCE_OPTIONS,
            onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
              setSelectedConfidence(e?.target?.value);
              setCurrentPage(0);
            },
            minWidth: '150px',
            label: 'Confidence',
            value: selectedConfidence,
          },
        ]}
      >
        {!loading && suggestions.length === 0 ? (
          <EmptyData
            id='threshold-suggestions-empty'
            img={DataNotAvailable}
            heading='No Data Available'
            subHeading='Alert tuning suggestions are automatically generated for noisy alerts with 5+ firings in the last 30 days.'
          />
        ) : (
          <KubernetesTable2
            id={tableId}
            totalRows={totalCount}
            data={tableData}
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
      </BoxLayout2>
    </div>
  );
};

export default ThresholdSuggestionsManager;
