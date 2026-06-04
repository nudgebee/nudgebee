import { Box, Grid, Typography } from '@mui/material';
import React, { useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/router';
import homeApi from '@api1/home';
import { v4 as uuidv4 } from 'uuid';
import apiAskNudgebee from '@api1/ask-nudgebee';
import QuickLink from '@assets/home/new/quick-link.icon.svg';
import WorkflowIconBlue from '@assets/workflow/workflow-icon-blue.icon.svg';
import RecentErrorIcon from '@assets/home/new/recent-error.icon.svg';
import MatricsIcon from '@assets/home/new/metrics_icon.icon.svg';
import PodsIcon from '@assets/home/new/pods_icon.icon.svg';
import ServiceMapsIcon from '@assets/home/new/service_maps_icon.icon.svg';
import PvcSightSizing from '@assets/kubernetes/optimize-icons/pv-right-sizing.icon.svg';
import TroubleshootIconBlue from '@assets/header/TroubleshootIconBlue.icon.svg';
import OptimizeIconBlue from '@assets/header/optimize-blue.icon.svg';
import OptimizeGaugeIcon from '@assets/home/optimize-icon.svg';
import { getBrandingAsset, getNubiIconUrl } from '@hooks/useTenantBranding';
import DataBaseBlueIcon from '@assets/kubernetes/app-nodes-icons/database-blue.icon.svg';
import SirenBlueIcon from '@assets/home/new/siren-rounded-blue.icon.svg';
import TicketBlueIcon from '@assets/home/new/ticket-blue.icon.svg';
import RepoBlueIcon from '@assets/home/new/repo-forked-blue.icon.svg';
import SlackIcon from '@assets/slack_icon.icon.svg';
import MsTeamsIcon from '@assets/ou-management/ms_teams.icon.svg';
import GChatIcon from '@assets/gchat-icon.icon.svg';
import PagerDutyIcon from '@assets/auto-pilot/pager-duty.svg';
import ServiceNowIcon from '@assets/servicenow.icon.svg';
import JiraIcon from '@assets/jira_icon.icon.svg';
import GithubIcon from '@assets/github-icon.icon.svg';
import LogsIcon from '@assets/home/logs-icon.icon.svg';
import TraceIcon from '@assets/home/traces-icon.icon.svg';
import NamespacesIcon from '@assets/kubernetes/app-nodes-icons/namespace-icon.icon.svg';
import SecurityIcon from '@assets/home/security-icon.icon.svg';
import AWSEC2Icon from '@assets/cloud-account/ec2-icon.icon.svg';
import AWSRDSIcon from '@assets/cloud-account/rds-icon.icon.svg';
import AWSS3Icon from '@assets/cloud-account/s3-icon.icon.svg';
import AWSECSIcon from '@assets/cloud-account/ecs-icon.icon.svg';
import AzureVMIcon from '@assets/cloud-account/azure-vm.icon.svg';
import AzureSqlIcon from '@assets/cloud-account/azure-sql.icon.svg';
import AzureBlobIcon from '@assets/cloud-account/azure-blob.icon.svg';
import GCPComputeEngineIcon from '@assets/cloud-account/gcp-compute-engine.icon.svg';
import GCPCloudSQLIcon from '@assets/cloud-account/gcp-cloud-sql.icon.svg';
import GCPCloudStorageIcon from '@assets/cloud-account/gcp-cloud-storage.icon.svg';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import Link from 'next/link';
import apiWorkflow from '@api1/workflow';
import { getLast24Hrs } from '@lib/datetime';
import DSCard from '@components1/ds/Card';
import CollapsableCard from '@components1/ds/CollapsableCard';
import { Skeleton } from '@components1/ds/Skeleton';
import { Chip } from '@components1/ds/Chip';
import { Button } from '@components1/ds/Button';
import { Stat } from '@components1/ds/Stat';
import SearchIcon from '@mui/icons-material/Search';
import KeyboardArrowRightIcon from '@mui/icons-material/KeyboardArrowRight';
import BoltIcon from '@mui/icons-material/Bolt';
import ShieldOutlinedIcon from '@mui/icons-material/ShieldOutlined';
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutline';
import SettingsOutlinedIcon from '@mui/icons-material/SettingsOutlined';
import BuildOutlinedIcon from '@mui/icons-material/BuildOutlined';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import SystemUpdateAltIcon from '@mui/icons-material/SystemUpdateAlt';
import NotificationsActiveOutlinedIcon from '@mui/icons-material/NotificationsActiveOutlined';
import DescriptionOutlinedIcon from '@mui/icons-material/DescriptionOutlined';
import BugReportOutlinedIcon from '@mui/icons-material/BugReportOutlined';
import AccessTimeOutlinedIcon from '@mui/icons-material/AccessTimeOutlined';
import EqualizerOutlinedIcon from '@mui/icons-material/EqualizerOutlined';
import VolumeUpOutlinedIcon from '@mui/icons-material/VolumeUpOutlined';
import TrendingUpOutlinedIcon from '@mui/icons-material/TrendingUpOutlined';
import UnfoldMoreOutlinedIcon from '@mui/icons-material/UnfoldMoreOutlined';
import StorageOutlinedIcon from '@mui/icons-material/StorageOutlined';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import GppMaybeOutlinedIcon from '@mui/icons-material/GppMaybeOutlined';
import ImageOutlinedIcon from '@mui/icons-material/ImageOutlined';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import { useData } from '@context/DataContext';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import { Textarea } from '@components1/k8s/common/TextArea';
import K8sAccountModal from '@components1/common/K8sAccountModal';
import SafeIcon from '@components1/common/SafeIcon';
import { getUserSession } from '@lib/auth';
import { FiArrowRight } from 'react-icons/fi';
import useCurrencySymbol from '@hooks/useCurrencySymbol';
import PendingFollowUps from '@components1/home/PendingFollowUps';

const replaceCurrencyInText = (text, targetCurrencySymbol) => {
  if (!text || targetCurrencySymbol === '$') return text;
  return text.replace(/\$(\d[\d,]*\.?\d*)/g, `${targetCurrencySymbol}$1`);
};

const FILTER_COLUMN_TO_PARAM = {
  status: 'status',
  eventstatus: 'eventStatus',
  category: 'category',
  severity: 'severity',
  rule_name: 'rule_name',
  source: 'source',
  aggregation_key: 'aggregation_key',
  subject_name: 'subject_name',
};

const applyFiltersToLink = (baseLink, filters) => {
  if (!baseLink || !filters || filters.length === 0) return baseLink;
  const hashIndex = baseLink.indexOf('#');
  const [pathAndQuery, hash] = hashIndex >= 0 ? [baseLink.slice(0, hashIndex), baseLink.slice(hashIndex)] : [baseLink, ''];
  let result = pathAndQuery;
  for (const filter of filters) {
    if (!filter?.value || (Array.isArray(filter?.value) && filter?.value.length === 0)) {
      continue;
    }
    const param = FILTER_COLUMN_TO_PARAM[filter.column?.toLowerCase()];
    if (!param) continue;
    const paramRegex = new RegExp(`[?&]${param}=`);
    if (paramRegex.test(result)) continue;
    const value = Array.isArray(filter.value) ? filter.value.join(',') : filter.value;
    const separator = result.includes('?') ? '&' : '?';
    result = `${result}${separator}${param}=${value}`;
  }
  return `${result}${hash}`;
};

// Extracts named placeholder values from a title using an insight_format template.
// e.g. format = "Most frequent issue: {aggregation_key} ({} FIRING events)"
//      title  = "Most frequent issue: RabbitmqUnroutableMessages (2058 FIRING events)"
//      returns { aggregation_key: "RabbitmqUnroutableMessages" }
//
// Named placeholders  {key} → regex named capture group (?<key>.+?)
// Unnamed placeholders {}   → non-capturing group        .+?   (value discarded)
const extractFromFormat = (format, title) => {
  if (!format || !title) return {};

  const placeholderRegex = /\{(\w*)\}/g;
  let match;
  const placeholders = [];
  while ((match = placeholderRegex.exec(format)) !== null) {
    placeholders.push({ name: match[1], index: match.index, length: match[0].length });
  }
  if (placeholders.length === 0) return {};

  let regexStr = '^';
  let lastIndex = 0;
  for (const ph of placeholders) {
    regexStr += format.slice(lastIndex, ph.index).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    regexStr += ph.name ? `(?<${ph.name}>.+?)` : '.+?';
    lastIndex = ph.index + ph.length;
  }
  regexStr += format.slice(lastIndex).replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '$';

  try {
    return new RegExp(regexStr).exec(title)?.groups ?? {};
  } catch {
    return {};
  }
};

const specialUniqueIdsForApplyFilter = {
  129: {
    keys: ['aggregation_key'],
    defaultFilters: [
      {
        column: 'eventStatus',
        value: 'FIRING',
      },
    ],
  },
  127: {
    keys: ['subject_name'],
    defaultFilters: null,
  },
  17: {
    keys: [],
    defaultFilters: [
      {
        column: 'severity',
        value: ['Critical', 'High'],
      },
      {
        column: 'status',
        value: 'Open',
      },
    ],
  },
  114: {
    keys: [],
    defaultFilters: [
      {
        column: 'severity',
        value: ['Low', 'Medium'],
      },
    ],
  },
};

// ─── Module-level helpers ─────────────────────────────────────────────────

const getApplicationLink = (rule, workloadFqdn) => {
  if (!rule?.redirect_url) return null;
  if (!workloadFqdn) return rule.redirect_url;
  const [workloadName, namespaceName] = workloadFqdn.split(':');
  if (!workloadName || !namespaceName) return rule.redirect_url;
  const url = new URL(rule.redirect_url, 'http://placeholder');
  if (rule.subcategory === 'Events') {
    url.searchParams.set('eventSubjectName', workloadName);
    url.searchParams.set('eventNamespace', namespaceName);
  } else if (rule.subcategory === 'LogGroup') {
    url.searchParams.set('workloadNamespace', namespaceName);
    url.searchParams.set('workloadName', workloadName);
  } else {
    url.searchParams.set('destinationWorkload', workloadName);
    url.searchParams.set('destinationNamespace', namespaceName);
  }
  return `${url.pathname}?${url.searchParams.toString()}${url.hash}`;
};

const buildInsightLink = (item) => {
  let link = item?.rule?.redirect_url || null;
  if (link && item.rule?.filters) {
    link = applyFiltersToLink(link, item.rule.filters);
  }
  if (link && specialUniqueIdsForApplyFilter[item?.rule?.unique_id]) {
    const { keys, defaultFilters } = specialUniqueIdsForApplyFilter[item.rule.unique_id];
    if (defaultFilters) link = applyFiltersToLink(link, defaultFilters);
    const extracted = extractFromFormat(item.rule.insight_format, item.title);
    const extractedFilters = keys.filter((key) => extracted[key]).map((key) => ({ column: key, value: extracted[key] }));
    link = applyFiltersToLink(link, extractedFilters);
  }
  return link;
};

const getInsightSeverity = (item) => {
  const cat = item?.rule?.category || item?.type;
  const source = item?.rule?.source;
  if (source === 'Event') return 'critical';
  switch (cat) {
    case 'Troubleshooting':
      return 'critical';
    case 'Performance':
    case 'Security':
      return 'high';
    case 'Configuration':
    case 'Ops':
    case 'InfraUpgrade':
      return 'medium';
    case 'Optimization':
    case 'Cost':
      return 'low';
    default:
      return 'info';
  }
};

const getActionLabel = (item) => {
  const cat = item?.rule?.category || item?.type;
  switch (cat) {
    case 'Troubleshooting':
    case 'Performance':
      return 'Investigate';
    case 'Security':
      return 'Secure Now';
    case 'Configuration':
      return 'Configure';
    case 'Ops':
      return 'View Details';
    case 'Optimization':
    case 'Cost':
      return 'Optimize';
    case 'InfraUpgrade':
      return 'Upgrade';
    default:
      return 'View Details';
  }
};

// Patterns that mark the "important" sub-spans inside an insight title — numbers,
// thresholds, change deltas, dollar amounts. Only these get heavier weight; the
// surrounding prose stays at the default so the metric reads first.
const HIGHLIGHT_PATTERNS = [
  // Parenthesized threshold/value: (>50%), (>5000ms), (3.4s)
  /\([^)]*\d[^)]*\)/g,
  // Change phrases: "increased by 18599", "decreased by 12%"
  /(?:increased|decreased|jumped|spiked|grew|dropped|fell|rose)\s+by\s+[\d,.]+%?/gi,
  // Number + optional Capitalized descriptor(s) + unit: "2 pods", "12388 events",
  // "1 OOMKill events", "228 PagerDuty incidents", "5 certs", "30 days".
  // Descriptors must be Capitalized so we don't sweep generic prose
  // ("1 cluster has 5 pods" → only "5 pods" matches, not the whole span).
  /\b\d[\d,.]*(?:\s+[A-Z][\w-]*){0,3}\s+(?:pods?|events?|nodes?|containers?|services?|requests?|errors?|alerts?|issues?|incidents?|cves?|certificates?|certs?|workloads?|namespaces?|users?|messages?|times?|hours?|minutes?|seconds?|days?|weeks?|months?|gb|mb|kb|tb)\b/gi,
  // Dollar amounts: $123, $1.2k, $500/mo
  /\$[\d,.]+[kKmMbB]?(?:\/(?:mo|month|yr|year|hr|hour))?/g,
];

