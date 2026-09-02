/**
 * NubiBrainNav — the "B-Cortex" + "Settings" button pair shown at the bottom of
 * a left navigation rail. Self-contained: it owns both the BCortexModal and the
 * SettingsModal, and (when an agent list isn't supplied by the parent) lazily
 * fetches the agents the Settings modal needs the first time it's opened.
 *
 * Surface tinting:
 *   • surface='dark'  — white icons/labels for the main app rail (brand-600 bg).
 *   • surface='light' — grey icons/labels for the Ask-Nubi rail (light bg).
 *
 * Agent source:
 *   • Pass `agents` (+ `loadingAgents` / `onRefreshAgents`) to reuse a list the
 *     parent already maintains (Ask-Nubi layout does this).
 *   • Omit them to let this component fetch its own list using `accountId`
 *     (falls back to the route's accountId) — used by the global main nav.
 */
import { useCallback, useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import dynamic from 'next/dynamic';
import { useRouter } from 'next/router';
import { Box, ButtonBase, Typography } from '@mui/material';
import PsychologyOutlined from '@mui/icons-material/PsychologyOutlined';
import SettingsOutlined from '@mui/icons-material/SettingsOutlined';
import SafeIcon from '@components/common/icons/SafeIcon';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { isOSSDeploymentMode } from '@hooks/useBCortexEnabled';

// Both modals are click-gated (default closed) and client-only, but this nav rail
// is rendered by the global PageLayout on every authenticated route. Importing the
// modals statically pulled their entire transitive tree — reactflow, elkjs, xterm,
// codemirror — into the shared layout chunk on *every* page, including /home, where
// none of it is used. next/dynamic + the mount latch below keep that code out of the
// initial bundle and fetch it on first open instead. See enterprise#25990.
const SettingsModal = dynamic(() => import('@components/llm/SettingsModal'), { ssr: false });
// BCortexModal is stripped from the OSS snapshot (see .oss-exclude).
// The marker-delimited line below is range-replaced with a () => null
// stub by scripts/oss-patches.sh — Turbopack's static analysis rejects
// dynamic imports of missing modules at build time, so a runtime
// .catch() fallback isn't enough.
const BCortexModal = () => null;

const RailButton = ({ label, testId, iconComponent, iconSx, color, onClick }) => (
  <ButtonBase
    component='div'
    data-testid={testId}
    aria-label={label}
    onClick={onClick}
    sx={{
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'center',
      gap: 'var(--ds-space-1)',
      width: '100%',
      py: 'var(--ds-space-1)',
      cursor: 'pointer',
      '&:hover': { bgcolor: 'rgba(255, 255, 255, 0.08)' },
      '&:focus-visible': { outline: '2px solid var(--ds-brand-400)', outlineOffset: '2px' },
    }}
  >
    <SafeIcon src={iconComponent} width={20} height={20} sx={iconSx} />
    <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)', color, textAlign: 'center', lineHeight: 1 }}>
      {label}
    </Typography>
  </ButtonBase>
);

RailButton.propTypes = {
  label: PropTypes.string.isRequired,
  testId: PropTypes.string,
  iconComponent: PropTypes.elementType.isRequired,
  iconSx: PropTypes.object,
  color: PropTypes.string,
  onClick: PropTypes.func,
};

