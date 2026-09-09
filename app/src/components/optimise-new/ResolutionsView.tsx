import { useCallback, useEffect, useMemo, useState } from 'react';
import apiRecommendations from '@api1/recommendation';
import apiHome from '@api1/home';
import apiUser from '@api1/user';
import Currency from '@shared/format/Currency';
import Datetime from '@shared/format/Datetime';
import Text from '@shared/format/Text';
import { Label, type LabelTone } from '@ui/Label';
import { Button as DsButton } from '@ui/Button';
import Tooltip from '@ui/Tooltip';
import RestartAltOutlinedIcon from '@mui/icons-material/RestartAltOutlined';
import { toast as snackbar } from '@ui/Toast';
import { Box, Typography } from '@mui/material';
import Link from 'next/link';
import { Link as DsLink } from '@ui/Link';
import SafeIcon from '@shared/icons/SafeIcon';
import { GithubIcon, GitLabIcon, BitBucketIcon } from '@assets';
import { useRouter } from 'next/router';
import { ds } from 'src/utils/colors';
import { containsLink, snakeToTitleCase, safeJSONParse } from 'src/utils/common';

import { ListingLayout } from '@ui/ListingLayout';
import CustomTable from '@shared/tables/CustomTable';
import FilterDropdown from '@ui/FilterDropdown';
import WidgetCard from '@ui/WidgetCard';
import { Stat } from '@ui/Stat';
import { cardTabSx, cardKeyDown } from './utils';
import { SeverityIcon, type SeverityLevel } from '@ui/SeverityIcon';
import DownloadButton from '@shared/buttons/DownloadButton';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import ResolutionDetailPanel from './ResolutionDetailPanel';
import { describeResolutionReference, formatResolutionType } from './resolutionDetail';

const renderAccountGroupIcon = (provider: string) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const SEVERITY_LEVELS = new Set(['critical', 'high', 'medium', 'low', 'info']);
const toSeverityLevel = (s: unknown): SeverityLevel => {
  const normalized = String(s || '').toLowerCase();
  return (SEVERITY_LEVELS.has(normalized) ? normalized : 'info') as SeverityLevel;
};

// Resolution lifecycle status → DS Label tone. Covers the three statuses the
// backend emits (InProgress / Success / Failed).
const statusToTone = (status: string): LabelTone => {
  switch (status) {
    case 'Success':
      return 'success';
    case 'Failed':
      return 'critical';
    case 'InProgress':
      return 'warning';
    default:
      return 'neutral';
  }
};

const RESOLUTION_HEADERS = [
  { name: 'Account', width: '14%' },
  { name: 'Recommendation', width: '22%' },
  { name: 'Severity', width: '8%' },
  { name: 'Status', width: '10%' },
  { name: 'Est. Savings', width: '10%' },
  { name: 'Resolver', width: '10%' },
  { name: 'Type', width: '10%' },
  { name: 'Updated At', width: '8%' },
  // Unnamed trailing actions column, matching the recommendations table. Row
  // actions belong here rather than inside a data cell.
  { name: '', width: '8%', align: 'right' as const },
];

// The stat cards above the listing, one per lifecycle status, and the ONLY status
// filter this tab has — a dropdown beside them would be a second control for one
// dimension, which is why the recommendations tab carries no Category dropdown
// next to its category cards either. Ordered the way a reader scans for trouble:
// what worked, what is still running, what did not.
// Only where the reference's host actually says which platform holds it. A
// ticket's reference is a bare id with a null provider_config, so it gets no
// mark rather than a guessed one.
const REFERENCE_PLATFORM_ICON: Record<string, any> = {
  github: GithubIcon,
  gitlab: GitLabIcon,
  bitbucket: BitBucketIcon,
};

// The recommendation's severity, carried onto the resolution — the same bands
// the listing's Severity column shows.
const SEVERITY_OPTIONS = ['Critical', 'High', 'Medium', 'Low', 'Info'].map((s) => ({ label: s, value: s }));

const STATUS_CARDS = [
  { status: 'Success', label: 'Success', tooltip: 'Resolutions that completed successfully.' },
  { status: 'InProgress', label: 'In Progress', tooltip: 'Resolutions that have been dispatched and have not finished yet.' },
  { status: 'Failed', label: 'Failed', tooltip: 'Resolutions that failed. Open one to see why, and retry it from its row.' },
] as const;

/**
 * Cross-account recommendation resolutions. Mirrors the per-account
 * `ListingRecommendationResolution` listing (mounted under each cloud-account /
 * kubernetes detail page) but spans every account in the tenant, adding an
 * Account column and an Account filter — the same way the Recommendations tab
 * aggregates recommendations across accounts.
 */
