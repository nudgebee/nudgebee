import { Box } from '@mui/material';
import { Banner } from '@ui/Banner';
import LLMConfigList from '@components/llm/LLMConfigList';
import { ds } from '@utils/colors';

/**
 * Admin → AI & Tools → Providers. Read-only listing, same component and
 * banner Settings' old LLMModelConfigurationTab used (both now deleted —
 * SettingsModal.jsx superseded, docs/ia-consolidation-plan.md) — canonical
 * editing stays at Admin → Integrations → LLM (Decision J: Integrations
 * itself is out of scope for this consolidation, so this pairing is a
 * mirror, not a code merge of the two pages).
 */
const ProvidersAdminTab = () => (
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
    <LLMConfigList stickyTable />
  </Box>
);

export default ProvidersAdminTab;
