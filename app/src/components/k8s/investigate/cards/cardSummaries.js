import { formatClockCompact, parseDateValue } from '@shared/format/Datetime';

// The one-line summaries shown on collapsed evidence cards.
//
// A card with no summary used to read "Nothing major to show", and later "Open the card to see
// what it holds", on cards that held the answer. Several card types never set a summary at all,
// and some have no stored evidence to carry one, so a summary stored by the server could not reach
// them. Each function here states what a card holds from the data that card already has, which
// also covers events stored before any of this existed.
//
// Every function returns a list of highlight lines ({message, severity}) and an empty list when it
// has nothing true to say.

const info = (message) => ({ message, severity: 'Info' });

const numbersOf = (values) => (Array.isArray(values) ? values : []).map((v) => parseFloat(v)).filter((v) => Number.isFinite(v));

// Looped rather than spread: a stored query result can hold more values than a call can take as
// arguments (Math.max(...values) throws past roughly 120,000).
const highestOf = (nums) => nums.reduce((max, v) => (v > max ? v : max), -Infinity);
const lowestOf = (nums) => nums.reduce((min, v) => (v < min ? v : min), Infinity);

const peakOf = (values) => {
  const nums = numbersOf(values);
  return nums.length ? highestOf(nums) : null;
};

// Readable without claiming a unit: 0.0004 stays 0.0004, 1234.5678 becomes 1,234.57.
const plain = (value) => {
  if (!Number.isFinite(value)) return '';
  const abs = Math.abs(value);
  if (abs !== 0 && abs < 0.01) return String(Number(value.toPrecision(2)));
  return value.toLocaleString('en-US', { maximumFractionDigits: 2 });
};

const MIB = 1024 * 1024;
const GIB = 1024 * MIB;
const mib = (bytes) => `${Math.round(bytes / MIB)} MiB`;
const gib = (bytes) => `${(bytes / GIB).toFixed(2)} GiB`;
const pct = (part, whole) => `${Math.round((part / whole) * 100)}%`;
const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

/**
 * Timeline: when this alert was first seen, when the workload last changed, and when it fired.
 * @param {{timestamp: string, ref_type: string, action: string}[]} items
 */
export const summarizeTimeline = (items) => {
  const list = Array.isArray(items) ? items : [];
  const fired = list.find((i) => i?.ref_type === 'event' && i?.action === 'fired');
  const first = list.find((i) => i?.ref_type === 'event' && i?.action === 'first_occurrence');
  // A change whose time cannot be read is left out: it cannot be placed before or after the others.
  const timeOf = (timestamp) => parseDateValue(timestamp).getTime();
  const changes = list.filter((i) => i?.ref_type === 'config_change' && Number.isFinite(timeOf(i?.timestamp)));
  const lastChange = changes
    .map((i) => i.timestamp)
    .sort((a, b) => timeOf(a) - timeOf(b))
    .at(-1);

  const parts = [];
  if (first?.timestamp && first.timestamp !== fired?.timestamp) parts.push(`First seen ${formatClockCompact(first.timestamp)}`);
  if (lastChange)
    parts.push(`${changes.length === 1 ? 'config changed' : `last of ${changes.length} config changes`} ${formatClockCompact(lastChange)}`);
  if (fired?.timestamp) parts.push(`fired ${formatClockCompact(fired.timestamp)}`);
  if (parts.length === 0) return [];
  const line = parts.join(' · ');
  return [info(line.charAt(0).toUpperCase() + line.slice(1))];
};

/**
 * Service map: who calls the service and what it calls. Follows the server's own reading of the
 * graph (CALLS edges to and from the target); that summary is built at enrichment time but is not
 * stored with the evidence.
 * @param {object[]} nodes
 * @param {object[]} edges
 * @param {string} target the service the alert is about
 */
export const summarizeDependencies = (nodes, edges, target) => {
  const nodeList = Array.isArray(nodes) ? nodes : [];
  if (nodeList.length === 0 || !target) return [];
  // A node without an id cannot be the end of an edge; left in, it would match every edge that
  // is itself missing an end.
  const identified = nodeList.filter((n) => n?.id);
  const nameById = new Map(identified.map((n) => [n.id, n.properties?.name]));
  const targetIds = new Set(identified.filter((n) => n.properties?.name === target).map((n) => n.id));
  // The server finds the target by its own lookup; here it is found by name. When that fails the
  // graph says nothing about this service either way, so nothing is claimed.
  if (targetIds.size === 0) return [];
  const upstream = new Set();
  const downstream = new Set();
  (Array.isArray(edges) ? edges : []).forEach((edge) => {
    if (edge?.relationship_type !== 'CALLS') return;
    const source = nameById.get(edge.source_node_id);
    const dest = nameById.get(edge.dest_node_id);
    if (targetIds.has(edge.dest_node_id) && source && source !== target) upstream.add(source);
    if (targetIds.has(edge.source_node_id) && dest && dest !== target) downstream.add(dest);
  });
  if (upstream.size === 0 && downstream.size === 0) {
    return [info(`Nothing in the service map calls ${target}, and it calls nothing`)];
  }
  const lines = [];
  if (upstream.size > 0) lines.push(info(`${plural(upstream.size, 'service calls', 'services call')} ${target}: ${[...upstream].join(', ')}`));
  if (downstream.size > 0) lines.push(info(`${target} calls ${plural(downstream.size, 'service', 'services')}: ${[...downstream].join(', ')}`));
  return lines;
};

