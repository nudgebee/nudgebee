import React, { useEffect, useState } from 'react';
import apiRecommendations from '@api1/recommendation';
import apiUser from '@api1/user';
import Currency from '@common-new/format/Currency';
import Datetime from '@common-new/format/Datetime';
import Text from '@common-new/format/Text';
import { Label } from '@components1/ds/Label';
import { Box, Typography } from '@mui/material';
import Link from 'next/link';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';
import { containsLink, snakeToTitleCase } from 'src/utils/common';
import useCurrencySymbol from '@hooks/useCurrencySymbol';

import { ListingLayout } from '@components1/ds/ListingLayout';
import CustomTable2 from '@common-new/tables/CustomTable2';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { SeverityIcon } from '@components1/ds/SeverityIcon';
import DownloadButton from '@common-new/DownloadButton';

const SEVERITY_LEVELS = new Set(['critical', 'high', 'medium', 'low', 'info']);
const toSeverityLevel = (s) => {
  const normalized = String(s || '').toLowerCase();
  return SEVERITY_LEVELS.has(normalized) ? normalized : 'info';
};

/**
 * Render a flat object as a 2-column key/value list using DS Typography.
 * Snake_case keys are title-cased. Non-scalar values fall back to JSON.stringify.
 * Returns null for non-object / empty input — caller decides the empty state.
 */
const KeyValueList = ({ data }) => {
  if (!data || typeof data !== 'object' || Array.isArray(data)) {
    return null;
  }
  const entries = Object.entries(data);
  if (entries.length === 0) {
    return null;
  }

  const formatValue = (value) => {
    if (value === null || value === undefined || value === '') return '—';
    if (typeof value === 'boolean') return value ? 'Yes' : 'No';
    if (typeof value === 'object') return JSON.stringify(value);
    return String(value);
  };

  return (
    <Box
      sx={{
        display: 'grid',
        gridTemplateColumns: 'minmax(140px, max-content) 1fr',
        columnGap: ds.space[4],
        rowGap: ds.space[3],
      }}
    >
      {entries.map(([key, value]) => (
        <React.Fragment key={key}>
          <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[600] }}>{snakeToTitleCase(key)}</Typography>
          <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700], wordBreak: 'break-word' }}>{formatValue(value)}</Typography>
        </React.Fragment>
      ))}
    </Box>
  );
};

KeyValueList.propTypes = {
  data: PropTypes.object,
};

const RESOLUTION_HEADERS = [
  { name: 'Recommendation', width: '32%' },
  { name: 'Severity', width: '10%' },
  { name: 'Status', width: '10%' },
  { name: 'Est. Savings', width: '10%' },
  { name: 'Resolver', width: '14%' },
  { name: 'Type', width: '12%' },
  { name: 'Updated At', width: '12%' },
];

const RED_LABELS = new Set([
  'error',
  'firing',
  'failed',
  'suspended',
  'high',
  'disabled',
  'highest',
  'rejected',
  'unhealthy',
  'incompatible',
  'critical',
]);
const GREEN_LABELS = new Set([
  'complete',
  'active',
  'succeeded',
  'resolved',
  'closed',
  'done',
  'ok',
  'enabled',
  'approved',
  'success',
  'completed',
  'healthy',
  'compatible',
]);
const YELLOW_LABELS = new Set(['pending', 'inactive', 'in progress', 'skipped', 'medium', 'in_progress']);
const BLUE_LABELS = new Set(['low', 'open', 'assigned']);

const statusToLabelTone = (text) => {
  if (!text) return 'neutral';
  const t = String(text).toLowerCase();
  if (RED_LABELS.has(t)) return 'critical';
  if (GREEN_LABELS.has(t)) return 'success';
  if (YELLOW_LABELS.has(t)) return 'warning';
  if (BLUE_LABELS.has(t)) return 'info';
  return 'neutral';
};

