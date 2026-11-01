import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import CustomTable from '@shared/tables/CustomTable';
import ListingLayout from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import SeverityIcon from '@ui/SeverityIcon';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import { Chip } from '@ui/Chip';
import { Label } from '@ui/Label';
import Datetime from '@shared/format/Datetime';
import { ToggleGroup } from '@ui/ToggleGroup';
import DownloadButton from '@shared/buttons/DownloadButton';
import TicketLink from '@shared/links/TicketLink';
import { useTenantBranding } from '@hooks/useTenantBranding';
import TicketCreatePopupForm from '@components/tickets/TicketCreatePopupForm';
import { toast as snackbar } from '@ui/Toast';
import { usePagination } from '@hooks/usePagination';
import { toSeverityLevel } from '@utils/common';
import { hasPermission, hasWriteAccess } from '@lib/auth';
import apiVm, { SEVERITY_ORDER, VmVulnerability, VmVulnerabilityGroup, VmVulnerabilityGrouping } from '@api1/vm';
import SecurityFindingPanel from '@components/recommendations/security/SecurityFindingPanel';
import { fromVmRow } from '@components/recommendations/security/securityFinding';
import { AskNubiIconButton, findingPromptContext, useAskNubiAboutSecurityFinding } from '@components/recommendations/security/askNubi';
import { dismissMenuEntry, useRecommendationDismissal } from '@components/recommendations/security/dismissal';
import { CellText, joinVmNames, severityFromWeight, useLatestRequest } from './common';
import { ds } from '@utils/colors';

// Widths are explicit because CustomTable falls back to 20% per column, which
// let Severity (a badge) and the version columns starve Last Seen until its
// relative time wrapped to three lines.
const HEADERS = [
  { name: 'Severity', width: '8%' },
  { name: 'Vulnerability', width: '16%' },
  { name: 'Package', width: '19%' },
  { name: 'Installed', width: '17%' },
  { name: 'Fixed In', width: '17%' },
  { name: 'CVSS', width: '5%' },
  { name: 'Last Seen', width: '10%' },
  { name: 'Actions', width: '8%' },
];

// Flat-table headers for the cross-account Security tab: same columns, with the
// owning account named first. Widths trimmed from the two widest text columns.
const HEADERS_WITH_ACCOUNT = [
  { name: 'Account', width: '12%' },
  { name: 'Severity', width: '8%' },
  { name: 'Vulnerability', width: '15%' },
  { name: 'Package', width: '16%' },
  { name: 'Installed', width: '15%' },
  { name: 'Fixed In', width: '15%' },
  { name: 'CVSS', width: '5%' },
  { name: 'Last Seen', width: '8%' },
  { name: 'Actions', width: '8%' },
];

const SEVERITY_OPTIONS = SEVERITY_ORDER.map((severity) => ({ label: severity, value: severity }));

/** Cap on the VM / Package filter lists. Both dropdowns search their options. */
const FILTER_OPTION_LIMIT = 200;

/**
 * How the findings are pivoted. `all` is the flat finding list; the rest roll it
 * up server-side into a different column set (see GROUP_HEADERS) — these are not
 * filters, which is why the control that selects them says "group by".
 */