const splitTitleByHighlights = (title) => {
  if (!title) return [{ text: '', bold: false }];
  const ranges = [];
  for (const pattern of HIGHLIGHT_PATTERNS) {
    const re = new RegExp(pattern.source, pattern.flags);
    let m;
    while ((m = re.exec(title)) !== null) {
      if (m[0].length > 0) ranges.push({ start: m.index, end: m.index + m[0].length });
    }
  }
  ranges.sort((a, b) => a.start - b.start);
  const merged = [];
  for (const r of ranges) {
    const last = merged[merged.length - 1];
    if (last && r.start <= last.end) last.end = Math.max(last.end, r.end);
    else merged.push({ ...r });
  }
  const segments = [];
  let cursor = 0;
  for (const r of merged) {
    if (cursor < r.start) segments.push({ text: title.slice(cursor, r.start), bold: false });
    segments.push({ text: title.slice(r.start, r.end), bold: true });
    cursor = r.end;
  }
  if (cursor < title.length) segments.push({ text: title.slice(cursor), bold: false });
  return segments.length ? segments : [{ text: title, bold: false }];
};

// Inline agent-sparkle micro-icon — used in row action buttons.
// Not in DS yet; flag as DS follow-up to add to IconRegistry.
// `.agent-sparkle` className lets the row container drive the hover animation
// (keyframes defined on InsightRow's root Box).
const AgentSparkleIcon = (props) => (
  <Box
    component='svg'
    viewBox='0 0 16 16'
    fill='currentColor'
    aria-hidden
    {...props}
    className={['agent-sparkle', props?.className].filter(Boolean).join(' ')}
    style={{ width: 10, height: 10, flexShrink: 0, transformOrigin: 'center', ...(props?.style || {}) }}
  >
    <path d='M8 0C8.5 4 12 7.5 16 8C12 8.5 8.5 12 8 16C7.5 12 4 8.5 0 8C4 7.5 7.5 4 8 0Z' />
  </Box>
);

// Severity colour ramp for the row-icon tinted square.
const SEVERITY_PALETTE = {
  critical: { bg: 'var(--ds-red-100)', fg: 'var(--ds-red-500)' },
  high: { bg: 'var(--ds-amber-100)', fg: 'var(--ds-amber-500)' },
  medium: { bg: 'var(--ds-blue-100)', fg: 'var(--ds-blue-500)' },
  low: { bg: 'var(--ds-green-100)', fg: 'var(--ds-green-500)' },
  info: { bg: 'var(--ds-gray-100)', fg: 'var(--ds-gray-500)' },
};

// Per-insight icon: pattern-match against title + source for a specific glyph
// (PagerDuty bell, log document, latency clock, trend arrow, etc.).
// Falls back to a category-level icon when no specific pattern matches.
const getInsightIcon = (item) => {
  const title = (item?.title || '').toLowerCase();
  const source = item?.rule?.source || '';
  const cat = item?.rule?.category || item?.type;

  // Source-specific (most specific)
  if (source === 'PagerDuty' || /pagerduty|incidents? are firing/i.test(title)) {
    return NotificationsActiveOutlinedIcon;
  }

  // Title-pattern matching (specific → general)
  if (/imagepullbackoff|image pull|crashloop/i.test(title)) return ImageOutlinedIcon;
  if (/log error|log group|frequent log/i.test(title)) return DescriptionOutlinedIcon;
  if (/api error|error rate/i.test(title)) return BugReportOutlinedIcon;
  if (/latency|response time/i.test(title)) return AccessTimeOutlinedIcon;
  if (/most frequent|top issue|frequent issue/i.test(title)) return EqualizerOutlinedIcon;
  if (/noisiest|noisy|noise|events this week/i.test(title)) return VolumeUpOutlinedIcon;
  if (/increased by|trend|trending|decreased by|volume.*last week/i.test(title)) return TrendingUpOutlinedIcon;

  // Optimisation patterns
  if (/right.?siz|vertical|horizontal/i.test(title)) return UnfoldMoreOutlinedIcon;
  if (/persistent volume|\bpvc?\b|storage|volume.*right|unused volume/i.test(title)) return StorageOutlinedIcon;

  // Security patterns
  if (/certificate|\bssl\b|cert.*expir/i.test(title)) return LockOutlinedIcon;
  if (/cve|vulnerab/i.test(title)) return GppMaybeOutlinedIcon;
  if (/image scan|critical image/i.test(title)) return ImageOutlinedIcon;

  // Category fallbacks
  switch (cat) {
    case 'Troubleshooting':
    case 'Performance':
      return ErrorOutlineIcon;
    case 'Security':
      return ShieldOutlinedIcon;
    case 'Optimization':
    case 'Cost':
      return BoltIcon;
    case 'Configuration':
      return SettingsOutlinedIcon;
    case 'Ops':
      return BuildOutlinedIcon;
    case 'InfraUpgrade':
      return SystemUpdateAltIcon;
    default:
      return InfoOutlinedIcon;
  }
};

