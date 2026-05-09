import React from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { IoIosStats } from 'react-icons/io';
import CustomTooltip from '@components1/common/CustomTooltip';
import { colors } from 'src/utils/colors';

/**
 * Utility function to format token numbers with K/M suffixes
 * @param {number} tokens - The number of tokens to format
 * @returns {string} Formatted token string
 */
const formatTokens = (tokens) => {
  if (tokens === null || tokens === undefined || tokens === 0) {
    return '0';
  }
  if (tokens >= 1000000) {
    return `${(tokens / 1000000).toFixed(1)}M`;
  }
  if (tokens >= 1000) {
    return `${(tokens / 1000).toFixed(1)}K`;
  }
  return tokens.toString();
};

/**
 * Utility function to format cost with proper currency formatting
 * @param {number} cost - The cost to format
 * @returns {string} Formatted cost string
 */
const formatCost = (cost) => {
  if (cost === null || cost === undefined || cost === 0) {
    return '$0.00';
  }
  if (cost >= 1000) {
    return `$${(cost / 1000).toFixed(2)}K`;
  }
  if (cost >= 1) {
    return `$${cost.toFixed(2)}`;
  }
  return `$${cost.toFixed(4)}`;
};

/**
 * Common styles for token usage display containers
 */
const tokenDisplayStyles = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '3px',
  cursor: 'pointer',
  padding: '4px 6px',
  borderRadius: '4px',
  border: `1px solid ${colors.border.secondaryLightest}`,
  transition: 'all 0.2s ease',
};

/**
 * Common tooltip styles for consistent appearance across all levels
 */
const tooltipStyles = {
  backgroundColor: colors.white,
  color: colors.text.secondary,
  boxShadow: '0 4px 20px rgba(0, 0, 0, 0.15)',
  padding: 0,
  border: `1px solid ${colors.border.primaryLightest}`,
  borderRadius: '8px',
  maxWidth: '420px',
  maxHeight: 'none',
  overflow: 'visible',
};

/**
 * Consistent tooltip content container styles
 */
const tooltipContentStyles = {
  padding: '16px',
  maxWidth: '300px',
  minWidth: '250px',
};

/**
 * Consistent title styles for all tooltips
 */
const tooltipTitleStyles = {
  fontSize: '14px',
  fontWeight: 600,
  marginBottom: '12px',
  color: colors.text.secondary,
  borderBottom: `1px solid ${colors.border.secondaryLightest}`,
  paddingBottom: '8px',
};

/**
 * Consistent content row styles
 */
const contentRowStyles = {
  display: 'flex',
  flexDirection: 'column',
  gap: '8px',
};

/**
 * Consistent text styles for content
 */
const contentTextStyles = {
  fontSize: '12px',
  color: colors.text.tertiary,
  lineHeight: '1.4',
};

/**
 * Consistent cost text styles (highlighted)
 */
const costTextStyles = {
  fontSize: '12px',
  color: colors.text.secondary,
  fontWeight: 600,
  marginTop: '4px',
  padding: '4px 8px',
  backgroundColor: colors.background.tertiaryLightest,
  borderRadius: '4px',
  border: `1px solid ${colors.border.secondaryLightest}`,
};

/**
 * Model info styles
 */
const modelInfoStyles = {
  fontSize: '11px',
  color: colors.text.secondaryDark,
  fontStyle: 'italic',
  marginBottom: '8px',
  padding: '4px 8px',
  backgroundColor: colors.background.tertiaryLightest,
  borderRadius: '4px',
};

/**
 * Conversation-level token usage display component
 * Shows total token usage, cost, cache savings, and performance metrics
 */
