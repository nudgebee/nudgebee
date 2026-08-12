import React, { useCallback, useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import CustomTable from '@shared/tables/CustomTable';
import ListingLayout from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import SeverityIcon from '@ui/SeverityIcon';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import { Chip } from '@ui/Chip';
import { Label } from '@ui/Label';
import Datetime from '@shared/format/Datetime';
import Tabs from '@shared/navigation/Tabs';
import DownloadButton from '@shared/buttons/DownloadButton';
import TicketCreatePopupForm from '@components/tickets/TicketCreatePopupForm';
import { toast as snackbar } from '@ui/Toast';
import { usePagination } from '@hooks/usePagination';
import { toSeverityLevel } from '@utils/common';
import { hasPermission, hasWriteAccess } from '@lib/auth';
import apiVm, { SEVERITY_ORDER, VmVulnerability, VmVulnerabilityGroup, VmVulnerabilityGrouping } from '@api1/vm';
import { CellText, joinVmNames, useLatestRequest } from './common';
import { ds } from '@utils/colors';

// Widths are explicit because CustomTable falls back to 20% per column, which
// let Severity (a badge) and the version columns starve Last Seen until its
// relative time wrapped to three lines.
const HEADERS = [
  { name: 'Severity', width: '6%' },
  { name: 'Vulnerability', width: '18%' },
  { name: 'Package', width: '19%' },
  { name: 'Installed', width: '17%' },
  { name: 'Fixed In', width: '17%' },
  { name: 'CVSS', width: '5%' },
  { name: 'Last Seen', width: '10%' },
  { name: 'Actions', width: '8%' },
];

const SEVERITY_OPTIONS = SEVERITY_ORDER.map((severity) => ({ label: severity, value: severity }));

/** Cap on the VM / Package filter lists. Both dropdowns search their options. */
const FILTER_OPTION_LIMIT = 200;

/** Grouping tabs. `all` is the flat finding list; the rest roll it up server-side. */
const GROUP_TABS = [
  { text: 'All', value: 'all', id: 'vm-vulnerability-tab-all' },
  { text: 'Vulnerability', value: 'vulnerability', id: 'vm-vulnerability-tab-vulnerability' },
  { text: 'Package', value: 'package', id: 'vm-vulnerability-tab-package' },
  { text: 'VM', value: 'vm', id: 'vm-vulnerability-tab-vm' },
];

const GROUP_HEADERS: Record<string, Array<{ name: string; width: string }>> = {
  vulnerability: [
    { name: 'Severity', width: '8%' },
    { name: 'Vulnerability', width: '37%' },
    { name: 'VM', width: '33%' },
    { name: 'Findings', width: '22%' },
  ],
  package: [
    { name: 'Severity', width: '8%' },
    { name: 'Package', width: '37%' },
    { name: 'VM', width: '33%' },
    { name: 'Findings', width: '22%' },
  ],
  // No VM column here — the row is the VM.
  vm: [
    { name: 'Severity', width: '8%' },
    { name: 'VM', width: '70%' },
    { name: 'Findings', width: '22%' },
  ],
};

/**
 * Reverse of the severity_weight ranking the grouping query aggregates with — a
 * group carries its worst severity as that weight, not as a name.
 */
const WEIGHT_SEVERITY: Array<[number, string]> = [
  [10, 'Critical'],
  [8, 'High'],
  [5, 'Medium'],
  [2, 'Low'],
];
const severityFromWeight = (weight: number) => WEIGHT_SEVERITY.find(([threshold]) => weight >= threshold)?.[1] || 'Info';

/**
 * Row actions. Create Ticket needs write access to the finding's account or the
 * tickets:Write grant — disabled rather than dropped, with the requirement named
 * in the label because a disabled menu item can't carry a tooltip. Applying the
 * patch is not built yet (the agent has no patch runner), so it's a placeholder.
 */
const menuItems = (canCreateTicket: boolean) => [
  {
    id: 'create-ticket',
    label: canCreateTicket ? 'Create Ticket' : 'Create Ticket · requires tickets:Write',
    disabled: !canCreateTicket,
  },
  {
    id: 'apply-patch',
    label: 'Apply Patch · Coming Soon',
    disabled: true,
  },
];

/** Markdown body for a ticket raised off one CVE finding. */
const ticketDescription = (finding: VmVulnerability) => {
  const payload = finding.recommendation || {};
  const lines = [
    `**Vulnerability**: ${payload.vuln_id || '-'}`,
    `**Severity**: ${finding.severity || '-'}`,
    `**VM**: ${finding.resource_name || '-'}`,
    `**Package**: ${payload.package?.name || '-'} ${payload.package?.version || ''}`.trim(),
    `**Fixed in**: ${payload.fixed_version || payload.fix_state || 'No fix available'}`,
  ];
  if (payload.cvss_v3_score != null) lines.push(`**CVSS v3**: ${payload.cvss_v3_score}`);
  if (payload.kev) lines.push('**Known exploited vulnerability**: yes');
  if (payload.description) lines.push(`\n${payload.description}`);
  return lines.join('\n');
};

interface VmVulnerabilitiesProps {
  accountId: string;
  /** Scope to a single VM. Set by the inventory table's expanded row. */
  cloudResourceId?: string;
  /** Scope to a single CVE. Set by the Vulnerability grouping's expanded row. */
  vulnId?: string;
  /** Scope to a single package. Set by the Package grouping's expanded row. */
  packageName?: string;
  /** Embedded in an expandable row: drops the card chrome, tabs, and VM subtext. */
  embedded?: boolean;
  /** Severity to open with. Set when a summary card links in pre-filtered. */
  initialSeverity?: string;
  /**
   * What the scope props mean in words, e.g. "Package: openssl". Rendering it
   * makes a scope arrived at by URL visible; passing onClearScope makes it
   * removable. Only the top-level view uses these — an expanded row's scope is
   * explained by the row it opened from.
   */
  scopeLabel?: string;
  onClearScope?: () => void;
}

/**
 * Open CVE findings written by the VM package scan — recommendation rows with
 * rule_name = 'vm_package_vulnerability'. The findings live in the same table as
 * every other recommendation, so this is a filtered view of it rather than a
 * separate store.
 *
 * The grouping tabs roll the same findings up by CVE, package, or VM. Each group
 * row expands into this component again, embedded and scoped to that group — so
 * the drill-down is the same list under a narrower filter, not a second view.
 */
const VmVulnerabilities = ({
  accountId,
  cloudResourceId,
  vulnId,
  packageName,
  embedded = false,
  initialSeverity,
  scopeLabel,
  onClearScope,
}: VmVulnerabilitiesProps) => {
  const [loading, setLoading] = useState(false);
  const [rows, setRows] = useState<VmVulnerability[]>([]);
  const [groups, setGroups] = useState<VmVulnerabilityGroup[]>([]);
  const [total, setTotal] = useState(0);
  const [severity, setSeverity] = useState<string | null>(initialSeverity || null);
  const [grouping, setGrouping] = useState<VmVulnerabilityGrouping | 'all'>('all');
  const [ticketFinding, setTicketFinding] = useState<VmVulnerability | null>(null);
  // Seeded from the scope props so a link that arrives filtered shows its filter
  // in the control that owns it; from then on the dropdown is authoritative, or
  // the filter could never be cleared.
  const [vmFilter, setVmFilter] = useState<string | null>(cloudResourceId || null);
  const [packageFilter, setPackageFilter] = useState<string | null>(packageName || null);
  const [vmOptions, setVmOptions] = useState<Array<{ label: string; value: string }>>([]);
  const [packageOptions, setPackageOptions] = useState<Array<{ label: string; value: string }>>([]);
  const { page, rowsPerPage, changePage, setPage } = usePagination(embedded ? 5 : 10);
  const beginRequest = useLatestRequest();

  const canCreateTicket = hasWriteAccess(accountId) || hasPermission('tickets', 'Write');

  // Embedded instances never show the tabs, so grouping stays 'all' for them.
  const isGrouped = grouping !== 'all';
  // Embedded scope comes from the row that opened it and cannot be changed;
  // everywhere else the dropdowns own it.
  const vmScope = (embedded ? cloudResourceId : vmFilter) || undefined;
  const packageScope = (embedded ? packageName : packageFilter) || undefined;
  // One table id per scope: the id is what DownloadButton exports and what the
  // DOM node is keyed by, and several of these render at once (one per expanded
  // group row).
  const scope = vmScope || vulnId || packageScope;
  const tableId = scope
    ? `VM_VULNERABILITIES_${scope.replace(/[^a-zA-Z0-9_-]/g, '_')}`
    : isGrouped
    ? `VM_VULNERABILITY_GROUPS_${grouping}`
    : 'VM_VULNERABILITIES_TABLE';

  const fetchRows = useCallback(() => {
    if (!accountId) return;
    setLoading(true);
    // Flipping the severity filter or the grouping tab mid-flight must not let
    // the older response overwrite the newer one.
    const isLatest = beginRequest();
    const paging = { limit: rowsPerPage, offset: page * rowsPerPage };
    const filters = { cloudResourceId: vmScope, vulnId, packageName: packageScope, severity: severity || undefined };
    const request =
      grouping === 'all'
        ? apiVm
            .listVulnerabilities({ accountId, ...filters, ...paging })
            .then(({ rows: findings, total: count }) => ({ findings, groupRows: [] as VmVulnerabilityGroup[], count }))
        : apiVm
            .listVulnerabilityGroups({ accountId, grouping, includeVms: true, ...filters, ...paging })
            .then(({ rows: groupRows, total: count }) => ({ findings: [] as VmVulnerability[], groupRows, count }));
    request
      .then(({ findings, groupRows, count }) => {
        if (!isLatest()) return;
        setRows(findings);
        setGroups(groupRows);
        setTotal(count);
      })
      .catch((error) => {
        if (!isLatest()) return;
        console.error('Failed to list VM vulnerabilities:', error);
        setRows([]);
        setGroups([]);
        setTotal(0);
      })
      .finally(() => {
        if (isLatest()) setLoading(false);
      });
  }, [accountId, vmScope, vulnId, packageScope, severity, grouping, page, rowsPerPage, beginRequest]);

  useEffect(() => {
    fetchRows();
  }, [fetchRows]);

  // Filter options are the VMs and packages that actually carry a finding —
  // filtering by one that has none would only ever produce an empty table. Read
  // once per account; the toolbar they feed only exists on the top-level view.
  useEffect(() => {
    if (embedded || !accountId) return undefined;
    let cancelled = false;
    const options = (grouping: 'vm' | 'package') =>
      apiVm.listVulnerabilityGroups({ accountId, grouping, limit: FILTER_OPTION_LIMIT, offset: 0, includeTotal: false, orderBy: 'count' });
    Promise.all([options('vm'), options('package')])
      .then(([vms, packages]) => {
        if (cancelled) return;
        setVmOptions(vms.rows.map((group) => ({ label: group.label || group.key, value: group.key })));
        setPackageOptions(packages.rows.filter((group) => group.key).map((group) => ({ label: group.label, value: group.key })));
      })
      .catch((error) => {
        if (!cancelled) console.error('Failed to load VM vulnerability filter options:', error);
      });
    return () => {
      cancelled = true;
    };
  }, [accountId, embedded]);

  const onMenuClick = (item: { id: string }, finding: VmVulnerability) => {
    if (item.id === 'create-ticket') setTicketFinding(finding);
  };

  // Where the VM goes depends on what the table is already scoped to. Scoped to
  // one VM (the inventory row's drill-down) it is not worth a line at all. Under
  // a CVE it belongs beside the CVE — that drill-down's question is "which
  // machines", and the package column varies too. Otherwise it rides under the
  // package, where a finding reads as "this package on this machine".
  const vmUnderVulnerability = Boolean(vulnId) && !cloudResourceId;
  const showVm = !cloudResourceId;

  const tableData = rows.map((finding) => {
    const payload = finding.recommendation || {};
    const packageSubtext = [payload.package?.type, showVm && !vmUnderVulnerability ? finding.resource_name : undefined].filter(Boolean).join(' · ');
    const vulnSubtext = [payload.kev ? 'Known exploited' : undefined, vmUnderVulnerability ? finding.resource_name : undefined]
      .filter(Boolean)
      .join(' · ');
    return [
      { component: <SeverityIcon level={toSeverityLevel(finding.severity)} size={14} aria-label={finding.severity} /> },
      { component: <CellText text={payload.vuln_id} subtext={vulnSubtext || undefined} mono /> },
      { component: <CellText text={payload.package?.name} subtext={packageSubtext || undefined} /> },
      { component: <CellText text={payload.package?.version} mono /> },
      {
        component: payload.fixed_version ? (
          <CellText text={payload.fixed_version} mono />
        ) : (
          <Label tone='neutral' size='sm' text={payload.fix_state || 'No fix'} />
        ),
      },
      { component: <CellText text={payload.cvss_v3_score != null ? String(payload.cvss_v3_score) : undefined} /> },
      { component: <Datetime value={finding.updated_at} /> },
      {
        component: (
          <ThreeDotsMenu
            id={`vm-vulnerability-actions-${finding.id}`}
            menuItems={menuItems(canCreateTicket)}
            data={finding}
            onMenuClick={onMenuClick}
            menuWidth={260}
          />
        ),
      },
    ];
  });

  // Which filter the drill-down applies is the only thing that differs per
  // grouping — the expanded row is this same component, scoped.
  const groupScope = (group: VmVulnerabilityGroup) => {
    if (grouping === 'vulnerability') return { vulnId: group.key };
    if (grouping === 'package') return { packageName: group.key };
    return { cloudResourceId: group.key };
  };

  const groupData = groups.map((group) => {
    const groupSeverity = severityFromWeight(group.max_severity_weight);
    const cells: any[] = [
      { component: <SeverityIcon level={toSeverityLevel(groupSeverity)} size={14} aria-label={groupSeverity} />, drilldownQuery: group },
      { component: <CellText text={group.label || '—'} mono={grouping !== 'vm'} /> },
    ];
    // A CVE or a package spans machines; a VM group is one, and says so already.
    if (grouping !== 'vm') {
      cells.push({ component: <CellText text={joinVmNames(group.resource_names)} /> });
    }
    cells.push({ component: <CellText text={String(group.count)} /> });
    return cells;
  });

  const table = (
    <CustomTable
      id={tableId}
      headers={isGrouped ? GROUP_HEADERS[grouping] : HEADERS}
      tableData={isGrouped ? groupData : tableData}
      loading={loading}
      rowsPerPage={rowsPerPage}
      pageNumber={page + 1}
      totalRows={total}
      onPageChange={changePage}
      showExpandable={isGrouped}
      expandable={
        isGrouped
          ? {
              tabs: [
                {
                  text: 'Findings',
                  value: 0,
                  key: `vm-vulnerability-group-${grouping}`,
                  componentFn: (_option: any, group: VmVulnerabilityGroup) => (
                    <VmVulnerabilities accountId={accountId} embedded {...groupScope(group)} />
                  ),
                },
              ],
            }
          : undefined
      }
      emptyHeading='No open vulnerabilities'
      emptySubHeading='Findings appear here after a VM package scan matches installed packages against the CVE database.'
      showUpdatedEmptyData={true}
    />
  );

  const ticketModal = ticketFinding && (
    <TicketCreatePopupForm
      open={!!ticketFinding}
      handleClose={() => setTicketFinding(null)}
      onClose={() => setTicketFinding(null)}
      onSuccess={() => setTicketFinding(null)}
      onFailure={(error: string) => snackbar.error(error || 'Failed to create ticket')}
      ticketData={{
        subject: `${ticketFinding.severity} vulnerability ${ticketFinding.recommendation?.vuln_id || ''} in ${
          ticketFinding.recommendation?.package?.name || 'package'
        } on ${ticketFinding.resource_name || 'VM'}`,
        description: ticketDescription(ticketFinding),
        accountId: ticketFinding.account_id || accountId,
      }}
      ticketUrl={{}}
      reference={{ id: ticketFinding.id, type: 'recommendation' }}
    />
  );

  if (embedded) {
    // Same card chrome as the standalone view — the expanding row supplies the
    // heading (its tab strip), so the card carries no toolbar of its own.
    return (
      <Box sx={{ py: ds.space[2] }}>
        <ListingLayout id={`${tableId}-card`}>
          <ListingLayout.Body>{table}</ListingLayout.Body>
        </ListingLayout>
        {ticketModal}
      </Box>
    );
  }

  return (
    <Box sx={{ px: ds.space[5], pb: ds.space[5] }}>
      {/* Grouping selector — an outer tab bar above the card, so the toolbar
          below keeps carrying only the filters that apply to every tab. */}
      <Box sx={{ pb: ds.space[3] }}>
        <Tabs
          value={grouping}
          onChange={(next: VmVulnerabilityGrouping | 'all') => {
            setPage(0);
            setGrouping(next);
          }}
          options={{ tabOptions: GROUP_TABS }}
          behavior='filter'
          ariaLabel='Group vulnerabilities by'
        />
      </Box>
      <ListingLayout id='vm-vulnerabilities'>
        <ListingLayout.Toolbar actions={<DownloadButton id={`${tableId}-download`} onClick={() => ({ tableId })} />}>
          <FilterDropdown
            id='vm-vulnerability-severity'
            label='Severity'
            value={severity}
            options={SEVERITY_OPTIONS}
            onSelect={(event: any) => {
              setPage(0);
              setSeverity(event?.target?.value || null);
            }}
          />
          {vmOptions.length > 0 && (
            <FilterDropdown
              id='vm-vulnerability-vm'
              label='VM'
              value={vmFilter}
              options={vmOptions}
              onSelect={(event: any) => {
                setPage(0);
                setVmFilter(event?.target?.value || null);
              }}
            />
          )}
          {packageOptions.length > 0 && (
            <FilterDropdown
              id='vm-vulnerability-package'
              label='Package'
              value={packageFilter}
              options={packageOptions}
              onSelect={(event: any) => {
                setPage(0);
                setPackageFilter(event?.target?.value || null);
              }}
            />
          )}
          {scopeLabel ? (
            <Chip variant='filter' size='sm' tone='info' onDismiss={onClearScope} sx={{ alignSelf: 'center' }}>
              {scopeLabel}
            </Chip>
          ) : null}
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], alignSelf: 'center' }}>Open findings only</Typography>
        </ListingLayout.Toolbar>
        <ListingLayout.Body>{table}</ListingLayout.Body>
      </ListingLayout>
      {ticketModal}
    </Box>
  );
};

export default VmVulnerabilities;