// InsightIcon — severity-tinted square containing a category-specific MUI icon.
// Replaces SeverityIcon for row markers per design: category-as-icon, severity-as-color.
const InsightIcon = ({ severity, icon: IconCmp }) => {
  const palette = SEVERITY_PALETTE[severity] || SEVERITY_PALETTE.info;
  return (
    <Box
      sx={{
        width: 24,
        height: 24,
        borderRadius: 'var(--ds-radius-sm)',
        backgroundColor: palette.bg,
        color: palette.fg,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        flexShrink: 0,
      }}
    >
      {IconCmp ? <IconCmp sx={{ fontSize: 14 }} /> : null}
    </Box>
  );
};
InsightIcon.propTypes = {
  severity: PropTypes.oneOf(['critical', 'high', 'medium', 'low', 'info']),
  icon: PropTypes.any,
};

// ─── Sub-components ───────────────────────────────────────────────────────

const ServiceChipRow = ({ applications, rule, accountId, maxShown = 4 }) => {
  const router = useRouter();
  if (!applications || applications === 'null') return null;
  let apps = applications;
  if (typeof apps === 'string') {
    try {
      apps = JSON.parse(apps);
    } catch {
      return null;
    }
  }
  if (!Array.isArray(apps) || apps.length === 0) return null;
  const shown = apps.slice(0, maxShown);
  const overflow = apps.length - maxShown;
  return (
    // Stop chip clicks from bubbling to the parent insight row, whose onClick
    // window.opens the rule's redirect_url (the events page for Event-source
    // insights). Without this, clicking an event_id chip both navigates to
    // /investigate (chip onClick) and opens the events page (row onClick).
    <Box onClick={(e) => e.stopPropagation()} sx={{ display: 'inline-flex', flexWrap: 'wrap', gap: 'var(--ds-space-1)', alignItems: 'center' }}>
      {shown.map((app) => {
        // Event-source insights carry an event_id per application — prefer routing to
        // the investigate page for that event; otherwise fall back to the rule link.
        const investigateId = app.event_id && accountId ? app.event_id : null;
        const href = investigateId ? null : getApplicationLink(rule, `${app.name}:${app.namespace}`);
        const onClick = investigateId
          ? () => router.push(`/investigate?id=${investigateId}&accountId=${accountId}`)
          : href
          ? () => window.open(href, '_blank')
          : undefined;
        return (
          <Chip
            key={`${app.name}:${app.namespace}`}
            variant='tag'
            size='xs'
            tone='neutral'
            shape='pill'
            icon={<SearchIcon sx={{ fontSize: 10, color: 'var(--ds-gray-400)' }} />}
            onClick={onClick}
            sx={{
              backgroundColor: 'var(--ds-gray-50)',
              '&:hover': { backgroundColor: 'var(--ds-gray-100)', color: 'var(--ds-gray-700)' },
            }}
          >
            {app.name}
          </Chip>
        );
      })}
      {overflow > 0 &&
        (() => {
          const moreHref = rule?.redirect_url || null;
          return (
            <Chip
              variant='count'
              size='xs'
              tone='neutral'
              shape='pill'
              onClick={moreHref ? () => window.open(moreHref, '_blank') : undefined}
              sx={{
                color: 'var(--ds-gray-500)',
                backgroundColor: 'var(--ds-gray-50)',
                borderColor: 'var(--ds-gray-100)',
                fontWeight: 400,
                ...(moreHref && {
                  cursor: 'pointer',
                  '&:hover': { backgroundColor: 'var(--ds-gray-100)', color: 'var(--ds-gray-700)' },
                }),
              }}
            >
              +{overflow} more
            </Chip>
          );
        })()}
    </Box>
  );
};
ServiceChipRow.propTypes = {
  applications: PropTypes.any,
  rule: PropTypes.object,
  accountId: PropTypes.string,
  maxShown: PropTypes.number,
};

const InsightRow = ({ item = {}, type = '', currencySymbol = '$' }) => {
  const visible = type === 'troubleshooting' || type === 'optimization' || type === 'Ops';
  if (!visible) return null;
  const severity = getInsightSeverity(item);
  const CategoryIcon = getInsightIcon(item);
  const link = buildInsightLink(item);
  const label = getActionLabel(item);
  const apps = item.applications;
  const hasApps = apps && apps !== 'null' && (typeof apps === 'string' ? apps.length > 0 : apps.length > 0);
  const displayTitle = item.rawTitle ? replaceCurrencyInText(item.rawTitle, currencySymbol) : item.title;
  return (
    <Box
      onClick={link ? () => window.open(link, '_blank') : undefined}
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--ds-space-3)',
        py: 'var(--ds-space-2)',
        px: 'var(--ds-space-3)',
        mx: 'var(--ds-space-2)',
        borderRadius: 'var(--ds-radius-sm)',
        borderBottom: '1px solid var(--ds-gray-100)',
        cursor: link ? 'pointer' : 'default',
        transition: 'background-color 120ms ease',
        '&:last-child': { borderBottom: 'none' },
        '&:hover': link ? { backgroundColor: 'var(--ds-background-200)' } : undefined,
        '@keyframes agentSparkleSpin': {
          '0%': { transform: 'rotate(0deg) scale(1)' },
          '50%': { transform: 'rotate(90deg) scale(1.2)' },
          '100%': { transform: 'rotate(180deg) scale(1)' },
        },
        '&:hover .agent-sparkle': {
          animation: 'agentSparkleSpin 600ms ease-in-out',
        },
      }}
    >
      <Box sx={{ flexShrink: 0 }}>
        <InsightIcon severity={severity} icon={CategoryIcon} />
      </Box>
      <Box sx={{ flex: 1, minWidth: 0, display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
        <Typography component='span' sx={{ fontSize: '13px', lineHeight: 1.5, color: 'var(--ds-gray-700)', fontWeight: 400 }}>
          {splitTitleByHighlights(displayTitle).map((seg, i) => (
            <Box
              key={i}
              component='span'
              sx={{
                fontWeight: seg.bold ? 500 : 400,
                color: seg.bold ? 'var(--ds-gray-900)' : 'inherit',
              }}
            >
              {seg.text}
            </Box>
          ))}
          {hasApps && (
            <Box component='span' sx={{ color: 'var(--ds-gray-500)' }}>
              {' '}
              detected for
            </Box>
          )}
        </Typography>
        {hasApps && <ServiceChipRow applications={apps} rule={item.rule} accountId={item.account_id} />}
      </Box>
      {link && (
        <Box sx={{ flexShrink: 0 }} onClick={(e) => e.stopPropagation()}>
          <Button tone='ghost' size='xs' icon={<AgentSparkleIcon />} iconPlacement='start' onClick={() => window.open(link, '_blank')}>
            {label}
          </Button>
        </Box>
      )}
    </Box>
  );
};
InsightRow.propTypes = {
  item: PropTypes.object,
  type: PropTypes.string,
  currencySymbol: PropTypes.string,
};

const SECTION_TONE = {
  critical: { bg: 'var(--ds-red-100)', color: 'var(--ds-red-600)' },
  info: { bg: 'var(--ds-blue-100)', color: 'var(--ds-blue-600)' },
  warning: { bg: 'var(--ds-amber-100)', color: 'var(--ds-amber-600)' },
  success: { bg: 'var(--ds-green-100)', color: 'var(--ds-green-600)' },
  agent: { bg: 'var(--ds-violet-100, #EDE9FE)', color: 'var(--ds-violet-600, #7C3AED)' },
  neutral: { bg: 'var(--ds-gray-100)', color: 'var(--ds-gray-700)' },
};

const SectionHeader = ({ icon, title, subtitle, tone = 'neutral' }) => {
  const palette = SECTION_TONE[tone] || SECTION_TONE.neutral;
  // Detect MUI icon component vs SVG asset path (Next imports SVGs as `{ src: ..., ... }` objects).
  const isMuiComponent = typeof icon === 'function';
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)', minWidth: 0 }}>
      <Box
        sx={{
          width: 36,
          height: 36,
          borderRadius: 'var(--ds-radius-md)',
          backgroundColor: palette.bg,
          color: palette.color,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          flexShrink: 0,
        }}
      >
        {icon ? isMuiComponent ? React.createElement(icon, { sx: { fontSize: 18 } }) : <SafeIcon src={icon} alt='' width={18} height={18} /> : null}
      </Box>
      <Box sx={{ minWidth: 0 }}>
        <Typography
          sx={{
            fontFamily: 'Poppins, sans-serif',
            fontSize: '14px',
            fontWeight: 600,
            color: 'var(--ds-gray-900)',
            lineHeight: 1.3,
            letterSpacing: '-0.01em',
          }}
        >
          {title}
        </Typography>
        {subtitle && (
          <Typography sx={{ fontFamily: 'Poppins, sans-serif', fontSize: '11px', color: 'var(--ds-gray-500)', mt: '1px' }}>{subtitle}</Typography>
        )}
      </Box>
    </Box>
  );
};
SectionHeader.propTypes = {
  icon: PropTypes.any,
  title: PropTypes.string,
  subtitle: PropTypes.node,
  tone: PropTypes.oneOf(['critical', 'info', 'warning', 'success', 'agent', 'neutral']),
};

