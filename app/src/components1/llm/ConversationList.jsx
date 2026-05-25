import { useRef, useEffect, useState, useMemo } from 'react';
import { Box, List, ListItemButton, ListItemText, CircularProgress, IconButton, Typography, Tooltip } from '@mui/material';
import PropTypes from 'prop-types';
import { Text } from '@components1/common';
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
} from '@assets';
import apiAskNudgebee from '@api1/ask-nudgebee';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { colors } from 'src/utils/colors';
import { snackbar } from '@components1/common/snackbarService';
import CustomButton from '@components1/common/NewCustomButton';
import CustomSearch from '@components1/common/CustomSearch';
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
  activeFilter,
  setActiveFilter,
  setSelectedConversation,
  rawConversations,
  setRawConversations,
}) => {
  const listRef = useRef(null);
  const latestLastRecordedAtRef = useRef('');
  const previousAccountIdRef = useRef(accountId);
  const pollingTimeoutRef = useRef(null);
  const PAGE_SIZE = 20;
  const router = useRouter();

  const [page, setPage] = useState(0);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [searchText, setSearchText] = useState('');

  const buttonStyle = (filter) => ({
    color: activeFilter === filter ? colors.text.primary : colors.text.tertiary,
    '& img': {
      filter:
        activeFilter === filter
          ? 'brightness(0) saturate(100%) invert(45%) sepia(76%) saturate(521%) hue-rotate(179deg) brightness(93%) contrast(108%)'
          : 'inherit',
    },
  });

  const handleFilterClick = (filter) => {
    setActiveFilter((prevFilter) => (prevFilter === filter ? null : filter));
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
    if (previousAccountIdRef.current !== accountId || source == 'on-enter') {
      setRawConversations([]);
      setPage(0);
      setTotalCount(0);
      setLikedConversations([]);
      previousAccountIdRef.current = accountId;
      latestLastRecordedAtRef.current = '';
    }
    setLoading(true);
    const query = {
      account_id: accountId,
      source: ['UserInvestigation', 'InstantNotification'],
      limit: PAGE_SIZE,
      offset: source !== 'polling' ? page * PAGE_SIZE : 0,
      latestLastRecordedAt: source !== 'polling' ? '' : latestLastRecordedAtRef.current,
      activeFilter: activeFilter,
      searchText: searchText,
    };
    apiAskNudgebee
      .llmConversationHistory(query)
      .then((res) => {
        const llmConversations = res?.data?.data?.llm_conversations ?? [];
        const conversationCount = res?.data?.data?.llm_conversations_aggregate?.aggregate?.count ?? 0;
        setTotalCount(conversationCount);
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
        setLoading(false);
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
    }
  };

  const getMenuItems = (username) => {
    let MENU_ITEMS = [
      {
        icon: ShareIconBlue,
        label: 'Share',
        id: 0,
        activeFilter: 'false',
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
          // ignore
        }
      }

      let status = item.for_status[0]?.status ?? 'FAILED';
      if (status === 'IN_PROGRESS') {
        status = 'Running';
      } else if (status === 'COMPLETED') {
        status = 'Completed';
      } else if (status === 'FAILED') {
        status = 'Failed';
      }

      return {
        id: item.id,
        sessionId: item.session_id,
        message: message,
        created_at: item.created_at,
        status: status,
        userName: item?.user?.display_name ?? '-',
        email: item?.user?.username ?? '-',
      };
    });
  }, [rawConversations]);

  const statusIconMap = useMemo(
    () => ({
      Running: RunningIcon,
      Completed: SuccessIcon,
      Failed: ErrorIcon,
    }),
    []
  );

  const getStatusIcon = (status) => statusIconMap[status] || ErrorIcon;

  const startPolling = () => {
    const pollConversations = () => {
      fetchConversations('polling');
      pollingTimeoutRef.current = setTimeout(pollConversations, 5000);
    };
    pollConversations();
  };

  useEffect(() => {
    if (activeFilter == 'All') {
      setRawConversations([]);
      setPage(0);
      latestLastRecordedAtRef.current = '';
      startPolling();
    } else if (activeFilter == 'Mine' || activeFilter == 'Saved') {
      setRawConversations([]);
      setPage(0);
      latestLastRecordedAtRef.current = '';
      fetchConversations();
    }
    return () => {
      if (pollingTimeoutRef.current) {
        clearTimeout(pollingTimeoutRef.current);
      }
    };
  }, [accountId, activeFilter]);

  useEffect(() => {
    if (latestLastRecordedAtRef.current) {
      fetchConversations('page');
    }
  }, [page]);

  useEffect(() => {
    if (searchText === '') {
      setRawConversations([]);
      setPage(0);
      latestLastRecordedAtRef.current = '';
      if (pollingTimeoutRef.current) {
        clearTimeout(pollingTimeoutRef.current);
      }
      startPolling();
    }
  }, [searchText]);

  const handleSelectConversation = (conversation) => {
    setSelectedConversation(conversation);
    onSelectConversation(conversation.sessionId, conversation.userName ?? '-');
  };

  const handleScroll = () => {
    const listElement = listRef.current;
    if (!listElement || loading || conversations.length >= totalCount) {
      return;
    }

    const { scrollTop, scrollHeight, clientHeight } = listElement;
    if (scrollTop + clientHeight >= scrollHeight - 10) {
      loadMoreConversations();
    }
  };

  const loadMoreConversations = () => {
    if (loading) {
      return;
    }
    setLoading(true);
    setPage(page + 1);
  };

  return (
    <>
      <Box
        position='sticky'
        top='0px'
        display={'flex'}
        alignItems={'left'}
        justifyContent={'space-between'}
        flexDirection={'column'}
        gap='2px'
        zIndex={2}
        backgroundColor={colors.background.conversationCardBG}
        borderBottom={`0.75px solid ${colors.border.vertical}`}
        sx={{ p: !isConversationListVisible ? '5px' : '10px 16px' }}
      >
        <Text value={'Recent Chats'} sx={{ fontSize: '16px', fontWeight: '500', mb: '4px' }} />

        <CustomSearch
          onChange={(e) => {
            setSearchText(e);
          }}
          id='search-chat'
          label='Search'
          onEnterPress={() => {
            if (pollingTimeoutRef.current) {
              clearTimeout(pollingTimeoutRef.current); // Stop polling
            }
            fetchConversations('on-enter');
          }}
          sx={{
            mb: '8px',
            bgcolor: colors.background.white,
            input: {
              padding: '4px 0px !important',
            },
          }}
        />

        <Box
          sx={{
            display: 'flex',
            flexDirection: isConversationListVisible ? 'row' : 'column',
            alignItems: 'center',
            gap: '2px',
            button: {
              padding: '4px',
            },
          }}
        >
          <CustomButton
            size='xSmall'
            startIcon={<SafeIcon src={UserIconOutline} height={16} width={16} alt={'Mine'} />}
            variant='secondary'
            text={'Mine'}
            onClick={() => handleFilterClick('Mine')}
            sx={{
              height: '24px',
              fontWeight: '400',
              backgroundColor: 'transparent',
              fontSize: '12px',
              border: 'none',
              boxShadow: 'none',
              '&:hover': {
                color: colors.text.primaryLight,
                img: {
                  filter: 'brightness(0) saturate(100%) invert(45%) sepia(76%) saturate(521%) hue-rotate(179deg) brightness(93%) contrast(108%)',
                },
              },
              ...buttonStyle('Mine'),
            }}
          />
          <Box sx={{ height: '20px', width: '0.5px', backgroundColor: colors.border.secondary, mx: '4px' }} />
          <CustomButton
            size='xSmall'
            startIcon={<SafeIcon src={SaveIconOutline} height={16} width={16} alt={'Saved'} />}
            variant='secondary'
            text={'Saved'}
            onClick={() => handleFilterClick('Saved')}
            sx={{
              height: '24px',
              fontWeight: '400',
              backgroundColor: 'transparent',
              fontSize: '12px',
              border: 'none',
              boxShadow: 'none',
              '&:hover': {
                color: colors.text.primaryLight,
                img: {
                  filter: 'brightness(0) saturate(100%) invert(45%) sepia(76%) saturate(521%) hue-rotate(179deg) brightness(93%) contrast(108%)',
                },
              },
              ...buttonStyle('Saved'),
            }}
          />
          <Box sx={{ height: '20px', width: '0.5px', backgroundColor: colors.border.secondary, mx: '4px' }} />

          <CustomButton
            size='xSmall'
            startIcon={<SafeIcon src={LogEventsIcon} height={14} width={14} alt={'All chats'} />}
            variant='secondary'
            text={'All'}
            onClick={() => handleFilterClick('All')}
            sx={{
              height: '24px',
              fontWeight: '400',
              backgroundColor: 'transparent',
              fontSize: '12px',
              border: 'none',
              boxShadow: 'none',
              '&:hover': {
                color: colors.text.primaryLight,
                img: {
                  filter: 'brightness(0) saturate(100%) invert(45%) sepia(76%) saturate(521%) hue-rotate(179deg) brightness(93%) contrast(108%)',
                },
              },
              ...buttonStyle('All'),
            }}
          />
        </Box>
      </Box>
      <Box
        sx={{
          width: '100%',
          backgroundColor: colors.background.conversationCardBG,
          borderRadius: '0px 0px 8px 8px',
          height: 'calc(100% - 186px)',
          overflow: 'auto',
          transition: 'transform 0.3s ease-in-out',
          transform: isConversationListVisible ? 'translateX(0)' : 'translateX(-100%)',
          visibility: isConversationListVisible ? 'visible' : 'hidden',
          opacity: isConversationListVisible ? 1 : 0,
          ...(!isConversationListVisible ? { height: '0px', overflow: 'hidden' } : {}),
          '&::-webkit-scrollbar': {
            width: '3px',
          },
        }}
        onScroll={handleScroll}
        ref={listRef}
      >
        <List>
          {conversations.map((conversation) => (
            <ListItemButton
              key={conversation.id}
              onClick={() => handleSelectConversation(conversation)}
              selected={selectedId === conversation.sessionId}
              sx={{
                p: '0px',
                '&.Mui-selected': {
                  backgroundColor: colors.primarylight,
                  color: colors.text.primary,
                  border: `0.5px solid ${colors.button.tertiaryDisabledText}`,
                  borderRadius: '8px',
                  gap: '40px',
                },
              }}
            >
              <Box
                sx={{
                  m: '0px 16px 0px 16px',
                  width: '100%',
                  borderBottom: `0.75px solid ${colors.border.vertical}`,
                  '&.Mui-selected': {
                    backgroundColor: colors.background.selectedConversation,
                    color: colors.text.primary,
                  },
                }}
              >
                <ListItemText
                  primary={
                    <Tooltip title={conversation.message} placement='right'>
                      <Typography
                        sx={{
                          display: '-webkit-box',
                          WebkitBoxOrient: 'vertical',
                          WebkitLineClamp: 2,
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          fontSize: '12px',
                          fontWeight: 400,
                          lineHeight: '16px',
                          color: colors.text.secondary,
                          mb: '6px',
                          fontFamily: 'Roboto',
                        }}
                      >
                        {conversation.message}
                      </Typography>
                    </Tooltip>
                  }
                  secondary={
                    <div style={{ display: 'flex', flexDirection: 'row', justifyContent: 'space-between' }}>
                      <Box display='flex' alignItems='center' gap='4px'>
                        <SafeIcon
                          src={getStatusIcon(conversation.status)}
                          alt='conversation status'
                          title={conversation.status}
                          height={12}
                          width={12}
                        />
                        <Tooltip title={conversation.userName ?? '-'}>
                          <Box>
                            <Text
                              value={`${(conversation.userName ?? '-').substring(0, 18)}${(conversation.userName ?? '-').length > 18 ? '...' : ''}`}
                              secondaryText
                              sx={{ fontFamily: 'Roboto', fontSize: '11px' }}
                            />
                          </Box>
                        </Tooltip>
                      </Box>
                      <Box display='flex' alignItems='center' gap='2px'>
                        <Tooltip title={likedConversations.includes(conversation.id) ? 'Unsave' : 'Save'} placement='top' arrow>
                          <IconButton
                            onClick={(e) => {
                              e.stopPropagation();
                              handleLike(conversation.id, likedConversations.includes(conversation.id));
                            }}
                            disabled={savingStates[conversation.id]}
                            sx={{
                              width: '16px',
                              height: '16px',
                              padding: '0px !important',
                              borderRadius: '4px',
                            }}
                          >
                            {savingStates[conversation.id] ? (
                              <CircularProgress size={16} />
                            ) : (
                              <SafeIcon
                                src={likedConversations.includes(conversation.id) ? SaveIconOutlineselect : SaveIconOutlinelight}
                                width='14px'
                                height='14px'
                                alt='like'
                                style={{ width: '14px', height: '14px' }}
                              />
                            )}
                          </IconButton>
                        </Tooltip>
                        <ThreeDotsMenu
                          icon
                          menuItems={getMenuItems(conversation.email)}
                          onMenuClick={onMenuClick}
                          lightIcon={colors.text.primary}
                          sx={{
                            width: '20px',
                            height: '20px',
                            padding: '0px !important',
                            borderRadius: '4px',
                            '& svg': {
                              width: '16px',
                              height: '16px',
                            },
                          }}
                          data={conversation}
                        />
                      </Box>
                    </div>
                  }
                  sx={{ wordBreak: 'break-word', m: '0px', py: '12px' }}
                />
              </Box>
            </ListItemButton>
          ))}
        </List>
        {loading && (
          <Box display='flex' justifyContent='center' alignItems='center' pt={2} pb={4}>
            <CircularProgress size={24} />
          </Box>
        )}
      </Box>
    </>
  );
};

ConversationList.propTypes = {
  accountId: PropTypes.string,
  onSelectConversation: PropTypes.func.isRequired,
  selectedId: PropTypes.string,
  activeFilter: PropTypes.string,
  isConversationListVisible: PropTypes.bool,
  searchText: PropTypes.string,
  triggerHandleNewChat: PropTypes.func.isRequired,
  handleShare: PropTypes.func,
};

export default ConversationList;