const NubiBrainNav = ({ surface = 'dark', accountId, agents = null, loadingAgents = false, onRefreshAgents }) => {
  const router = useRouter();
  const resolvedAccountId = accountId ?? router.query?.accountId ?? '';

  const [openSettingsModal, setOpenSettingsModal] = useState(false);
  const [openBCortexModal, setOpenBCortexModal] = useState(false);
  // Set when Settings is opened programmatically (e.g. from b-Cortex's
  // BCortexDisabled placeholder) so Settings starts on a specific tab.
  // Reset on close so the next normal open lands on the default 'agents' tab.
  const [settingsInitialTab, setSettingsInitialTab] = useState(null);
  // Same idea for b-Cortex, driven by a ?bcortex=<tab> deep link. The weekly
  // weekly digest is delivered to notification channels, and its "view the
  // full review" link has to land on the Digests tab — b-Cortex is a modal, so
  // there is no route to point at.
  const [bcortexInitialTab, setBcortexInitialTab] = useState(null);

  // Mount latches: a dynamically-imported modal only fetches its chunk once it is
  // actually rendered. We latch on first open so the chunk loads on the user's first
  // click (not on page load), then stay mounted so the `open` prop keeps driving the
  // show/hide transition exactly as before.
  const [settingsMounted, setSettingsMounted] = useState(false);
  const [bcortexMounted, setBcortexMounted] = useState(false);

  const handleOpenSettings = useCallback(() => {
    setSettingsMounted(true);
    setOpenSettingsModal(true);
  }, []);
  const handleOpenBCortex = useCallback(() => {
    setBcortexMounted(true);
    setOpenBCortexModal(true);
  }, []);
  const handleCloseBCortex = useCallback(() => {
    setOpenBCortexModal(false);
    setBcortexInitialTab(null);
  }, []);
  // Cross-modal hand-off used by BCortexDisabled → "View legacy memory
  // layer". Closes b-Cortex and opens Settings on its Memory tab; when
  // MEMORY_MODULE is disabled the Memory tab renders the legacy MemoryTab
  // inline rather than the b-Cortex redirect panel.
  const handleOpenSettingsMemory = useCallback(() => {
    setOpenBCortexModal(false);
    setSettingsMounted(true);
    setSettingsInitialTab('memory');
    setOpenSettingsModal(true);
  }, []);

  // Only the tabs we actually mint links to. Deliberately not the full tab list
  // from BCortexModal: an unrecognised value falls through that component's
  // switch to Knowledge Graph, so a stale or mistyped link would quietly open
  // the wrong tab instead of doing nothing. Kept here rather than exported from
  // the modal so this stays a link allowlist, not a second copy of the registry.
  const DEEP_LINKABLE_TABS = ['digests'];

  useEffect(() => {
    if (!router.isReady) return;
    const tab = router.query?.bcortex;
    if (typeof tab !== 'string' || !DEEP_LINKABLE_TABS.includes(tab)) return;

    setBcortexInitialTab(tab);
    setBcortexMounted(true);
    setOpenBCortexModal(true);

    // Drop the param so a refresh (or a back-navigation) doesn't reopen the
    // modal over whatever the user moved on to. shallow: the page's own data
    // does not depend on it, so there is nothing to re-fetch.
    const { bcortex: _consumed, ...rest } = router.query;
    router.replace({ pathname: router.pathname, query: rest }, undefined, { shallow: true });
    // router identity changes on every navigation; keying on the param alone
    // keeps this from re-firing (and re-opening) on unrelated route changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [router.isReady, router.query?.bcortex]);

  // When the parent passes an agent list we mirror it; otherwise self-fetch.
  const usingExternalAgents = Array.isArray(agents);
  const [internalAgents, setInternalAgents] = useState([]);
  const [internalLoading, setInternalLoading] = useState(false);

  const effectiveAgents = usingExternalAgents ? agents : internalAgents;
  const effectiveLoading = usingExternalAgents ? !!loadingAgents : internalLoading;

  const fetchAgents = useCallback(() => {
    if (usingExternalAgents) {
      onRefreshAgents?.();
      return;
    }
    // Empty resolvedAccountId is valid: backend's agentListAgent routes it
    // to ListAgentsForTenant — system catalog + every custom agent the
    // caller can read across the tenant. The Settings/b-Cortex modals
    // open from the global sidebar with no account in scope, so we must
    // fire the request regardless.
    setInternalLoading(true);
    apiAskNudgebee
      .listAgents({ accountId: resolvedAccountId })
      .then((res) => {
        setInternalAgents(res?.data?.data?.ai_list_agents?.data ?? []);
        setInternalLoading(false);
      })
      .catch(() => setInternalLoading(false));
  }, [usingExternalAgents, onRefreshAgents, resolvedAccountId]);

  // Lazily load agents the first time Settings is opened (self-fetch mode only).
  // resolvedAccountId is in the dep list because fetchAgents bails when it's
  // empty — if the modal opens before the account id has hydrated, the first
  // call returns early; we need to re-fire as soon as the id arrives.
  // fetchAgents itself is intentionally omitted: it'd re-run on every
  // callback identity change and re-trigger after a successful load, since
  // internalAgents.length transitions to non-zero would still satisfy the
  // guard on the immediately-next render only if openSettingsModal flips.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (openSettingsModal && !usingExternalAgents && internalAgents.length === 0 && !internalLoading) {
      fetchAgents();
    }
  }, [openSettingsModal, resolvedAccountId]);

  const tint = surface === 'dark' ? 'var(--ds-background-100)' : 'var(--ds-gray-600)';

  return (
    <>
      {settingsMounted && (
        <SettingsModal
          open={openSettingsModal}
          onClose={() => {
            setOpenSettingsModal(false);
            setSettingsInitialTab(null);
          }}
          accountId={resolvedAccountId}
          allAgents={effectiveAgents}
          refreshAgentListing={fetchAgents}
          loadingAgents={effectiveLoading}
          onOpenBCortex={handleOpenBCortex}
          initialTab={settingsInitialTab}
        />
      )}
      {bcortexMounted && (
        <BCortexModal
          open={openBCortexModal}
          onClose={handleCloseBCortex}
          accountId={resolvedAccountId}
          onOpenSettingsMemory={handleOpenSettingsMemory}
          initialTab={bcortexInitialTab}
        />
      )}

      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: 'var(--ds-space-2)',
        }}
      >
        {!isOSSDeploymentMode() && (
          <RailButton
            label='b-Cortex'
            testId='nav-bcortex-btn'
            iconComponent={PsychologyOutlined}
            iconSx={{
              color: 'var(--ds-yellow-500)',
              // Subtle looping "shine" so the icon gently draws attention in the rail.
              animation: 'bcortexShine 8s ease-in-out infinite',
              '@keyframes bcortexShine': {
                '0%, 100%': { filter: 'drop-shadow(0 0 0px var(--ds-yellow-500))' },
                '50%': { filter: 'drop-shadow(0 0 3px var(--ds-yellow-500))' },
              },
              '@media (prefers-reduced-motion: reduce)': { animation: 'none' },
            }}
            color={tint}
            onClick={handleOpenBCortex}
          />
        )}
        <RailButton
          label='Settings'
          testId='nav-nubi-settings-btn'
          iconComponent={SettingsOutlined}
          iconSx={{ color: tint }}
          color={tint}
          onClick={handleOpenSettings}
        />
      </Box>
    </>
  );
};

NubiBrainNav.propTypes = {
  surface: PropTypes.oneOf(['dark', 'light']),
  accountId: PropTypes.string,
  agents: PropTypes.array,
  loadingAgents: PropTypes.bool,
  onRefreshAgents: PropTypes.func,
};

export default NubiBrainNav;
