import { Box, Typography, Divider } from '@mui/material';
import { ds } from 'src/utils/colors';
import Currency from '@shared/format/Currency';

const formatMetricValue = (value: any, unit: string): string => {
  if (value == null || value === '') return '—';
  let formatted: string;
  if (typeof value === 'number') {
    formatted = Number.isInteger(value) ? String(value) : value.toFixed(3);
  } else {
    formatted = String(value);
  }
  return unit ? formatted + ' ' + unit : formatted;
};

export const MetricRow = ({ label, value, unit = '', highlight = false }: { label: string; value: any; unit?: string; highlight?: boolean }) => (
  <Box sx={{ display: 'flex', justifyContent: 'space-between', py: ds.space.mul(0, 3), borderBottom: `1px solid ${ds.gray[200]}` }}>
    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontWeight: ds.weight.medium }}>{label}</Typography>
    <Typography
      sx={{
        fontSize: ds.text.small,
        color: highlight ? ds.green[600] : ds.gray[700],
        fontWeight: ds.weight.semibold,
        maxWidth: '60%',
        textAlign: 'right',
        wordBreak: 'break-word',
      }}
    >
      {formatMetricValue(value, unit)}
    </Typography>
  </Box>
);

export const SectionTitle = ({
  title,
  icon: _icon,
  muiIcon,
  adornment,
}: {
  title: string;
  icon?: string;
  muiIcon?: React.ReactNode;
  adornment?: React.ReactNode;
}) => (
  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3), mt: ds.space[4], mb: ds.space[2] }}>
    {muiIcon && <Box sx={{ display: 'flex', color: ds.gray[500], fontSize: ds.text.title }}>{muiIcon}</Box>}
    <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{title}</Typography>
    {adornment}
  </Box>
);

export const SavingsFooter = ({ savings, note = 'Based on observed usage data' }: { savings: number; note?: string }) => {
  const displayValue = Math.abs(savings);
  let savingsColor: string = ds.gray[700];
  if (savings > 0) savingsColor = ds.green[600];
  else if (savings < 0) savingsColor = ds.red[600];

  return (
    <>
      <Divider sx={{ my: ds.space.mul(0, 7) }} />
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          py: ds.space[1],
          px: ds.space[0],
        }}
      >
        <Box>
          <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[500] }}>Projected Monthly Savings</Typography>
          {note && <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], fontStyle: 'italic' }}>{note}</Typography>}
        </Box>
        <Currency
          value={displayValue}
          precison={2}
          withTooltip={false}
          sx={{
            fontSize: ds.text.title,
            fontWeight: ds.weight.semibold,
            color: savingsColor,
          }}
        />
      </Box>
    </>
  );
};
