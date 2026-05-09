import apiKubernetes from '@api1/kubernetes';
import apiAskNudgebee from '@api1/ask-nudgebee';
import MarkDowns from '@components1/common/MarkDowns';
import { Typography, Box, CircularProgress, Chip, TextField, Button } from '@mui/material';
import Link from 'next/link';
import DOMPurify from 'dompurify';
import KubernetesRightSizingUpdateForm from '@components1/recommendations/KubernetesRightSizingUpdateForm';
import { useEffect, useState, useCallback, useRef } from 'react';
import ConversationLoader from '@common/ConversationLoader';
import { colors } from 'src/utils/colors';
import { getNubiIconUrl } from '@hooks/useTenantBranding';
import SimpleDiffViewer from '@components1/common/SimpleDiffViewer';
import { FiArrowRight } from 'react-icons/fi';
import { useConversationSuggestions } from '@hooks/useConversationSuggestions';
import { safeJSONParse } from '@utils/common';

const ZERO_UUID = '00000000-0000-0000-0000-000000000000';

class AskAiCard {
  constructor() {
    this.id = 'AskAiCard';
    this.icon = getNubiIconUrl();
    this.text = 'Investigation Analysis';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.line = '';
    this.event = {};
    this.onDataUpdate = null;
    this.errorMessage = '';
    this.refreshRenderId = 0;
  }

  // Method to set data update callback
  setDataUpdateCallback(callback) {
    this.onDataUpdate = callback;
    this.refreshRenderId += 1;
  }