export const ConversationTokenUsage = ({ tokenUsageData, isLoading = false }) => {
  // Show loading state in full tooltip popup with bee animation
  if (isLoading && !tokenUsageData) {
    const loadingContent = (
      <Box sx={{ padding: '20px 10px', width: '380px', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '16px' }}>
        {/* Bee Loading Animation */}
        <Box
          sx={{
            height: '40px',
            width: '40px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            borderRadius: '4px',
            position: 'relative',
            '&::before': {
              content: '""',
              position: 'absolute',
              width: '28px',
              height: '28px',
              borderRadius: '50%',
              border: `2px solid ${colors.text.yellowLabel}`,
              animation: 'ripple 2s ease-in-out infinite',
              top: '50%',
              left: '50%',
            },
            '&::after': {
              content: '""',
              position: 'absolute',
              width: '18px',
              height: '18px',
              borderRadius: '50%',
              backgroundColor: colors.nudgebeeMain,
              animation: 'pulse 2s ease-in-out infinite',
              top: '50%',
              left: '50%',
            },
            '@keyframes ripple': {
              '0%': {
                transform: 'translate(-50%, -50%) scale(0.8)',
                opacity: 0.8,
              },
              '50%': {
                transform: 'translate(-50%, -50%) scale(1)',
                opacity: 0.3,
              },
              '100%': {
                transform: 'translate(-50%, -50%) scale(0.8)',
                opacity: 0.8,
              },
            },
            '@keyframes pulse': {
              '0%': {
                transform: 'translate(-50%, -50%) scale(1)',
                opacity: 0.8,
              },
              '50%': {
                transform: 'translate(-50%, -50%) scale(0.8)',
                opacity: 1,
              },
              '100%': {
                transform: 'translate(-50%, -50%) scale(1)',
                opacity: 0.8,
              },
            },
          }}
        />
        <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary }}>Loading usage metrics...</Typography>
        <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, textAlign: 'center' }}>
          Fetching token usage, costs, and performance data
        </Typography>
      </Box>
    );

    return (
      <CustomTooltip title={loadingContent} placement='bottom' tooltipStyle={tooltipStyles}>
        <Box sx={tokenDisplayStyles}>
          <IoIosStats size={14} color={colors.text.tertiary} />
        </Box>
      </CustomTooltip>
    );
  }

  // Show icon with placeholder message if no data loaded yet
  if (!tokenUsageData) {
    const placeholderContent = (
      <Box sx={{ padding: '10px', width: '380px' }}>
        <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary, marginBottom: '4px' }}>Usage Metrics</Typography>
        <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>
          Hover to load conversation usage metrics including tokens, costs, and performance data.
        </Typography>
      </Box>
    );

    return (
      <CustomTooltip title={placeholderContent} placement='bottom' tooltipStyle={tooltipStyles}>
        <Box sx={tokenDisplayStyles}>
          <IoIosStats size={14} color={colors.text.tertiary} />
        </Box>
      </CustomTooltip>
    );
  }

  const {
    total_input_tokens = 0,
    total_output_tokens = 0,
    total_cached_input_tokens = 0,
    total_cache_hit_rate_percentage: _total_cache_hit_rate_percentage = null,
    total_cost_usd = 0,
    model_usage = [],
    cache_savings = null,
    success_rate_percentage = null,
    total_requests = 0,
    successful_requests: _successful_requests = 0,
    failed_requests: _failed_requests = 0,
    total_tool_calls = 0,
    successful_tool_calls = 0,
    total_latency_seconds: _total_latency_seconds = null,
    average_latency_seconds = null,
    wall_time_seconds = null,
    agent_active_time_seconds = null,
    tool_time_seconds = null,
    api_time_seconds = null,
    api_time_percentage = null,
    tool_time_percentage = null,
  } = tokenUsageData;

  const totalTokens = total_input_tokens + total_output_tokens;
  if (totalTokens === 0) {
    return null;
  }

  const hasCacheSavings = cache_savings && (cache_savings.cost_savings_usd > 0 || cache_savings.tokens_saved > 0);

  // Helper to format time
  const formatTime = (seconds) => {
    if (seconds === null || seconds === undefined) {
      return null;
    }
    if (seconds < 60) {
      return `${seconds.toFixed(1)}s`;
    }
    const minutes = Math.floor(seconds / 60);
    const secs = Math.floor(seconds % 60);
    if (minutes < 60) {
      return `${minutes}m ${secs}s`;
    }
    const hours = Math.floor(minutes / 60);
    const mins = minutes % 60;
    return `${hours}h ${mins}m ${secs}s`;
  };

  const tooltipContent = (
    <Box sx={{ padding: '10px', width: '380px' }}>
      <Typography sx={{ ...tooltipTitleStyles, fontSize: '13px', marginBottom: '8px' }}>Conversation Metrics</Typography>

      {/* Interaction Summary */}
      {(total_requests > 0 || total_tool_calls > 0) && (
        <Box sx={{ marginBottom: '8px' }}>
          <Typography sx={{ fontSize: '11px', fontWeight: 700, color: colors.text.secondary, marginBottom: '4px' }}>Interaction Summary</Typography>
          {total_requests > 0 && (
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px', marginBottom: '2px' }}>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Success Rate:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 600 }}>
                {success_rate_percentage !== null ? `${success_rate_percentage.toFixed(1)}%` : 'N/A'}
              </Typography>
            </Box>
          )}
          {total_tool_calls > 0 && (
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px' }}>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Tool Calls:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 600 }}>
                {total_tool_calls} (✓ {successful_tool_calls})
              </Typography>
            </Box>
          )}
        </Box>
      )}

      {/* Performance Section */}
      {(wall_time_seconds !== null || agent_active_time_seconds !== null || average_latency_seconds !== null) && (
        <Box sx={{ marginBottom: '8px' }}>
          <Typography sx={{ fontSize: '11px', fontWeight: 700, color: colors.text.secondary, marginBottom: '4px' }}>Performance</Typography>
          {wall_time_seconds !== null && (
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px', marginBottom: '2px' }}>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Wall Time:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 600 }}>{formatTime(wall_time_seconds)}</Typography>
            </Box>
          )}
          {agent_active_time_seconds !== null && (
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px', marginBottom: '2px' }}>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Agent Active:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 600 }}>
                {formatTime(agent_active_time_seconds)}
              </Typography>
            </Box>
          )}
          {api_time_seconds !== null && api_time_percentage !== null && (
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px', marginBottom: '2px', paddingLeft: '8px' }}>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>» API Time:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary }}>
                {formatTime(api_time_seconds)} ({api_time_percentage.toFixed(1)}%)
              </Typography>
            </Box>
          )}
          {tool_time_seconds !== null && tool_time_percentage !== null && (
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px', paddingLeft: '8px' }}>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>» Tool Time:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary }}>
                {formatTime(tool_time_seconds)} ({tool_time_percentage.toFixed(1)}%)
              </Typography>
            </Box>
          )}
        </Box>
      )}

      {/* Model Usage */}
      {model_usage && model_usage.length > 0 && (
        <Box sx={{ marginBottom: '8px' }}>
          <Typography sx={{ fontSize: '11px', fontWeight: 700, color: colors.text.secondary, marginBottom: '4px' }}>Model Usage</Typography>
          <Box
            sx={{
              display: 'grid',
              gridTemplateColumns: '2fr 0.7fr 1fr 1fr 1fr 0.8fr',
              gap: '2px',
              fontSize: '10px',
              fontFamily: 'monospace',
            }}
          >
            {/* Table Header */}
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
              }}
            >
              Model
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Reqs
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Input
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Cached
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Output
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Cost
            </Typography>

            {/* Table Rows */}
            {model_usage.map((model, idx) => (
              <React.Fragment key={idx}>
                <Typography sx={{ fontSize: '10px', color: colors.text.secondary, paddingTop: '4px' }}>{model.model_name}</Typography>
                <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, paddingTop: '4px', textAlign: 'right' }}>
                  {model.requests}
                </Typography>
                <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, paddingTop: '4px', textAlign: 'right' }}>
                  {formatTokens(model.input_tokens)}
                </Typography>
                <Typography
                  sx={{
                    fontSize: '10px',
                    color: model.cached_input_tokens > 0 ? colors.text.success : colors.text.tertiary,
                    paddingTop: '4px',
                    textAlign: 'right',
                  }}
                >
                  {formatTokens(model.cached_input_tokens)}
                </Typography>
                <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, paddingTop: '4px', textAlign: 'right' }}>
                  {formatTokens(model.output_tokens)}
                </Typography>
                <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, paddingTop: '4px', textAlign: 'right' }}>
                  {formatCost(model.cost_usd)}
                </Typography>
              </React.Fragment>
            ))}
          </Box>
        </Box>
      )}

      {/* Cache Summary */}
      {hasCacheSavings && (
        <Box sx={{ marginBottom: '8px' }}>
          <Typography sx={{ fontSize: '11px', color: colors.text.secondary, fontWeight: 700, marginBottom: '4px' }}>Cache Summary</Typography>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px' }}>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Tokens Cached:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 600 }}>
                {formatTokens(cache_savings.tokens_saved)} (
                {cache_savings.cache_hit_rate_percentage !== null ? cache_savings.cache_hit_rate_percentage.toFixed(1) : '0'}%)
              </Typography>
            </Box>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px' }}>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Cost without Cache:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 600 }}>
                {formatCost(cache_savings.estimated_cost_without_cache_usd)}
              </Typography>
            </Box>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px' }}>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Actual Cost:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 600 }}>
                {formatCost(cache_savings.actual_cost_usd)}
              </Typography>
            </Box>
            <Box
              sx={{
                display: 'grid',
                gridTemplateColumns: '1fr auto',
                columnGap: '16px',
                paddingTop: '3px',
                borderTop: `1px solid ${colors.border.secondaryLightest}`,
                marginTop: '2px',
              }}
            >
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 700 }}>You Saved:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 700 }}>
                {formatCost(cache_savings.cost_savings_usd)}
              </Typography>
            </Box>
          </Box>
        </Box>
      )}

      {/* Total Cost */}
      <Box sx={{ marginBottom: '8px' }}>
        <Typography sx={{ fontSize: '11px', color: colors.text.secondary, fontWeight: 700, marginBottom: '4px' }}>Total Cost</Typography>
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px', alignItems: 'center', marginBottom: '2px' }}>
          <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Cost:</Typography>
          <Typography sx={{ fontSize: '11px', color: colors.text.secondary, fontWeight: 700 }}>{formatCost(total_cost_usd)}</Typography>
        </Box>
        <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>
          Input: {formatTokens(total_input_tokens)} • Output: {formatTokens(total_output_tokens)} • Cached: {formatTokens(total_cached_input_tokens)}
        </Typography>
      </Box>

      {/* Savings Highlight Statement */}
      {hasCacheSavings && (
        <Typography sx={{ fontSize: '11px', color: colors.success, fontStyle: 'italic', marginTop: '4px' }}>
          💰 Savings Highlight: {formatTokens(cache_savings.tokens_saved)} (
          {cache_savings.cache_hit_rate_percentage !== null ? cache_savings.cache_hit_rate_percentage.toFixed(1) : '0'}%) of input tokens were served
          from the cache, reducing costs.
        </Typography>
      )}
    </Box>
  );

  return (
    <CustomTooltip title={tooltipContent} placement='bottom' tooltipStyle={tooltipStyles}>
      <Box sx={tokenDisplayStyles}>
        <IoIosStats size={14} color={colors.text.tertiary} />
      </Box>
    </CustomTooltip>
  );
};

