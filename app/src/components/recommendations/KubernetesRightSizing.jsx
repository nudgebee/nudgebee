import { Box } from '@mui/material';
import React, { useEffect, useState, useMemo } from 'react';
import { formatMemory } from '@lib/formatter';
import recommendationApi, { RECOMMENDATION_STATUS } from '@api1/recommendation';
import k8sApi from '@api1/kubernetes';
import Currency from '@shared/format/Currency';
import KubernetesRightSizingUpdateForm from '@components/recommendations/KubernetesRightSizingUpdateForm';
import ResolveModal from '@components/optimise-new/ResolveModal';
import KubernetesUtilization from '@components/k8s/KubernetesUtilization';
import Datetime from '@shared/format/Datetime';
import PropTypes from 'prop-types';
import { hasWriteAccess } from '@lib/auth';
import RecommendationResolution from '@components/k8s/common/RecommendationResolution';
import { useData } from '@context/DataContext';
import Heading from '@components/common/Heading';
import Text from '@shared/format/Text';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiUser from '@api1/user';
import CustomTicketLink from '@shared/links/TicketLink';
import PRLink from '@shared/links/PRLink';
import { ds } from 'src/utils/colors';
import apiHome from '@api1/home';
import useRecommendationExport from '@hooks/useRecommendationExport';
import Link from '@ui/Link';
import SafeIcon from '@shared/icons/SafeIcon';
import NubiChatSidebar from '@shared/layout/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import Tooltip from '@ui/Tooltip';
import { syncFilterFromQuery } from '@utils/common';

// DS V2 primitives — phased in alongside the legacy components. Visual swap only;
// API calls, handlers, and modal forms (AutoOptimizeForm and friends) untouched.
import WidgetCard from '@ui/WidgetCard';
import { ListingLayout } from '@ui/ListingLayout';
import { Stat } from '@ui/Stat';
import { CostCallout } from '@ui/CostCallout';
import CustomTable from '@shared/tables/CustomTable';
import { Button as DsButton } from '@ui/Button';
import { DropdownMenu as DsDropdownMenu } from '@ui/DropdownMenu';
import { ScanRefreshButton } from './ScanRefreshButton';
import FilterDropdown from '@ui/FilterDropdown';
import { Comparison as DsComparison, ComparisonGroup as DsComparisonGroup } from '@ui/Comparison';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';

const RIGHT_SIZING_HEADER = [
  'Name',
  { name: 'Namespace', width: '12%' },
  { name: 'Kind', width: '10%' },
  { name: 'CPU Req/Recommended', secondryText: '(Core)' },
  { name: 'Memory Req/Recommended', secondryText: '(MB)' },
  'Updated at',
  { name: 'Savings' },
  '',
];

