import React from 'react';
import PropTypes from 'prop-types';
import { Box, Avatar } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import MarkDowns from '@components1/common/MarkDowns';
import SafeIcon from '@components1/common/SafeIcon';
import Text from '@common-new/format/Text';
import ConversationCollapsableCard from '@components1/llm/common/ConversationCollapsableCardV2';
import KubernetesLLMRequestResponse from './KubernetesLLMRequestResponseV2';
import { AskNudgebeeErrorIcon, AskNudgebeeInProgressIcon, AskNudgebeeSkipIcon, AskNudgebeeSuccessIcon, AskNudgebeeWaitingIcon } from '@assets';
import capitalize from 'lodash/capitalize';
import { ds } from '@utils/colors';
import { Button } from '@components1/ds/Button';
import ReferencesPopover from './common/ReferencesModal';
import ResponseMetaRail from './common/ResponseMetaRail';
import FileDownloadIcon from '@mui/icons-material/FileDownload';
import { useTenantBranding, getNubiIconUrl } from '@hooks/useTenantBranding';

const QUESTION_PREVIEW_LINES = 6;

const getCardTitle = (data) => {
  // `thought`/`log` are rewritten by the backend on every LLM completion, so reading
  // them mid-run makes the title flicker between intermediate ReAct steps.
  // Fall back to stable fields (`query`, `agentName`) until status is terminal.
  const isInProgress = data.response_status === 'in_progress' || data.response_status === 'waiting';

  let response = '';
  if (data.question) {
    response = data.question;
  } else if (data.tool === 'planner' && (data.thought || data.query)) {
    response = isInProgress ? data.query || '' : data.thought || data.query;
  } else if (data.log && !isInProgress) {
    response = data.log.replace('Thought: ', '');
  } else if (data.text?.indexOf(': ') === 0) {
    response = data.text.replace(': ', '');
  } else if (data.type === 'followup-question') {
    // Append the user's answer to the title so the collapsed card surfaces "Q → A" without
    // expanding. multi_select responses are stored as JSON arrays; render as comma-list.
    let answerSuffix = '';
    if (data?.response?.status === 'COMPLETED' && data?.response?.text) {
      let answer = data.response.text;
      try {
        const messageConfig =
          typeof data.response.message_config === 'string' ? JSON.parse(data.response.message_config) : data.response.message_config;
        if (messageConfig?.followupType === 'multi_select') {
          const parsed = JSON.parse(answer);
          if (Array.isArray(parsed)) {
            answer = parsed.join(', ');
          }
        }
      } catch {
        // keep raw answer
      }
      answerSuffix = ' → ' + answer;
    }
    response = 'Followup Question: ' + data.text + answerSuffix;
  } else if (isInProgress) {
    response = data.query || '';
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
  followupReadOnlyKey,
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
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space.mul(0, 3), flexShrink: 0 }}>
        <Button
          tone='secondary'
          size='xs'
          id='tool-details-btn'
          onClick={(e) => {
            e.stopPropagation();
            onOpenToolDetails(message);
          }}
        >
          Tool Details
        </Button>
      </Box>
    );
  }

  // Timeline-column icon — avatar for question, Nubi icon for response, status dot for tasks.
  // Extracted from the JSX for the same Sonar S3358 reason as headerActionsNode above.
  let timelineIcon = null;
  if (isQuestion) {
    timelineIcon = (
      <Tooltip title={fullName} placement='top'>
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
          {initials.toUpperCase()}
        </Avatar>
      </Tooltip>
    );
  } else if (isResponse) {
    // Push the Nubi icon down past the (now-compact) meta-rail row so it aligns with
    // the first line of the response body.
    timelineIcon = (
      <Box sx={{ mt: ds.space.mul(1, 7) }}>
        <SafeIcon src={getNubiIconUrl()} alt={assistantName} width={28} height={28} />
      </Box>
    );
  } else if (isTask) {
    let dotBorderColor = '#94A3B8';
    if (message.response_status === 'in_progress') {
      dotBorderColor = 'var(--ds-blue-600)';
    } else if (message.response_status === 'waiting') {
      dotBorderColor = '#F59E0B';
    }
    timelineIcon = (
      <>
        <Box
          sx={{
            width: ds.space[2],
            height: ds.space[2],
            borderRadius: '50%',
            border: `1.5px solid ${dotBorderColor}`,
            backgroundColor: message.response_status === 'in_progress' ? 'var(--ds-blue-100)' : 'var(--ds-brand-100)',
            mt: ds.space.mul(0, 5),
            flexShrink: 0,
          }}
        />
        {!isLastTask && (
          <Box
            sx={{
              width: '1.5px',
              flexGrow: 1,
              background: `linear-gradient(to bottom, #CBD5E1, #E2E8F0)`,
              mt: ds.space[1],
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
        gap: ds.space[3],
        mb: isQuestion && ds.space.mul(1, 5),
        mt: isQuestion && ds.space.mul(1, 15),
        pb: isLastTaskOfLastGroup ? ds.space.mul(0, 25) : 0,
      }}
    >
      {/* Timeline Column */}
      <Box sx={{ width: ds.space.mul(1, 7), display: 'flex', flexDirection: 'column', alignItems: 'center', flexShrink: 0, mt: ds.space[1] }}>
        {timelineIcon}
      </Box>

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
            <Box sx={{ display: 'flex', alignItems: 'start', flexDirection: 'column', gap: ds.space[0] }}>
              <Tooltip title={!isQuestion && cardTitle ? <MarkDowns data={cardTitle} /> : ''} placement='top'>
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
                              height: ds.space.mul(1, 10),
                              background: `linear-gradient(to bottom, transparent, ${'var(--ds-background-200)'})`,
                              pointerEvents: 'none',
                            },
                          }
                        : {}),
                    }}
                  >
                    <Text
                      sx={{
                        fontWeight: isResponse ? '500' : '400',
                        fontSize: isQuestion ? 'var(--ds-text-body)' : isResponse ? 'var(--ds-text-title)' : 'var(--ds-text-small)',
                        color: 'var(--ds-gray-700)',
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
                        mt: ds.space.mul(0, 3),
                        cursor: 'pointer',
                        fontFamily: ds.font.sans,
                        fontSize: 'var(--ds-text-small)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        color: 'var(--ds-gray-500)',
                        '&:hover': { color: 'var(--ds-gray-700)', textDecoration: 'underline' },
                        '&:focus-visible': {
                          outline: `${ds.space[0]} solid var(--ds-gray-300)`,
                          outlineOffset: ds.space[0],
                          borderRadius: ds.space[0],
                        },
                      }}
                    >
                      {showFullText ? 'Show less' : 'Show more'}
                    </Box>
                  )}
                </Box>
              </Tooltip>
              {isQuestion && Array.isArray(message.attachments) && message.attachments.length > 0 && (
                <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: ds.space[2], mt: ds.space[2] }} data-testid='question-attachments'>
                  {message.attachments.map((att, idx) => {
                    const hasData = !!att?.data;
                    const src = hasData ? `data:${att.mime_type || 'image/png'};base64,${att.data}` : null;
                    // Chromium/Safari block top-level navigation to data: URIs, so
                    // window.open(src) silently no-ops. Render the thumbnail as a
                    // download anchor instead — clicking saves the original bytes,
                    // which is allowed for data: URIs.
                    const downloadName = att?.description || `attachment-${att?.id || idx}`;
                    const commonSx = {
                      width: ds.space.mul(1, 18),
                      height: ds.space.mul(1, 18),
                      borderRadius: ds.radius.md,
                      overflow: 'hidden',
                      border: `1px solid var(--ds-gray-300)`,
                      backgroundColor: 'var(--ds-background-200)',
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      cursor: src ? 'pointer' : 'default',
                      textDecoration: 'none',
                    };
                    return hasData ? (
                      <Box
                        key={att?.id || idx}
                        component='a'
                        href={src}
                        download={downloadName}
                        title={att?.description || 'Attached image'}
                        sx={commonSx}
                      >
                        <Box
                          component='img'
                          src={src}
                          alt={att?.description || 'Attached image'}
                          sx={{ width: '100%', height: '100%', objectFit: 'cover', display: 'block' }}
                        />
                      </Box>
                    ) : (
                      <Box key={att?.id || idx} title='This image is no longer available' sx={commonSx}>
                        <Text
                          value='Image expired'
                          format='text'
                          sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', textAlign: 'center', px: ds.space[1] }}
                        />
                      </Box>
                    );
                  })}
                </Box>
              )}
              {!['question', 'response', 'followup-question', 'acknowledgment'].includes(messageType) && (
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], flexWrap: 'wrap' }}>
                  <Tooltip
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
                  </Tooltip>
                  <Text
                    value={
                      message.response_status === 'in_progress'
                        ? 'In-Progress'
                        : message?.response_summary
                        ? message?.response_summary
                        : message?.response?.text === 'error: unable to fetch data'
                        ? 'Unable to fetch data'
                        : capitalize(message.response_status)
                    }
                    sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', fontFamily: ds.font.sans, flex: 1, minWidth: 0 }}
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
                        gap: ds.space[1],
                        cursor: 'pointer',
                        padding: `${ds.space[0]} ${ds.space.mul(0, 3)}`,
                        borderRadius: ds.radius.sm,
                        fontSize: 'var(--ds-text-caption)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        color: 'var(--ds-blue-600)',
                        transition: 'all 0.15s ease',
                        whiteSpace: 'nowrap',
                        '&:hover': {
                          backgroundColor: 'var(--ds-blue-100)',
                        },
                      }}
                    >
                      <svg width='11' height='11' viewBox='0 0 24 24' fill={'var(--ds-blue-600)'} opacity='0.7' aria-hidden='true' focusable='false'>
                        <path d='M14 2H6c-1.1 0-2 .9-2 2v16c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V8l-6-6zm-1 2l5 5h-5V4zM6 20V4h5v7h7v9H6z' />
                      </svg>
                      {`${getUniqueReferencesCount(parsedReferences)} source${getUniqueReferencesCount(parsedReferences) !== 1 ? 's' : ''}`}
                      {parsedReferences.some((r) => r.type === 'file') && (
                        <FileDownloadIcon sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-blue-600)' }} />
                      )}
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
                followupReadOnlyKey={followupReadOnlyKey}
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
  followupReadOnlyKey: PropTypes.string,
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
