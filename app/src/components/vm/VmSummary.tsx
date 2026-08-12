import React, { useCallback, useEffect, useState } from 'react';
import { useRouter } from 'next/router';
import { Box, Typography } from '@mui/material';
import Stat from '@ui/Stat';
import Card from '@ui/Card';
import SeverityIcon from '@ui/SeverityIcon';
import Datetime from '@shared/format/Datetime';
import { toSeverityLevel } from '@utils/common';
import apiVm, { SEVERITY_ORDER, VmVulnerabilityGroup } from '@api1/vm';
import { sumSeverities, useLatestRequest } from './common';
import { ds } from '@utils/colors';

interface VmSummaryState {
  vms: number;
  packages: number;
  scannedVms: number;
  agentsConnected: number;
  agentsTotal: number;
  lastScanAt: string;
  severities: Record<string, number>;
  topVms: VmVulnerabilityGroup[];
  topPackages: VmVulnerabilityGroup[];
  topVulnerabilities: VmVulnerabilityGroup[];
  operatingSystems: Array<{ label: string; count: number }>;
}

const EMPTY: VmSummaryState = {
  vms: 0,
  packages: 0,
  scannedVms: 0,
  agentsConnected: 0,
  agentsTotal: 0,
  lastScanAt: '',
  severities: {},
  topVms: [],
  topPackages: [],
  topVulnerabilities: [],
  operatingSystems: [],
};

/** How many rows each of the "top N" cards shows. */
const TOP_N = 5;

/** Reverse of the severity_weight ranking the grouping query aggregates with. */
const WEIGHT_SEVERITY: Array<[number, string]> = [
  [10, 'Critical'],
  [8, 'High'],
  [5, 'Medium'],
  [2, 'Low'],
];
const severityFromWeight = (weight: number) => WEIGHT_SEVERITY.find(([threshold]) => weight >= threshold)?.[1] || 'Info';

/**
 * One ranked row — optional severity badge, label, count.
 *
 * `bar` draws the proportional track, which is worth its width only where the
 * card is a distribution (the severity breakdown). The top-N cards leave it off:
 * their rows are already ordered by count, so the bar restates the number, and
 * on a single-row card it is a full-width rectangle that means nothing.
 */
const RankBar = ({
  label,
  count,
  max = 0,
  severity,
  mono,
  bar = false,
  onClick,
}: {
  label: string;
  count: number;
  max?: number;
  severity?: string;
  mono?: boolean;
  bar?: boolean;
  onClick?: () => void;
}) => (
  <Box
    onClick={onClick}
    onKeyDown={onClick ? (event) => ['Enter', ' '].includes(event.key) && (event.preventDefault(), onClick()) : undefined}
    role={onClick ? 'button' : undefined}
    tabIndex={onClick ? 0 : undefined}
    sx={{
      display: 'flex',
      alignItems: 'center',
      gap: ds.space[3],
      ...(onClick && {
        cursor: 'pointer',
        borderRadius: ds.radius.sm,
        mx: `calc(-1 * ${ds.space[2]})`,
        px: ds.space[2],
        py: ds.space[1],
        '&:hover': { backgroundColor: ds.gray[100] },
        '&:focus-visible': { outline: 'none', boxShadow: `0 0 0 ${ds.space[0]} ${ds.blue[100]}` },
      }),
    }}
  >
    <Box
      sx={{
        width: bar ? ds.space.mul(0, 90) : undefined,
        flex: bar ? undefined : 1,
        display: 'flex',
        alignItems: 'center',
        gap: ds.space[2],
        minWidth: 0,
      }}
    >
      {severity ? <SeverityIcon level={toSeverityLevel(severity)} size={14} aria-label={severity} /> : null}
      <Typography
        title={label}
        noWrap
        sx={{ fontSize: ds.text.small, color: ds.gray[700], fontFamily: mono ? ds.font.mono : undefined, minWidth: 0 }}
      >
        {label}
      </Typography>
    </Box>
    {bar ? (
      <Box sx={{ flex: 1, height: ds.space[2], borderRadius: ds.radius.sm, backgroundColor: ds.gray[100], overflow: 'hidden' }}>
        <Box sx={{ width: `${max ? Math.round((count / max) * 100) : 0}%`, height: '100%', backgroundColor: ds.gray[400] }} />
      </Box>
    ) : null}
    <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700], minWidth: ds.space[6], textAlign: 'right' }}>
      {count}
    </Typography>
  </Box>
);

