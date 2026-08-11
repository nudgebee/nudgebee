import React, { useState, useEffect, useCallback, useMemo } from 'react';
import { Box, Stack } from '@mui/material';
import { ListingLayout } from '@ui/ListingLayout';
import { Banner } from '@ui/Banner';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { EmptyState } from '@ui/EmptyState';
import { Input } from '@ui/Input';
import { Modal } from '@ui/Modal';
import FilterDropdown from '@ui/FilterDropdown';
import Tooltip from '@ui/Tooltip';
import SearchInput from '@ui/SearchInput';
import CustomTable from '@shared/tables/CustomTable';
import { toast as snackbar } from '@ui/Toast';
import { ds } from '@utils/colors';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { isTenantAdmin } from '@lib/auth';

// CustomTable is tableLayout:fixed and falls back to 20% per column, so eight
// bare string headers declared 160% and the browser rationed the overflow.
// Widths below sum to 100%, and each is sized to its HEADER rather than its
// values: "$10.00" is short, but "Output / 1M" is not, and a header that wraps
// to two lines is what makes the row look ragged. Hence the "/ 1M" moves out of
// the headers into one caption above the table.
const HEADERS = [
  { name: 'Model', width: '22%' },
  { name: 'Provider', width: '12%' },
  { name: 'Input', width: '9%' },
  { name: 'Output', width: '9%' },
  { name: 'Cached', width: '9%' },
  { name: 'Long context', width: '15%' },
  { name: 'Source', width: '12%' },
  { name: '', width: '12%' },
];

// Providers a price row can legitimately carry. This is the api-server's
// configurable allowlist plus `vertexai_endpoint`, which is not settable from
// the LLM config form but does appear in llm_conversation_token_usage — leaving
// it out would make that spend permanently unpriceable, which is the $0-cost
// problem this tab exists to solve. Free text was the alternative, but a typo
// (`open-ai`, a trailing space) silently never matches a usage row.
const PRICEABLE_PROVIDERS = [
  'anthropic',
  'azure',
  'bedrock',
  'custom',
  'googleai',
  'huggingface',
  'openai',
  'sagemaker',
  'vertexai',
  'vertexai_endpoint',
  // AI Gateway custom / Vertex-OpenAI (MaaS) models route on the vLLM lane, so their usage
  // rows carry provider `vllm`. The gateway matches a price on model id (provider ignored),
  // but allowing `vllm` lets a tenant enter the provider exactly as it appears on usage.
  'vllm',
];

const SOURCE_ANY = '';
const SOURCE_TENANT = 'tenant';
const SOURCE_BUILT_IN = 'built-in';

const SOURCE_OPTIONS = [
  { value: SOURCE_TENANT, label: 'Custom rate' },
  { value: SOURCE_BUILT_IN, label: 'Built-in' },
];

const money = (v) => (v === undefined || v === null ? '—' : `$${Number(v).toFixed(2)}`);
const tokens = (v) => (v >= 1000 ? `${Math.round(v / 1000)}k` : String(v));

const EMPTY_DRAFT = {
  provider: '',
  model: '',
  input: '',
  output: '',
  cachedInput: '',
  cacheCreation: '',
  threshold: '',
  inputLong: '',
  outputLong: '',
};

/**
 * Model Pricing tab inside the Nubi Settings modal.
 *
 * The whole tab is one table: `llm_model_pricing`. It holds the built-in rates
 * we ship plus — since V843 — each tenant's own, and a tenant rate overrides
 * the built-in for the same (provider, model).
 *
 * Nothing else is consulted, deliberately. A model absent from this table has
 * no price; that IS "not priced", and no other source can say otherwise. An
 * earlier version cross-referenced the account's LLM configs to flag unpriced
 * models, which bought one warning at the cost of an account requirement, a
 * dependency on the integrations tables, and a tab that silently rendered
 * nothing when Settings was opened without an account.
 */
