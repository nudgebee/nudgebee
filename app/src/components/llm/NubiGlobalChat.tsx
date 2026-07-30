import { Box, IconButton, Typography } from '@mui/material';
import AddIcon from '@mui/icons-material/Add';
import HistoryIcon from '@mui/icons-material/History';
import CloseIcon from '@mui/icons-material/Close';
import FullscreenIcon from '@mui/icons-material/Fullscreen';
import FullscreenExitIcon from '@mui/icons-material/FullscreenExit';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import { useEffect, useMemo, useState, useRef } from 'react';
import { createPortal } from 'react-dom';
import CustomDrawer from '@shared/CustomDrawer';
import SafeIcon from '@shared/icons/SafeIcon';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import NubiWatchingIcon from '@shared/NubiWatchingIcon';
import Chip from '@ui/Chip';
import Tooltip from '@ui/Tooltip';
import KubernetesLLMResponseGenerator from '@components/llm/KubernetesLLMResponseGeneratorV2';
import { useTenantBranding, useBrandingConfig } from '@hooks/useTenantBranding';
import { useData } from '@context/DataContext';
import { useUpdateAllClusterOption } from '@shared/layout/UpdateDataContext';
import { useNubiGlobalChat, NUBI_GLOBAL_CHAT_SHORTCUT_LABEL } from '@context/NubiGlobalChatContext';
import { buildNubiWelcomeTemplates } from '@components/llm/NubiWelcome';
import { ds } from '@utils/colors';

const LAUNCHER_SIZE = 56;

const ACCOUNT_SCOPE_HINT = 'Scoped to this account — from the page, or your last visit. Change it from the page.';

