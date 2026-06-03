import { Box, IconButton, Typography, Tooltip } from '@mui/material';
import { useState, useEffect } from 'react';
import { createPortal } from 'react-dom';
import SafeIcon from '@components1/common/SafeIcon';
import { colors } from 'src/utils/colors';
import { CollapseLeftIcon } from '@assets';
import KubernetesLLMResponseGenerator from '@components1/llm/KubernetesLLMResponseGeneratorV2';
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
  width = '500px',
  topOffset = '56px',
  showHeader = true,
  enableKeyboardShortcut = true,
  onWorkflowGenerated,
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

  // Determine positioning and animation
  const isOverlay = mode === 'overlay';
  const isLeft = position === 'left';
  const isRight = position === 'right';

  const getTransform = () => {
    // Both fixed and overlay modes respond to isVisible
    if (!isVisible) {
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

  const sidebar = (
    <Box
      sx={{
        ...getPositionStyles(),
        width: isVisible ? width : '0px',
        backgroundColor: colors.background.white,
        boxShadow: isOverlay && isVisible ? (isLeft ? '4px 0px 20px rgba(0, 0, 0, 0.1)' : '-4px 0px 20px rgba(0, 0, 0, 0.1)') : 'none',
        transform: getTransform(),
        transition:
          'transform 0.3s cubic-bezier(0.4, 0, 0.2, 1), width 0.3s cubic-bezier(0.4, 0, 0.2, 1), min-width 0.3s cubic-bezier(0.4, 0, 0.2, 1), max-width 0.3s cubic-bezier(0.4, 0, 0.2, 1), box-shadow 0.3s ease',
        display: 'flex',
        flexDirection: 'column',
        borderLeft: isRight ? `1px solid ${colors.border.secondary}` : 'none',
        borderRight: isLeft && isVisible ? `1px solid ${colors.border.secondary}` : 'none',
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
            borderBottom: `1px solid ${colors.border.vertical}`,
            backgroundColor: colors.background.white,
            minHeight: '24px',
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
                  color: colors.text.secondary,
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
                    color: colors.text.tertiary,
                    fontFamily: 'Roboto',
                    textTransform: 'capitalize',
                  }}
                >
                  {context.type === 'workflow' ? 'Automation' : context.type} Context
                </Typography>
              )}
            </Box>
          </Box>

          {onClose && (
            <Tooltip title={enableKeyboardShortcut ? 'Close (⌘K)' : 'Close'} placement='left'>
              <IconButton
                onClick={onClose}
                size='small'
                sx={{
                  color: colors.text.secondary,
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
      )}

      {/* Chat Content */}
      <Box
        sx={{
          flex: 1,
          overflow: 'hidden',
          position: 'relative',
          backgroundColor: colors.background.white,
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
                color: colors.text.tertiary,
                fontFamily: 'Roboto',
              }}
            >
              {`Please select a cluster to start chatting with ${assistantName}`}
            </Typography>
          </Box>
        )}
      </Box>
    </Box>
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
