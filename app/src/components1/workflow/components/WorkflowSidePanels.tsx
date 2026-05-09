import { useCallback, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import ChevronLeftIcon from '@mui/icons-material/ChevronLeft';
import ChevronRightIcon from '@mui/icons-material/ChevronRight';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import JsonEditorTab from './JsonEditorTab';
import { colors } from 'src/utils/colors';

interface NubiChatContext {
  type: 'workflow' | 'workflowbuilder';
  data?: any;
}

interface WorkflowSidePanelsProps {
  // NubiChat props
  showNubiChat: boolean;
  setShowNubiChat: (show: boolean) => void;
  nubiChatContext: NubiChatContext;
  nubiChatSuffix: string;
  nubiChatWindowWidth: string;
  accountId: string;

  // JSON Panel props
  jsonPanelVisible: boolean;
  setJsonPanelVisible: (visible: boolean) => void;
  jsonWindowWidth: string;
  jsonEditorText: string;
  onJsonChange: (text: string) => void;
  onJsonApply: () => Promise<void>;
  jsonValid: boolean;
  jsonParseError: string;
  jsonHasUnsavedChanges: boolean;
  isApplyingJson: boolean;
  canRevert: boolean;
  onRevert: () => void;
  loading: boolean;

  // Children for the middle content
  children: React.ReactNode;
}

export function WorkflowSidePanels({
  showNubiChat,
  setShowNubiChat,
  nubiChatContext,
  nubiChatSuffix,
  nubiChatWindowWidth: initialNubiWidth,
  accountId,
  jsonPanelVisible,
  setJsonPanelVisible,
  jsonWindowWidth: initialJsonWidth,
  jsonEditorText,
  onJsonChange,
  onJsonApply,
  jsonValid,
  jsonParseError,
  jsonHasUnsavedChanges,
  isApplyingJson,
  canRevert,
  onRevert,
  loading,
  children,
}: WorkflowSidePanelsProps) {
  const [nubiWidth, setNubiWidth] = useState(() => parseInt(initialNubiWidth) || 500);
  const [jsonWidth, setJsonWidth] = useState(() => parseInt(initialJsonWidth) || 400);

  const isResizingRef = useRef<'nubi' | 'json' | null>(null);
  const startXRef = useRef(0);
  const startWidthRef = useRef(0);

  const handleResizeMouseDown = useCallback(
    (panel: 'nubi' | 'json') => (e: React.MouseEvent) => {
      e.preventDefault();
      e.stopPropagation();
      isResizingRef.current = panel;
      startXRef.current = e.clientX;
      startWidthRef.current = panel === 'nubi' ? nubiWidth : jsonWidth;

      const handleMouseMove = (moveEvent: MouseEvent) => {
        if (!isResizingRef.current) return;
        const delta = moveEvent.clientX - startXRef.current;
        const newWidth = isResizingRef.current === 'nubi' ? startWidthRef.current + delta : startWidthRef.current - delta;
        const clampedWidth = Math.max(300, Math.min(800, newWidth));
        if (isResizingRef.current === 'nubi') {
          setNubiWidth(clampedWidth);
        } else {
          setJsonWidth(clampedWidth);
        }
      };

      const handleMouseUp = () => {
        isResizingRef.current = null;
        document.removeEventListener('mousemove', handleMouseMove);
        document.removeEventListener('mouseup', handleMouseUp);
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
        // Trigger layout recalculation so ReactFlow adjusts to new container size
        window.dispatchEvent(new Event('resize'));
      };

      document.body.style.cursor = 'col-resize';
      document.body.style.userSelect = 'none';
      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
    },
    [nubiWidth, jsonWidth]
  );

  return (
    <Box sx={{ height: '100%', width: '100%', display: 'flex', flexDirection: 'row', position: 'relative', overflow: 'hidden' }}>
      {/* NubiChat Sidebar - Sliding Panel on Left */}
      <Box
        sx={{
          position: 'absolute',
          left: showNubiChat ? '0' : `-${nubiWidth}px`,
          top: 0,
          height: '100%',
          width: `${nubiWidth}px`,
          borderRight: '1px solid rgb(226, 226, 227)',
          backgroundColor: colors.background.white,
          overflow: 'hidden',
          transition: isResizingRef.current ? 'none' : 'left 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
          zIndex: 20,
        }}
      >
        <NubiChatSidebar
          isVisible={true}
          onClose={() => setShowNubiChat(false)}
          accountId={accountId}
          position='left'
          mode='fixed'
          width={`${nubiWidth}px`}
          showHeader={true}
          context={nubiChatContext}
          querySuffix={nubiChatSuffix}
        />
        {/* Drag resize handle */}
        <Box
          onMouseDown={handleResizeMouseDown('nubi')}
          sx={{
            position: 'absolute',
            right: -3,
            top: 0,
            width: '8px',
            height: '100%',
            cursor: 'col-resize',
            zIndex: 25,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            '&::after': {
              content: '""',
              width: '2px',
              height: '32px',
              borderRadius: '2px',
              backgroundColor: 'rgba(0, 0, 0, 0.15)',
              transition: 'background-color 0.15s, height 0.15s',
            },
            '&:hover::after': {
              backgroundColor: 'rgba(59, 130, 246, 0.7)',
              height: '48px',
            },
            '&:hover': {
              backgroundColor: 'rgba(59, 130, 246, 0.08)',
            },
          }}
        />
      </Box>

      {/* NubiChat Toggle Button */}
      <Box
        onClick={() => setShowNubiChat(!showNubiChat)}
        sx={{
          position: 'absolute',
          left: showNubiChat ? `${nubiWidth}px` : '0',
          top: '50%',
          ml: '-25px',
          transform: 'translateY(-50%) rotate(-90deg)',
          transformOrigin: 'center',
          zIndex: 30,
          backgroundColor: colors.background.white,
          border: `1px solid ${colors.border.secondary}`,
          borderLeft: showNubiChat ? `1px solid ${colors.border.secondary}` : 'none',
          borderTopLeftRadius: 0,
          borderBottomLeftRadius: '8px',
          borderTopRightRadius: 0,
          borderBottomRightRadius: '8px',
          padding: '8px 12px',
          transition: isResizingRef.current ? 'none' : 'left 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          gap: '4px',
          '&:hover': {
            backgroundColor: 'rgb(249, 249, 249)',
          },
          boxShadow: '2px 0 8px rgba(0, 0, 0, 0.1)',
        }}
      >
        {showNubiChat ? (
          <ChevronLeftIcon fontSize='small' sx={{ color: colors.text.secondary, transform: 'rotate(90deg)' }} />
        ) : (
          <ChevronRightIcon fontSize='small' sx={{ color: colors.text.secondary, transform: 'rotate(90deg)' }} />
        )}
        <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary, whiteSpace: 'nowrap' }}>AI Chat</Typography>
      </Box>

      {/* Middle: Workflow Editor Content */}
      <Box
        sx={{
          height: '100%',
          flex: 1,
          marginLeft: showNubiChat ? `${nubiWidth}px` : '0',
          marginRight: jsonPanelVisible ? `${jsonWidth}px` : '0',
          position: 'relative',
          transition: isResizingRef.current
            ? 'none'
            : 'margin-left 0.3s cubic-bezier(0.4, 0, 0.2, 1), margin-right 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
        }}
      >
        {children}
      </Box>

      {/* Toggle Button for JSON Panel */}
      <Box
        onClick={() => setJsonPanelVisible(!jsonPanelVisible)}
        sx={{
          position: 'absolute',
          right: jsonPanelVisible ? `${jsonWidth}px` : '0',
          top: '50%',
          mr: '-22px',
          transform: 'translateY(-50%) rotate(90deg)',
          transformOrigin: 'center',
          zIndex: 30,
          backgroundColor: colors.background.white,
          border: `1px solid ${colors.border.secondary}`,
          borderRight: jsonPanelVisible ? `1px solid ${colors.border.secondary}` : 'none',
          borderBottomLeftRadius: '8px',
          borderTopRightRadius: 0,
          borderBottomRightRadius: '8px',
          padding: '8px 12px',
          transition: isResizingRef.current ? 'none' : 'right 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          gap: '4px',
          '&:hover': {
            backgroundColor: 'rgb(249, 249, 249)',
          },
          boxShadow: '-2px 0 8px rgba(0, 0, 0, 0.1)',
        }}
      >
        <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.secondary, whiteSpace: 'nowrap' }}>JSON</Typography>
        {jsonPanelVisible ? (
          <ChevronRightIcon fontSize='small' sx={{ color: colors.text.secondary, transform: 'rotate(-90deg)' }} />
        ) : (
          <ChevronLeftIcon fontSize='small' sx={{ color: colors.text.secondary, transform: 'rotate(-90deg)' }} />
        )}
      </Box>

      {/* Right Side: JSON Editor (sliding panel) */}
      <Box
        sx={{
          position: 'absolute',
          right: jsonPanelVisible ? '0' : `-${jsonWidth}px`,
          top: 0,
          height: 'calc(100% - 8px)',
          width: `${jsonWidth}px`,
          borderLeft: '1px solid rgb(226, 226, 227)',
          backgroundColor: 'rgb(30, 30, 30)',
          overflow: 'hidden',
          transition: isResizingRef.current ? 'none' : 'right 0.3s cubic-bezier(0.4, 0, 0.2, 1)',
          zIndex: 20,
        }}
      >
        {/* Drag resize handle */}
        <Box
          onMouseDown={handleResizeMouseDown('json')}
          sx={{
            position: 'absolute',
            left: -3,
            top: 0,
            width: '8px',
            height: '100%',
            cursor: 'col-resize',
            zIndex: 25,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            '&::after': {
              content: '""',
              width: '2px',
              height: '32px',
              borderRadius: '2px',
              backgroundColor: 'rgba(0, 0, 0, 0.15)',
              transition: 'background-color 0.15s, height 0.15s',
            },
            '&:hover::after': {
              backgroundColor: 'rgba(59, 130, 246, 0.7)',
              height: '48px',
            },
            '&:hover': {
              backgroundColor: 'rgba(59, 130, 246, 0.08)',
            },
          }}
        />
        <JsonEditorTab
          jsonText={jsonEditorText}
          onChange={onJsonChange}
          onApply={onJsonApply}
          isValid={jsonValid}
          parseError={jsonParseError}
          hasUnsavedChanges={jsonHasUnsavedChanges}
          disabled={loading || isApplyingJson}
          canRevert={canRevert}
          onRevert={onRevert}
          isLoading={isApplyingJson}
        />
      </Box>
    </Box>
  );
}

export default WorkflowSidePanels;