const CardHeading = ({ text }: { text: string }) => (
  <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{text}</Typography>
);

/**
 * A ranked card — the shared shape of the four "what is worst / most common"
 * cards. Renders the empty line rather than an empty card so a fresh account
 * says why it is blank.
 */
const RankCard = ({
  heading,
  rows,
  loading,
  empty,
  mono,
}: {
  heading: string;
  rows: Array<{ id?: string; label: string; count: number; severity?: string; onClick?: () => void }>;
  loading: boolean;
  empty: string;
  mono?: boolean;
}) => (
  <Card size='md' header={<CardHeading text={heading} />}>
    {rows.length ? (
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
        {rows.map((row) => (
          <RankBar key={row.id || row.label} label={row.label} count={row.count} severity={row.severity} mono={mono} onClick={row.onClick} />
        ))}
      </Box>
    ) : (
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>{loading ? 'Loading…' : empty}</Typography>
    )}
  </Card>
);

/**
 * The Summary tab.
 *
 * Every number here comes from the same query the tab that owns it uses, so a
 * tile and its tab can never disagree. Nothing is computed server-side: there is
 * no coverage projection yet (that's the open half of the VM Discovery UI
 * ticket), so "scanned" here means "has package inventory", which is the closest
 * honest proxy the data supports today.
 */
