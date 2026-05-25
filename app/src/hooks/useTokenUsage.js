import { useState, useCallback } from 'react';
import apiAskNudgebee from '@api1/ask-nudgebee'; // Adjust path as needed

export const useTokenUsage = (accountId) => {
  // Grouped state to prevent multiple re-renders
  const [metrics, setMetrics] = useState({
    conversation: null, // tokenUsageData
    messages: {}, // messageTokenData
    agents: {}, // agentTokenData
  });

  const resetTokenMetrics = useCallback(() => {
    setMetrics({ conversation: null, messages: {}, agents: {} });
  }, []);

  const fetchTokenUsage = useCallback(
    async (sessionId) => {
      if (!accountId || !sessionId) {
        return;
      }

      try {
        const res = await apiAskNudgebee.getConversationUsageMetrics(accountId, sessionId);

        // Handle nested conversation structure depending on API response format
        const conversationData = res?.conversation || res;

        // 1. Extract Conversation Level Metrics
        const conversationTokens = conversationData
          ? {
              total_input_tokens: conversationData.total_input_tokens || 0,
              total_output_tokens: conversationData.total_output_tokens || 0,
              total_cached_input_tokens: conversationData.total_cached_input_tokens || 0,
              total_cache_hit_rate_percentage: conversationData.total_cache_hit_rate_percentage,
              total_cost_usd: conversationData.total_cost_usd || 0,
              model_usage: conversationData.model_usage || [],
              cache_savings: conversationData.cache_savings || null,
              success_rate_percentage: conversationData.success_rate_percentage,
              total_requests: conversationData.total_requests || 0,
              successful_requests: conversationData.successful_requests || 0,
              failed_requests: conversationData.failed_requests || 0,
              total_tool_calls: conversationData.total_tool_calls || 0,
              successful_tool_calls: conversationData.successful_tool_calls || 0,
              total_latency_seconds: conversationData.total_latency_seconds,
              average_latency_seconds: conversationData.average_latency_seconds,
              wall_time_seconds: conversationData.wall_time_seconds,
              agent_active_time_seconds: conversationData.agent_active_time_seconds,
              tool_time_seconds: conversationData.tool_time_seconds,
              api_time_seconds: conversationData.api_time_seconds,
              api_time_percentage: conversationData.api_time_percentage,
              tool_time_percentage: conversationData.tool_time_percentage,
            }
          : null;

        // 2. Process Message & Agent Level Metrics
        const messageData = {};
        const agentData = {};

        if (conversationData?.messages) {
          conversationData.messages.forEach((message) => {
            // Map message metrics
            messageData[message.message_id] = {
              message_input_tokens: message.message_input_tokens || 0,
              message_output_tokens: message.message_output_tokens || 0,
              message_cached_input_tokens: message.message_cached_input_tokens || 0,
              message_cache_hit_rate_percentage: message.message_cache_hit_rate_percentage,
              message_cost_usd: message.message_cost_usd || 0,
              agents: message.agents,
            };

            // Map agent metrics within message
            if (message.agents) {
              message.agents.forEach((agent) => {
                agentData[agent.agent_id] = {
                  input_tokens: agent.input_tokens || 0,
                  output_tokens: agent.output_tokens || 0,
                  cached_input_tokens: agent.cached_input_tokens || 0,
                  cache_hit_rate_percentage: agent.cache_hit_rate_percentage || { Valid: false, Float64: 0 },
                  cost_usd: agent.cost_usd || 0,
                  model_name: agent.model_name || { Valid: false, String: '' },
                  model_provider_name: agent.model_provider_name || { Valid: false, String: '' },
                  agent_name: agent.agent_name || '',
                };
              });
            }
          });
        }

        setMetrics({
          conversation: conversationTokens,
          messages: messageData,
          agents: agentData,
        });
      } catch (error) {
        console.error('Error fetching token usage metrics:', error);
      }
    },
    [accountId]
  );

  // Helper moved from component to hook (since it uses the state)
  const getAgentTokenDataForMessage = useCallback(
    (message) => {
      // 1. Try finding by tool_id (direct mapping)
      if (message?.tool_id && metrics.agents[message.tool_id]) {
        return metrics.agents[message.tool_id];
      }

      // 2. Try finding by message ID
      const msgId = message?.id || message?.messageId;
      if (msgId && metrics.messages[msgId]?.agents) {
        const messageAgents = metrics.messages[msgId].agents;
        // Find agent with actual usage
        const matchingAgent = messageAgents.find((agent) => agent.input_tokens > 0 || agent.output_tokens > 0);
        if (matchingAgent) {
          return matchingAgent;
        }
      }

      return null;
    },
    [metrics.agents, metrics.messages]
  );

  return {
    tokenUsageData: metrics.conversation,
    messageTokenData: metrics.messages,
    agentTokenData: metrics.agents,
    fetchTokenUsage,
    resetTokenMetrics,
    getAgentTokenDataForMessage,
  };
};
