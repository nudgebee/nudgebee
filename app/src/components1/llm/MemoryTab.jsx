import { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography, Alert } from '@mui/material';
import WidgetCard from '@components1/common/WidgetCard';
import PushPinIcon from '@mui/icons-material/PushPin';
import PushPinOutlinedIcon from '@mui/icons-material/PushPinOutlined';
import CheckIcon from '@mui/icons-material/Check';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import Link from 'next/link';
import { colors } from 'src/utils/colors';
import api from '@api1/ask-nudgebee';
import Loader from '@components1/common/Loader';
import { snackbar } from '@components1/common/snackbarService';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
import CustomSearch from '@components1/common/CustomSearch';
import CustomCheckBox from '@components1/common/CustomCheckbox';

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
      backgroundColor: colors.background.primaryLightest,
      border: `1px solid ${colors.border.primaryLight}`,
      color: colors.primary,
    };
  }

  if (typeStr === 'configuration_insight') {
    return {
      backgroundColor: colors.background.warningLight,
      border: '1px solid #FCD34D',
      color: colors.text.warning,
    };
  }

  if (typeStr === 'user_preference') {
    return {
      backgroundColor: '#F0FDF4',
      border: `1px solid ${colors.done}`,
      color: '#166534',
    };
  }

  // Default neutral grey for all other types
  return {
    backgroundColor: colors.background.suggestionCardBG,
    border: `1px solid ${colors.border.nudgebeeSuggestionHover}`,
    color: colors.text.secondary,
  };
};

const MemoryRow = ({ memory, accountId, isPinned, onTogglePin }) => {
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
        borderBottom: `1px solid ${colors.border.secondaryLightest}`,
      }}
    >
      {/* Main content */}
      <Box
        sx={{
          flex: 1,
          display: 'flex',
          flexDirection: 'column',
          padding: '12px 16px',
          minWidth: 0,
        }}
      >
        <Box
          sx={{
            display: 'flex',
            alignItems: 'flex-start',
            gap: '8px',
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
              gap: '4px',
              flexShrink: 0,
              width: '120px',
            }}
          >
            <Box
              sx={{
                display: 'inline-flex',
                alignItems: 'center',
                px: 0.75,
                py: 0.25,
                borderRadius: '4px',
                ...chipStyles,
              }}
            >
              <Typography
                sx={{
                  fontSize: '10px',
                  fontWeight: 600,
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
                fontSize: '10px',
                color: colors.text.tertiary,
                fontWeight: 400,
              }}
            >
              {formatDate(memory.created_at)}
            </Typography>
          </Box>

          {/* Memory content - clamped to 2 lines */}
          <Typography
            sx={{
              flex: 1,
              fontSize: '12px',
              color: colors.text.secondary,
              overflow: 'hidden',
              display: '-webkit-box',
              WebkitLineClamp: 2,
              WebkitBoxOrient: 'vertical',
              lineHeight: '1.4',
              pr: '60px',
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
          gap: '4px',
          paddingRight: '16px',
          paddingLeft: '8px',
          flexShrink: 0,
        }}
      >
        {/* Open Source Conversation button */}
        {memory.conversation_id && (
          <Link href={conversationLink} target='_blank' style={{ textDecoration: 'none' }}>
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                padding: '4px 8px',
                borderRadius: '4px',
                transition: 'all 0.2s',
                '&:hover': {
                  backgroundColor: colors.background.tertiaryLightest,
                },
              }}
            >
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '3px' }}>
                <OpenInNewIcon sx={{ fontSize: '16px', color: colors.primary }} />
              </Box>
            </Box>
          </Link>
        )}

        {/* Pin icon (functional) */}
        <Box
          onClick={handlePinClick}
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            cursor: 'pointer',
            padding: '4px',
            borderRadius: '4px',
          }}
        >
          {isPinned ? (
            <PushPinIcon sx={{ fontSize: '18px', color: colors.background.activeTabIndicator, flexShrink: 0 }} />
          ) : (
            <PushPinOutlinedIcon sx={{ fontSize: '18px', color: '#FBCFE8', flexShrink: 0 }} />
          )}
        </Box>
      </Box>
    </Box>
  );
};

MemoryRow.propTypes = {
  memory: PropTypes.object.isRequired,
  accountId: PropTypes.string.isRequired,
  isPinned: PropTypes.bool.isRequired,
  onTogglePin: PropTypes.func.isRequired,
};