/**
 * Message-level token usage display component
 * Shows token usage, agent breakdown, and cost for a specific message (matches ConversationMetrics format)
 */
export const MessageTokenUsage = ({ messageData, onHover, isLoading = false }) => {
  // Always show placeholder if no data available yet
  const hasData = messageData && Object.keys(messageData).length > 0;
  const totalTokens = hasData ? (messageData.message_input_tokens || 0) + (messageData.message_output_tokens || 0) : 0;

  // Handle hover event
  const handleMouseEnter = () => {
    if (onHover && (!hasData || totalTokens === 0)) {
      onHover();
    }
  };

  // Show loading state
  if (isLoading && (!hasData || totalTokens === 0)) {
    const loadingContent = (
      <Box sx={{ padding: '20px 10px', width: '300px', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '16px' }}>
        {/* Bee Loading Animation */}
        <Box
          sx={{
            height: '40px',
            width: '40px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            borderRadius: '4px',
            position: 'relative',
            '&::before': {
              content: '""',
              position: 'absolute',
              width: '28px',
              height: '28px',
              borderRadius: '50%',
              border: `2px solid ${colors.text.yellowLabel}`,
              animation: 'ripple 2s ease-in-out infinite',
              top: '50%',
              left: '50%',
            },
            '&::after': {
              content: '""',
              position: 'absolute',
              width: '18px',
              height: '18px',
              borderRadius: '50%',
              backgroundColor: colors.nudgebeeMain,
              animation: 'pulse 2s ease-in-out infinite',
              top: '50%',
              left: '50%',
            },
            '@keyframes ripple': {
              '0%': {
                transform: 'translate(-50%, -50%) scale(0.8)',
                opacity: 0.8,
              },
              '50%': {
                transform: 'translate(-50%, -50%) scale(1)',
                opacity: 0.3,
              },
              '100%': {
                transform: 'translate(-50%, -50%) scale(0.8)',
                opacity: 0.8,
              },
            },
            '@keyframes pulse': {
              '0%': {
                transform: 'translate(-50%, -50%) scale(1)',
                opacity: 0.8,
              },
              '50%': {
                transform: 'translate(-50%, -50%) scale(0.8)',
                opacity: 1,
              },
              '100%': {
                transform: 'translate(-50%, -50%) scale(1)',
                opacity: 0.8,
              },
            },
          }}
        />
        <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary }}>Loading usage metrics...</Typography>
        <Typography sx={{ fontSize: '11px', color: colors.text.tertiary, textAlign: 'center' }}>
          Fetching token usage, costs, and performance data
        </Typography>
      </Box>
    );

    return (
      <CustomTooltip title={loadingContent} placement='bottom' tooltipStyle={tooltipStyles}>
        <Box sx={tokenDisplayStyles} onMouseEnter={handleMouseEnter}>
          <IoIosStats size={14} color={colors.text.tertiary} />
        </Box>
      </CustomTooltip>
    );
  }

  if (!hasData || totalTokens === 0) {
    const placeholderContent = (
      <Box sx={{ padding: '10px', width: '300px' }}>
        <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary, marginBottom: '4px' }}>Message Metrics</Typography>
        <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>Hover to load usage metrics for this message.</Typography>
      </Box>
    );

    return (
      <CustomTooltip title={placeholderContent} placement='bottom' tooltipStyle={tooltipStyles}>
        <Box sx={tokenDisplayStyles} onMouseEnter={handleMouseEnter}>
          <IoIosStats size={14} color={colors.text.tertiary} />
        </Box>
      </CustomTooltip>
    );
  }

  const {
    message_input_tokens = 0,
    message_output_tokens = 0,
    message_cached_input_tokens = 0,
    message_cache_hit_rate_percentage = null,
    message_cost_usd = 0,
    agents = [],
  } = messageData;

  const hasCacheData = message_cached_input_tokens > 0 || (message_cache_hit_rate_percentage !== null && message_cache_hit_rate_percentage > 0);

  // Filter agents with valid data
  const validAgents = agents.filter(
    (agent) => (agent.input_tokens > 0 || agent.output_tokens > 0) && agent.model_name?.Valid && agent.model_name?.String
  );

  // Aggregate by model
  const modelUsage = validAgents.reduce((acc, agent) => {
    const modelName = agent.model_name.String;
    if (!acc[modelName]) {
      acc[modelName] = {
        model_name: modelName,
        agents: 0,
        input_tokens: 0,
        cached_input_tokens: 0,
        output_tokens: 0,
        cost_usd: 0,
      };
    }
    acc[modelName].agents += 1;
    acc[modelName].input_tokens += agent.input_tokens || 0;
    acc[modelName].cached_input_tokens += agent.cached_input_tokens || 0;
    acc[modelName].output_tokens += agent.output_tokens || 0;
    acc[modelName].cost_usd += agent.cost_usd || 0;
    return acc;
  }, {});

  const modelUsageArray = Object.values(modelUsage);

  // Calculate cache savings with cost details
  let cacheSavings = null;
  if (hasCacheData) {
    // Estimate cost savings
    // Cached tokens typically cost 10% of regular tokens, so we save ~90%
    // Estimate the average cost per input token and calculate savings
    const totalInputTokens = message_input_tokens;

    // If we have cost data, estimate what cached tokens would have cost at full price
    // Rough estimation: cached tokens save ~90% of their cost
    // Average input token cost = total_cost / (input_tokens + output_tokens * output_multiplier)
    // Using a rough 3:1 ratio for output:input cost
    const estimatedCostPerInputToken = message_cost_usd / (totalInputTokens + message_output_tokens * 3);
    const estimatedSavings = message_cached_input_tokens * estimatedCostPerInputToken * 0.9;
    const estimatedCostWithoutCache = message_cost_usd + estimatedSavings;

    cacheSavings = {
      tokens_saved: message_cached_input_tokens,
      cache_hit_rate_percentage: message_cache_hit_rate_percentage,
      actual_cost_usd: message_cost_usd,
      estimated_cost_without_cache_usd: estimatedCostWithoutCache,
      cost_savings_usd: estimatedSavings,
    };
  }

  const tooltipContent = (
    <Box sx={{ padding: '10px', width: '380px' }}>
      <Typography sx={{ ...tooltipTitleStyles, fontSize: '13px', marginBottom: '8px' }}>Message Metrics</Typography>

      {/* Model Usage Table */}
      {modelUsageArray.length > 0 && (
        <Box sx={{ marginBottom: '8px' }}>
          <Typography sx={{ fontSize: '11px', fontWeight: 700, color: colors.text.secondary, marginBottom: '4px' }}>Model Usage</Typography>
          <Box
            sx={{
              display: 'grid',
              gridTemplateColumns: '2fr 0.7fr 1fr 1fr 1fr 0.8fr',
              gap: '2px',
              fontSize: '10px',
              fontFamily: 'monospace',
            }}
          >
            {/* Table Header */}
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
              }}
            >
              Model
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Agents
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Input
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Cached
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Output
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Cost
            </Typography>

            {/* Table Rows */}
            {modelUsageArray.map((model, idx) => (
              <React.Fragment key={idx}>
                <Typography sx={{ fontSize: '10px', color: colors.text.secondary, paddingTop: '4px' }}>{model.model_name}</Typography>
                <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, paddingTop: '4px', textAlign: 'right' }}>{model.agents}</Typography>
                <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, paddingTop: '4px', textAlign: 'right' }}>
                  {formatTokens(model.input_tokens)}
                </Typography>
                <Typography
                  sx={{
                    fontSize: '10px',
                    color: model.cached_input_tokens > 0 ? colors.text.success : colors.text.tertiary,
                    paddingTop: '4px',
                    textAlign: 'right',
                  }}
                >
                  {formatTokens(model.cached_input_tokens)}
                </Typography>
                <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, paddingTop: '4px', textAlign: 'right' }}>
                  {formatTokens(model.output_tokens)}
                </Typography>
                <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, paddingTop: '4px', textAlign: 'right' }}>
                  {formatCost(model.cost_usd)}
                </Typography>
              </React.Fragment>
            ))}
          </Box>
        </Box>
      )}

      {/* Agent Usage Table */}
      {validAgents.length > 0 && (
        <Box sx={{ marginBottom: '8px' }}>
          <Typography sx={{ fontSize: '11px', fontWeight: 700, color: colors.text.secondary, marginBottom: '4px' }}>Agent Usage</Typography>
          <Box
            sx={{
              display: 'grid',
              gridTemplateColumns: '2fr 1fr 1fr 1fr 0.8fr',
              gap: '2px',
              fontSize: '10px',
              fontFamily: 'monospace',
            }}
          >
            {/* Table Header */}
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
              }}
            >
              Agent
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Input
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Cached
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Output
            </Typography>
            <Typography
              sx={{
                fontSize: '10px',
                fontWeight: 600,
                color: colors.text.secondary,
                paddingBottom: '4px',
                borderBottom: `1px solid ${colors.border.secondaryLightest}`,
                textAlign: 'right',
              }}
            >
              Cost
            </Typography>

            {/* Table Rows */}
            {validAgents.map((agent, idx) => {
              // Use agent_name if available, otherwise use shortened agent_id
              const hasAgentName = agent.agent_name && agent.agent_name.trim().length > 0;
              const displayName = hasAgentName
                ? agent.agent_name
                : agent.agent_id.length > 20
                ? agent.agent_id.substring(0, 8) + '...'
                : agent.agent_id;
              const tooltipText = hasAgentName ? `${agent.agent_name} (${agent.agent_id})` : agent.agent_id;

              return (
                <React.Fragment key={idx}>
                  <Typography
                    sx={{ fontSize: '10px', color: colors.text.secondary, paddingTop: '4px' }}
                    title={tooltipText} // Show full info on hover
                  >
                    {displayName}
                  </Typography>
                  <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, paddingTop: '4px', textAlign: 'right' }}>
                    {formatTokens(agent.input_tokens)}
                  </Typography>
                  <Typography
                    sx={{
                      fontSize: '10px',
                      color: agent.cached_input_tokens > 0 ? colors.text.success : colors.text.tertiary,
                      paddingTop: '4px',
                      textAlign: 'right',
                    }}
                  >
                    {formatTokens(agent.cached_input_tokens)}
                  </Typography>
                  <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, paddingTop: '4px', textAlign: 'right' }}>
                    {formatTokens(agent.output_tokens)}
                  </Typography>
                  <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, paddingTop: '4px', textAlign: 'right' }}>
                    {formatCost(agent.cost_usd)}
                  </Typography>
                </React.Fragment>
              );
            })}
          </Box>
        </Box>
      )}

      {/* Cache Summary */}
      {cacheSavings && (
        <Box sx={{ marginBottom: '8px' }}>
          <Typography sx={{ fontSize: '11px', color: colors.text.secondary, fontWeight: 700, marginBottom: '4px' }}>Cache Summary</Typography>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px' }}>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Tokens Cached:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 600 }}>
                {formatTokens(cacheSavings.tokens_saved)} (
                {cacheSavings.cache_hit_rate_percentage !== null ? cacheSavings.cache_hit_rate_percentage.toFixed(1) : '0'}%)
              </Typography>
            </Box>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px' }}>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Cost without Cache:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 600 }}>
                {formatCost(cacheSavings.estimated_cost_without_cache_usd)}
              </Typography>
            </Box>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px' }}>
              <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Actual Cost:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 600 }}>
                {formatCost(cacheSavings.actual_cost_usd)}
              </Typography>
            </Box>
            <Box
              sx={{
                display: 'grid',
                gridTemplateColumns: '1fr auto',
                columnGap: '16px',
                paddingTop: '3px',
                borderTop: `1px solid ${colors.border.secondaryLightest}`,
                marginTop: '2px',
              }}
            >
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 700 }}>You Saved:</Typography>
              <Typography sx={{ fontSize: '10px', color: colors.text.secondary, fontWeight: 700 }}>
                {formatCost(cacheSavings.cost_savings_usd)}
              </Typography>
            </Box>
          </Box>
        </Box>
      )}

      {/* Total Cost */}
      <Box sx={{ marginBottom: '8px' }}>
        <Typography sx={{ fontSize: '11px', color: colors.text.secondary, fontWeight: 700, marginBottom: '4px' }}>Total Cost</Typography>
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: '16px', alignItems: 'center', marginBottom: '2px' }}>
          <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>Cost:</Typography>
          <Typography sx={{ fontSize: '11px', color: colors.text.secondary, fontWeight: 700 }}>{formatCost(message_cost_usd)}</Typography>
        </Box>
        <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>
          Input: {formatTokens(message_input_tokens)} • Output: {formatTokens(message_output_tokens)} • Cached:{' '}
          {formatTokens(message_cached_input_tokens)}
        </Typography>
      </Box>

      {/* Cache Savings Highlight */}
      {cacheSavings && cacheSavings.cost_savings_usd > 0 && (
        <Typography sx={{ fontSize: '11px', color: colors.success, fontStyle: 'italic', marginTop: '4px' }}>
          💰 Savings Highlight: {formatTokens(cacheSavings.tokens_saved)} (
          {cacheSavings.cache_hit_rate_percentage !== null ? cacheSavings.cache_hit_rate_percentage.toFixed(1) : '0'}%) of input tokens were served
          from the cache, reducing costs.
        </Typography>
      )}
    </Box>
  );

  return (
    <CustomTooltip title={tooltipContent} placement='bottom' tooltipStyle={tooltipStyles}>
      <Box sx={tokenDisplayStyles}>
        <IoIosStats size={14} color={colors.text.tertiary} />
      </Box>
    </CustomTooltip>
  );
};