const GROUP_TABS: Array<{ text: string; value: VmVulnerabilityGrouping | 'all'; id: string }> = [
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
 * Row actions. Create Ticket needs write access to the finding's account or the
 * tickets:Write grant — disabled rather than dropped, with the requirement named
 * in the label because a disabled menu item can't carry a tooltip. Applying the
 * patch is not built yet (the agent has no patch runner), so it's a placeholder.
 */
const menuItems = (canCreateTicket: boolean, finding: VmVulnerability) => {
  const dismissEntry = dismissMenuEntry(finding.status, hasWriteAccess(finding.account_id));
  const existingTicket = finding.ticket?.ticket_id;
  return [
    {
      id: 'create-ticket',
      label: existingTicket ? `Ticket: ${existingTicket}` : canCreateTicket ? 'Create Ticket' : 'Create Ticket · requires tickets:Write',
      disabled: !!existingTicket || !canCreateTicket,
    },
    {
      id: 'apply-patch',
      label: 'Apply Patch · Coming Soon',
      disabled: true,
    },
    ...(dismissEntry ? [{ id: dismissEntry.id, label: dismissEntry.label }] : []),
  ];
};

// Dismissed covers snoozed rows too: a snooze is Dismissed plus an expiry.
const STATUS_OPTIONS = [
  { label: 'Open', value: 'Open' },
  { label: 'Dismissed / snoozed', value: 'Dismissed' },
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
  /** One VM account, or the list of them (the cross-account Security tab). */
  accountId: string | string[];
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
  /** Extra filter(s) rendered first in the toolbar — the cross-account Security tab's Account picker. */
  leadingFilters?: React.ReactNode;
  /**
   * Drop the standalone /vm page's own outer padding. Set when this view is
   * hosted inside another page's tab, where the host already supplies the page
   * inset — without it the table sits narrower than its sibling tabs.
   */
  hidePageInset?: boolean;
  /**
   * account id → display name. Set only by the cross-account Security tab, where
   * it adds an Account column to the flat findings table. The grouped views roll
   * findings up ACROSS accounts, so a single account per group row would be
   * wrong there and the column is deliberately omitted.
   */
  accountsById?: Record<string, string>;
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
  leadingFilters,
  accountsById,
  hidePageInset = false,
}: VmVulnerabilitiesProps) => {
  const [loading, setLoading] = useState(false);
  const [rows, setRows] = useState<VmVulnerability[]>([]);
  const [groups, setGroups] = useState<VmVulnerabilityGroup[]>([]);
  const [total, setTotal] = useState(0);
  const [severity, setSeverity] = useState<string | null>(initialSeverity || null);
  const [statusFilter, setStatusFilter] = useState<string>('Open');
  const [grouping, setGrouping] = useState<VmVulnerabilityGrouping | 'all'>('all');
  const [ticketFinding, setTicketFinding] = useState<VmVulnerability | null>(null);
  // Flat findings had no detail view at all — the scan already stores a
  // description, EPSS, KEV and advisory links that nothing rendered.
  const [panelFinding, setPanelFinding] = useState<VmVulnerability | null>(null);
  // Seeded from the scope props so a link that arrives filtered shows its filter
  // in the control that owns it; from then on the dropdown is authoritative, or
  // the filter could never be cleared.
  const [vmFilter, setVmFilter] = useState<string | null>(cloudResourceId || null);
  const [packageFilter, setPackageFilter] = useState<string | null>(packageName || null);
  const [vmOptions, setVmOptions] = useState<Array<{ label: string; value: string }>>([]);
  const [packageOptions, setPackageOptions] = useState<Array<{ label: string; value: string }>>([]);
  const { page, rowsPerPage, changePage, setPage } = usePagination(embedded ? 5 : 10);
  const beginRequest = useLatestRequest();
  const { assistantName } = useTenantBranding();
  const askNubi = useAskNubiAboutSecurityFinding();
  const askAboutFinding = useCallback(
    (finding: VmVulnerability) =>
      askNubi({
        accountId: finding.account_id,
        sessionKey: finding.id,
        context: findingPromptContext(fromVmRow(finding), accountsById?.[finding.account_id]),
      }),
    [askNubi, accountsById]
  );

  // With a multi-account scope the check falls back to tenant-level write; the
  // ticket itself is always created against the finding's own account.
  const canCreateTicket = hasWriteAccess(Array.isArray(accountId) ? undefined : accountId) || hasPermission('tickets', 'Write');

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
    const filters = {
      cloudResourceId: vmScope,
      vulnId,
      packageName: packageScope,
      severity: severity || undefined,
      status: statusFilter === 'Dismissed' ? ['Dismissed'] : undefined,
    };
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
  }, [accountId, vmScope, vulnId, packageScope, severity, statusFilter, grouping, page, rowsPerPage, beginRequest]);

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

  const dismissal = useRecommendationDismissal(() => {
    setPanelFinding(null);
    fetchRows();
  });

  const onMenuClick = useCallback(
    (item: { id: string }, finding: VmVulnerability) => {
      if (item.id === 'create-ticket') setTicketFinding(finding);
      else if (item.id === 'dismiss' || item.id === 'reactivate') dismissal.toggle(finding);
    },
    [dismissal]
  );

  // Where the VM goes depends on what the table is already scoped to. Scoped to
  // one VM (the inventory row's drill-down) it is not worth a line at all. Under
  // a CVE it belongs beside the CVE — that drill-down's question is "which
  // machines", and the package column varies too. Otherwise it rides under the
  // package, where a finding reads as "this package on this machine".
  const vmUnderVulnerability = Boolean(vulnId) && !cloudResourceId;
  const showVm = !cloudResourceId;

  const tableData = useMemo(
    () =>
      rows.map((finding) => {
        const payload = finding.recommendation || {};
        // arch distinguishes the common case of the same CVE/package appearing
        // as two rows for one VM — once per architecture the package is
        // installed under (e.g. noarch and x86_64) — which otherwise look
        // identical apart from this column (#38976).
        const packageSubtext = [payload.package?.type, payload.package?.arch, showVm && !vmUnderVulnerability ? finding.resource_name : undefined]
          .filter(Boolean)
          .join(' · ');
        const vulnSubtext = [payload.kev ? 'Known exploited' : undefined, vmUnderVulnerability ? finding.resource_name : undefined]
          .filter(Boolean)
          .join(' · ');
        return [
          ...(accountsById
            ? [{ component: <CellText text={accountsById[finding.account_id] || finding.account_id} />, drilldownQuery: { finding } }]
            : []),
          {
            component: <SeverityIcon level={toSeverityLevel(finding.severity)} size={14} aria-label={finding.severity} />,
            drilldownQuery: { finding },
          },
          {
            component: (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[0] }}>
                <CellText text={payload.vuln_id} subtext={vulnSubtext || undefined} mono />
                {finding.ticket ? <TicketLink ticketURL={finding.ticket.url || ''} ticketID={finding.ticket.ticket_id} /> : null}
              </Box>
            ),
          },
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
              <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
                <AskNubiIconButton
                  id={`vm-vulnerability-ask-nubi-${finding.id}`}
                  assistantName={assistantName}
                  onClick={() => askAboutFinding(finding)}
                />
                <ThreeDotsMenu
                  id={`vm-vulnerability-actions-${finding.id}`}
                  menuItems={menuItems(canCreateTicket, finding)}
                  data={finding}
                  onMenuClick={onMenuClick}
                  menuWidth={260}
                />
              </Box>
            ),
          },
        ];
      }),
    [rows, accountsById, showVm, vmUnderVulnerability, assistantName, canCreateTicket, onMenuClick, askAboutFinding]
  );

  // Which filter the drill-down applies is the only thing that differs per
  // grouping — the expanded row is this same component, scoped.
  const groupScope = (group: VmVulnerabilityGroup) => {
    if (grouping === 'vulnerability') return { vulnId: group.key };
    if (grouping === 'package') return { packageName: group.key };
    return { cloudResourceId: group.key };
  };

  // An empty result reads as "no open vulnerabilities" (good news) only when
  // nothing narrowed the query — otherwise it just means the filter (severity,
  // status, VM, package, or a deep-link scope) matched nothing, which
  // CustomTable's showUpdatedEmptyData path can't tell apart on its own.
  const isFiltered = Boolean(severity || vmFilter || packageFilter || scopeLabel || statusFilter === 'Dismissed');

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
      headers={isGrouped ? GROUP_HEADERS[grouping] : accountsById ? HEADERS_WITH_ACCOUNT : HEADERS}
      tableData={isGrouped ? groupData : tableData}
      loading={loading}
      rowsPerPage={rowsPerPage}
      pageNumber={page + 1}
      totalRows={total}
      onPageChange={changePage}
      tableHeadingCenter={['Severity']}
      showExpandable={isGrouped}
      onRowClick={isGrouped ? undefined : (query: any) => query?.finding && setPanelFinding(query.finding)}
      expandable={
        isGrouped
          ? {
              tabs: [
                {
                  text: 'Findings',
                  value: 0,
                  key: `vm-vulnerability-group-${grouping}`,
                  componentFn: (_option: any, group: VmVulnerabilityGroup) => (
                    <VmVulnerabilities accountId={accountId} accountsById={accountsById} embedded {...groupScope(group)} />
                  ),
                },
              ],
            }
          : undefined
      }
      emptyHeading={isFiltered ? 'No vulnerabilities match these filters' : 'No open vulnerabilities'}
      emptySubHeading={
        isFiltered
          ? 'Try adjusting or clearing the Severity, Status, VM, or Package filters.'
          : 'Findings appear here after a VM package scan matches installed packages against the CVE database.'
      }
      showUpdatedEmptyData={true}
    />
  );

  const findingPanel = (
    <SecurityFindingPanel
      open={Boolean(panelFinding)}
      onClose={() => setPanelFinding(null)}
      finding={panelFinding ? fromVmRow(panelFinding) : null}
      accountName={panelFinding ? accountsById?.[panelFinding.account_id] : undefined}
      scopeAccountId={accountId}
      onCreateTicket={() => {
        if (panelFinding) setTicketFinding(panelFinding);
      }}
      onAskNubi={() => {
        if (panelFinding) askAboutFinding(panelFinding);
      }}
      onDismiss={panelFinding && hasWriteAccess(panelFinding.account_id) ? () => dismissal.toggle(panelFinding) : undefined}
    />
  );

  const ticketModal = ticketFinding && (
    <TicketCreatePopupForm
      open={!!ticketFinding}
      handleClose={() => setTicketFinding(null)}
      onClose={() => setTicketFinding(null)}
      onSuccess={({ ticketId, url }: { ticketId?: string; url?: string } = {}) => {
        // Update the row (and open detail panel, if it's the same finding) locally rather than
        // refetch — the ticket→finding link can lag a re-query.
        if (ticketFinding && ticketId) {
          const ticket = { ticket_id: ticketId, url };
          setRows((prev) => prev.map((row) => (row.id === ticketFinding.id ? { ...row, ticket } : row)));
          if (panelFinding?.id === ticketFinding.id) {
            setPanelFinding((prev) => (prev ? { ...prev, ticket } : null));
          }
        }
        setTicketFinding(null);
      }}
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
        {findingPanel}
        {dismissal.modal}
      </Box>
    );
  }

  return (
    <Box sx={{ px: hidePageInset ? 0 : ds.space[5], pb: ds.space[5] }}>
      <ListingLayout id='vm-vulnerabilities'>
        {/* The grouping selector sits in the toolbar, as a ToggleGroup, because
            Image Scan's Apps/Images/CVE/Details is the same control over the
            same kind of data and that is where it lives. It used to be an outer
            Tabs bar above the card, which put a second full-width strip
            directly under the Security sub-tab strip. */}
        <ListingLayout.Toolbar
          actions={
            <>
              {/* No size prop, matching Image Scan's — both take the 'md' default. */}
              <ToggleGroup
                id='vm-vulnerability-grouping'
                selection='single'
                value={grouping}
                onChange={(next: VmVulnerabilityGrouping | 'all') => {
                  setPage(0);
                  setGrouping(next);
                }}
                options={GROUP_TABS.map((t) => ({ value: t.value, label: t.text }))}
                ariaLabel='Group vulnerabilities by'
              />
              <DownloadButton id={`${tableId}-download`} onClick={() => ({ tableId })} />
            </>
          }
        >
          {leadingFilters}
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
          {!embedded && (
            <FilterDropdown
              id='vm-vulnerability-status'
              label='Status'
              value={statusFilter}
              options={STATUS_OPTIONS}
              clearable={false}
              onSelect={(event: any) => {
                setPage(0);
                setStatusFilter(event?.target?.value || 'Open');
              }}
            />
          )}
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
      {findingPanel}
      {dismissal.modal}
    </Box>
  );
};

export default VmVulnerabilities;
