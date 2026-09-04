/**
 * NubiBrainNav — the "b-Cortex" / "AI & Tools" buttons shown at the bottom
 * of a left navigation rail. Self-contained: it owns BCortexModal and
 * AIToolsModal.
 *
 * "AI & Tools" repurposes what used to be the rail's only "Settings" button
 * (docs/ia-consolidation-plan.md, PR 3) — same content Admin's own AI & Tools
 * tab shows, at modal density, admin-gated (Decision T), surface='light'-only
 * (Decision W — the main docked sidebar already has its own "Admin" entry
 * for this content, so a second copy there was redundant).
 *
 * A non-admin user on a tenant without b-Cortex (OSS, or not yet migrated to
 * MEMORY_MODULE) has no rail path to Memory / Account Context — both live
 * inside the admin-gated AI & Tools content only (Decision Y). A dedicated,
 * non-admin-gated "Memory" button was tried and reverted (Decision AB): the
 * narrowing is an accepted trade-off, not a regression to mitigate.
 *
 * Surface tinting:
 *   • surface='dark'  — white icons/labels for the main app rail (brand-600 bg).
 *   • surface='light' — grey icons/labels for the Ask-Nubi rail (light bg).
 */
import { useCallback, useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import dynamic from 'next/dynamic';
import { useRouter } from 'next/router';
import { Box, ButtonBase, Typography } from '@mui/material';
import PsychologyOutlined from '@mui/icons-material/PsychologyOutlined';
import AutoAwesomeIcon from '@mui/icons-material/AutoAwesome';
import SafeIcon from '@components/common/icons/SafeIcon';
import { isOSSDeploymentMode } from '@hooks/useBCortexEnabled';
import { hasAdminSurfaceAccess } from '@lib/auth';

// These modals are click-gated (default closed) and client-only, but this nav rail
// is rendered by the global PageLayout on every authenticated route. Importing them
// statically pulled their entire transitive tree — reactflow, elkjs, xterm,
// codemirror — into the shared layout chunk on *every* page, including /home, where
// none of it is used. next/dynamic + the mount latch below keep that code out of the
// initial bundle and fetch it on first open instead. See enterprise#25990.
//
// AI & Tools — the repurposed rail button's own content, same
// AI_TOOLS_SUB_TABS Admin's own page renders (aiToolsConfig.js). No @ee/
// reference in this file or AIToolsModal.jsx (each of AI & Tools' leaf
// components handles its own EE dynamic-import internally), so no
// OSS-STRIP marker is needed here.
const AIToolsModal = dynamic(() => import('@components/llm/admin/AIToolsModal'), { ssr: false });
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
    <SafeIcon src={iconComponent} alt='' width={20} height={20} sx={iconSx} />
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

const NubiBrainNav = ({ surface = 'dark', accountId }) => {
  const router = useRouter();
  const resolvedAccountId = accountId ?? router.query?.accountId ?? '';

  const [openAIToolsModal, setOpenAIToolsModal] = useState(false);
  const [openBCortexModal, setOpenBCortexModal] = useState(false);
  // Driven by a ?bcortex=<tab> deep link. The weekly digest is delivered to
  // notification channels, and its "view the full review" link has to land
  // on the Digests tab — b-Cortex is a modal, so there is no route to point at.
  const [bcortexInitialTab, setBcortexInitialTab] = useState(null);

  // Mount latches: a dynamically-imported modal only fetches its chunk once it is
  // actually rendered. We latch on first open so the chunk loads on the user's first
  // click (not on page load), then stay mounted so the `open` prop keeps driving the
  // show/hide transition exactly as before.
  const [aiToolsMounted, setAiToolsMounted] = useState(false);
  const [bcortexMounted, setBcortexMounted] = useState(false);

  const handleOpenAITools = useCallback(() => {
    setAiToolsMounted(true);
    setOpenAIToolsModal(true);
  }, []);
  const handleOpenBCortex = useCallback(() => {
    setBcortexMounted(true);
    setOpenBCortexModal(true);
  }, []);
  const handleCloseBCortex = useCallback(() => {
    setOpenBCortexModal(false);
    setBcortexInitialTab(null);
  }, []);

  // Only the tabs we actually mint links to. Deliberately not the full tab list
  // from BCortexModal: even though that component's own resolveTabState now
  // falls back to a sane default (Memory → Patterns) for an unrecognised id
  // rather than the wrong tab, a stale or mistyped link should still do
  // nothing rather than silently open something. Kept here rather than
  // exported from the modal so this stays a link allowlist, not a second
  // copy of the registry.
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

  const tint = surface === 'dark' ? 'var(--ds-background-100)' : 'var(--ds-gray-600)';

  return (
    <>
      {aiToolsMounted && <AIToolsModal open={openAIToolsModal} onClose={() => setOpenAIToolsModal(false)} />}
      {bcortexMounted && (
        <BCortexModal open={openBCortexModal} onClose={handleCloseBCortex} accountId={resolvedAccountId} initialTab={bcortexInitialTab} />
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
        {/* Decision T: admin-gated like every other Admin tab. Decision W:
            surface='light'-only — the main docked sidebar already has its
            own "Admin" entry for this content, so a second copy there was
            redundant. */}
        {hasAdminSurfaceAccess() && surface === 'light' && (
          <RailButton
            label='AI & Tools'
            testId='nav-nubi-ai-tools-btn'
            iconComponent={AutoAwesomeIcon}
            iconSx={{ color: tint }}
            color={tint}
            onClick={handleOpenAITools}
          />
        )}
      </Box>
    </>
  );
};

NubiBrainNav.propTypes = {
  surface: PropTypes.oneOf(['dark', 'light']),
  accountId: PropTypes.string,
};

export default NubiBrainNav;
