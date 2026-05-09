import { Box, Chip, Tooltip, Typography } from '@mui/material';

const MAX_VISIBLE_TAGS = 2;

export const normalizeTags = (tags: any): { key: string; value: string }[] => {
  if (!tags || typeof tags !== 'object' || Array.isArray(tags)) {
    return [];
  }
  return Object.entries(tags)
    .filter(([key]) => !key.startsWith('nb_'))
    .map(([key, val]: [string, any]) => ({
      key,
      value: Array.isArray(val) ? val.join(', ') : String(val ?? ''),
    }));
};

const TagsCell = ({ tags }: { tags: any }) => {
  const normalizedTags = normalizeTags(tags);
  if (normalizedTags.length === 0) {
    return <Typography sx={{ color: '#9F9F9F', fontSize: 13 }}>-</Typography>;
  }

  const visibleTags = normalizedTags.slice(0, MAX_VISIBLE_TAGS);
  const remainingCount = normalizedTags.length - MAX_VISIBLE_TAGS;

  const tagsTooltipContent = (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '4px', p: '4px' }}>
      {normalizedTags.map((t) => (
        <Box key={t.key} sx={{ display: 'flex', gap: '6px', alignItems: 'baseline' }}>
          <span style={{ fontWeight: 600, color: '#B0BEC5', whiteSpace: 'nowrap' }}>{t.key}</span>
          <span style={{ color: '#fff', wordBreak: 'break-all' }}>{t.value}</span>
        </Box>
      ))}
    </Box>
  );

  return (
    <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: '4px', alignItems: 'center' }}>
      {visibleTags.map((tag) => (
        <Tooltip
          key={tag.key}
          title={
            <Box sx={{ display: 'flex', gap: '6px', alignItems: 'baseline' }}>
              <span style={{ fontWeight: 600, color: '#B0BEC5' }}>{tag.key}</span>
              <span>{tag.value}</span>
            </Box>
          }
          arrow
        >
          <Chip
            label={`${tag.key}=${tag.value}`}
            size='small'
            variant='outlined'
            sx={{
              maxWidth: 120,
              fontSize: 11,
              height: 22,
              '& .MuiChip-label': {
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              },
            }}
          />
        </Tooltip>
      ))}
      {remainingCount > 0 && (
        <Tooltip title={tagsTooltipContent} arrow>
          <Chip
            label={`+${remainingCount} more`}
            size='small'
            sx={{
              fontSize: 11,
              height: 22,
              backgroundColor: '#F5F5F5',
              color: '#616161',
            }}
          />
        </Tooltip>
      )}
    </Box>
  );
};

export default TagsCell;