/**
 * Agent-level token usage display component
 * Shows token usage, cost, and model information for a specific agent
 */
export const AgentTokenUsage = ({ agentData }) => {
  if (!agentData) {
    return null;
  }

  const {
    input_tokens = 0,
    output_tokens = 0,
    cached_input_tokens = 0,
    cache_hit_rate_percentage = { Valid: false, Float64: 0 },
    cost_usd = 0,
    model_name = { Valid: false, String: '' },
    model_provider_name = { Valid: false, String: '' },
    agent_name = '',
  } = agentData;

  const totalTokens = input_tokens + output_tokens;
  if (totalTokens === 0) {
    return null;
  }

  const hasCacheData = cached_input_tokens > 0 || (cache_hit_rate_percentage?.Valid && cache_hit_rate_percentage.Float64 > 0);
  const displayTitle = agent_name && agent_name.trim().length > 0 ? `Agent: ${agent_name}` : 'Agent Token Usage';

  const tooltipContent = (
    <Box sx={tooltipContentStyles}>
      <Typography sx={tooltipTitleStyles}>{displayTitle}</Typography>
      {model_name?.Valid && (
        <Typography sx={modelInfoStyles}>
          {model_name.String} ({model_provider_name?.String || 'Unknown'})
        </Typography>
      )}
      <Box sx={contentRowStyles}>
        <Typography sx={contentTextStyles}>
          Input: <strong>{formatTokens(input_tokens)} tokens</strong>
        </Typography>
        <Typography sx={contentTextStyles}>
          Output: <strong>{formatTokens(output_tokens)} tokens</strong>
        </Typography>
        {hasCacheData && (
          <>
            <Typography sx={contentTextStyles}>
              Cached: <strong>{formatTokens(cached_input_tokens)} tokens</strong>
            </Typography>
            {cache_hit_rate_percentage?.Valid && (
              <Typography sx={contentTextStyles}>
                Cache Hit Rate: <strong>{cache_hit_rate_percentage.Float64.toFixed(1)}%</strong>
              </Typography>
            )}
          </>
        )}
        <Typography sx={costTextStyles}>Cost: {formatCost(cost_usd)}</Typography>
      </Box>
    </Box>
  );

  return (
    <CustomTooltip title={tooltipContent} placement='bottom' tooltipStyle={tooltipStyles}>
      <Box sx={tokenDisplayStyles}>
        <IoIosStats size={12} color='#666' />
      </Box>
    </CustomTooltip>
  );
};