const VmSummary = ({ accountId }: { accountId: string }) => {
  const router = useRouter();
  const [summary, setSummary] = useState<VmSummaryState>(EMPTY);
  const [loading, setLoading] = useState(true);
  const beginRequest = useLatestRequest();

  const fetchSummary = useCallback(async () => {
    if (!accountId) return;
    const isLatest = beginRequest();
    setLoading(true);
    try {
      // Every "top N" card is the same grouping query the Vulnerabilities tab's
      // matching tab runs, capped at TOP_N and without the group-count round
      // trip — a card and its tab can never disagree.
      const topGroups = (grouping: 'vm' | 'package' | 'vulnerability') =>
        apiVm.listVulnerabilityGroups({ accountId, grouping, limit: TOP_N, offset: 0, includeTotal: false, orderBy: 'count' });
      const [vms, packageCounts, severities, agents, topVms, topPackages, topVulnerabilities, osByVm] = await Promise.all([
        apiVm.listVms({ accountId, limit: 1, offset: 0 }),
        apiVm.getPackageCountsByVm(accountId),
        apiVm.getSeverityCounts({ accountId }),
        apiVm.listAgents(accountId),
        topGroups('vm'),
        topGroups('package'),
        topGroups('vulnerability'),
        apiVm.getOsByVm(accountId),
      ]);
      if (!isLatest()) return;
      const scanned = Object.values(packageCounts);
      const osCounts: Record<string, number> = {};
      for (const os of Object.values(osByVm)) {
        if (os) osCounts[os] = (osCounts[os] || 0) + 1;
      }
      setSummary({
        vms: vms.total,
        packages: scanned.reduce((total, entry) => total + (entry.count || 0), 0),
        scannedVms: scanned.length,
        agentsConnected: agents.filter((agent) => agent.connected).length,
        agentsTotal: agents.length,
        // The newest package row across the fleet — a scan writes them, so this
        // is when the inventory was last refreshed by any VM.
        lastScanAt: scanned.reduce((latest, entry) => (entry.lastSeenAt > latest ? entry.lastSeenAt : latest), ''),
        severities,
        topVms: topVms.rows,
        topPackages: topPackages.rows,
        topVulnerabilities: topVulnerabilities.rows,
        operatingSystems: Object.entries(osCounts)
          .map(([label, count]) => ({ label, count }))
          .sort((a, b) => b.count - a.count)
          .slice(0, TOP_N),
      });
    } catch (error) {
      if (!isLatest()) return;
      console.error('Failed to load VM summary:', error);
      setSummary(EMPTY);
    } finally {
      if (isLatest()) setLoading(false);
    }
  }, [accountId, beginRequest]);

  useEffect(() => {
    fetchSummary();
  }, [fetchSummary]);

  const presentSeverities = SEVERITY_ORDER.filter((severity) => (summary.severities[severity] || 0) > 0);
  const maxSeverityCount = Math.max(1, ...presentSeverities.map((severity) => summary.severities[severity] || 0));
  const openFindings = sumSeverities(summary.severities);
  // A VM with no package inventory has never completed a scan, so nothing is
  // known about what it runs — the number worth surfacing, not a percentage.
  const neverScanned = Math.max(0, summary.vms - summary.scannedVms);

  // Every card links into the tab that owns its numbers, carrying the filter that
  // reproduces the row. Only one scope can be active, so any previous one is
  // dropped rather than stacked.
  const openTab = (fragment: string, params: Record<string, string> = {}) => {
    const query: Record<string, any> = { ...router.query };
    ['vmId', 'packageName', 'vulnId', 'severity'].forEach((key) => delete query[key]);
    router.push({ pathname: '/vm', query: { ...query, ...params }, hash: fragment });
  };

  const toRankRows = (groups: VmVulnerabilityGroup[], param: 'vmId' | 'packageName' | 'vulnId') =>
    groups.map((group) => ({
      id: group.key,
      label: group.label || '—',
      count: group.count,
      severity: severityFromWeight(group.max_severity_weight),
      onClick: group.key ? () => openTab('vulnerabilities', { [param]: group.key }) : undefined,
    }));

  return (
    <Box sx={{ px: ds.space[5], pb: ds.space[5], display: 'flex', flexDirection: 'column', gap: ds.space[5] }}>
      <Card size='md'>
        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[7] }}>
          <Stat id='vm-summary-vms' label='Virtual Machines' value={summary.vms} size='md' onClick={() => openTab('instances')} />
          <Stat
            id='vm-summary-agents'
            label='Proxy Agents Connected'
            value={`${summary.agentsConnected} / ${summary.agentsTotal}`}
            size='md'
            onClick={() => router.push({ pathname: '/agentHealth', query: { accountId }, hash: 'proxy-agent' })}
          />
          <Stat id='vm-summary-packages' label='Packages Tracked' value={summary.packages} size='md' onClick={() => openTab('packages')} />
          <Stat
            id='vm-summary-vulnerabilities'
            label='Open Vulnerabilities'
            value={openFindings}
            size='md'
            onClick={() => openTab('vulnerabilities')}
          />
          <Stat
            id='vm-summary-last-scan'
            label='Last Scan'
            value={summary.lastScanAt ? <Datetime value={summary.lastScanAt} /> : '—'}
            size='md'
            sub='most recent inventory'
          />
        </Box>
      </Card>

      <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '1fr 1fr' }, gap: ds.space[5] }}>
        <Card size='md' header={<CardHeading text='Vulnerabilities by severity' />}>
          {presentSeverities.length ? (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
              {presentSeverities.map((severity) => (
                <RankBar
                  key={severity}
                  label={severity}
                  severity={severity}
                  count={summary.severities[severity] || 0}
                  max={maxSeverityCount}
                  bar
                  onClick={() => openTab('vulnerabilities', { severity })}
                />
              ))}
            </Box>
          ) : (
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
              {loading ? 'Loading…' : 'No open findings. Scan a VM to match its packages against the CVE database.'}
            </Typography>
          )}
        </Card>

        <Card size='md' header={<CardHeading text='Scan coverage' />}>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[7] }}>
            <Stat id='vm-summary-scanned' label='Inventoried' value={summary.scannedVms} size='md' sub='has package inventory' />
            <Stat id='vm-summary-never-scanned' label='Never scanned' value={neverScanned} size='md' sub='no inventory collected yet' />
          </Box>
        </Card>

        <RankCard heading='Most vulnerable VMs' rows={toRankRows(summary.topVms, 'vmId')} loading={loading} empty='No open findings on any VM yet.' />

        <RankCard
          heading='Most affected packages'
          rows={toRankRows(summary.topPackages, 'packageName')}
          loading={loading}
          empty='No package has an open finding yet.'
        />

        <RankCard
          heading='Most common vulnerabilities'
          rows={toRankRows(summary.topVulnerabilities, 'vulnId')}
          loading={loading}
          empty='No CVE has matched an installed package yet.'
          mono
        />

        <RankCard
          heading='Operating systems'
          rows={summary.operatingSystems}
          loading={loading}
          empty='No OS reported yet — it is collected with the package inventory.'
        />
      </Box>
    </Box>
  );
};

export default VmSummary;
