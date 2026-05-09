import React from 'react';
import PropTypes from 'prop-types';
import { Box, Avatar } from '@mui/material';
import CustomTooltip from '@components1/common/CustomTooltip';
import MarkDowns from '@components1/common/MarkDowns';
import SafeIcon from '@components1/common/SafeIcon';
import { Text } from '@components1/common';
import ConversationCollapsableCard from '@components1/llm/common/ConversationCollapsableCardV2';
import KubernetesLLMRequestResponse from './KubernetesLLMRequestResponseV2';
import { AskNudgebeeErrorIcon, AskNudgebeeInProgressIcon, AskNudgebeeSkipIcon, AskNudgebeeSuccessIcon, AskNudgebeeWaitingIcon } from '@assets';
import capitalize from 'lodash/capitalize';
import { colors } from 'src/utils/colors';
import CustomButton from '@components1/common/NewCustomButton';
import ReferencesPopover from './common/ReferencesModal';
import ResponseMetaRail from './common/ResponseMetaRail';
import FileDownloadIcon from '@mui/icons-material/FileDownload';
import { useTenantBranding, getNubiIconUrl } from '@hooks/useTenantBranding';

const QUESTION_PREVIEW_LINES = 6;

const getCardTitle = (data) => {
  let response = '';
  if (data.question) {
    response = data.question;
  } else if (data.tool === 'planner' && (data.thought || data.query)) {
    response = data.thought || data.query;
  } else if (data.log) {
    response = data.log.replace('Thought: ', '');
  } else if (data.text?.indexOf(': ') === 0) {
    response = data.text.replace(': ', '');
  } else if (data.type === 'followup-question') {
    response = 'Followup Question: ' + data.text;
  } else {
    response = data.text || '';
  }

  try {
    if (typeof response === 'string' && response.trim().startsWith('{')) {
      const parsed = JSON.parse(response);
      if (parsed && typeof parsed === 'object' && parsed.command) {
        response = parsed.command;
      }
    }
  } catch {
    // ignore
  }

  if (data.agentName?.length > 0) {
    response = data.agentName + ' -> ' + response;
  }
  if (data?.parentAgents?.length > 0) {
    response = data.parentAgents.join(' -> ') + ' -> ' + response;
  }
  if (data?.plannerId) {
    response = '(' + data.plannerId + ') ' + response;
  }

  return response;
};