const AutomationsCard = ({ workflowData, accountId, onManage }) => {
  const { totalCount = 0, actionedCount = 0, configuredCount = 0 } = workflowData || {};

  // Mirror the legacy behaviour: hide the card entirely when the cluster has no
  // workflows / no recent executions. Showing "0 / 0 / 0" looks like the fetch
  // failed, even when it succeeded with empty data.
  if (totalCount === 0 && actionedCount === 0 && configuredCount === 0) {
    return null;
  }

  const header = (
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0 }}>
        <Box
          sx={{
            width: 28,
            height: 28,
            borderRadius: 'var(--ds-radius-md)',
            backgroundColor: 'var(--ds-green-100)',
            color: 'var(--ds-green-600)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexShrink: 0,
          }}
        >
          <SafeIcon src={WorkflowIconBlue} alt='Automations' width={16} height={16} />
        </Box>
        <Typography
          sx={{
            fontFamily: 'Poppins, sans-serif',
            fontSize: '13px',
            fontWeight: 600,
            color: 'var(--ds-gray-900)',
            letterSpacing: '-0.01em',
          }}
        >
          Automations
        </Typography>
      </Box>
      <Button tone='ghost' size='xs' icon={<KeyboardArrowRightIcon />} iconPlacement='end' onClick={() => onManage?.(accountId)}>
        Manage
      </Button>
    </Box>
  );

  // Pre-format values as strings so Stat doesn't render '-' for 0 (formatNumber treats 0 as falsy).
  return (
    <DSCard size='sm' elevation='flat' header={header} sx={{ overflow: 'hidden' }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: 'var(--ds-space-3)' }}>
        <Stat size='sm' align='center' label='Configured' value={String(totalCount)} />
        <Stat size='sm' align='center' label='Triggered (24h)' value={String(actionedCount)} />
        <Stat size='sm' align='center' label='Event-based' value={String(configuredCount)} />
      </Box>
    </DSCard>
  );
};
AutomationsCard.propTypes = {
  workflowData: PropTypes.object,
  accountId: PropTypes.string,
  onManage: PropTypes.func,
};

const renderContent = (title, accountId, cloudProvider) => {
  const isK8s = cloudProvider === 'K8s';
  const detailsBase = isK8s ? '/kubernetes/details' : '/cloud-account/details';
  const getLink = (type) => {
    if (type == 'workflow') {
      return `/auto-pilot?${accountId}#workflow`;
    } else if (type == 'image-scan') {
      return `/kubernetes/details/${accountId}#security/image-scan`;
    } else if (type == 'certificate') {
      return `/kubernetes/details/${accountId}#security/ssl-certificate-issues`;
    } else if (type == 'upgrade') {
      return `/kubernetes/details/${accountId}#security/cluster-upgrade`;
    }
  };

  switch (title) {
    case 'Troubleshoot':
      return (
        <Box maxWidth={'90%'} mx={'auto'} py={2}>
          <Grid container spacing={1}>
            <Grid item xs={3}>
              <SafeIcon src={getBrandingAsset('troubleshootBee')} alt='Bee with magnifying glass' width={180} height={180} />
            </Grid>
            <Grid item xs={9} sx={{ display: 'flex', alignItems: 'center' }}>
              <Box>
                <Typography
                  sx={{
                    mb: 1,
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    lineHeight: 'var(--ds-text-title-lh)',
                    color: 'var(--ds-gray-700)',
                  }}
                >
                  Just added this account? Awesome! Give me about an hour to generate insights.
                </Typography>
                <Box
                  sx={{
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    color: 'var(--ds-gray-600)',
                    lineHeight: '28px',
                  }}
                >
                  While you wait, you can explore your{' '}
                  <Button tone='secondary' size='xs' href={`${detailsBase}/${accountId}#summary`} trailingAccent={<ArrowForwardIcon />}>
                    Cluster
                  </Button>{' '}
                  or check out the{' '}
                  <Button tone='secondary' size='xs' href={`/troubleshoot?accountId=${accountId}`} trailingAccent={<ArrowForwardIcon />}>
                    Troubleshooting
                  </Button>{' '}
                  for specific issues.
                </Box>
              </Box>
            </Grid>
          </Grid>
        </Box>
      );

    case 'Optimize':
      return (
        <Box maxWidth={'90%'} mx={'auto'} py={2}>
          <Grid container spacing={1}>
            <Grid item xs={3} sx={{ '@media (max-width: 1200px)': { pl: '0px !important', pr: '20px !important' } }}>
              <SafeIcon src={getBrandingAsset('optimizeBee')} alt='Bee with magnifying glass' width={180} height={180} />
            </Grid>
            <Grid item xs={9} sx={{ display: 'flex', alignItems: 'center' }}>
              <Box>
                <Typography
                  sx={{
                    mb: 1,
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    lineHeight: 'var(--ds-text-title-lh)',
                    color: 'var(--ds-gray-700)',
                  }}
                >
                  I can generate some quick optimization tips, but the best ones come from watching trends for a day or up to 7 days.
                </Typography>
                <Box
                  sx={{
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    color: 'var(--ds-gray-600)',
                    lineHeight: '28px',
                  }}
                >
                  In the meantime, check your{' '}
                  <Button tone='secondary' size='xs' href={`${detailsBase}/${accountId}#optimize/summary`} trailingAccent={<ArrowForwardIcon />}>
                    Cluster
                  </Button>{' '}
                  or check out the{' '}
                  <Button tone='secondary' size='xs' href={`/optimise?accountId=${accountId}`} trailingAccent={<ArrowForwardIcon />}>
                    Optimize
                  </Button>{' '}
                  section for plenty of options!
                </Box>
              </Box>
            </Grid>
          </Grid>
        </Box>
      );

    case 'K8s Ops Agent':
      return (
        <Box maxWidth={'90%'} mx={'auto'} py={2}>
          <Grid container spacing={2}>
            <Grid item xs={9}>
              <Typography sx={{ mb: 2, fontSize: '18px', fontweight: '400', lineHeight: '21.09px', color: colors.text.secondary }}>
                I can help with a bunch of things! Just tell me what you need
              </Typography>
              {[
                { label: 'Scan images for vulnerabilities', action: 'Start Scan', type: 'image-scan' },
                { label: 'Check certificate expire', action: 'View Status', type: 'certificate' },
                { label: 'Create automations', action: 'Create Now', type: 'workflow' },
                { label: "Upgrading K8s? Let's figure it out", action: 'Explore upgrade path', type: 'upgrade' },
              ].map((item, index) => (
                <Box key={index} sx={{ borderBottom: '0.5px dotted #D0D0D0', padding: '9px 0px 0px 0px', width: '60%' }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: '10px', mb: 1, flexWrap: 'wrap' }}>
                    <Typography sx={{ fontSize: '14px', fontweight: '400', lineHeight: '18px', color: '#1B2D4A' }}>{item.label}</Typography>
                    <Link href={getLink(item.type)}>
                      <CustomButton
                        sx={{
                          padding: '0px 8px !important',
                          fontSize: '12px',
                          color: '#1B2D4A',
                          backgroundColor: '#FFFFFF',
                          border: '0.5px solid #1B2D4A',
                          height: '22px',
                          alignItems: 'center',
                          gap: '4px',
                          minWidth: 'fit-content',
                          '& .MuiButton-endIcon svg,img': {
                            height: '14px',
                            width: '17px',
                            filter:
                              'brightness(0) saturate(100%) invert(14%) sepia(23%) saturate(1507%) hue-rotate(178deg) brightness(96%) contrast(92%)',
                          },
                          '&:hover': {
                            backgroundColor: '#FFFFFF',
                          },
                        }}
                        text={item.action}
                        endIcon={
                          <Box
                            sx={{
                              backgroundColor: '#FACF39',
                              borderRadius: '2px',
                              height: '14px',
                              display: 'flex',
                              justifyContent: 'center',
                              alignItems: 'center',
                            }}
                          >
                            <FiArrowRight />
                          </Box>
                        }
                      />
                    </Link>
                  </Box>
                </Box>
              ))}
            </Grid>
            <Grid item xs={3}>
              <SafeIcon src={getBrandingAsset('k8sBee')} alt='Bee with magnifying glass' width={150} height={152} />
            </Grid>
          </Grid>
        </Box>
      );

    case 'Security & Compliance':
      return (
        <Box maxWidth={'90%'} mx={'auto'} py={2}>
          <Grid container spacing={1}>
            <Grid item xs={3}>
              <SafeIcon src={getBrandingAsset('securityBee')} alt='Bee inspecting for security findings' width={180} height={180} />
            </Grid>
            <Grid item xs={9} sx={{ display: 'flex', alignItems: 'center' }}>
              <Box>
                <Typography
                  sx={{
                    mb: 1,
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    lineHeight: 'var(--ds-text-title-lh)',
                    color: 'var(--ds-gray-700)',
                  }}
                >
                  All quiet on the security front.
                </Typography>
                <Box
                  sx={{
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    color: 'var(--ds-gray-600)',
                    lineHeight: '28px',
                  }}
                >
                  I&apos;ll surface vulnerabilities, certificate alerts, and image scan findings here the moment they show up. In the meantime, you
                  can dig in from the{' '}
                  <Button tone='secondary' size='xs' href={`${detailsBase}/${accountId}#security/image-scan`} trailingAccent={<ArrowForwardIcon />}>
                    Security dashboard
                  </Button>{' '}
                  or kick off a fresh scan.
                </Box>
              </Box>
            </Grid>
          </Grid>
        </Box>
      );

    default:
      return <Box>Default content</Box>;
  }
};

