import { Chip, type SxProps } from '@mui/material';
import { CATEGORY_LABELS, categoryColors } from './utils';

const DEFAULT_CONFIG = { bg: '#F3F4F6', color: '#374151', border: '#E5E7EB' };

interface CategoryChipProps {
  category: string;
  sx?: SxProps;
}

const CategoryChip = ({ category, sx }: CategoryChipProps) => {
  const config = categoryColors[category] || DEFAULT_CONFIG;

  return (
    <Chip
      label={CATEGORY_LABELS[category] || category}
      size='small'
      sx={{
        fontSize: '10px',
        fontWeight: 500,
        height: '22px',
        backgroundColor: config.bg,
        color: config.color,
        border: `1px solid ${config.border}`,
        '& .MuiChip-label': { px: '8px' },
        ...sx,
      }}
    />
  );
};

export default CategoryChip;
