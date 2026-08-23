import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import Text from '@shared/format/Text';
import Chip from '@ui/Chip';
import { Button as DsButton } from '@ui/Button';
import { DropdownMenu as DsDropdownMenu } from '@ui/DropdownMenu';
import { Switch } from '@ui/Switch';
import { Banner } from '@ui/Banner';
import FilterDropdown from '@ui/FilterDropdown';
import SearchInput from '@ui/SearchInput';
import { ListingLayout } from '@ui/ListingLayout';
import KubernetesTable from '@components/k8s/common/KubernetesTable';
import SeverityInfographics from '@components/k8s/common/SeverityInfographic';
import EmptyData from '@shared/EmptyData';
import { DataNotAvailable } from '@assets';
import { toast as snackbar } from '@ui/Toast';
import { hasWriteAccess } from '@lib/auth';
import apiCriticality, { type Criticality, type WorkloadCriticalityItem } from '@api1/criticality';

interface WorkloadCriticalityManagerProps {
  accountId?: string;
}

const LEVELS: Criticality[] = ['critical', 'high', 'medium', 'low'];

// Map a criticality to a design-system Chip tone (for the per-row tier pill).
const LEVEL_TONE: Record<Criticality, 'critical' | 'warning' | 'info' | 'neutral'> = {
  critical: 'critical',
  high: 'warning',
  medium: 'info',
  low: 'neutral',
};

const SOURCE_LABEL: Record<string, string> = {
  user: 'You',
  fact_signal: 'Auto · topology',
  llm_inferred: 'Auto · LLM',
  default: 'Default',
};

