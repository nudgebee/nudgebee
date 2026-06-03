import { useEffect, useState, useCallback, memo, useMemo } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography, Alert } from '@mui/material';
import WidgetCard from '@components1/ds/WidgetCard';
import PushPinIcon from '@mui/icons-material/PushPin';
import PushPinOutlinedIcon from '@mui/icons-material/PushPinOutlined';
import CheckIcon from '@mui/icons-material/Check';
import { Link } from '@components1/ds/Link';
import api from '@api1/ask-nudgebee';
import Loader from '@components1/common/Loader';
import { toast as snackbar } from '@components1/ds/Toast';
import { Modal } from '@components1/ds/Modal';
import { Button } from '@components1/ds/Button';
import CustomSearch from '@common-new/CustomSearch';
import { Checkbox } from '@components1/ds/Checkbox';
import { ds } from '@utils/colors';

const formatDate = (dateString) => {
  if (!dateString) {
    return '-';
  }
  const date = new Date(dateString);
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
};

// Get type chip styles based on memory type
const getTypeChipStyles = (memoryType) => {
  const typeStr = (memoryType || 'general').toLowerCase();

  if (typeStr === 'investigation_result') {
    return {
      backgroundColor: 'var(--ds-blue-100)',
      border: `1px solid ${'var(--ds-blue-200)'}`,
      color: 'var(--ds-blue-600)',
    };
  }

  if (typeStr === 'configuration_insight') {
    return {
      backgroundColor: 'var(--ds-amber-100)',
      border: '1px solid var(--ds-amber-300)',
      color: 'var(--ds-amber-700)',
    };
  }

  if (typeStr === 'user_preference') {
    return {
      backgroundColor: 'var(--ds-green-100)',
      border: `1px solid ${'var(--ds-green-300)'}`,
      color: 'var(--ds-green-700)',
    };
  }

  // Default neutral grey for all other types
  return {
    backgroundColor: 'var(--ds-background-200)',
    border: `1px solid ${'var(--ds-gray-300)'}`,
    color: 'var(--ds-gray-700)',
  };
};

const MemoryRow = memo(({ memory, accountId, isPinned, onTogglePin }) => {
  const chipStyles = getTypeChipStyles(memory.memory_type);

  const handlePinClick = (e) => {
    e.stopPropagation();
    onTogglePin(memory.id);
  };

  const conversationLink = `/ask-nudgebee?accountId=${accountId}&conversation_id=${memory.conversation_id}`;

  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'stretch',
        borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
      }}
    >
      {/* Main content */}
      <Box
        sx={{
          flex: 1,
          display: 'flex',
          flexDirection: 'column',
          padding: `${ds.space[3]} ${ds.space[4]}`,
          minWidth: 0,
        }}
      >
        <Box
          sx={{
            display: 'flex',
            alignItems: 'flex-start',
            gap: ds.space[2],
            flex: 1,
            minWidth: 0,
          }}
        >
          {/* Type chip */}
          <Box
            sx={{
              display: 'inline-flex',
              flexDirection: 'column',
              alignItems: 'flex-start',
              gap: ds.space[1],
              flexShrink: 0,
              width: ds.space.mul(2, 15),
            }}
          >
            <Box
              sx={{
                display: 'inline-flex',
                alignItems: 'center',
                px: ds.space.mul(0, 3),
                py: ds.space[0],
                borderRadius: ds.radius.sm,
                ...chipStyles,
              }}
            >
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-caption)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: 'inherit',
                  whiteSpace: 'nowrap',
                }}
              >
                {memory.memory_type || 'General'}
              </Typography>
            </Box>
            {/* Date below type chip */}
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                color: 'var(--ds-gray-500)',
                fontWeight: 'var(--ds-font-weight-regular)',
              }}
            >
              {formatDate(memory.created_at)}
            </Typography>
          </Box>

          {/* Memory content - clamped to 2 lines */}
          <Typography
            sx={{
              flex: 1,
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-700)',
              overflow: 'hidden',
              display: '-webkit-box',
              WebkitLineClamp: 2,
              WebkitBoxOrient: 'vertical',
              lineHeight: '1.4',
              pr: ds.space.mul(1, 15),
            }}
          >
            {memory.content}
          </Typography>
        </Box>
      </Box>

      {/* Right icons */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: ds.space[1],
          paddingRight: ds.space[4],
          paddingLeft: ds.space[2],
          flexShrink: 0,
        }}
      >
        {/* Open Source Conversation button */}
        {memory.conversation_id && (
          <Link href={conversationLink} openInNew>
            View conversation
          </Link>
        )}

        {/* Pin icon (functional) */}
        <Button
          tone='secondary'
          size='sm'
          icon={
            isPinned ? (
              <PushPinIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-red-400)' }} />
            ) : (
              <PushPinOutlinedIcon sx={{ fontSize: 'var(--ds-text-title)', color: 'var(--ds-pink-300)' }} />
            )
          }
          onClick={handlePinClick}
        />
      </Box>
    </Box>
  );
});

