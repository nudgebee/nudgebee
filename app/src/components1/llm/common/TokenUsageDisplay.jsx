import React from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { IoIosStats } from 'react-icons/io';
import Tooltip from '@components1/ds/Tooltip';
import { ds } from '@utils/colors';

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
  gap: ds.space[1],
  cursor: 'pointer',
  padding: `${ds.space[1]} ${ds.space.mul(0, 3)}`,
  borderRadius: ds.radius.sm,
  border: `1px solid ${'var(--ds-gray-200)'}`,
  transition: 'all 0.2s ease',
};

/**
 * Common tooltip styles for consistent appearance across all levels
 */
const tooltipStyles = {
  backgroundColor: 'var(--ds-background-100)',
  color: 'var(--ds-gray-700)',
  boxShadow: '0 4px 20px rgba(0, 0, 0, 0.15)',
  padding: 0,
  border: `1px solid ${'var(--ds-blue-400)'}`,
  borderRadius: ds.radius.lg,
  maxWidth: ds.space.mul(1, 105),
  maxHeight: 'none',
  overflow: 'visible',
};

/**
 * Consistent tooltip content container styles
 */
const tooltipContentStyles = {
  padding: ds.space[4],
  maxWidth: ds.space.mul(1, 75),
  minWidth: ds.space.mul(0, 125),
};

/**
 * Consistent title styles for all tooltips
 */
const tooltipTitleStyles = {
  fontSize: 'var(--ds-text-body-lg)',
  fontWeight: 'var(--ds-font-weight-semibold)',
  marginBottom: ds.space[3],
  color: 'var(--ds-gray-700)',
  borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
  paddingBottom: ds.space[2],
};

/**
 * Consistent content row styles
 */
const contentRowStyles = {
  display: 'flex',
  flexDirection: 'column',
  gap: ds.space[2],
};

/**
 * Consistent text styles for content
 */
const contentTextStyles = {
  fontSize: 'var(--ds-text-small)',
  color: 'var(--ds-gray-500)',
  lineHeight: '1.4',
};

/**
 * Consistent cost text styles (highlighted)
 */
const costTextStyles = {
  fontSize: 'var(--ds-text-small)',
  color: 'var(--ds-gray-700)',
  fontWeight: 'var(--ds-font-weight-semibold)',
  marginTop: ds.space[1],
  padding: `${ds.space[1]} ${ds.space[2]}`,
  backgroundColor: 'var(--ds-background-200)',
  borderRadius: ds.radius.sm,
  border: `1px solid ${'var(--ds-gray-200)'}`,
};

/**
 * Model info styles
 */
const modelInfoStyles = {
  fontSize: 'var(--ds-text-caption)',
  color: 'var(--ds-gray-400)',
  fontStyle: 'italic',
  marginBottom: ds.space[2],
  padding: `${ds.space[1]} ${ds.space[2]}`,
  backgroundColor: 'var(--ds-background-200)',
  borderRadius: ds.radius.sm,
};

/**
 * Conversation-level token usage display component
 * Shows total token usage, cost, cache savings, and performance metrics
 */