const WorkloadCriticalityManager: React.FC<WorkloadCriticalityManagerProps> = ({ accountId }) => {
  const [items, setItems] = useState<WorkloadCriticalityItem[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [tieredOnly, setTieredOnly] = useState(false);
  const [showInfo, setShowInfo] = useState(true);
  const [nsFilter, setNsFilter] = useState<string[]>([]);
  const [critFilter, setCritFilter] = useState<string[]>([]);
  const [nameSearch, setNameSearch] = useState('');
  const [savingId, setSavingId] = useState<string | null>(null);
  const canWrite = accountId ? hasWriteAccess(accountId) : false;

  const load = useCallback(async () => {
    if (!accountId) {
      return;
    }
    setLoading(true);
    try {
      // Load the whole inventory (backend caps limit at 1000) so client-side search/namespace/criticality
      // filtering and the summary counts cover every workload, not just an arbitrary first window.
      const res = await apiCriticality.list({ cloud_account_id: accountId, tiered_only: tieredOnly, limit: 1000 });
      setItems(res?.items ?? []);
      setTotal(res?.total ?? 0);
    } catch {
      snackbar.error('Failed to load workload criticality');
    } finally {
      setLoading(false);
    }
  }, [accountId, tieredOnly]);

  useEffect(() => {
    load();
  }, [load]);

  const setLevel = useCallback(
    async (item: WorkloadCriticalityItem, level: Criticality) => {
      if (!accountId || level === item.criticality) {
        return;
      }
      setSavingId(item.cloud_resource_id);
      try {
        await apiCriticality.upsert({
          cloud_account_id: accountId,
          cloud_resource_id: item.cloud_resource_id,
          namespace: item.namespace,
          criticality: level,
        });
        snackbar.success(`${item.name} set to ${level}`);
        await load();
      } catch {
        snackbar.error('Failed to update criticality');
      } finally {
        setSavingId(null);
      }
    },
    [accountId, load]
  );

  const resetOverride = useCallback(
    async (item: WorkloadCriticalityItem) => {
      if (!accountId) {
        return;
      }
      setSavingId(item.cloud_resource_id);
      try {
        await apiCriticality.remove({ cloud_account_id: accountId, cloud_resource_id: item.cloud_resource_id });
        snackbar.success(`Reset ${item.name} to derived criticality`);
        await load();
      } catch {
        snackbar.error('Failed to reset criticality');
      } finally {
        setSavingId(null);
      }
    },
    [accountId, load]
  );

  // Count per tier for the summary widget. Medium is the untiered default majority, so it's derived
  // from the total rather than counted (medium workloads have no persisted row).
  const severityData = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const it of items) {
      counts[it.criticality] = (counts[it.criticality] ?? 0) + 1;
    }
    const tiered = (counts.critical ?? 0) + (counts.high ?? 0) + (counts.low ?? 0);
    return [
      { label: 'Critical', value: counts.critical ?? '-' },
      { label: 'High', value: counts.high ?? '-' },
      { label: 'Medium', value: tieredOnly ? '-' : total - tiered },
      { label: 'Low', value: counts.low ?? '-' },
    ];
  }, [items, total, tieredOnly]);

  const renderActions = useCallback(
    (item: WorkloadCriticalityItem) => {
      if (!canWrite) {
        return null;
      }
      const setItems = LEVELS.filter((l) => l !== item.criticality).map((l) => ({
        id: `set-${item.cloud_resource_id}-${l}`,
        label: `Set ${l}`,
        onSelect: () => setLevel(item, l),
      }));
      const resetItem = item.is_user_override
        ? [
            {
              id: `reset-${item.cloud_resource_id}`,
              label: 'Reset to derived',
              onSelect: () => resetOverride(item),
            },
          ]
        : [];
      const menuItems = [...setItems, ...resetItem];
      if (menuItems.length === 0) {
        return null;
      }
      return (
        <DsDropdownMenu
          align='end'
          size='sm'
          items={menuItems}
          trigger={
            <DsButton
              tone='ghost'
              size='xs'
              composition='icon-only'
              aria-label='Change criticality'
              icon={<MoreVertIcon />}
              disabled={savingId === item.cloud_resource_id}
            />
          }
        />
      );
    },
    [canWrite, savingId, setLevel, resetOverride]
  );

  const namespaceOptions = useMemo(
    () =>
      Array.from(new Set(items.map((i) => i.namespace)))
        .sort()
        .map((ns) => ({ label: ns, value: ns })),
    [items]
  );
  const levelOptions = useMemo(() => LEVELS.map((l) => ({ label: l, value: l })), []);

  const shownItems = useMemo(() => {
    const q = nameSearch.trim().toLowerCase();
    return items.filter(
      (i) =>
        (nsFilter.length === 0 || nsFilter.includes(i.namespace)) &&
        (critFilter.length === 0 || critFilter.includes(i.criticality)) &&
        (q === '' || i.name.toLowerCase().includes(q))
    );
  }, [items, nsFilter, critFilter, nameSearch]);

  const tableData = useMemo(
    () =>
      shownItems.map((item) => [
        { component: <Text value={item.namespace} /> },
        { component: <Text value={item.name} /> },
        { component: <Text value={item.kind} /> },
        {
          component: (
            <Chip variant='tag' size='xs' tone={LEVEL_TONE[item.criticality]}>
              {item.criticality}
            </Chip>
          ),
        },
        { component: <Text value={SOURCE_LABEL[item.source] ?? item.source} /> },
        { component: <Text value={item.rationale || '-'} /> },
        { component: renderActions(item) },
      ]),
    [shownItems, renderActions]
  );

  if (!accountId) {
    return null;
  }

  return (
    <>
      {showInfo && (
        <Banner
          id='workload-criticality-info'
          tone='info'
          surface='section'
          dismissible
          onDismiss={() => setShowInfo(false)}
          title='Why set service criticality?'
          message='Criticality tells Nudgebee how much each workload matters, so incident triage surfaces and prioritizes failures on your important services first — and downranks the noise from demo, test, and internal tooling. Nudgebee infers a baseline automatically (topology + AI); review and correct it here so scoring reflects what is actually business-critical in your environment. Your edits are kept and never overwritten by the automatic refresh.'
        />
      )}
      <ListingLayout id='workload-criticality-list-box'>
        <ListingLayout.Toolbar
          data-testid='workload-criticality-toolbar'
          actions={
            <Switch
              checked={tieredOnly}
              onChange={(_e, checked) => setTieredOnly(checked)}
              label='Only classified'
              description='Hide workloads left at the default (medium) tier'
              aria-label='Show only workloads with an assigned tier'
            />
          }
        >
          <Box display='flex' alignItems='center' gap='var(--ds-space-3)' flexWrap='wrap'>
            <SeverityInfographics severityData={severityData} customStyle={{}} />
            <SearchInput label='Search workload' value={nameSearch} onChange={setNameSearch} />
            <FilterDropdown
              id='criticality-filter-namespace'
              label='Namespace'
              multiple
              options={namespaceOptions}
              value={namespaceOptions.filter((o) => nsFilter.includes(o.value))}
              onSelect={(_e: any, sel: any) => setNsFilter((Array.isArray(sel) ? sel : []).map((v: any) => v.value))}
            />
            <FilterDropdown
              id='criticality-filter-tier'
              label='Criticality'
              multiple
              options={levelOptions}
              value={levelOptions.filter((o) => critFilter.includes(o.value))}
              onSelect={(_e: any, sel: any) => setCritFilter((Array.isArray(sel) ? sel : []).map((v: any) => v.value))}
            />
          </Box>
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          {!loading && shownItems.length === 0 ? (
            <EmptyData
              id='workload-criticality-empty'
              img={DataNotAvailable}
              heading='No Data Available'
              subHeading='Workloads and their inferred criticality will appear here'
            />
          ) : (
            <KubernetesTable
              id='workloadCriticality'
              data={tableData}
              headers={[
                { name: 'Namespace', width: '16%' },
                { name: 'Workload', width: '22%' },
                { name: 'Kind', width: '10%' },
                { name: 'Criticality', width: '12%' },
                { name: 'Source', width: '14%' },
                { name: 'Why', width: '20%' },
                { name: '', width: '6%' },
              ]}
              loading={loading}
              tableHeadingCenter={['Criticality', 'Source']}
            />
          )}
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default WorkloadCriticalityManager;
