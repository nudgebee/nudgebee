import { Box } from '@mui/material';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useSession } from 'next-auth/react';
import PropTypes from 'prop-types';
import dynamic from 'next/dynamic';
import TuneIcon from '@mui/icons-material/Tune';
import AutoAwesomeIcon from '@mui/icons-material/AutoAwesome';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import PsychologyOutlinedIcon from '@mui/icons-material/PsychologyOutlined';
import SettingsOutlinedIcon from '@mui/icons-material/SettingsOutlined';
import PaidOutlinedIcon from '@mui/icons-material/PaidOutlined';
import HubOutlinedIcon from '@mui/icons-material/HubOutlined';
import ShieldOutlinedIcon from '@mui/icons-material/ShieldOutlined';
import { Modal } from '@ui/Modal';
import { EmptyState } from '@ui/EmptyState';
import Tabs from '@shared/navigation/Tabs';
import Loader from '@shared/Loader';
import ListAgents from '@components/llm/ListAgents';
import ListTools from '@components/llm/ListTools';
import LLMConsumptionTab from '@components/llm/LLMConsumptionTab';
import LLMModelConfigurationTab from '@components/llm/LLMModelConfigurationTab';
import ModelPricingTab from '@components/llm/ModelPricingTab';
import GlobalContextTab from '@components/llm/GlobalContextTab';
import UserFeedbackTab from '@components/llm/UserFeedbackTab';
import { BCortexFlagContext, isOSSDeploymentMode, useBCortexEnabled } from '@hooks/useBCortexEnabled';
import { getSessionAccountIds, hasFeatureAccess, hasPermission, isTenantAdmin, isTenantWideRole, isUiFeatureEnabled } from '@lib/auth';
import {
  AgentIcon,
  ToolsIcon,
  LLMFunctionIcon,
  LLMConsumptionIcon,
  InMemoryIcon,
  DocumentationIcon,
  FileOutlineIcon,
  FeedbackBlueIcon,
} from '@assets';
import { ds } from '@utils/colors';

// Both tabs pull heavy, tab-specific bundles that aren't the modal's default
// (Agents) view: Functions reaches the Kubernetes tooling tree (elkjs, xterm,
// reactflow) and RCA Format pulls in CodeMirror. Load them on demand so the
// Settings modal opens instantly and the heavy chunks fetch only when their tab
// is selected.
const ListFunctions = dynamic(() => import('@components/llm/ListFunctions'), {
  ssr: false,
  loading: () => <Loader style={{ position: 'static', height: '60vh', width: '100%' }} />,
});
const RCAFormatTab = dynamic(() => import('@components/llm/RCAFormatTab'), {
  ssr: false,
  loading: () => <Loader style={{ position: 'static', height: '60vh', width: '100%' }} />,
});
// MemoryTab is only rendered when MEMORY_MODULE is off (rollback / not-yet-
// enrolled tenants) — dynamic-import so the majority-case bundle doesn't pay
// for it.
const MemoryTab = dynamic(() => import('@components/llm/MemoryTab'), {
  ssr: false,
  loading: () => <Loader style={{ position: 'static', height: '60vh', width: '100%' }} />,
});
// Gateway admin config (routing rules + quotas) is an ENTERPRISE surface — it
// lives under @ee/ and is stripped from the OSS snapshot (oss-patches.sh replaces
// the marker-delimited block below with a () => null stub, mirroring the memory2
// tabs). Dynamic so its api1 client + CodeMirror-backed advanced editor don't load
// for the common Agents view.
// OSS-STRIP-BEGIN-GATEWAY-CONFIG-DYNAMIC-IMPORT
const GatewayConfigTab = dynamic(() => import('@ee/components/gateway-config/GatewayConfigTab'), {
  ssr: false,
  loading: () => <Loader style={{ position: 'static', height: '60vh', width: '100%' }} />,
});
// OSS-STRIP-END-GATEWAY-CONFIG-DYNAMIC-IMPORT
// Egress-filter admin config (LLM secret-DLP mode) is an ENTERPRISE surface —
// it lives under @ee/ and is stripped from the OSS snapshot (oss-patches.sh
// replaces the marker-delimited block below with a () => null stub, mirroring
// the gateway/memory2 tabs). Dynamic so its api1 client doesn't load for the
// common Agents view.
// OSS-STRIP-BEGIN-EGRESS-FILTER-DYNAMIC-IMPORT
const EgressFilterTab = dynamic(() => import('@ee/components/egress-filter/EgressFilterTab'), {
  ssr: false,
  loading: () => <Loader style={{ position: 'static', height: '60vh', width: '100%' }} />,
});
// OSS-STRIP-END-EGRESS-FILTER-DYNAMIC-IMPORT
// PreferencesTab / SoulTab / PrivacyTab live under llm/memory2/ which is
// stripped from the OSS snapshot via .oss-exclude. The marker-delimited
// block below is range-deleted and replaced with () => null stubs by
// scripts/oss-patches.sh when producing the OSS snapshot — Next.js
// Turbopack's static analysis fails on a missing module even for
// dynamic() imports, so a runtime .catch() fallback isn't enough.
//
// Turbopack also requires the { ssr, loading } options to be an INLINE
// object literal, so the block is repeated per call site rather than
// hoisted to a variable.
// OSS-STRIP-BEGIN-MEMORY-DYNAMIC-IMPORTS
const PreferencesTab = dynamic(() => import('@ee/components/memory2/PreferencesTab'), {
  ssr: false,
  loading: () => <Loader style={{ position: 'static', height: '60vh', width: '100%' }} />,
});
const SoulTab = dynamic(() => import('@ee/components/memory2/SoulTab'), {
  ssr: false,
  loading: () => <Loader style={{ position: 'static', height: '60vh', width: '100%' }} />,
});
const PrivacyTab = dynamic(() => import('@ee/components/memory2/PrivacyTab'), {
  ssr: false,
  loading: () => <Loader style={{ position: 'static', height: '60vh', width: '100%' }} />,
});
// OSS-STRIP-END-MEMORY-DYNAMIC-IMPORTS