const NubiGlobalChat: React.FC = () => {
  const { isOpen, open, close, wipCount, completedWhileClosed, accountId } = useNubiGlobalChat();
  const { assistantName, nubiIconUrl } = useTenantBranding();
  // The animated mascot is Nudgebee-specific — white-label tenants keep their
  // branded icon. Gate on `loading` too so the mascot never flashes before the
  // branding config resolves.
  const { isWhiteLabel, loading: brandingLoading } = useBrandingConfig();

  const welcomeTemplates = useMemo(() => buildNubiWelcomeTemplates(assistantName || 'Nubi'), [assistantName]);
  const [welcomeIdx, setWelcomeIdx] = useState(0);
  const welcomeMessage = welcomeTemplates[welcomeIdx % welcomeTemplates.length];

  const [newChatSignal, setNewChatSignal] = useState<number | undefined>(undefined);
  const [historySignal, setHistorySignal] = useState<number | undefined>(undefined);
  const [isExpanded, setIsExpanded] = useState(false);
  const historyButtonRef = useRef<HTMLButtonElement>(null);

  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    setMounted(true);
  }, []);

  // The account label shown in the header. `allCluster` is only populated by the
  // header cluster dropdown, which the all-accounts pages (/troubleshoot,
  // /optimise) don't render — precisely where the scope is least obvious — so
  // fetch it on first open. Deferred to open (not mount) to keep page loads
  // untouched; it lands once per session.
  const { allCluster } = useData();
  const updateAllClusters = useUpdateAllClusterOption();
  useEffect(() => {
    if (isOpen && allCluster == null) {
      updateAllClusters();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen, allCluster]);

  const account = useMemo(() => (accountId ? (allCluster || []).find((c: any) => c.value === accountId) : null), [allCluster, accountId]);

  const [showReadySlate, setShowReadySlate] = useState(false);
  useEffect(() => {
    if (!completedWhileClosed) {
      return;
    }
    setShowReadySlate(true);
    const timer = setTimeout(() => setShowReadySlate(false), 6000);
    return () => clearTimeout(timer);
  }, [completedWhileClosed]);

  if (!mounted) {
    return null;
  }

  const hasWip = wipCount > 0;
  const hasCompleted = completedWhileClosed;

  const launcherTooltip = (
    <Box sx={{ display: 'flex', alignItems: 'flex-end', gap: 'var(--ds-space-3)', py: 'var(--ds-space-1)', minWidth: ds.space.mul(1, 40) }}>
      <Typography
        sx={{
          flex: 1,
          fontSize: 'var(--ds-text-body)',
          color: ds.gray[700],
          fontFamily: ds.font.sans,
          lineHeight: 1.5,
        }}
      >
        {welcomeMessage}
      </Typography>
      <Box
        component='kbd'
        sx={{
          flexShrink: 0,
          fontFamily: ds.font.sans,
          fontSize: 'var(--ds-text-caption)',
          fontWeight: 'var(--ds-font-weight-medium)',
          color: ds.gray[600],
          backgroundColor: ds.gray[100],
          border: `1px solid ${ds.gray[200]}`,
          borderRadius: 'var(--ds-radius-sm)',
          px: 'var(--ds-space-1)',
          lineHeight: 1.6,
        }}
      >
        {NUBI_GLOBAL_CHAT_SHORTCUT_LABEL}
      </Box>
    </Box>
  );

  const launcher =
    !isOpen &&
    createPortal(
      <Box
        sx={{
          position: 'fixed',
          bottom: 'var(--ds-space-6)',
          right: 'var(--ds-space-6)',
          zIndex: 1250,
        }}
      >
        <Tooltip title={launcherTooltip} placement='left'>
          <Box
            component='button'
            type='button'
            onClick={open}
            onMouseEnter={() => setWelcomeIdx((i) => (i + 1) % welcomeTemplates.length)}
            aria-label={`Open ${assistantName}`}
            data-testid='nubi-global-chat-launcher'
            sx={{
              position: 'relative',
              width: LAUNCHER_SIZE,
              height: LAUNCHER_SIZE,
              borderRadius: '50%',
              border: `1px solid ${hasCompleted ? ds.green[400] : ds.brand[200]}`,
              backgroundColor: hasCompleted ? ds.green[100] : ds.background[100],
              boxShadow: `0 ${ds.space[1]} 16px ${ds.gray.alpha[300]}`,
              cursor: 'pointer',
              display: 'grid',
              placeItems: 'center',
              padding: 0,
              transition: 'transform 0.15s ease, box-shadow 0.15s ease, background-color 0.2s ease, border-color 0.2s ease',
              '&:hover': {
                boxShadow: `0 ${ds.space[2]} 22px ${ds.gray.alpha[300]}`,
              },
              // On hover the icon grows up from its bottom edge, poking out above the circle.
              '&:hover .nb-launcher-icon': {
                transform: 'scale(1.375)',
              },
              ...(hasCompleted
                ? {
                    '&::after': {
                      content: '""',
                      position: 'absolute',
                      inset: 0,
                      borderRadius: '50%',
                      border: `2px solid ${ds.green[400]}`,
                      animation: 'nubiGlobalChatDoneRing 1.6s ease-out infinite',
                    },
                    '@keyframes nubiGlobalChatDoneRing': {
                      '0%': { transform: 'scale(1)', opacity: 0.7 },
                      '100%': { transform: 'scale(1.7)', opacity: 0 },
                    },
                  }
                : hasWip
                ? {
                    '&::after': {
                      content: '""',
                      position: 'absolute',
                      inset: 0,
                      borderRadius: '50%',
                      border: `2px solid ${ds.brand[400]}`,
                      animation: 'nubiGlobalChatWipRing 1.6s ease-out infinite',
                    },
                    '@keyframes nubiGlobalChatWipRing': {
                      '0%': { transform: 'scale(1)', opacity: 0.6 },
                      '100%': { transform: 'scale(1.8)', opacity: 0 },
                    },
                  }
                : {}),
            }}
          >
            <Box
              className='nb-launcher-icon'
              sx={{
                display: 'grid',
                placeItems: 'center',
                transformOrigin: 'bottom center',
                transition: 'transform 0.25s cubic-bezier(0.34, 1.56, 0.64, 1)',
              }}
            >
              {isWhiteLabel || brandingLoading ? (
                <Box
                  sx={{
                    display: 'grid',
                    placeItems: 'center',
                    ...(hasCompleted
                      ? {
                          animation: 'nubiGlobalChatDoneWiggle 1s ease-in-out infinite',
                          '@keyframes nubiGlobalChatDoneWiggle': {
                            '0%, 100%': { transform: 'rotate(0deg) scale(1)' },
                            '20%': { transform: 'rotate(-9deg) scale(1.1)' },
                            '60%': { transform: 'rotate(9deg) scale(1.1)' },
                          },
                        }
                      : {
                          animation: 'nubiGlobalChatIdleLook 5.5s ease-in-out infinite',
                          '@keyframes nubiGlobalChatIdleLook': {
                            '0%, 62%, 100%': { transform: 'translateX(0) rotate(0deg)' },
                            '16%': { transform: 'translateX(-1.5px) rotate(-6deg)' },
                            '31%': { transform: 'translateX(0) rotate(0deg)' },
                            '46%': { transform: 'translateX(1.5px) rotate(6deg)' },
                          },
                        }),
                  }}
                >
                  <Box
                    sx={{
                      display: 'grid',
                      placeItems: 'center',
                      ...(!hasCompleted && {
                        animation: 'nubiGlobalChatIdleBlink 4s ease-in-out infinite',
                        '@keyframes nubiGlobalChatIdleBlink': {
                          '0%, 88%, 100%': { transform: 'scaleY(1)' },
                          '92%': { transform: 'scaleY(0.8)' },
                          '96%': { transform: 'scaleY(1)' },
                        },
                      }),
                    }}
                  >
                    <SafeIcon src={nubiIconUrl} alt={assistantName} width={40} height={40} />
                  </Box>
                </Box>
              ) : (
                <Box
                  sx={{
                    display: 'grid',
                    placeItems: 'center',
                    ...(hasCompleted && {
                      animation: 'nubiGlobalChatDoneWiggle 1s ease-in-out infinite',
                      '@keyframes nubiGlobalChatDoneWiggle': {
                        '0%, 100%': { transform: 'rotate(0deg) scale(1)' },
                        '20%': { transform: 'rotate(-9deg) scale(1.1)' },
                        '60%': { transform: 'rotate(9deg) scale(1.1)' },
                      },
                    }),
                  }}
                >
                  <NubiWatchingIcon size={40} ariaLabel={assistantName} />
                </Box>
              )}
            </Box>
          </Box>
        </Tooltip>

        {hasCompleted ? (
          <Tooltip title={`${assistantName} finished — open to view`} placement='left'>
            <Box
              aria-label={`${assistantName} finished responding`}
              data-testid='nubi-global-chat-done-badge'
              sx={{
                position: 'absolute',
                top: ds.space.mul(1, -1),
                right: ds.space.mul(1, -1),
                width: ds.space[5],
                height: ds.space[5],
                borderRadius: ds.radius.pill,
                backgroundColor: ds.green[600],
                color: ds.background[100],
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                lineHeight: 1,
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                boxShadow: `0 1px 4px ${ds.gray.alpha[300]}`,
              }}
            >
              ✓
            </Box>
          </Tooltip>
        ) : hasWip ? (
          <Tooltip title={`${wipCount} chat${wipCount > 1 ? 's' : ''} in progress`} placement='left'>
            <Box
              aria-label={`${wipCount} chats in progress`}
              data-testid='nubi-global-chat-wip-badge'
              sx={{
                position: 'absolute',
                top: ds.space.mul(1, -1),
                right: ds.space.mul(1, -1),
                minWidth: ds.space[5],
                height: ds.space[5],
                px: ds.space[1],
                borderRadius: ds.radius.pill,
                backgroundColor: ds.brand[600],
                color: ds.background[100],
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                boxShadow: `0 1px 4px ${ds.gray.alpha[300]}`,
                animation: 'nubiGlobalChatWipPulse 1.6s ease-in-out infinite',
                '@keyframes nubiGlobalChatWipPulse': {
                  '0%, 100%': { transform: 'scale(1)' },
                  '50%': { transform: 'scale(1.12)' },
                },
              }}
            >
              {wipCount}
            </Box>
          </Tooltip>
        ) : null}

        {hasCompleted && showReadySlate && (
          <Box
            sx={{
              position: 'absolute',
              top: '50%',
              right: `calc(100% + ${ds.space[2]})`,
              transform: 'translateY(-50%)',
              pointerEvents: 'none',
            }}
          >
            <Box
              data-testid='nubi-global-chat-ready-slate'
              sx={{
                transformOrigin: 'right center',
                animation: 'nubiGlobalChatSlateInOut 6s ease forwards',
                display: 'inline-block',
                whiteSpace: 'nowrap',
                px: 'var(--ds-space-4)',
                py: 'var(--ds-space-2)',
                borderRadius: ds.radius.lg,
                backgroundColor: ds.background[100],
                border: `1px solid ${ds.gray[400]}`,
                color: ds.gray[700],
                fontFamily: ds.font.sans,
                fontStyle: 'italic',
                fontSize: 'var(--ds-text-body)',
                fontWeight: 'var(--ds-font-weight-medium)',
                lineHeight: 1.2,
                '@keyframes nubiGlobalChatSlateInOut': {
                  '0%': { opacity: 0, transform: 'translateX(12px) scale(0.6)' },
                  '6%': { opacity: 1, transform: 'translateX(0) scale(1)' },
                  '90%': { opacity: 1, transform: 'translateX(0) scale(1)' },
                  '100%': { opacity: 0, transform: 'translateX(12px) scale(0.6)' },
                },
              }}
            >
              {'Your answer is ready'}
            </Box>
          </Box>
        )}
      </Box>,
      document.body
    );

  // Open the full Nubi page in a new tab — carry the current account + active
  // chat so the detailed page continues the same conversation.
  const openDetailPage = () => {
    if (typeof window === 'undefined') {
      return;
    }
    const sessionId = localStorage.getItem('nubi_selected_conversation_id');
    const params = new URLSearchParams();
    if (accountId) {
      params.set('accountId', accountId);
    }
    if (sessionId) {
      params.set('session_id', sessionId);
    }
    const qs = params.toString();
    window.open(`/ask-nudgebee${qs ? `?${qs}` : ''}`, '_blank', 'noopener,noreferrer');
  };

  const drawerHeader = (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        padding: 'var(--ds-space-2) var(--ds-space-3)',
        borderBottom: `1px solid ${ds.gray[200]}`,
        backgroundColor: ds.background[100],
        minHeight: ds.space.mul(0, 12),
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0 }}>
        <Tooltip title='Open full page' placement='bottom'>
          <Box
            role='button'
            tabIndex={0}
            onClick={openDetailPage}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                openDetailPage();
              }
            }}
            aria-label={`Open ${assistantName} full page`}
            data-testid='nubi-global-chat-open-detail'
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--ds-space-2)',
              minWidth: 0,
              cursor: 'pointer',
              borderRadius: ds.radius.md,
              px: 'var(--ds-space-1)',
              mx: 'calc(-1 * var(--ds-space-1))',
              py: 'var(--ds-space-1)',
              transition: 'background-color 0.15s ease',
              '&:hover': { backgroundColor: ds.gray[100] },
            }}
          >
            <SafeIcon src={nubiIconUrl} alt={assistantName} width={28} height={28} />
            <Typography
              sx={{
                fontSize: 'var(--ds-text-title)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: ds.brand[500],
                fontFamily: ds.font.sans,
                whiteSpace: 'nowrap',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
              }}
            >
              {assistantName}
            </Typography>
          </Box>
        </Tooltip>

        {account && (
          <Box data-testid='nubi-global-chat-account' sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', minWidth: 0 }}>
            <Chip
              variant='tag'
              tone='info'
              size='xs'
              icon={<CloudProviderIcon cloud_provider={account.cloud_provider} width='12px' height='12px' />}
              displayTooltip
              tooltipCharLimit={18}
            >
              {account.label}
            </Chip>
            <Tooltip title={ACCOUNT_SCOPE_HINT} placement='bottom'>
              <InfoOutlinedIcon aria-label='How this account is chosen' sx={{ fontSize: 14, color: ds.gray[400], cursor: 'help', flexShrink: 0 }} />
            </Tooltip>
          </Box>
        )}
      </Box>

      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
        <Tooltip title='New chat' placement='bottom'>
          <IconButton
            size='small'
            onClick={() => setNewChatSignal((n) => (n ?? 0) + 1)}
            aria-label='New chat'
            data-testid='nubi-global-chat-new-chat'
            sx={{ color: ds.brand[500] }}
          >
            <AddIcon fontSize='small' />
          </IconButton>
        </Tooltip>
        <Tooltip title='Your Chats' placement='bottom'>
          <IconButton
            ref={historyButtonRef}
            size='small'
            onClick={() => setHistorySignal((n) => (n ?? 0) + 1)}
            aria-label='Your Chats'
            data-testid='nubi-global-chat-history'
            sx={{ color: ds.brand[500] }}
          >
            <HistoryIcon fontSize='small' />
          </IconButton>
        </Tooltip>
        <Tooltip title={isExpanded ? 'Collapse' : 'Expand'} placement='bottom'>
          <IconButton
            size='small'
            onClick={() => setIsExpanded(!isExpanded)}
            aria-label={isExpanded ? 'Collapse' : 'Expand'}
            data-testid='nubi-global-chat-expand'
            sx={{ color: ds.brand[500] }}
          >
            {isExpanded ? <FullscreenExitIcon fontSize='small' /> : <FullscreenIcon fontSize='small' />}
          </IconButton>
        </Tooltip>
        <Tooltip title={`Close (${NUBI_GLOBAL_CHAT_SHORTCUT_LABEL})`} placement='bottom'>
          <IconButton size='small' onClick={close} aria-label='Close' data-testid='nubi-global-chat-close' sx={{ color: ds.brand[500] }}>
            <CloseIcon fontSize='small' />
          </IconButton>
        </Tooltip>
      </Box>
    </Box>
  );

  const drawer = (
    <CustomDrawer
      open={isOpen}
      onClose={close}
      width={isExpanded ? '50vw' : '460px'}
      nonModal
      bare
      variant='modern'
      storageKey='nb.nubiGlobalChat.width'
    >
      <Box
        // Signals other global shortcut handlers (e.g. Cmd/Ctrl+K page search) that the chat is open.
        data-nubi-chat-open={isOpen || undefined}
        sx={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden', backgroundColor: ds.background[100] }}
      >
        {drawerHeader}
        <Box sx={{ flex: 1, minHeight: 0, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
          {accountId ? (
            <KubernetesLLMResponseGenerator
              accountId={accountId}
              popup
              showBorder={false}
              source='ask_nudgbee_chat'
              newChatSignal={newChatSignal as any}
              historySignal={historySignal as any}
              historyButtonRef={historyButtonRef as any}
              drawerIsOpen={isOpen}
            />
          ) : (
            <Box
              sx={{ display: 'flex', alignItems: 'center', justifyContent: 'center', height: '100%', p: 'var(--ds-space-5)', textAlign: 'center' }}
            >
              <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: ds.gray[600], fontFamily: ds.font.sans }}>
                {`Please select a cluster to start chatting with ${assistantName}`}
              </Typography>
            </Box>
          )}
        </Box>
      </Box>
    </CustomDrawer>
  );

  return (
    <>
      {launcher}
      {drawer}
    </>
  );
};

export default NubiGlobalChat;