// PropTypes for type checking - optimized for minimal necessary data
ConversationTokenUsage.propTypes = {
  tokenUsageData: PropTypes.shape({
    total_input_tokens: PropTypes.number.isRequired,
    total_output_tokens: PropTypes.number.isRequired,
    total_cached_input_tokens: PropTypes.number,
    total_cache_hit_rate_percentage: PropTypes.number,
    total_cost_usd: PropTypes.number.isRequired,
    model_usage: PropTypes.arrayOf(
      PropTypes.shape({
        model_provider: PropTypes.string,
        model_name: PropTypes.string,
        requests: PropTypes.number,
        cost_usd: PropTypes.number,
        success_rate_percentage: PropTypes.number,
      })
    ),
    cache_savings: PropTypes.shape({
      cost_savings_usd: PropTypes.number,
      estimated_cost_without_cache_usd: PropTypes.number,
      actual_cost_usd: PropTypes.number,
      tokens_saved: PropTypes.number,
    }),
    success_rate_percentage: PropTypes.number,
    total_requests: PropTypes.number,
    successful_requests: PropTypes.number,
    failed_requests: PropTypes.number,
    total_tool_calls: PropTypes.number,
    successful_tool_calls: PropTypes.number,
    total_latency_seconds: PropTypes.number,
    average_latency_seconds: PropTypes.number,
  }),
  isLoading: PropTypes.bool,
};