const MemoryRedirectPanel = ({ onOpenBCortex }) => (
  <Box sx={{ py: ds.space[8] }}>
    <EmptyState
      surface
      size='page'
      icon={<PsychologyOutlinedIcon sx={{ fontSize: 28 }} />}
      title='Memory lives in b-Cortex'
      description='Patterns, Decisions, Sessions, Collective, Knowledge Graph and Knowledge Base are all consolidated under b-Cortex. Open it from the left rail or use the button below.'
      action={onOpenBCortex ? { label: 'Open b-Cortex', onClick: onOpenBCortex, icon: <PsychologyOutlinedIcon sx={{ fontSize: 16 }} /> } : undefined}
    />
  </Box>
);
MemoryRedirectPanel.propTypes = { onOpenBCortex: PropTypes.func };

const SettingsModal = ({ open, onClose, accountId, allAgents, refreshAgentListing, loadingAgents, onOpenBCortex, initialTab }) => {
  const { data: session } = useSession();
  const isSuperAdmin = !!session?.isSuperAdmin;
  const isTenantAdminUser = isTenantAdmin();
  const [tabsConfig, setTabsConfig] = useState([]);
  const [typeSelected, setTypeSelected] = useState('agents');
  // MEMORY_MODULE flag — gates the memory-backed Settings tabs
  // (Preferences, Soul, Privacy). When off, the "View old memory tab" link
  // inside BCortexDisabled just switches this modal to its Memory tab,
  // which renders the legacy MemoryTab inline (see the memory-tab branch
  // below).
  const bcortexEnabled = useBCortexEnabled(open);
  // Stable handler so the memoized provider value below stays referentially
  // equal across renders that don't actually change bcortexEnabled.
  const handleOpenLegacy = useCallback(() => setTypeSelected('memory'), []);
  const flagContextValue = useMemo(() => ({ enabled: bcortexEnabled, onOpenLegacy: handleOpenLegacy }), [bcortexEnabled, handleOpenLegacy]);

  useEffect(() => {
    // `active` guards the stale-response race: if the modal closes or
    // accountId / bcortexEnabled changes while the LLM_FUNCTION check is
    // in flight, we bail before overwriting tabsConfig with a stale set.
    let active = true;
    const initializeTabs = async () => {
      const baseTabsConfig = [
        { id: 'agents', icon: AgentIcon, label: 'Agents', alt: 'agent', size: 16 },
        { id: 'tools', icon: ToolsIcon, label: 'Tools', alt: 'tools', size: 16 },
      ];

      try {
        const hasAccess = await hasFeatureAccess('LLM_FUNCTION');
        if (!active) return;
        if (hasAccess) {
          baseTabsConfig.push({ id: 'functions', icon: LLMFunctionIcon, label: 'Functions', alt: 'functions', size: 18 });
        }
      } catch (error) {
        if (process.env.NODE_ENV === 'development') {
          console.error('Error checking feature access:', error);
        }
      }

      // Final order: Agents · Tools · Functions · RCA Format · Global Context ·
      // Memory · Usage & Limits · Configurations · Soul · Preferences ·
      // Privacy · User Feedback. The memory module's old standalone "Memory"
      // tab is now a redirect panel to b-Cortex.
      //
      // RCA Format is stored 1-per-account with no tenant rollup, so when
      // Settings is opened from the global sidebar (no accountId) the tab
      // has nothing meaningful to show — hide it instead of rendering a
      // "select an account" empty state.
      if (accountId) {
        baseTabsConfig.push({ id: 'rca-format', icon: FileOutlineIcon, label: 'RCA Format', alt: 'rca-format', size: 16 });
      }
      // Global Context lives in b-Cortex (as "Account Context") when the
      // MEMORY_MODULE flag is on. Keep it in Settings only when the flag
      // resolves as disabled so tenants without b-Cortex still see it here.
      // `null` (still resolving) is treated as enabled to avoid a flash of
      // the extra tab for tenants that DO have b-Cortex.
      if (bcortexEnabled === false) {
        baseTabsConfig.push({ id: 'global-context', icon: DocumentationIcon, label: 'Account Context', alt: 'account-context', size: 16 });
      }
      baseTabsConfig.push({ id: 'memory', icon: InMemoryIcon, label: 'Memory', alt: 'memory', size: 16 });
      baseTabsConfig.push({ id: 'consumption', icon: LLMConsumptionIcon, label: 'Usage & Limits', alt: 'consumption', size: 16 });
      // Configurations had been sharing LLMConsumptionIcon with Usage &
      // Limits — visual duplication. SettingsOutlinedIcon is the standard
      // MUI cog for an admin / config surface.
      baseTabsConfig.push({ id: 'llm-configuration', icon: SettingsOutlinedIcon, label: 'Configurations', alt: 'llm-configuration', size: 16 });
      // Next to Configurations because the two are visited together: adding a
      // custom provider is what leaves a model unpriced.
      //
      // No accountId gate: prices are tenant-wide and the tab reads only the
      // pricing table, so it is meaningful however Settings was opened.
      baseTabsConfig.push({ id: 'model-pricing', icon: PaidOutlinedIcon, label: 'Model Pricing', alt: 'model-pricing', size: 16 });
      // Soul / Preferences / Privacy are memory-v2 (b-Cortex) surfaces —
      // hide them entirely in OSS deployment mode, same as the sidebar
      // b-Cortex button in NubiBrainNav. In EE mode with the tenant
      // MEMORY_MODULE flag off, the tabs still list but render the
      // BCortexDisabled placeholder (existing behaviour).
      if (!isOSSDeploymentMode()) {
        baseTabsConfig.push({ id: 'soul', icon: AutoAwesomeIcon, label: 'Soul', alt: 'soul', size: 16 });
        baseTabsConfig.push({ id: 'preferences', icon: TuneIcon, label: 'Preferences', alt: 'preferences', size: 16 });
        baseTabsConfig.push({ id: 'privacy', icon: LockOutlinedIcon, label: 'Privacy', alt: 'privacy', size: 16 });
      }
      // User Feedback is authorized server-side for tenant + account
      // admins/readonly (ai_list_conversation_feedback in actions.yaml) and
      // scopes its rows by account ACL. Gating on isTenantAdmin() hid the tab
      // from account_admin / account_admin_readonly (and tenant_admin_readonly)
      // even though the backend grants them (#34514) — a FE guard stricter than
      // the backend. Show it to any tenant-wide role or account-scoped user; the
      // tab itself handles the tenant-wide (no accountId) open. hasReadAccess is
      // avoided here because accountId is empty when Settings opens from the
      // global sidebar, which would wrongly deny account-scoped users.
      if (isTenantWideRole() || getSessionAccountIds().length > 0) {
        baseTabsConfig.push({ id: 'user-feedback', icon: FeedbackBlueIcon, label: 'User Feedback', alt: 'user-feedback', size: 16 });
      }
      // Gateway admin config (routing rules + quotas) is a tenant-admin surface,
      // and is also delegable through dynamic RBAC: `llm_gateway_*` classifies to
      // the tenant-scoped `llm` module (@lib/permissionCatalog), so an `llm:Read`
      // grant clears the gateway for every read this tab makes. Gating on
      // isTenantAdmin() alone made the grant unreachable — the holder is not a
      // tenant admin, so the tab was never pushed and the permission an admin had
      // just ticked did nothing.
      // Also gate on the llmGateway UI feature flag — the same flag that shows/hides the
      // sidebar's AI Gateway entry (layout/index.jsx). Without it the Gateway tab appeared in
      // Settings even when the gateway UI was disabled for the tenant, which was confusing.
      if (isUiFeatureEnabled('llmGateway') && (isSuperAdmin || isTenantAdminUser || hasPermission('llm', 'Read'))) {
        baseTabsConfig.push({ id: 'gateway-config', icon: HubOutlinedIcon, label: 'Gateway', alt: 'gateway-config', size: 16 });
      }
      // Egress Filter (per-tenant LLM secret-DLP mode) is the same shape, but a
      // SEPARATE grant: `egressfilter_*` is its own module, so it gets its own
      // gate rather than riding along with Gateway — an admin who delegates only
      // secret-DLP policy must not thereby hand over gateway routing and quotas.
      // The OSS build stubs the tab component to () => null via the dynamic-import
      // strip above.
      if (isSuperAdmin || isTenantAdminUser || hasPermission('egressfilter', 'Read')) {
        baseTabsConfig.push({ id: 'egress-filter', icon: ShieldOutlinedIcon, label: 'Egress Filter', alt: 'egress-filter', size: 16 });
      }

      if (!active) return;
      setTabsConfig(baseTabsConfig);
    };
    if (open) {
      initializeTabs();
    }
    return () => {
      active = false;
    };
    // `session` is a dependency because the User Feedback gate reads
    // session-derived roles/account ids (isTenantWideRole / getSessionAccountIds).
    // On first mount the session is often still loading, and for account-scoped
    // roles none of the other deps (isSuperAdmin / isTenantAdminUser) flip when it
    // resolves — without `session` the effect never re-runs and the tab stays
    // hidden. `accountId` is kept (contra the review suggestion) because it gates
    // the RCA Format tab above, so account switches must re-run this effect.
  }, [open, isSuperAdmin, isTenantAdminUser, accountId, bcortexEnabled, session]);

  useEffect(() => {
    if (!open) {
      const timer = setTimeout(() => setTypeSelected('agents'), 1000);
      return () => clearTimeout(timer);
    }
  }, [open]);

  // Programmatic tab landing: when Settings is opened with `initialTab`
  // (e.g. NubiBrainNav hands off from b-Cortex → Memory tab), honor it on
  // this open. Falls back to 'agents' when initialTab is falsy so the
  // reset survives a close → reopen within the close-timer's 1000ms
  // window (that timer's cleanup would otherwise leave typeSelected on
  // whichever tab the user last had open).
  useEffect(() => {
    if (open) {
      setTypeSelected(initialTab || 'agents');
    }
  }, [open, initialTab]);

  const handleClose = () => {
    onClose();
  };

  const customTabs = tabsConfig.map((t) => ({
    value: t.id,
    text: t.label,
    icon: t.icon,
    iconSize: t.size,
  }));

  return (
    <BCortexFlagContext.Provider value={flagContextValue}>
      <Modal
        width='xl'
        title={'Settings'}
        open={open}
        handleClose={handleClose}
        onClose={handleClose}
        maxHeight='90vh'
        contentStyles={{
          overflowY: 'auto',
          overflowX: 'hidden',
          padding: '0px',
        }}
      >
        <Box
          sx={{
            position: 'sticky',
            top: 0,
            zIndex: 10,
            backgroundColor: 'var(--ds-background-100)',
            mb: ds.space[4],
            padding: `${ds.space[2]} ${ds.space[5]} 0px ${ds.space[5]}`,
          }}
        >
          {customTabs.length > 0 && (
            <Tabs
              options={{ tabOptions: customTabs }}
              value={typeSelected}
              onChange={(next) => setTypeSelected(next)}
              smallSize
              behavior='filter'
              variant='secondary'
              ariaLabel='Settings'
            />
          )}
        </Box>
        <Box sx={{ padding: `0px ${ds.space[5]}` }}>
          {typeSelected == 'agents' ? (
            <ListAgents
              accountId={accountId}
              allAgents={allAgents}
              refreshAgentListing={refreshAgentListing}
              loadingAgents={loadingAgents}
              stickyTable
            />
          ) : typeSelected == 'tools' ? (
            <ListTools accountId={accountId} stickyTable />
          ) : typeSelected == 'consumption' ? (
            <LLMConsumptionTab accountId={accountId} />
          ) : typeSelected == 'llm-configuration' ? (
            <LLMModelConfigurationTab />
          ) : typeSelected == 'model-pricing' ? (
            <ModelPricingTab stickyTable />
          ) : typeSelected == 'global-context' ? (
            <GlobalContextTab accountId={accountId} />
          ) : typeSelected == 'memory' ? (
            // When MEMORY_MODULE is off the tenant has no b-Cortex data to
            // route to, so this tab surfaces the legacy MemoryTab (reads
            // the pre-b-Cortex llm_conversation_memory path — still alive
            // for tenants that never migrated). While the flag is
            // resolving (null) we keep the redirect panel so we don't
            // flash MemoryTab for tenants that DO have b-Cortex enabled.
            bcortexEnabled === false ? (
              <MemoryTab accountId={accountId} />
            ) : (
              <MemoryRedirectPanel
                onOpenBCortex={
                  onOpenBCortex
                    ? () => {
                        onClose();
                        onOpenBCortex();
                      }
                    : undefined
                }
              />
            )
          ) : typeSelected == 'preferences' ? (
            <PreferencesTab scope='mine' readOnly={false} />
          ) : typeSelected == 'soul' ? (
            <SoulTab scope='mine' readOnly={false} />
          ) : typeSelected == 'privacy' ? (
            <PrivacyTab scope='mine' readOnly={false} />
          ) : typeSelected == 'rca-format' ? (
            <RCAFormatTab accountId={accountId} />
          ) : typeSelected == 'user-feedback' ? (
            <UserFeedbackTab accountId={accountId} stickyTable />
          ) : typeSelected == 'gateway-config' ? (
            <GatewayConfigTab />
          ) : typeSelected == 'egress-filter' ? (
            <EgressFilterTab />
          ) : (
            <ListFunctions accountId={accountId} stickyTable />
          )}
        </Box>
      </Modal>
    </BCortexFlagContext.Provider>
  );
};

SettingsModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  // Optional: empty / unset means tenant-wide read (Settings opened from
  // the global sidebar). Each tab decides its own tenant-wide behaviour.
  accountId: PropTypes.string,
  allAgents: PropTypes.array.isRequired,
  refreshAgentListing: PropTypes.func.isRequired,
  loadingAgents: PropTypes.bool.isRequired,
  onOpenBCortex: PropTypes.func,
  // Programmatic tab-landing (e.g. cross-modal hand-off from b-Cortex).
  // When null the modal opens on its default 'agents' tab.
  initialTab: PropTypes.string,
};

export default SettingsModal;