const MessageItem = ({
  message,
  index,
  isLastInGroup,
  isLastTaskOfLastGroup,
  showFullText,
  onShowFullText,
  isLoadingInvestigation,
  siblingTasks,
  // Context Props
  accountId,
  generateQuestionText,
  handleShare,
  sessionId,
  conversationId,
  agentTokenData,
  messageTokenData,
  handleTokenUsageHover,
  isFetchingTokenData,
  selectedModel,
  conversationStatus,
  // Tool Details & Navigation Props
  onOpenToolDetails,
  onNavigateToTask,
  groupIndex,
  responseMeta,
}) => {
  const [referencesAnchorEl, setReferencesAnchorEl] = React.useState(null);

  const parsedReferences = React.useMemo(() => {
    if (!message.references) {
      return [];
    }
    if (typeof message.references === 'string') {
      try {
        return JSON.parse(message.references);
      } catch {
        return [];
      }
    }
    return Array.isArray(message.references) ? message.references : [];
  }, [message.references]);

  const getUniqueReferencesCount = (references) => {
    if (!references || references.length === 0) {
      return 0;
    }
    const seenUrls = new Set();
    references.forEach((ref) => {
      seenUrls.add(ref.url);
    });
    return seenUrls.size;
  };
  const { assistantName } = useTenantBranding();
  const messageType = message.tool ?? message.type;
  const isQuestion = messageType === 'question';
  const isResponse = messageType === 'response';
  const cardTitle = getCardTitle(message);
  // Heuristic for whether the question can plausibly exceed 6 visible lines.
  // CSS line-clamp does the actual visual clipping; this just decides if we offer the toggle.
  const newlineCount = typeof cardTitle === 'string' ? (cardTitle.match(/\n/g) || []).length : 0;
  const isTruncatable = isQuestion && (cardTitle.length > 240 || newlineCount > QUESTION_PREVIEW_LINES - 1);

  const isTask = !isResponse && messageType !== 'question';
  const isLastTask = isTask && isLastInGroup;

  const fullName = (message?.user && message.user) || '';
  const names = fullName?.trim().split(' ');
  const initials = (names[0]?.charAt(0) ?? '') + (names[names.length - 1]?.charAt(0) ?? '');

  // Header actions slot — meta-rail for responses, "Tool Details" button for tool tasks,
  // nothing for everything else. Extracted to an if-else (instead of a nested ternary) per
  // Sonar S3358 to keep the conditional readable.
  let headerActionsNode = null;
  if (isResponse && responseMeta) {
    headerActionsNode = (
      <ResponseMetaRail
        createdAt={message.created_at}
        updatedAt={message.updated_at}
        taskCount={responseMeta.taskCount}
        contextCount={responseMeta.contextCount}
        memoryCount={responseMeta.memoryCount}
        onOpenTasks={responseMeta.onOpenTasks}
        onOpenContexts={responseMeta.onOpenContexts}
        onOpenMemories={responseMeta.onOpenMemories}
        messageTokenData={responseMeta.messageTokenData}
        onTokenUsageHover={responseMeta.onTokenUsageHover}
        isFetchingTokenData={responseMeta.isFetchingTokenData}
      />
    );
  } else if (isTask && !['followup-question', 'acknowledgment', 'planner'].includes(messageType) && onOpenToolDetails) {
    headerActionsNode = (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', flexShrink: 0 }}>
        <CustomButton
          text='Tool Details'
          variant='secondary'
          id='tool-details-btn'
          onClick={(e) => {
            e.stopPropagation();
            onOpenToolDetails(message);
          }}
          sx={{ fontSize: '10px', padding: '2px 8px', minWidth: 'auto', height: '22px', whiteSpace: 'nowrap' }}
        />
      </Box>
    );
  }

  // Timeline-column icon — avatar for question, Nubi icon for response, status dot for tasks.
  // Extracted from the JSX for the same Sonar S3358 reason as headerActionsNode above.
  let timelineIcon = null;
  if (isQuestion) {
    timelineIcon = (
      <CustomTooltip title={fullName} placement='top'>
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
          {initials.toUpperCase()}
        </Avatar>
      </CustomTooltip>
    );
  } else if (isResponse) {
    // Push the Nubi icon down past the (now-compact) meta-rail row so it aligns with
    // the first line of the response body.
    timelineIcon = (
      <Box sx={{ mt: '28px' }}>
        <SafeIcon src={getNubiIconUrl()} alt={assistantName} width={28} height={28} />
      </Box>
    );
  } else if (isTask) {
    let dotBorderColor = '#94A3B8';
    if (message.response_status === 'in_progress') {
      dotBorderColor = colors.primary;
    } else if (message.response_status === 'waiting') {
      dotBorderColor = '#F59E0B';
    }
    timelineIcon = (
      <>
        <Box
          sx={{
            width: '8px',
            height: '8px',
            borderRadius: '50%',
            border: `1.5px solid ${dotBorderColor}`,
            backgroundColor: message.response_status === 'in_progress' ? colors.background.primaryLightest : '#F1F5F9',
            mt: '10px',
            flexShrink: 0,
          }}
        />
        {!isLastTask && (
          <Box
            sx={{
              width: '1.5px',
              flexGrow: 1,
              background: `linear-gradient(to bottom, #CBD5E1, #E2E8F0)`,
              mt: '4px',
            }}
          />
        )}
      </>
    );
  }

  return (
    <Box
      sx={{
        display: 'flex',
        gap: '12px',
        mb: isQuestion && '20px',
        mt: isQuestion && '60px',
        pb: isLastTaskOfLastGroup ? '50px' : 0,
      }}
    >
      {/* Timeline Column */}
      <Box sx={{ width: '28px', display: 'flex', flexDirection: 'column', alignItems: 'center', flexShrink: 0, mt: '5px' }}>{timelineIcon}</Box>

      {/* Card Content Column */}
      <Box
        sx={{
          width: '100%',
          mb: 'auto',
        }}
      >
        <ConversationCollapsableCard
          id={`task-card-${index}`}
          showFullTextHandler={onShowFullText}
          textLength={isTruncatable}
          toolData={message}
          text={
            <Box sx={{ display: 'flex', alignItems: 'start', flexDirection: 'column', gap: '2px' }}>
              <CustomTooltip title={!isQuestion && cardTitle ? <MarkDowns data={cardTitle} /> : ''} placement='top'>
                <Box sx={{ width: '100%' }}>
                  <Box
                    sx={{
                      width: '100%',
                      // Clip the question to ~6 lines worth of height when collapsed.
                      // max-height (rather than -webkit-line-clamp) is used because the
                      // question renders as markdown, which produces block-level elements
                      // (lists, code blocks) that line-clamp does not measure correctly.
                      ...(isQuestion && isTruncatable && !showFullText
                        ? {
                            maxHeight: `calc(${QUESTION_PREVIEW_LINES} * 1.5em)`,
                            overflow: 'hidden',
                            position: 'relative',
                            // Soft fade-out at the bottom so the truncation reads as
                            // "more content below" rather than a hard cut. Fades into
                            // the question card's background colour.
                            '&::after': {
                              content: '""',
                              position: 'absolute',
                              left: 0,
                              right: 0,
                              bottom: 0,
                              height: '40px',
                              background: `linear-gradient(to bottom, transparent, ${colors.background.NubiQuestion})`,
                              pointerEvents: 'none',
                            },
                          }
                        : {}),
                    }}
                  >
                    <Text
                      sx={{
                        fontWeight: isResponse ? '500' : '400',
                        fontSize: isQuestion ? '13px' : isResponse ? '16px' : '12px',
                        color: colors.text.secondary,
                        fontFamily: isQuestion ? "'Poppins', sans-serif" : 'Roboto',
                        wordBreak: 'break-all',
                        lineHeight: isQuestion ? 1.5 : undefined,
                      }}
                      value={isResponse ? <Box /> : cardTitle}
                      format={isQuestion ? 'markdown' : 'text'}
                      requiredToolTip={false}
                      tooltipClassName={'large-tooltip'}
                      showAutoEllipsis={!isQuestion}
                    />
                  </Box>
                  {isQuestion && isTruncatable && (
                    <Box
                      component='button'
                      type='button'
                      onClick={(e) => {
                        e.stopPropagation();
                        onShowFullText();
                      }}
                      sx={{
                        all: 'unset',
                        mt: '6px',
                        cursor: 'pointer',
                        fontFamily: 'Roboto',
                        fontSize: '12px',
                        fontWeight: 500,
                        color: colors.text.tertiary,
                        '&:hover': { color: colors.text.secondary, textDecoration: 'underline' },
                        '&:focus-visible': {
                          outline: `2px solid ${colors.border.secondary}`,
                          outlineOffset: '2px',
                          borderRadius: '2px',
                        },
                      }}
                    >
                      {showFullText ? 'Show less' : 'Show more'}
                    </Box>
                  )}
                </Box>
              </CustomTooltip>
              {!['question', 'response', 'followup-question', 'acknowledgment'].includes(messageType) && (
                <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px', flexWrap: 'wrap' }}>
                  <CustomTooltip
                    title={
                      message.response_status == 'fail'
                        ? 'Error'
                        : message.response_status == 'skipped'
                        ? 'Skipped'
                        : message.response_status === 'waiting'
                        ? 'Waiting'
                        : message.response_status === 'in_progress'
                        ? 'In-Progress'
                        : 'Success'
                    }
                    placement='top'
                  >
                    <SafeIcon
                      src={
                        message.response_status == 'fail'
                          ? AskNudgebeeErrorIcon
                          : message.response_status == 'skipped'
                          ? AskNudgebeeSkipIcon
                          : message.response_status === 'waiting'
                          ? AskNudgebeeWaitingIcon
                          : message.response_status === 'in_progress'
                          ? AskNudgebeeInProgressIcon
                          : AskNudgebeeSuccessIcon
                      }
                      alt='status icon'
                    />
                  </CustomTooltip>
                  <Text
                    value={
                      message?.response_summary
                        ? message?.response_summary
                        : message?.response?.text === 'error: unable to fetch data'
                        ? 'Unable to fetch data'
                        : message.response_status === 'in_progress'
                        ? 'In-Progress'
                        : capitalize(message.response_status)
                    }
                    sx={{ fontSize: '11px', color: colors.text.tertiary, fontFamily: 'Roboto', flex: 1, minWidth: 0 }}
                  />
                  {parsedReferences.length > 0 && (
                    <Box
                      onMouseEnter={(e) => setReferencesAnchorEl(e.currentTarget)}
                      onClick={(e) => {
                        e.stopPropagation();
                        setReferencesAnchorEl(e.currentTarget);
                      }}
                      sx={{
                        display: 'inline-flex',
                        alignItems: 'center',
                        gap: '4px',
                        cursor: 'pointer',
                        padding: '1px 6px',
                        borderRadius: '4px',
                        fontSize: '10px',
                        fontWeight: 500,
                        color: colors.primary,
                        transition: 'all 0.15s ease',
                        whiteSpace: 'nowrap',
                        '&:hover': {
                          backgroundColor: '#EFF6FF',
                        },
                      }}
                    >
                      <svg width='11' height='11' viewBox='0 0 24 24' fill={colors.primary} opacity='0.7' aria-hidden='true' focusable='false'>
                        <path d='M14 2H6c-1.1 0-2 .9-2 2v16c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V8l-6-6zm-1 2l5 5h-5V4zM6 20V4h5v7h7v9H6z' />
                      </svg>
                      {`${getUniqueReferencesCount(parsedReferences)} source${getUniqueReferencesCount(parsedReferences) !== 1 ? 's' : ''}`}
                      {parsedReferences.some((r) => r.type === 'file') && <FileDownloadIcon sx={{ fontSize: '11px', color: colors.primary }} />}
                    </Box>
                  )}
                  {parsedReferences.length > 0 && (
                    <ReferencesPopover
                      anchorEl={referencesAnchorEl}
                      open={Boolean(referencesAnchorEl)}
                      onClose={(e) => {
                        if (e) {
                          e.stopPropagation();
                        }
                        setReferencesAnchorEl(null);
                      }}
                      references={parsedReferences}
                      accountId={accountId}
                      conversationId={conversationId}
                    />
                  )}
                </Box>
              )}
            </Box>
          }
          contentComponents={
            ['response', 'question', 'acknowledgment', 'followup-question'].includes(messageType) ? (
              <KubernetesLLMRequestResponse
                toolCall={message}
                messages={siblingTasks || []}
                isLoadingInvestigation={isLoadingInvestigation}
                generateQuestionText={generateQuestionText}
                accountId={accountId}
                handleShare={handleShare}
                sessionId={sessionId}
                conversationId={conversationId}
                agentTokenData={agentTokenData}
                messageTokenData={messageTokenData}
                handleTokenUsageHover={handleTokenUsageHover}
                isFetchingTokenData={isFetchingTokenData}
                selectedModel={selectedModel}
                conversationStatus={conversationStatus}
                onOpenToolDetails={onOpenToolDetails}
                onNavigateToTask={onNavigateToTask}
                groupIndex={groupIndex}
              />
            ) : null
          }
          conversationCreatedAt={message?.created_at}
          conversationUpdatedAt={message?.updated_at}
          headerActions={headerActionsNode}
        />
      </Box>
    </Box>
  );
};