MemoryRow.displayName = 'MemoryRow';
MemoryRow.propTypes = {
  memory: PropTypes.object.isRequired,
  accountId: PropTypes.string.isRequired,
  isPinned: PropTypes.bool.isRequired,
  onTogglePin: PropTypes.func.isRequired,
};

const MEMORY_TYPES = [
  { value: 'ALL', label: 'All Types', bgColor: 'var(--ds-background-200)', borderColor: 'var(--ds-gray-300)' },
  {
    value: 'investigation_result',
    label: 'Investigation Result',
    bgColor: 'var(--ds-background-200)',
    borderColor: 'var(--ds-gray-300)',
  },
  {
    value: 'architectural_fact',
    label: 'Architectural Fact',
    bgColor: 'var(--ds-background-200)',
    borderColor: 'var(--ds-gray-300)',
  },
  {
    value: 'dependency_mapping',
    label: 'Dependency Mapping',
    bgColor: 'var(--ds-background-200)',
    borderColor: 'var(--ds-gray-300)',
  },
  {
    value: 'troubleshooting_guide',
    label: 'Troubleshooting Guide',
    bgColor: 'var(--ds-background-200)',
    borderColor: 'var(--ds-gray-300)',
  },
  {
    value: 'configuration_insight',
    label: 'Configuration Insight',
    bgColor: 'var(--ds-background-200)',
    borderColor: 'var(--ds-gray-300)',
  },
  {
    value: 'user_preference',
    label: 'User Preference',
    bgColor: 'var(--ds-background-200)',
    borderColor: 'var(--ds-gray-300)',
  },
  { value: 'pattern', label: 'Pattern', bgColor: 'var(--ds-background-200)', borderColor: 'var(--ds-gray-300)' },
  { value: 'workflow', label: 'Automation', bgColor: 'var(--ds-background-200)', borderColor: 'var(--ds-gray-300)' },
];

// TypeChipFilter component
const TypeChipFilter = ({ value, options, onChange }) => (
  <Box sx={{ display: 'flex', gap: ds.space.mul(0, 3), flexWrap: 'wrap', alignItems: 'center' }}>
    {options.map((option) => {
      const isSelected = value === option.value;
      return (
        <Box
          key={option.value}
          onClick={() => onChange(option.value)}
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: ds.space[1],
            padding: `${ds.space[1]} ${ds.space[2]}`,
            borderRadius: ds.radius.xl,
            backgroundColor: isSelected ? option.bgColor : 'transparent',
            border: `1px solid ${option.borderColor}`,
            cursor: 'pointer',
            transition: 'all 0.2s ease',
            '&:hover': {
              backgroundColor: option.bgColor,
            },
          }}
        >
          {/* Checkbox icon for selected */}
          {isSelected && (
            <CheckIcon
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                color: 'var(--ds-blue-600)',
                flexShrink: 0,
              }}
            />
          )}
          {/* Label */}
          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              fontWeight: isSelected ? 600 : 400,
              color: isSelected ? 'var(--ds-blue-600)' : 'var(--ds-gray-700)',
            }}
          >
            {option.label}
          </Typography>
        </Box>
      );
    })}
  </Box>
);

TypeChipFilter.propTypes = {
  value: PropTypes.string.isRequired,
  options: PropTypes.arrayOf(
    PropTypes.shape({
      value: PropTypes.string.isRequired,
      label: PropTypes.string.isRequired,
      bgColor: PropTypes.string.isRequired,
      borderColor: PropTypes.string.isRequired,
    })
  ).isRequired,
  onChange: PropTypes.func.isRequired,
};

