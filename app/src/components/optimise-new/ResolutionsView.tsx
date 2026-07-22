import { useEffect, useMemo, useState } from 'react';
import apiRecommendations from '@api1/recommendation';
import apiHome from '@api1/home';
import apiUser from '@api1/user';
import Currency from '@shared/format/Currency';
import Datetime from '@shared/format/Datetime';
import Text from '@shared/format/Text';
import { Label, type LabelTone } from '@ui/Label';
import { Box, Typography } from '@mui/material';
import Link from 'next/link';
import { ds } from 'src/utils/colors';
import { containsLink, snakeToTitleCase, safeJSONParse } from 'src/utils/common';

import { ListingLayout } from '@ui/ListingLayout';
import CustomTable from '@shared/tables/CustomTable';
import FilterDropdown from '@ui/FilterDropdown';
import { SeverityIcon, type SeverityLevel } from '@ui/SeverityIcon';
import DownloadButton from '@shared/buttons/DownloadButton';
import CommandExecutionHistory from '@components/cloudaccount/CommandExecutionHistory';

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

// Guard against unexpectedly deep payloads — beyond this nesting level we dump
// the remaining subtree as raw JSON rather than risk unbounded recursion.
const KEY_VALUE_MAX_DEPTH = 10;

/**
 * Render an object as a structured key/value tree. Top-level keys (entity
 * identifiers such as container / workload names) are rendered verbatim;
 * nested field names are snake_case → Title Cased. Nested objects recurse into
 * an indented sub-list (rather than dumping raw JSON), so resource specs like
 * `{ cpu: { request }, memory: { limit, request } }` read as a labelled tree.
 * Arrays and remaining scalars render inline. Recursion is capped at
 * KEY_VALUE_MAX_DEPTH (deeper subtrees fall back to a raw JSON dump). Returns
 * null for non-object / empty input so the caller can decide the empty state.
 */
const KeyValueList = ({ data, depth = 0 }: { data: unknown; depth?: number }) => {
  if (data === null || typeof data !== 'object' || Array.isArray(data)) {
    return null;
  }
  if (depth >= KEY_VALUE_MAX_DEPTH) {
    return <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700], wordBreak: 'break-word' }}>{JSON.stringify(data)}</Typography>;
  }
  const entries = Object.entries(data);
  if (entries.length === 0) {
    return null;
  }

  const isNestedObject = (value: unknown): value is Record<string, unknown> =>
    value !== null && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).length > 0;

  const formatScalar = (value: unknown) => {
    if (value === null || value === undefined || value === '') return '—';
    if (typeof value === 'boolean') return value ? 'Yes' : 'No';
    if (typeof value === 'number') return Number.isFinite(value) ? String(value) : '—';
    if (Array.isArray(value)) return value.map((v) => (v !== null && typeof v === 'object' ? JSON.stringify(v) : String(v))).join(', ');
    if (typeof value === 'object') return JSON.stringify(value);
    return String(value);
  };

  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        rowGap: ds.space[3],
        ...(depth > 0 && { pl: ds.space[4], borderLeft: `1px solid ${ds.gray[200]}` }),
      }}
    >
      {entries.map(([key, value]) => {
        // Top-level keys are entity identifiers (e.g. container / workload names) —
        // render them verbatim. Only the structured field names nested below
        // (cpu, memory, request, limit, …) get snake_case → Title Case.
        const label = depth === 0 ? key : snakeToTitleCase(key);
        return isNestedObject(value) ? (
          <Box key={key} sx={{ display: 'flex', flexDirection: 'column', rowGap: ds.space[2] }}>
            <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[600] }}>{label}</Typography>
            <KeyValueList data={value} depth={depth + 1} />
          </Box>
        ) : (
          <Box
            key={key}
            sx={{ display: 'grid', gridTemplateColumns: 'minmax(140px, max-content) 1fr', columnGap: ds.space[4], alignItems: 'baseline' }}
          >
            <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[600] }}>{label}</Typography>
            <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700], wordBreak: 'break-word' }}>{formatScalar(value)}</Typography>
          </Box>
        );
      })}
    </Box>
  );
};

const RESOLUTION_HEADERS = [
  { name: 'Account', width: '14%' },
  { name: 'Recommendation', width: '26%' },
  { name: 'Severity', width: '8%' },
  { name: 'Status', width: '10%' },
  { name: 'Est. Savings', width: '10%' },
  { name: 'Resolver', width: '12%' },
  { name: 'Type', width: '10%' },
  { name: 'Updated At', width: '10%' },
];

