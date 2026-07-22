import { Box, IconButton, Typography, Tooltip } from '@mui/material';
import { useState, useEffect } from 'react';
import { createPortal } from 'react-dom';
import SafeIcon from '@shared/icons/SafeIcon';
import ChevronLeftIcon from '@mui/icons-material/ChevronLeft';
import ChevronRightIcon from '@mui/icons-material/ChevronRight';
import CloseIcon from '@mui/icons-material/Close';
import { ds } from 'src/utils/colors';
import { CollapseLeftIcon } from '@assets';
import KubernetesLLMResponseGenerator from '@components/llm/KubernetesLLMResponseGeneratorV2';
import { v4 as uuidv4 } from 'uuid';
import { useTenantBranding } from '@hooks/useTenantBranding';

interface NubiChatSidebarProps {
  isVisible: boolean;
  onClose?: () => void;
  accountId: string;
  context?: {
    type: 'workflow' | 'workflowbuilder' | 'cluster' | 'general';
    data?: any;
  };
  // Query customization
  query?: string;
  queryPrefix?: string;
  querySuffix?: string;
  // API customization
  apiMode?: 'investigate' | 'workflow';
  source?: string;
  categorySource?: string;
  // Workflow generation callback - called when AI chat generates a workflow JSON
  onWorkflowGenerated?: (workflowJson: string, sessionId: string) => void;
  // Fires on every completed workflow-mode turn (even tool-only server-side edits that
  // don't echo a JSON), so the parent can re-fetch and refresh after the assistant edits.
  onConversationComplete?: (sessionId: string) => void;
  // Layout customization
  position?: 'left' | 'right';
  mode?: 'overlay' | 'fixed';
  width?: string;
  topOffset?: string;
  showHeader?: boolean;
  enableKeyboardShortcut?: boolean;
  // URL-driven conversation loading: when the parent route carries
  // `?conversation_id` / `?session_id`, forward the ids so the chat opens
  // straight on that historical conversation instead of a fresh session.
  urlConversationId?: string;
  urlSessionId?: string;
}

