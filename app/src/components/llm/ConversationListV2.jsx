import { useRef, useEffect, useState, useMemo } from 'react';
import { Box, List, ListItemButton, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import Text from '@shared/format/Text';
import Tooltip from '@ui/Tooltip';
import SafeIcon from '@shared/icons/SafeIcon';
import {
  SaveIconOutlinelight,
  SaveIconOutlineselect,
  ShareIconBlue,
  DeleteIconRed,
  LogEventsIcon,
  UserIconOutline,
  CollapseLeftIcon,
  SaveIconOutline,
  FilterIcon,
  RunningIcon,
} from '@assets';
import { resolveStatusLabel, getStatusIcon } from '@utils/conversationStatus';
import { Skeleton } from '@ui/Skeleton';
import apiAskNudgebee from '@api1/ask-nudgebee';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import { ds } from '@utils/colors';
import { toast as snackbar } from '@ui/Toast';
import { Button } from '@ui/Button';
import SearchInput from '@ui/SearchInput';
import { DropdownMenu } from '@ui/DropdownMenu';
import ToggleButtons from '@components/workflow/NewToggleButtons';
import { useRouter } from 'next/router';
import { getUserSession } from '@lib/auth';

const conversationSources = [
  { value: 'UserInvestigation', label: 'User Chat' },
  { value: 'Optimize', label: 'Optimize' },
  { value: 'PrometheusQuery', label: 'Prometheus Query' },
  { value: 'LokiQuery', label: 'Loki Query' },
  { value: 'ESQuery', label: 'ES Query' },
  { value: 'Investigation', label: 'Event Analysis' },
  { value: 'InstantNotification', label: 'Slack Channel' },
  { value: 'Automation', label: 'Automation' },
];

const allSourceValues = conversationSources.map((s) => s.value);
const sourceLabels = Object.fromEntries(conversationSources.map((s) => [s.value, s.label]));

// Users distinguish three kinds of conversations, not nine sources: things
// they said, things the system ran, and query-page transcripts. Individual
// sources stay reachable as sub-options for the rare precise need.
const TYPE_GROUPS = {
  Chats: ['UserInvestigation', 'InstantNotification'],
  System: ['Investigation', 'Automation', 'Optimize'],
  Queries: ['PrometheusQuery', 'LokiQuery', 'ESQuery'],
};

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
  // Bumped whenever the query identity changes (account / filter / sources / visibility / search).
  // A response — or a poll reschedule — from an older epoch is dropped, otherwise an in-flight
  // "All" request resolves into the freshly cleared "Mine" list.
  const requestEpochRef = useRef(0);
  const inFlightEpochRef = useRef(-1);
  const lastQueryIdentityRef = useRef(null);
  const PAGE_SIZE = 20;
  const router = useRouter();

  const [page, setPage] = useState(0);
  const [hasMore, setHasMore] = useState(true);
  const [loading, setLoading] = useState(false);
  const [searchText, setSearchText] = useState('');
  const [hoveredItemId, setHoveredItemId] = useState(null);
  // Scope (whose conversations) and state (Waiting/Saved) are independent
  // axes — the old All/Mine/Saved/Waiting tabs flattened them into one
  // exclusive row, which made "my waiting conversations" inexpressible.
  // Deep links: ?status=WAITING preselects the Waiting toggle, ?filter=Mine
  // the Mine scope (also the default).
  const [scope, setScope] = useState(router.query.filter === 'Everyone' ? 'Everyone' : 'Mine');
  const [stateFilter, setStateFilter] = useState(router.query.status === 'WAITING' ? 'Waiting' : '');
  const [waitingCount, setWaitingCount] = useState(null);
  const [savedCount, setSavedCount] = useState(null);

  const [selectedSources, setSelectedSources] = useState(allSourceValues);
  const [selectedType, setSelectedType] = useState('All');

  const handleTypeSelect = (value) => {
    const next = value || 'All';
    setSelectedType(next);
    if (next === 'All') {
      setSelectedSources(allSourceValues);
    } else if (TYPE_GROUPS[next]) {
      setSelectedSources(TYPE_GROUPS[next]);
    } else {
      setSelectedSources([next]);
    }
  };

  const typeTriggerLabel =
    selectedType === 'All' ? 'Type: All' : TYPE_GROUPS[selectedType] ? selectedType : sourceLabels[selectedType] || selectedType;

  // On a hard page load router.query is empty until hydration, so the useState
  // initializers above miss deep links; re-sync once the router is ready.
  useEffect(() => {
    if (!router.isReady) {
      return;
    }
    if (router.query.filter === 'Everyone') {
      setScope('Everyone');
    } else if (router.query.filter === 'Mine') {
      setScope('Mine');
    }
    if (router.query.status === 'WAITING') {
      setStateFilter('Waiting');
    }
  }, [router.isReady, router.query.filter, router.query.status]);

  const clearDeepLinkParams = () => {
    if (router.query.status || router.query.filter) {
      const { status: _status, filter: _filter, ...rest } = router.query;
      router.replace({ pathname: router.pathname, query: rest }, undefined, { shallow: true });
    }
  };

  const handleScopeChange = (value) => {
    setScope(value);
    clearDeepLinkParams();
  };

  // Waiting and Saved are toggles: clicking the active one turns it off.
  const handleStateToggle = (value) => {
    setStateFilter((prev) => (prev === value ? '' : value));
    clearDeepLinkParams();
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
    const epoch = requestEpochRef.current;
    // Dedupe only against a request from the same epoch — a request left over from the
    // previous filter must not swallow the new filter's fetch.
    if (loadingRef.current && inFlightEpochRef.current === epoch) return;
    if (source == 'on-enter') {
      setRawConversations([]);
      setPage(0);
      setHasMore(true);
      setLikedConversations([]);
      latestLastRecordedAtRef.current = '';
    }
    loadingRef.current = true;
    inFlightEpochRef.current = epoch;
    setLoading(true);
    const query = {
      account_id: accountId,
      source:
        selectedSources.length > 0
          ? selectedSources
          : [
              'UserInvestigation',
              'Optimize',
              'PrometheusQuery',
              'LokiQuery',
              'ESQuery',
              'Investigation',
              'InstantNotification',
              'WorkflowBuilder',
              'Automation',
            ],
      limit: PAGE_SIZE,
      offset: source !== 'polling' ? page * PAGE_SIZE : 0,
      latestLastRecordedAt: source !== 'polling' ? '' : latestLastRecordedAtRef.current,
      activeFilter: stateFilter === 'Saved' ? 'Saved' : 'All',
      ...(stateFilter === 'Waiting' && { status: 'WAITING' }),
      searchText: searchText,
      skipTotalCount: true,
      ...(scope === 'Mine' && { user_username: getUserSession()?.user?.email }),
    };
    apiAskNudgebee
      .llmConversationHistory(query)
      .then((res) => {
        // Stale response from a filter the user has already left — dropping it keeps
        // other users' conversations out of the "Mine" list.
        if (epoch !== requestEpochRef.current) return;
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
        // Only the newest request owns the loading flags.
        if (inFlightEpochRef.current === epoch) {
          loadingRef.current = false;
          setLoading(false);
        }
        // A stale request must not reschedule a poll: its closure still carries the
        // previous filter, and the effect cleanup has already run for it.
        if (epoch !== requestEpochRef.current) return;
        if (source === 'polling' && isConversationListVisible && searchText === '') {
          pollingTimeoutRef.current = setTimeout(() => {
            fetchConversations('polling');
          }, 5000);
        }
      });
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 'share') {
      handleShare();
    } else if (menuItem.id === 'delete') {
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
    } else if (menuItem.id === 'toggle-save') {
      handleLike(data.id, likedConversations.includes(data.id));
    }
  };

  const getMenuItems = (username, conversationId) => {
    let MENU_ITEMS = [
      {
        icon: ShareIconBlue,
        label: 'Share',
        id: 'share',
        activeFilter: 'false',
      },
      {
        icon: savingStates?.[conversationId] ? null : likedConversations.includes(conversationId) ? SaveIconOutlineselect : SaveIconOutlinelight,
        label: likedConversations.includes(conversationId) ? 'Unsave' : 'Save',
        id: 'toggle-save',
        disabled: savingStates?.[conversationId],
        showLoader: savingStates?.[conversationId],
      },
    ];
    if (getUserSession()?.user?.email === username) {
      MENU_ITEMS.push({
        icon: DeleteIconRed,
        label: 'Delete',
        id: 'delete',
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

      const status = resolveStatusLabel(item.status, item.for_status);

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

    // 1b. Invalidate whatever is already in flight — its response belongs to the
    // filter the user just left.
    requestEpochRef.current += 1;

    // 2. STOP if the list is not visible.
    // This prevents API calls and polling when the drawer is closed.
    if (!isConversationListVisible) {
      return;
    }

    // 3. STOP if only the search box changed.
    // We don't want to poll 'All' or auto-fetch while the user is typing a specific query;
    // that fetch is triggered manually by the 'onEnterPress' in SearchInput. A filter /
    // account / source change still has to refetch, otherwise switching to 'Mine' mid-search
    // leaves the previous filter's rows on screen.
    const queryIdentity = JSON.stringify([accountId, scope, stateFilter, selectedSources]);
    const queryIdentityChanged = queryIdentity !== lastQueryIdentityRef.current;
    lastQueryIdentityRef.current = queryIdentity;
    if (searchText !== '' && !queryIdentityChanged) {
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

    // 5. Trigger standard data loading (fetchConversations defaults to the
    // polling style, which primes the incremental-merge cursor)
    fetchConversations('polling');

    // Cleanup function strictly for unmounting/re-running
    return () => {
      if (pollingTimeoutRef.current) {
        clearTimeout(pollingTimeoutRef.current);
      }
    };
  }, [
    accountId,
    scope,
    stateFilter,
    selectedSources,
    isConversationListVisible, // Added dependency
    searchText, // Added dependency
  ]);

  // Waiting badge: cheap count query (limit 1 + total_count), scoped like the
  // list. Refreshed on scope/account change and on a slow interval while the
  // drawer is visible.
  useEffect(() => {
    if (!accountId || !isConversationListVisible) return undefined;
    let active = true;
    const countQuery = (extra) => ({
      account_id: accountId,
      source: allSourceValues,
      limit: 1,
      offset: 0,
      latestLastRecordedAt: '',
      searchText: '',
      skipTotalCount: false,
      ...(scope === 'Mine' && { user_username: getUserSession()?.user?.email }),
      ...extra,
    });
    const readCount = (res) => {
      const count = res?.data?.data?.llm_conversations_aggregate?.aggregate?.count;
      return typeof count === 'number' ? count : null;
    };
    const fetchStateCounts = () => {
      apiAskNudgebee
        .llmConversationHistory(countQuery({ activeFilter: 'All', status: 'WAITING' }))
        .then((res) => active && setWaitingCount(readCount(res)))
        .catch(() => active && setWaitingCount(null));
      apiAskNudgebee
        .llmConversationHistory(countQuery({ activeFilter: 'Saved' }))
        .then((res) => active && setSavedCount(readCount(res)))
        .catch(() => active && setSavedCount(null));
    };
    fetchStateCounts();
    const interval = setInterval(fetchStateCounts, 60000);
    return () => {
      active = false;
      clearInterval(interval);
    };
  }, [accountId, scope, isConversationListVisible]);

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
        top: 0,
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
          borderRight: `0.5px solid ${isConversationListVisible ? 'var(--ds-gray-300)' : 'transparent'}`,
          transition: 'border-right 0.4s cubic-bezier(0.4, 0, 0.2, 1)',
          position: 'absolute',
          width: isConversationListVisible ? ds.space.mul(1, 75) : 0,
          maxWidth: ds.space.mul(0, 175),
        }}
      >
        <Box
          display='flex'
          flexDirection='column'
          flexShrink={0}
          backgroundColor={'var(--ds-background-100)'}
          borderBottom={`0.75px solid var(--ds-gray-200)`}
        >
          {/* Header row */}
          <Box display='flex' alignItems='center' justifyContent='space-between' sx={{ px: ds.space[4], pt: ds.space.mul(0, 7), pb: ds.space[2] }}>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                fontFamily: ds.font.sans,
              }}
            >
              Chat History
            </Typography>
            <Tooltip title='Collapse Recent' placement='bottom'>
              <Box>
                <Button
                  tone='secondary'
                  size='sm'
                  composition='icon-only'
                  aria-label='Collapse Recent'
                  icon={<SafeIcon src={CollapseLeftIcon} width={14} height={14} alt='collapse' />}
                  onClick={(e) => {
                    e.stopPropagation();
                    onCollapseConversationList?.();
                  }}
                />
              </Box>
            </Tooltip>
          </Box>

          {/* Search bar */}
          <Box sx={{ px: ds.space[3], pb: ds.space[3] }}>
            <SearchInput
              value={searchText}
              onChange={(e) => {
                setSearchText(e);
              }}
              id='search-chat'
              label='Search conversations...'
              onEnterPress={() => {
                if (pollingTimeoutRef.current) {
                  clearTimeout(pollingTimeoutRef.current);
                }
                fetchConversations('on-enter');
              }}
            />
          </Box>

          {/* Row 1: scope (Mine/Everyone) */}
          <Box sx={{ px: ds.space[3] }}>
            <ToggleButtons
              options={[
                { value: 'Mine', label: 'Mine', icon: UserIconOutline },
                { value: 'Everyone', label: 'Everyone', icon: LogEventsIcon },
              ]}
              activeValue={scope}
              size='sm'
              onChange={(value) => handleScopeChange(value)}
            />
          </Box>

          {/* Row 2: type menu, then Saved, then Waiting */}
          <Box sx={{ px: ds.space[3], pt: ds.space[2], pb: ds.space[2], display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3) }}>
            <Box sx={{ flex: 1, display: 'flex' }}>
              <DropdownMenu
                align='start'
                size='sm'
                disablePortal={false}
                trigger={
                  <Box
                    data-testid='conv-type-menu'
                    sx={{
                      width: '100%',
                      px: ds.space.mul(0, 5),
                      py: ds.space[1],
                      borderRadius: ds.space.mul(1, 5),
                      fontSize: 'var(--ds-text-caption)',
                      fontFamily: ds.font.sans,
                      fontWeight: selectedType !== 'All' ? 500 : 400,
                      whiteSpace: 'nowrap',
                      cursor: 'pointer',
                      display: 'inline-flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      gap: ds.space[1],
                      backgroundColor: selectedType !== 'All' ? 'var(--ds-blue-100)' : 'var(--ds-background-200)',
                      color: selectedType !== 'All' ? 'var(--ds-blue-500)' : 'var(--ds-gray-400)',
                      border: selectedType !== 'All' ? `1px solid var(--ds-blue-500)` : '1px solid transparent',
                    }}
                  >
                    <SafeIcon src={FilterIcon} alt='' width={12} height={12} />
                    {typeTriggerLabel} ▾
                  </Box>
                }
                itemsMaxHeight='70vh'
                items={[
                  { label: 'All types', active: selectedType === 'All', onSelect: () => handleTypeSelect('All') },
                  {
                    label: 'Chats',
                    description: 'User Chat · Slack Channel',
                    active: selectedType === 'Chats',
                    onSelect: () => handleTypeSelect('Chats'),
                  },
                  {
                    label: 'System',
                    description: 'Event Analysis · Automation · Optimize',
                    active: selectedType === 'System',
                    onSelect: () => handleTypeSelect('System'),
                  },
                  {
                    label: 'Query transcripts',
                    description: 'Prometheus · Loki · ES',
                    active: selectedType === 'Queries',
                    onSelect: () => handleTypeSelect('Queries'),
                  },
                  { type: 'section', label: 'Specific source' },
                  ...conversationSources.map((src) => ({
                    label: src.label,
                    active: selectedType === src.value,
                    onSelect: () => handleTypeSelect(src.value),
                  })),
                ]}
              />
            </Box>
            {[
              { value: 'Saved', label: 'Saved', count: savedCount, icon: SaveIconOutline },
              { value: 'Waiting', label: 'Waiting', count: waitingCount, icon: RunningIcon },
            ].map((chip) => {
              const isActive = stateFilter === chip.value;
              return (
                <Box
                  key={chip.value}
                  title={chip.count != null ? `${chip.count} ${chip.value.toLowerCase()} conversation${chip.count === 1 ? '' : 's'}` : undefined}
                  onClick={() => handleStateToggle(chip.value)}
                  data-testid={`conv-state-${chip.value.toLowerCase()}`}
                  sx={{
                    flex: 1,
                    display: 'inline-flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    gap: ds.space[1],
                    px: ds.space.mul(0, 5),
                    py: ds.space[1],
                    borderRadius: ds.space.mul(1, 5),
                    fontSize: 'var(--ds-text-caption)',
                    fontFamily: ds.font.sans,
                    fontWeight: isActive ? 500 : 400,
                    whiteSpace: 'nowrap',
                    cursor: 'pointer',
                    backgroundColor: isActive ? 'var(--ds-blue-100)' : 'var(--ds-background-200)',
                    color: isActive ? 'var(--ds-blue-500)' : 'var(--ds-gray-400)',
                    border: isActive ? `1px solid var(--ds-blue-500)` : '1px solid transparent',
                    transition: 'all 0.2s ease',
                  }}
                >
                  <Box component='span' sx={{ display: 'inline-flex', filter: isActive ? 'none' : 'grayscale(1)', opacity: isActive ? 1 : 0.65 }}>
                    <SafeIcon src={chip.icon} alt='' width={12} height={12} />
                  </Box>
                  {chip.label}
                </Box>
              );
            })}
          </Box>
        </Box>
        <Box
          ref={scrollContainerRef}
          sx={{
            width: '100%',
            backgroundColor: 'var(--ds-background-100)',
            flex: 1,
            overflowY: 'auto',
            '::-webkit-scrollbar': { width: ds.space[1] },
          }}
          onScroll={handleScroll}
        >
          <List sx={{ pt: ds.space[1] }}>
            {groupedConversations.map((item, index) =>
              item.type === 'header' ? (
                <Typography
                  key={`header-${item.label}`}
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    fontFamily: ds.font.sans,
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: 'var(--ds-gray-400)',
                    textTransform: 'uppercase',
                    letterSpacing: '0.5px',
                    px: ds.space.mul(1, 5),
                    pt: index === 0 ? ds.space[2] : ds.space[4],
                    pb: ds.space.mul(0, 3),
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
                    p: `${ds.space[1]} ${ds.space[3]}`,
                    mx: ds.space[2],
                    borderRadius: ds.radius.lg,
                    '&.Mui-selected': {
                      backgroundColor: 'var(--ds-blue-100)',
                    },
                    '&:hover': {
                      backgroundColor: 'var(--ds-blue-100)',
                      '&.Mui-selected': {
                        backgroundColor: 'var(--ds-blue-100)',
                      },
                    },
                  }}
                >
                  <Box sx={{ width: '100%' }}>
                    <Box sx={{ py: ds.space.mul(0, 5) }}>
                      {/* Row 1: Status icon + Title + Menu */}
                      <Box display='flex' alignItems='flex-start' gap={ds.space.mul(0, 3)} position='relative'>
                        <Tooltip title={item.data.status}>
                          <SafeIcon
                            src={getStatusIcon(item.data.status)}
                            alt={item.data.status}
                            height={14}
                            width={14}
                            style={{ marginTop: ds.space[0], flexShrink: 0 }}
                          />
                        </Tooltip>
                        <Text
                          value={item.data.message}
                          showAutoEllipsis
                          sx={{
                            fontSize: 'var(--ds-text-small)',
                            fontFamily: ds.font.sans,
                            fontWeight: 'var(--ds-font-weight-regular)',
                            pr: ds.space.mul(0, 9),
                            flex: 1,
                            lineHeight: '16px',
                          }}
                        />
                        {hoveredItemId === item.data.id && (
                          <Box
                            onClick={(e) => e.stopPropagation()}
                            sx={{
                              position: 'absolute',
                              right: '-5px',
                              top: '-2px',
                              width: ds.space.mul(1, 5),
                              height: ds.space.mul(1, 5),
                              '& svg': { width: ds.space[4], height: ds.space[4] },
                            }}
                          >
                            <ThreeDotsMenu
                              icon
                              menuItems={getMenuItems(item.data.email, item.data.id)}
                              onMenuClick={onMenuClick}
                              lightIcon={'var(--ds-blue-500)'}
                              menuWidth='137px'
                              data={item.data}
                            />
                          </Box>
                        )}
                      </Box>
                      {/* Row 2: Source badge + Author */}
                      <Box display='flex' alignItems='center' gap={ds.space.mul(0, 3)} mt={ds.space[1]} sx={{ pl: ds.space.mul(1, 5) }}>
                        {item.data.source && sourceLabels[item.data.source] && (
                          <Box
                            sx={{
                              px: ds.space.mul(0, 3),
                              py: ds.space[0],
                              borderRadius: ds.radius.sm,
                              fontSize: 'var(--ds-text-caption)',
                              fontFamily: ds.font.sans,
                              fontWeight: 'var(--ds-font-weight-regular)',
                              whiteSpace: 'nowrap',
                              backgroundColor: 'var(--ds-background-200)',
                              color: 'var(--ds-gray-500)',
                            }}
                          >
                            {sourceLabels[item.data.source]}
                          </Box>
                        )}
                        {scope !== 'Mine' && (
                          <Typography
                            sx={{
                              fontSize: 'var(--ds-text-caption)',
                              fontFamily: ds.font.sans,
                              color: 'var(--ds-gray-400)',
                              overflow: 'hidden',
                              textOverflow: 'ellipsis',
                              whiteSpace: 'nowrap',
                              maxWidth: ds.space.mul(2, 15),
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
            <Box sx={{ px: ds.space[4], pt: ds.space[3], display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
              {['s1', 's2', 's3', 's4', 's5', 's6'].map((id) => (
                <Skeleton key={id} shape='rect' height='52px' width='94%' />
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