const ListingRecommendationResolution = ({ accountId }) => {
  const recommendationResolutionTableId = 'recommendation-resolution';

  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [selectedStatus, setSelectedStatus] = useState('InProgress');
  const [recommendationTypes, setRecommendationTypes] = useState([]);
  const [resolverTypes, setResolverTypes] = useState([]);
  const [selectedRecommendation, setSelectedRecommendation] = useState('');
  const [selectedResolver, setSelectedResolver] = useState('');
  const currencySymbol = useCurrencySymbol(accountId);

  useEffect(() => {
    apiRecommendations.getDistinctResolverTypes('type').then((res) => {
      setRecommendationTypes(
        res?.data?.data?.recommendation_resolution.map((item) => {
          return item.type;
        })
      );
    });
    apiRecommendations.getDistinctResolverTypes('resolver_type').then((res) => {
      setResolverTypes(
        res?.data?.data?.recommendation_resolution.map((item) => {
          return item.resolver_type;
        })
      );
    });
  }, []);

  const changePage = (nextPage, limit) => {
    setPage(nextPage - 1);
    setRowsPerPage(limit);
  };

  const getResolutionListingData = () => {
    setData([]);
    setLoading(true);
    apiRecommendations
      .getRecommendationResolution({
        limit: rowsPerPage,
        offset: rowsPerPage * page,
        accountId: accountId,
        status: selectedStatus,
        type: selectedRecommendation,
        resolverType: selectedResolver,
      })
      .then((res) => {
        const resolutionData = res?.data?.data?.recommendation_resolution || [];
        const resolutionCount = res?.data?.data?.recommendation_resolution_aggregate?.aggregate?.count || 0;

        if (resolutionData.length > 0) {
          const builtCells = resolutionData.map((rr) => {
            const namespace =
              rr.recommendation.cloud_resourse?.meta?.namespace ||
              rr.recommendation.cloud_resourse?.meta?.config?.namespace ||
              rr.recommendation.recommendation?.spec?.claimRef?.namespace ||
              rr.recommendation.recommendation?.metadata?.namespace ||
              rr.recommendation.recommendation?.namespace ||
              '';
            const name = rr?.recommendation?.recommendation?.spec?.claimRef?.name || rr?.recommendation?.recommendation?.metadata?.name || '';
            const workloadName =
              rr.recommendation?.cloud_resourse?.meta?.controller ||
              rr.recommendation?.cloud_resourse?.meta?.config?.labels?.['app.kubernetes.io/name'];

            const referenceObj = {};
            if (containsLink(rr.type_reference_id)) {
              referenceObj['component'] = (
                <Link
                  onClick={(e) => e.stopPropagation()}
                  href={rr?.type_reference_id}
                  target='_blank'
                  style={{ fontSize: ds.text.body, fontWeight: ds.weight.regular }}
                >
                  {rr.type}
                </Link>
              );
            } else {
              referenceObj['component'] = <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.regular }}>{rr.type}</Typography>;
            }

            return [
              {
                component: (
                  <Box display='flex' flexDirection='column'>
                    {rr.recommendation.rule_name && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontWeight: ds.weight.regular, fontSize: ds.text.body }}>
                          {snakeToTitleCase(rr.recommendation.rule_name)}
                        </Typography>
                      </Box>
                    )}
                    {name && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.medium, color: ds.gray[500], mr: ds.space[1] }}>
                          name:
                        </Typography>
                        <Text
                          value={name}
                          showAutoEllipsis={true}
                          sx={{ maxWidth: '120px', fontSize: ds.text.caption, fontWeight: ds.weight.regular, color: ds.gray[500] }}
                        />
                      </Box>
                    )}
                    {namespace && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.medium, color: ds.gray[500], mr: ds.space[1] }}>
                          ns:
                        </Typography>
                        <Text
                          value={namespace}
                          showAutoEllipsis={true}
                          sx={{ maxWidth: '120px', fontSize: ds.text.caption, fontWeight: ds.weight.regular, color: ds.gray[500] }}
                        />
                      </Box>
                    )}
                    {workloadName && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.medium, color: ds.gray[500], mr: ds.space[1] }}>
                          workload:
                        </Typography>
                        <Text
                          value={workloadName}
                          showAutoEllipsis={true}
                          sx={{ maxWidth: '120px', fontSize: ds.text.caption, fontWeight: ds.weight.regular, color: ds.gray[500] }}
                        />
                      </Box>
                    )}
                  </Box>
                ),
                drilldownQuery: {
                  recommendation: rr,
                  message: rr.status_message,
                },
              },
              {
                component: <SeverityIcon level={toSeverityLevel(rr.recommendation.severity)} size={14} />,
                data: toSeverityLevel(rr.recommendation.severity),
              },
              {
                component: (() => {
                  const statusText = rr.status === 'InProgress' ? 'In Progress' : rr.status;
                  return (
                    <Label tone={statusToLabelTone(statusText)} size='sm'>
                      {statusText}
                    </Label>
                  );
                })(),
              },
              {
                component: (
                  <Currency
                    precison={1}
                    value={rr.recommendation.estimated_savings}
                    prefix={currencySymbol || '$'}
                    varient='savings'
                    sx={{
                      fontWeight: ds.weight.medium,
                      fontSize: ds.text.body,
                      color: ds.gray[700],
                    }}
                    sxPrefix={{
                      fontSize: ds.text.small,
                      fontWeight: ds.weight.regular,
                      color: ds.gray[500],
                    }}
                  />
                ),
              },
              {
                component: (() => {
                  const resolverName = rr.resolver_display_name || rr.data?.provider_config?.name;
                  return (
                    <Box display='flex' flexDirection='column'>
                      <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.regular, color: ds.gray[600] }}>
                        {rr.resolver_type ? rr.resolver_type : '-'}
                      </Typography>
                      {resolverName && (
                        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.regular, color: ds.gray[500] }}>{resolverName}</Typography>
                      )}
                    </Box>
                  );
                })(),
              },
              referenceObj,
              {
                component: <Datetime value={rr.updated_at} />,
              },
            ];
          });
          setData(builtCells);
        }
        setTotalCount(resolutionCount);
      })
      .finally(() => {
        setLoading(false);
      });
  };
  useEffect(() => {
    if (currencySymbol === undefined) return;
    getResolutionListingData();
  }, [accountId, selectedStatus, rowsPerPage, page, selectedRecommendation, selectedResolver, currencySymbol]);

  const statusOptions = [
    { label: 'Success', value: 'Success' },
    { label: 'Failed', value: 'Failed' },
    { label: 'In Progress', value: 'InProgress' },
  ];

  return (
    <ListingLayout id={`${recommendationResolutionTableId}-listing-layout`}>
      <ListingLayout.Toolbar
        data-testid='rr-filter-toolbar'
        actions={<DownloadButton id={`${recommendationResolutionTableId}-download`} onClick={() => ({ tableId: recommendationResolutionTableId })} />}
      >
        <FilterDropdown
          id='rr-filter-status'
          label='Status'
          options={statusOptions}
          value={statusOptions.find((o) => o.value === selectedStatus) ?? null}
          onSelect={(_e, item) => {
            setSelectedStatus(item?.value || '');
            setPage(0);
          }}
        />
        <FilterDropdown
          id='rr-filter-recommendation'
          label='Recommendation'
          options={(recommendationTypes || []).filter(Boolean).map((t) => ({ label: t, value: t }))}
          value={selectedRecommendation ? { label: selectedRecommendation, value: selectedRecommendation } : null}
          onSelect={(_e, item) => {
            setSelectedRecommendation(item?.value || '');
            setPage(0);
          }}
        />
        <FilterDropdown
          id='rr-filter-resolver'
          label='Resolver'
          options={(resolverTypes || []).filter(Boolean).map((t) => ({ label: t, value: t }))}
          value={selectedResolver ? { label: selectedResolver, value: selectedResolver } : null}
          onSelect={(_e, item) => {
            setSelectedResolver(item?.value || '');
            setPage(0);
          }}
        />
      </ListingLayout.Toolbar>

      <ListingLayout.Body>
        <CustomTable2
          id={recommendationResolutionTableId}
          headers={RESOLUTION_HEADERS}
          tableData={data}
          rowsPerPage={rowsPerPage}
          totalRows={totalCount}
          onPageChange={changePage}
          pageNumber={page + 1}
          loading={loading}
          showExpandable={true}
          expandable={{
            tabs: [
              {
                text: 'Details',
                componentFn: (_option, drilldownQuery) => {
                  const raw = drilldownQuery?.recommendation?.data?.data;
                  let parsed = raw;
                  if (typeof raw === 'string') {
                    try {
                      parsed = JSON.parse(raw);
                    } catch {
                      parsed = raw;
                    }
                  }

                  if (parsed && typeof parsed === 'object' && !Array.isArray(parsed) && Object.keys(parsed).length > 0) {
                    return (
                      <Box sx={{ padding: ds.space[4], backgroundColor: ds.background[100], borderRadius: ds.radius.sm }}>
                        <KeyValueList data={parsed} />
                      </Box>
                    );
                  }

                  const fallbackText = parsed ? (typeof parsed === 'string' ? parsed : JSON.stringify(parsed, null, 2)) : 'No Details Available';
                  return (
                    <Typography
                      sx={{
                        padding: ds.space[4],
                        backgroundColor: ds.background[100],
                        borderRadius: ds.radius.sm,
                        fontSize: ds.text.body,
                        color: ds.gray[700],
                        whiteSpace: 'pre-wrap',
                        wordBreak: 'break-word',
                      }}
                    >
                      {fallbackText}
                    </Typography>
                  );
                },
              },
              {
                text: 'Message',
                componentFn: (_option, drilldownQuery) => {
                  const messageData = drilldownQuery?.message || 'No Message Available';
                  return (
                    <Typography
                      sx={{
                        padding: ds.space[4],
                        backgroundColor: ds.background[100],
                        borderRadius: ds.radius.sm,
                        fontSize: ds.text.body,
                        color: ds.gray[700],
                        whiteSpace: 'pre-wrap',
                        wordBreak: 'break-word',
                      }}
                    >
                      {messageData}
                    </Typography>
                  );
                },
              },
            ],
          }}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

ListingRecommendationResolution.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default ListingRecommendationResolution;
