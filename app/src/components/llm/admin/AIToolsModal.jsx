/**
 * AIToolsModal — the Nubi rail's compact-density mount of AI & Tools
 * (docs/ia-consolidation-plan.md, PR 3). Repurposes the rail's old
 * "Settings" button per the plan's mirrors rule: "One component, two
 * mounts... pass a context prop that governs density and available
 * actions." The 9 sub-tabs and their gating come from AI_TOOLS_SUB_TABS
 * (aiToolsConfig.js) — the same source Admin's own page
 * (pages/user-management/index.jsx) builds its tabOptions from — so the
 * two mounts can't drift apart on which tabs exist or what gates them.
 * Only the chrome differs: Admin's page routes through AnchorComponent
 * (hash routing, hasAdminSurfaceAccess() page guard); this modal owns a
 * lighter internal Tabs strip, same pattern BCortexModal already uses for
 * b-Cortex's own tab groups.
 */
import { useEffect, useMemo, useState } from 'react';
import PropTypes from 'prop-types';
import { useSession } from 'next-auth/react';
import { Box } from '@mui/material';
import AutoAwesomeIcon from '@mui/icons-material/AutoAwesome';
import { Modal } from '@ui/Modal';
import Tabs from '@shared/navigation/Tabs';
import ErrorBoundary from '@shared/ErrorBoundary';
import { missingPermissionMessage, isUiFeatureEnabled } from '@lib/auth';
import { useBCortexEnabled } from '@hooks/useBCortexEnabled';
import { useFeatureAccess } from '@hooks/useFeatureAccess';
import { ds } from '@utils/colors';
import { AI_TOOLS_SUB_TABS } from './aiToolsConfig';

const AIToolsModal = ({ open, onClose }) => {
  const { data: session } = useSession();
  // Gates the legacyOnly sub-tabs (Memory, Account Context) — see
  // aiToolsConfig.js. `open` keeps the lookup active only while the modal
  // can actually be seen, same as BCortexModal's own useBCortexEnabled(open).
  const bcortexEnabled = useBCortexEnabled(open);
  // Gates the Functions sub-tab (requiresFeature) — same
  // hasFeatureAccess('LLM_FUNCTION') check Settings used.
  const llmFunctionEnabled = useFeatureAccess('LLM_FUNCTION');
  // `bcortexEnabled !== false` (true or still resolving) hides the
  // legacyOnly tabs by default, same reasoning as user-management/index.jsx.
  // requiresFeature only ever names 'LLM_FUNCTION' today (see aiToolsConfig.js).
  const visibleSubTabs = useMemo(
    () =>
      AI_TOOLS_SUB_TABS.filter((t) => {
        if (t.legacyOnly && bcortexEnabled !== false) {
          return false;
        }
        if (t.requiresFeature && llmFunctionEnabled !== true) {
          return false;
        }
        if (t.requiresUiFeature && !isUiFeatureEnabled(t.requiresUiFeature)) {
          return false;
        }
        return true;
      }),
    [bcortexEnabled, llmFunctionEnabled]
  );
  const [activeTab, setActiveTab] = useState(AI_TOOLS_SUB_TABS[0].id);

  // The active tab's own gate (legacyOnly/requiresFeature/requiresUiFeature)
  // can flip false after an async check resolves while it's selected — reset
  // to the first still-visible tab so the strip's highlighted tab and the
  // rendered body never disagree. Same pattern BCortexModal.jsx already uses
  // for its own gated sub-tabs.
  useEffect(() => {
    if (!visibleSubTabs.some((t) => t.id === activeTab)) {
      setActiveTab(visibleSubTabs[0]?.id ?? AI_TOOLS_SUB_TABS[0].id);
    }
  }, [visibleSubTabs, activeTab]);

  // Same per-tab dynamic-RBAC gating as Admin's own page (filterOptions in
  // user-management/index.jsx) — a custom-role holder sees the identical set
  // of enabled/disabled tabs in both places, rather than the modal being
  // more permissive just because it skips AnchorComponent's own gating.
  const tabOptions = useMemo(() => {
    const roles = session?.roles ?? [];
    const isAdmin = roles.includes('tenant_admin') || roles.includes('tenant_admin_readonly') || !!session?.isSuperAdmin;
    const perms = session?.permissions ?? [];
    return visibleSubTabs.map((t) => {
      const lacksPermission = !isAdmin && !!t.module && !perms.includes(`${t.module}:Read`);
      return {
        value: t.id,
        text: t.text,
        icon: t.icon,
        iconSize: 16,
        disabled: lacksPermission,
        disabledTooltip: t.module && lacksPermission ? missingPermissionMessage(`${t.module}:Read`) : undefined,
      };
    });
  }, [session, visibleSubTabs]);

  const activeTabConfig = visibleSubTabs.find((t) => t.id === activeTab) ?? visibleSubTabs[0] ?? AI_TOOLS_SUB_TABS[0];
  const isActiveDisabled = tabOptions.find((t) => t.value === activeTabConfig.id)?.disabled;
  const ActiveBody = activeTabConfig.Body;

  return (
    <Modal
      width='lg'
      title={
        <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[2] }}>
          <AutoAwesomeIcon sx={{ fontSize: 22, color: 'var(--ds-brand-600)' }} />
          AI & Tools
        </Box>
      }
      open={open}
      handleClose={onClose}
      onClose={onClose}
      maxHeight='90vh'
      contentStyles={{ overflowY: 'auto', overflowX: 'hidden', padding: '0px' }}
    >
      <Box
        sx={{
          position: 'sticky',
          top: 0,
          zIndex: 10,
          backgroundColor: 'var(--ds-background-100)',
          mb: ds.space[4],
          padding: `${ds.space[3]} ${ds.space[4]} 0px ${ds.space[4]}`,
        }}
      >
        <Tabs
          options={{ tabOptions }}
          value={activeTab}
          onChange={(next) => setActiveTab(next)}
          smallSize
          behavior='filter'
          variant='primary'
          ariaLabel='AI & Tools'
        />
      </Box>
      <Box sx={{ padding: `0px ${ds.space[7]} ${ds.space[6]}` }}>
        {!isActiveDisabled && (
          <ErrorBoundary key={activeTab}>
            <ActiveBody />
          </ErrorBoundary>
        )}
      </Box>
    </Modal>
  );
};

AIToolsModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
};

export default AIToolsModal;
