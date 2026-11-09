import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { Box, Stack } from '@mui/material';
import { ListingLayout } from '@ui/ListingLayout';
import { Chip } from '@ui/Chip';
import { Label } from '@ui/Label';
import CustomTable from '@shared/tables/CustomTable';
import { toast as snackbar } from '@ui/Toast';
import { ds } from '@utils/colors';
import apiIntegrations from '@api1/integrations';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { parseIntegrationItem } from '@api1/integrations/helpers';

// Display names for the llm_provider enum (api-server integrations/llm.go).
const PROVIDER_LABELS = {
  anthropic: 'Anthropic',
  azure: 'Azure OpenAI',
  bedrock: 'Amazon Bedrock',
  googleai: 'Google AI',
  huggingface: 'Hugging Face',
  openai: 'OpenAI',
  custom: 'Custom (OpenAI-compatible)',
  sagemaker: 'Amazon SageMaker',
  vertexai: 'Vertex AI',
};

// Provider-level secret config keys. A configuration counts as "credential
// configured" when any of these rows exist on it — per-tier / per-agent /
// fallback secrets carry suffixes and belong to those slots, not here.
const PROVIDER_SECRET_KEYS = ['llm_provider_api_key', 'llm_provider_access_key', 'llm_provider_secret_key', 'llm_oauth_client_secret'];

const MAIN_HEADERS = ['Provider', 'Configurations', 'Credentials', 'Models'];
const CONFIG_HEADERS = ['Name', 'Accounts', 'Credential', 'Status'];
const MODEL_HEADERS = ['Model', 'Input $/1M', 'Output $/1M', 'Cached $/1M', 'Context', 'Max output', 'Vision', 'Source'];

const configValue = (item, name) =>
  (Array.isArray(item?.integration_config_values) ? item.integration_config_values : []).find((v) => v?.name === name)?.value;

const hasProviderSecret = (item) =>
  (Array.isArray(item?.integration_config_values) ? item.integration_config_values : []).some((v) => PROVIDER_SECRET_KEYS.includes(v?.name));

const isTenantDefault = (item) => configValue(item, 'default_llm_provider') === 'true';

const fmtRate = (v) => (v == null || v === '' ? '—' : `$${v}`);
const fmtTokens = (v) => (v ? `${Math.round(Number(v) / 1000)}k` : '—');

const SectionHeading = ({ children }) => (
  <Box sx={{ fontWeight: ds.weight.semibold, color: ds.gray[600], mb: ds.space[2], mt: ds.space[3], textTransform: 'uppercase' }}>{children}</Box>
);

const ExpandedProviderGroup = ({ group }) => {
  if (!group) {
    return null;
  }

  const configRows = group.integrations.map((item) => [
    {
      component: (
        <Stack direction='row' spacing={0.5} alignItems='center' useFlexGap flexWrap='wrap'>
          <span>{item.name || '-'}</span>
          {isTenantDefault(item) && (
            <Chip size='sm' variant='tag' tone='success'>
              Default
            </Chip>
          )}
        </Stack>
      ),
    },
    {
      component: (
        <Stack direction='row' spacing={0.5} useFlexGap flexWrap='wrap'>
          {(Array.isArray(item?.integrations_cloud_accounts) ? item.integrations_cloud_accounts : [])
            .filter((d) => d?.cloud_account_name)
            .map((acc, i) => (
              <Chip key={`${acc.cloud_account_name}-${i}`} size='sm' variant='tag' tone='neutral'>
                {acc.cloud_account_name}
              </Chip>
            ))}
        </Stack>
      ),
    },
    { component: <Label text={hasProviderSecret(item) ? 'Configured' : 'Not configured'} tone={hasProviderSecret(item) ? 'success' : 'warning'} /> },
    { component: <Label text={item.status || '-'} /> },
  ]);

  const modelRows = group.models.map((m) => [
    { text: m.model_name },
    { text: fmtRate(m.cost_per_million_input_tokens) },
    { text: fmtRate(m.cost_per_million_output_tokens) },
    { text: fmtRate(m.cost_per_million_cached_input_tokens) },
    { text: fmtTokens(m.max_context_tokens) },
    { text: m.max_output_tokens ? String(m.max_output_tokens) : '—' },
    { text: m.supports_image_input ? 'Yes' : '—' },
    { text: m.is_built_in ? 'Built-in' : 'Custom' },
  ]);

  return (
    <Box>
      <SectionHeading>Configurations on {group.label}</SectionHeading>
      <CustomTable
        id={`llm-provider-configs-${group.type}`}
        tableData={configRows}
        headers={CONFIG_HEADERS}
        totalRows={configRows.length}
        rowsPerPage={Math.max(configRows.length, 10)}
        pageNumber={1}
        onPageChange={() => {}}
      />
      <SectionHeading>Models priced for {group.label}</SectionHeading>
      {modelRows.length === 0 ? (
        <Box sx={{ color: ds.gray[500], mb: ds.space[2] }}>No pricing entries for this provider yet. Rates can be added under Model Pricing.</Box>
      ) : (
        <CustomTable
          id={`llm-provider-models-${group.type}`}
          tableData={modelRows}
          headers={MODEL_HEADERS}
          totalRows={modelRows.length}
          rowsPerPage={Math.max(modelRows.length, 10)}
          pageNumber={1}
          onPageChange={() => {}}
        />
      )}
    </Box>
  );
};

