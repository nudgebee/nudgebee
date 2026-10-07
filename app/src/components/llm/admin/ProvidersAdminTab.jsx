import { Box } from '@mui/material';
import { Banner } from '@ui/Banner';
import LLMConfigList from '@components/llm/LLMConfigList';
import LLMConfigurationScreen from '@components/llm/configuration/LLMConfigurationScreen';
import { useFeatureAccess } from '@hooks/useFeatureAccess';
import { ds } from '@utils/colors';

/**
 * Admin → AI & Tools → Providers. Read-only listing, same component and
 * banner Settings' old LLMModelConfigurationTab used (both now deleted —
 * SettingsModal.jsx superseded, docs/ia-consolidation-plan.md) — canonical
 * editing stays at Admin → Integrations → LLM (Decision J: Integrations
 * itself is out of scope for this consolidation, so this pairing is a
 * mirror, not a code merge of the two pages).
 *
 * LLM_CONFIG_V2 (internal, QA-only until the Phase 2 mapping editor ships)
 * swaps the flat list for the provider-grouped v2 view; both stay read-only
 * here, so the banner applies to either.
 */
const ProvidersAdminTab = ({ stickyTable = false }) => {
  const llmConfigV2 = useFeatureAccess('LLM_CONFIG_V2') === true;
  return (
    <Box>
      <Box sx={{ mb: ds.space[3] }}>
        <Banner
          tone='info'
          surface='section'
          actionsPlacement='inline'
          message={
            <>
              This view is read-only. Manage <strong>LLM Providers</strong> from <strong>Admin → Integrations → LLM</strong>.
            </>
          }
          actions={[{ label: 'Manage in Integrations', onClick: () => window.open('/accounts/account-form?cloudProvider=llm', '_blank') }]}
        />
      </Box>
      {llmConfigV2 ? <LLMConfigurationScreen stickyTable={stickyTable} /> : <LLMConfigList stickyTable={stickyTable} />}
    </Box>
  );
};

export default ProvidersAdminTab;