const CardsBlock = ({
  icon,
  title,
  subtitle,
  tone = 'neutral',
  items = [],
  type = '',
  accountId = '',
  loadingInsights = false,
  hasExternalData = false,
  currencySymbol = '$',
  cloudProvider = '',
  meta = null,
  summary = null,
  footer = null,
  extra = null,
}) => {
  const generateRows = (rowItems) => {
    return rowItems.map((item) => <InsightRow item={item} key={item.id} type={type} currencySymbol={currencySymbol} />);
  };

  const renderSkeletonRows = (count = 4) =>
    Array.from({ length: count }).map((_, i) => (
      <Box
        key={`sk-${i}`}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-3)',
          py: 'var(--ds-space-2)',
          px: 'var(--ds-space-3)',
          mx: 'var(--ds-space-2)',
          borderBottom: '1px solid var(--ds-gray-100)',
          '&:last-child': { borderBottom: 'none' },
        }}
      >
        <Skeleton shape='rect' width={24} height={24} />
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Skeleton shape='text' size='text' width={`${60 + ((i * 7) % 30)}%`} />
        </Box>
      </Box>
    ));

  const hasItems = items?.length > 0;
  const shouldShowEmptyState = !hasItems && !loadingInsights && !hasExternalData;

  const renderingItem = () => {
    if (loadingInsights && !hasItems) {
      return renderSkeletonRows(4);
    }
    if (shouldShowEmptyState) {
      return renderContent(title, accountId, cloudProvider);
    }
    return generateRows(items);
  };

  return (
    <Box sx={{ mr: 'var(--ds-space-5)', mb: 'var(--ds-space-5)' }}>
      <CollapsableCard
        id={`home-section-${String(title).toLowerCase().replace(/\s+/g, '-')}`}
        persist='local'
        defaultOpen
        elevation='raised'
        composition={meta ? 'header+meta+body' : 'header+body'}
        header={<SectionHeader icon={icon} title={title} subtitle={subtitle} tone={tone} />}
        meta={meta}
        footer={shouldShowEmptyState ? null : footer}
      >
        {summary}
        <Box sx={{ display: 'flex', flexDirection: 'column' }}>{renderingItem()}</Box>
        {extra}
      </CollapsableCard>
    </Box>
  );
};
CardsBlock.propTypes = {
  icon: PropTypes.any,
  title: PropTypes.any,
  subtitle: PropTypes.node,
  tone: PropTypes.string,
  items: PropTypes.array,
  type: PropTypes.string,
  accountId: PropTypes.string,
  loadingInsights: PropTypes.bool,
  hasExternalData: PropTypes.bool,
  currencySymbol: PropTypes.string,
  cloudProvider: PropTypes.string,
  meta: PropTypes.node,
  summary: PropTypes.node,
  footer: PropTypes.node,
  extra: PropTypes.node,
};

const buildUrl = (selectedCluster, id, fragment, navigate, additionalQuery = {}) => {
  let route = '';

  if (navigate === 'details') {
    const isK8s = selectedCluster?.cloud_provider === 'K8s';
    const base = isK8s ? '/kubernetes/details' : '/cloud-account/details';

    // Construct Query Params
    const params = new URLSearchParams();

    if (additionalQuery && Object.keys(additionalQuery).length > 0) {
      Object.entries(additionalQuery).forEach(([key, value]) => {
        if (value !== undefined && value !== null) {
          params.set(key, value);
        }
      });
    }

    const queryString = params.toString();
    const queryPart = queryString ? `?${queryString}` : '';
    const fragmentPart = fragment ? `#${fragment}` : '';

    // Construct Full URL: /path/id?query=params#fragment
    route = `${base}/${id}${queryPart}${fragmentPart}`;
  } else if (navigate === 'auto-pilot') {
    route = `/auto-pilot?accountId=${id}`;
  }

  return route;
};

const HomeWidgets = ({ quickLinksData, selectedCluster, cluster }) => {
  const links = quickLinksData
    .filter((d) => d.cloudProvider === selectedCluster?.cloud_provider)
    .map((data) => data.links.map((link) => ({ ...link })))
    .flat();

  const header = (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
      <Box
        sx={{
          width: 28,
          height: 28,
          borderRadius: 'var(--ds-radius-md)',
          backgroundColor: 'var(--ds-blue-100)',
          color: 'var(--ds-blue-600)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          flexShrink: 0,
        }}
      >
        <SafeIcon src={QuickLink} width='14px' height='14px' />
      </Box>
      <Typography
        sx={{
          fontFamily: 'Poppins, sans-serif',
          fontSize: '13px',
          fontWeight: 600,
          color: 'var(--ds-gray-900)',
          letterSpacing: '-0.01em',
        }}
      >
        Quick Links
      </Typography>
    </Box>
  );

  // Recolour SafeIcon src-based SVGs (originals may be black, white, or
  // already-tinted) to gray-700.
  const GRAY_700_FILTER = 'brightness(0) saturate(100%) invert(28%) sepia(3%) saturate(0%) hue-rotate(0deg) brightness(95%) contrast(90%)';

  return (
    <DSCard size='sm' elevation='flat' header={header} sx={{ overflow: 'hidden' }}>
      <Box
        display={'grid'}
        gridTemplateColumns={'1fr 1fr'}
        gap={'7px'}
        sx={{
          '@media (max-width: 1250px)': {
            gridTemplateColumns: '1fr',
          },
        }}
      >
        {links.map((link) => (
          <Link href={buildUrl(selectedCluster, cluster, link.fragment, 'details', {})} key={link.name} style={{ textDecoration: 'none' }}>
            <Box
              display={'flex'}
              alignItems={'center'}
              gap='var(--ds-space-2)'
              borderRadius='var(--ds-radius-md)'
              sx={{
                px: 'var(--ds-space-2)',
                py: '6px',
                cursor: 'pointer',
                '& .ql-icon': {
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  width: 18,
                  height: 18,
                  flexShrink: 0,
                  transition: 'filter 0.2s ease',
                  filter: GRAY_700_FILTER,
                  '& img, & svg': {
                    maxWidth: '100%',
                    maxHeight: '100%',
                    objectFit: 'contain',
                  },
                },
                '&:hover': {
                  backgroundColor: 'var(--ds-brand-100)',
                  '& .ql-icon': { filter: GRAY_700_FILTER },
                },
              }}
            >
              <Box className='ql-icon'>
                <SafeIcon src={link.icon} alt={link.name} width='16px' height='16px' />
              </Box>
              <Typography fontSize={'var(--ds-text-small)'} fontWeight={400} color='var(--ds-gray-700)'>
                {link.name}
              </Typography>
            </Box>
          </Link>
        ))}
      </Box>
    </DSCard>
  );
};
HomeWidgets.propTypes = {
  quickLinksData: PropTypes.any,
  selectedCluster: PropTypes.any,
  cluster: PropTypes.any,
};
HomeWidgets.propTypes = {
  quickLinksData: PropTypes.any,
  selectedCluster: PropTypes.any,
  cluster: PropTypes.any,
};