const STATUS_OPTIONS = [
  { label: 'All', value: '' },
  { label: 'In Progress', value: 'InProgress' },
  { label: 'Success', value: 'Success' },
  { label: 'Failed', value: 'Failed' },
];

/**
 * Cross-account recommendation resolutions. Mirrors the per-account
 * `ListingRecommendationResolution` listing (mounted under each cloud-account /
 * kubernetes detail page) but spans every account in the tenant, adding an
 * Account column and an Account filter — the same way the Recommendations tab
 * aggregates recommendations across accounts.
 */
const ResolutionsView = () => {
  const resolutionTableId = 'optimise-resolutions';

  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [rawRows, setRawRows] = useState<any[]>([]);
  const [totalCount, setTotalCount] = useState(0);

  // Accounts map (id → { name, cloud_provider }) for the Account column + filter.
  const [accounts, setAccounts] = useState<Record<string, { name: string; cloud_provider: string }>>({});

  // Filters
  const [selectedAccounts, setSelectedAccounts] = useState<string[]>([]);
  const [selectedStatus, setSelectedStatus] = useState('');
  const [selectedType, setSelectedType] = useState('');
  const [selectedResolver, setSelectedResolver] = useState('');
  const [recommendationTypes, setRecommendationTypes] = useState<string[]>([]);
  const [resolverTypes, setResolverTypes] = useState<string[]>([]);

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
        const provider = (info.cloud_provider || '').toUpperCase();
        const name = info.name || id;
        return { label: provider ? `${provider} · ${name}` : name, value: id };
      }),
    [accounts]
  );

  const changePage = (nextPage: number, limit: number) => {
    setPage(nextPage - 1);
    setRowsPerPage(limit);
  };

  useEffect(() => {
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
  }, [selectedAccounts, selectedStatus, selectedType, selectedResolver, rowsPerPage, page]);

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

        const referenceObj: any = {};
        if (containsLink(rr.type_reference_id)) {
          referenceObj.component = (
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
          referenceObj.component = <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.regular }}>{rr.type}</Typography>;
        }

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
            drilldownQuery: {
              recommendation: rr,
              message: rr.status_message,
              recommendationId: rr.recommendation_id,
              typeReferenceId: rr.type_reference_id,
              resolutionId: rr.id,
              accountId: rr.account_id,
            },
          },
          {
            component: <SeverityIcon level={toSeverityLevel(rr.recommendation.severity)} size={14} />,
            data: toSeverityLevel(rr.recommendation.severity),
          },
          {
            component: (
              <Label tone={statusToTone(rr.status)} size='sm'>
                {statusText}
              </Label>
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
        ];
      }),
    [rawRows, accounts]
  );

  return (
    <ListingLayout id={`${resolutionTableId}-listing-layout`}>
      <ListingLayout.Toolbar
        data-testid='resolutions-filter-toolbar'
        actions={<DownloadButton id={`${resolutionTableId}-download`} onClick={() => ({ tableId: resolutionTableId })} />}
      >
        <FilterDropdown
          id='resolutions-filter-account'
          label='Account'
          multiple
          options={accountFilterOptions}
          value={accountFilterOptions.filter((o) => selectedAccounts.includes(o.value))}
          onSelect={(_e: any, items: any) => {
            setSelectedAccounts((Array.isArray(items) ? items : []).map((it: any) => it.value));
            setPage(0);
          }}
        />
        <FilterDropdown
          id='resolutions-filter-status'
          label='Status'
          options={STATUS_OPTIONS}
          value={STATUS_OPTIONS.find((o) => o.value === selectedStatus) ?? null}
          onSelect={(_e: any, item: any) => {
            setSelectedStatus(item?.value || '');
            setPage(0);
          }}
        />
        <FilterDropdown
          id='resolutions-filter-recommendation'
          label='Recommendation'
          options={(recommendationTypes || []).filter(Boolean).map((t) => ({ label: t, value: t }))}
          value={selectedType ? { label: selectedType, value: selectedType } : null}
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
        <CustomTable
          id={resolutionTableId}
          headers={RESOLUTION_HEADERS}
          tableData={tableData}
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
                componentFn: (_option: any, drilldownQuery: any) => {
                  if (drilldownQuery?.typeReferenceId === 'cli_execution' && drilldownQuery?.recommendationId) {
                    return (
                      <CommandExecutionHistory
                        accountId={drilldownQuery.accountId}
                        recommendationId={drilldownQuery.recommendationId}
                        resolutionId={drilldownQuery.resolutionId}
                      />
                    );
                  }

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
                componentFn: (_option: any, drilldownQuery: any) => {
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

export default ResolutionsView;
