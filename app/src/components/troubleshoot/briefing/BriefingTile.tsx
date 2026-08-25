import { Box, Typography } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import Tooltip from '@ui/Tooltip';
import { ds } from 'src/utils/colors';
import type { BriefingTile as BriefingTileModel, TileTone } from './resolveBriefing';

const TONE: Record<TileTone, { background: string; border: string; value: string }> = {
  default: { background: ds.background[200], border: ds.gray[100], value: ds.brand[600] },
  critical: { background: ds.red[100], border: ds.red[200], value: ds.red[600] },
  positive: { background: ds.green[100], border: ds.green[200], value: ds.green[600] },
};

interface Props {
  tile: BriefingTileModel;
  active?: boolean;
  onDrillDown?: (query: Record<string, string>) => void;
}

const BriefingTile = ({ tile, active, onDrillDown }: Props) => {
  const tone = TONE[tile.tone ?? 'default'];
  const clickable = Boolean(tile.drill && onDrillDown);
  const open = () => tile.drill && onDrillDown?.(tile.drill);

  const valueLine = (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'baseline',
        gap: 'var(--ds-space-1)',
        marginTop: '2px',
        minWidth: 0,
        ...(tile.valueTooltip ? { cursor: 'help' } : {}),
      }}
    >
      <Typography
        sx={{
          fontSize: ds.text.title,
          fontWeight: ds.weight.semibold,
          color: tone.value,
          fontVariantNumeric: 'tabular-nums',
          lineHeight: 1.2,
          whiteSpace: 'nowrap',
        }}
      >
        {tile.value}
      </Typography>
      {tile.secondary && (
        <Typography
          sx={{ fontSize: ds.text.caption, color: ds.gray[600], whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', minWidth: 0 }}
        >
          {tile.secondary}
        </Typography>
      )}
    </Box>
  );

  return (
    <Box
      sx={{
        padding: 'var(--ds-space-2) var(--ds-space-3)',
        borderRadius: 'var(--ds-radius-sm)',
        border: `1px solid ${tone.border}`,
        backgroundColor: tone.background,
        minWidth: 0,
        ...(clickable
          ? {
              cursor: 'pointer',
              transition: 'border-color 120ms ease, box-shadow 120ms ease',
              '&:hover': { borderColor: ds.gray[300] },
              '&:focus-visible': { outline: `2px solid ${ds.blue[300]}`, outlineOffset: '1px' },
            }
          : {}),
        ...(active ? { borderColor: ds.blue[300], boxShadow: `inset 0 0 0 1px ${ds.blue[200]}` } : {}),
      }}
      {...(clickable
        ? {
            role: 'button',
            tabIndex: 0,
            onClick: open,
            onKeyDown: (event: React.KeyboardEvent) => {
              if (event.key === 'Enter' || event.key === ' ') {
                event.preventDefault();
                open();
              }
            },
          }
        : {})}
      aria-pressed={clickable ? Boolean(active) : undefined}
      data-testid={`briefing-tile-${tile.key}`}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: '3px', minWidth: 0 }}>
        <Typography
          sx={{
            fontSize: '10px',
            fontWeight: ds.weight.semibold,
            color: ds.gray[700],
            letterSpacing: '0.02em',
            lineHeight: 1.3,
          }}
        >
          {tile.label}
        </Typography>
        {tile.tooltip && (
          <Tooltip title={tile.tooltip}>
            <InfoOutlinedIcon sx={{ fontSize: ds.text.caption, color: ds.gray[400], cursor: 'help', flexShrink: 0 }} />
          </Tooltip>
        )}
      </Box>

      {tile.valueTooltip ? <Tooltip title={tile.valueTooltip}>{valueLine}</Tooltip> : valueLine}
    </Box>
  );
};

export default BriefingTile;