const Home = () => {
  const router = useRouter();
  const [cluster, setCluster] = useState(router.query.accountId);
  const [insightData, setInsightData] = useState([]);
  const [workflowData, setWorkflowData] = useState({ totalCount: 0, configuredCount: 0, actionedCount: 0 });
  const [imageScanData, setImageScanData] = useState({});
  const [certificateData, setCertificateData] = useState({});
  const [generateQuestionText, setGenerateQuestionText] = useState('');
  const [showModal, setShowModal] = useState(false);
  const [loadingInsights, setLoadingInsights] = useState({
    troubleshooting: false,
    k8sOps: false,
  });
  const [loadingConversation, setLoadingConversation] = useState(false);
  const { selectedCluster, allCluster } = useData();
  const textareaRef = useRef(null);
  const currencySymbol = useCurrencySymbol(cluster);
  // Map integers to fragments based on KubernetesDetails config
  const QuickLinksData = [
    {
      links: [
        {
          name: 'Query Logs',
          fragment: 'monitoring/logs', // Tab 4, Subtab 0
          icon: LogsIcon,
        },
        {
          name: 'Recent Errors',
          fragment: 'monitoring/groups', // Tab 4, Subtab 1
          icon: RecentErrorIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Query Metrics',
          // Note: In your config, both Logs and Metrics had fragment 'query'.
          // Ensure your Router config distinguishes them, or this will open Logs.
          fragment: 'monitoring/query', // Tab 4, Subtab 2
          icon: MatricsIcon,
        },
      ],
      insights: [],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'View Traces',
          fragment: 'monitoring/traces', // Tab 4, Subtab 5
          icon: TraceIcon,
        },
        {
          name: 'Service Maps',
          fragment: 'monitoring/service-map', // Tab 4, Subtab 6
          icon: ServiceMapsIcon,
        },
      ],
      insights: [],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'View Applications',
          fragment: 'kubernetes/applications', // Tab 3, Subtab 1
          icon: NamespacesIcon,
        },
        {
          name: 'View Pods',
          fragment: 'kubernetes/pods', // Tab 3, Subtab 3
          icon: PodsIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Security',
          fragment: 'security/image-scan', // Tab 5, Subtab 0
          icon: SecurityIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Troubleshoot',
          fragment: 'events/summary', // Tab 2, Subtab 0
          icon: TroubleshootIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Optimize',
          fragment: 'optimize/summary', // Tab 1, Subtab 7
          icon: OptimizeIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'K8s',
      accountId: cluster,
    },
    // --- Non-K8s Providers (Assumed Fragments) ---
    {
      links: [
        {
          name: 'Cloud Logs',
          fragment: 'monitoring/cloud-logs',
          icon: LogsIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Troubleshoot',
          fragment: 'events/events',
          icon: TroubleshootIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Optimize',
          fragment: 'optimize/right-sizing',
          icon: OptimizeIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Services',
          fragment: 'services',
          icon: PvcSightSizing,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'EC2',
          fragment: 'ec2/summary',
          icon: AWSEC2Icon,
          base: 'black-dominant',
        },
        {
          name: 'RDS',
          fragment: 'rds/summary',
          icon: AWSRDSIcon,
          base: 'black-dominant',
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'S3',
          fragment: 's3/summary',
          icon: AWSS3Icon,
          base: 'black-dominant',
        },
        {
          name: 'ECS',
          fragment: 'ecs/summary',
          icon: AWSECSIcon,
          base: 'black-dominant',
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'AWS',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Cloud Logs',
          fragment: 'monitoring/cloud-logs',
          icon: LogsIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'Azure',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Troubleshoot',
          fragment: 'events/events',
          icon: TroubleshootIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'Azure',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Optimize',
          fragment: 'optimize/right-sizing',
          icon: OptimizeIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'Azure',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Services',
          fragment: 'services',
          icon: PvcSightSizing,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'Azure',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'VM',
          fragment: 'vm/summary',
          icon: AzureVMIcon,
          base: 'white-dominant',
        },
        {
          name: 'SQL',
          fragment: 'sql/summary',
          icon: AzureSqlIcon,
          base: 'white-dominant',
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'Azure',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Blob Container',
          fragment: 'blob/summary',
          icon: AzureBlobIcon,
          base: 'white-dominant',
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'Azure',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Cloud Logs',
          fragment: 'monitoring/cloud-logs',
          icon: LogsIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'GCP',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Troubleshoot',
          fragment: 'events/events',
          icon: TroubleshootIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'GCP',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Optimize',
          fragment: 'optimize/right-sizing',
          icon: OptimizeIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'GCP',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Services',
          fragment: 'services',
          icon: PvcSightSizing,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'GCP',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Compute Engine',
          fragment: 'compute-engine/summary',
          icon: GCPComputeEngineIcon,
        },
        {
          name: 'Cloud SQL',
          fragment: 'cloud-sql/summary',
          icon: GCPCloudSQLIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'GCP',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Cloud Storage',
          fragment: 'cloud-storage/summary',
          icon: GCPCloudStorageIcon,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'GCP',
      accountId: cluster,
    },
    // --- CloudFoundry ---
    {
      links: [
        {
          name: 'Troubleshoot',
          fragment: 'events/events',
          icon: TroubleshootIconBlue,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'CloudFoundry',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Apps',
          fragment: 'cf-apps/instances',
          icon: NamespacesIcon,
        },
        {
          name: 'Organizations',
          fragment: 'cf-organizations/instances',
          icon: PvcSightSizing,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'CloudFoundry',
      accountId: cluster,
    },
    {
      links: [
        {
          name: 'Spaces',
          fragment: 'cf-spaces/instances',
          icon: PvcSightSizing,
        },
        {
          name: 'Routes',
          fragment: 'cf-routes/instances',
          icon: PvcSightSizing,
        },
      ],
      navigate: 'details',
      loading: false,
      cloudProvider: 'CloudFoundry',
      accountId: cluster,
    },
  ];

  const integrationData = [
    {
      id: 'messaging',
      anchor: 'messaging',
      startIcon: DataBaseBlueIcon,
      title: 'Connect to your messaging tool',
      options: [
        { name: 'Slack', icon: SlackIcon, redirect: '/accounts/account-form?cloudProvider=SLACK' },
        { name: 'MS Teams', icon: MsTeamsIcon, redirect: '/accounts/account-form?cloudProvider=MSTEAMS' },
        { name: 'G Chat', icon: GChatIcon, redirect: '/accounts/account-form?cloudProvider=GOOGLE_CHAT' },
      ],
      actionText: 'Add Messaging',
      actionIcon: <FiArrowRight />,
    },
    {
      id: 'pagerduty',
      anchor: 'messaging',
      startIcon: SirenBlueIcon,
      title: 'Using PagerDuty? You can connect to your',
      options: [{ name: 'PagerDuty', icon: PagerDutyIcon, redirect: '/accounts/account-form?cloudProvider=PAGERDUTY' }],
      actionText: 'Add PagerDuty',
      actionIcon: <FiArrowRight />,
    },
    {
      id: 'ticketing',
      anchor: 'ticket',
      startIcon: TicketBlueIcon,
      title: 'I offer integrations with your Ticketing system',
      options: [
        { name: 'Service Now', icon: ServiceNowIcon, redirect: '/accounts/account-form?cloudProvider=SERVICENOW' },
        { name: 'Jira', icon: JiraIcon, redirect: '/accounts/account-form?cloudProvider=JIRA' },
      ],
      actionText: 'Add Ticketing',
      actionIcon: <FiArrowRight />,
    },
    {
      id: 'code',
      anchor: 'repo',
      startIcon: RepoBlueIcon,
      title: 'btw, I can also integrate with your code repo',
      options: [],
      actionText: 'Add GitHub',

      actionIcon: <FiArrowRight />,
      actionStartIcon: GithubIcon,
    },
  ];

  const footerSections = [
    {
      type: 'integrations',
      title: "We've got many other integrations available for you to explore.",
      icon: DataBaseBlueIcon,
      action: {
        label: 'Check Integrations',
        redirect: '/user-management#integrations',
      },
    },
  ];

  useEffect(() => {
    if (router.query.accountId) {
      setCluster(router.query.accountId);
      setLoadingInsights({ troubleshooting: true, k8sOps: true });
    }
  }, [router.query.accountId]);

  useEffect(() => {
    if (!cluster) {
      return;
    }
    setLoadingInsights({
      troubleshooting: true,
      k8sOps: true,
    });
    getTroubleShootData(cluster);
    getWorkflowData(cluster);
    getImageScan(cluster);
    getCertificate(cluster);
  }, [cluster]);

  const getImageScan = async (cluster) => {
    setImageScanData({});
    homeApi
      .getImageScanData(cluster)
      .then((res) => {
        const recommendationSecurityData = res?.data?.data?.recommendation_security_groupings_v2?.rows || [];
        if (recommendationSecurityData.length > 0) {
          const totalCritical = recommendationSecurityData.reduce((sum, item) => sum + item.count_severity_critical, 0);
          const imageCount = recommendationSecurityData.reduce((sum, item) => sum + item.count_image, 0);
          const appCount = recommendationSecurityData.length;
          setImageScanData({ totalCritical, imageCount, appCount });
        }
      })
      .finally(() => {
        setLoadingInsights((prevState) => ({
          ...prevState,
          k8sOps: false,
        }));
      });
  };

  const getCertificate = async (cluster) => {
    setCertificateData({});
    homeApi
      .getCertificateIssue(cluster)
      .then((res) => {
        const recommendation = res?.data?.data?.recommendation?.rows || [];
        if (recommendation.length > 0) {
          const allRecommendations = recommendation.map((r) => r.recommendation);
          const parsedArray = allRecommendations.map(JSON.parse);
          const expiringSoon = parsedArray?.filter((item) => {
            const expiry = new Date(item.expiry_date);
            return expiry.getTime() - new Date().getTime() <= 30 * 24 * 60 * 60 * 1000;
          })?.length;
          if (expiringSoon) {
            setCertificateData({ expiringSoon });
          }
        }
      })
      .finally(() => {
        setLoadingInsights((prevState) => ({
          ...prevState,
          k8sOps: false,
        }));
      });
  };

  const getTroubleShootData = async (cluster) => {
    setInsightData([]);
    homeApi
      .getInsights(cluster)
      .then((res) => {
        const insights = res?.data?.data?.insights_list?.rows || [];
        // Store raw titles for currency processing
        const insightsWithRaw = insights.map((item) => ({
          ...item,
          rawTitle: item.title,
        }));
        setInsightData(insightsWithRaw);
      })
      .finally(() => {
        setLoadingInsights((prevState) => ({
          ...prevState,
          troubleshooting: false,
        }));
      });
  };

  const getWorkflowData = async (accountId) => {
    setWorkflowData({ totalCount: 0, configuredCount: 0, actionedCount: 0 });
    const dateRange = {
      startDate: getLast24Hrs(),
      endDate: new Date(),
    };
    try {
      const [totalResponse, configuredResponse, actionedResponse] = await Promise.all([
        apiWorkflow.getWorkflowCount(accountId, { status: 'ACTIVE' }),
        apiWorkflow.getWorkflowCount(accountId, { status: 'ACTIVE', triggerType: 'event' }),
        apiWorkflow.getWorkflowExecutionCount(accountId, { startDate: dateRange.startDate }),
      ]);

      // Tolerate two response shapes: { data: { <field>: { count } } } or { <field>: { count } }
      const pickCount = (resp, field) => {
        const root = resp?.data ?? resp;
        const v = root?.[field]?.count;
        return typeof v === 'number' ? v : 0;
      };
      const next = {
        totalCount: pickCount(totalResponse, 'workflows_count'),
        configuredCount: pickCount(configuredResponse, 'workflows_count'),
        actionedCount: pickCount(actionedResponse, 'workflows_count_executions'),
      };
      setWorkflowData(next);
    } catch (error) {
      console.error('Failed to fetch workflow data:', error);
    } finally {
      setLoadingInsights((prevState) => ({
        ...prevState,
        k8sOps: false,
      }));
    }
  };

  const handleGenerateInvestigation = async () => {
    setLoadingConversation(true);
    const newSessionId = uuidv4();
    apiAskNudgebee
      .aiGenerateInvestigate({
        account_id: router.query.accountId,
        query: generateQuestionText,
        session_id: newSessionId,
      })
      .then((res) => {
        const response = res?.data?.data?.ai_execute_investigation ?? {};
        if (!response?.data?.query) {
          snackbar.error('Cant process your request right now.');
          setLoadingConversation(false);
        } else {
          setLoadingConversation(false);
          setGenerateQuestionText('');
          router.push(`/ask-nudgebee?accountId=${router.query.accountId}&session_id=${newSessionId}`);
        }
      });
  };

  const closeModal = () => {
    setShowModal(false);
  };

  const troubleshootItems = insightData.filter(
    (o) =>
      o.type == 'Troubleshooting' ||
      o.rule?.category == 'Troubleshooting' ||
      o.type == 'Performance' ||
      (selectedCluster?.cloud_provider != 'K8s' && o.type == 'Ops')
  );
  const optimizeItems = insightData.filter(
    (g) =>
      g.type == 'Optimization' ||
      g.rule?.category == 'Optimization' ||
      g.type == 'Cost' ||
      g.type == 'InfraUpgrade' ||
      g.type == 'Security' ||
      g.type == 'Configuration'
  );
  const opsItems = insightData.filter((g) => g.type == 'Ops' || g.rule?.category == 'Ops');

  const securityHasExternal =
    Object.keys(imageScanData).length > 0 ||
    Object.keys(certificateData).length > 0 ||
    workflowData.totalCount > 0 ||
    workflowData.configuredCount > 0 ||
    workflowData.actionedCount > 0;

  // Static subtitles — live counts were unreliable due to row-list truncation
  // and savings sum quirks. Revert to descriptive text per design.
  const troubleshootSubtitle = 'Active incidents and event trends';
  const optimizeSubtitle = 'Right-sizing, storage, and cost recommendations';

  // Security subtitle stays live — driven by separate image-scan / cert APIs
  // whose counts match the row data exactly.
  const securitySubtitle = (() => {
    const parts = [];
    if (imageScanData?.totalCritical) parts.push(`${imageScanData.totalCritical} critical CVEs`);
    if (certificateData?.expiringSoon) parts.push(`${certificateData.expiringSoon} certs expiring`);
    if (opsItems.length) parts.push(`${opsItems.length} ops issues`);
    return parts.length ? parts.join(' · ') : 'Vulnerabilities, certificates, image scans';
  })();

  return (
    <Grid container spacing={6} mt='28px'>
      <Grid item xs={9} sx={{ pt: '0px !important' }}>
        <Grid container>
          <K8sAccountModal openModal={showModal} handleClose={closeModal} />
          <Grid item xs={12} sx={{ mr: '24px', pb: '16px' }}>
            <DSCard
              size='sm'
              elevation='raised'
              sx={{
                display: 'flex',
                alignItems: 'center',
                boxShadow: '0 4px 14px rgba(0, 0, 0, 0.1)',
                gap: 'var(--ds-space-3)',

                transition: 'border-color 200ms ease, box-shadow 200ms ease, transform 200ms ease',
                '&:hover': {
                  borderColor: 'var(--ds-blue-300)',
                  boxShadow: '0 4px 14px rgba(0, 0, 0, 0.1)',
                },
                '&:focus-within': {
                  borderColor: 'var(--ds-blue-500)',
                  boxShadow: '0 0 0 3px rgba(59, 130, 246, 0.06), 0 6px 18px rgba(59, 130, 246, 0.14)',
                },
                '& textarea': {
                  width: '100%',
                  border: 0,
                  resize: 'none',
                  boxShadow: 'none',
                  color: 'var(--ds-gray-800)',
                  fontWeight: 400,
                  fontSize: '15px',
                  textAlign: 'left',
                  padding: '0 var(--ds-space-3) 0 0 !important',
                  '&::placeholder': { color: 'var(--ds-gray-500)', fontWeight: 400, fontSize: '15px' },
                  '&:focus': { boxShadow: 'none' },
                  '&::-webkit-scrollbar': { display: 'none' },
                },
                '& .MuiOutlinedInput-notchedOutline': { border: '0 !important' },
              }}
            >
              <Box
                sx={{
                  width: 36,
                  height: 36,
                  borderRadius: '50%',
                  backgroundColor: 'var(--ds-yellow-200, #FFF3D0)',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  flexShrink: 0,
                  overflow: 'hidden',
                  '& img': { width: 28, height: 28, objectFit: 'contain' },
                }}
              >
                <SafeIcon src={getNubiIconUrl()} alt='nubi' width={28} height={28} />
              </Box>
              <Box sx={{ flex: 1, minWidth: 0, display: 'flex', alignItems: 'center', minHeight: 36 }}>
                <Textarea
                  ref={textareaRef}
                  id='custom-textarea'
                  fontSize='15px'
                  fontWeight='400'
                  value={generateQuestionText}
                  maxLength={500000}
                  placeholder={'How can I assist you today?'}
                  onChange={(e) => {
                    setGenerateQuestionText(e.target.value);
                  }}
                  sx={{ width: '100%', ':disabled': { opacity: 0.5 } }}
                  maxRows={5}
                  disabled={loadingConversation}
                />
              </Box>

              <Button
                id='ask-me-btn'
                tone='primary'
                size='md'
                composition='icon-only'
                icon={<ArrowForwardIcon />}
                aria-label='Send'
                onClick={() => handleGenerateInvestigation()}
                disabled={!generateQuestionText || loadingConversation}
              />
            </DSCard>
          </Grid>
          {getUserSession()?.user?.name && allCluster?.length == 1 ? (
            <Grid item xs={12} sx={{ mr: '24px', pb: 'var(--ds-space-4)' }}>
              <CollapsableCard
                elevation='flat'
                defaultOpen={allCluster?.length == 0 || false}
                header={
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-title)',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      color: '#6366f1',
                    }}
                  >
                    Hi {(getUserSession()?.user?.name || '')?.split(' ')[0]}, Nice to have you here in sandbox!
                  </Typography>
                }
              >
                <Box
                  sx={{
                    display: 'grid',
                    gridTemplateColumns: '220px 1fr',
                    gap: 'var(--ds-space-5)',
                    '@media (max-width: 1230px)': {
                      gridTemplateColumns: '150px 1fr',
                    },
                  }}
                >
                  <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center' }}>
                    <SafeIcon src={getBrandingAsset('newUserBee')} alt='Welcome' width={200} height={200} />
                  </Box>
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
                    <DSCard variant='accent' tone='info' size='md' elevation='flat'>
                      <Box
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'space-between',
                          gap: 'var(--ds-space-3)',
                          flexWrap: 'wrap',
                        }}
                      >
                        <Box sx={{ flex: 1, minWidth: '240px' }}>
                          {getUserSession()?.user?.name && (
                            <Typography
                              sx={{
                                fontSize: 'var(--ds-text-title)',
                                fontWeight: 'var(--ds-font-weight-semibold)',
                                color: '#6366f1',
                                mb: 'var(--ds-space-1)',
                              }}
                            >
                              Hi {getUserSession()?.user?.name?.split(' ')[0]}, Nice to have you here in sandbox!
                            </Typography>
                          )}
                          <Typography
                            sx={{
                              fontSize: 'var(--ds-text-body-lg)',
                              fontWeight: 'var(--ds-font-weight-regular)',
                              color: 'var(--ds-gray-600)',
                              lineHeight: 'var(--ds-text-body-lg-lh)',
                            }}
                          >
                            You are currently on our Demo Account (which only has partial data) Let&apos;s get you started with your cluster/account
                          </Typography>
                        </Box>
                        <Button
                          tone='primary'
                          size='sm'
                          trailingAccent={<ArrowForwardIcon />}
                          onClick={() => router.push(`/user-management#integrations`)}
                        >
                          Add K8s Account
                        </Button>
                      </Box>
                    </DSCard>

                    <Box>
                      <Typography
                        sx={{
                          fontSize: 'var(--ds-text-body-lg)',
                          fontWeight: 'var(--ds-font-weight-medium)',
                          color: 'var(--ds-gray-700)',
                          mb: 'var(--ds-space-2)',
                        }}
                      >
                        Other things you can do to make sure you get the full intelligent automation experience.
                      </Typography>
                      {integrationData.map((item, index) => (
                        <Box
                          key={item.id}
                          sx={{
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'space-between',
                            gap: 'var(--ds-space-3)',
                            py: 'var(--ds-space-2)',
                            borderBottom: index < integrationData.length - 1 ? '1px solid var(--ds-gray-200)' : 'none',
                            flexWrap: 'wrap',
                          }}
                        >
                          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
                            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                              {item.startIcon && <SafeIcon src={item.startIcon} alt={item.id} width={20} height={20} />}
                              <Typography
                                sx={{
                                  fontSize: 'var(--ds-text-body-lg)',
                                  fontWeight: 'var(--ds-font-weight-regular)',
                                  color: 'var(--ds-gray-700)',
                                  lineHeight: 'var(--ds-text-body-lg-lh)',
                                }}
                              >
                                {item.title}
                              </Typography>
                            </Box>
                            {item.options && item.options.length > 0 && (
                              <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', flexWrap: 'wrap' }}>
                                {item.options.map((option) => (
                                  <Chip
                                    key={option.name}
                                    size='xs'
                                    tone='info'
                                    shape='rect'
                                    icon={option.icon && <SafeIcon src={option.icon} alt={option.name} width={14} height={14} />}
                                    onClick={() => router.push(option.redirect)}
                                  >
                                    {option.name}
                                  </Chip>
                                ))}
                              </Box>
                            )}
                          </Box>
                          <Button
                            tone='secondary'
                            size='xs'
                            icon={item.actionStartIcon && <SafeIcon src={item.actionStartIcon} alt={item.actionText} width={14} height={14} />}
                            iconPlacement='start'
                            trailingAccent={<ArrowForwardIcon />}
                            onClick={() => router.push(`/user-management#integrations`)}
                          >
                            {item.actionText}
                          </Button>
                        </Box>
                      ))}
                      <Grid container spacing={2} sx={{ mt: 'var(--ds-space-1)' }}>
                        {footerSections.map((section) => (
                          <Grid item xl={6} md={12} key={section.type}>
                            <DSCard variant='accent' tone='success' size='sm' elevation='flat'>
                              <Box
                                sx={{
                                  display: 'flex',
                                  alignItems: 'center',
                                  justifyContent: 'space-between',
                                  gap: 'var(--ds-space-3)',
                                  flexWrap: 'wrap',
                                }}
                              >
                                <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flex: 1, minWidth: 0 }}>
                                  {section.icon && <SafeIcon src={section.icon} alt={section.title} width={20} height={20} />}
                                  <Typography
                                    sx={{
                                      fontSize: 'var(--ds-text-body-lg)',
                                      fontWeight: 'var(--ds-font-weight-regular)',
                                      color: 'var(--ds-gray-700)',
                                      lineHeight: 'var(--ds-text-body-lg-lh)',
                                    }}
                                  >
                                    {section.title}
                                  </Typography>
                                </Box>
                                <Button
                                  tone='secondary'
                                  size='xs'
                                  icon={
                                    section?.action?.icon && (
                                      <SafeIcon src={section.action.icon} alt={section?.action?.label} width={14} height={14} />
                                    )
                                  }
                                  iconPlacement='start'
                                  trailingAccent={<ArrowForwardIcon />}
                                  onClick={() => router.push(`/user-management#integrations`)}
                                >
                                  {section?.action?.label}
                                </Button>
                              </Box>
                            </DSCard>
                          </Grid>
                        ))}
                      </Grid>
                    </Box>
                  </Box>
                </Box>
              </CollapsableCard>
            </Grid>
          ) : null}{' '}
        </Grid>
        <CardsBlock
          title='Troubleshoot'
          subtitle={troubleshootSubtitle}
          icon={ErrorOutlineIcon}
          tone='critical'
          items={troubleshootItems}
          type={'troubleshooting'}
          accountId={selectedCluster.value || ''}
          loadingInsights={loadingInsights.troubleshooting}
          currencySymbol={currencySymbol || '$'}
          cloudProvider={selectedCluster?.cloud_provider || ''}
          footer={
            <Button
              tone='ghost'
              size='xs'
              icon={<KeyboardArrowRightIcon />}
              iconPlacement='end'
              onClick={() => window.open(`/troubleshoot?accountId=${selectedCluster?.value || ''}`, '_blank')}
            >
              View all issues
            </Button>
          }
        />
        {selectedCluster?.cloud_provider !== 'CloudFoundry' && (
          <CardsBlock
            title='Optimize'
            subtitle={optimizeSubtitle}
            icon={OptimizeGaugeIcon}
            tone='info'
            items={optimizeItems}
            type={'optimization'}
            accountId={selectedCluster?.value || ''}
            loadingInsights={loadingInsights.troubleshooting}
            currencySymbol={currencySymbol || '$'}
            cloudProvider={selectedCluster?.cloud_provider || ''}
            footer={
              <Button
                tone='ghost'
                size='xs'
                icon={<KeyboardArrowRightIcon />}
                iconPlacement='end'
                onClick={() => window.open(`/optimise?accountId=${selectedCluster?.value || ''}`, '_blank')}
              >
                View all recommendations
              </Button>
            }
          />
        )}
        {selectedCluster?.cloud_provider === 'K8s' && (
          <CardsBlock
            title='Security & Compliance'
            subtitle={securitySubtitle}
            icon={ShieldOutlinedIcon}
            tone='agent'
            items={opsItems}
            type={'Ops'}
            loadingInsights={loadingInsights.k8sOps}
            accountId={selectedCluster?.value || ''}
            currencySymbol={currencySymbol || '$'}
            hasExternalData={securityHasExternal}
            footer={
              <Button
                tone='ghost'
                size='xs'
                icon={<KeyboardArrowRightIcon />}
                iconPlacement='end'
                onClick={() => window.open(`/kubernetes/details/${selectedCluster?.value || ''}#security/image-scan`, '_blank')}
              >
                View security dashboard
              </Button>
            }
            extra={
              <>
                {imageScanData && Object.keys(imageScanData).length > 0 ? (
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 'var(--ds-space-3)',
                      py: 'var(--ds-space-2)',
                      px: 'var(--ds-space-3)',
                      mx: 'var(--ds-space-2)',
                      borderBottom: '1px solid var(--ds-gray-100)',
                    }}
                  >
                    <Box sx={{ flexShrink: 0 }}>
                      <InsightIcon severity='critical' icon={ImageOutlinedIcon} />
                    </Box>
                    <Typography sx={{ flex: 1, minWidth: 0, fontSize: '13px', lineHeight: 1.5, color: 'var(--ds-gray-800)' }}>
                      {`${imageScanData.appCount} apps have ${imageScanData.totalCritical} critical CVEs in ${imageScanData.imageCount} images`}
                    </Typography>
                    <Button
                      tone='ghost'
                      size='xs'
                      icon={<AgentSparkleIcon />}
                      iconPlacement='start'
                      onClick={() =>
                        window.open(`/kubernetes/details/${selectedCluster?.value}?status=Open&severity=Critical#security/image-scan`, '_blank')
                      }
                    >
                      Review
                    </Button>
                  </Box>
                ) : null}
                {certificateData && Object.keys(certificateData).length > 0 ? (
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 'var(--ds-space-3)',
                      py: 'var(--ds-space-2)',
                      px: 'var(--ds-space-3)',
                      mx: 'var(--ds-space-2)',
                      borderBottom: '1px solid var(--ds-gray-100)',
                      '&:last-child': { borderBottom: 'none' },
                    }}
                  >
                    <Box sx={{ flexShrink: 0 }}>
                      <InsightIcon severity='high' icon={LockOutlinedIcon} />
                    </Box>
                    <Typography sx={{ flex: 1, minWidth: 0, fontSize: '13px', lineHeight: 1.5, color: 'var(--ds-gray-800)' }}>
                      {`${certificateData.expiringSoon} certificates expiring in less than 30 days`}
                    </Typography>
                    <Button
                      tone='ghost'
                      size='xs'
                      icon={<AgentSparkleIcon />}
                      iconPlacement='start'
                      onClick={() => window.open(`/kubernetes/details/${selectedCluster?.value}#security/ssl-certificate-issues`, '_blank')}
                    >
                      Review
                    </Button>
                  </Box>
                ) : null}
              </>
            }
          />
        )}
      </Grid>
      <Grid item xs={3} sx={{ pl: '15px !important', pt: '0px !important', display: 'flex', flexDirection: 'column', gap: '16px' }}>
        <AutomationsCard
          workflowData={workflowData}
          accountId={selectedCluster?.value || ''}
          onManage={(id) => window.open(`/auto-pilot?accountId=${id}&status=Active`, '_blank')}
        />
        <PendingFollowUps accountId={cluster} />
        <HomeWidgets quickLinksData={QuickLinksData} selectedCluster={selectedCluster} cluster={cluster} />
      </Grid>
    </Grid>
  );
};

export default Home;