/**
 * Noisy neighbours: how full the node was, and this pod's share of it.
 * @param {object|null} parsed the result of parseNoisyNeighbours
 * @param {string} [subjectPod] the alerting pod's name
 */
export const summarizeNoisyNeighbours = (parsed, subjectPod) => {
  const allocatable = Number(parsed?.memory?.allocatable);
  if (!(allocatable > 0)) return [];
  const parts = [];
  const requested = Number(parsed.memory.requested);
  if (requested > 0) parts.push(`Node memory ${pct(requested, allocatable)} committed (${gib(requested)} of ${gib(allocatable)})`);
  // Older blocks do not flag the alerting pod's rows; its name identifies them.
  const subject = (parsed.neighbours || []).filter((n) => n?.is_subject || (subjectPod && n?.pod_name === subjectPod));
  const subjectUsed = subject.reduce((sum, n) => sum + (Number(n.memory_used) || 0), 0);
  if (subjectUsed > 0) parts.push(`this pod uses ${pct(subjectUsed, allocatable)} of the node`);
  return parts.length ? [info(parts.join('; '))] : [];
};

/**
 * Resource allocation: the container's peak memory against its limit.
 * @param {{container?: string, request?: number, limits?: number, peakUsage?: number|null, resource_type?: string}[]} items
 *   the card's per-container memory entries, in bytes
 * @param {string} [preferred] the container the alert is about, when known
 */
export const summarizeAllocation = (items, preferred) => {
  const memory = (Array.isArray(items) ? items : []).filter((i) => String(i?.resource_type ?? 'memory').toLowerCase() === 'memory' && i?.container);
  const item = memory.find((i) => i.container === preferred) || memory[0];
  if (!item) return [];
  const limit = Number(item.limits);
  const peak = Number(item.peakUsage);
  if (peak > 0 && limit > 0) return [info(`Container ${item.container} peaked at ${mib(peak)}, ${pct(peak, limit)} of its ${mib(limit)} limit`)];
  if (peak > 0) return [info(`Container ${item.container} peaked at ${mib(peak)} and has no memory limit`)];
  // The request and limit are left to the panel and the card body: this evidence takes them from
  // the workload's spec, which a pod from an older revision does not run with.
  return [info(`Container ${item.container}: no memory samples around the alert`)];
};

/**
 * CPU usage: the peak against the limit, or that there were no samples to draw.
 * @param {{usage?: number[], request?: number[], limit?: number[]}|null} series cores
 */
export const summarizeCpu = (series) => {
  // No usage entry at all means the query was not answered, which is not the same as no samples.
  if (!series || !Array.isArray(series.usage)) return [];
  const peak = peakOf(series.usage);
  if (peak === null) return [info('No CPU samples for this pod in the 10 minutes around the alert')];
  const limit = peakOf(series.limit);
  const request = peakOf(series.request);
  if (limit > 0) return [info(`CPU peaked at ${plain(peak)} cores, ${pct(peak, limit)} of its ${plain(limit)}-core limit`)];
  if (request > 0) return [info(`CPU peaked at ${plain(peak)} cores, ${pct(peak, request)} of its ${plain(request)}-core request; no CPU limit`)];
  return [info(`CPU peaked at ${plain(peak)} cores; no request or limit set`)];
};

/**
 * Metrics explorer: each charted metric's peak and average. The evidence records no unit.
 * @param {{query_key?: string, payload?: {values?: Array}[]}[]} results
 */
export const summarizeQueryMetrics = (results) =>
  (Array.isArray(results) ? results : [])
    .map((result) => {
      const values = (result?.payload || []).flatMap((p) => numbersOf(p?.values));
      if (values.length === 0) return null;
      const average = values.reduce((a, b) => a + b, 0) / values.length;
      return info(`${result.query_key || 'Metric'}: peak ${plain(highestOf(values))}, average ${plain(average)}`);
    })
    .filter(Boolean)
    .slice(0, 3);

/**
 * CloudWatch alarm: the alarm's own reason for its state.
 * @param {object|null} alarm the raw alarm event
 */