const ModelPricingTab = ({ stickyTable = false }) => {
  const [prices, setPrices] = useState([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loadError, setLoadError] = useState('');

  const [search, setSearch] = useState('');
  const [providerFilter, setProviderFilter] = useState('');
  const [sourceFilter, setSourceFilter] = useState(SOURCE_ANY);

  // Set to a row to edit it, or to EMPTY_DRAFT-with-editable-identity to add
  // one. Editing lives in a modal because a tiered price is five fields.
  const [draft, setDraft] = useState(null);
  const [isNew, setIsNew] = useState(false);
  // Set to the tenant row awaiting removal. Confirmed rather than immediate:
  // dropping an override changes what every account in the tenant is billed.
  const [pendingRemove, setPendingRemove] = useState(null);
  const [removing, setRemoving] = useState(false);

  // Writing a rate affects every account in the tenant, so the server restricts
  // it to tenant admins. This only hides controls that would fail — the server
  // is the real check.
  const canEdit = useMemo(() => {
    try {
      return isTenantAdmin();
    } catch {
      return false;
    }
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    setLoadError('');
    const res = await apiAskNudgebee.listModelPricing().catch((err) => ({ errors: [err] }));
    if (res?.errors?.length) {
      console.error('failed to load model pricing', res.errors);
      setLoadError('Could not load pricing.');
      setPrices([]);
    } else {
      setPrices(res?.data?.prices || []);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // A tenant rate replaces the built-in for the same (provider, model) — the
  // same precedence the server bills by, so the table shows what is charged
  // rather than everything on file.
  const allRows = useMemo(() => {
    const byKey = new Map();
    for (const p of prices) {
      const key = `${p.provider_name} ${p.model_name}`;
      const existing = byKey.get(key);
      // Tenant rows win; a built-in never overwrites one.
      if (!existing || (existing.is_built_in && !p.is_built_in)) byKey.set(key, p);
    }
    return [...byKey.values()].sort((a, b) => a.provider_name.localeCompare(b.provider_name) || a.model_name.localeCompare(b.model_name));
  }, [prices]);

  const providerOptions = useMemo(() => [...new Set(allRows.map((r) => r.provider_name))].sort().map((p) => ({ value: p, label: p })), [allRows]);

  const rows = useMemo(() => {
    const q = search.trim().toLowerCase();
    return allRows.filter(
      (r) =>
        (!q || r.model_name.toLowerCase().includes(q) || r.provider_name.toLowerCase().includes(q)) &&
        (!providerFilter || r.provider_name === providerFilter) &&
        (!sourceFilter || (sourceFilter === SOURCE_TENANT) === !r.is_built_in)
    );
  }, [allRows, search, providerFilter, sourceFilter]);

  const startAdd = () => {
    setIsNew(true);
    setDraft({ ...EMPTY_DRAFT });
  };

  // Overriding prefills from the built-in — including its long-context tier.
  // Without that, replacing a tiered model (every Gemini Pro) with a flat rate
  // would drop the tier and bill long prompts at the short rate, under-
  // reporting spend by roughly half.
  const startEdit = useCallback((row) => {
    setIsNew(false);
    setDraft({
      provider: row.provider_name,
      model: row.model_name,
      input: String(row.cost_per_million_input_tokens ?? ''),
      output: String(row.cost_per_million_output_tokens ?? ''),
      cachedInput: row.cost_per_million_cached_input_tokens != null ? String(row.cost_per_million_cached_input_tokens) : '',
      cacheCreation: row.cost_per_million_cache_creation_tokens != null ? String(row.cost_per_million_cache_creation_tokens) : '',
      threshold: row.context_threshold_tokens != null ? String(row.context_threshold_tokens) : '',
      inputLong: row.cost_per_million_input_tokens_long_ctx != null ? String(row.cost_per_million_input_tokens_long_ctx) : '',
      outputLong: row.cost_per_million_output_tokens_long_ctx != null ? String(row.cost_per_million_output_tokens_long_ctx) : '',
    });
  }, []);

  const closeEdit = () => setDraft(null);

  // Mirrors the server's rules so the common mistakes are caught before a round
  // trip. The server still rejects anything that gets past this.
  const draftError = () => {
    if (!draft) return null;
    if (!draft.provider.trim() || !draft.model.trim()) return 'Enter both a provider and a model name.';
    // A typo (`open-ai`, a stray capital or trailing space) would store a row
    // that never matches a usage row — the price would look set and the model
    // would still report $0. Normalise what is normalisable, reject the rest.
    if (!PRICEABLE_PROVIDERS.includes(draft.provider.trim().toLowerCase())) {
      return `Unknown provider. Use one of: ${PRICEABLE_PROVIDERS.join(', ')}.`;
    }
    const input = Number(draft.input);
    const output = Number(draft.output);
    if (draft.input === '' || draft.output === '') return 'Enter both an input and an output rate.';
    if (!Number.isFinite(input) || !Number.isFinite(output) || input < 0 || output < 0) return 'Rates must be zero or a positive number.';

    // Cache rates are independently optional: an omitted one falls back to the
    // input rate server-side, which is correct rather than merely tolerable.
    for (const [value, label] of [
      [draft.cachedInput, 'Cached input rate'],
      [draft.cacheCreation, 'Cache creation rate'],
    ]) {
      if (value === '') continue;
      const n = Number(value);
      if (!Number.isFinite(n) || n < 0) return `${label} must be zero or a positive number.`;
    }

    const filled = [draft.threshold, draft.inputLong, draft.outputLong].filter((v) => v !== '').length;
    // All three or none: a threshold without rates (or rates without a
    // threshold) stores a tier that never fires — it reads as configured but
    // bills as flat.
    if (filled !== 0 && filled !== 3) return 'Long-context pricing needs a threshold and both long-context rates — or leave all three blank.';
    if (filled === 3) {
      const t = Number(draft.threshold);
      const li = Number(draft.inputLong);
      const lo = Number(draft.outputLong);
      if (!Number.isFinite(t) || t <= 0) return 'Context threshold must be a positive number of tokens.';
      if (!Number.isFinite(li) || !Number.isFinite(lo) || li < 0 || lo < 0) return 'Long-context rates must be zero or a positive number.';
    }
    return null;
  };

  const save = async () => {
    const err = draftError();
    if (err) {
      snackbar.error(err);
      return;
    }
    setSaving(true);
    try {
      const price = {
        model_name: draft.model.trim(),
        provider_name: draft.provider.trim().toLowerCase(),
        cost_per_million_input_tokens: Number(draft.input),
        cost_per_million_output_tokens: Number(draft.output),
      };
      if (draft.cachedInput !== '') price.cost_per_million_cached_input_tokens = Number(draft.cachedInput);
      if (draft.cacheCreation !== '') price.cost_per_million_cache_creation_tokens = Number(draft.cacheCreation);
      if (draft.threshold !== '') {
        price.context_threshold_tokens = Number(draft.threshold);
        price.cost_per_million_input_tokens_long_ctx = Number(draft.inputLong);
        price.cost_per_million_output_tokens_long_ctx = Number(draft.outputLong);
      }
      const res = await apiAskNudgebee.upsertModelPricing([price]);
      if (res?.errors?.length) {
        snackbar.error(res.errors[0]?.message || 'Could not save pricing.');
        return;
      }
      snackbar.success(`Saved pricing for ${price.model_name}.`);
      closeEdit();
      await load();
    } finally {
      setSaving(false);
    }
  };

  const removeOverride = async () => {
    if (!pendingRemove) return;
    setRemoving(true);
    try {
      const res = await apiAskNudgebee.deleteModelPricing(pendingRemove.provider_name, pendingRemove.model_name);
      if (res?.errors?.length) {
        snackbar.error(res.errors[0]?.message || 'Could not remove the override.');
        return;
      }
      // removed === 0 means the row was already gone. Saying "removed" would
      // claim a change this request did not make.
      snackbar.success(
        res?.data?.removed
          ? `Removed the override for ${pendingRemove.model_name}.`
          : `No override was left to remove for ${pendingRemove.model_name}.`
      );
      setPendingRemove(null);
      await load();
    } finally {
      setRemoving(false);
    }
  };

  // Warns when an override is about to drop a tier the built-in defines. The
  // lookup has to normalise exactly as the save does, or typing `GoogleAI`
  // would find no built-in, skip the warning, and then save a row that does
  // match — losing the tier without ever having said so.
  const builtInForDraft =
    draft && prices.find((p) => p.is_built_in && p.provider_name === draft.provider.trim().toLowerCase() && p.model_name === draft.model.trim());
  const droppingTier = Boolean(builtInForDraft?.context_threshold_tokens && draft?.threshold === '');

  const tableData = useMemo(
    () =>
      rows.map((row) => {
        const tiered = row.context_threshold_tokens;
        return [
          { text: row.model_name },
          { text: row.provider_name },
          { text: money(row.cost_per_million_input_tokens) },
          { text: money(row.cost_per_million_output_tokens) },
          {
            // An unset cached rate is not "free" — the server bills cache reads at
            // the input rate — so show what will actually be charged rather than a
            // bare dash that reads as "no cost".
            component:
              row.cost_per_million_cached_input_tokens != null ? (
                <Tooltip
                  title={
                    row.cost_per_million_cache_creation_tokens != null
                      ? `Prompt tokens served from cache bill at ${money(
                          row.cost_per_million_cached_input_tokens
                        )} per 1M. Writing tokens into the cache bills at ${money(row.cost_per_million_cache_creation_tokens)} per 1M.`
                      : `Prompt tokens served from cache bill at ${money(row.cost_per_million_cached_input_tokens)} per 1M.`
                  }
                >
                  <span>{money(row.cost_per_million_cached_input_tokens)}</span>
                </Tooltip>
              ) : (
                <Tooltip title='No cached rate set — cache reads bill at the full input rate.'>
                  <span>—</span>
                </Tooltip>
              ),
          },
          {
            component: tiered ? (
              <Tooltip
                title={`Above ${Number(tiered).toLocaleString()} prompt tokens this model bills at ${money(
                  row.cost_per_million_input_tokens_long_ctx
                )} in / ${money(row.cost_per_million_output_tokens_long_ctx)} out per 1M.`}
              >
                <span>
                  <Chip tone='info'>{`>${tokens(Number(tiered))} · ${money(row.cost_per_million_input_tokens_long_ctx)}`}</Chip>
                </span>
              </Tooltip>
            ) : (
              <span>—</span>
            ),
          },
          {
            component: row.is_built_in ? (
              <Chip tone='neutral'>Built-in</Chip>
            ) : (
              <Tooltip title={row.pricing_updated_at ? `Set by your team on ${row.pricing_updated_at.slice(0, 10)}` : 'Set by your team'}>
                <span>
                  <Chip tone='success'>Custom rate</Chip>
                </span>
              </Tooltip>
            ),
          },
          {
            component: !canEdit ? null : (
              <Stack direction='row' gap={1}>
                <Button size='sm' tone='secondary' onClick={() => startEdit(row)} data-testid={`edit-price-${row.provider_name}-${row.model_name}`}>
                  {row.is_built_in ? 'Override' : 'Edit'}
                </Button>
                {/* Only a tenant row can be removed. A built-in has no override
                    to drop, and the server refuses to touch it regardless. */}
                {!row.is_built_in && (
                  <Button
                    size='sm'
                    tone='secondary'
                    onClick={() => setPendingRemove(row)}
                    data-testid={`remove-price-${row.provider_name}-${row.model_name}`}
                  >
                    Remove
                  </Button>
                )}
              </Stack>
            ),
          },
        ];
      }),
    [rows, canEdit, startEdit]
  );

  return (
    <Box sx={{ py: 0 }}>
      {!canEdit && (
        <Banner
          tone='info'
          message={
            <>
              Pricing is read-only here. Only a <strong>tenant admin</strong> can change it, because a rate applies to every account in the tenant.
            </>
          }
        />
      )}
      {/* Slots, not a hand-rolled Stack: ListingLayout owns the toolbar's
          alignment and spacing, and ToolbarSpacer is what pushes the actions
          to the right edge. Composing this by hand is what left the button
          misaligned against the filters. */}
      <ListingLayout id='model-pricing-list'>
        <ListingLayout.Toolbar>
          <SearchInput id='pricing-model-search' value={search} onChange={setSearch} label='Filter models…' onClear={() => setSearch('')} />
          <FilterDropdown
            id='pricing-provider-filter'
            label='Provider'
            options={providerOptions}
            value={providerOptions.find((o) => o.value === providerFilter) ?? null}
            onSelect={(_e, item) => setProviderFilter(item?.value || '')}
          />
          <FilterDropdown
            id='pricing-source-filter'
            label='Source'
            options={SOURCE_OPTIONS}
            value={SOURCE_OPTIONS.find((o) => o.value === sourceFilter) ?? null}
            onSelect={(_e, item) => setSourceFilter(item?.value || '')}
          />
          <ListingLayout.ToolbarSpacer />
          {rows.length !== allRows.length && (
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', whiteSpace: 'nowrap' }}>
              {rows.length} of {allRows.length}
            </Box>
          )}
          {canEdit && (
            <Button size='sm' onClick={startAdd} data-testid='add-model-price'>
              Add price
            </Button>
          )}
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          {!loading && loadError ? (
            <Banner tone='critical' title={loadError} message='Reopen Settings to retry. If it keeps failing, the LLM server may be unreachable.' />
          ) : !loading && allRows.length === 0 ? (
            <EmptyState title='No pricing on file' description='Add a price for any model you use — anything without one is reported as $0 spend.' />
          ) : !loading && rows.length === 0 ? (
            <EmptyState title='No models match these filters' description='Clear the search or filters to see the full list.' />
          ) : (
            <>
              {/* Units live here rather than repeated in three headers, which
                  is what forced those columns wide enough to wrap. */}
              <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', pb: ds.space[2] }}>Rates are USD per 1M tokens.</Box>
              {/* stickyHeader needs a bounded scroll container to stick inside.
                  Without the maxHeight the table grows to its full height and
                  pushes CustomTable's own pagination footer below the modal, so
                  only the first page is reachable. Same bound as the sibling
                  tabs in this modal. */}
              <CustomTable
                id='model-pricing'
                loading={loading}
                tableData={tableData}
                headers={HEADERS}
                stickyHeader={stickyTable}
                sx={stickyTable ? { maxHeight: 'calc(90vh - 340px)', overflowY: 'auto' } : undefined}
              />
            </>
          )}
        </ListingLayout.Body>
      </ListingLayout>

      {draft && (
        <Modal open onClose={closeEdit} title={isNew ? 'Add model price' : `Pricing — ${draft.model}`} width='sm'>
          <Stack gap={ds.space[3]} sx={{ pt: ds.space[2] }}>
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
              Rates are USD per 1M tokens and apply to every account in this tenant.
            </Box>

            {isNew && (
              <>
                <Input
                  label='Provider'
                  size='sm'
                  value={draft.provider}
                  onChange={(v) => setDraft({ ...draft, provider: v })}
                  placeholder='e.g. custom'
                  help={`Must match the provider shown on your usage rows. AI Gateway models (custom / Vertex-OpenAI) use "vllm" and are priced by model id. One of: ${PRICEABLE_PROVIDERS.join(
                    ', '
                  )}.`}
                />
                <Input
                  label='Model'
                  size='sm'
                  value={draft.model}
                  onChange={(v) => setDraft({ ...draft, model: v })}
                  placeholder='e.g. Qwen/Qwen3.6-35B-A3B-FP8'
                  help='The exact model id addressed on your usage rows (e.g. google/gemma-4-26b-a4b-it-maas for a Vertex MaaS model).'
                />
              </>
            )}

            <Input label='Input rate' size='sm' type='number' value={draft.input} onChange={(v) => setDraft({ ...draft, input: v })} />
            <Input label='Output rate' size='sm' type='number' value={draft.output} onChange={(v) => setDraft({ ...draft, output: v })} />

            <Box sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 500, color: 'var(--ds-gray-700)' }}>Prompt caching (optional)</Box>
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
              Most providers bill cached prompt tokens at a discount. Leave blank to bill them at the input rate above.
            </Box>
            <Input
              label='Cached input rate'
              size='sm'
              type='number'
              value={draft.cachedInput}
              onChange={(v) => setDraft({ ...draft, cachedInput: v })}
            />
            <Input
              label='Cache creation rate'
              size='sm'
              type='number'
              value={draft.cacheCreation}
              onChange={(v) => setDraft({ ...draft, cacheCreation: v })}
              help='Charged when tokens are first written to the cache. Anthropic bills this at 1.25x input; OpenAI-compatible providers do not charge it.'
            />

            <Box sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 500, color: 'var(--ds-gray-700)' }}>Long-context tier (optional)</Box>
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
              Some providers charge more once a prompt crosses a threshold. Leave blank for flat pricing.
            </Box>
            <Input
              label='Threshold (prompt tokens)'
              size='sm'
              type='number'
              value={draft.threshold}
              onChange={(v) => setDraft({ ...draft, threshold: v })}
            />
            <Input
              label='Input rate above threshold'
              size='sm'
              type='number'
              value={draft.inputLong}
              onChange={(v) => setDraft({ ...draft, inputLong: v })}
            />
            <Input
              label='Output rate above threshold'
              size='sm'
              type='number'
              value={draft.outputLong}
              onChange={(v) => setDraft({ ...draft, outputLong: v })}
            />

            {droppingTier && (
              <Banner
                tone='warning'
                message={`The built-in rate charges more above ${Number(
                  builtInForDraft.context_threshold_tokens
                ).toLocaleString()} tokens. Saving without a threshold bills long prompts at your flat rate, which will under-report spend.`}
              />
            )}

            <Stack direction='row' gap={ds.space[2]} justifyContent='flex-end'>
              <Button size='sm' tone='secondary' onClick={closeEdit} disabled={saving}>
                Cancel
              </Button>
              <Button size='sm' onClick={save} disabled={saving}>
                Save
              </Button>
            </Stack>
          </Stack>
        </Modal>
      )}

      {pendingRemove && (
        <Modal open onClose={() => setPendingRemove(null)} title={`Remove override — ${pendingRemove.model_name}`} width='sm'>
          <Stack gap={ds.space[3]} sx={{ pt: ds.space[2] }}>
            <Box sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)' }}>
              This drops your team&apos;s rate for <strong>{pendingRemove.model_name}</strong> on <strong>{pendingRemove.provider_name}</strong>. It
              affects every account in the tenant.
            </Box>
            {/* The two outcomes differ enough to be worth stating outright: one
                reverts to a known rate, the other stops costing the model at all. */}
            {pendingRemove.has_built_in ? (
              <Banner tone='info' message={`${pendingRemove.model_name} will bill at the built-in rate again.`} />
            ) : (
              <Banner
                tone='warning'
                title='No built-in rate to fall back on'
                message={`Nothing else prices ${pendingRemove.model_name}, so its spend will report as $0 until you add a rate again.`}
              />
            )}
            <Stack direction='row' gap={ds.space[2]} justifyContent='flex-end'>
              <Button size='sm' tone='secondary' onClick={() => setPendingRemove(null)} disabled={removing}>
                Cancel
              </Button>
              <Button size='sm' tone='danger' onClick={removeOverride} disabled={removing} data-testid='confirm-remove-price'>
                Remove
              </Button>
            </Stack>
          </Stack>
        </Modal>
      )}
    </Box>
  );
};

export default ModelPricingTab;
