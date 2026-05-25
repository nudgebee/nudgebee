import { useRef, useEffect, useState, useMemo, useCallback } from 'react';
import { Box, List, ListItemButton, Tooltip, Typography } from '@mui/material';
import ChevronLeftIcon from '@mui/icons-material/ChevronLeft';
import ChevronRightIcon from '@mui/icons-material/ChevronRight';
import PropTypes from 'prop-types';
import { Text } from '@components1/common';
import CustomTooltip from '@components1/common/CustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';
import {
  ErrorIcon,
  RunningIcon,
  SaveIconOutlinelight,
  SaveIconOutlineselect,
  SuccessIcon,
  ShareIconBlue,
  DeleteIconRed,
  LogEventsIcon,
  SaveIconOutline,
  UserIconOutline,
  CollapseLeftIcon,
} from '@assets';
import ShimmerLoading from '@components1/common/ShimmerLoading';
import apiAskNudgebee from '@api1/ask-nudgebee';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';
import CustomButton from '@components1/common/NewCustomButton';
import CustomSearch from '@components1/common/CustomSearch';
import ToggleButtons from '@components1/workflow/NewToggleButtons';
import { useRouter } from 'next/router';
import { getUserSession } from '@lib/auth';

const ConversationList = ({
  accountId,
  onSelectConversation,
  selectedId,
  isConversationListVisible,
  triggerHandleNewChat,
  handleShare,
  likedConversations,
  setLikedConversations,
  savingStates,
  handleLike,
  setSelectedConversation,
  rawConversations,
  setRawConversations,
  onCollapseConversationList,
}) => {
  const latestLastRecordedAtRef = useRef('');
  const pollingTimeoutRef = useRef(null);
  const loadingRef = useRef(false);
  const scrollContainerRef = useRef(null);
  const PAGE_SIZE = 20;
  const router = useRouter();

  const [page, setPage] = useState(0);
  const [hasMore, setHasMore] = useState(true);
  const [loading, setLoading] = useState(false);
  const [searchText, setSearchText] = useState('');
  const [hoveredItemId, setHoveredItemId] = useState(null);
  const initialFilter = router.query.status === 'WAITING' ? 'Waiting' : 'All';
  const [activeFilter, setActiveFilter] = useState(initialFilter);
  const [filterMine, setFilterMine] = useState(router.query.filter === 'Mine');

  const conversationSources = [
    { value: 'UserInvestigation', label: 'User Chat' },
    { value: 'Optimize', label: 'Optimize' },
    { value: 'PrometheusQuery', label: 'Prometheus Query' },
    { value: 'LokiQuery', label: 'Loki Query' },
    { value: 'ESQuery', label: 'ES Query' },
    { value: 'Investigation', label: 'Event Analysis' },
    { value: 'InstantNotification', label: 'Slack Channel' },
  ];

  const allSourceValues = conversationSources.map((s) => s.value);
  const sourceLabels = Object.fromEntries(conversationSources.map((s) => [s.value, s.label]));

  const [selectedSources, setSelectedSources] = useState(allSourceValues);
  const [selectedChip, setSelectedChip] = useState('All');
  const chipScrollRef = useRef(null);
  const [canScrollLeft, setCanScrollLeft] = useState(false);
  const [canScrollRight, setCanScrollRight] = useState(false);

  const updateScrollArrows = useCallback(() => {
    const el = chipScrollRef.current;
    if (!el) return;
    setCanScrollLeft(el.scrollLeft > 0);
    setCanScrollRight(el.scrollLeft + el.clientWidth < el.scrollWidth - 1);
  }, []);

  const scrollChips = (direction) => {
    const el = chipScrollRef.current;
    if (!el) return;
    el.scrollBy({ left: direction === 'left' ? -100 : 100, behavior: 'smooth' });
  };

  const handleChipClick = (chipValue) => {
    setSelectedChip(chipValue);
    if (chipValue === 'All') {
      setSelectedSources(allSourceValues);
    } else {
      setSelectedSources([chipValue]);
    }
  };

  useEffect(() => {
    updateScrollArrows();
  }, [isConversationListVisible, updateScrollArrows]);

  const handleFilterClick = (filter) => {
    setActiveFilter(filter);
    setFilterMine(false);
    // Clear status and filter query params when user manually changes filter
    if (router.query.status || router.query.filter) {
      const { status: _status, filter: _filter, ...rest } = router.query;
      router.replace({ pathname: router.pathname, query: rest }, undefined, { shallow: true });
    }
  };

  const mergeConversations = (prevConversations, newConversations, source) => {
    if (source === 'page') {
      const existingIds = new Set(prevConversations.map((conv) => conv.id));
      const uniqueNewConversations = newConversations.filter((conv) => !existingIds.has(conv.id));
      return [...prevConversations, ...uniqueNewConversations];
    }
    const existingConversationsMap = new Map(prevConversations.map((conv) => [conv.id, conv]));
    const newItems = [];
    const updatedItems = [];
    newConversations.forEach((newConv) => {
      const existingConv = existingConversationsMap.get(newConv.id);

      if (existingConv) {
        updatedItems.push({
          ...existingConv,
          ...newConv,
          lastUpdated: new Date().toISOString(),
        });
        existingConversationsMap.delete(newConv.id);
      } else {
        newItems.push({
          ...newConv,
          lastUpdated: new Date().toISOString(),
        });
      }
    });
    return [
      ...newItems,
      ...prevConversations.map((prevConv) => {
        const updatedVersion = updatedItems.find((item) => item.id === prevConv.id);
        return updatedVersion || prevConv;
      }),
    ];
  };

  const fetchConversations = (source = 'polling') => {
    if (loadingRef.current) return;
    if (source == 'on-enter') {
      setRawConversations([]);
      setPage(0);
      setHasMore(true);
      setLikedConversations([]);
      latestLastRecordedAtRef.current = '';
    }
    loadingRef.current = true;
    setLoading(true);
    const query = {
      account_id: accountId,
      source:
        selectedSources.length > 0
          ? selectedSources
          : ['UserInvestigation', 'Optimize', 'PrometheusQuery', 'LokiQuery', 'ESQuery', 'Investigation', 'InstantNotification', 'WorkflowBuilder'],
      limit: PAGE_SIZE,
      offset: source !== 'polling' ? page * PAGE_SIZE : 0,
      latestLastRecordedAt: source !== 'polling' ? '' : latestLastRecordedAtRef.current,
      activeFilter: activeFilter,
      searchText: searchText,
      skipTotalCount: true,
      ...(filterMine && { user_username: getUserSession()?.user?.email }),
    };
    apiAskNudgebee
      .llmConversationHistory(query)
      .then((res) => {
        const llmConversations = res?.data?.data?.llm_conversations ?? [];
        // The All-tab initial load calls fetchConversations('polling') with an empty
        // latestLastRecordedAtRef, so use that as the marker for "first fetch" rather
        // than relying on source alone.
        if (source !== 'polling' || latestLastRecordedAtRef.current === '') {
          setHasMore(llmConversations.length === PAGE_SIZE);
        }
        if (llmConversations.length) {
          if (source === 'polling') {
            latestLastRecordedAtRef.current = llmConversations[0].updated_at;
          }
          setRawConversations((prevConversations) => mergeConversations(prevConversations, llmConversations, source));
          const likedConversations =
            llmConversations
              .map((f) => f.llm_conversation_saveds?.map((g) => g.conversation_id))
              ?.filter((id) => id)
              .flat() ?? [];
          setLikedConversations((prev) => {
            const likedSet = new Set(prev);
            likedConversations.forEach((id) => {
              likedSet.add(id);
            });
            return Array.from(likedSet);
          });
        }
      })
      .finally(() => {
        loadingRef.current = false;
        setLoading(false);
        const shouldPollForFilter =
          activeFilter === 'All' || (activeFilter === 'Mine' && (selectedChip === 'All' || selectedChip === 'UserInvestigation'));
        if (source === 'polling' && shouldPollForFilter && isConversationListVisible && searchText === '') {
          pollingTimeoutRef.current = setTimeout(() => {
            fetchConversations('polling');
          }, 5000);
        }
      });
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      handleShare();
    } else if (menuItem.id === 1) {
      apiAskNudgebee
        .deleteConversation({
          conversation_id: data.id,
        })
        .then((res) => {
          const response = res?.data?.data?.ai_delete_llm_conversation_by_id?.data?.success ?? false;
          if (response) {
            setRawConversations((prevConversations) => prevConversations.filter((convo) => convo.id !== data.id));
            snackbar.success('Conversation deleted successfully');
            if (data.sessionId == router.query?.session_id) {
              triggerHandleNewChat();
            }
          } else {
            snackbar.error('Failed to delete conversation');
          }
        })
        .catch((error) => {
          console.error('Error deleting conversation:', error);
          snackbar.error('An error occurred while deleting the conversation');
        });
    } else if (menuItem.id === 2) {
      handleLike(data.id, likedConversations.includes(data.id));
    }
  };

  const getMenuItems = (username, conversationId) => {
    let MENU_ITEMS = [
      {
        icon: ShareIconBlue,
        label: 'Share',
        id: 0,
        activeFilter: 'false',
      },
      {
        icon: savingStates?.[conversationId] ? null : likedConversations.includes(conversationId) ? SaveIconOutlineselect : SaveIconOutlinelight,
        label: likedConversations.includes(conversationId) ? 'Unsave' : 'Save',
        id: 2,
        disabled: savingStates?.[conversationId],
        showLoader: savingStates?.[conversationId],
      },
    ];
    if (getUserSession()?.user?.email === username) {
      MENU_ITEMS.push({
        icon: DeleteIconRed,
        label: 'Delete',
        id: 1,
      });
    }
    return MENU_ITEMS;
  };

  const conversations = useMemo(() => {
    return rawConversations.map((item) => {
      let message = item.title || '';
      if (message.includes('"query"')) {
        try {
          message = JSON.parse(message).query;
        } catch {
          // Keep original message if JSON parsing fails
        }
      }

      const statusMap = {
        IN_PROGRESS: 'Running',
        COMPLETED: 'Completed',
        FAILED: 'Failed',
        WAITING: 'Waiting for Approval',
      };
      const status = statusMap[item.status] || statusMap[item.for_status[0]?.status] || 'Failed';

      return {
        id: item.id,
        sessionId: item.session_id,
        message,
        created_at: item.created_at,
        source: item.source,
        status,
        userName: item?.user?.display_name ?? '-',
        email: item?.user?.username ?? '-',
      };
    });
  }, [rawConversations]);

  const statusIconMap = {
    Running: RunningIcon,
    Completed: SuccessIcon,
    Failed: ErrorIcon,
    'Waiting for Approval': RunningIcon,
  };
  const getStatusIcon = (status) => statusIconMap[status] || ErrorIcon;

  const getDateGroup = (dateStr) => {
    if (!dateStr) return 'Older';
    const date = new Date(dateStr);
    const now = new Date();
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    const yesterday = new Date(today);
    yesterday.setDate(yesterday.getDate() - 1);
    const weekAgo = new Date(today);
    weekAgo.setDate(weekAgo.getDate() - 7);
    const monthAgo = new Date(today);
    monthAgo.setDate(monthAgo.getDate() - 30);

    if (date >= today) return 'Today';
    if (date >= yesterday) return 'Yesterday';
    if (date >= weekAgo) return 'Last 7 Days';
    if (date >= monthAgo) return 'Last 30 Days';
    return 'Older';
  };

  const groupedConversations = useMemo(() => {
    const buckets = { Today: [], Yesterday: [], 'Last 7 Days': [], 'Last 30 Days': [], Older: [] };
    conversations.forEach((conv) => {
      const group = getDateGroup(conv.created_at);
      buckets[group].push(conv);
    });
    const result = [];
    Object.entries(buckets).forEach(([label, items]) => {
      if (items.length > 0) {
        result.push({ type: 'header', label });
        items.forEach((conv) => result.push({ type: 'conversation', data: conv }));
      }
    });
    return result;
  }, [conversations]);

  useEffect(() => {
    // 1. Always cleanup existing polling on any dependency change
    if (pollingTimeoutRef.current) {
      clearTimeout(pollingTimeoutRef.current);
    }

    // 2. STOP if the list is not visible.
    // This prevents API calls and polling when the drawer is closed.
    if (!isConversationListVisible) {
      return;
    }

    // 3. STOP if the user is currently searching (has text in the box).
    // We don't want to poll 'All' or auto-fetch while the user is typing a specific query.
    // The fetch will be triggered manually by the 'onEnterPress' in CustomSearch.
    if (searchText !== '') {
      return;
    }

    // 4. Reset List & State (This replaces the logic from the useEffect you questioned)
    setRawConversations([]);
    setPage(0);
    setHasMore(true);
    latestLastRecordedAtRef.current = '';
    if (scrollContainerRef.current) {
      scrollContainerRef.current.scrollTop = 0;
    }

    // 5. Trigger standard data loading
    if (activeFilter === 'All') {
      fetchConversations('polling');
    } else if (activeFilter === 'Mine' || activeFilter === 'Saved' || activeFilter === 'Waiting') {
      fetchConversations();
    }

    // Cleanup function strictly for unmounting/re-running
    return () => {
      if (pollingTimeoutRef.current) {
        clearTimeout(pollingTimeoutRef.current);
      }
    };
  }, [
    accountId,
    activeFilter,
    selectedSources,
    isConversationListVisible, // Added dependency
    searchText, // Added dependency
    filterMine,
  ]);

  useEffect(() => {
    if (page > 0) {
      fetchConversations('page');
    }
  }, [page]);

  useEffect(() => {
    if (selectedId && selectedId.toLowerCase().includes('event')) {
      // For event-related conversations, ensure we include Investigation if not already selected
      setSelectedSources((prev) => {
        if (!prev.includes('Investigation')) {
          return [...prev, 'Investigation'];
        }
        return prev;
      });
    }
  }, [selectedId]);

  const handleSelectConversation = (conversation) => {
    setSelectedConversation(conversation);
    onSelectConversation(conversation.sessionId, conversation.userName ?? '-');
  };

  const handleScroll = (event) => {
    const listElement = event.target;
    if (!listElement || loadingRef.current || !hasMore || !isConversationListVisible) {
      return;
    }

    const { scrollTop, scrollHeight, clientHeight } = listElement;
    const isNearBottom = scrollTop + clientHeight >= scrollHeight - 50;

    if (isNearBottom) {
      loadMoreConversations();
    }
  };

  const loadMoreConversations = () => {
    if (!loading) {
      setPage((prevPage) => prevPage + 1);
    }
  };

  return (
    <Box
      sx={{
        position: 'sticky',
        top: '0px',
        zIndex: 40,
        transform: isConversationListVisible ? 'translateX(0)' : 'translateX(-100%)',
        transition: 'transform 0.4s cubic-bezier(0.4, 0, 0.2, 1)',
        opacity: isConversationListVisible ? 1 : 0,
        visibility: isConversationListVisible ? 'visible' : 'hidden',
        willChange: 'transform, opacity',
      }}
    >
      <Box
        sx={{
          height: '100vh',
          display: 'flex',
          flexDirection: 'column',
          borderRight: `0.5px solid ${isConversationListVisible ? colors.border.secondary : colors.background.transparent}`,
          transition: 'border-right 0.4s cubic-bezier(0.4, 0, 0.2, 1)',
          position: 'absolute',
          width: isConversationListVisible ? '300px' : '0px',
          maxWidth: '350px',
        }}
      >
        <Box
          display='flex'
          flexDirection='column'
          flexShrink={0}
          backgroundColor={colors.background.white}
          borderBottom={`0.75px solid ${colors.border.vertical}`}
        >
          {/* Header row */}
          <Box display='flex' alignItems='center' justifyContent='space-between' sx={{ px: '16px', pt: '14px', pb: '8px' }}>
            <Typography
              sx={{
                fontSize: '15px',
                fontWeight: 600,
                color: colors.text.secondary,
                fontFamily: 'Roboto',
              }}
            >
              Chat History
            </Typography>
            <CustomTooltip title='Collapse Recent' placement='bottom'>
              <Box>
                <CustomButton
                  variant='secondary'
                  sx={{
                    height: '28px',
                    width: '28px',
                    minWidth: '28px',
                    padding: '4px',
                  }}
                  startIcon={<SafeIcon src={CollapseLeftIcon} width={14} height={14} alt='collapse' />}
                  onClick={(e) => {
                    e.stopPropagation();
                    onCollapseConversationList?.();
                  }}
                />
              </Box>
            </CustomTooltip>
          </Box>

          {/* Search bar */}
          <Box sx={{ px: '12px', pb: '12px' }}>
            <CustomSearch
              onChange={(e) => {
                setSearchText(e);
              }}
              id='search-chat'
              label='Search conversations...'
              inputHeight='36px'
              minWidth='100%'
              maxWidth='100%'
              onEnterPress={() => {
                if (pollingTimeoutRef.current) {
                  clearTimeout(pollingTimeoutRef.current);
                }
                fetchConversations('on-enter');
              }}
              sx={{
                flex: 1,
                borderRadius: '8px',
                bgcolor: colors.background.white,
                '& .MuiOutlinedInput-notchedOutline': {
                  border: `3px solid ${colors.border.tertiaryLight}`,
                },
                '& .MuiOutlinedInput-root': {
                  borderRadius: '8px',
                  '&:hover .MuiOutlinedInput-notchedOutline': {
                    border: `1px solid ${colors.border.primaryLightest}`,
                  },
                  '&.Mui-focused .MuiOutlinedInput-notchedOutline': {
                    border: `1px solid ${colors.border.primaryLightest}`,
                    boxShadow: `0 0 0 2px ${colors.border.primary}33`,
                  },
                },
                input: {
                  padding: '6px 0px !important',
                  height: '18px',
                  '&::placeholder': {
                    color: colors.text.secondaryDark,
                    fontSize: '12.5px',
                    fontWeight: 400,
                    opacity: 0.7,
                  },
                },
              }}
            />
          </Box>

          {/* Mine/Saved/All toggle */}
          <Box sx={{ px: '12px' }}>
            <ToggleButtons
              options={[
                { value: 'All', label: 'All', icon: LogEventsIcon },
                { value: 'Mine', label: 'Mine', icon: UserIconOutline },
                { value: 'Saved', label: 'Saved', icon: SaveIconOutline },
                { value: 'Waiting', label: 'Waiting', icon: RunningIcon },
              ]}
              activeValue={activeFilter}
              size='sm'
              onChange={(value) => handleFilterClick(value)}
            />
          </Box>

          {/* Source chip filters */}
          <Box sx={{ position: 'relative', p: '12px' }}>
            {canScrollLeft && (
              <Box
                onClick={() => scrollChips('left')}
                sx={{
                  position: 'absolute',
                  left: '4px',
                  top: '50%',
                  transform: 'translateY(-50%)',
                  zIndex: 1,
                  cursor: 'pointer',
                  color: colors.text.secondaryDark,
                  backgroundColor: colors.background.white,
                  borderRadius: '50%',
                  width: '20px',
                  height: '20px',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  boxShadow: '0px 1px 3px rgba(0,0,0,0.12)',
                }}
              >
                <ChevronLeftIcon sx={{ fontSize: 16 }} />
              </Box>
            )}
            <Box
              ref={chipScrollRef}
              onScroll={updateScrollArrows}
              sx={{
                display: 'flex',
                gap: '6px',
                overflowX: 'auto',
                scrollbarWidth: 'none',
                '&::-webkit-scrollbar': { display: 'none' },
                scrollBehavior: 'smooth',
              }}
            >
              {[{ value: 'All', label: 'All' }, ...conversationSources].map((chip) => {
                const isActive = selectedChip === chip.value;
                return (
                  <Box
                    key={chip.value}
                    onClick={() => handleChipClick(chip.value)}
                    sx={{
                      px: '10px',
                      py: '4px',
                      borderRadius: '20px',
                      fontSize: '11px',
                      fontFamily: 'Roboto',
                      fontWeight: isActive ? 500 : 400,
                      whiteSpace: 'nowrap',
                      cursor: 'pointer',
                      flexShrink: 0,
                      backgroundColor: isActive ? colors.background.primaryLightest : colors.background.tertiaryLightest,
                      color: isActive ? colors.text.primary : colors.text.secondaryDark,
                      border: isActive ? `1px solid ${colors.border.primary}` : '1px solid transparent',
                      transition: 'all 0.2s ease',
                      '&:hover': {
                        backgroundColor: isActive ? colors.background.primaryLightest : colors.background.tertiaryLight,
                      },
                    }}
                  >
                    {chip.label}
                  </Box>
                );
              })}
            </Box>
            {canScrollRight && (
              <Box
                onClick={() => scrollChips('right')}
                sx={{
                  position: 'absolute',
                  right: '4px',
                  top: '50%',
                  transform: 'translateY(-50%)',
                  zIndex: 1,
                  cursor: 'pointer',
                  color: colors.text.secondaryDark,
                  backgroundColor: colors.background.white,
                  borderRadius: '50%',
                  width: '20px',
                  height: '20px',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  boxShadow: '0px 1px 3px rgba(0,0,0,0.12)',
                }}
              >
                <ChevronRightIcon sx={{ fontSize: 16 }} />
              </Box>
            )}
          </Box>
        </Box>
        <Box
          ref={scrollContainerRef}
          sx={{
            width: '100%',
            backgroundColor: colors.background.white,
            flex: 1,
            overflowY: 'auto',
            '::-webkit-scrollbar': { width: '4px' },
          }}
          onScroll={handleScroll}
        >
          <List sx={{ pt: '4px' }}>
            {groupedConversations.map((item, index) =>
              item.type === 'header' ? (
                <Typography
                  key={`header-${item.label}`}
                  sx={{
                    fontSize: '11px',
                    fontFamily: 'Roboto',
                    fontWeight: 600,
                    color: colors.text.secondaryDark,
                    textTransform: 'uppercase',
                    letterSpacing: '0.5px',
                    px: '20px',
                    pt: index === 0 ? '8px' : '16px',
                    pb: '6px',
                  }}
                >
                  {item.label}
                </Typography>
              ) : (
                <ListItemButton
                  key={item.data.id}
                  onClick={() => handleSelectConversation(item.data)}
                  onMouseEnter={() => setHoveredItemId(item.data.id)}
                  onMouseLeave={() => setHoveredItemId(null)}
                  selected={selectedId === item.data.sessionId}
                  sx={{
                    p: '4px 12px',
                    mx: '8px',
                    borderRadius: '8px',
                    '&.Mui-selected': {
                      backgroundColor: colors.background.activeAnchorButton,
                    },
                    '&:hover': {
                      backgroundColor: colors.background.primaryLightest,
                      '&.Mui-selected': {
                        backgroundColor: colors.background.activeAnchorButton,
                      },
                    },
                  }}
                >
                  <Box sx={{ width: '100%' }}>
                    <Box sx={{ py: '10px' }}>
                      {/* Row 1: Status icon + Title + Menu */}
                      <Box display='flex' alignItems='flex-start' gap='6px' position='relative'>
                        <Tooltip title={item.data.status}>
                          <SafeIcon
                            src={getStatusIcon(item.data.status)}
                            alt={item.data.status}
                            height={14}
                            width={14}
                            style={{ marginTop: '2px', flexShrink: 0 }}
                          />
                        </Tooltip>
                        <Text
                          value={item.data.message}
                          showAutoEllipsis
                          sx={{
                            fontSize: '12px',
                            fontFamily: 'Roboto',
                            fontWeight: 400,
                            pr: '18px',
                            flex: 1,
                            lineHeight: '16px',
                          }}
                        />
                        {hoveredItemId === item.data.id && (
                          <ThreeDotsMenu
                            icon
                            menuItems={getMenuItems(item.data.email, item.data.id)}
                            onMenuClick={onMenuClick}
                            lightIcon={colors.text.primary}
                            menuWidth='137px'
                            sx={{
                              width: '20px',
                              height: '20px',
                              position: 'absolute',
                              right: '-5px',
                              top: '-2px',
                              '& svg': { width: '16px', height: '16px' },
                            }}
                            data={item.data}
                          />
                        )}
                      </Box>
                      {/* Row 2: Source badge + Author */}
                      <Box display='flex' alignItems='center' gap='6px' mt='4px' sx={{ pl: '20px' }}>
                        {item.data.source && sourceLabels[item.data.source] && (
                          <Box
                            sx={{
                              px: '6px',
                              py: '1px',
                              borderRadius: '4px',
                              fontSize: '10px',
                              fontFamily: 'Roboto',
                              fontWeight: 400,
                              whiteSpace: 'nowrap',
                              backgroundColor: colors.background.tertiaryLightest,
                              color: colors.text.tertiary,
                            }}
                          >
                            {sourceLabels[item.data.source]}
                          </Box>
                        )}
                        {activeFilter !== 'Mine' && (
                          <Typography
                            sx={{
                              fontSize: '11px',
                              fontFamily: 'Roboto',
                              color: colors.text.secondaryDark,
                              overflow: 'hidden',
                              textOverflow: 'ellipsis',
                              whiteSpace: 'nowrap',
                              maxWidth: '120px',
                            }}
                          >
                            {item.data.userName ?? '-'}
                          </Typography>
                        )}
                        {likedConversations.includes(item.data.id) && (
                          <Box sx={{ ml: 'auto', display: 'flex', alignItems: 'center', flexShrink: 0 }}>
                            <SafeIcon src={SaveIconOutlineselect} alt='saved' height={12} width={12} />
                          </Box>
                        )}
                      </Box>
                    </Box>
                  </Box>
                </ListItemButton>
              )
            )}
          </List>
          {loading && (
            <Box sx={{ px: '16px', pt: '12px', display: 'flex', flexDirection: 'column', gap: '8px' }}>
              {['s1', 's2', 's3', 's4', 's5', 's6'].map((id) => (
                <ShimmerLoading key={id} isLoading height='52px' width='94%' />
              ))}
            </Box>
          )}
        </Box>
      </Box>
    </Box>
  );
};

ConversationList.propTypes = {
  accountId: PropTypes.string,
  onSelectConversation: PropTypes.func.isRequired,
  selectedId: PropTypes.string,
  isConversationListVisible: PropTypes.bool,
  searchText: PropTypes.string,
  triggerHandleNewChat: PropTypes.func.isRequired,
  handleShare: PropTypes.func,
  likedConversations: PropTypes.array,
  setLikedConversations: PropTypes.func,
  savingStates: PropTypes.object,
  handleLike: PropTypes.func,
  setSelectedConversation: PropTypes.func,
  rawConversations: PropTypes.array,
  setRawConversations: PropTypes.func,
  onCollapseConversationList: PropTypes.func,
};

export default ConversationList;
