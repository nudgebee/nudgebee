import apiAskNudgebee, { createConversationFetcher } from '@api1/ask-nudgebee';
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
import { Modal } from '@components1/ds/Modal';
import { Button } from '@components1/ds/Button';
import { toast as snackbar } from '@components1/ds/Toast';
import { applyFiltersOnRouter } from '@lib/router';
import { Avatar, Box, CircularProgress, Typography } from '@mui/material';
import { ToggleGroup } from '@components1/ds/ToggleGroup';
import { Divider } from '@components1/ds/Divider';
import SafeIcon from '@components1/common/SafeIcon';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import { useEffect, useRef, useState } from 'react';
import { ds } from '@utils/colors';
import Text from '@common-new/format/Text';
import { getUserSession } from '@lib/auth';
import { IoIosLogIn, IoIosStats } from 'react-icons/io';
import TextSelectionHoverBox from './common/TextSelectionHoverBox';
import LLMConversationWithTabs from './LLMConversationWithTabs';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import { Skeleton } from '@components1/ds/Skeleton';
import { v4 as uuidv4 } from 'uuid';
import AutoSuggestTextarea from '@components1/k8s/common/TextArea';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import ListAgents from './ListAgents';
import ListTools from './ListTools';
import Tooltip from '@components1/ds/Tooltip';

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
  // Stateful fetcher tracks cursor + merged Maps across poll/initial calls so
  // every fetchBotResponse invocation pulls only deltas. Auto-resets when the
  // (accountId, sessionId) pair changes.
  const conversationFetcherRef = useRef(null);
  if (!conversationFetcherRef.current) {
    conversationFetcherRef.current = createConversationFetcher();
  }

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
          const response = res?.data?.data?.ai_create_saved_conversation?.data?.success ?? false;
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
      // Single delta-fetch path for both initial load (source !== 'poll') and
      // 5s polls. The fetcher's cursor cuts polling cost to "rows changed
      // since last call" while keeping full TOAST data in the response.
      const res = await conversationFetcherRef.current.fetch({
        accountId,
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
        const agentIdMap = {};
        const orphanFollowupsByGenId = {};
        if (conversationMessages.length > 0) {
          conversationMessages.forEach((cm, _i) => {
            cm.llm_conversation_agents?.forEach((agent) => {
              agentIdMap[agent.id] = agent;
            });
            if (cm.message_type == 'followup') {
              if (!followupMessages[cm.parent_agent_id]) {
                followupMessages[cm.parent_agent_id] = [cm];
              } else {
                followupMessages[cm.parent_agent_id].push(cm);
              }
            }
          });

          // Orphan followups — followups whose parent_agent_id has no row in
          // llm_conversation_agent. Newer memory-driven LLM flows emit these.
          // Assign each to the most recent preceding generation message by
          // chronological order.
          const sortedByCreated = [...conversationMessages].sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime());
          let currentGenId = null;
          sortedByCreated.forEach((cm) => {
            if (cm.message_type !== 'followup') {
              currentGenId = cm.id;
              return;
            }
            if (cm.parent_agent_id && !agentIdMap[cm.parent_agent_id] && currentGenId) {
              if (!orphanFollowupsByGenId[currentGenId]) orphanFollowupsByGenId[currentGenId] = [];
              orphanFollowupsByGenId[currentGenId].push(cm);
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
              // Render orphan followups (parent_agent_id has no resolved agent row).
              const orphanFollowups = orphanFollowupsByGenId[conversationMessage.id] ?? [];
              orphanFollowups.forEach((fMsg, i) => {
                const key = fMsg.id + '-followup-question-orphan-' + i;
                messageSequence.push(key);
                toolRequestResponse[key] = {
                  text: fMsg.message,
                  type: 'followup-question',
                  tool: 'followup-question',
                  ack_message: fMsg.ack_message,
                  response: {
                    type: 'followup-response',
                    text: fMsg.response,
                    status: fMsg.status,
                    message_config: fMsg.message_config,
                    message_id: conversationMessage.id,
                    account_id: accountId,
                    conversation_id: response.id,
                    agent_id: fMsg.parent_agent_id,
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
          const response = res?.data?.data?.ai_execute_investigation ?? {};
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
        <Box display='flex' flexDirection={'column'} alignItems={'center'} my={2}>
          <ToggleGroup
            selection='single'
            value={typeSelected}
            onChange={(newValue) => setTypeSelected(newValue)}
            size='md'
            ariaLabel='View type'
            options={[
              { value: 'agents', label: 'Agents', icon: <SafeIcon src={AgentIcon} alt='agent' /> },
              { value: 'tools', label: 'Tools', icon: <SafeIcon src={ToolsIcon} alt='tools' /> },
            ]}
          />
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
            px: 0,
          }}
        >
          {isChatScreen && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                borderBottom: `0.8px solid var(--ds-gray-200)`,
                height: ds.space[7],
                position: 'absolute',
                zIndex: 2,
                top: 0,
                right: 0,
                left: 0,
                px: ds.space.mul(1, 10),
                backgroundColor: 'var(--ds-background-100)',
              }}
            >
              <Box>
                <Text
                  requiredToolTip={false}
                  value={ConversationHeaderData.title}
                  sx={{
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    color: 'var(--ds-gray-700)',
                    fontFamily: ds.font.sans,
                    pr: ds.space.mul(0, 15),
                  }}
                  showAutoEllipsis
                />
              </Box>
              <Box sx={{ display: 'inline-flex', alignItems: 'center' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(1, 5) }}>
                  {ConversationHeaderData.queriesAsked > 0 && (
                    <Tooltip
                      placement='bottom'
                      title='Queries Asked'
                      tooltipStyle={{
                        backgroundColor: 'white',
                        color: 'var(--ds-gray-700)',
                        py: ds.space[2],
                        px: ds.space[3],
                        fontSize: 'var(--ds-text-small)',
                        borderRadius: ds.radius.sm,
                        boxShadow: '0 2px 4px rgba(0,0,0,0.1)',
                      }}
                    >
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3) }}>
                        <SafeIcon src={ChatRoundedIcon} alt='chat icon' width={14} height={14} />
                        <Typography
                          sx={{
                            fontSize: 'var(--ds-text-small)',
                            fontWeight: 'var(--ds-font-weight-regular)',
                            color: 'var(--ds-gray-400)',
                            fontFamily: ds.font.sans,
                            span: {
                              color: 'var(--ds-gray-700)',
                              fontWeight: 'var(--ds-font-weight-medium)',
                              mr: ds.space.mul(0, 3),
                            },
                          }}
                        >
                          <span>{ConversationHeaderData.queriesAsked}</span>
                        </Typography>
                      </Box>
                    </Tooltip>
                  )}
                  {ConversationHeaderData.participants > 0 && (
                    <Tooltip
                      tooltipStyle={{
                        backgroundColor: 'var(--ds-background-100)',
                        color: 'var(--ds-gray-700)',
                        boxShadow: '0 2px 10px rgba(0,0,0,0.1)',
                        padding: 0,
                        border: '1px solid rgba(0,0,0,0.08)',
                        borderRadius: ds.radius.lg,
                      }}
                      title={
                        <Box
                          sx={{
                            display: 'flex',
                            flexDirection: 'column',
                            gap: ds.space[3],
                            padding: ds.space[4],
                          }}
                        >
                          <Typography
                            sx={{
                              fontSize: 'var(--ds-text-body-lg)',
                              fontWeight: 'var(--ds-font-weight-semibold)',
                              color: 'var(--ds-gray-700)',
                              borderBottom: `1px solid ${'var(--ds-background-200)'}`,
                              paddingBottom: ds.space[2],
                            }}
                          >
                            Participants
                          </Typography>
                          <Box sx={{ display: 'flex', flexDirection: 'column', flexWrap: 'wrap', gap: ds.space.mul(0, 5) }}>
                            {Array.from(uniqueParticipantNames).map((name, index) => (
                              <Box
                                key={index}
                                sx={{
                                  display: 'flex',
                                  alignItems: 'center',
                                  gap: ds.space[2],
                                  backgroundColor: 'var(--ds-background-200)',
                                  borderRadius: ds.space.mul(0, 8),
                                  pt: ds.space[1],
                                  pr: ds.space[3],
                                  pb: ds.space[1],
                                  pl: ds.space[1],
                                  transition: 'all 0.2s ease',
                                  '&:hover': {
                                    backgroundColor: 'var(--ds-background-200)',
                                    transform: 'translateY(-1px)',
                                  },
                                }}
                              >
                                <Avatar
                                  sx={{
                                    bgcolor: 'var(--ds-gray-600)',
                                    height: ds.space.mul(1, 7),
                                    width: ds.space.mul(1, 7),
                                    fontSize: 'var(--ds-text-body-lg)',
                                    fontWeight: 'var(--ds-font-weight-regular)',
                                    fontFamily: ds.font.sans,
                                    cursor: 'pointer',
                                  }}
                                >
                                  {name?.trim().split(' ')[0].charAt(0).toUpperCase() +
                                    name?.trim().split(' ')[name?.trim().split(' ').length - 1].charAt(0).toUpperCase()}
                                </Avatar>
                                <Typography
                                  sx={{
                                    fontSize: 'var(--ds-text-body)',
                                    fontWeight: 'var(--ds-font-weight-medium)',
                                    color: 'var(--ds-gray-700)',
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
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3) }}>
                        <SafeIcon src={UsersIcon} alt='users icon' width={14} height={14} />
                        <Typography
                          sx={{
                            fontSize: 'var(--ds-text-small)',
                            fontWeight: 'var(--ds-font-weight-regular)',
                            color: 'var(--ds-gray-400)',
                            fontFamily: ds.font.sans,
                            span: {
                              color: 'var(--ds-gray-700)',
                              fontWeight: 'var(--ds-font-weight-medium)',
                              mr: ds.space.mul(0, 3),
                            },
                          }}
                        >
                          <span>{ConversationHeaderData.participants}</span>
                        </Typography>
                      </Box>
                    </Tooltip>
                  )}
                </Box>
                <Divider orientation='vertical' sx={{ mx: ds.space.mul(0, 5) }} />
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                  <Button
                    tone='secondary'
                    size='sm'
                    composition='icon-only'
                    aria-label='Save conversation'
                    icon={
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
                  <Button
                    tone='secondary'
                    size='sm'
                    composition='icon-only'
                    aria-label='Share'
                    icon={<SafeIcon src={ShareIconBlue} height={18} width={18} alt={'Share'} />}
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
              borderRadius: ds.radius.lg,
              px: ds.space[5],
              height: '100%',
              '&::-webkit-scrollbar': {
                display: isLoadingInvestigation ? 'none' : 'block',
                width: ds.space.mul(0, 3),
              },

              '&::-webkit-scrollbar-track': {
                background: 'transparent',
                marginRight: ds.space[1], // Additional spacing
              },

              '&::-webkit-scrollbar-thumb': {
                background: 'var(--ds-gray-400)',
                borderRadius: ds.radius.sm,
              },

              '&::-webkit-scrollbar-thumb:hover': {
                background: 'var(--ds-gray-500)',
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
                gap={ds.space.mul(0, 5)}
                position={'relative'}
                sx={{
                  mt: selectedSessionId == '' ? ds.space.mul(1, 20) : 0,
                  '@media (max-width: 1280px)': {
                    mt: selectedSessionId == '' ? ds.space.mul(1, 15) : 0,
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
                        fontSize: 'var(--ds-text-display)',
                        color: 'var(--ds-brand-600)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        letterSpacing: '-0.5px',
                        lineHeight: '42px',
                        '@media (max-width: 1300px)': {
                          fontSize: 'var(--ds-text-display)',
                        },
                      }}
                      style={{ animationDelay: '0.1s' }}
                    >
                      Hi{' '}
                      <span
                        style={{
                          background: `radial-gradient(circle 120px at 50% 50%, ${'var(--ds-blue-600)'} 0%, ${'var(--ds-brand-600)'} 80%)`,
                          WebkitBackgroundClip: 'text',
                          WebkitTextFillColor: 'transparent',
                        }}
                      >
                        {getUserSession()?.user?.name?.split(' ')[0]},
                      </span>
                    </Typography>
                    <Typography
                      sx={{
                        fontSize: 'var(--ds-text-display)',
                        color: 'var(--ds-brand-600)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        letterSpacing: '-0.5px',
                        '@media (max-width: 1300px)': {
                          fontSize: 'var(--ds-text-heading)',
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
                      maxWidth: ds.space.mul(0, 362),
                      mx: 'auto',
                      width: '100%',
                      mb: ds.space[6],
                      '@media (max-width: 1300px)': {
                        maxWidth: ds.space.mul(0, 245),
                      },
                    }}
                  >
                    <Box
                      className={`animated-box`}
                      style={{ animationDelay: '0.3s' }}
                      sx={{
                        p: ds.space.mul(0, 3),
                        borderRadius: ds.radius.xl,
                        background: 'linear-gradient(to right,rgb(96, 165, 250, 0.2), rgb(96, 165, 250, 0.1), rgb(96, 165, 250, 0.2))',
                        mt: ds.space.mul(1, 5),
                        mb: ds.space[3],
                        mx: 'auto',
                      }}
                    >
                      <SummaryBlock
                        hideTitle
                        sx={{
                          display: 'flex',
                          alignItems: 'flex-end',
                          gap: ds.space.mul(0, 15),
                          backgroundColor: 'var(--ds-background-100)',
                          borderRadius: ds.radius.xl,
                          border: `0.75px solid ${'var(--ds-gray-600)'} !important`,
                          boxShadow: '0px 2px 7px 0px #3B82F60F,0px 4px 6px -1px #3B82F61F',
                          padding: `${ds.space[4]} ${ds.space.mul(1, 5)}`,
                          '& textarea': {
                            width: '100%',
                            border: '0px',
                            resize: 'none',
                            boxShadow: 'none',
                            backgroundColor: 'var(--ds-background-100)',
                            minHeight: ds.space.mul(1, 20),
                            '&:focus': {
                              boxShadow: 'none',
                            },
                            '&::placeholder': {
                              color: 'var(--ds-gray-400)',
                              '@media (max-width: 1300px)': {
                                fontSize: 'var(--ds-text-body)',
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
                            padding: `0 ${ds.space.mul(0, 5)} !important`,
                          },
                        }}
                      >
                        <AutoSuggestTextarea
                          ref={textareaRef}
                          value={generateQuestionText}
                          fontSize='var(--ds-text-body-lg)'
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
                        fontSize: 'var(--ds-text-small)',
                        lineHeight: '20px',
                        color: 'var(--ds-blue-500)',
                        cursor: 'pointer',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        pr: ds.space.mul(1, 5),
                        ml: 'auto',
                        maxWidth: ds.space.mul(1, 15),
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
                      maxWidth: ds.space.mul(0, 362),
                      mb: ds.space[6],
                      '@media (max-width: 1300px)': {
                        maxWidth: ds.space.mul(0, 235),
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
                      <Box mb={ds.space.mul(0, 45)}>
                        <Skeleton.Card width='100%' />
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