  showCardInsights = async () => {
    let message = '';
    let component = null;

    if (this.aiData?.status?.toLowerCase() === 'completed') {
      this.insightData = this.insightData.filter((insight) => insight.message !== 'Ai Analysis in progress');
    }
    if (this.aiData?.file_details?.files?.[0]?.file_path) {
      let path = this.aiData?.file_details?.files?.[0]?.file_path;

      if (path) {
        if (path.startsWith('/')) {
          path = path.slice(1);
        }
        let paths = path.split('/');
        if (paths.length > 2) {
          path = paths.slice(-2).join('/');
        } else if (paths.length > 1) {
          path = paths.slice(1).join('/');
        }
      }

      let text = path;
      if (this.aiData?.file_details?.files?.[0]?.line_number) {
        text = text + ':' + this.aiData?.file_details?.files?.[0]?.line_number;
      }

      message = this?.aiData?.source_updates?.gitDiff ? `Found issue on file - ${text} ` : `File - ${text} `;

      if (this.aiData?.source_details?.['workloads.nudgebee.com/git.repo']) {
        let githubRepoUrl = this.aiData?.source_details?.['workloads.nudgebee.com/git.repo'];
        let githubRepo = githubRepoUrl.replace('https://github.com/', '').split('/');
        if (this?.aiData?.source_updates?.gitDiff) {
          this.resolveButton = true;
        }
        component = (
          <>
            {text && (
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '8px',
                  paddingY: '10px',
                }}
              >
                <Typography fontSize={'14px'}>{message}</Typography>
                <Link
                  target='_blank'
                  href={`https://github.com/search?q=repo%3A${githubRepo[0]}%2F${githubRepo[1]}+${path}&type=code`}
                  passHref
                  style={{
                    textDecoration: 'underline',
                    fontWeight: 500,
                    lineHeight: 1.5,
                  }}
                >
                  {text}
                </Link>
              </div>
            )}
            {this?.aiData?.source_updates?.gitDiff && (
              <Typography color={'red'} fontSize={'14px'}>
                {'Code Suggestion Available.'}
              </Typography>
            )}
          </>
        );
      }
    } else if (this.aiData?.status?.toLowerCase() === 'in_progress') {
      message = 'Ai Analysis in progress';
    } else {
      // if status in_progress, optionally add your logic here
      return;
    }

    // Check for duplicates
    if (!this.insightData.some((insight) => insight.message === message)) {
      this.insightData.push({
        message,
        component,
        severity: 'Info',
      });
    }
  };

  canRenderContent = async (_, event) => {
    this.event = event;
    try {
      this.aiData = await apiKubernetes.generateAiRecommendation(event.cloud_account_id, event.id, 'pod_log_analysis');
      // generateAiRecommendation returns either the recommendation object
      // or the GraphQL/Hasura error string from parseHttpResponseBodyMessage.
      // Show an error for the failure object, the string fallback, and the
      // empty/null cases — but stay silent while status is in_progress.
      const isStringError = typeof this.aiData === 'string';
      const isFailedStatus = this.aiData?.status?.toLowerCase() === 'failed';
      const isMissing = !this.aiData;
      if (!this.aiData?.analysis && !this.aiData?.summary && (isStringError || isFailedStatus || isMissing)) {
        const detail = isStringError ? this.aiData : this.aiData?.status_reason || 'Unknown error';
        this.errorMessage = `Failed to generate investigation- ${detail}`;
      }
      if (this.aiData?.status?.toLowerCase() === 'completed') {
        this.showCardInsights();
        if (this.onDataUpdate && typeof this.onDataUpdate === 'function') {
          this.onDataUpdate(this);
        }
      }
      this.showCardInsights();
      this.renderContent = true;
    } catch (e) {
      console.error('Error:', e);
      this.renderContent = false;
    }
    return this.renderContent;
  };

  refreshInvestigation = async () => {
    this.aiData = { ...this.aiData, status: 'in_progress' };
    this.showCardInsights();
    if (this.onDataUpdate && typeof this.onDataUpdate === 'function') {
      this.onDataUpdate(this);
    }
    try {
      this.aiData = await apiKubernetes.generateAiRecommendation(this.event.cloud_account_id, this.event.id, 'pod_log_analysis', true);
      this.showCardInsights();
      if (this.onDataUpdate && typeof this.onDataUpdate === 'function') {
        this.onDataUpdate(this);
      }
    } catch (e) {
      console.error('Error refreshing AI recommendation:', e);
    }
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderAskAI()];
  };

  renderAskAI = () => {
    const cardInstance = this;
    const { event } = cardInstance;

    const AskAICardComponent = ({ noPadding = false }) => {
      const [localAiData, setLocalAiData] = useState(cardInstance.aiData);
      const [hasFetchedInitial, setHasFetchedInitial] = useState(!!cardInstance.aiData);
      const [, setWaitingConversation] = useState(null);
      const [followUpMessages, setFollowUpMessages] = useState([]);
      const [followUpLoading, setFollowUpLoading] = useState(false);
      const followUpPollRef = useRef(null);
      const { suggestions: followUpQuestions, fetchSuggestions, clearSuggestions } = useConversationSuggestions(event?.cloud_account_id);

      // State for inline follow-up questions (with options or text input)
      const [waitingFollowUpItems, setWaitingFollowUpItems] = useState([]);
      const [followUpSubmitting, setFollowUpSubmitting] = useState(false);
      const [followUpInputs, setFollowUpInputs] = useState({});
      const lastFetchedConvIdRef = useRef(null);
      const isMountedRef = useRef(true);

      useEffect(() => {
        return () => {
          isMountedRef.current = false;
        };
      }, []);

      const checkWaitingConversation = () => {
        if (!event?.fingerprint || !event?.cloud_account_id) return;
        apiAskNudgebee
          .llmConversationHistoryForInvestigation({
            account_id: event.cloud_account_id,
            session_id: `event-${event.fingerprint}`,
            status: 'WAITING',
            source: 'Investigation',
            limit: 1,
            offset: 0,
          })
          .then((res) => {
            const conversations = res?.data?.data?.llm_conversations || [];
            const conv = conversations[0] || null;
            setWaitingConversation(conv);
            if (conv) {
              // Only fetch detail if conversation ID changed (avoid repeated getLlmConversation calls)
              if (lastFetchedConvIdRef.current !== conv.id) {
                lastFetchedConvIdRef.current = conv.id;
                fetchWaitingFollowUps(conv);
              }
            } else {
              lastFetchedConvIdRef.current = null;
              setWaitingFollowUpItems([]);
            }
          })
          .catch((error) => {
            console.error('Failed to fetch waiting conversations for investigation:', error);
          });
      };

      const fetchWaitingFollowUps = (conv) => {
        apiAskNudgebee
          .getLlmConversation({
            conversationId: conv.id,
            accountId: event.cloud_account_id,
          })
          .then((detailRes) => {
            const convDetail = detailRes?.data?.data?.llm_conversations?.[0];
            const messages = convDetail?.llm_conversation_messages || [];
            let waitingMsg;
            for (let i = messages.length - 1; i >= 0; i--) {
              if (messages[i].status === 'WAITING') {
                waitingMsg = messages[i];
                break;
              }
            }
            if (!waitingMsg) {
              setWaitingFollowUpItems([]);
              return;
            }

            const items = [];

            // 1. Check for followup-type messages (same pattern as useLLMInvestigationControl)
            //    These are separate messages with message_type === 'followup' that haven't been answered yet
            const followupMsgs = messages.filter((m) => m.message_type === 'followup' && !m.response);
            const followupAgentIds = new Set();
            for (const fMsg of followupMsgs) {
              let msgConfig = {};
              try {
                if (fMsg.message_config) {
                  msgConfig = typeof fMsg.message_config === 'string' ? JSON.parse(fMsg.message_config) : fMsg.message_config;
                }
              } catch {
                // ignore
              }
              followupAgentIds.add(fMsg.parent_agent_id);
              items.push({
                id: fMsg.id,
                agentName: msgConfig.toolName || 'followup-question',
                question: msgConfig.question || fMsg.message,
                followupType: msgConfig.followupType || 'text',
                followupOptions: msgConfig.followupOptions || [],
                status: fMsg.status,
                // Fields needed for aiFollowupResponse (same as KubernetesLLMRequestResponseV2)
                account_id: event.cloud_account_id,
                conversation_id: conv.id,
                message_id: waitingMsg.id,
                agent_id: fMsg.parent_agent_id,
                parent_agent_id: fMsg.parent_agent_id,
              });
            }

            // 2. Check for waiting agents that DON'T already have a followup message
            //    Also skip parent agents whose child agent already has a followup
            const agents = waitingMsg.llm_conversation_agents || [];
            for (const agent of agents) {
              const hasChildWithFollowup = (agent.llm_conversation_tool_calls || []).some(
                (tc) => tc.child_agent_id && followupAgentIds.has(tc.child_agent_id)
              );
              if (agent.status === 'waiting' && agent.response && !followupAgentIds.has(agent.id) && !hasChildWithFollowup) {
                items.push({
                  id: agent.id,
                  agentName: agent.agent_name,
                  question: agent.response,
                  followupType: 'text',
                  followupOptions: [],
                  status: 'WAITING',
                  account_id: event.cloud_account_id,
                  conversation_id: conv.id,
                  message_id: waitingMsg.id,
                  agent_id: agent.id,
                  parent_agent_id: agent.parent_agent_id,
                });
              }
            }

            setWaitingFollowUpItems(items);
          })
          .catch((err) => {
            console.error('Failed to fetch conversation detail for follow-ups:', err);
            setWaitingFollowUpItems([]);
          });
      };

      const handleFollowUpSubmit = useCallback(
        async (responseText, item) => {
          if (followUpSubmitting || !responseText?.trim()) return;
          setFollowUpSubmitting(true);
          try {
            const resolvedParentAgentId = item.parent_agent_id === ZERO_UUID || !item.parent_agent_id ? item.agent_id : item.parent_agent_id;

            await apiAskNudgebee.aiFollowupResponse({
              account_id: item.account_id,
              query: responseText,
              conversation_id: item.conversation_id,
              message_id: item.message_id,
              agent_id: item.agent_id,
              parent_agent_id: resolvedParentAgentId,
            });

            // Remove answered item from list
            setWaitingFollowUpItems((prev) => prev.filter((i) => i.id !== item.id));
            setFollowUpInputs((prev) => {
              const updated = { ...prev };
              delete updated[item.id];
              return updated;
            });
            // Reset so next poll re-fetches conversation detail
            lastFetchedConvIdRef.current = null;
            // Re-check after a delay (guard against unmount)
            setTimeout(() => {
              if (isMountedRef.current) checkWaitingConversation();
            }, 3000);
          } catch (err) {
            console.error('Error submitting follow-up response:', err);
          } finally {
            setFollowUpSubmitting(false);
          }
        },
        [followUpSubmitting, event]
      );

      // Check on mount
      useEffect(() => {
        checkWaitingConversation();
      }, []);

      useEffect(() => {
        if (cardInstance.aiData?.status?.toLowerCase() === 'completed') {
          return;
        }

        let attempts = 0;
        const interval = setInterval(async () => {
          try {
            const res = await apiKubernetes.generateAiRecommendation(event.cloud_account_id, event.id, 'pod_log_analysis');
            cardInstance.aiData = res;
            setLocalAiData(res);
            if (!hasFetchedInitial) {
              setHasFetchedInitial(true);
            }
            const terminalStatuses = ['completed', 'failed', 'killed'];
            if (terminalStatuses.includes(res?.status?.toLowerCase())) {
              cardInstance.showCardInsights();
              if (cardInstance.onDataUpdate && typeof cardInstance.onDataUpdate === 'function') {
                cardInstance.onDataUpdate(cardInstance);
              }
              clearInterval(interval);
            } else {
              cardInstance.showCardInsights();
            }
            // Re-check waiting status on each poll cycle (skip terminal statuses)
            if (!['FAILED', 'KILLED', 'COMPLETED'].includes(res?.status?.toUpperCase())) {
              checkWaitingConversation();
            }
          } catch (e) {
            console.error('Error fetching AI recommendation:', e);
          }

          attempts++;
          if (attempts >= 50) {
            clearInterval(interval);
          }
        }, 5000);

        return () => clearInterval(interval);
      }, [hasFetchedInitial]);

      // Fetch follow-up questions when analysis completes
      useEffect(() => {
        if (localAiData?.status?.toLowerCase() !== 'completed') return;
        if (!localAiData?.conversation_id || !localAiData?.message_id) return;
        if (followUpQuestions.length > 0) return;
        fetchSuggestions(localAiData.conversation_id, localAiData.message_id);
      }, [localAiData?.status, localAiData?.conversation_id, localAiData?.message_id]);

      // Cleanup polling on unmount
      useEffect(() => {
        return () => {
          if (followUpPollRef.current) clearInterval(followUpPollRef.current);
        };
      }, []);

      const handleFollowUpQuestion = useCallback(
        async (questionText) => {
          if (followUpLoading || !questionText) return;

          // Clear any existing polling interval before starting a new one
          if (followUpPollRef.current) {
            clearInterval(followUpPollRef.current);
            followUpPollRef.current = null;
          }

          const sessionId = `event-${event.fingerprint}`;
          setFollowUpLoading(true);
          clearSuggestions();

          // Add the question to the follow-up messages immediately
          setFollowUpMessages((prev) => [...prev, { type: 'question', text: questionText }]);

          try {
            await apiAskNudgebee.aiGenerateInvestigate({
              account_id: event.cloud_account_id,
              query: questionText,
              session_id: sessionId,
            });

            // Poll for the response by fetching conversation messages
            let attempts = 0;
            followUpPollRef.current = setInterval(async () => {
              attempts++;
              try {
                // Use lightweight polling query (no heavy TOAST columns)
                const res = await apiAskNudgebee.getLlmConversationPolling({
                  accountId: event.cloud_account_id,
                  sessionId,
                });
                let conversation = res?.data?.data?.llm_conversations?.[0];
                let messages = conversation?.llm_conversation_messages || [];
                const isComplete = conversation?.status === 'COMPLETED' || conversation?.status === 'FAILED';

                // On terminal status, stop polling immediately and re-fetch with full detail
                if (isComplete) {
                  clearInterval(followUpPollRef.current);
                  followUpPollRef.current = null;
                  const fullRes = await apiAskNudgebee.getLlmConversation({
                    accountId: event.cloud_account_id,
                    sessionId,
                  });
                  conversation = fullRes?.data?.data?.llm_conversations?.[0] || conversation;
                  messages = conversation?.llm_conversation_messages || [];
                }

                // Find the latest answer message (type !== 'question', after our question)
                const lastMessage = messages[messages.length - 1];

                if (lastMessage && lastMessage.type !== 'question' && lastMessage.text) {
                  setFollowUpMessages((prev) => {
                    // Replace or add the answer for the latest question
                    const updated = [...prev];
                    const lastIdx = updated.length - 1;
                    if (lastIdx >= 0 && updated[lastIdx].type === 'question') {
                      return [...updated, { type: 'answer', text: lastMessage.text, messageId: lastMessage.id }];
                    }
                    // Update existing answer
                    if (lastIdx >= 0 && updated[lastIdx].type === 'answer') {
                      updated[lastIdx] = { type: 'answer', text: lastMessage.text, messageId: lastMessage.id };
                    }
                    return updated;
                  });
                }

                if (isComplete || attempts >= 60) {
                  clearInterval(followUpPollRef.current);
                  followUpPollRef.current = null;
                  setFollowUpLoading(false);
                  // Fetch new suggestions for the latest message
                  if (conversation?.id && lastMessage?.id) {
                    fetchSuggestions(conversation.id, lastMessage.id);
                  }
                }
              } catch (err) {
                console.error('Error polling follow-up response:', err);
                if (attempts >= 60) {
                  clearInterval(followUpPollRef.current);
                  followUpPollRef.current = null;
                  setFollowUpLoading(false);
                }
              }
            }, 3000);
          } catch (err) {
            console.error('Error sending follow-up question:', err);
            setFollowUpMessages((prev) => [...prev, { type: 'answer', text: 'Failed to get a response. Please try again.' }]);
            setFollowUpLoading(false);
          }
        },
        [event, followUpLoading, fetchSuggestions, clearSuggestions]
      );

      if (!localAiData) {
        return <ConversationLoader />;
      }

      let { analysis, summary, investigation, detailed_response, source_updates, task_statuses, code_analysis_enabled } = localAiData || {};

      const parsedAnalysis = typeof analysis === 'string' ? safeJSONParse(analysis) : null;
      if (parsedAnalysis) {
        // Only use the inner 'analysis' field — don't fall back to 'summary'
        // since that duplicates the Summary section content.
        analysis = parsedAnalysis.analysis || '';
      }

      // detailed_response is the enriched synthesis of summary + investigation + log analysis.
      // Fall back to initial summary while it is still being generated.
      const summaryContent = detailed_response || summary;

      let sections = [];
      const isTerminal = ['completed', 'failed', 'killed'].includes(localAiData?.status?.toLowerCase());

      const fallbackStatus = (taskStatus, content) => {
        const normalizedStatus = taskStatus?.toUpperCase();
        if (content) return 'COMPLETED';
        if (isTerminal && ['PENDING', 'IN_PROGRESS'].includes(normalizedStatus)) return 'FAILED';
        if (normalizedStatus) return normalizedStatus;
        if (isTerminal) return 'FAILED';
        return 'IN_PROGRESS';
      };

      if (task_statuses) {
        if (task_statuses.summary !== undefined || task_statuses.detailed_response !== undefined || summaryContent) {
          // Show content as soon as anything is available — no spinner once initial summary loads.
          const summaryStatus = summaryContent
            ? 'COMPLETED'
            : fallbackStatus(task_statuses.detailed_response || task_statuses.summary, summaryContent);
          sections.push({
            id: 'summary',
            label: 'Summary',
            status: summaryStatus,
            content: summaryContent,
          });
        }
        if (task_statuses.investigation !== undefined || investigation) {
          sections.push({
            id: 'investigation',
            label: 'Investigation',
            status: fallbackStatus(task_statuses.investigation, investigation),
            content: investigation,
          });
        }
        if (task_statuses.log_analysis !== undefined || analysis) {
          sections.push({
            id: 'log_analysis',
            label: 'Log Analysis',
            status: fallbackStatus(task_statuses.log_analysis, analysis),
            content: analysis,
          });
        }
      } else {
        if (summaryContent || !isTerminal) {
          sections.push({
            id: 'summary',
            label: 'Summary',
            status: summaryContent ? 'COMPLETED' : isTerminal ? 'FAILED' : 'IN_PROGRESS',
            content: summaryContent,
          });
        }
        if (investigation || !isTerminal) {
          sections.push({
            id: 'investigation',
            label: 'Investigation',
            status: investigation ? 'COMPLETED' : isTerminal ? 'FAILED' : 'IN_PROGRESS',
            content: investigation,
          });
        }
        if (analysis || !isTerminal) {
          sections.push({
            id: 'log_analysis',
            label: 'Log Analysis',
            status: analysis ? 'COMPLETED' : isTerminal ? 'FAILED' : 'IN_PROGRESS',
            content: analysis,
          });
        }
      }

      const cleanContent = (content) => {
        let finalContent = content || '';
        if (finalContent.startsWith('```markdown')) {
          finalContent = finalContent.replace(/^```markdown\s*/, '').replace(/```$/, '');
        }
        finalContent = finalContent.replace(/(?<!\n)\n(?!\n)/g, '\n\n');
        return DOMPurify.sanitize(finalContent);
      };

      const renderExtras = () => {
        let extrasContent = '';

        if (localAiData?.pr_list?.length > 0) {
          localAiData.pr_list.forEach((pr) => {
            let component = '';
            if (localAiData?.file_details?.files?.length > 0) {
              const filePath = localAiData?.file_details?.files[0]?.file_path;
              component = filePath?.split('/')?.[0] ?? '';
            }
            if (localAiData?.root_cause_analysis) {
              extrasContent += `\n\n**Root Cause Analysis**\n\n${localAiData?.root_cause_analysis}`;
            }
            extrasContent += `\n\n**The issue was introduced by the following PRs:**\n 🔀 **\`PR #${pr.number.toString()} ${pr.state.toUpperCase()}\`**${
              component ? ` \`${component}\`` : ''
            }\n${pr.title}. ([PR #${pr.number}](${pr.url}), @${pr.author})\n`;
          });
        }

        const Insights = cardInstance.insightData.map((insight) => (
          <Box key={insight.message} sx={{ marginBottom: '8px' }}>
            {insight.component && <Box sx={{ marginTop: '4px', fontSize: '12px' }}>{insight.component}</Box>}
          </Box>
        ));

        const fileName = localAiData?.file_details?.files?.[0]?.file_path?.split('/').pop() || 'code';

        return (
          <Box>
            {extrasContent && (
              <MarkDowns data={DOMPurify.sanitize(extrasContent)} sx={{ width: '100%', padding: noPadding ? '0px' : undefined, maxHeight: 'auto' }} />
            )}

            {code_analysis_enabled !== false && source_updates?.gitDiff && (
              <Box sx={{ marginTop: '16px' }}>
                <SimpleDiffViewer
                  gitDiff={source_updates.gitDiff}
                  fileName={fileName}
                  defaultExpanded={true}
                  title='Proposed Code Changes'
                  showHeader={true}
                />
              </Box>
            )}

            {code_analysis_enabled !== false && source_updates?.explanation && (
              <Box sx={{ marginTop: '16px', padding: '12px 16px', backgroundColor: '#f0f9ff', borderLeft: '4px solid #3b82f6', borderRadius: '4px' }}>
                <Typography sx={{ fontSize: '14px', fontWeight: 600, marginBottom: '8px', color: '#1e40af' }}>
                  {source_updates?.gitDiff ? 'Reasoning behind the proposed code changes' : 'No Code Changes Required'}
                </Typography>
                <MarkDowns data={source_updates.explanation} sx={{ fontSize: '13px', lineHeight: 1.6 }} />
              </Box>
            )}

            {Insights.length > 0 && (
              <Box sx={{ marginTop: '16px', padding: '8px 16px', backgroundColor: '#f9f9f9', borderRadius: '4px', fontSize: '12px' }}>{Insights}</Box>
            )}
          </Box>
        );
      };

      return (
        <div style={{ width: '100%' }}>
          {/* Inline follow-up questions with options or text input */}
          {waitingFollowUpItems.length > 0 && (
            <Box sx={{ mb: '16px', display: 'flex', flexDirection: 'column', gap: '12px' }}>
              {waitingFollowUpItems.map((item, idx) => {
                const hasOptions = item.followupOptions && item.followupOptions.length > 0;
                return (
                  <Box
                    key={item.id || idx}
                    sx={{
                      padding: '16px',
                      backgroundColor: colors.background.warningLight,
                      border: `0.5px solid ${colors.border.warning}`,
                      borderRadius: '8px',
                    }}
                  >
                    <Typography sx={{ fontSize: '14px', fontWeight: 600, color: colors.text.primary, mb: '8px' }}>
                      Agent - {item.agentName}
                    </Typography>
                    <Typography sx={{ fontSize: '13px', color: colors.text.primary, mb: '12px' }}>{item.question}</Typography>
                    {item.toolParams && (
                      <Box sx={{ mb: '12px', p: '10px 14px', backgroundColor: '#f5f5f5', borderRadius: '6px' }}>
                        <MarkDowns
                          data={typeof item.toolParams === 'object' ? JSON.stringify(item.toolParams) : item.toolParams}
                          sx={{ width: '100%', padding: '0px' }}
                        />
                      </Box>
                    )}

                    {/* Option buttons (single_select / tool_confirmation / multi_select) */}
                    {hasOptions && (
                      <>
                        <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.primary, mb: '8px' }}>Options</Typography>
                        <Box sx={{ display: 'flex', gap: '12px', flexWrap: 'wrap' }}>
                          {item.followupOptions.map((option) => (
                            <Box
                              key={option}
                              data-testid={`followup-option-${option}`}
                              onClick={() => !followUpSubmitting && handleFollowUpSubmit(option, item)}
                              sx={{
                                padding: '6px 20px',
                                border: `1px solid ${colors.border.primary}`,
                                borderRadius: '6px',
                                cursor: followUpSubmitting ? 'not-allowed' : 'pointer',
                                opacity: followUpSubmitting ? 0.6 : 1,
                                fontSize: '13px',
                                fontWeight: 500,
                                color: colors.text.primary,
                                backgroundColor: '#fff',
                                transition: 'all 0.2s ease',
                                '&:hover': {
                                  backgroundColor: followUpSubmitting ? '#fff' : colors.background.blueLabel,
                                  borderColor: followUpSubmitting ? colors.border.primary : colors.text.primary,
                                },
                              }}
                            >
                              {option}
                            </Box>
                          ))}
                        </Box>
                      </>
                    )}

                    {/* Free text input for agents waiting without predefined options */}
                    {!hasOptions && (
                      <Box sx={{ display: 'flex', gap: '8px', alignItems: 'flex-start', mt: '4px' }}>
                        <TextField
                          size='small'
                          placeholder='Type your response...'
                          value={followUpInputs[item.id] || ''}
                          onChange={(e) => setFollowUpInputs((prev) => ({ ...prev, [item.id]: e.target.value }))}
                          disabled={followUpSubmitting}
                          multiline
                          maxRows={3}
                          sx={{
                            flex: 1,
                            '& .MuiOutlinedInput-root': {
                              fontSize: '13px',
                              backgroundColor: '#fff',
                              borderRadius: '6px',
                            },
                          }}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter' && !e.shiftKey) {
                              e.preventDefault();
                              handleFollowUpSubmit(followUpInputs[item.id], item);
                            }
                          }}
                          data-testid={`followup-input-${item.agentName}`}
                        />
                        <Button
                          variant='contained'
                          size='small'
                          disabled={followUpSubmitting || !followUpInputs[item.id]?.trim()}
                          onClick={() => handleFollowUpSubmit(followUpInputs[item.id], item)}
                          data-testid={`followup-submit-${item.agentName}`}
                          sx={{
                            textTransform: 'none',
                            fontSize: '13px',
                            fontWeight: 500,
                            borderRadius: '6px',
                            minWidth: '70px',
                            height: '40px',
                          }}
                        >
                          {followUpSubmitting ? <CircularProgress size={16} color='inherit' /> : 'Submit'}
                        </Button>
                      </Box>
                    )}

                    {followUpSubmitting && (
                      <Box sx={{ mt: '8px', display: 'flex', alignItems: 'center', gap: '8px' }}>
                        <CircularProgress size={14} />
                        <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>Submitting response...</Typography>
                      </Box>
                    )}
                  </Box>
                );
              })}
            </Box>
          )}

          {sections.length > 0 && (
            <Box
              sx={{
                display: 'flex',
                gap: '8px',
                padding: '8px 0',
                marginBottom: '16px',
                borderBottom: '1px solid #e0e0e0',
                flexWrap: 'wrap',
              }}
            >
              {sections.map((sec) => {
                const isCompleted = sec.status === 'COMPLETED';
                const isInProgress = sec.status === 'IN_PROGRESS' || sec.status === 'PENDING';
                return (
                  <Chip
                    key={sec.id}
                    label={sec.label}
                    onClick={() => {
                      document.getElementById(`section-${sec.id}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' });
                    }}
                    icon={isInProgress ? <CircularProgress size={12} sx={{ ml: 1 }} /> : undefined}
                    variant={isCompleted ? 'filled' : 'outlined'}
                    color={isCompleted ? 'primary' : 'default'}
                    sx={{
                      cursor: 'pointer',
                      fontSize: '12px',
                      height: '28px',
                      fontWeight: 500,
                      bgcolor: isCompleted ? colors.background.blueLabel : 'transparent',
                      color: isCompleted ? colors.text.primary : colors.text.secondary,
                      border: isCompleted ? 'none' : '1px solid #d1d5db',
                      '&:hover': {
                        bgcolor: isCompleted ? '#dbeefe' : '#f3f4f6',
                      },
                    }}
                  />
                );
              })}
            </Box>
          )}

          {event.id !== localAiData?.related_event_id && localAiData?.related_event_id && (
            <Box sx={{ mt: '16px', mb: '8px', display: 'flex', alignItems: 'center', gap: '8px' }}>
              <Typography sx={{ fontSize: '16px', fontWeight: 600, color: colors.text.primary }}>Related Event:</Typography>
              <Link
                target='_blank'
                href={`/investigate?id=${encodeURIComponent(localAiData.related_event_id)}&accountId=${event?.cloud_account_id}`}
                passHref
                style={{ fontSize: '14px', color: '#1a73e8', textDecoration: 'underline', fontWeight: 500 }}
              >
                {localAiData.related_event_id.replace(/[^a-zA-Z0-9-]/g, '')}
              </Link>
            </Box>
          )}

          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '32px', marginTop: '16px' }}>
            {sections.map((sec) => (
              <Box key={sec.id} id={`section-${sec.id}`}>
                <Typography
                  sx={{
                    fontSize: '18px',
                    fontWeight: 600,
                    color: colors.text.primary,
                    mb: 2,
                    pb: 1,
                    borderBottom: `2px solid ${colors.border.secondaryLightest}`,
                  }}
                >
                  {sec.label}
                </Typography>

                {sec.status === 'IN_PROGRESS' || sec.status === 'PENDING' ? (
                  <ConversationLoader />
                ) : sec.content && sec.content.trim() !== '' ? (
                  <MarkDowns
                    data={cleanContent(sec.content)}
                    sx={{ width: '100%', padding: noPadding ? '0px' : undefined, maxHeight: 'none', overflowY: 'visible' }}
                  />
                ) : (
                  <Box
                    sx={{
                      padding: '16px',
                      backgroundColor: '#f9fafb',
                      border: '1px dashed #d1d5db',
                      borderRadius: '8px',
                      textAlign: 'center',
                    }}
                  >
                    <Typography sx={{ color: '#6b7280', fontSize: '14px', fontStyle: 'italic' }}>
                      No {sec.label.toLowerCase()} content found.
                    </Typography>
                  </Box>
                )}
              </Box>
            ))}
          </Box>

          {renderExtras()}

          {/* Follow-up conversation messages */}
          {followUpMessages.length > 0 && (
            <Box sx={{ mt: '24px' }}>
              {followUpMessages.map((msg, idx) =>
                msg.type === 'question' ? (
                  <Box key={`fu-q-${idx}`} sx={{ mb: '12px', p: '10px 14px', backgroundColor: colors.background.tertiaryLight, borderRadius: '8px' }}>
                    <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary }}>{msg.text}</Typography>
                  </Box>
                ) : (
                  <Box key={msg.messageId || `fu-a-${idx}`} sx={{ mb: '16px' }}>
                    <MarkDowns data={msg.text} sx={{ width: '100%', padding: '0px', maxHeight: 'none', overflowY: 'visible' }} />
                  </Box>
                )
              )}
              {followUpLoading && <ConversationLoader />}
            </Box>
          )}

          {/* Follow-up Questions */}
          {!followUpLoading && followUpQuestions.length > 0 && localAiData?.status?.toLowerCase() === 'completed' && (
            <Box sx={{ mt: '24px', mb: '8px' }}>
              <Typography sx={{ fontSize: '14px', color: colors.text.secondary, fontWeight: 500, mb: '8px' }}>Related Questions</Typography>
              <Box sx={{ display: 'flex', flexDirection: 'column' }}>
                {followUpQuestions.map((suggestion, idx) => {
                  const suggestionText = typeof suggestion === 'string' ? suggestion : suggestion?.message || '';
                  if (!suggestionText) return null;
                  return (
                    <Box
                      key={suggestion.id || idx}
                      data-testid={`follow-up-question-${idx}`}
                      sx={{
                        width: '100%',
                        p: '8px 0px',
                        borderBottom: `0.5px solid ${colors.border.nudgebeeSuggestion}`,
                        cursor: 'pointer',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'space-between',
                        transition: 'all 0.3s ease',
                        animation: `fadeIn 0.6s ease ${idx * 0.15}s both`,
                        '@keyframes fadeIn': {
                          '0%': { opacity: 0, transform: 'translateY(4px)' },
                          '100%': { opacity: 1, transform: 'translateY(0)' },
                        },
                        '&:hover': {
                          backgroundColor: colors.background.ticketDescription,
                        },
                      }}
                      onClick={() => handleFollowUpQuestion(suggestionText)}
                    >
                      <Typography sx={{ fontSize: '13px', color: colors.text.secondary }}>{suggestionText}</Typography>
                      <FiArrowRight size={16} color={colors.text.secondary} style={{ marginLeft: 'auto', flexShrink: 0 }} />
                    </Box>
                  );
                })}
              </Box>
            </Box>
          )}
        </div>
      );
    };

    return <>{cardInstance.errorMessage ? <Typography>{cardInstance.errorMessage}</Typography> : <AskAICardComponent noPadding={true} />}</>;
  };

  ResolveComponent = (props) => {
    let data = {};
    let namespace = this.event?.subject_namespace,
      workload,
      workloadType,
      container = '';

    if (this.event.subject_type === 'pod') {
      let serviceKeys = this.event.service_key?.split('/');
      workload = serviceKeys[2];
      workloadType = serviceKeys[1];
    }

    if (!workload) {
      for (let e of this.event.evidences) {
        if (e.type === 'json') {
          let jsonData = JSON.parse(e.data);
          if (jsonData.name === 'noisy_neighbours') {
            for (let n of jsonData.data.neighbours) {
              if (n.pod_name === this.event.subject_name && n.namespace === this.event.subject_namespace) {
                let kind = n.kind[0];
                if (kind) {
                  workload = kind.name;
                  workloadType = kind.kind;
                }
                break;
              }
            }
          }
        }
      }
    }

    if (!workload || workloadType === 'ReplicaSet') {
      let workloadSplit = this.event.subject_name?.split('-');
      workload = workloadSplit.slice(0, workloadSplit.length - 2).join('-');
      workloadType = 'Deployment';
    }

    data = {
      id: this.event.id,
      accountId: this.event?.cloud_account_id,
      card_id: this.id,
      container_name: container,
      cloud_resourse: {
        meta: {
          namespace: namespace,
          controller: workload,
          controllerKind: workloadType,
          container: container,
          name: this.event.subject_name,
        },
      },
      aiData: this.aiData,
    };
    return (
      <KubernetesRightSizingUpdateForm
        open={props.open}
        onClose={props.onCloseComponent}
        onSuccess={props.onCloseComponent}
        onFailure={props.onCloseComponent}
        data={data}
        updateResourceType={'raise-pr'}
        recommendationSource='event'
        title={`Raise PR`}
      />
    );
  };

  getResolveComponent = () => {
    return this.ResolveComponent;
  };

  isCompleted = () => {
    return this.aiData?.status?.toLowerCase() === 'completed';
  };
}

export default AskAiCard;
