import { Box, Typography } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import Chip from '@components1/ds/Chip';
import { ds } from '@utils/colors';

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
    return <Typography sx={{ color: ds.gray[400], fontSize: ds.text.body }}>-</Typography>;
  }

  const visibleTags = normalizedTags.slice(0, MAX_VISIBLE_TAGS);
  const remainingCount = normalizedTags.length - MAX_VISIBLE_TAGS;

  const tagsTooltipContent = (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], p: ds.space[1] }}>
      {normalizedTags.map((t) => (
        <Box key={t.key} sx={{ display: 'flex', gap: ds.space[2], alignItems: 'baseline' }}>
          <Box component='span' sx={{ fontWeight: ds.weight.semibold, color: ds.gray[400], whiteSpace: 'nowrap' }}>
            {t.key}
          </Box>
          <Box component='span' sx={{ wordBreak: 'break-all' }}>
            {t.value}
          </Box>
        </Box>
      ))}
    </Box>
  );

  return (
    <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[1], alignItems: 'center' }}>
      {visibleTags.map((tag) => (
        <Tooltip
          key={tag.key}
          title={
            <Box sx={{ display: 'flex', gap: ds.space[2], alignItems: 'baseline' }}>
              <Box component='span' sx={{ fontWeight: ds.weight.semibold, color: ds.gray[400] }}>
                {tag.key}
              </Box>
              <Box component='span'>{tag.value}</Box>
            </Box>
          }
          arrow
        >
          <span>
            <Chip variant='tag' size='xs' tone='neutral'>
              <Box
                component='span'
                sx={{
                  maxWidth: '140px',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  display: 'inline-block',
                }}
              >
                {`${tag.key}=${tag.value}`}
              </Box>
            </Chip>
          </span>
        </Tooltip>
      ))}
      {remainingCount > 0 && (
        <Tooltip title={tagsTooltipContent} arrow>
          <span>
            <Chip variant='count' size='xs' tone='neutral'>
              +{remainingCount} more
            </Chip>
          </span>
        </Tooltip>
      )}
    </Box>
  );
};

export default TagsCell;