MessageTokenUsage.propTypes = {
  messageData: PropTypes.shape({
    message_input_tokens: PropTypes.number.isRequired,
    message_output_tokens: PropTypes.number.isRequired,
    message_cached_input_tokens: PropTypes.number,
    message_cache_hit_rate_percentage: PropTypes.number,
    message_cost_usd: PropTypes.number.isRequired,
    agents: PropTypes.arrayOf(
      PropTypes.shape({
        agent_id: PropTypes.string,
        agent_name: PropTypes.string,
        input_tokens: PropTypes.number,
        output_tokens: PropTypes.number,
        cached_input_tokens: PropTypes.number,
        cost_usd: PropTypes.number,
        model_name: PropTypes.object,
        model_provider_name: PropTypes.object,
      })
    ),
  }),
  onHover: PropTypes.func,
  isLoading: PropTypes.bool,
};

AgentTokenUsage.propTypes = {
  agentData: PropTypes.shape({
    input_tokens: PropTypes.number.isRequired,
    output_tokens: PropTypes.number.isRequired,
    cached_input_tokens: PropTypes.number,
    cache_hit_rate_percentage: PropTypes.shape({
      Valid: PropTypes.bool,
      Float64: PropTypes.number,
    }),
    cost_usd: PropTypes.number.isRequired,
    model_name: PropTypes.shape({
      Valid: PropTypes.bool.isRequired,
      String: PropTypes.string.isRequired,
    }).isRequired,
    model_provider_name: PropTypes.shape({
      Valid: PropTypes.bool.isRequired,
      String: PropTypes.string.isRequired,
    }).isRequired,
    agent_name: PropTypes.string,
  }),
};

// Default exports for convenience
export default {
  ConversationTokenUsage,
  MessageTokenUsage,
  AgentTokenUsage,
};
