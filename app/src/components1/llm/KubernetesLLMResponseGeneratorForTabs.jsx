import apiAskNudgebee from '@api1/ask-nudgebee';
import {
  AgentIcon,
  ApplicationsIcon,
  ChatRoundedIcon,
  PvPvcIcon,
  SaveIconOutlinelight,
  SaveIconOutlineselect,
  SecurityIcon,
  ShareIconBlue,
  ToolsIcon,
  UsersIcon,
} from '@assets';
import { Modal } from '@components1/common/modal';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import { applyFiltersOnRouter } from '@lib/router';
import { Avatar, Box, CircularProgress, Divider, ToggleButton, ToggleButtonGroup, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import { useEffect, useRef, useState } from 'react';
import { colors } from 'src/utils/colors';
import { Text } from '@components1/common';
import { getUserSession } from '@lib/auth';
import { IoIosLogIn, IoIosStats } from 'react-icons/io';
import TextSelectionHoverBox from './common/TextSelectionHoverBox';
import LLMConversationWithTabs from './LLMConversationWithTabs';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import ShimmerLoading from '@components1/common/NewShimmerloading';
import { v4 as uuidv4 } from 'uuid';
import AutoSuggestTextarea from '@components1/k8s/common/TextArea';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import ListAgents from './ListAgents';
import ListTools from './ListTools';
import CustomTooltip from '@components1/common/CustomTooltip';

const KubernetesLLMResponseGenerator = ({ accountId, query = '', popup = false, sessionId = '' }) => {
  const router = useRouter();
  const previousAccountIdRef = useRef(accountId);
  const textareaRef = useRef(null);
  const bottomRef = useRef(null);
  const processedSessionIds = useRef(new Set());
  const isChatScreen = router.query.session_id;

  const [generateQuestionText, setGenerateQuestionText] = useState('');
  const [isLoadingInvestigation, setIsLoadingInvestigation] = useState(false);
  const [messages, setMessages] = useState([]);
  const [conversationStatus, setConversationStatus] = useState('');
  const [intervalCounter, setIntervalCounter] = useState(0);
  const [expandedCardIndex, setExpandedCardIndex] = useState(null);
  const [collapsedObj, setCollapsedObj] = useState({});
  const [openSettingsModal, setOpenSettingsModal] = useState(false);
  const [typeSelected, setTypeSelected] = useState('agents');

  const [conversationIdAtDb, setConversationIdAtDb] = useState('');
  const [showFullText, setShowFullText] = useState(false);
  const [allAgents, setAllAgents] = useState([]);
  const [enabledAgents, setEnabledAgents] = useState([]);
  const [loadingAgents, setLoadingAgents] = useState(false);
  const [savingStates, setSavingStates] = useState({});
  const [likedConversations, setLikedConversations] = useState([]);
  const [activeFilter, _setActiveFilter] = useState('All');
  const [selectedConversation, _setSelectedConversation] = useState(null);

  const [_conversationSuggestions, setConversationSuggestions] = useState([]);
  const [_rawConversations, setRawConversations] = useState([]);

  const [conversationTitle, setConversationTitle] = useState('');
  const [selectedSessionId, setSelectedSessionId] = useState(router.query.session_id || sessionId || '');

  const uniqueParticipantNames = new Set();
  messages.forEach((msg) => {
    if (msg?.user && typeof msg?.user === 'string' && msg.user?.trim() !== '') {
      uniqueParticipantNames.add(msg?.user);
    }
  });
  const participantCount = uniqueParticipantNames.size;

  const queryCount = messages?.filter((message) => message.type === 'question' || message.tool === 'question').length;

  let _hasShownResponse = false;

  const showFullTextHandler = () => {
    setShowFullText(!showFullText);
  };

  const handleLike = (sessionId, starred) => {
    if (savingStates[sessionId]) {
      return;
    }
    setSavingStates((prev) => ({ ...prev, [sessionId]: true }));

    if (!starred) {
      apiAskNudgebee
        .saveConversation({
          conversation_id: sessionId,
        })
        .then((res) => {
          const response = res?.data?.data?.ai_save_conversation?.data?.success ?? false;
          if (response) {
            setLikedConversations((prev) => {
              if (prev.includes(sessionId)) {
                return prev;
              }
              return [...prev, sessionId];
            });
            snackbar.success('Conversation saved successfully');
          } else {
            snackbar.error('Failed to save conversation');
          }
        })
        .catch((error) => {
          console.error('Error saving conversation:', error);
          snackbar.error('An error occurred while saving the conversation');
        })
        .finally(() => {
          setSavingStates((prev) => ({ ...prev, [sessionId]: false }));
        });
    } else {
      setLikedConversations((prev) => prev.filter((id) => id !== sessionId));
      apiAskNudgebee
        .deleteSavedConversation({
          conversation_id: sessionId,
        })
        .then((res) => {
          const success = res?.data?.data?.ai_delete_saved_conversation?.data?.success ?? false;
          if (!success) {
            snackbar.error('Failed to unsave the conversation');
          } else {
            setLikedConversations((prev) => prev.filter((id) => id !== sessionId));
            if (activeFilter == 'Saved') {
              setRawConversations((prevConversations) => prevConversations.filter((convo) => convo.id !== sessionId));
            }
            snackbar.success('Conversation unsaved successfully');
          }
        })
        .catch((error) => {
          console.error('Error unsaving conversation:', error);
          snackbar.error('An error occurred while unsaving the conversation');
        })
        .finally(() => {
          setSavingStates((prev) => ({ ...prev, [sessionId]: false }));
        });
    }
  };

  const handleShare = () => {
    navigator.clipboard.writeText(
      window.location.origin + window.location.pathname + `?accountId=${accountId}&session_id=${router.query.session_id}`
    );
    snackbar.success('Link copied to clipboard');
  };

  useEffect(() => {
    if (messages.length > 0) {
      const timeoutId = setTimeout(() => {
        bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
      }, 1000);
      return () => {
        clearTimeout(timeoutId);
      };
    }
  }, [messages]);

  const listAgents = () => {
    setAllAgents([]);
    setLoadingAgents(true);
    apiAskNudgebee.listAgents({ accountId }).then((res) => {
      let listAgentResponse = res?.data?.data?.ai_list_agents?.data ?? [];
      if (listAgentResponse.length > 0) {
        const agents = listAgentResponse
          .filter((agent) => agent.status === 'enabled')
          .map((agent) => {
            return { name: agent.name, display_name: agent.aliases?.[0] ?? agent.name };
          });
        setEnabledAgents(agents.sort());
        setAllAgents(listAgentResponse);
      }
      setLoadingAgents(false);
    });
  };

  useEffect(() => {
    listAgents();
  }, [accountId]);

  const addMessagesSequentially = async (allMessages) => {
    const newMessages = [...messages];
    for (const element of allMessages) {
      newMessages.push(element);
      setMessages([...newMessages]);
    }
  };
  const handleCardClick = (index) => {
    setExpandedCardIndex(index === expandedCardIndex ? null : index);
    setCollapsedObj((prev) => ({ ...prev, [index]: !prev[index] }));
  };

  const fetchBotResponse = async (source = 'poll') => {
    if (!selectedSessionId) {
      return;
    }
    setIsLoadingInvestigation(true);
    try {
      // Use lightweight polling query for poll requests (no heavy TOAST columns)
      const fetchFn = source === 'poll' ? apiAskNudgebee.getLlmConversationPolling : apiAskNudgebee.getLlmConversation;
      const res = await fetchFn({
        accountId: accountId,
        sessionId: selectedSessionId,
      });
      const errors = res?.data?.errors ?? [];
      if (errors.length > 0) {
        setIsLoadingInvestigation(false);
        snackbar.error('Failed to load Conversation');
        setConversationStatus('FAILED');
        return;
      }
      const conversationResponses = res?.data?.data?.llm_conversations ?? [];
      if (conversationResponses.length > 0) {
        setConversationTitle(conversationResponses[0].title);
        setConversationIdAtDb(conversationResponses[0].id);
        let response = conversationResponses[conversationResponses?.length - 1];
        const conversationMessages = response?.llm_conversation_messages ?? [];
        const allMessages = [];
        const followupMessages = {};
        if (conversationMessages.length > 0) {
          conversationMessages.forEach((cm, _i) => {
            if (cm.message_type == 'followup') {
              if (!followupMessages[cm.parent_agent_id]) {
                followupMessages[cm.parent_agent_id] = [cm];
              } else {
                followupMessages[cm.parent_agent_id].push(cm);
              }
            }
          });

          conversationMessages.forEach((conversationMessage) => {
            if (conversationMessage.message_type == 'followup') {
              return;
            }
            setConversationStatus(response.status);
            const conversationResponse = conversationMessage.response && response.status != 'WAITING' ? conversationMessage.response : '';
            try {
              let toolRequestResponse = {};
              let messageSequence = [];
              const responseReferences = [];
              let agentsWithoutRouter = conversationMessage?.llm_conversation_agents?.filter((r) => r.agent_name != 'router') ?? [];

              if (!messageSequence.includes(conversationMessage.id + '-question')) {
                messageSequence.push(conversationMessage.id + '-question');
                toolRequestResponse[conversationMessage.id + '-question'] = {
                  messageId: conversationMessage.id,
                  text: conversationMessage.message,
                  type: 'question',
                  created_at: conversationMessage.created_at,
                  updated_at: conversationMessage.updated_at,
                  user: conversationMessage?.user?.display_name || 'System',
                };
                // Add acknowledgment as a final response if it exists
                if (conversationMessage.ack_message && conversationMessage.ack_message.trim() !== '') {
                  messageSequence.push(conversationMessage.id + '-acknowledgment');
                  toolRequestResponse[conversationMessage.id + '-acknowledgment'] = {
                    text: conversationMessage.ack_message,
                    content: conversationMessage.ack_message,
                    type: 'acknowledgment',
                    created_at: conversationMessage.created_at,
                    updated_at: conversationMessage.updated_at,
                    user: conversationMessage?.user?.display_name || 'System',
                  };
                }
              }
              let firstAgent = agentsWithoutRouter?.length > 0 ? agentsWithoutRouter[0] : {};
              let childAgentsToSkip = [];

              let hasPlanner = false;
              agentsWithoutRouter
                .filter((r) => r.parent_agent_id == firstAgent.parent_agent_id || r.parent_agent_id == firstAgent.id)
                .map((r) => {
                  hasPlanner = hasPlanner || r.agent_name == 'planner';
                  return r;
                })
                .forEach((message) => {
                  let followups = followupMessages[message.id];
                  if (followups) {
                    var i = 0;
                    for (const followupMessage of followups) {
                      messageSequence.push(followupMessage.id + '-followup-question-' + i);
                      toolRequestResponse[followupMessage.id + '-followup-question-' + i] = {
                        text: followupMessage.message,
                        type: 'followup-question',
                        tool: 'followup-question',
                        ack_message: followupMessage.ack_message,
                        response: {
                          type: 'followup-response',
                          text: followupMessage.response,
                          status: followupMessage.status,
                          message_config: followupMessage.message_config,
                          message_id: conversationMessage.id,
                          account_id: accountId,
                          conversation_id: response.id,
                          agent_id: message.id,
                        },
                      };
                      i++;
                    }
                  }

                  if (hasPlanner && message?.agent_name == firstAgent.agent_name) {
                    return;
                  }

                  for (const t of message?.llm_conversation_tool_calls ?? []) {
                    if (t.child_agent_id) {
                      childAgentsToSkip.push(t.child_agent_id);
                    }
                  }

                  // do not show child agents
                  if (childAgentsToSkip.includes(message.id)) {
                    return;
                  }

                  // parsing
                  for (const t of message?.llm_conversation_tool_calls ?? []) {
                    if (t.references) {
                      let jsonReferences = JSON.parse(t.references);
                      t.references = jsonReferences;
                      responseReferences.push(...jsonReferences);
                    }
                  }

                  if ((message?.llm_conversation_tool_calls?.length ?? 0) == 0 && message?.agent_name != 'planner') {
                    // debug agents are followed by planner..so dont show debug agent itself instead show planner
                    message['llm_conversation_tool_calls'] = [
                      {
                        thought: message.thought,
                        response: message.response,
                        created_at: message.created_at,
                        updated_at: message.updated_at,
                      },
                    ];
                  }

                  if (!messageSequence.includes(message.id)) {
                    messageSequence.push(message.id);
                  }

                  let toolCallIndexForResponse = message?.llm_conversation_tool_calls?.length - 1 ?? 0;
                  // if its debugger, then dont use LLM response (last response) instead of actual tool-response
                  if (
                    message?.llm_conversation_tool_calls?.[toolCallIndexForResponse]?.tool_name === 'LLM' ||
                    message?.llm_conversation_tool_calls?.[toolCallIndexForResponse]?.tool_name === 'event_summary'
                  ) {
                    toolCallIndexForResponse = toolCallIndexForResponse - 1;
                    if (toolCallIndexForResponse < 0) {
                      toolCallIndexForResponse = 0;
                    }
                  }

                  let query = message?.query;
                  if (query) {
                    try {
                      let query1 = JSON.parse(query);
                      query = query1?.command ?? query;
                    } catch {
                      //ignore
                    }
                  }

                  let parameter = message?.llm_conversation_tool_calls?.[toolCallIndexForResponse]?.parameters ?? '{}';
                  try {
                    parameter = JSON.parse(parameter);
                    if (!parameter.command) {
                      parameter.command = parameter.query;
                    }
                  } catch {
                    parameter = {};
                  }

                  toolRequestResponse[message.id] = {
                    response_text: message.response,
                    response_status: message.status,
                    response_summary: message.response_summary,
                    question: query,
                    log: message?.llm_conversation_tool_calls?.[0]?.thought?.split('\n\nAction:')?.[0] || message.thought?.split('\n\nAction:')?.[0],
                    text: message?.query, //message?.llm_conversation_tool_calls?.[lastToolCall - 1]?.parameters,
                    tool: message?.llm_conversation_tool_calls?.[toolCallIndexForResponse]?.tool_name ?? message?.agent_name,
                    tool_id: message.id,
                    created_at: message.created_at,
                    updated_at: message.updated_at,
                    type: 'tool_call',
                    toolParameters: parameter,
                    references: message?.llm_conversation_tool_calls?.[toolCallIndexForResponse]?.references,
                    response: {
                      type: 'tool_call_response',
                      text:
                        message?.llm_conversation_tool_calls?.[toolCallIndexForResponse]?.response ||
                        message?.llm_conversation_tool_calls?.[0]?.response,
                      created_at:
                        message?.llm_conversation_tool_calls?.[toolCallIndexForResponse]?.created_at ||
                        message?.llm_conversation_tool_calls?.[0]?.created_at,
                      updated_at:
                        message?.llm_conversation_tool_calls?.[toolCallIndexForResponse]?.updated_at ||
                        message?.llm_conversation_tool_calls?.[0]?.updated_at,
                    },
                  };
                });
              if (conversationResponse) {
                messageSequence.push(conversationMessage.id);
                const lastConversationAgent = conversationMessage?.llm_conversation_agents?.at(-1);
                toolRequestResponse[conversationMessage.id] = {
                  text: conversationResponse,
                  type: 'response',
                  created_at: conversationMessage.created_at,
                  updated_at: conversationMessage.updated_at,
                  id: conversationMessage.id,
                  agentName: lastConversationAgent?.agent_name || '',
                  references: responseReferences,
                  ack_message: conversationMessage.ack_message,
                  status: conversationMessage.status,
                };
              }
              const finalData = messageSequence.map((s) => toolRequestResponse[s]);
              allMessages.push(...finalData);
              const followupIndexes = allMessages.map((msg, i) => (msg.type === 'followup-question' ? i : -1)).filter((index) => index !== -1);
              if (followupIndexes.length > 0) {
                setCollapsedObj((prev) => ({
                  ...prev,
                  ...Object.fromEntries(followupIndexes.map((index) => [index, index in prev ? prev[index] : true])),
                }));
              }
            } catch {
              setIsLoadingInvestigation(false);
            }
          });
          if (source !== 'poll') {
            addMessagesSequentially(allMessages);
          } else {
            setMessages(allMessages);
          }
        }
      } else {
        setIsLoadingInvestigation(false);
        if (!(source && sessionId)) {
          snackbar.error('Conversation not found');
        }
      }
    } catch {
      setIsLoadingInvestigation(false);
    }
  };

  useEffect(() => {
    const shouldStopPolling = conversationStatus === 'COMPLETED' || conversationStatus === 'FAILED' || conversationStatus === 'KILLED';

    if (shouldStopPolling) {
      setIsLoadingInvestigation(false);
      if (messages && messages.length > 0) {
        setConversationSuggestions([]);
      }
      return; // Do not start polling
    }

    if (selectedSessionId !== '' && conversationStatus) {
      const id = setInterval(async () => {
        await fetchBotResponse('poll');
        setIntervalCounter(Date.now());
      }, 5000);

      return () => clearInterval(id);
    }
  }, [intervalCounter, conversationStatus]);

  // change ui as conversation changes instead of waiting
  useEffect(() => {
    if (previousAccountIdRef.current !== accountId) {
      setMessages([]);
      setConversationSuggestions([]);
      setGenerateQuestionText('');
      previousAccountIdRef.current = accountId;
      setConversationStatus('');
      setIsLoadingInvestigation(false);
      if (!popup) {
        applyFiltersOnRouter(router, { session_id: '' });
      }
      setSelectedSessionId('');
    } else if (selectedSessionId) {
      setGenerateQuestionText('');
      fetchBotResponse('selected');
    }
  }, [accountId, selectedSessionId]);

  useEffect(() => {
    const checkConversationExists = async () => {
      if (query && popup && selectedSessionId && !processedSessionIds.current.has(selectedSessionId)) {
        try {
          processedSessionIds.current.add(selectedSessionId);
          const res = await apiAskNudgebee.getLlmConversation({
            accountId: accountId,
            sessionId: selectedSessionId,
          });
          const errors = res?.data?.errors ?? [];
          if (errors.length > 0) {
            snackbar.error('Failed to load Conversation');
            setConversationStatus('FAILED');
            if (!popup) {
              applyFiltersOnRouter(router, { session_id: '' });
            }
            return;
          }
          const conversationResponses = res?.data?.data?.llm_conversations ?? [];
          if (conversationResponses.length === 0) {
            handleGenerateInvestigation(query);
          }
        } catch (error) {
          console.error('Error checking conversation:', error);
          snackbar.error('Failed to load conversation');
        }
      }
    };
    checkConversationExists();
  }, [popup, query, selectedSessionId]);

  const handleGenerateInvestigation = async (text) => {
    if (conversationStatus == 'IN_PROGRESS' || !text) {
      return;
    }
    setGenerateQuestionText(text);
    const llmSessionId = selectedSessionId || uuidv4();
    try {
      setConversationStatus('');
      setIsLoadingInvestigation(true);
      setConversationSuggestions([]);
      apiAskNudgebee
        .aiGenerateInvestigate({
          account_id: accountId,
          query: text,
          session_id: llmSessionId,
        })
        .then((res) => {
          const response = res?.data?.data?.ai_trigger_investigation ?? {};
          if (!response?.data?.query) {
            setIsLoadingInvestigation(false);
            setConversationStatus('FAILED');
            if (!popup) {
              applyFiltersOnRouter(router, { session_id: '' });
            }
            snackbar.error(parseHttpResponseBodyMessage(response) || 'Cant process your request right now.');
          } else {
            if (!popup) {
              applyFiltersOnRouter(router, { session_id: llmSessionId });
            }
            setSelectedSessionId(llmSessionId);
            setGenerateQuestionText('');
            setConversationStatus('IN_PROGRESS');
          }
        });
    } catch (error) {
      console.error(error);
    }
  };

  useEffect(() => {
    if (!selectedSessionId && textareaRef.current) {
      textareaRef.current.focus();
    }
  }, [selectedSessionId]);

  const _handleNewChat = () => {
    setSelectedSessionId('');
    setMessages([]);
    setGenerateQuestionText('');
    setConversationStatus('');
    setExpandedCardIndex(null);
    setCollapsedObj({});
    setIsLoadingInvestigation(false);
    setConversationSuggestions([]);
    if (!popup) {
      applyFiltersOnRouter(router, { conversation_id: '' });
    }
    setTimeout(() => {
      textareaRef.current?.focus();
    }, 0);
  };

  const getCardTitle = (data) => {
    if (data.question) {
      return data.question;
    } else if (data.log) {
      return data.log.replace('Thought: ', '');
    } else if (data.text.indexOf(': ') == 0) {
      return data.text.replace(': ', '');
    } else if (data.type == 'followup-question') {
      return 'Followup Question: ' + data.text;
    }
    return data.text;
  };

  const ConversationHeaderData = {
    title: conversationTitle || messages[0]?.text,
    queriesAsked: queryCount,
    participants: participantCount,
  };

  return (
    <>
      <Modal
        width='lg'
        title={'Settings'}
        open={openSettingsModal}
        handleClose={() => setOpenSettingsModal(false)}
        onClose={() => setOpenSettingsModal(false)}
      >
        <Box
          display='flex'
          flexDirection={'column'}
          alignItems={'center'}
          my={2}
          sx={{
            button: {
              minWidth: '120px',
            },
          }}
        >
          <ToggleButtonGroup
            color='primary'
            exclusive
            value={typeSelected}
            sx={{
              button: {
                minWidth: '120px',
                height: '36px',
                textTransform: 'inherit',
                color: colors.text.secondary,
              },
              img: {
                filter: 'brightness(0) saturate(100%) invert(23%) sepia(21%) saturate(699%) hue-rotate(178deg) brightness(87%) contrast(85%)',
              },
              '& .Mui-selected': {
                color: `${colors.text.primary} !important`,
                backgroundColor: colors.background.toggle,
                img: {
                  filter: 'brightness(0) saturate(100%) invert(45%) sepia(23%) saturate(3237%) hue-rotate(195deg) brightness(98%) contrast(98%)',
                },
              },
            }}
            onChange={(_event, newValue) => {
              if (newValue) {
                setTypeSelected(newValue);
              }
            }}
            aria-label='Platform'
          >
            <ToggleButton value='agents'>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', fontFamily: 'Roboto', fontSize: '14px', fontWeight: 500 }}>
                <SafeIcon src={AgentIcon} alt='agent' />
                Agents
              </Box>
            </ToggleButton>
            <ToggleButton value='tools'>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', fontFamily: 'Roboto', fontSize: '14px', fontWeight: 500 }}>
                <SafeIcon src={ToolsIcon} alt='agent' />
                Tools
              </Box>
            </ToggleButton>
          </ToggleButtonGroup>
        </Box>
        {typeSelected == 'agents' ? (
          <ListAgents accountId={accountId} allAgents={allAgents} refreshAgentListing={() => listAgents()} loadingAgents={loadingAgents} />
        ) : (
          <ListTools accountId={accountId} />
        )}
      </Modal>
      <Box
        sx={{
          display: 'flex',
          transition: 'grid-template-columns 0.3s ease-in-out',
        }}
      >
        <Box
          sx={{
            height: 'max-content',
            position: 'relative',
            width: '100%',
            overflowX: 'auto',
            px: '0px',
          }}
        >
          {isChatScreen && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                borderBottom: `0.8px solid ${colors.border.vertical}`,
                height: '52px',
                position: 'absolute',
                zIndex: 2,
                top: 0,
                right: 0,
                left: 0,
                px: '40px',
                backgroundColor: colors.background.white,
              }}
            >
              <Box>
                <Text
                  requiredToolTip={false}
                  value={ConversationHeaderData.title}
                  sx={{
                    fontSize: '16px',
                    fontWeight: 500,
                    color: colors.text.secondary,
                    fontFamily: 'Roboto',
                    pr: '30px',
                  }}
                  showAutoEllipsis
                />
              </Box>
              <Box sx={{ display: 'inline-flex', alignItems: 'center' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: '20px' }}>
                  {ConversationHeaderData.queriesAsked > 0 && (
                    <CustomTooltip
                      placement='bottom'
                      title='Queries Asked'
                      tooltipStyle={{
                        backgroundColor: 'white',
                        color: colors.text.secondary,
                        padding: '8px 12px',
                        fontSize: '12px',
                        borderRadius: '4px',
                        boxShadow: '0 2px 4px rgba(0,0,0,0.1)',
                      }}
                    >
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                        <SafeIcon src={ChatRoundedIcon} alt='chat icon' width={14} height={14} />
                        <Typography
                          sx={{
                            fontSize: '12px',
                            fontWeight: 400,
                            color: colors.text.secondaryDark,
                            fontFamily: 'Roboto',
                            span: {
                              color: colors.text.secondary,
                              fontWeight: 500,
                              mr: '6px',
                            },
                          }}
                        >
                          <span>{ConversationHeaderData.queriesAsked}</span>
                        </Typography>
                      </Box>
                    </CustomTooltip>
                  )}
                  {ConversationHeaderData.participants > 0 && (
                    <CustomTooltip
                      tooltipStyle={{
                        backgroundColor: colors.background.white,
                        color: colors.text.secondary,
                        boxShadow: '0 2px 10px rgba(0,0,0,0.1)',
                        padding: 0,
                        border: '1px solid rgba(0,0,0,0.08)',
                        borderRadius: '8px',
                      }}
                      title={
                        <Box
                          sx={{
                            display: 'flex',
                            flexDirection: 'column',
                            gap: '12px',
                            padding: '16px',
                          }}
                        >
                          <Typography
                            sx={{
                              fontSize: '14px',
                              fontWeight: 600,
                              color: colors.text.secondary,
                              borderBottom: `1px solid ${colors.background.input}`,
                              paddingBottom: '8px',
                            }}
                          >
                            Participants
                          </Typography>
                          <Box sx={{ display: 'flex', flexDirection: 'column', flexWrap: 'wrap', gap: '10px' }}>
                            {Array.from(uniqueParticipantNames).map((name, index) => (
                              <Box
                                key={index}
                                sx={{
                                  display: 'flex',
                                  alignItems: 'center',
                                  gap: '8px',
                                  backgroundColor: colors.background.suggestionCardBG,
                                  borderRadius: '16px',
                                  padding: '4px 12px 4px 4px',
                                  transition: 'all 0.2s ease',
                                  '&:hover': {
                                    backgroundColor: colors.background.suggestionCardHover,
                                    transform: 'translateY(-1px)',
                                  },
                                }}
                              >
                                <Avatar
                                  sx={{
                                    bgcolor: colors.text.greyDark,
                                    height: '28px',
                                    width: '28px',
                                    fontSize: '14px',
                                    fontWeight: 400,
                                    fontFamily: 'Roboto',
                                    cursor: 'pointer',
                                  }}
                                >
                                  {name?.trim().split(' ')[0].charAt(0).toUpperCase() +
                                    name?.trim().split(' ')[name?.trim().split(' ').length - 1].charAt(0).toUpperCase()}
                                </Avatar>
                                <Typography
                                  sx={{
                                    fontSize: '13px',
                                    fontWeight: 500,
                                    color: colors.text.secondary,
                                  }}
                                >
                                  {name}
                                </Typography>
                              </Box>
                            ))}
                          </Box>
                        </Box>
                      }
                    >
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                        <SafeIcon src={UsersIcon} alt='users icon' width={14} height={14} />
                        <Typography
                          sx={{
                            fontSize: '12px',
                            fontWeight: 400,
                            color: colors.text.secondaryDark,
                            fontFamily: 'Roboto',
                            span: {
                              color: colors.text.secondary,
                              fontWeight: 500,
                              mr: '6px',
                            },
                          }}
                        >
                          <span>{ConversationHeaderData.participants}</span>
                        </Typography>
                      </Box>
                    </CustomTooltip>
                  )}
                </Box>
                <Divider orientation='vertical' variant='middle' flexItem sx={{ height: '28px', mx: '10px' }} />
                <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                  <CustomButton
                    variant='secondary'
                    sx={{
                      height: '28px',
                      width: '28px',
                      '& img': {
                        filter: likedConversations.includes(selectedSessionId)
                          ? 'brightness(0) saturate(100%) invert(45%) sepia(76%) saturate(521%) hue-rotate(179deg) brightness(93%) contrast(108%)'
                          : 'none',
                      },
                    }}
                    startIcon={
                      savingStates[selectedConversation?.id] ? (
                        <CircularProgress size={20} />
                      ) : (
                        <SafeIcon
                          src={likedConversations.includes(selectedConversation?.id) ? SaveIconOutlineselect : SaveIconOutlinelight}
                          width={'24px'}
                          height={'24px'}
                          alt='save'
                        />
                      )
                    }
                    onClick={(e) => {
                      e.stopPropagation();
                      handleLike(selectedConversation?.id, likedConversations.includes(selectedConversation?.id));
                    }}
                    disabled={savingStates[selectedConversation?.id]}
                  />
                  <CustomButton
                    startIcon={<SafeIcon src={ShareIconBlue} height={18} width={18} alt={'Share'} />}
                    variant='tertiary'
                    showTooltip
                    sx={{ height: '28px' }}
                    onClick={handleShare}
                  />
                </Box>
              </Box>
            </Box>
          )}

          <Box
            sx={{
              maxHeight: 'calc(100vh - 0px - 0px - 0px - 91px)',
              overflowY: 'auto',
              overflowX: 'hidden',
              position: 'relative',
              borderRadius: '8px',
              px: '24px',
              height: '100%',
              '&::-webkit-scrollbar': {
                display: isLoadingInvestigation ? 'none' : 'block',
                width: '6px',
              },

              '&::-webkit-scrollbar-track': {
                background: 'transparent',
                marginRight: '4px', // Additional spacing
              },

              '&::-webkit-scrollbar-thumb': {
                background: colors.background.secondaryDark,
                borderRadius: '3px',
              },

              '&::-webkit-scrollbar-thumb:hover': {
                background: colors.text.tertiary,
              },
            }}
          >
            <Box
              sx={{
                display: 'flex',
                position: 'relative',
                flexDirection: 'column',
                height: selectedSessionId == '' ? '100%' : 'auto',
                justifyContent: selectedSessionId == '' && 'center',
              }}
            >
              <Box
                display={'flex'}
                flexDirection={'column'}
                gap='10px'
                position={'relative'}
                sx={{
                  mt: selectedSessionId == '' ? '80px' : '0px',
                  '@media (max-width: 1280px)': {
                    mt: selectedSessionId == '' ? '60px' : '0px',
                  },
                }}
              >
                {selectedSessionId == '' && (
                  <Box
                    sx={{
                      display: 'flex',
                      flexDirection: 'column',
                      textAlign: 'center',
                      marginX: 'auto',
                    }}
                  >
                    <Typography
                      className={`poppins-font animated-box`}
                      sx={{
                        fontSize: '42px',
                        color: colors.text.askNudgebeeWelcomeText,
                        fontWeight: 500,
                        letterSpacing: '-0.5px',
                        lineHeight: '42px',
                        '@media (max-width: 1300px)': {
                          fontSize: '32px',
                        },
                      }}
                      style={{ animationDelay: '0.1s' }}
                    >
                      Hi{' '}
                      <span
                        style={{
                          background: `radial-gradient(circle 120px at 50% 50%, ${colors.text.askNudgebeeUserName} 0%, ${colors.text.askNudgebeeWelcomeText} 80%)`,
                          WebkitBackgroundClip: 'text',
                          WebkitTextFillColor: 'transparent',
                        }}
                      >
                        {getUserSession()?.user?.name?.split(' ')[0]},
                      </span>
                    </Typography>
                    <Typography
                      sx={{
                        fontSize: '32px',
                        color: colors.text.askNudgebeeWelcomeText,
                        fontWeight: 500,
                        letterSpacing: '-0.5px',
                        '@media (max-width: 1300px)': {
                          fontSize: '22px',
                        },
                      }}
                      className={`poppins-font animated-box`}
                      style={{ animationDelay: '0.2s' }}
                    >
                      how can I assist you today?
                    </Typography>
                  </Box>
                )}
                {selectedSessionId == '' ? (
                  <Box
                    sx={{
                      maxWidth: '725px',
                      mx: 'auto',
                      width: '100%',
                      mb: '32px',
                      '@media (max-width: 1300px)': {
                        maxWidth: '490px',
                      },
                    }}
                  >
                    <Box
                      className={`animated-box`}
                      style={{ animationDelay: '0.3s' }}
                      sx={{
                        p: '6px',
                        borderRadius: '12px',
                        background: 'linear-gradient(to right,rgb(96, 165, 250, 0.2), rgb(96, 165, 250, 0.1), rgb(96, 165, 250, 0.2))',
                        mt: '20px',
                        mb: '12px',
                        mx: 'auto',
                      }}
                    >
                      <SummaryBlock
                        hideTitle
                        sx={{
                          display: 'flex',
                          alignItems: 'flex-end',
                          gap: '30px',
                          backgroundColor: colors.background.white,
                          borderRadius: '12px',
                          border: `0.75px solid ${colors.border.conversationCard} !important`,
                          boxShadow: '0px 2px 7px 0px #3B82F60F,0px 4px 6px -1px #3B82F61F',
                          padding: '16px 20px',
                          '& textarea': {
                            width: '100%',
                            border: '0px',
                            resize: 'none',
                            boxShadow: 'none',
                            backgroundColor: colors.background.white,
                            minHeight: '80px',
                            '&:focus': {
                              boxShadow: 'none',
                            },
                            '&::placeholder': {
                              color: colors.background.lastSync,
                              '@media (max-width: 1300px)': {
                                fontSize: '13px',
                              },
                            },
                            '&::-webkit-scrollbar': {
                              display: 'none',
                            },
                          },
                          '& .MuiOutlinedInput-notchedOutline': {
                            border: '0px !important',
                          },
                          '& button': {
                            padding: '0px 10px !important',
                          },
                        }}
                      >
                        <AutoSuggestTextarea
                          ref={textareaRef}
                          value={generateQuestionText}
                          fontSize='14px'
                          fontWeight='400'
                          placeholder='Ask me about troubleshooting, error logs, resource usage, or optimizations.'
                          maxRows={4}
                          minRows={3}
                          maxLength={500000}
                          suggestionsAt={enabledAgents}
                          showBorderleft={false}
                          disabled={isLoadingInvestigation}
                          buttonProperties={{
                            show: true,
                            enable: !isLoadingInvestigation,
                            onClick: (text) => {
                              handleGenerateInvestigation(text);
                            },
                          }}
                        />
                      </SummaryBlock>
                    </Box>
                    <Typography
                      className={`animated-box`}
                      style={{ animationDelay: '0.4s' }}
                      sx={{
                        fontSize: '12px',
                        lineHeight: '20px',
                        color: colors.text.primary,
                        cursor: 'pointer',
                        fontWeight: 500,
                        pr: '20px',
                        ml: 'auto',
                        maxWidth: '60px',
                      }}
                      onClick={() => setOpenSettingsModal(true)}
                    >
                      My Agents
                    </Typography>
                  </Box>
                ) : null}

                {selectedSessionId == '' && (
                  <Box
                    className={`animated-box`}
                    style={{ animationDelay: '0.4s' }}
                    sx={{
                      mx: 'auto',
                      width: '100%',
                      maxWidth: '725px',
                      mb: '32px',
                      '@media (max-width: 1300px)': {
                        maxWidth: '470px',
                      },
                    }}
                  >
                    <TextSelectionHoverBox
                      charLimit={90}
                      showSlider={true}
                      handleTextSelection={(selectedText) => {
                        setGenerateQuestionText(selectedText);
                      }}
                      textList={[
                        { text: 'Scan and check for critical CVEs', icon: <SafeIcon src={SecurityIcon} alt='security icon' /> },
                        {
                          text: 'Show me the list of unused services and PVs for the last 1 month',
                          icon: <SafeIcon src={PvPvcIcon} alt='security icon' />,
                        },
                        {
                          text: 'Share list of applications that can be right-sized?',
                          icon: <SafeIcon src={ApplicationsIcon} alt='security icon' />,
                        },
                        { text: 'Generate logs for application XXXX in namespace YYYY', icon: <IoIosLogIn /> },
                        { text: 'Show the count of OOMKilled events in the cluster over the past week', icon: <IoIosStats /> },
                      ]}
                    />
                  </Box>
                )}
                {selectedSessionId != '' && messages && messages.length > 0 ? (
                  <>
                    <LLMConversationWithTabs
                      messages={messages}
                      isLoading={isLoadingInvestigation}
                      accountId={accountId}
                      handleShare={handleShare}
                      sessionId={selectedSessionId}
                      conversationId={conversationIdAtDb}
                      generateQuestionText={generateQuestionText}
                      showFullTextHandler={showFullTextHandler}
                      showFullText={showFullText}
                      handleCardClick={handleCardClick}
                      collapsedObj={collapsedObj}
                      getCardTitle={getCardTitle}
                    />
                    {isLoadingInvestigation && (
                      <Box mb='90px'>
                        <ShimmerLoading />
                      </Box>
                    )}
                  </>
                ) : (
                  <></>
                )}
              </Box>
            </Box>
          </Box>
        </Box>
        <div ref={bottomRef} />
      </Box>
    </>
  );
};

KubernetesLLMResponseGenerator.propTypes = {
  accountId: PropTypes.string,
  sessionId: PropTypes.string,
  query: PropTypes.string,
  popup: PropTypes.bool,
};

export default KubernetesLLMResponseGenerator;
