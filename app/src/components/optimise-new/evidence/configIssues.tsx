/**
 * Shared rendering + summarisation for K8s misconfiguration "array of findings"
 * payloads. Used on the Details tab (the findings are the recommendation) and
 * the Evidence tab.
 *
 * Two shapes:
 *  - `misconfigurations`: items have `category` (Container/Pod/Security) + an
 *    optional `container`, no severity → grouped by category.
 *  - `popeye/pods/secrets_misconfigurations`: items have `level` (1=Info,
 *    2=Medium, 3=Critical) + `code` (POP) → per-issue severity badge + code,
 *    sorted worst-first.
 * Both are deduped (identical code+message collapse to one "×N" row).
 */
import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
import { Label, type LabelTone } from '@ui/Label';

export const LEVEL_TONE: Record<number, LabelTone> = { 3: 'critical', 2: 'warning', 1: 'info' };
export const LEVEL_NAME: Record<number, string> = { 3: 'Critical', 2: 'Medium', 1: 'Info' };

export interface DedupedIssue {
  code: string;
  message: string;
  level?: number;
  category?: string;
  count: number;
  resources: string[];
  containers: string[];
}

export function dedupeIssues(items: any[]): DedupedIssue[] {
  const map = new Map<string, DedupedIssue & { _res: Set<string>; _cont: Set<string> }>();
  for (const it of items) {
    const code = it.code != null ? String(it.code) : '';
    const message = it.message || 'Unknown issue';
    const key = `${code}|${message}`;
    let e = map.get(key);
    if (!e) {
      e = { code, message, level: undefined, category: it.category, count: 0, resources: [], containers: [], _res: new Set(), _cont: new Set() };
      map.set(key, e);
    }
    e.count++;
    if (it.name) e._res.add(`${it.kind ? `${it.kind}/` : ''}${it.name}`);
    if (it.container) e._cont.add(it.container);
    if (typeof it.level === 'number' && (e.level == null || it.level > e.level)) e.level = it.level;
  }
  return [...map.values()].map(({ _res, _cont, ...rest }) => ({ ...rest, resources: [..._res], containers: [..._cont] }));
}

export interface ConfigIssuesSummary {
  total: number;
  unique: number;
  hasLevels: boolean;
  levelCounts: Record<number, number>;
  categoryCounts: Record<string, number>;
  hasSecurity: boolean;
}

export function summarizeConfigIssues(items: any[]): ConfigIssuesSummary {
  const deduped = dedupeIssues(items);
  const levelCounts: Record<number, number> = { 3: 0, 2: 0, 1: 0 };
  const categoryCounts: Record<string, number> = {};
  let hasLevels = false;
  for (const d of deduped) {
    if (d.level != null) {
      hasLevels = true;
      levelCounts[d.level] = (levelCounts[d.level] || 0) + 1;
    }
    const cat = d.category || 'Other';
    categoryCounts[cat] = (categoryCounts[cat] || 0) + 1;
  }
  const hasSecurity = (categoryCounts.Security || 0) > 0 || levelCounts[3] > 0;
  return { total: items.length, unique: deduped.length, hasLevels, levelCounts, categoryCounts, hasSecurity };
}

const CodeChip = ({ code }: { code: string }) => (
  <Box
    component='span'
    sx={{
      fontFamily: ds.font.mono,
      fontSize: '10px',
      fontWeight: ds.weight.medium,
      color: ds.blue[700],
      backgroundColor: ds.blue[100],
      border: `1px solid ${ds.blue[200]}`,
      borderRadius: ds.radius.sm,
      px: ds.space[1],
      py: '1px',
    }}
  >
    POP-{code}
  </Box>
);

const CountChip = ({ count, unit }: { count: number; unit: string }) => (
  <Box
    component='span'
    sx={{
      fontSize: '10px',
      fontWeight: ds.weight.semibold,
      color: ds.gray[600],
      backgroundColor: ds.gray[100],
      border: `1px solid ${ds.gray[200]}`,
      borderRadius: ds.radius.pill,
      px: ds.space[2],
      py: '1px',
    }}
  >
    ×{count}
    {unit ? ` ${unit}` : ''}
  </Box>
);

const IssueRow = ({ issue, last }: { issue: DedupedIssue; last: boolean }) => {
  const where = issue.containers.length > 0 ? issue.containers.join(', ') : issue.resources[0] || '';
  const dupUnit = issue.containers.length > 1 ? 'containers' : '';
  return (
    <Box
      sx={{
        px: ds.space[3],
        py: ds.space[2],
        borderBottom: last ? 'none' : `1px solid ${ds.gray[200]}`,
        display: 'flex',
        gap: ds.space[2],
        alignItems: 'flex-start',
      }}
    >
      {issue.level != null && (
        <Label size='sm' tone={LEVEL_TONE[issue.level] ?? 'neutral'}>
          {LEVEL_NAME[issue.level] ?? '—'}
        </Label>
      )}
      <Box sx={{ flex: 1, minWidth: 0 }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], flexWrap: 'wrap' }}>
          <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.5 }}>{issue.message}</Typography>
          {issue.code && <CodeChip code={issue.code} />}
          {issue.count > 1 && <CountChip count={issue.count} unit={dupUnit} />}
        </Box>
        {where && <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], fontFamily: ds.font.mono, mt: '2px' }}>{where}</Typography>}
      </Box>
    </Box>
  );
};

const issueCard = {
  backgroundColor: ds.background[100],
  borderRadius: ds.radius.lg,
  overflow: 'hidden',
  border: `1px solid ${ds.gray[200]}`,
} as const;

/** The deduped findings list — severity-sorted (Popeye shape) or category-grouped. */
const ConfigIssuesList = ({ items }: { items: any[] }) => {
  const issues = dedupeIssues(items);
  const hasLevels = issues.some((i) => i.level != null);

  if (hasLevels) {
    const sorted = [...issues].sort((a, b) => (b.level || 0) - (a.level || 0));
    return (
      <Box sx={issueCard}>
        {sorted.map((iss, idx) => (
          <IssueRow key={`${iss.code}|${iss.message}`} issue={iss} last={idx === sorted.length - 1} />
        ))}
      </Box>
    );
  }

  const grouped: Record<string, DedupedIssue[]> = {};
  issues.forEach((i) => {
    const cat = i.category || 'Other';
    (grouped[cat] = grouped[cat] || []).push(i);
  });
  return (
    <>
      {Object.entries(grouped).map(([groupName, groupIssues]) => (
        <Box key={groupName} sx={{ mb: ds.space[3] }}>
          <Typography
            sx={{
              fontSize: ds.text.caption,
              fontWeight: ds.weight.semibold,
              color: ds.gray[500],
              textTransform: 'uppercase',
              letterSpacing: '0.05em',
              mb: '6px',
              mt: ds.space[2],
            }}
          >
            {groupName} ({groupIssues.length})
          </Typography>
          <Box sx={issueCard}>
            {groupIssues.map((iss, idx) => (
              <IssueRow key={`${iss.code}|${iss.message}`} issue={iss} last={idx === groupIssues.length - 1} />
            ))}
          </Box>
        </Box>
      ))}
    </>
  );
};

export default ConfigIssuesList;
