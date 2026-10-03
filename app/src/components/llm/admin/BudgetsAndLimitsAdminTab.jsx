import { useState } from 'react';
import { Box } from '@mui/material';
import { ToggleGroup } from '@ui/ToggleGroup';
import LLMConsumptionTab from '@components/llm/LLMConsumptionTab';
import ModelPricingTab from '@components/llm/ModelPricingTab';
import { ds } from '@utils/colors';

/**
 * Admin → AI & Tools → Budgets & Limits. Takes the tenant/per-model half of
 * today's Usage & Limits tab, plus all of Model Pricing (docs/ia-consolidation-plan.md,
 * Decision F) — the personal-spend half stays at b-Cortex → Insights → My Usage
 * (PR 4). Mounted with no accountId: LLMConsumptionTab's own
 * `isTenantWide = !accountId` renders its tenant-wide view; its internal
 * budget-editor already supports creating either a tenant- or account-scoped
 * rule from there, independent of this outer prop.
 */
const BudgetsAndLimitsAdminTab = () => {
  const [activeSubTab, setActiveSubTab] = useState('usage');

  return (
    <Box>
      <Box sx={{ mb: ds.space[3] }}>
        <ToggleGroup
          selection='single'
          options={[
            { value: 'usage', label: 'Usage & Limits' },
            { value: 'pricing', label: 'Model Pricing' },
          ]}
          value={activeSubTab}
          onChange={(next) => setActiveSubTab(next)}
          size='md'
          ariaLabel='Budgets & Limits'
        />
      </Box>
      {activeSubTab === 'usage' && <LLMConsumptionTab />}
      {activeSubTab === 'pricing' && <ModelPricingTab stickyTable />}
    </Box>
  );
};

export default BudgetsAndLimitsAdminTab;