const MEMORY_TYPES = [
  { value: 'ALL', label: 'All Types', bgColor: colors.background.suggestionCardHover, borderColor: colors.border.nudgebeeSuggestionHover },
  {
    value: 'investigation_result',
    label: 'Investigation Result',
    bgColor: colors.background.suggestionCardHover,
    borderColor: colors.border.nudgebeeSuggestionHover,
  },
  {
    value: 'architectural_fact',
    label: 'Architectural Fact',
    bgColor: colors.background.suggestionCardHover,
    borderColor: colors.border.nudgebeeSuggestionHover,
  },
  {
    value: 'dependency_mapping',
    label: 'Dependency Mapping',
    bgColor: colors.background.suggestionCardHover,
    borderColor: colors.border.nudgebeeSuggestionHover,
  },
  {
    value: 'troubleshooting_guide',
    label: 'Troubleshooting Guide',
    bgColor: colors.background.suggestionCardHover,
    borderColor: colors.border.nudgebeeSuggestionHover,
  },
  {
    value: 'configuration_insight',
    label: 'Configuration Insight',
    bgColor: colors.background.suggestionCardHover,
    borderColor: colors.border.nudgebeeSuggestionHover,
  },
  {
    value: 'user_preference',
    label: 'User Preference',
    bgColor: colors.background.suggestionCardHover,
    borderColor: colors.border.nudgebeeSuggestionHover,
  },
  { value: 'pattern', label: 'Pattern', bgColor: colors.background.suggestionCardHover, borderColor: colors.border.nudgebeeSuggestionHover },
  { value: 'workflow', label: 'Automation', bgColor: colors.background.suggestionCardHover, borderColor: colors.border.nudgebeeSuggestionHover },
];

