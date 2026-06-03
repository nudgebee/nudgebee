import { useState, useMemo } from 'react';
import { Box, Typography } from '@mui/material';
import dayjs from 'dayjs';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';
import { Chip } from '@components1/ds/Chip';
import Text from '@common-new/format/Text';

const formatTimestamp = (iso) => {
  const d = dayjs(iso);
  if (!d.isValid()) {
    return '';
  }
  return d.format('HH:mm DD-MMM');
};

const PREVIEW_LINES = 2;

// Deterministic mapping from a memory_type string → one of the four pastel tones,
// so the same type always gets the same colour across the page.
const HUES = ['blue', 'green', 'pink', 'violet'];
const hueFor = (s) => {
  const str = s || '';
  let h = 0;
  for (let i = 0; i < str.length; i++) {
    h = (h * 31 + str.charCodeAt(i)) | 0;
  }
  return HUES[Math.abs(h) % HUES.length];
};

const MemoryCard = ({ memory }) => {
  const hue = hueFor(memory.memory_type);

  return (
    <Box
      sx={{
        border: `1px solid ${'var(--ds-gray-200)'}`,
        borderRadius: ds.radius.lg,
        backgroundColor: 'var(--ds-background-100)',
        p: `${ds.space[2]} ${ds.space[3]}`,
        mb: ds.space[2],
        transition: 'border-color 0.15s ease',
        '&:hover': {
          borderColor: 'var(--ds-blue-200)',
        },
      }}
    >
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: ds.space[2],
          mb: ds.space[1],
        }}
      >
        <Chip variant='tag' hue={hue} size='xs'>
          {memory.memory_type || 'memory'}
        </Chip>
        <Chip variant='tag' tone='neutral' size='xs'>
          {formatTimestamp(memory.created_at)}
        </Chip>
      </Box>

      <Text
        value={memory.content || ''}
        showAutoEllipsis
        lineClamp={PREVIEW_LINES}
        sx={{
          fontSize: 'var(--ds-text-small)',
          fontFamily: ds.font.sans,
          color: 'var(--ds-gray-700)',
          lineHeight: 1.4,
          whiteSpace: 'pre-wrap',
        }}
      />
    </Box>
  );
};

MemoryCard.propTypes = {
  memory: PropTypes.object.isRequired,
};

const MemoriesDrawerContent = ({ memories }) => {
  const [filterType, setFilterType] = useState('all');

  const memoryTypes = useMemo(() => {
    const types = new Set();
    (memories || []).forEach((m) => {
      if (m.memory_type) {
        types.add(m.memory_type);
      }
    });
    return Array.from(types);
  }, [memories]);

  const filtered = useMemo(() => {
    if (filterType === 'all') {
      return memories || [];
    }
    return (memories || []).filter((m) => m.memory_type === filterType);
  }, [memories, filterType]);

  if (!memories || memories.length === 0) {
    return (
      <Typography
        sx={{
          fontSize: 'var(--ds-text-body)',
          color: 'var(--ds-gray-500)',
          fontFamily: ds.font.sans,
          textAlign: 'center',
          mt: ds.space[5],
        }}
      >
        No memories captured for this response.
      </Typography>
    );
  }

  return (
    <Box>
      {memoryTypes.length > 1 && (
        <Box
          sx={{
            display: 'flex',
            flexWrap: 'wrap',
            gap: ds.space.mul(0, 3),
            mb: ds.space[3],
            pb: ds.space.mul(0, 5),
            borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
          }}
        >
          <Chip variant='filter' size='xs' selected={filterType === 'all'} onClick={() => setFilterType('all')}>
            All
          </Chip>
          {memoryTypes.map((t) => (
            <Chip key={t} variant='filter' size='xs' selected={filterType === t} onClick={() => setFilterType(t)}>
              {t}
            </Chip>
          ))}
        </Box>
      )}

      {filtered.length === 0 ? (
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body)',
            color: 'var(--ds-gray-500)',
            fontFamily: ds.font.sans,
            textAlign: 'center',
            mt: ds.space[3],
          }}
        >
          No memories match this filter.
        </Typography>
      ) : (
        filtered.map((m) => <MemoryCard key={m.id || m.created_at} memory={m} />)
      )}
    </Box>
  );
};

MemoriesDrawerContent.propTypes = {
  memories: PropTypes.array.isRequired,
};

export default MemoriesDrawerContent;