/**
 * Read-only, provider-type-grouped view of the tenant's LLM connections
 * (LLM_CONFIG_V2). One row per provider *type*; its configurations and the
 * models priced for that type expand below the row. Models are listed once
 * per type — llm_model_pricing is keyed by (model, provider name), not by
 * integration, so a per-configuration model list would imply ownership the
 * storage doesn't have.
 */
const ProvidersTable = ({ stickyTable = false }) => {
  const [integrations, setIntegrations] = useState([]);
  const [prices, setPrices] = useState([]);
  const [loading, setLoading] = useState(true);

  const fetchAll = useCallback(async () => {
    setLoading(true);
    try {
      const [intRes, priceRes] = await Promise.all([
        apiIntegrations.listIntegrations({ type: 'llm', limit: 200 }),
        apiAskNudgebee.listModelPricing().catch((err) => ({ errors: [err] })),
      ]);

      const gqlErrors = intRes?.data?.errors;
      if (Array.isArray(gqlErrors) && gqlErrors.length > 0) {
        snackbar.error(gqlErrors[0]?.message || 'Failed to load LLM configurations');
        return;
      }
      const rawRows = intRes?.data?.data?.integrations_list?.rows || [];
      setIntegrations(rawRows.map(parseIntegrationItem));
      const total = intRes?.data?.data?.integrations_aggregate?.rows?.[0]?.count || 0;
      if (total > rawRows.length) {
        snackbar.warning(`Showing ${rawRows.length} of ${total} LLM configurations.`);
      }

      if (priceRes?.errors?.length) {
        // Pricing is supplementary here — the provider rows still render.
        console.error('ProvidersTable: failed to load model pricing', priceRes.errors);
        setPrices([]);
      } else {
        setPrices(priceRes?.data?.prices || []);
      }
    } catch (err) {
      console.error('ProvidersTable: fetch threw', err);
      snackbar.error('Failed to load LLM configurations');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchAll();
  }, [fetchAll]);

  // A tenant rate replaces the built-in for the same (provider, model) — the
  // same precedence the server bills by (mirrors ModelPricingTab).
  const effectivePrices = useMemo(() => {
    const byKey = new Map();
    for (const p of prices) {
      const key = `${p.provider_name} ${p.model_name}`;
      const existing = byKey.get(key);
      if (!existing || (existing.is_built_in && !p.is_built_in)) {
        byKey.set(key, p);
      }
    }
    return [...byKey.values()];
  }, [prices]);

  const groupsByType = useMemo(() => {
    const byType = new Map();
    for (const item of integrations) {
      const type = configValue(item, 'llm_provider') || 'custom';
      if (!byType.has(type)) {
        byType.set(type, { type, label: PROVIDER_LABELS[type] || type, integrations: [], models: [] });
      }
      byType.get(type).integrations.push(item);
    }
    for (const group of byType.values()) {
      group.models = effectivePrices.filter((p) => p.provider_name === group.type).sort((a, b) => a.model_name.localeCompare(b.model_name));
    }
    return byType;
  }, [integrations, effectivePrices]);

  const groups = useMemo(() => [...groupsByType.values()].sort((a, b) => a.label.localeCompare(b.label)), [groupsByType]);

  const tableData = useMemo(
    () =>
      groups.map((group) => {
        const configured = group.integrations.filter(hasProviderSecret).length;
        const total = group.integrations.length;
        const credentialTone = configured === total ? 'success' : configured === 0 ? 'warning' : 'info';
        const credentialText = configured === total ? 'Configured' : `${configured} of ${total} configured`;
        return [
          {
            component: (
              <Stack direction='row' spacing={0.5} alignItems='center' useFlexGap flexWrap='wrap'>
                <span>{group.label}</span>
                <Chip size='sm' variant='tag' tone='neutral'>
                  {group.type}
                </Chip>
              </Stack>
            ),
            drilldownQuery: { data: { providerType: group.type } },
          },
          { text: String(group.integrations.length) },
          { component: <Label text={credentialText} tone={credentialTone} /> },
          { text: String(group.models.length) },
        ];
      }),
    [groups]
  );

  const expandable = useMemo(
    () => ({
      tabs: [
        {
          text: 'Details',
          componentFn: (_option, query) => <ExpandedProviderGroup group={groupsByType.get(query?.data?.providerType)} />,
        },
      ],
    }),
    [groupsByType]
  );

  return (
    <ListingLayout id='llm-providers-v2'>
      <ListingLayout.Body>
        <CustomTable
          id='llm-providers-by-type'
          loading={loading}
          tableData={tableData}
          headers={MAIN_HEADERS}
          expandable={expandable}
          totalRows={groups.length}
          rowsPerPage={Math.max(groups.length, 10)}
          pageNumber={1}
          onPageChange={() => {}}
          stickyHeader={stickyTable}
          sx={stickyTable ? { maxHeight: 'calc(90vh - 340px)', overflowY: 'auto' } : undefined}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default ProvidersTable;