export const summarizeCloudWatchAlarm = (alarm) => {
  const reason = alarm?.stateReason || alarm?.state?.reason;
  if (typeof reason === 'string' && reason.trim()) return [info(reason.trim())];
  if (alarm?.metricName && alarm?.threshold !== undefined) {
    return [info(`${alarm.metricName} ${alarm.comparisonOperator || 'crossed'} ${alarm.threshold}`)];
  }
  return [];
};

/**
 * Metrics for a cloud resource: how many metrics are charted and the peak of the one that alarmed.
 * @param {Record<string, {series_list_result?: {metric?: object, values?: Array}[]}>} groups metric_groups
 * @param {string} [alarmMetric] the metric the alarm watches
 */
export const summarizeMetricGroups = (groups, alarmMetric) => {
  const entries = Object.entries(groups && typeof groups === 'object' ? groups : {});
  if (entries.length === 0) return [];
  const count = plural(entries.length, 'metric charted', 'metrics charted');
  // A group is the alarm's metric when its key is that name ("CPUUtilization (Average)") or one of
  // its series is.
  const isAlarmMetric = ([name, group]) =>
    name === alarmMetric || name.startsWith(`${alarmMetric} (`) || (group?.series_list_result || []).some((s) => s?.metric?.name === alarmMetric);
  const alarming = alarmMetric ? entries.find(isAlarmMetric) : null;
  const peak = alarming ? peakOf((alarming[1]?.series_list_result || []).flatMap((s) => s?.values || [])) : null;
  if (alarming && peak !== null) return [info(`${alarming[0]} peaked at ${plain(peak)}; ${count}`)];
  if (alarmMetric) return [info(`${count}; the alarm's own metric, ${alarmMetric}, is not one of them`)];
  const names = entries.map(([name]) => name);
  return [info(`${count}: ${names.slice(0, 3).join(', ')}${names.length > 3 ? ` and ${names.length - 3} more` : ''}`)];
};

/**
 * A query result drawn as series: how many, and the range they cover.
 * @param {{values?: Array}[]} seriesList series_list_result
 * @param {(value: number) => string} [format] formats a value with its unit when the unit is known
 */
export const summarizeSeries = (seriesList, format = plain) => {
  const list = Array.isArray(seriesList) ? seriesList : [];
  const values = list.flatMap((s) => numbersOf(s?.values));
  if (values.length === 0) return [];
  const low = lowestOf(values);
  const high = highestOf(values);
  const range = low === high ? `constant at ${format(high)}` : `between ${format(low)} and ${format(high)}`;
  return [info(`${plural(list.length, 'series', 'series')}, ${range}`)];
};

/**
 * Cloud resource details: the resource, with its status — a deleted or stopped resource is the
 * finding.
 * @param {{name?: string, type?: string, status?: string, region?: string}[]} rows
 */
export const summarizeResources = (rows) => {
  const list = (Array.isArray(rows) ? rows : []).filter((r) => r && typeof r === 'object');
  if (list.length === 0) return [];
  const first = [list[0].name, list[0].type, list[0].status, list[0].region].filter(Boolean).join(' · ');
  if (!first) return [];
  return [info(list.length > 1 ? `${first} and ${list.length - 1} more` : first)];
};

/**
 * Alert labels: which alert this is and how severe its source says it is.
 * @param {{label?: string, value?: string}[]} rows the label table's rows
 */
export const summarizeAlertLabels = (rows) => {
  const list = Array.isArray(rows) ? rows : [];
  const valueOf = (label) => list.find((r) => r?.label === label)?.value;
  const name = valueOf('alertname');
  if (!name) return [];
  const subject = [valueOf('namespace'), valueOf('pod') || valueOf('service') || valueOf('instance')].filter(Boolean).join('/');
  return [info([name, valueOf('severity') ? `severity ${valueOf('severity')}` : '', subject].filter(Boolean).join(' · '))];
};

/**
 * The first line of a description that says something, without markdown marks.
 * @param {string} text
 * @param {number} [max] the longest line to show
 */
export const summarizeDescription = (text, max = 160) => {
  if (typeof text !== 'string') return [];
  for (const raw of text.replace(/%%%/g, '').split('\n')) {
    const line = raw
      .replace(/^[#>\s*-]+/, '')
      .replace(/\*\*/g, '')
      .trim();
    if (line) return [info(line.length > max ? `${line.slice(0, max)}…` : line)];
  }
  return [];
};

/**
 * Whether a summary line is about the named container. Matched on the whole name: a line about
 * "app-sidecar" is not about "app".
 * @param {{message?: string}|string} line
 * @param {string} container
 */
export const mentionsContainer = (line, container) => {
  if (!container) return false;
  const escaped = container.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return new RegExp(`Container ${escaped}(?![\\w.-])`).test(String(line?.message ?? line ?? ''));
};