const NubiChatSidebar: React.FC<NubiChatSidebarProps> = ({
  isVisible,
  onClose,
  accountId,
  context,
  query = '',
  queryPrefix,
  querySuffix,
  apiMode: apiModeProp,
  source: sourceProp,
  categorySource,
  position = 'right',
  mode = 'overlay',
  width = ds.space.mul(0, 250),
  topOffset = ds.space.mul(0, 28),
  showHeader = true,
  enableKeyboardShortcut = true,
  onWorkflowGenerated,
  onConversationComplete,
  urlConversationId = '',
  urlSessionId = '',
}) => {
  const { assistantName, nubiIconUrl } = useTenantBranding();
  // Self-managed session id used only when the parent does not pass a conversationId.
  // Derive the effective sessionId synchronously from props so it never lags one render
  // behind context.data.conversationId — a lag would let the child mount with a stale id,
  // fire handleGenerateInvestigation, then re-fire with the real id and produce duplicate
  // optimistic question messages.
  const [internalSessionId, setInternalSessionId] = useState<string>(() => uuidv4());
  const sessionId = context?.data?.conversationId || internalSessionId;

  // Collapsed = panel slides off-screen and a small floating Nubi bubble takes its place, so
  // it stops covering the page. The chat stays mounted (off-screen, not unmounted) so the
  // conversation / draft input survives a collapse→expand round-trip. Only offered in overlay
  // mode with a header (fixed-mode embeds manage their own width via the parent layout).
  const [isCollapsed, setIsCollapsed] = useState(false);

  // Start a fresh internal conversation when the query or queryPrefix changes
  // (only relevant when the parent did not provide a conversationId).
  useEffect(() => {
    if ((query || queryPrefix) && !context?.data?.conversationId) {
      setInternalSessionId(uuidv4());
    }
  }, [query, queryPrefix, context?.data?.conversationId]);

  // Keyboard shortcut: Cmd/Ctrl + K to toggle
  useEffect(() => {
    if (!enableKeyboardShortcut || !onClose) {
      return;
    }

    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        onClose();
      }
    };

    if (isVisible) {
      window.addEventListener('keydown', handleKeyDown);
    }

    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [isVisible, onClose, enableKeyboardShortcut]);

  // Always reopen expanded — a leftover collapsed bubble from a previous open would look like
  // the panel failed to open.
  useEffect(() => {
    if (!isVisible) {
      setIsCollapsed(false);
    }
  }, [isVisible]);

  // Determine positioning and animation
  const isOverlay = mode === 'overlay';
  const isLeft = position === 'left';
  const isRight = position === 'right';

  // Collapse is an overlay-mode affordance only (fixed-mode width is parent-owned) and needs
  // the header to host the collapse control.
  const canCollapse = isOverlay && showHeader;
  const collapsedActive = canCollapse && isCollapsed;
  const effectiveWidth = isVisible ? width : '0px';

  const getTransform = () => {
    // Collapsed slides the full-width panel off-screen (chat stays mounted) and a floating
    // launcher bubble takes its place — so collapsed animates the same way as closed.
    if (!isVisible || collapsedActive) {
      return isLeft ? 'translateX(-100%)' : 'translateX(100%)';
    }
    return 'translateX(0)';
  };

  const getPositionStyles = () => {
    if (isOverlay) {
      return {
        position: 'fixed' as const,
        top: topOffset,
        [position]: 0,
        height: `calc(100vh - ${topOffset})`,
        zIndex: 1000,
      };
    }
    // Fixed mode - part of layout flow with sticky positioning
    return {
      position: 'sticky' as const,
      top: 0,
      height: '100%',
      minWidth: isVisible ? width : '0px',
      maxWidth: isVisible ? width : '0px',
      flexShrink: 0,
    };
  };

  // Floating launcher shown while collapsed: a circular Nubi bubble (click to re-expand) with
  // a small × badge to fully close. Lives in the same portal as the panel so it floats above
  // the page at the docked edge.
  const collapsedLauncher = collapsedActive && (
    <Box
      sx={{
        position: 'fixed',
        top: `calc(${topOffset} + var(--ds-space-4))`,
        [position]: 'var(--ds-space-4)',
        zIndex: 1000,
        width: ds.space.mul(0, 14),
        height: ds.space.mul(0, 14),
      }}
    >
      <Tooltip title={`Expand ${assistantName}`} placement={isRight ? 'left' : 'right'}>
        <Box
          component='button'
          type='button'
          onClick={() => setIsCollapsed(false)}
          aria-label='Expand assistant panel'
          sx={{
            width: '100%',
            height: '100%',
            borderRadius: '50%',
            border: `1px solid ${ds.brand[200]}`,
            backgroundColor: ds.background[100],
            boxShadow: `0 ${ds.space[1]} 16px ${ds.gray.alpha[300]}`,
            cursor: 'pointer',
            display: 'grid',
            placeItems: 'center',
            padding: 0,
            transition: 'transform 0.15s ease, box-shadow 0.15s ease',
            '&:hover': { transform: 'scale(1.05)', boxShadow: `0 ${ds.space[2]} 22px ${ds.gray.alpha[300]}` },
          }}
        >
          <SafeIcon src={nubiIconUrl} alt={assistantName} width={32} height={32} />
        </Box>
      </Tooltip>
      {onClose && (
        <Tooltip title='Close' placement='top'>
          <IconButton
            onClick={onClose}
            size='small'
            aria-label='Close assistant panel'
            sx={{
              position: 'absolute',
              top: ds.space.mul(1, -1),
              right: ds.space.mul(1, -1),
              width: ds.space.mul(0, 6),
              height: ds.space.mul(0, 6),
              backgroundColor: ds.brand[600],
              color: ds.background[100],
              boxShadow: `0 1px 4px ${ds.gray.alpha[300]}`,
              '&:hover': { backgroundColor: ds.brand[700] },
            }}
          >
            <CloseIcon sx={{ fontSize: 'var(--ds-text-caption)' }} />
          </IconButton>
        </Tooltip>
      )}
    </Box>
  );

  const sidebar = (
    <>
      <Box
        sx={{
          ...getPositionStyles(),
          width: effectiveWidth,
          backgroundColor: ds.background[100],
          boxShadow:
            isOverlay && isVisible && !collapsedActive
              ? isLeft
                ? `${ds.space[1]} 0px 20px ${ds.gray.alpha[300]}`
                : `${ds.space.mul(1, -1)} 0px 20px ${ds.gray.alpha[300]}`
              : 'none',
          transform: getTransform(),
          // `visibility 0.3s` (no easing = it flips at the *end* of the transition) keeps the
          // panel paintable through the slide-out, then hides it once off-screen.
          transition:
            'transform 0.3s cubic-bezier(0.4, 0, 0.2, 1), width 0.3s cubic-bezier(0.4, 0, 0.2, 1), min-width 0.3s cubic-bezier(0.4, 0, 0.2, 1), max-width 0.3s cubic-bezier(0.4, 0, 0.2, 1), box-shadow 0.3s ease, visibility 0.3s',
          // Collapsed/closed: panel sits off-screen but stays mounted so chat state survives.
          // `visibility: hidden` (not just pointerEvents) also pulls the off-screen focusable
          // elements out of the tab order and accessibility tree so keyboard users can't tab
          // into the hidden panel.
          pointerEvents: collapsedActive ? 'none' : 'auto',
          visibility: isVisible && !collapsedActive ? 'visible' : 'hidden',
          display: 'flex',
          flexDirection: 'column',
          borderLeft: isRight && !collapsedActive ? `1px solid ${ds.brand[200]}` : 'none',
          borderRight: isLeft && isVisible && !collapsedActive ? `1px solid ${ds.brand[200]}` : 'none',
          overflow: 'hidden',
        }}
      >
        {/* Header */}
        {showHeader && (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              padding: 'var(--ds-space-2) var(--ds-space-4)',
              borderBottom: `1px solid ${ds.gray[200]}`,
              backgroundColor: ds.background[100],
              minHeight: ds.space.mul(0, 12),
              position: 'sticky',
              top: 0,
              zIndex: 10,
            }}
          >
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)' }}>
              <SafeIcon src={nubiIconUrl} alt={assistantName} width={32} height={32} />
              <Box>
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-title)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    fontFamily: 'Roboto',
                    color: ds.brand[500],
                    lineHeight: 1.2,
                  }}
                >
                  {assistantName} Assistant
                </Typography>
                {context?.type && (
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-caption)',
                      fontWeight: 'var(--ds-font-weight-regular)',
                      color: ds.gray[600],
                      fontFamily: 'Roboto',
                      textTransform: 'capitalize',
                    }}
                  >
                    {context.type === 'workflow' ? 'Automation' : context.type} Context
                  </Typography>
                )}
              </Box>
            </Box>

            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
              {canCollapse && (
                <Tooltip title='Collapse' placement='bottom'>
                  <IconButton onClick={() => setIsCollapsed(true)} size='small' sx={{ color: ds.brand[500] }} aria-label='Collapse assistant panel'>
                    {isRight ? <ChevronRightIcon /> : <ChevronLeftIcon />}
                  </IconButton>
                </Tooltip>
              )}
              {onClose && (
                <Tooltip title={enableKeyboardShortcut ? 'Close (⌘K)' : 'Close'} placement='left'>
                  <IconButton
                    onClick={onClose}
                    size='small'
                    sx={{
                      color: ds.brand[500],
                    }}
                  >
                    <Box
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        transform: isRight ? 'rotate(180deg)' : 'none',
                      }}
                    >
                      <SafeIcon src={CollapseLeftIcon} width={20} height={20} alt='close' />
                    </Box>
                  </IconButton>
                </Tooltip>
              )}
            </Box>
          </Box>
        )}

        {/* Chat Content — kept mounted but hidden while collapsed so the live conversation,
          scroll position and any draft input survive a collapse→expand round-trip. */}
        <Box
          sx={{
            flex: 1,
            overflow: 'hidden',
            position: 'relative',
            backgroundColor: ds.background[100],
            display: 'flex',
            flexDirection: 'column',
            minHeight: 0,
          }}
        >
          {accountId && isVisible ? (
            <KubernetesLLMResponseGenerator
              accountId={accountId}
              query={query}
              queryPrefix={queryPrefix}
              querySuffix={querySuffix}
              popup={true}
              // URL-driven session takes precedence: if the parent route
              // explicitly names a session/conversation, hand that to the chat
              // so it loads the historical thread instead of starting fresh.
              sessionId={urlSessionId || (context?.data?.conversationId || query || queryPrefix ? sessionId : '')}
              conversationId={urlConversationId}
              source={sourceProp || (context?.type === 'workflow' ? 'workflow_builder' : 'ask_nudgbee_chat')}
              categorySource={categorySource}
              showBorder={false}
              apiMode={apiModeProp || (context?.type === 'workflow' || context?.type === 'workflowbuilder' ? 'workflow' : 'investigate')}
              workflowId={context?.data?.id || ''}
              workflowDefinition={context?.data?.definition || null}
              // @ts-expect-error JSX component lacks type annotations for this callback prop
              onWorkflowGenerated={onWorkflowGenerated}
              // @ts-expect-error JSX component lacks type annotations for this callback prop
              onConversationComplete={onConversationComplete}
            />
          ) : (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                height: '100%',
                padding: 'var(--ds-space-5)',
                textAlign: 'center',
              }}
            >
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body-lg)',
                  color: ds.gray[600],
                  fontFamily: 'Roboto',
                }}
              >
                {`Please select a cluster to start chatting with ${assistantName}`}
              </Typography>
            </Box>
          )}
        </Box>
      </Box>
      {collapsedLauncher}
    </>
  );

  // Portal is only needed for overlay mode — fixed mode uses sticky positioning
  // inside a flex layout and must stay in the document flow.
  if (isOverlay) {
    if (typeof document === 'undefined') return null;
    return createPortal(sidebar, document.body);
  }
  return sidebar;
};

export default NubiChatSidebar;
