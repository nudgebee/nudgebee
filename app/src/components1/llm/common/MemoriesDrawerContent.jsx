import { useState, useMemo } from 'react';
import { Box, Typography } from '@mui/material';
import dayjs from 'dayjs';
import PropTypes from 'prop-types';
import { IoIosArrowDown, IoIosArrowUp } from 'react-icons/io';
import { colors } from 'src/utils/colors';
import CustomChip from '@components1/common/CustomChip';

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
const TONES = ['blue', 'green', 'pink', 'lavender'];
const toneFor = (s) => {
  const str = s || '';
  let h = 0;
  for (let i = 0; i < str.length; i++) {
    h = (h * 31 + str.charCodeAt(i)) | 0;
  }
  return TONES[Math.abs(h) % TONES.length];
};

const MemoryCard = ({ memory }) => {
  const [expanded, setExpanded] = useState(false);
  const isLong = (memory.content || '').length > 140;
  const showToggle = isLong;
  const tone = toneFor(memory.memory_type);

  return (
    <Box
      sx={{
        border: `1px solid ${colors.border.secondaryLightest}`,
        borderRadius: '8px',
        backgroundColor: colors.background.white,
        p: '8px 12px',
        mb: '8px',
        transition: 'border-color 0.15s ease',
        '&:hover': {
          borderColor: colors.border.primaryLight,
        },
      }}
    >
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: '8px',
          mb: '4px',
        }}
      >
        <CustomChip variant='tag' tone={tone} label={memory.memory_type || 'memory'} />
        <CustomChip variant='info' label={formatTimestamp(memory.created_at)} />
      </Box>

      <Typography
        sx={{
          fontSize: '12.5px',
          fontFamily: 'Roboto',
          color: colors.text.secondary,
          lineHeight: 1.4,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          ...(expanded
            ? {}
            : {
                display: '-webkit-box',
                WebkitLineClamp: PREVIEW_LINES,
                WebkitBoxOrient: 'vertical',
                overflow: 'hidden',
              }),
        }}
      >
        {memory.content}
      </Typography>

      {showToggle && (
        <Box
          component='button'
          type='button'
          onClick={() => setExpanded((p) => !p)}
          sx={{
            all: 'unset',
            mt: '2px',
            display: 'inline-flex',
            alignItems: 'center',
            gap: '2px',
            cursor: 'pointer',
            fontFamily: 'Roboto',
            fontSize: '11.5px',
            fontWeight: 500,
            color: colors.primary,
            '&:hover': { textDecoration: 'underline' },
          }}
        >
          {expanded ? (
            <>
              Show less <IoIosArrowUp />
            </>
          ) : (
            <>
              Show more <IoIosArrowDown />
            </>
          )}
        </Box>
      )}
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
          fontSize: '13px',
          color: colors.text.tertiary,
          fontFamily: 'Roboto',
          textAlign: 'center',
          mt: '24px',
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
            gap: '6px',
            mb: '12px',
            pb: '10px',
            borderBottom: `1px solid ${colors.border.secondaryLightest}`,
          }}
        >
          <CustomChip variant='filter' label='All' selected={filterType === 'all'} onClick={() => setFilterType('all')} />
          {memoryTypes.map((t) => (
            <CustomChip key={t} variant='filter' label={t} selected={filterType === t} onClick={() => setFilterType(t)} />
          ))}
        </Box>
      )}

      {filtered.length === 0 ? (
        <Typography
          sx={{
            fontSize: '13px',
            color: colors.text.tertiary,
            fontFamily: 'Roboto',
            textAlign: 'center',
            mt: '12px',
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
