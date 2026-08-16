import { useRef, useEffect, useState, useMemo } from 'react';
import { Box, List, ListItemButton, Typography, Popper, ClickAwayListener } from '@mui/material';
import PropTypes from 'prop-types';
import Text from '@shared/format/Text';
import Tooltip from '@ui/Tooltip';
import SafeIcon from '@shared/icons/SafeIcon';
import { ShareIconBlue, DeleteIconRed, SaveIconOutlinelight, SaveIconOutlineselect } from '@assets';
import { resolveStatusLabel, getStatusIcon } from '@utils/conversationStatus';
import { Skeleton } from '@ui/Skeleton';
import apiAskNudgebee from '@api1/ask-nudgebee';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import { ds } from '@utils/colors';
import { toast as snackbar } from '@ui/Toast';
import { getUserSession } from '@lib/auth';

const PAGE_SIZE = 10;

const ConversationListDrawer = ({
  accountId,
  onSelectConversation,
  selectedId,
  isVisible,
  onClose,
  handleLike,
  likedConversations,
  setLikedConversations,
  triggerButtonRef,
}) => {
  const pollingTimeoutRef = useRef(null);
  const loadingRef = useRef(false);
  const scrollContainerRef = useRef(null);
  const latestLastRecordedAtRef = useRef('');
  // Guards for async callbacks: mounted-state and whether polling is still active.
  const isMountedRef = useRef(true);
  const isPollingActiveRef = useRef(false);
  // Active account at resolve time — drops responses from a previous account.
  const accountIdRef = useRef(accountId);

  const [rawConversations, setRawConversations] = useState([]);
  const [loading, setLoading] = useState(false);
  const [hoveredItemId, setHoveredItemId] = useState(null);
  const [page, setPage] = useState(0);
  const [hasMore, setHasMore] = useState(true);

  const [persistedSelectedId, setPersistedSelectedIdLocal] = useState(() => {
    if (typeof window !== 'undefined') {
      return localStorage.getItem('nubi_selected_conversation_id') || selectedId || '';
    }
    return selectedId || '';
  });

  const setPersistedSelectedId = (id) => {
    if (id) {
      localStorage.setItem('nubi_selected_conversation_id', id);
    }
    setPersistedSelectedIdLocal(id);
  };

  const sourceLabels = {
    UserInvestigation: 'User Chat',
    Optimize: 'Optimize',
    PrometheusQuery: 'Prometheus Query',
    LokiQuery: 'Loki Query',
    ESQuery: 'ES Query',
    Investigation: 'Event Analysis',
    InstantNotification: 'Slack Channel',
    Automation: 'Automation',
  };

  const selectedSources = [
    'UserInvestigation',
    'Optimize',
    'PrometheusQuery',
    'LokiQuery',
    'ESQuery',
    'Investigation',
    'InstantNotification',
    'Automation',
  ];

  const fetchConversations = (source = 'polling') => {
    if (loadingRef.current) return;
    const requestAccountId = accountId;
    if (source === 'on-enter') {
      setRawConversations([]);
      setLikedConversations([]);
      latestLastRecordedAtRef.current = '';
    }
    loadingRef.current = true;
    setLoading(true);

    const query = {
      account_id: accountId,
      source: selectedSources,
      limit: PAGE_SIZE,
      offset: source === 'page' ? page * PAGE_SIZE : 0,
      latestLastRecordedAt: source === 'polling' ? latestLastRecordedAtRef.current : '',
      activeFilter: 'Mine',
      searchText: '',
      skipTotalCount: true,
      user_username: getUserSession()?.user?.email,
    };

    apiAskNudgebee
      .llmConversationHistory(query)
      .then((res) => {
        if (requestAccountId !== accountIdRef.current) {
          return;
        }
        const llmConversations = res?.data?.data?.llm_conversations ?? [];
        if (source !== 'polling' || latestLastRecordedAtRef.current === '') {
          setHasMore(llmConversations.length === PAGE_SIZE);
        }
        if (llmConversations.length) {
          if (source === 'polling') {
            latestLastRecordedAtRef.current = llmConversations[0].updated_at;
          }
          setRawConversations((prevConversations) => {
            if (source === 'polling') {
              const existingMap = new Map(prevConversations.map((c) => [c.id, c]));
              const merged = [];

              llmConversations.forEach((newConv) => {
                if (existingMap.has(newConv.id)) {
                  merged.push({ ...existingMap.get(newConv.id), ...newConv });
                  existingMap.delete(newConv.id);
                } else {
                  merged.push(newConv);
                }
              });

              merged.push(...existingMap.values());
              return merged;
            }
            if (source === 'page') {
              const existingIds = new Set(prevConversations.map((c) => c.id));
              return [...prevConversations, ...llmConversations.filter((c) => !existingIds.has(c.id))];
            }
            return llmConversations;
          });

          const likedIds =
            llmConversations
              .map((f) => f.llm_conversation_saveds?.map((g) => g.conversation_id))
              ?.filter((id) => id)
              .flat() ?? [];
          setLikedConversations((prev) => {
            const likedSet = new Set(prev);
            likedIds.forEach((id) => {
              likedSet.add(id);
            });
            return Array.from(likedSet);
          });
        }
      })
      .catch(() => {
        // Transient poll failures are non-fatal
      })
      .finally(() => {
        if (!isMountedRef.current) {
          return;
        }
        loadingRef.current = false;
        setLoading(false);
        if (source === 'polling' && isPollingActiveRef.current) {
          pollingTimeoutRef.current = setTimeout(() => {
            fetchConversations('polling');
          }, 5000);
        }
      });
  };

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  // Account switch: drop the previous account's data so polling can't merge it
  // into the new account's list, and invalidate any in-flight request.
  useEffect(() => {
    accountIdRef.current = accountId;
    setRawConversations([]);
    setLikedConversations([]);
    latestLastRecordedAtRef.current = '';
  }, [accountId]);

  useEffect(() => {
    if (pollingTimeoutRef.current) {
      clearTimeout(pollingTimeoutRef.current);
    }

    if (!isVisible) {
      isPollingActiveRef.current = false;
      return;
    }

    isPollingActiveRef.current = true;
    setPage(0);
    setHasMore(true);
    latestLastRecordedAtRef.current = '';
    fetchConversations('polling');

    return () => {
      isPollingActiveRef.current = false;
      if (pollingTimeoutRef.current) {
        clearTimeout(pollingTimeoutRef.current);
      }
    };
  }, [accountId, isVisible]);

  useEffect(() => {
    if (page > 0) {
      fetchConversations('page');
    }
  }, [page]);

  const loadMoreConversations = () => {
    if (!loadingRef.current && hasMore) {
      setPage((prev) => prev + 1);
    }
  };

  const handleScroll = (event) => {
    const el = event.target;
    if (!el || loadingRef.current || !hasMore || !isVisible) {
      return;
    }
    const { scrollTop, scrollHeight, clientHeight } = el;
    if (scrollTop + clientHeight >= scrollHeight - 50) {
      loadMoreConversations();
    }
  };

  useEffect(() => {
    if (selectedId) {
      setPersistedSelectedId(selectedId);
    }
  }, [selectedId]);

  const conversations = useMemo(() => {
    return rawConversations.map((item) => {
      let message = item.title || '';
      if (message.includes('"query"')) {
        try {
          const parsed = JSON.parse(message);
          if (parsed && typeof parsed === 'object' && parsed.query) {
            message = parsed.query;
          }
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

  // Auto-select the most recent chat when the persisted selection isn't in the
  // list (e.g. a stale localStorage id). Kept out of the memo so it stays pure.
  useEffect(() => {
    if (conversations.length > 0 && persistedSelectedId) {
      const isSelected = conversations.some((c) => c.sessionId === persistedSelectedId || c.id === persistedSelectedId);
      if (!isSelected) {
        setPersistedSelectedId(conversations[0].sessionId);
      }
    }
  }, [conversations, persistedSelectedId]);

  const getMenuItems = (username, conversationId) => {
    let items = [
      {
        icon: ShareIconBlue,
        label: 'Share',
        id: 'share',
      },
      {
        icon: likedConversations.includes(conversationId) ? SaveIconOutlineselect : SaveIconOutlinelight,
        label: likedConversations.includes(conversationId) ? 'Unsave' : 'Save',
        id: 'toggle-save',
      },
    ];

    if (getUserSession()?.user?.email === username) {
      items.push({
        icon: DeleteIconRed,
        label: 'Delete',
        id: 'delete',
      });
    }

    return items;
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 'delete') {
      apiAskNudgebee
        .deleteConversation({ conversation_id: data.id })
        .then((res) => {
          const success = res?.data?.data?.ai_delete_llm_conversation_by_id?.data?.success ?? false;
          if (success) {
            setRawConversations((prev) => prev.filter((c) => c.id !== data.id));
            snackbar.success('Conversation deleted');
          } else {
            snackbar.error('Failed to delete conversation');
          }
        })
        .catch(() => {
          snackbar.error('Error deleting conversation');
        });
    } else if (menuItem.id === 'toggle-save') {
      handleLike(data.id, likedConversations.includes(data.id));
    }
  };

  const handleSelectConversation = (conversation) => {
    onSelectConversation(conversation.sessionId, conversation.userName ?? '-');
    if (onClose) {
      onClose();
    }
  };

  return (
    <ClickAwayListener onClickAway={() => onClose?.()}>
      <Popper open={isVisible} anchorEl={triggerButtonRef?.current} placement='bottom-end' sx={{ zIndex: 1300 }}>
        <Box
          ref={scrollContainerRef}
          onScroll={handleScroll}
          sx={{
            width: 320,
            maxHeight: 400,
            overflowY: 'auto',
            backgroundColor: 'var(--ds-background-100)',
            borderRadius: ds.radius.lg,
            border: `1px solid ${ds.gray[200]}`,
            boxShadow: `0 ${ds.space[2]} 16px ${ds.gray.alpha[300]}`,
            '::-webkit-scrollbar': { width: ds.space[1] },
          }}
        >
          <Box
            sx={{
              position: 'sticky',
              top: 0,
              zIndex: 1,
              backgroundColor: 'var(--ds-background-100)',
              px: ds.space[3],
              py: ds.space[2],
              borderBottom: `1px solid ${ds.gray[200]}`,
            }}
          >
            <Typography
              sx={{
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: ds.gray[700],
                fontFamily: ds.font.sans,
              }}
            >
              Your Chats
            </Typography>
          </Box>
          <List sx={{ pt: ds.space[1], pb: ds.space[1] }}>
            {conversations.map((conversation) => (
              <ListItemButton
                key={conversation.id}
                onClick={() => {
                  handleSelectConversation(conversation);
                  setPersistedSelectedId(conversation.sessionId);
                }}
                onMouseEnter={() => setHoveredItemId(conversation.id)}
                onMouseLeave={() => setHoveredItemId(null)}
                selected={persistedSelectedId === conversation.sessionId || persistedSelectedId === conversation.id}
                sx={{
                  p: `${ds.space[1]} ${ds.space[3]}`,
                  mx: ds.space[1],
                  mb: ds.space.mul(0, 5),
                  borderRadius: ds.radius.md,
                  '&.Mui-selected': {
                    backgroundColor: 'var(--ds-blue-100)',
                  },
                  '&:hover': {
                    backgroundColor: 'var(--ds-blue-100)',
                  },
                }}
              >
                <Box sx={{ width: '100%' }}>
                  <Box sx={{ py: ds.space.mul(0, 5) }}>
                    <Box display='flex' alignItems='flex-start' gap={ds.space.mul(0, 3)} position='relative'>
                      <Tooltip title={conversation.status}>
                        <Box sx={{ display: 'flex', alignItems: 'center', marginTop: ds.space[0], flexShrink: 0 }}>
                          <SafeIcon src={getStatusIcon(conversation.status)} alt={conversation.status} height={14} width={14} />
                        </Box>
                      </Tooltip>
                      <Text
                        value={conversation.message}
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
                      {hoveredItemId === conversation.id && (
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
                            menuItems={getMenuItems(conversation.email, conversation.id)}
                            onMenuClick={onMenuClick}
                            lightIcon={'var(--ds-blue-500)'}
                            menuWidth='137px'
                            data={conversation}
                          />
                        </Box>
                      )}
                    </Box>

                    <Box display='flex' alignItems='center' gap={ds.space.mul(0, 3)} mt={ds.space[1]} sx={{ pl: ds.space.mul(1, 5) }}>
                      {conversation.source && (
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
                          {sourceLabels[conversation.source] || conversation.source}
                        </Box>
                      )}
                      {likedConversations.includes(conversation.id) && (
                        <Box sx={{ ml: 'auto', display: 'flex', alignItems: 'center', flexShrink: 0 }}>
                          <SafeIcon src={SaveIconOutlineselect} alt='saved' height={12} width={12} />
                        </Box>
                      )}
                    </Box>
                  </Box>
                </Box>
              </ListItemButton>
            ))}

            {loading && (
              <Box sx={{ px: ds.space[3], pt: ds.space[2], display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
                {['s1', 's2', 's3'].map((id) => (
                  <Skeleton key={id} shape='rect' height='40px' width='94%' />
                ))}
              </Box>
            )}

            {!loading && conversations.length === 0 && (
              <Box sx={{ p: ds.space[3], textAlign: 'center' }}>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-400)', fontFamily: ds.font.sans }}>
                  No conversations yet
                </Typography>
              </Box>
            )}
          </List>
        </Box>
      </Popper>
    </ClickAwayListener>
  );
};

ConversationListDrawer.propTypes = {
  accountId: PropTypes.string,
  onSelectConversation: PropTypes.func,
  selectedId: PropTypes.string,
  isVisible: PropTypes.bool,
  onClose: PropTypes.func,
  handleLike: PropTypes.func,
  likedConversations: PropTypes.array,
  setLikedConversations: PropTypes.func,
  triggerButtonRef: PropTypes.shape({ current: PropTypes.any }),
};

export default ConversationListDrawer;