const MemoryTab = ({ accountId }) => {
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [memories, setMemories] = useState([]);
  const [error, setError] = useState(null);
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [selectedMemory, setSelectedMemory] = useState(null);
  const [memoryType, setMemoryType] = useState('ALL');
  const [searchQuery, setSearchQuery] = useState('');
  const [committedSearchQuery, setCommittedSearchQuery] = useState('');
  const [filterPinned, setFilterPinned] = useState(false);
  const [pinnedMemories, setPinnedMemories] = useState(() => {
    if (typeof window !== 'undefined') {
      const stored = localStorage.getItem(`nudgebee_pinned_memories_${accountId}`);
      return new Set(stored ? JSON.parse(stored) : []);
    }
    return new Set();
  });

  const fetchMemories = async () => {
    if (!accountId) {
      setError('Account ID is required');
      setLoading(false);
      return;
    }

    try {
      setLoading(true);
      const typeParam = memoryType === 'ALL' ? undefined : memoryType;
      const queryParam = committedSearchQuery.trim() === '' ? undefined : committedSearchQuery.trim();
      const response = await api.listMemory(accountId, undefined, undefined, typeParam, queryParam);
      if (!response || (response.errors && response.errors.length > 0)) {
        setMemories([]);
        setError('Failed to fetch memories');
        snackbar.error('Failed to fetch memories');
      } else {
        setMemories(response.data || []);
        setError(null);
      }
    } catch (err) {
      console.error('Error fetching memories:', err);
      setError('An error occurred while fetching memories');
      snackbar.error('An error occurred while fetching memories');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchMemories();
  }, [accountId, memoryType, committedSearchQuery]);

  const handleConfirmDelete = async () => {
    if (!selectedMemory) {
      return;
    }

    try {
      setSubmitting(true);
      const response = await api.deleteMemory(accountId, selectedMemory.id);

      if (!response || (response.errors && response.errors.length > 0)) {
        const errorMessage = response?.errors?.[0]?.message || 'Failed to delete memory';
        snackbar.error(errorMessage);
        return;
      }

      snackbar.success('Memory deleted successfully');
      setDeleteModalOpen(false);
      setSelectedMemory(null);
      fetchMemories();
    } catch (err) {
      console.error('Error deleting memory:', err);
      snackbar.error('An error occurred while deleting the memory');
    } finally {
      setSubmitting(false);
    }
  };

  const handleTogglePin = useCallback((memoryId) => {
    setPinnedMemories((prev) => {
      const updated = new Set(prev);
      if (updated.has(memoryId)) {
        updated.delete(memoryId);
      } else {
        updated.add(memoryId);
      }
      return updated;
    });
  }, []);

  useEffect(() => {
    if (typeof window !== 'undefined') {
      localStorage.setItem(`nudgebee_pinned_memories_${accountId}`, JSON.stringify(Array.from(pinnedMemories)));
    }
  }, [pinnedMemories, accountId]);

  const filteredMemories = useMemo(
    () =>
      memories.filter((memory) => {
        if (memoryType !== 'ALL' && memory.memory_type !== memoryType) return false;
        if (filterPinned && !pinnedMemories.has(memory.id)) return false;
        return true;
      }),
    [memories, memoryType, filterPinned, pinnedMemories]
  );

  const isMemoryVisible = useCallback(
    (memory) => {
      if (memoryType !== 'ALL' && memory.memory_type !== memoryType) return false;
      if (filterPinned && !pinnedMemories.has(memory.id)) return false;
      return true;
    },
    [memoryType, filterPinned, pinnedMemories]
  );

  const thisWeekCount = useMemo(() => {
    const now = new Date();
    const oneWeekAgo = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
    return memories.filter((m) => {
      if (!m.created_at) return false;
      const d = new Date(m.created_at);
      return d >= oneWeekAgo && d <= now;
    }).length;
  }, [memories]);

  let memoryContent;
  if (loading) {
    memoryContent = (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', maxHeight: ds.space.mul(1, 75) }}>
        <Loader />
      </Box>
    );
  } else if (error) {
    memoryContent = (
      <Box sx={{ p: ds.space[5] }}>
        <Alert severity='error'>{error}</Alert>
      </Box>
    );
  } else {
    memoryContent = (
      <>
        {/* Empty State */}
        {filteredMemories.length === 0 && (
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              justifyContent: 'center',
              padding: `${ds.space[7]} ${ds.space[5]}`,
              marginBottom: ds.space[3],
              border: `1px dashed var(--ds-gray-300)`,
              borderRadius: ds.radius.lg,
              backgroundColor: 'var(--ds-background-200)',
            }}
          >
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                color: 'var(--ds-gray-700)',
                mb: ds.space[2],
              }}
            >
              No memories found
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-small)',
                color: 'var(--ds-gray-500)',
                textAlign: 'center',
              }}
            >
              {filterPinned
                ? 'No pinned memories yet. Click the pin icon to save memories for quick access.'
                : 'Memories will appear here as the AI learns from your conversations.'}
            </Typography>
          </Box>
        )}

        {/* Memory List — always mounted, hidden via CSS to avoid remount cost on filter toggle */}
        {memories.length > 0 && (
          <Box sx={{ overflowAnchor: 'none' }}>
            {memories.map((memory) => (
              <Box key={memory.id} sx={{ display: isMemoryVisible(memory) ? undefined : 'none' }}>
                <MemoryRow memory={memory} accountId={accountId} isPinned={pinnedMemories.has(memory.id)} onTogglePin={handleTogglePin} />
              </Box>
            ))}
          </Box>
        )}
      </>
    );
  }

  return (
    <Box sx={{ p: 0 }}>
      {/* Header Section with Title and Description */}
      <WidgetCard
        sx={{
          p: `${ds.space[4]} ${ds.space.mul(1, 5)}`,
          mt: 0,
          mb: ds.space[4],
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-start',
        }}
      >
        <Box>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              color: 'var(--ds-gray-700)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              fontFamily: 'Poppins',
            }}
          >
            Memory
          </Typography>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-gray-500)',
            }}
          >
            Nubi has saved from past investigations. Pin the ones that matter most, remove anything outdated, and Nubi will use these to resolve
            similar issues faster
          </Typography>
        </Box>
      </WidgetCard>

      {/* Search Bar and Summary Stats */}
      <Box
        sx={{
          display: 'flex',
          p: `0 ${ds.space[3]}`,
          gap: ds.space[5],
          mb: ds.space.mul(0, 5),
          alignItems: 'center',
          justifyContent: 'space-between',
        }}
      >
        {/* Summary Stats Line */}
        {memories.length > 0 && (
          <Box sx={{ flexShrink: 0 }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body)',
                color: 'var(--ds-gray-700)',
                fontWeight: 'var(--ds-font-weight-regular)',
                whiteSpace: 'nowrap',
              }}
            >
              <span style={{ color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-medium)' }}>{memories.length} memories</span>
              {thisWeekCount > 0 && (
                <>
                  <span> · </span>
                  <span style={{ color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-regular)' }}>{thisWeekCount} created this week</span>
                </>
              )}
            </Typography>
          </Box>
        )}
        {/* Search Box */}
        <Box sx={{ flexShrink: 0, ml: 'auto', display: 'flex', alignItems: 'center', gap: ds.space[3] }}>
          <Checkbox checked={filterPinned} onChange={(next) => setFilterPinned(next)} label='Pinned' size='sm' />
          <CustomSearch
            label='Search memories...'
            value={searchQuery}
            onChange={(value) => {
              if (committedSearchQuery != '' && value === '') {
                setCommittedSearchQuery('');
              }
              setSearchQuery(value);
            }}
            onEnterPress={() => {
              setCommittedSearchQuery(searchQuery);
            }}
            onClear={() => {
              setSearchQuery('');
              setCommittedSearchQuery('');
            }}
            minWidth={ds.space.mul(0, 125)}
            maxWidth='100%'
          />
        </Box>
      </Box>

      {/* Filters: Type Chips and Pinned Checkbox */}
      <Box sx={{ display: 'flex', p: `0 ${ds.space[3]}`, gap: ds.space[4], mb: ds.space[4], alignItems: 'center', justifyContent: 'space-between' }}>
        {/* Left side: Type filter chips */}
        <Box sx={{ flex: 1 }}>
          <TypeChipFilter value={memoryType} options={MEMORY_TYPES} onChange={(value) => setMemoryType(value)} />
        </Box>
      </Box>

      {/* Memory List Container with Padding */}
      <Box>{memoryContent}</Box>

      {/* Delete Confirmation Modal */}
      <Modal
        open={deleteModalOpen}
        handleClose={() => {
          setDeleteModalOpen(false);
          setSelectedMemory(null);
        }}
        title='Delete Memory'
        width='sm'
      >
        <Box sx={{ padding: ds.space[5] }}>
          <Typography variant='body1' sx={{ mb: ds.space[4] }}>
            Are you sure you want to delete this memory?
          </Typography>
          <Typography variant='body2' sx={{ color: 'var(--ds-gray-500)', mb: ds.space[5] }}>
            This action cannot be undone. The AI will no longer have access to this information.
          </Typography>
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3] }}>
            <Button
              tone='secondary'
              size='sm'
              onClick={() => {
                setDeleteModalOpen(false);
                setSelectedMemory(null);
              }}
              disabled={submitting}
            >
              Cancel
            </Button>
            <Button tone='danger' size='sm' onClick={handleConfirmDelete} loading={submitting}>
              Delete
            </Button>
          </Box>
        </Box>
      </Modal>
    </Box>
  );
};

MemoryTab.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default MemoryTab;