const KubernetesRightSizing = ({ enabledSummary = true, enabledFilters = true, isOptimisePage = false, resourceIds, groupName, ...props }) => {
  const router = useRouter();
  const { assistantName } = useTenantBranding();
  const kubernetesRightSizingTable = 'kubernetesRightSizingTable';
  const { selectedCluster, allCluster } = useData();

  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [kubernetesRightSizing, setKubernetesRightSizing] = useState([]);
  const [kubernetesRightSizingCount, setKubernetesRightSizingCount] = useState(0);
  const [kubernetesRightSizingEstimatedSaving, setKubernetesRightSizingEstimatedSaving] = useState(0);
  const [openKubernetesRightSizingUpdateForm, setOpenKubernetesRightSizingUpdateForm] = useState(false);
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [selectedNamespace, setSelectedNamespace] = useState(router.query.namespace || '');
  const [workloadTypeFilter, setWorkloadTypeFilter] = useState([]);
  const [selectedWorkloadType, setSelectedWorkloadType] = useState('');
  const [workloadNameFilter, setWorkloadNameFilter] = useState([]);
  const [selectedWorkloadName, setSelectedWorkloadName] = useState('');
  const [recommendationStatus, setRecommendationStatus] = useState([{ label: 'Open', value: 'Open' }]);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);
  const [resolveModalRec, setResolveModalRec] = useState(null);
  const [kubernetesTotalCost, setKubernetesTotalCost] = useState('-');
  const [kubernetesFixedCount, setKubernetesFixedCount] = useState(0);
  const [listAutoPilots, setListAutoPilots] = useState();
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiAccountId, setNubiAccountId] = useState('');
  const [nubiConversationId, setNubiConversationId] = useState('');
  const [accounts, setAccounts] = useState([]);
  const [selectedAccountId, setSelectedAccountId] = useState(props?.kubernetes?.id || selectedCluster?.value);

  useEffect(() => {
    setSelectedAccountId(props?.kubernetes?.id || selectedCluster?.value);
  }, [props?.kubernetes?.id, selectedCluster?.value]);

  const tableHeaders = React.useMemo(() => {
    if (!groupName) {
      return RIGHT_SIZING_HEADER;
    }
    const newHeaders = [...RIGHT_SIZING_HEADER];
    newHeaders[0] = `Name (${groupName} Group)`;
    return newHeaders;
  }, [groupName]);

  useEffect(() => {
    if (isOptimisePage) {
      if (allCluster?.length) {
        setAccounts(allCluster);
      } else {
        apiHome.getCloudAccounts('K8s').then((res) => {
          setAccounts(res);
        });
      }
    }
  }, [isOptimisePage, allCluster]);

  const [underProvisionedResources, setUnderProvisionedResources] = useState(0);

  const changePage = (page, limit) => {
    setPage(page - 1);
    setRowsPerPage(limit);
  };

  const closeKubernetesRightSizingUpdateForm = () => {
    setOpenKubernetesRightSizingUpdateForm(false);
  };

  const onNamespaceFilterChange = (e, _p) => {
    setSelectedNamespace(e?.target?.value);
    setSelectedWorkloadName('');
    applyFiltersOnRouter(router, { namespace: e?.target?.value });
    setPage(0);
  };

  const onWorkloadTypeFilterChange = (e, _p) => {
    setSelectedWorkloadType(e?.target?.value);
    setPage(0);
  };

  const onWorkloadNameFilterChange = (e, _p) => {
    setSelectedWorkloadName(e?.target?.value);
    setPage(0);
  };

  const fetchActiveAutoPilots = () => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    recommendationApi
      .getAutoOptimize({ accountId: selectedAccountId, status: 'Active', category: ['continuous_rightsize', 'vertical_rightsize'] })
      .then((res) => {
        let listRecommendation = res?.data?.auto_pilot ?? [];
        setListAutoPilots(listRecommendation);
      });
  };

  useEffect(() => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    setPage(0);
    fetchActiveAutoPilots();
  }, [selectedAccountId, recommendationStatus]);

  const getAccountName = (id) => {
    const filteredAcc = accounts.find((ac) => ac.id == id);
    return filteredAcc?.account_name || id || '-';
  };

  const listRecommendations = () => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    if (!Array.isArray(listAutoPilots)) {
      return;
    }
    if (isOptimisePage && accounts.length === 0) {
      return;
    }
    setLoading(true);
    setKubernetesRightSizing([]);
    setKubernetesRightSizingCount(0);
    recommendationApi
      .getK8sRecommendation({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'pod_right_sizing',
        resourceNamespace: selectedNamespace,
        resourceWorkloadType: selectedWorkloadType,
        resourceWorkloadName: selectedWorkloadName || undefined,
        status: recommendationStatus.length > 0 ? recommendationStatus.map((s) => s.value) : undefined,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
        fetchTicket: true,
        accountObjectId: props?.accountObjectId,
        resource_ids: resourceIds,
      })
      .then((res) => {
        let k8sRecommendationData = res?.data?.recommendation?.map((item) => {
          const data = [];
          let hasAutopilotConfigured = false;
          let scheduledAutoPilotId;
          let continuousAutoPilotId;
          // Handle both Pod entries (use meta.controller) and Workload entries (use name directly)
          const itemWorkloadName = item.cloud_resourse.type === 'Pod' ? item.cloud_resourse?.meta?.controller : item.cloud_resourse?.name;
          for (let a of listAutoPilots) {
            let matched = false;
            for (let r of a.auto_optimize_resource_maps) {
              if (
                (r?.resource_identifier?.name == itemWorkloadName || r?.resource_identifier?.name == null) &&
                r?.resource_identifier?.namespace == item.resource_k8s_namespace
              ) {
                matched = true;
                break;
              }
            }
            if (matched) {
              hasAutopilotConfigured = true;
              if (a.category === 'vertical_rightsize') {
                scheduledAutoPilotId = a.id;
              } else if (a.category === 'continuous_rightsize') {
                continuousAutoPilotId = a.id;
              }
            }
          }
          item.hasAutopilotConfigured = hasAutopilotConfigured;
          item.scheduledAutoPilotId = scheduledAutoPilotId;
          item.continuousAutoPilotId = continuousAutoPilotId;
          // Handle both Pod entries (use meta.controller) and Workload entries (use name directly)
          let workloadName = item.cloud_resourse.type === 'Pod' ? item.cloud_resourse.meta?.controller : item.cloud_resourse.name;
          data.push({
            component: (
              <>
                <Link
                  href={{
                    pathname: `/kubernetes/details/${props?.kubernetes?.id || item.account_id}`,
                    query: {
                      subtab: 1,
                      tab: 3,
                      namespace: item.cloud_resourse.meta?.config?.namespace || item.cloud_resourse.meta?.namespace,
                      workloadName: workloadName,
                      workloadType: item.cloud_resourse.type === 'Pod' ? item.cloud_resourse.meta?.controllerKind : item.cloud_resourse.type,
                    },
                    hash: 'kubernetes/applications',
                  }}
                  target='_blank'
                >
                  {workloadName}
                </Link>
                <Text value={`Pod- ` + item.cloud_resourse.name} secondaryText />
                {isOptimisePage && (
                  <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)' }}>
                    <Text value={'acc: '} secondaryText />
                    <Link
                      href={{
                        pathname: `/kubernetes/details/${item.account_id}`,
                      }}
                      target='_blank'
                      secondaryText
                    >
                      {getAccountName(item.account_id)}
                    </Link>
                  </Box>
                )}
                {item.ticket && <CustomTicketLink ticketURL={item.ticket?.url} ticketID={item.ticket?.ticket_id} />}
                {item.resolution && <PRLink prURL={item.resolution.type_reference_id} statusMessage={item.resolution.status_message} />}
              </>
            ),
            drilldownQuery: {
              podName: item.cloud_resourse.name,
              workloadName: item.cloud_resourse.type === 'Pod' ? item.cloud_resourse.meta?.controller : item.cloud_resourse.name,
              namespaceName: item.cloud_resourse.meta?.config?.namespace || item.cloud_resourse.meta?.namespace,
              resourceId: item.cloud_resourse.resourceId,
              recommendation: item,
              accountId: item.account_id,
            },
          });
          data.push({
            component: <Text value={item.cloud_resourse.meta?.config?.namespace || item.cloud_resourse.meta?.namespace} />,
          });
          data.push({
            component: <Text value={item.cloud_resourse.type === 'Pod' ? item.cloud_resourse.meta?.controllerKind : item.cloud_resourse.type} />,
          });
          // CPU column — one Comparison per container, stacked.
          // Units (Core / MB) live in the column header, so we omit `unit` on
          // each atom to keep the row narrow enough for a single line in the
          // 14%-wide cell. Re-enable `unit: 'Core'` if the column ever widens.
          data.push({
            component: (
              <DsComparisonGroup spacing='xs'>
                {Object.entries(item.recommendation)
                  .map(([key, value]) => [key, value?.filter((v) => v.resource === 'cpu')])
                  .map(([key, value]) => {
                    const allocated = value?.[0]?.allocated?.request;
                    const recommended = value?.[0]?.recommended?.request;
                    return (
                      <DsComparison
                        key={key}
                        label={key}
                        size='sm'
                        polarity='lower-is-better'
                        before={{ value: typeof allocated === 'number' ? allocated : null }}
                        after={{ value: typeof recommended === 'number' ? recommended : null }}
                      />
                    );
                  })}
              </DsComparisonGroup>
            ),
          });
          // Memory column — same structure as CPU; unit conveyed by header.
          // Convert raw bytes → MB directly; formatMemory returns a comma-formatted
          // string ("1,024") which Number() can't parse.
          data.push({
            component: (
              <DsComparisonGroup spacing='xs'>
                {Object.entries(item.recommendation)
                  .map(([key, value]) => [key, value?.filter((v) => v.resource === 'memory')])
                  .map(([key, value]) => {
                    const allocatedBytes = value?.[0]?.allocated?.request;
                    const recommendedBytes = value?.[0]?.recommended?.request;
                    const allocatedMb = typeof allocatedBytes === 'number' ? allocatedBytes / (1024 * 1024) : null;
                    const recommendedMb = typeof recommendedBytes === 'number' ? recommendedBytes / (1024 * 1024) : null;
                    return (
                      <DsComparison
                        key={key}
                        label={key}
                        size='sm'
                        polarity='lower-is-better'
                        before={{ value: allocatedMb }}
                        after={{ value: recommendedMb }}
                      />
                    );
                  })}
              </DsComparisonGroup>
            ),
          });
          data.push({
            component: <Datetime value={item.updated_at} />,
            data: item.updated_at,
          });
          data.push({
            component: <Currency value={item.estimated_savings} precison={1} suffix='/mo' />,
            data: item.estimated_savings,
          });
          data.push({
            component: (
              <Box
                onClick={(e) => e.stopPropagation()}
                sx={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'flex-end', gap: 'var(--ds-space-1)' }}
              >
                {hasWriteAccess(item.account_id || selectedAccountId) && (
                  <DsButton
                    tone='secondary'
                    size='xs'
                    id={`rs-resolve-${item.id}`}
                    trailingAccent={<ArrowForwardIcon />}
                    onClick={() => setResolveModalRec(item)}
                  >
                    {hasAutopilotConfigured ? 'Configured' : 'Optimize'}
                  </DsButton>
                )}
                <Tooltip title={`Ask ${assistantName}`} placement='top'>
                  <span>
                    <DsButton
                      tone='ghost'
                      size='xs'
                      composition='icon-only'
                      aria-label={`Ask ${assistantName}`}
                      id={`k8s-rs-ask-nubi-${item.id}`}
                      icon={<SafeIcon src={getNubiIconUrl()} alt='' width={16} height={16} />}
                      onClick={() => {
                        const resourceName =
                          item.cloud_resourse?.type === 'Pod'
                            ? item.cloud_resourse?.meta?.controller || item.cloud_resourse?.name
                            : item.cloud_resourse?.name || item.resource_name || '';
                        const namespace = item.cloud_resourse?.meta?.config?.namespace || item.cloud_resourse?.meta?.namespace || '';
                        const prompt = buildNubiOptimizePrompt({
                          ruleName: item.rule_name || 'Pod Right Sizing',
                          category: item.category || 'RightSizing',
                          severity: item.severity || 'Info',
                          resourceName,
                          resourceType: item.cloud_resourse?.type || '',
                          namespace,
                          estimatedSavings: item.estimated_savings || undefined,
                        });
                        setNubiQuery(prompt);
                        setNubiAccountId(item.account_id || selectedAccountId);
                        setNubiConversationId(`recom_${item.id}`);
                        setNubiSidebarVisible(true);
                      }}
                    />
                  </span>
                </Tooltip>
              </Box>
            ),
          });
          return data;
        });
        setKubernetesRightSizing(k8sRecommendationData);
        setKubernetesRightSizingCount(res?.data?.recommendation_aggregate?.aggregate?.count);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    listRecommendations();
  }, [
    selectedAccountId,
    page,
    rowsPerPage,
    selectedNamespace,
    selectedWorkloadType,
    selectedWorkloadName,
    recommendationStatus,
    listAutoPilots,
    accounts?.length,
    isOptimisePage,
    resourceIds,
  ]);

  useEffect(() => {
    if (router.isReady && router.query.namespace) {
      if (namespaceFilter && namespaceFilter.length > 0) {
        const namespaceExists = namespaceFilter.find((ns) => ns === router.query.namespace);
        if (namespaceExists) {
          setSelectedNamespace(router.query.namespace);
        } else {
          setSelectedNamespace('');
          applyFiltersOnRouter(router, { namespace: '' });
        }
      }
    } else {
      setSelectedNamespace('');
    }
  }, [router.isReady, router.query.namespace, namespaceFilter]);

  useEffect(() => {
    if (!router?.query?.status) {
      return;
    }
    const statusOptions = RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s }));
    setRecommendationStatus(syncFilterFromQuery(statusOptions, router?.query?.status, (f) => f.value));
    setPage(0);
  }, [router?.query?.status]);

  useEffect(() => {
    if (!selectedAccountId && !isOptimisePage) {
      return;
    }
    if (!enabledSummary) {
      return;
    }
    recommendationApi
      .getK8sRecommendationSummary({
        accountId: selectedAccountId,
        category: 'RightSizing',
        ruleName: 'pod_right_sizing',
        status: ['Open', 'InProgress'],
        resource_ids: resourceIds,
      })
      .then((res) => {
        setKubernetesFixedCount(res?.data?.recommendation_aggregate.aggregate.count);
        setKubernetesTotalCost(res?.data?.spends_aggregate?.aggregate?.sum?.amount);
        setKubernetesRightSizingEstimatedSaving(res?.data?.recommendation_aggregate.aggregate.sum.estimated_savings);
        setUnderProvisionedResources(res?.data?.recommendation_expense_aggregate?.aggregate?.count);
      })
      .catch((error) => {
        console.error(error);
      });
  }, [selectedAccountId, enabledSummary, resourceIds]);

  useEffect(() => {
    if (!selectedAccountId) {
      return;
    }
    if (!enabledFilters) {
      return;
    }
    k8sApi.getK8sNamespaceNames(selectedAccountId).then((res) => {
      setNamespaceFilter(res?.data?.namespaces);
    });

    k8sApi.listK8sPodWorkloadType({ accountId: selectedAccountId }).then((res) => {
      let data = res.data.k8s_pods;
      setWorkloadTypeFilter(data?.map((d) => d.workload_type));
    });
  }, [selectedAccountId, enabledFilters]);

  useEffect(() => {
    if (!selectedAccountId || !enabledFilters) {
      return;
    }
    k8sApi.getK8sWorkloadNames({ accountId: selectedAccountId, namespace: selectedNamespace || undefined }).then((res) => {
      setWorkloadNameFilter(res?.data?.workloadNames || []);
    });
  }, [selectedAccountId, enabledFilters, selectedNamespace]);

  const onAccountFilterChange = (e) => {
    setSelectedAccountId(e.target.value);
    setPage(0);
    applyFiltersOnRouter(router, { accountId: e.target.value });
  };

  const statusValues = useMemo(
    () => (recommendationStatus.length > 0 ? recommendationStatus.map((s) => s.value) : undefined),
    [recommendationStatus]
  );

  const { handleExportDownload } = useRecommendationExport({
    accountId: selectedAccountId,
    category: 'RightSizing',
    ruleName: 'pod_right_sizing',
    namespace: selectedNamespace,
    workloadType: selectedWorkloadType,
    workloadName: selectedWorkloadName,
    status: statusValues,
  });

  return (
    <>
      {resolveModalRec && (
        <ResolveModal
          open={!!resolveModalRec}
          onClose={() => setResolveModalRec(null)}
          recommendation={resolveModalRec}
          clusterName={selectedCluster?.label}
          onSuccess={() => {
            setResolveModalRec(null);
            fetchActiveAutoPilots();
          }}
        />
      )}
      {enabledSummary && (
        <Box
          sx={{
            display: 'flex',
            flex: 1,
            flexDirection: 'row',
            gap: ds.space[3],
            '& > *': { maxWidth: `calc((100% - 3 * ${ds.space[3]}) / 4)` },
          }}
          mt={ds.space[4]}
          mb={ds.space[4]}
        >
          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Total Recommendations'
              info={{ tooltip: 'Active right-sizing recommendations across all workloads' }}
              value={Number.isFinite(kubernetesFixedCount) ? kubernetesFixedCount.toLocaleString() : kubernetesFixedCount ?? '—'}
            />
          </WidgetCard>

          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Total Cost'
              info={{ tooltip: 'Current monthly spend across the workloads listed below' }}
              value={
                kubernetesTotalCost === '-' || kubernetesTotalCost == null ? (
                  '—'
                ) : (
                  <CostCallout size='lg' tone='neutral' value={Number(kubernetesTotalCost) || 0} period='/ mo' />
                )
              }
            />
          </WidgetCard>

          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Savings Potential'
              info={{ tooltip: 'Estimated yearly savings if every recommendation is applied' }}
              value={<CostCallout size='lg' tone='high-savings' value={(Number(kubernetesRightSizingEstimatedSaving) || 0) * 12} period='/ yr' />}
            />
          </WidgetCard>

          <WidgetCard sx={{ flex: 1, minWidth: 0, mt: 0, padding: `${ds.space[3]} ${ds.space[4]}` }}>
            <Stat
              size='md'
              label='Expense Recommendations'
              info={{
                tooltip:
                  'Recommendations that suggest allocating MORE resources to underprovisioned workloads. Applying them improves reliability but increases cost.',
              }}
              value={Number.isFinite(underProvisionedResources) ? underProvisionedResources.toLocaleString() : underProvisionedResources ?? '—'}
            />
          </WidgetCard>
        </Box>
      )}
      <KubernetesRightSizingUpdateForm
        open={openKubernetesRightSizingUpdateForm}
        onClose={closeKubernetesRightSizingUpdateForm}
        onSuccess={closeKubernetesRightSizingUpdateForm}
        onFailure={closeKubernetesRightSizingUpdateForm}
        data={{}}
      />

      <ListingLayout id='right-sizing'>
        <ListingLayout.Toolbar
          title={props.heading === undefined ? 'Right Sizing' : props.heading || undefined}
          data-testid='rs-filter-toolbar'
          actions={
            <>
              {!isOptimisePage && <ScanRefreshButton accountId={selectedAccountId} jobName='krr_scan' idPrefix='rs' />}
              {selectedAccountId && (
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
                      id='rs-download'
                    />
                  }
                />
              )}
            </>
          }
        >
          {enabledFilters && (
            <>
              {isOptimisePage && (
                <FilterDropdown
                  id='rs-filter-account'
                  label='Account'
                  options={accounts.map((acc) => ({ label: acc.label || acc.account_name, value: acc.id || acc.value }))}
                  value={
                    accounts
                      .map((acc) => ({ label: acc.label || acc.account_name, value: acc.id || acc.value }))
                      .find((o) => o.value === selectedAccountId) ?? null
                  }
                  onSelect={(_e, item) => onAccountFilterChange({ target: { value: item?.value || '' } })}
                />
              )}
              <FilterDropdown
                id='rs-filter-namespace'
                label='Namespace'
                options={(namespaceFilter || []).map((n) => ({ label: n, value: n }))}
                value={selectedNamespace ? { label: selectedNamespace, value: selectedNamespace } : null}
                onSelect={(_e, item) => onNamespaceFilterChange({ target: { value: item?.value || '' } })}
              />
              <FilterDropdown
                id='rs-filter-workload'
                label='Workload'
                options={(workloadNameFilter || []).map((n) => ({ label: n, value: n }))}
                value={selectedWorkloadName ? { label: selectedWorkloadName, value: selectedWorkloadName } : null}
                onSelect={(_e, item) => onWorkloadNameFilterChange({ target: { value: item?.value || '' } })}
              />
              <FilterDropdown
                id='rs-filter-workload-type'
                label='Workload Type'
                options={(workloadTypeFilter || []).map((n) => ({ label: n, value: n }))}
                value={selectedWorkloadType ? { label: selectedWorkloadType, value: selectedWorkloadType } : null}
                onSelect={(_e, item) => onWorkloadTypeFilterChange({ target: { value: item?.value || '' } })}
              />
              <FilterDropdown
                id='rs-filter-status'
                label='Status'
                multiple
                options={RECOMMENDATION_STATUS.map((s) => ({ label: s === 'InProgress' ? 'In Progress' : s, value: s }))}
                value={recommendationStatus.map((s) => ({
                  label: s.label || (s.value === 'InProgress' ? 'In Progress' : s.value),
                  value: s.value,
                }))}
                onSelect={(_e, items) => {
                  const next = (Array.isArray(items) ? items : []).map((it) => ({
                    label: it.label || (it.value === 'InProgress' ? 'In Progress' : it.value),
                    value: it.value,
                  }));
                  setRecommendationStatus(next);
                  setPage(0);
                }}
              />
            </>
          )}
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          <CustomTable
            id={kubernetesRightSizingTable}
            headers={tableHeaders}
            tableData={kubernetesRightSizing}
            rowsPerPage={rowsPerPage}
            totalRows={kubernetesRightSizingCount}
            onPageChange={changePage}
            pageNumber={page + 1}
            loading={loading}
            stickyColumnIndex='8'
            showUpdatedEmptyData={props.showUpdatedEmptyData}
            showExpandable={true}
            expandable={{
              tabs: [
                {
                  text: 'CPU & Memory Utilization',
                  componentFn: (_option, drilldownQuery) => (
                    <Box
                      sx={{
                        backgroundColor: ds.background[100],
                        padding: 'var(--ds-space-4) var(--ds-space-4)',
                        borderRadius: 'var(--ds-radius-lg)',
                        border: `1px solid ${ds.blue[300]}`,
                      }}
                    >
                      {Object.entries(drilldownQuery?.recommendation?.recommendation || {}).map(([containerName, entries]) => {
                        if (!Array.isArray(entries)) {
                          return null;
                        }
                        const cpu = entries.find((e) => e.resource === 'cpu') || {};
                        const mem = entries.find((e) => e.resource === 'memory') || {};
                        return (
                          <React.Fragment key={containerName}>
                            <Heading value={'Container - ' + containerName} borderWidth='md' borderColor='var(--ds-blue-500)' />
                            <KubernetesUtilization
                              account={drilldownQuery?.accountId || selectedAccountId}
                              namespaceName={drilldownQuery.namespaceName}
                              workloadName={drilldownQuery.workloadName}
                              containerName={containerName}
                              podName={drilldownQuery.podName}
                              recc={{
                                cpuRecc: cpu?.recommended?.request,
                                cpuRequest: cpu?.allocated?.request,
                                cpuLimit: cpu?.allocated?.limit,
                                memoryRecc: formatMemory(mem?.recommended?.request, 'bytes', 'mb', false),
                                memRequest: formatMemory(mem?.allocated?.request, 'bytes', 'mb', false),
                                memLimit: formatMemory(mem?.allocated?.limit, 'bytes', 'mb', false),
                              }}
                              datasource={'prometheus'}
                            />
                          </React.Fragment>
                        );
                      })}
                    </Box>
                  ),
                },
                {
                  text: 'Resolutions',
                  componentFn: (_option, drilldownQuery) => <RecommendationResolution recommendation={drilldownQuery.recommendation} />,
                },
              ],
            }}
          />
        </ListingLayout.Body>
      </ListingLayout>

      <NubiChatSidebar
        isVisible={nubiSidebarVisible}
        onClose={() => setNubiSidebarVisible(false)}
        accountId={nubiAccountId}
        queryPrefix={nubiQuery}
        context={{ type: 'cluster', data: { conversationId: nubiConversationId } }}
        apiMode='investigate'
        categorySource='Optimize'
        position='right'
        mode='overlay'
      />
    </>
  );
};

// (RsResourceLine removed — replaced by the DS Comparison + ComparisonGroup primitives.)
KubernetesRightSizing.propTypes = {
  heading: PropTypes.string,
  kubernetes: PropTypes.object,
  showUpdatedEmptyData: PropTypes.bool,
  namespaceName: PropTypes.string,
  workloadType: PropTypes.string,
  enabledFilters: PropTypes.bool,
  enabledSummary: PropTypes.bool,
  accountObjectId: PropTypes.string,
  resourceIds: PropTypes.arrayOf(PropTypes.string),
  groupName: PropTypes.string,
  isOptimisePage: PropTypes.bool,
};

export default KubernetesRightSizing;
