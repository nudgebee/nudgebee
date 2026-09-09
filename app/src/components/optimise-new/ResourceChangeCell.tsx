/**
 * `current → recommended`, with the size of the move.
 *
 * Shared by the recommendation panel (what a right-sizing proposes) and the
 * resolution panel (what an apply attempt actually wrote), so a reader sees one
 * treatment for one kind of fact rather than two that nearly match.
 */
import { Box, Typography } from '@mui/material';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import DragHandleIcon from '@mui/icons-material/DragHandle';
import { ds } from 'src/utils/colors';

export const formatMemValue = (val: number | null | undefined): string => {
  if (val == null) return '—';
  const mi = val / (1024 * 1024);
  if (mi >= 1024) return (mi / 1024).toFixed(1) + ' Gi';
  return Math.round(mi) + ' Mi';
};

export const formatCpuValue = (val: number | null | undefined): string => {
  if (val == null) return '—';
  if (val < 1) return Math.round(val * 1000) + 'm';
  return Number(val).toFixed(3);
};

export const ResourceChangeCell = ({ current, recommended, isMem }: { current: number | null; recommended: number | null; isMem: boolean }) => {
  const fmt = isMem ? formatMemValue : formatCpuValue;
  const isChanged = current != null && recommended != null && current !== recommended;
  const pct = current != null && recommended != null && Math.abs(current) > 1e-10 ? Math.round(((current - recommended) / current) * 100) : null;

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3), flexWrap: 'nowrap' }}>
      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], whiteSpace: 'nowrap' }}>{fmt(current)}</Typography>
      {isChanged ? (
        <ArrowForwardIcon sx={{ fontSize: ds.text.bodyLg, color: ds.gray[400], flexShrink: 0 }} />
      ) : (
        <DragHandleIcon sx={{ fontSize: ds.text.bodyLg, color: ds.gray[400], flexShrink: 0 }} />
      )}
      <Typography
        sx={{
          fontSize: ds.text.small,
          fontWeight: isChanged ? ds.weight.semibold : ds.weight.regular,
          color: ds.gray[700],
          whiteSpace: 'nowrap',
        }}
      >
        {fmt(recommended)}
      </Typography>
      {pct != null && pct !== 0 && (
        <Typography
          sx={{ fontSize: ds.text.caption, color: pct > 0 ? ds.green[600] : ds.red[600], fontWeight: ds.weight.medium, whiteSpace: 'nowrap' }}
        >
          {pct > 0 ? '-' : '+'}
          {Math.abs(pct)}%
        </Typography>
      )}
    </Box>
  );
};