const ResolutionsView = () => {
  const resolutionTableId = 'optimise-resolutions';

  const router = useRouter();
  const deepLinkRecId = typeof router.query.id === 'string' ? router.query.id : '';

  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [rawRows, setRawRows] = useState<any[]>([]);
  const [totalCount, setTotalCount] = useState(0);

  // The resolution whose panel is open. A resolution is one attempt at one
  // recommendation — an individual record, so it opens a drawer rather than
  // expanding in place. See docs/architecture-decisions.md.
  const [panelResolution, setPanelResolution] = useState<any>(null);

  // Resolution counts per status, for the stat cards above the listing.
  const [statusCounts, setStatusCounts] = useState<Record<string, number>>({});
  const [cardsLoading, setCardsLoading] = useState(false);

  // Accounts map (id → { name, cloud_provider }) for the Account column + filter.
  const [accounts, setAccounts] = useState<Record<string, { name: string; cloud_provider: string }>>({});

  // Filters
  const [selectedAccounts, setSelectedAccounts] = useState<string[]>([]);
  const [selectedStatus, setSelectedStatus] = useState('');
  const [selectedType, setSelectedType] = useState('');
  const [selectedResolver, setSelectedResolver] = useState('');
  const [selectedSeverity, setSelectedSeverity] = useState<string[]>([]);
  const [recommendationTypes, setRecommendationTypes] = useState<string[]>([]);
  const [resolverTypes, setResolverTypes] = useState<string[]>([]);

  // Retrying a Failed attempt re-dispatches it server-side; refreshKey refetches
  // the listing so the row reflects its new InProgress state.
  const [retryingId, setRetryingId] = useState('');
  const [refreshKey, setRefreshKey] = useState(0);

  const handleRetry = useCallback(async (resolutionId: string, accountId: string) => {
    setRetryingId(resolutionId);
    try {
      const res = await apiRecommendations.retryRecommendationResolution(accountId, resolutionId);
      if (res?.errors?.length) {
        snackbar.error(res.errors[0]?.message || 'Failed to retry resolution');
      } else {
        snackbar.success('Retry started');
        setRefreshKey((k) => k + 1);
      }
    } catch {
      snackbar.error('Failed to retry resolution');
    } finally {
      setRetryingId('');
    }
  }, []);

  // Load accounts + distinct filter values once.
  useEffect(() => {
    apiHome.getCloudAccounts().then((res: any) => {
      // getCloudAccounts resolves to an Error object on failure (not an array), so guard before mapping.
      const accountsList = Array.isArray(res) ? res : [];
      setAccounts(Object.fromEntries(accountsList.map((v: any) => [v.id, { name: v.account_name, cloud_provider: v.cloud_provider || '' }])));
    });
    apiRecommendations.getDistinctResolverTypes('type').then((res: any) => {
      setRecommendationTypes((res?.data?.data?.recommendation_resolution || []).map((item: any) => item.type).filter(Boolean));
    });
    apiRecommendations.getDistinctResolverTypes('resolver_type').then((res: any) => {
      setResolverTypes((res?.data?.data?.recommendation_resolution || []).map((item: any) => item.resolver_type).filter(Boolean));
    });
  }, []);

  const accountFilterOptions = useMemo(
    () =>
      Object.entries(accounts).map(([id, info]) => {
        const name = info.name || id;
        return { label: name, value: id, group: info.cloud_provider || 'Other' };
      }),
    [accounts]
  );

  const changePage = (nextPage: number, limit: number) => {
    setPage(nextPage - 1);
    setRowsPerPage(limit);
  };

  // Reset to the first page whenever the deep-link scope toggles, so results aren't hidden on
  // a stale page offset.
  useEffect(() => {
    setPage(0);
  }, [deepLinkRecId]);

  // Deliberately not keyed on selectedStatus, page or rowsPerPage: the cards show
  // the split across every status, so narrowing to the selected one would collapse
  // them into a restatement of the row count — and paging is not a change of scope.
  useEffect(() => {
    if (!router.isReady) return;
    let active = true;
    setCardsLoading(true);
    apiRecommendations
      .getRecommendationResolutionStatusCounts({
        accountId: selectedAccounts,
        type: selectedType,
        resolverType: selectedResolver,
        recommendationId: deepLinkRecId,
        severity: selectedSeverity,
      })
      .then((counts) => {
        if (active) setStatusCounts(counts);
      })
      .catch(() => {
        if (active) setStatusCounts({});
      })
      .finally(() => {
        if (active) setCardsLoading(false);
      });
    return () => {
      active = false;
    };
  }, [selectedAccounts, selectedType, selectedResolver, selectedSeverity, deepLinkRecId, router.isReady, refreshKey]);

  useEffect(() => {
    // Wait for router.query hydration so a deep-linked ?id= scopes the first fetch
    // instead of firing an unfiltered request that a filtered one then supersedes.
    if (!router.isReady) return;
    // Guard against out-of-order responses when filters/page change rapidly —
    // ignore any in-flight request whose effect has been superseded.
    let active = true;
    setRawRows([]);
    setLoading(true);
    apiRecommendations
      .getRecommendationResolution({
        limit: rowsPerPage,
        offset: rowsPerPage * page,
        accountId: selectedAccounts,
        status: selectedStatus,
        type: selectedType,
        resolverType: selectedResolver,
        recommendationId: deepLinkRecId,
        severity: selectedSeverity,
      })
      .then((res: any) => {
        if (!active) return;
        setRawRows(res?.data?.data?.recommendation_resolution || []);
        setTotalCount(res?.data?.data?.recommendation_resolution_aggregate?.aggregate?.count || 0);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [
    selectedAccounts,
    selectedStatus,
    selectedType,
    selectedResolver,
    selectedSeverity,
    rowsPerPage,
    page,
    deepLinkRecId,
    router.isReady,
    refreshKey,
  ]);

  // Build table cells from the fetched rows + accounts map. Kept separate from
  // the fetch so Account labels resolve once accounts load without refetching.
  const tableData = useMemo(
    () =>
      rawRows.map((rr: any) => {
        const accountInfo = accounts[rr.account_id];
        const accountProvider = (accountInfo?.cloud_provider || '').toUpperCase();

        // rec_recommendation is typed as String in the backend metadata though it's a jsonb object,
        // so it may arrive as a JSON string — parse before reaching into .spec / .metadata.
        const rec =
          typeof rr.recommendation.recommendation === 'string' ? safeJSONParse(rr.recommendation.recommendation) : rr.recommendation.recommendation;

        const namespace =
          rr.recommendation.cloud_resourse?.meta?.namespace ||
          rr.recommendation.cloud_resourse?.meta?.config?.namespace ||
          rec?.spec?.claimRef?.namespace ||
          rec?.metadata?.namespace ||
          rec?.namespace ||
          '';
        const name = rec?.spec?.claimRef?.name || rec?.metadata?.name || '';
        const workloadName =
          rr.recommendation?.cloud_resourse?.meta?.controller || rr.recommendation?.cloud_resourse?.meta?.config?.labels?.['app.kubernetes.io/name'];

        // The type, plus what it produced when that is something a reader can use.
        const reference = describeResolutionReference(rr.type_reference_id, rr.status, rr.data?.ticket_key);
        // Two lines, like Account and Resolver beside it: what kind of thing on
        // top, which one underneath. The identifier carries the link, because it
        // is the part that names a specific artefact.
        const referenceObj: any = {
          data: `${formatResolutionType(rr.type)} ${reference.detail || ''}`.trim(),
          component: (
            <Box display='flex' flexDirection='column'>
              <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.regular, color: ds.gray[700] }}>
                {formatResolutionType(rr.type) || '—'}
              </Typography>
              {reference.href && reference.detail && (
                // The mark sits with the identifier, not the type: #950 is the
                // thing that lives on GitHub — "Pull Request" is only its kind.
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
                  {REFERENCE_PLATFORM_ICON[reference.platform ?? ''] && (
                    <SafeIcon src={REFERENCE_PLATFORM_ICON[reference.platform ?? '']} alt='' width={13} height={13} />
                  )}
                  <DsLink href={reference.href} openInNew secondaryText maxWidth='92px'>
                    {reference.detail}
                  </DsLink>
                </Box>
              )}
              {!reference.href && reference.detail && (
                <Text value={reference.detail} secondaryText showAutoEllipsis sx={{ fontSize: ds.text.small }} />
              )}
              {reference.missing && <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>none created</Typography>}
            </Box>
          ),
        };

        const statusText = rr.status === 'InProgress' ? 'In Progress' : rr.status;
        const resolverName = rr.resolver_display_name || rr.data?.provider_config?.name;

        return [
          {
            component: (
              <Box display='flex' flexDirection='column'>
                <Text
                  value={accountInfo?.name || rr.account_id || '-'}
                  showAutoEllipsis={true}
                  sx={{ maxWidth: '140px', fontSize: ds.text.body, fontWeight: ds.weight.regular, color: ds.gray[700] }}
                />
                {accountProvider && (
                  <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.regular, color: ds.gray[500] }}>{accountProvider}</Typography>
                )}
              </Box>
            ),
            data: accountInfo?.name || rr.account_id || '',
          },
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
            drilldownQuery: { resolution: rr },
          },
          {
            component: <SeverityIcon level={toSeverityLevel(rr.recommendation.severity)} size={14} />,
            data: toSeverityLevel(rr.recommendation.severity),
          },
          {
            component: (
              <Box display='flex' flexDirection='column' gap={ds.space[1]}>
                <Label tone={statusToTone(rr.status)} size='sm'>
                  {statusText}
                </Label>
                {/* Surface the reason inline: a failure (or a success that produced no
                    link) is otherwise only readable after expanding the row. */}
                {rr.status_message && (rr.status === 'Failed' || (rr.status === 'Success' && !containsLink(rr.type_reference_id))) && (
                  <Text value={rr.status_message} secondaryText showAutoEllipsis sx={{ fontSize: ds.text.small }} />
                )}
              </Box>
            ),
          },
          {
            component: (
              <Currency
                precison={1}
                value={rr.recommendation.estimated_savings}
                prefix='$'
                varient='savings'
                sx={{ fontWeight: ds.weight.medium, fontSize: ds.text.body, color: ds.gray[700] }}
                sxPrefix={{ fontSize: ds.text.small, fontWeight: ds.weight.regular, color: ds.gray[500] }}
              />
            ),
          },
          {
            component: (
              <Box display='flex' flexDirection='column'>
                <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.regular, color: ds.gray[600] }}>
                  {rr.resolver_type ? rr.resolver_type : '-'}
                </Typography>
                {resolverName && (
                  <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.regular, color: ds.gray[500] }}>{resolverName}</Typography>
                )}
              </Box>
            ),
          },
          referenceObj,
          {
            component: <Datetime value={rr.updated_at} />,
          },
          {
            component: (
              // stopPropagation on the cell, not the button: the row opens the
              // panel, and the whole action area has to be exempt from that.
              <Box
                onClick={(e: React.MouseEvent) => e.stopPropagation()}
                // Held off the table's right edge rather than flush against it.
                sx={{ display: 'inline-flex', justifyContent: 'flex-end', width: '100%', pr: ds.space[4] }}
              >
                {rr.status === 'Failed' && (
                  <Tooltip title={retryingId === rr.id ? 'Retrying…' : 'Retry'} placement='top'>
                    <span>
                      <DsButton
                        tone='ghost'
                        // `lg` is the largest icon-only size the DS offers (18px glyph
                        // in a 40px target). ds/Button owns its icon sizing and takes
                        // no sx, so this is the ceiling without overriding the system.
                        size='lg'
                        composition='icon-only'
                        icon={<RestartAltOutlinedIcon />}
                        aria-label='Retry'
                        id={`resolution-retry-${rr.id}`}
                        disabled={!!retryingId}
                        onClick={() => handleRetry(rr.id, rr.account_id)}
                      />
                    </span>
                  </Tooltip>
                )}
              </Box>
            ),
          },
        ];
      }),
    [rawRows, accounts, retryingId, handleRetry]
  );

  // Summed across every status the backend reports, not just the three carded
  // ones — an unrecognised status must still be counted somewhere, and the
  // listing's totalCount cannot serve here because it honours the status filter.
  const allResolutionsCount = useMemo(() => Object.values(statusCounts).reduce((sum, n) => sum + n, 0), [statusCounts]);

  const handleStatusCardClick = useCallback((status: string) => {
    setSelectedStatus((current) => (current === status ? '' : status));
    setPage(0);
  }, []);

  const handleAllCardClick = useCallback(() => {
    setSelectedStatus('');
    setPage(0);
  }, []);

  return (
    <Box sx={{ p: '0px' }} data-testid='optimize-resolutions-page'>
      {/* Stat cards sit ABOVE the listing shell, per the ListingLayout contract. */}
      <Box sx={{ display: 'flex', gap: ds.space[3], mt: ds.space[4] }}>
        <WidgetCard
          role='button'
          tabIndex={0}
          aria-pressed={selectedStatus === ''}
          data-testid='resolutions-card-all'
          onClick={handleAllCardClick}
          onKeyDown={cardKeyDown(handleAllCardClick)}
          sx={cardTabSx(selectedStatus === '', false)}
        >
          <Stat
            size='md'
            label='All Resolutions'
            info={{ tooltip: 'Every resolution attempted on a recommendation, across all statuses. Click to clear the status filter.' }}
            value={cardsLoading ? '…' : allResolutionsCount.toLocaleString()}
          />
        </WidgetCard>

        {STATUS_CARDS.map(({ status, label, tooltip }) => {
          const count = statusCounts[status] || 0;
          const pressed = selectedStatus === status;
          // A zero card is muted and inert rather than tooltipped: the count on its
          // face already says why, and WidgetCard is not a forwardRef, so a MUI
          // Tooltip wrapped round it cannot hold the ref it needs to position.
          const muted = count === 0 && !pressed && !cardsLoading;
          return (
            <WidgetCard
              key={status}
              role='button'
              tabIndex={muted ? -1 : 0}
              aria-pressed={pressed}
              aria-disabled={muted || undefined}
              data-testid={`resolutions-card-${status.toLowerCase()}`}
              onClick={muted ? undefined : () => handleStatusCardClick(status)}
              onKeyDown={muted ? undefined : cardKeyDown(() => handleStatusCardClick(status))}
              sx={cardTabSx(pressed, muted)}
            >
              <Stat
                size='md'
                label={label}
                // A muted card is inert, so it does not get told it can be clicked.
                info={{ tooltip: muted ? tooltip : `${tooltip} Click to filter the list; click again to unselect.` }}
                value={cardsLoading ? '…' : count.toLocaleString()}
              />
            </WidgetCard>
          );
        })}
      </Box>

      <ListingLayout id={`${resolutionTableId}-listing-layout`} sx={{ mt: ds.space[4] }}>
        <ListingLayout.Toolbar
          data-testid='resolutions-filter-toolbar'
          actions={<DownloadButton id={`${resolutionTableId}-download`} onClick={() => ({ tableId: resolutionTableId })} />}
        >
          <FilterDropdown
            id='resolutions-filter-account'
            label='Account'
            multiple
            grouped
            groupIcon={renderAccountGroupIcon}
            options={accountFilterOptions}
            value={accountFilterOptions.filter((o) => selectedAccounts.includes(o.value))}
            onSelect={(_e: any, items: any) => {
              setSelectedAccounts((Array.isArray(items) ? items : []).map((it: any) => it.value));
              setPage(0);
            }}
          />
          <FilterDropdown
            id='resolutions-filter-severity'
            label='Severity'
            multiple
            options={SEVERITY_OPTIONS}
            value={SEVERITY_OPTIONS.filter((o) => selectedSeverity.includes(o.value))}
            onSelect={(_e: any, items: any) => {
              setSelectedSeverity((Array.isArray(items) ? items : []).map((it: any) => it.value));
              setPage(0);
            }}
          />
          <FilterDropdown
            id='resolutions-filter-recommendation'
            label='Type'
            // Labelled the way the Type column labels the same values — the filter
            // that feeds a column should not spell its options differently.
            options={(recommendationTypes || []).filter(Boolean).map((t) => ({ label: formatResolutionType(t), value: t }))}
            value={selectedType ? { label: formatResolutionType(selectedType), value: selectedType } : null}
            onSelect={(_e: any, item: any) => {
              setSelectedType(item?.value || '');
              setPage(0);
            }}
          />
          <FilterDropdown
            id='resolutions-filter-resolver'
            label='Resolver'
            options={(resolverTypes || []).filter(Boolean).map((t) => ({ label: t, value: t }))}
            value={selectedResolver ? { label: selectedResolver, value: selectedResolver } : null}
            onSelect={(_e: any, item: any) => {
              setSelectedResolver(item?.value || '');
              setPage(0);
            }}
          />
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          {deepLinkRecId && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: ds.space[2],
                mb: ds.space[3],
                px: ds.space[3],
                py: ds.space[2],
                backgroundColor: ds.background[100],
                borderRadius: ds.radius.sm,
              }}
            >
              <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700] }}>
                Showing resolutions for the recommendation from your notification.
              </Typography>
              <Link href='/optimise#resolutions' style={{ fontSize: ds.text.body, fontWeight: ds.weight.medium }}>
                View all resolutions
              </Link>
            </Box>
          )}
          <CustomTable
            id={resolutionTableId}
            headers={RESOLUTION_HEADERS}
            tableData={tableData}
            rowsPerPage={rowsPerPage}
            totalRows={totalCount}
            onPageChange={changePage}
            pageNumber={page + 1}
            loading={loading}
            onRowClick={(query: any) => query?.resolution && setPanelResolution(query.resolution)}
          />
        </ListingLayout.Body>
      </ListingLayout>

      <ResolutionDetailPanel
        open={Boolean(panelResolution)}
        onClose={() => setPanelResolution(null)}
        resolution={panelResolution}
        accounts={accounts}
        onRetry={handleRetry}
        retrying={Boolean(retryingId)}
      />
    </Box>
  );
};

export default ResolutionsView;
