import apiKubernetes from '@api1/kubernetes';
import apiAskNudgebee, { createConversationFetcher } from '@api1/ask-nudgebee';
import MarkDowns from '@components1/common/MarkDowns';
import { Typography, Box, CircularProgress } from '@mui/material';
import { Link } from '@components1/ds/Link';
import { Chip } from '@components1/ds/Chip';
import { Input } from '@components1/ds/Input';
import { Button as DsButton } from '@components1/ds/Button';
import DOMPurify from 'dompurify';
import KubernetesRightSizingUpdateForm from '@components1/recommendations/KubernetesRightSizingUpdateForm';
import { useEffect, useState, useCallback, useRef } from 'react';
import ConversationLoader from '@common-new/ConversationLoader';
import { ds } from 'src/utils/colors';
import { ANNOTATIONS } from '@lib/annotationKeys';
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

      if (this.aiData?.source_details?.[ANNOTATIONS.WORKLOAD_GIT_REPO]) {
        let githubRepoUrl = this.aiData?.source_details?.[ANNOTATIONS.WORKLOAD_GIT_REPO];
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
                  gap: 'var(--ds-space-2)',
                  paddingY: ds.space.mul(0, 5),
                }}
              >
                <Typography fontSize={ds.text.bodyLg}>{message}</Typography>
                <Link
                  target='_blank'
                  href={`https://github.com/search?q=repo%3A${githubRepo[0]}%2F${githubRepo[1]}+${path}&type=code`}
                  style={{
                    textDecoration: 'underline',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    lineHeight: 1.5,
                  }}
                >
                  {text}
                </Link>
              </div>
            )}
            {this?.aiData?.source_updates?.gitDiff && (
              <Typography color={ds.red[500]} fontSize={ds.text.bodyLg}>
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
      // or the GraphQL/RPC error string from parseHttpResponseBodyMessage.
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
      // One fetcher for the follow-up poll loop. Holds cursor + merged Maps,
      // so the 3s poll fetches only deltas — and the response payload always
      // contains full TOAST fields for any row that changed.
      const followUpFetcherRef = useRef(null);
      if (!followUpFetcherRef.current) {
        followUpFetcherRef.current = createConversationFetcher();
      }

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

            // Reset the fetcher so this submit starts from a clean cursor on
            // the new (account_id, session_id) pair. The fetcher auto-resets
            // on identity change too, but explicit reset is clearer here and
            // prevents leaking state across rapid resubmits to the same id.
            followUpFetcherRef.current.reset();

            // Poll for the response. Each delta returns full TOAST fields for
            // any row that changed — no separate "full re-fetch on terminal"
            // step needed.
            let attempts = 0;
            followUpPollRef.current = setInterval(async () => {
              attempts++;
              try {
                const res = await followUpFetcherRef.current.fetch({
                  accountId: event.cloud_account_id,
                  sessionId,
                });
                const conversation = res?.data?.data?.llm_conversations?.[0];
                const messages = conversation?.llm_conversation_messages || [];
                const isComplete = conversation?.status === 'COMPLETED' || conversation?.status === 'FAILED';

                if (isComplete) {
                  clearInterval(followUpPollRef.current);
                  followUpPollRef.current = null;
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
          <Box key={insight.message} sx={{ marginBottom: 'var(--ds-space-2)' }}>
            {insight.component && <Box sx={{ marginTop: 'var(--ds-space-1)', fontSize: 'var(--ds-text-small)' }}>{insight.component}</Box>}
          </Box>
        ));

        const fileName = localAiData?.file_details?.files?.[0]?.file_path?.split('/').pop() || 'code';

        return (
          <Box>
            {extrasContent && (
              <MarkDowns data={DOMPurify.sanitize(extrasContent)} sx={{ width: '100%', padding: noPadding ? '0px' : undefined, maxHeight: 'auto' }} />
            )}

            {code_analysis_enabled !== false && source_updates?.gitDiff && (
              <Box sx={{ marginTop: 'var(--ds-space-4)' }}>
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
              <Box
                sx={{
                  marginTop: 'var(--ds-space-4)',
                  padding: 'var(--ds-space-3) var(--ds-space-4)',
                  backgroundColor: 'var(--ds-blue-100)',
                  borderLeft: '4px solid var(--ds-blue-500)',
                  borderRadius: 'var(--ds-radius-sm)',
                }}
              >
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    marginBottom: 'var(--ds-space-2)',
                    color: 'var(--ds-blue-700)',
                  }}
                >
                  {source_updates?.gitDiff ? 'Reasoning behind the proposed code changes' : 'No Code Changes Required'}
                </Typography>
                <MarkDowns data={source_updates.explanation} sx={{ fontSize: 'var(--ds-text-body)', lineHeight: 1.6 }} />
              </Box>
            )}

            {Insights.length > 0 && (
              <Box
                sx={{
                  marginTop: 'var(--ds-space-4)',
                  padding: 'var(--ds-space-2) var(--ds-space-4)',
                  backgroundColor: 'var(--ds-background-200)',
                  borderRadius: 'var(--ds-radius-sm)',
                  fontSize: 'var(--ds-text-small)',
                }}
              >
                {Insights}
              </Box>
            )}
          </Box>
        );
      };

      return (
        <div style={{ width: '100%' }}>
          {/* Inline follow-up questions with options or text input */}
          {waitingFollowUpItems.length > 0 && (
            <Box sx={{ mb: 'var(--ds-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
              {waitingFollowUpItems.map((item, idx) => {
                const hasOptions = item.followupOptions && item.followupOptions.length > 0;
                return (
                  <Box
                    key={item.id || idx}
                    sx={{
                      padding: 'var(--ds-space-4)',
                      backgroundColor: ds.yellow[100],
                      border: `0.5px solid ${ds.amber[300]}`,
                      borderRadius: 'var(--ds-radius-lg)',
                    }}
                  >
                    <Typography
                      sx={{
                        fontSize: 'var(--ds-text-body-lg)',
                        fontWeight: 'var(--ds-font-weight-semibold)',
                        color: ds.blue[500],
                        mb: 'var(--ds-space-2)',
                      }}
                    >
                      Agent - {item.agentName}
                    </Typography>
                    <Typography sx={{ fontSize: 'var(--ds-text-body)', color: ds.blue[500], mb: 'var(--ds-space-3)' }}>{item.question}</Typography>
                    {item.toolParams && (
                      <Box
                        sx={{
                          mb: 'var(--ds-space-3)',
                          p: 'var(--ds-space-2) var(--ds-space-3)',
                          backgroundColor: 'var(--ds-background-300)',
                          borderRadius: 'var(--ds-radius-md)',
                        }}
                      >
                        <MarkDowns
                          data={typeof item.toolParams === 'object' ? JSON.stringify(item.toolParams) : item.toolParams}
                          sx={{ width: '100%', padding: '0px' }}
                        />
                      </Box>
                    )}

                    {/* Option buttons (single_select / tool_confirmation / multi_select) */}
                    {hasOptions && (
                      <>
                        <Typography
                          sx={{
                            fontSize: 'var(--ds-text-body)',
                            fontWeight: 'var(--ds-font-weight-medium)',
                            color: ds.blue[500],
                            mb: 'var(--ds-space-2)',
                          }}
                        >
                          Options
                        </Typography>
                        <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', flexWrap: 'wrap' }}>
                          {item.followupOptions.map((option) => (
                            <Box
                              key={option}
                              data-testid={`followup-option-${option}`}
                              onClick={() => !followUpSubmitting && handleFollowUpSubmit(option, item)}
                              sx={{
                                padding: 'var(--ds-space-1) var(--ds-space-4)',
                                border: `1px solid ${ds.blue[500]}`,
                                borderRadius: 'var(--ds-radius-md)',
                                cursor: followUpSubmitting ? 'not-allowed' : 'pointer',
                                opacity: followUpSubmitting ? 0.6 : 1,
                                fontSize: 'var(--ds-text-body)',
                                fontWeight: 'var(--ds-font-weight-medium)',
                                color: ds.blue[500],
                                backgroundColor: 'var(--ds-background-100)',
                                transition: 'all 0.2s ease',
                                '&:hover': {
                                  backgroundColor: followUpSubmitting ? ds.background[100] : ds.blue[200],
                                  borderColor: followUpSubmitting ? ds.blue[500] : ds.blue[500],
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
                      <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', alignItems: 'flex-start', mt: 'var(--ds-space-1)' }}>
                        <Box sx={{ flex: 1 }} data-testid={`followup-input-${item.agentName}`}>
                          <Input
                            size='sm'
                            type='textarea'
                            placeholder='Type your response...'
                            value={followUpInputs[item.id] || ''}
                            onChange={(value) => setFollowUpInputs((prev) => ({ ...prev, [item.id]: value }))}
                            disabled={followUpSubmitting}
                            maxRows={3}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter' && !e.shiftKey) {
                                e.preventDefault();
                                handleFollowUpSubmit(followUpInputs[item.id], item);
                              }
                            }}
                          />
                        </Box>
                        <DsButton
                          tone='primary'
                          size='sm'
                          disabled={followUpSubmitting || !followUpInputs[item.id]?.trim()}
                          loading={followUpSubmitting}
                          onClick={() => handleFollowUpSubmit(followUpInputs[item.id], item)}
                          data-testid={`followup-submit-${item.agentName}`}
                        >
                          Submit
                        </DsButton>
                      </Box>
                    )}

                    {followUpSubmitting && (
                      <Box sx={{ mt: 'var(--ds-space-2)', display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                        <CircularProgress size={14} />
                        <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.brand[500] }}>Submitting response...</Typography>
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
                gap: 'var(--ds-space-2)',
                padding: 'var(--ds-space-2) 0',
                marginBottom: 'var(--ds-space-4)',
                borderBottom: '1px solid var(--ds-gray-300)',
                flexWrap: 'wrap',
              }}
            >
              {sections.map((sec) => {
                const isCompleted = sec.status === 'COMPLETED';
                const isInProgress = sec.status === 'IN_PROGRESS' || sec.status === 'PENDING';
                return (
                  <Chip
                    key={sec.id}
                    size='sm'
                    tone={isCompleted ? 'info' : 'neutral'}
                    icon={isInProgress ? <CircularProgress size={12} /> : undefined}
                    onClick={() => {
                      document.getElementById(`section-${sec.id}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' });
                    }}
                  >
                    {sec.label}
                  </Chip>
                );
              })}
            </Box>
          )}

          {event.id !== localAiData?.related_event_id && localAiData?.related_event_id && (
            <Box sx={{ mt: 'var(--ds-space-4)', mb: 'var(--ds-space-2)', display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
              <Typography sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', color: ds.blue[500] }}>
                Related Event:
              </Typography>
              <Link
                target='_blank'
                href={`/investigate?id=${encodeURIComponent(localAiData.related_event_id)}&accountId=${event?.cloud_account_id}`}
                style={{
                  fontSize: 'var(--ds-text-body-lg)',
                  color: 'var(--ds-blue-500)',
                  textDecoration: 'underline',
                  fontWeight: 'var(--ds-font-weight-medium)',
                }}
              >
                {localAiData.related_event_id.replace(/[^a-zA-Z0-9-]/g, '')}
              </Link>
            </Box>
          )}

          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-6)', marginTop: 'var(--ds-space-4)' }}>
            {sections.map((sec) => (
              <Box key={sec.id} id={`section-${sec.id}`}>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: ds.blue[500],
                    mb: 'var(--ds-space-4)',
                    pb: 'var(--ds-space-2)',
                    borderBottom: `2px solid ${ds.gray[300]}`,
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
                      padding: 'var(--ds-space-4)',
                      backgroundColor: 'var(--ds-background-200)',
                      border: '1px dashed var(--ds-brand-200)',
                      borderRadius: 'var(--ds-radius-lg)',
                      textAlign: 'center',
                    }}
                  >
                    <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-body-lg)', fontStyle: 'italic' }}>
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
            <Box sx={{ mt: 'var(--ds-space-5)' }}>
              {followUpMessages.map((msg, idx) =>
                msg.type === 'question' ? (
                  <Box
                    key={`fu-q-${idx}`}
                    sx={{
                      mb: 'var(--ds-space-3)',
                      p: 'var(--ds-space-2) var(--ds-space-3)',
                      backgroundColor: ds.gray[100],
                      borderRadius: 'var(--ds-radius-lg)',
                    }}
                  >
                    <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.brand[500] }}>
                      {msg.text}
                    </Typography>
                  </Box>
                ) : (
                  <Box key={msg.messageId || `fu-a-${idx}`} sx={{ mb: 'var(--ds-space-4)' }}>
                    <MarkDowns data={msg.text} sx={{ width: '100%', padding: '0px', maxHeight: 'none', overflowY: 'visible' }} />
                  </Box>
                )
              )}
              {followUpLoading && <ConversationLoader />}
            </Box>
          )}

          {/* Follow-up Questions */}
          {!followUpLoading && followUpQuestions.length > 0 && localAiData?.status?.toLowerCase() === 'completed' && (
            <Box sx={{ mt: 'var(--ds-space-5)', mb: 'var(--ds-space-2)' }}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body-lg)',
                  color: ds.brand[500],
                  fontWeight: 'var(--ds-font-weight-medium)',
                  mb: 'var(--ds-space-2)',
                }}
              >
                Related Questions
              </Typography>
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
                        p: 'var(--ds-space-2) 0px',
                        borderBottom: `0.5px solid ${ds.brand[150]}`,
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
                          backgroundColor: ds.background[200],
                        },
                      }}
                      onClick={() => handleFollowUpQuestion(suggestionText)}
                    >
                      <Typography sx={{ fontSize: 'var(--ds-text-body)', color: ds.brand[500] }}>{suggestionText}</Typography>
                      <FiArrowRight size={16} color={ds.brand[500]} style={{ marginLeft: 'auto', flexShrink: 0 }} />
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
