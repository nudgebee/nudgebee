import React, { useCallback, useRef } from 'react';
import { Box, Typography } from '@mui/material';
import SeverityIcon from '@ui/SeverityIcon';
import { toSeverityLevel } from '@utils/common';
import { SEVERITY_ORDER } from '@api1/vm';
import { ds } from '@utils/colors';

/** Plain table-cell text with an optional second line. Mirrors cloudaccount's CustomText. */
export const CellText = ({ text, subtext, mono = false }: { text?: React.ReactNode; subtext?: React.ReactNode; mono?: boolean }) => (
  <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[0] }}>
    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], fontFamily: mono ? ds.font.mono : undefined, wordBreak: 'break-word' }}>
      {text || '-'}
    </Typography>
    {subtext ? <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], wordBreak: 'break-word' }}>{subtext}</Typography> : null}
  </Box>
);

/**
 * Severity counts as a row of letter badges, worst first. Severities with no
 * findings are dropped rather than rendered as zeros — a VM with 3 Critical and
 * 0 Low should read "3 C", not "3 C 0 H 0 M 0 L".
 */
export const SeverityCounts = ({ counts }: { counts?: Record<string, number> }) => {
  const present = SEVERITY_ORDER.filter((severity) => (counts?.[severity] || 0) > 0);
  if (!present.length) {
    return <Typography sx={{ fontSize: ds.text.small, color: ds.gray[400] }}>—</Typography>;
  }
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], flexWrap: 'wrap' }}>
      {present.map((severity) => (
        <SeverityIcon key={severity} level={toSeverityLevel(severity)} size={14} count={counts?.[severity]} aria-label={severity} />
      ))}
    </Box>
  );
};

export const sumSeverities = (counts?: Record<string, number>) => Object.values(counts || {}).reduce((total, n) => total + (n || 0), 0);

/** How many VM names a grouped cell spells out before it starts counting. */
const VM_NAME_LIMIT = 3;

/**
 * VM names as one cell value, comma separated. A package or a CVE can sit on the
 * whole fleet, so the list is capped and the remainder becomes a count — an
 * unbounded join would push every other column off the row.
 *
 * `nameById` translates resource ids where the caller aggregated ids rather than
 * names; values already in name form pass through untouched.
 */
export const joinVmNames = (values: string[], nameById?: Record<string, string>) => {
  const names = (values || []).map((value) => nameById?.[value] || value);
  if (!names.length) return '';
  if (names.length <= VM_NAME_LIMIT) return names.join(', ');
  return `${names.slice(0, VM_NAME_LIMIT).join(', ')} +${names.length - VM_NAME_LIMIT} more`;
};

/**
 * Guards a fetch-in-effect against out-of-order responses.
 *
 * Changing a filter or page fires a new request while the previous one is still
 * in flight; if the older one resolves last it overwrites the newer result and
 * the table shows the previous page's rows. Call the returned function when a
 * request starts — it stamps that request and hands back a predicate that is
 * true only while it is still the newest.
 *
 *   const beginRequest = useLatestRequest();
 *   const isLatest = beginRequest();
 *   fetch().then((res) => { if (!isLatest()) return; setRows(res); });
 *
 * Same mechanism as PendingFollowUps.jsx's latestRequestRef, factored out
 * because this folder has six of these.
 */
export const useLatestRequest = () => {
  const latest = useRef(0);
  return useCallback(() => {
    const id = ++latest.current;
    return () => id === latest.current;
  }, []);
};