export const ConversationTokenUsage = ({ tokenUsageData, isLoading = false }) => {
  // Show loading state in full tooltip popup with bee animation
  if (isLoading && !tokenUsageData) {
    const loadingContent = (
      <Box
        sx={{
          padding: `${ds.space.mul(1, 5)} ${ds.space.mul(0, 5)}`,
          width: ds.space.mul(1, 95),
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: ds.space[4],
        }}
      >
        {/* Bee Loading Animation */}
        <Box
          sx={{
            height: ds.space.mul(1, 10),
            width: ds.space.mul(1, 10),
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            borderRadius: ds.radius.sm,
            position: 'relative',
            '&::before': {
              content: '""',
              position: 'absolute',
              width: ds.space.mul(1, 7),
              height: ds.space.mul(1, 7),
              borderRadius: '50%',
              border: `${ds.space[0]} solid ${'var(--ds-amber-500)'}`,
              animation: 'ripple 2s ease-in-out infinite',
              top: '50%',
              left: '50%',
            },
            '&::after': {
              content: '""',
              position: 'absolute',
              width: ds.space.mul(0, 9),
              height: ds.space.mul(0, 9),
              borderRadius: '50%',
              backgroundColor: 'var(--ds-yellow-400)',
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
        <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
          Loading usage metrics...
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', textAlign: 'center' }}>
          Fetching token usage, costs, and performance data
        </Typography>
      </Box>
    );

    return (
      <Tooltip title={loadingContent} placement='bottom' tooltipStyle={tooltipStyles}>
        <Box sx={tokenDisplayStyles}>
          <IoIosStats size={14} color={'var(--ds-gray-500)'} />
        </Box>
      </Tooltip>
    );
  }

  // Show icon with placeholder message if no data loaded yet
  if (!tokenUsageData) {
    const placeholderContent = (
      <Box sx={{ padding: ds.space.mul(0, 5), width: ds.space.mul(1, 95) }}>
        <Typography
          sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)', marginBottom: ds.space[1] }}
        >
          Usage Metrics
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          Hover to load conversation usage metrics including tokens, costs, and performance data.
        </Typography>
      </Box>
    );

    return (
      <Tooltip title={placeholderContent} placement='bottom' tooltipStyle={tooltipStyles}>
        <Box sx={tokenDisplayStyles}>
          <IoIosStats size={14} color={'var(--ds-gray-500)'} />
        </Box>
      </Tooltip>
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

  // A conversation can finish with zero recorded token usage while still
  // carrying other metrics (timing, requests, tool calls) — e.g. the API
  // returns total_input/output_tokens = 0 but valid wall_time_seconds. The
  // component previously returned null in that case, which made the metrics
  // icon vanish the moment data loaded on hover/click (issue #31409). Keep
  // the icon visible whenever any metric is present, and fall back to a
  // placeholder tooltip (matching MessageTokenUsage) only when there is
  // genuinely nothing to show — never disappear after data loads.
  const hasMeaningfulMetrics =
    totalTokens > 0 ||
    total_cost_usd > 0 ||
    total_requests > 0 ||
    total_tool_calls > 0 ||
    wall_time_seconds !== null ||
    agent_active_time_seconds !== null;

  if (!hasMeaningfulMetrics) {
    const placeholderContent = (
      <Box sx={{ padding: ds.space.mul(0, 5), width: ds.space.mul(1, 95) }}>
        <Typography
          sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)', marginBottom: ds.space[1] }}
        >
          Conversation Metrics
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          No usage metrics are available for this conversation.
        </Typography>
      </Box>
    );

    return (
      <Tooltip title={placeholderContent} placement='bottom' tooltipStyle={tooltipStyles}>
        <Box sx={tokenDisplayStyles}>
          <IoIosStats size={14} color={'var(--ds-gray-500)'} />
        </Box>
      </Tooltip>
    );
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
    <Box sx={{ padding: ds.space.mul(0, 5), width: ds.space.mul(1, 95) }}>
      <Typography sx={{ ...tooltipTitleStyles, fontSize: 'var(--ds-text-body)', marginBottom: ds.space[2] }}>Conversation Metrics</Typography>

      {/* Interaction Summary */}
      {(total_requests > 0 || total_tool_calls > 0) && (
        <Box sx={{ marginBottom: ds.space[2] }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
              marginBottom: ds.space[1],
            }}
          >
            Interaction Summary
          </Typography>
          {total_requests > 0 && (
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4], marginBottom: ds.space[0] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Success Rate:</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {success_rate_percentage !== null ? `${success_rate_percentage.toFixed(1)}%` : 'N/A'}
              </Typography>
            </Box>
          )}
          {total_tool_calls > 0 && (
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Tool Calls:</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {total_tool_calls} (✓ {successful_tool_calls})
              </Typography>
            </Box>
          )}
        </Box>
      )}

      {/* Performance Section */}
      {(wall_time_seconds !== null || agent_active_time_seconds !== null || average_latency_seconds !== null) && (
        <Box sx={{ marginBottom: ds.space[2] }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
              marginBottom: ds.space[1],
            }}
          >
            Performance
          </Typography>
          {wall_time_seconds !== null && (
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4], marginBottom: ds.space[0] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Wall Time:</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {formatTime(wall_time_seconds)}
              </Typography>
            </Box>
          )}
          {agent_active_time_seconds !== null && (
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4], marginBottom: ds.space[0] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Agent Active:</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {formatTime(agent_active_time_seconds)}
              </Typography>
            </Box>
          )}
          {api_time_seconds !== null && api_time_percentage !== null && (
            <Box
              sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4], marginBottom: ds.space[0], paddingLeft: ds.space[2] }}
            >
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>» API Time:</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>
                {formatTime(api_time_seconds)} ({api_time_percentage.toFixed(1)}%)
              </Typography>
            </Box>
          )}
          {tool_time_seconds !== null && tool_time_percentage !== null && (
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4], paddingLeft: ds.space[2] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>» Tool Time:</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)' }}>
                {formatTime(tool_time_seconds)} ({tool_time_percentage.toFixed(1)}%)
              </Typography>
            </Box>
          )}
        </Box>
      )}

      {/* Model Usage */}
      {model_usage && model_usage.length > 0 && (
        <Box sx={{ marginBottom: ds.space[2] }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
              marginBottom: ds.space[1],
            }}
          >
            Model Usage
          </Typography>
          <Box
            sx={{
              display: 'grid',
              gridTemplateColumns: '2fr 0.7fr 1fr 1fr 1fr 0.8fr',
              gap: ds.space[0],
              fontSize: 'var(--ds-text-caption)',
              fontFamily: 'monospace',
            }}
          >
            {/* Table Header */}
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
              }}
            >
              Model
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Reqs
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Input
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Cached
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Output
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Cost
            </Typography>

            {/* Table Rows */}
            {model_usage.map((model, idx) => (
              <React.Fragment key={idx}>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', paddingTop: ds.space[1] }}>
                  {model.model_name}
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', paddingTop: ds.space[1], textAlign: 'right' }}>
                  {model.requests}
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', paddingTop: ds.space[1], textAlign: 'right' }}>
                  {formatTokens(model.input_tokens)}
                </Typography>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    color: model.cached_input_tokens > 0 ? 'var(--ds-green-600)' : 'var(--ds-gray-500)',
                    paddingTop: ds.space[1],
                    textAlign: 'right',
                  }}
                >
                  {formatTokens(model.cached_input_tokens)}
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', paddingTop: ds.space[1], textAlign: 'right' }}>
                  {formatTokens(model.output_tokens)}
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', paddingTop: ds.space[1], textAlign: 'right' }}>
                  {formatCost(model.cost_usd)}
                </Typography>
              </React.Fragment>
            ))}
          </Box>
        </Box>
      )}

      {/* Cache Summary */}
      {hasCacheSavings && (
        <Box sx={{ marginBottom: ds.space[2] }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-gray-700)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              marginBottom: ds.space[1],
            }}
          >
            Cache Summary
          </Typography>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[0] }}>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Tokens Cached:</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {formatTokens(cache_savings.tokens_saved)} (
                {cache_savings.cache_hit_rate_percentage !== null ? cache_savings.cache_hit_rate_percentage.toFixed(1) : '0'}%)
              </Typography>
            </Box>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Cost without Cache:</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {formatCost(cache_savings.estimated_cost_without_cache_usd)}
              </Typography>
            </Box>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Actual Cost:</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {formatCost(cache_savings.actual_cost_usd)}
              </Typography>
            </Box>
            <Box
              sx={{
                display: 'grid',
                gridTemplateColumns: '1fr auto',
                columnGap: ds.space[4],
                paddingTop: ds.space.mul(0, 3),
                borderTop: `1px solid ${'var(--ds-gray-200)'}`,
                marginTop: ds.space[0],
              }}
            >
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                You Saved:
              </Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {formatCost(cache_savings.cost_savings_usd)}
              </Typography>
            </Box>
          </Box>
        </Box>
      )}

      {/* Total Cost */}
      <Box sx={{ marginBottom: ds.space[2] }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: 'var(--ds-gray-700)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            marginBottom: ds.space[1],
          }}
        >
          Total Cost
        </Typography>
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4], alignItems: 'center', marginBottom: ds.space[0] }}>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Cost:</Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
            {formatCost(total_cost_usd)}
          </Typography>
        </Box>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          Input: {formatTokens(total_input_tokens)} • Output: {formatTokens(total_output_tokens)} • Cached: {formatTokens(total_cached_input_tokens)}
        </Typography>
      </Box>

      {/* Savings Highlight Statement */}
      {hasCacheSavings && (
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-green-600)', fontStyle: 'italic', marginTop: ds.space[1] }}>
          💰 Savings Highlight: {formatTokens(cache_savings.tokens_saved)} (
          {cache_savings.cache_hit_rate_percentage !== null ? cache_savings.cache_hit_rate_percentage.toFixed(1) : '0'}%) of input tokens were served
          from the cache, reducing costs.
        </Typography>
      )}
    </Box>
  );

  return (
    <Tooltip title={tooltipContent} placement='bottom' tooltipStyle={tooltipStyles}>
      <Box sx={tokenDisplayStyles}>
        <IoIosStats size={14} color={'var(--ds-gray-500)'} />
      </Box>
    </Tooltip>
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
      <Box
        sx={{
          padding: `${ds.space.mul(1, 5)} ${ds.space.mul(0, 5)}`,
          width: ds.space.mul(1, 75),
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: ds.space[4],
        }}
      >
        {/* Bee Loading Animation */}
        <Box
          sx={{
            height: ds.space.mul(1, 10),
            width: ds.space.mul(1, 10),
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            borderRadius: ds.radius.sm,
            position: 'relative',
            '&::before': {
              content: '""',
              position: 'absolute',
              width: ds.space.mul(1, 7),
              height: ds.space.mul(1, 7),
              borderRadius: '50%',
              border: `${ds.space[0]} solid ${'var(--ds-amber-500)'}`,
              animation: 'ripple 2s ease-in-out infinite',
              top: '50%',
              left: '50%',
            },
            '&::after': {
              content: '""',
              position: 'absolute',
              width: ds.space.mul(0, 9),
              height: ds.space.mul(0, 9),
              borderRadius: '50%',
              backgroundColor: 'var(--ds-yellow-400)',
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
        <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
          Loading usage metrics...
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', textAlign: 'center' }}>
          Fetching token usage, costs, and performance data
        </Typography>
      </Box>
    );

    return (
      <Tooltip title={loadingContent} placement='bottom' tooltipStyle={tooltipStyles}>
        <Box sx={tokenDisplayStyles} onMouseEnter={handleMouseEnter}>
          <IoIosStats size={14} color={'var(--ds-gray-500)'} />
        </Box>
      </Tooltip>
    );
  }

  if (!hasData || totalTokens === 0) {
    const placeholderContent = (
      <Box sx={{ padding: ds.space.mul(0, 5), width: ds.space.mul(1, 75) }}>
        <Typography
          sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)', marginBottom: ds.space[1] }}
        >
          Message Metrics
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          Hover to load usage metrics for this message.
        </Typography>
      </Box>
    );

    return (
      <Tooltip title={placeholderContent} placement='bottom' tooltipStyle={tooltipStyles}>
        <Box sx={tokenDisplayStyles} onMouseEnter={handleMouseEnter}>
          <IoIosStats size={14} color={'var(--ds-gray-500)'} />
        </Box>
      </Tooltip>
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
    <Box sx={{ padding: ds.space.mul(0, 5), width: ds.space.mul(1, 95) }}>
      <Typography sx={{ ...tooltipTitleStyles, fontSize: 'var(--ds-text-body)', marginBottom: ds.space[2] }}>Message Metrics</Typography>

      {/* Model Usage Table */}
      {modelUsageArray.length > 0 && (
        <Box sx={{ marginBottom: ds.space[2] }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
              marginBottom: ds.space[1],
            }}
          >
            Model Usage
          </Typography>
          <Box
            sx={{
              display: 'grid',
              gridTemplateColumns: '2fr 0.7fr 1fr 1fr 1fr 0.8fr',
              gap: ds.space[0],
              fontSize: 'var(--ds-text-caption)',
              fontFamily: 'monospace',
            }}
          >
            {/* Table Header */}
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
              }}
            >
              Model
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Agents
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Input
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Cached
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Output
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Cost
            </Typography>

            {/* Table Rows */}
            {modelUsageArray.map((model, idx) => (
              <React.Fragment key={idx}>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', paddingTop: ds.space[1] }}>
                  {model.model_name}
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', paddingTop: ds.space[1], textAlign: 'right' }}>
                  {model.agents}
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', paddingTop: ds.space[1], textAlign: 'right' }}>
                  {formatTokens(model.input_tokens)}
                </Typography>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    color: model.cached_input_tokens > 0 ? 'var(--ds-green-600)' : 'var(--ds-gray-500)',
                    paddingTop: ds.space[1],
                    textAlign: 'right',
                  }}
                >
                  {formatTokens(model.cached_input_tokens)}
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', paddingTop: ds.space[1], textAlign: 'right' }}>
                  {formatTokens(model.output_tokens)}
                </Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', paddingTop: ds.space[1], textAlign: 'right' }}>
                  {formatCost(model.cost_usd)}
                </Typography>
              </React.Fragment>
            ))}
          </Box>
        </Box>
      )}

      {/* Agent Usage Table */}
      {validAgents.length > 0 && (
        <Box sx={{ marginBottom: ds.space[2] }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
              marginBottom: ds.space[1],
            }}
          >
            Agent Usage
          </Typography>
          <Box
            sx={{
              display: 'grid',
              gridTemplateColumns: '2fr 1fr 1fr 1fr 0.8fr',
              gap: ds.space[0],
              fontSize: 'var(--ds-text-caption)',
              fontFamily: 'monospace',
            }}
          >
            {/* Table Header */}
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
              }}
            >
              Agent
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Input
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Cached
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
                textAlign: 'right',
              }}
            >
              Output
            </Typography>
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-700)',
                paddingBottom: ds.space[1],
                borderBottom: `1px solid ${'var(--ds-gray-200)'}`,
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
                    sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', paddingTop: ds.space[1] }}
                    title={tooltipText} // Show full info on hover
                  >
                    {displayName}
                  </Typography>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', paddingTop: ds.space[1], textAlign: 'right' }}>
                    {formatTokens(agent.input_tokens)}
                  </Typography>
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-caption)',
                      color: agent.cached_input_tokens > 0 ? 'var(--ds-green-600)' : 'var(--ds-gray-500)',
                      paddingTop: ds.space[1],
                      textAlign: 'right',
                    }}
                  >
                    {formatTokens(agent.cached_input_tokens)}
                  </Typography>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', paddingTop: ds.space[1], textAlign: 'right' }}>
                    {formatTokens(agent.output_tokens)}
                  </Typography>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', paddingTop: ds.space[1], textAlign: 'right' }}>
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
        <Box sx={{ marginBottom: ds.space[2] }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-caption)',
              color: 'var(--ds-gray-700)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              marginBottom: ds.space[1],
            }}
          >
            Cache Summary
          </Typography>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[0] }}>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Tokens Cached:</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {formatTokens(cacheSavings.tokens_saved)} (
                {cacheSavings.cache_hit_rate_percentage !== null ? cacheSavings.cache_hit_rate_percentage.toFixed(1) : '0'}%)
              </Typography>
            </Box>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Cost without Cache:</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {formatCost(cacheSavings.estimated_cost_without_cache_usd)}
              </Typography>
            </Box>
            <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Actual Cost:</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {formatCost(cacheSavings.actual_cost_usd)}
              </Typography>
            </Box>
            <Box
              sx={{
                display: 'grid',
                gridTemplateColumns: '1fr auto',
                columnGap: ds.space[4],
                paddingTop: ds.space.mul(0, 3),
                borderTop: `1px solid ${'var(--ds-gray-200)'}`,
                marginTop: ds.space[0],
              }}
            >
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                You Saved:
              </Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                {formatCost(cacheSavings.cost_savings_usd)}
              </Typography>
            </Box>
          </Box>
        </Box>
      )}

      {/* Total Cost */}
      <Box sx={{ marginBottom: ds.space[2] }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: 'var(--ds-gray-700)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            marginBottom: ds.space[1],
          }}
        >
          Total Cost
        </Typography>
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr auto', columnGap: ds.space[4], alignItems: 'center', marginBottom: ds.space[0] }}>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Cost:</Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-700)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
            {formatCost(message_cost_usd)}
          </Typography>
        </Box>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
          Input: {formatTokens(message_input_tokens)} • Output: {formatTokens(message_output_tokens)} • Cached:{' '}
          {formatTokens(message_cached_input_tokens)}
        </Typography>
      </Box>

      {/* Cache Savings Highlight */}
      {cacheSavings && cacheSavings.cost_savings_usd > 0 && (
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-green-600)', fontStyle: 'italic', marginTop: ds.space[1] }}>
          💰 Savings Highlight: {formatTokens(cacheSavings.tokens_saved)} (
          {cacheSavings.cache_hit_rate_percentage !== null ? cacheSavings.cache_hit_rate_percentage.toFixed(1) : '0'}%) of input tokens were served
          from the cache, reducing costs.
        </Typography>
      )}
    </Box>
  );

  return (
    <Tooltip title={tooltipContent} placement='bottom' tooltipStyle={tooltipStyles}>
      <Box sx={tokenDisplayStyles}>
        <IoIosStats size={14} color={'var(--ds-gray-500)'} />
      </Box>
    </Tooltip>
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
    <Tooltip title={tooltipContent} placement='bottom' tooltipStyle={tooltipStyles}>
      <Box sx={tokenDisplayStyles}>
        <IoIosStats size={12} color='#666' />
      </Box>
    </Tooltip>
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