// TypeChipFilter component
const TypeChipFilter = ({ value, options, onChange }) => (
  <Box sx={{ display: 'flex', gap: '6px', flexWrap: 'wrap', alignItems: 'center' }}>
    {options.map((option) => {
      const isSelected = value === option.value;
      return (
        <Box
          key={option.value}
          onClick={() => onChange(option.value)}
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '4px',
            padding: '4px 8px',
            borderRadius: '12px',
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
                fontSize: '14px',
                color: colors.primary,
                flexShrink: 0,
              }}
            />
          )}
          {/* Label */}
          <Typography
            sx={{
              fontSize: '11px',
              fontWeight: isSelected ? 600 : 400,
              color: isSelected ? colors.primary : colors.text.secondary,
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

  const handleTogglePin = (memoryId) => {
    setPinnedMemories((prev) => {
      const updated = new Set(prev);
      if (updated.has(memoryId)) {
        updated.delete(memoryId);
      } else {
        updated.add(memoryId);
      }
      if (typeof window !== 'undefined') {
        localStorage.setItem(`nudgebee_pinned_memories_${accountId}`, JSON.stringify(Array.from(updated)));
      }
      return updated;
    });
  };

  // Filter memories based on selected type and pinned status
  const filteredMemories = memories.filter((memory) => {
    // Apply type filter
    if (memoryType !== 'ALL' && memory.memory_type !== memoryType) {
      return false;
    }
    // Apply pinned filter
    if (filterPinned && !pinnedMemories.has(memory.id)) {
      return false;
    }
    return true;
  });

  // Get memories from this week
  const getThisWeekMemories = () => {
    const now = new Date();
    const oneWeekAgo = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
    return memories.filter((memory) => {
      if (!memory.created_at) return false;
      const memoryDate = new Date(memory.created_at);
      return memoryDate >= oneWeekAgo && memoryDate <= now;
    });
  };

  const thisWeekMemories = getThisWeekMemories();
  const thisWeekCount = thisWeekMemories.length;

  let memoryContent;
  if (loading) {
    memoryContent = (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', maxHeight: '300px' }}>
        <Loader />
      </Box>
    );
  } else if (error) {
    memoryContent = (
      <Box sx={{ p: 3 }}>
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
              padding: '48px 24px',
              marginBottom: '12px',
              border: `1px dashed ${colors.border.secondary}`,
              borderRadius: '8px',
              backgroundColor: colors.background.tertiaryLightest,
            }}
          >
            <Typography
              sx={{
                fontSize: '13px',
                color: colors.text.secondary,
                mb: 1,
              }}
            >
              No memories found
            </Typography>
            <Typography
              sx={{
                fontSize: '12px',
                color: colors.text.tertiary,
                textAlign: 'center',
              }}
            >
              {filterPinned
                ? 'No pinned memories yet. Click the pin icon to save memories for quick access.'
                : 'Memories will appear here as the AI learns from your conversations.'}
            </Typography>
          </Box>
        )}

        {/* Memory List */}
        {filteredMemories.length > 0 && (
          <Box>
            {filteredMemories.map((memory) => (
              <MemoryRow
                key={memory.id}
                memory={memory}
                accountId={accountId}
                isPinned={pinnedMemories.has(memory.id)}
                onTogglePin={handleTogglePin}
              />
            ))}
          </Box>
        )}
      </>
    );
  }

  return (
    <Box sx={{ p: 0 }}>
      {/* Header Section with Title and Description */}
      <WidgetCard sx={{ p: '16px 20px', mt: 0, mb: 2, display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
        <Box>
          <Typography
            sx={{
              fontSize: '14px',
              color: colors.text.secondary,
              fontWeight: 600,
              fontFamily: 'Poppins',
            }}
          >
            Memory
          </Typography>
          <Typography
            sx={{
              fontSize: '12px',
              color: colors.text.tertiary,
            }}
          >
            Nubi has saved from past investigations. Pin the ones that matter most, remove anything outdated, and Nubi will use these to resolve
            similar issues faster
          </Typography>
        </Box>
      </WidgetCard>

      {/* Search Bar and Summary Stats */}
      <Box sx={{ display: 'flex', p: '0px 12px', gap: 3, mb: '10px', alignItems: 'center', justifyContent: 'space-between' }}>
        {/* Summary Stats Line */}
        {memories.length > 0 && (
          <Box sx={{ flexShrink: 0 }}>
            <Typography sx={{ fontSize: '13px', color: colors.text.secondary, fontWeight: 400, whiteSpace: 'nowrap' }}>
              <span style={{ color: colors.text.secondary, fontWeight: 500 }}>{memories.length} memories</span>
              {thisWeekCount > 0 && (
                <>
                  <span> · </span>
                  <span style={{ color: colors.text.secondary, fontWeight: 400 }}>{thisWeekCount} created this week</span>
                </>
              )}
            </Typography>
          </Box>
        )}
        {/* Search Box */}
        <Box sx={{ flexShrink: 0 }}>
          <CustomCheckBox
            checked={filterPinned}
            onChange={(e) => setFilterPinned(e.target.checked)}
            text='Pinned'
            sx={{ fontSize: '12px', flexShrink: 0 }}
          />
          <CustomSearch
            label='Search memories...'
            value={searchQuery}
            onChange={(value) => {
              setSearchQuery(value);
            }}
            onEnterPress={() => {
              setCommittedSearchQuery(searchQuery);
            }}
            onClear={() => {
              setSearchQuery('');
              setCommittedSearchQuery('');
            }}
            minWidth='250px'
            maxWidth='100%'
          />
        </Box>
      </Box>

      {/* Filters: Type Chips and Pinned Checkbox */}
      <Box sx={{ display: 'flex', p: '0px 12px', gap: 2, mb: 2, alignItems: 'center', justifyContent: 'space-between' }}>
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
        <Box sx={{ padding: '24px' }}>
          <Typography variant='body1' sx={{ mb: 2 }}>
            Are you sure you want to delete this memory?
          </Typography>
          <Typography variant='body2' sx={{ color: colors.text.tertiary, mb: 3 }}>
            This action cannot be undone. The AI will no longer have access to this information.
          </Typography>
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px' }}>
            <CustomButton
              variant='secondary'
              size='Small'
              text='Cancel'
              onClick={() => {
                setDeleteModalOpen(false);
                setSelectedMemory(null);
              }}
              disabled={submitting}
            />
            <CustomButton
              variant='primary'
              size='Small'
              text='Delete'
              onClick={handleConfirmDelete}
              loading={submitting}
              sx={{
                backgroundColor: colors.error,
                '&:hover': {
                  backgroundColor: colors.error,
                  filter: 'brightness(0.9)',
                },
              }}
            />
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
