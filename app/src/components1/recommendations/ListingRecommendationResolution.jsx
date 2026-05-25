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
import { toast as snackbar } from '@components1/ds/Toast';

import { ListingLayout } from '@components1/ds/ListingLayout';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { SeverityIcon } from '@components1/ds/SeverityIcon';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';

const SEVERITY_LEVELS = new Set(['critical', 'high', 'medium', 'low', 'info']);
const toSeverityLevel = (s) => {
  const normalized = String(s || '').toLowerCase();
  return SEVERITY_LEVELS.has(normalized) ? normalized : 'info';
};

const RESOLUTION_HEADERS = ['Recommendation', 'Severity', 'Status', 'Est. Savings', 'Resolver', 'Type', 'Updated At'];

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

  const handleExportDownload = async (format) => {
    try {
      const exportFormat = format === 'xlsx' ? 'xlsx' : 'csv';
      const response = await apiRecommendations.exportRecommendations({
        accountId: accountId,
        status: selectedStatus ? [selectedStatus] : undefined,
        type: selectedRecommendation || undefined,
        resolverType: selectedResolver || undefined,
        format: exportFormat,
      });

      if (response?.data?.data?.recommendation_export) {
        const { file_data, filename, content_type } = response.data.data.recommendation_export;
        const byteCharacters = atob(file_data);
        const byteNumbers = new Array(byteCharacters.length);
        for (let i = 0; i < byteCharacters.length; i++) {
          byteNumbers[i] = byteCharacters.charCodeAt(i);
        }
        const byteArray = new Uint8Array(byteNumbers);
        const blob = new Blob([byteArray], { type: content_type });
        const url = window.URL.createObjectURL(blob);
        const link = document.createElement('a');
        link.href = url;
        link.download = filename;
        document.body.appendChild(link);
        link.click();
        document.body.removeChild(link);
        window.URL.revokeObjectURL(url);
        snackbar.success('Export downloaded successfully');
      } else {
        snackbar.error('Export failed: No data received');
      }
    } catch (error) {
      console.error('Export error:', error);
      snackbar.error(`Export failed: ${error.message}`);
    }
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
              referenceObj['component'] = (
                <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.regular, color: ds.gray[500] }}>{rr.type}</Typography>
              );
            }

            return [
              {
                component: (
                  <Box display='flex' flexDirection='column'>
                    {rr.recommendation.rule_name && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.medium, color: ds.gray[700], mr: ds.space[1] }}>
                          Rec:{' '}
                        </Typography>
                        <Typography sx={{ fontWeight: ds.weight.regular, fontSize: ds.text.caption }}>
                          {snakeToTitleCase(rr.recommendation.rule_name)}
                        </Typography>
                      </Box>
                    )}
                    {name && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[700], mr: ds.space[1] }}>
                          Name:
                        </Typography>
                        <Text
                          value={name}
                          showAutoEllipsis={true}
                          sx={{ maxWidth: '120px', fontSize: ds.text.body, fontWeight: ds.weight.regular }}
                        />
                      </Box>
                    )}
                    {namespace && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.medium, color: ds.gray[700], mr: ds.space[1] }}>
                          Namespace:
                        </Typography>
                        <Text
                          value={namespace}
                          showAutoEllipsis={true}
                          sx={{ maxWidth: '120px', fontSize: ds.text.caption, fontWeight: ds.weight.regular }}
                        />
                      </Box>
                    )}
                    {workloadName && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.medium, color: ds.gray[700], mr: ds.space[1] }}>
                          Workload:
                        </Typography>
                        <Text
                          value={workloadName}
                          showAutoEllipsis={true}
                          sx={{ maxWidth: '120px', fontSize: ds.text.caption, fontWeight: ds.weight.regular }}
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
                      fontSize: ds.text.caption,
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
                      <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.regular, color: ds.gray[500] }}>
                        {rr.resolver_type ? rr.resolver_type : '-'}
                      </Typography>
                      {resolverName && (
                        <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.regular, color: ds.gray[500] }}>{resolverName}</Typography>
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
    <ListingLayout id={recommendationResolutionTableId}>
      <ListingLayout.Toolbar
        data-testid='rr-filter-toolbar'
        actions={
          <DsDropdownMenu
            align='end'
            size='sm'
            items={[
              { id: 'export-csv', label: 'Download CSV', onSelect: () => handleExportDownload('csv') },
              { id: 'export-xlsx', label: 'Download Excel (XLSX)', onSelect: () => handleExportDownload('xlsx') },
            ]}
            trigger={
              <DsButton
                tone='secondary'
                size='sm'
                composition='icon-only'
                icon={<FileDownloadOutlinedIcon />}
                aria-label='Download'
                id='rr-download'
              />
            }
          />
        }
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
                  let detailsData = drilldownQuery?.recommendation?.data?.data;
                  if (detailsData && typeof detailsData === 'string') {
                    try {
                      detailsData = JSON.parse(detailsData);
                      detailsData = JSON.stringify(detailsData, null, 2);
                    } catch (e) {
                      console.error(e);
                    }
                  } else {
                    detailsData = JSON.stringify(detailsData, null, 2);
                  }
                  if (!detailsData) detailsData = 'No Details Available';
                  return (
                    <pre
                      style={{
                        backgroundColor: ds.background[100],
                        padding: ds.space[4],
                        borderRadius: ds.radius.sm,
                        whiteSpace: 'pre-wrap',
                        wordWrap: 'break-word',
                        overflowWrap: 'break-word',
                        maxWidth: '100%',
                        overflow: 'auto',
                      }}
                    >
                      {detailsData}
                    </pre>
                  );
                },
              },
              {
                text: 'Message',
                componentFn: (_option, drilldownQuery) => {
                  const messageData = drilldownQuery?.message || 'No Message Available';
                  return (
                    <pre
                      style={{
                        backgroundColor: ds.background[100],
                        padding: ds.space[4],
                        borderRadius: ds.radius.sm,
                        whiteSpace: 'pre-wrap',
                        wordWrap: 'break-word',
                        overflowWrap: 'break-word',
                        maxWidth: '100%',
                        overflow: 'auto',
                      }}
                    >
                      {messageData}
                    </pre>
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