MessageItem.propTypes = {
  message: PropTypes.shape({
    id: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
    type: PropTypes.string,
    tool: PropTypes.string,
    text: PropTypes.string,
    response: PropTypes.any,
    response_status: PropTypes.string,
    response_summary: PropTypes.string,
    references: PropTypes.any,
    created_at: PropTypes.string,
    updated_at: PropTypes.string,
    user: PropTypes.string,
    log: PropTypes.string,
    thought: PropTypes.string,
    query: PropTypes.string,
    agentName: PropTypes.string,
    parentAgents: PropTypes.array,
    plannerId: PropTypes.string,
    question: PropTypes.string,
    messageId: PropTypes.string,
  }).isRequired,
  index: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  isLastInGroup: PropTypes.bool,
  isLastTaskOfLastGroup: PropTypes.bool,
  showFullText: PropTypes.bool,
  onShowFullText: PropTypes.func,
  isLoadingInvestigation: PropTypes.bool,
  siblingTasks: PropTypes.array,
  accountId: PropTypes.string,
  generateQuestionText: PropTypes.string,
  handleShare: PropTypes.func,
  sessionId: PropTypes.string,
  conversationId: PropTypes.string,
  agentTokenData: PropTypes.any,
  messageTokenData: PropTypes.any,
  handleTokenUsageHover: PropTypes.func,
  isFetchingTokenData: PropTypes.bool,
  selectedModel: PropTypes.any,
  conversationStatus: PropTypes.string,
  onOpenToolDetails: PropTypes.func,
  onNavigateToTask: PropTypes.func,
  groupIndex: PropTypes.number,
  responseMeta: PropTypes.shape({
    taskCount: PropTypes.number,
    contextCount: PropTypes.number,
    memoryCount: PropTypes.number,
    onOpenTasks: PropTypes.func,
    onOpenContexts: PropTypes.func,
    onOpenMemories: PropTypes.func,
    messageTokenData: PropTypes.any,
    onTokenUsageHover: PropTypes.func,
    isFetchingTokenData: PropTypes.bool,
  }),
};

export default MessageItem;
