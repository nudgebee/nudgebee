import { Button } from '@ui/Button';
import TextareaAutosize, { type TextareaAutosizeProps } from '@mui/material/TextareaAutosize';
import { useForkRef } from '@mui/material/utils';
import { Avatar, Box, ButtonBase as MuiButtonBase, ClickAwayListener, Popper, styled, Typography } from '@mui/material';
import type { Theme } from '@mui/material/styles';
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { ArrowRightWhiteIcon, CustomAgentBlueIcon } from '@assets';
import { ds } from 'src/utils/colors';
import { getIcon } from '@components/llm/common/AgentIcon';
import StopIcon from '@mui/icons-material/Stop';
import ArrowDropDownIcon from '@mui/icons-material/ArrowDropDown';
import AttachFileIcon from '@mui/icons-material/AttachFile';
import CloseIcon from '@mui/icons-material/Close';
import SafeIcon from '@shared/icons/SafeIcon';
import { toast as snackbar } from '@ui/Toast';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Input } from '@ui/Input';
import Tooltip from '@ui/Tooltip';
import CheckIcon from '@mui/icons-material/Check';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import { baseConfigSourceFor, configKeyForSource, wholeConfigSourceFor } from '@utils/llmConfigSource';

// Define custom props interface
interface CustomTextareaProps extends TextareaAutosizeProps {
  fontSize?: string;
  fontWeight?: string;
  width?: string;
  theme?: Theme;
  maxRows?: number;
}

export const Textarea = styled(TextareaAutosize, { shouldForwardProp: (prop) => prop !== 'fontSize' && prop !== 'maxRows' })<CustomTextareaProps>(
  ({ fontSize = 'var(--ds-text-body-lg)', fontWeight = 'var(--ds-font-weight-regular)', width = ds.space.mul(0, 250), maxRows = 5 }) => `
    box-sizing: border-box;
    width: ${width};
    font-family: "Roboto", sans-serif;
    font-size: ${fontSize};
    font-weight: ${fontWeight};
    line-height: 1.5;
    padding: ${ds.space[2]} ${ds.space[3]};
    border-radius: var(--ds-radius-md);
    color: var(--ds-gray-700);
    background: var(--ds-background-100);
    border: 1px solid var(--ds-gray-200);
    box-shadow: 0 2px 2px var(--ds-gray-100);
    max-height: calc(${maxRows} * 1.5em + ${ds.space[4]});
    overflow-y: auto !important;
    resize: vertical;
    &:hover {
      border-color: var(--ds-blue-400);
    }
  
    &:focus {
      border-color: var(--ds-blue-400);
      box-shadow: 0 0 0 3px var(--ds-blue-200);
    }
  
    // firefox
    &:focus-visible {
      outline: 0;
    }

    &::-webkit-scrollbar {
      width: ${ds.space.mul(0, 3)};
      display: none;
    }

    &:hover::-webkit-scrollbar {
      display: block;
    }

    &::-webkit-scrollbar-track {
      border-radius: var(--ds-radius-sm);
      background-color: var(--ds-gray-200);
    }

    &::-webkit-scrollbar-thumb {
      background-color: var(--ds-gray-400);
      border-radius: var(--ds-radius-sm);
    }

    &::-webkit-scrollbar-thumb:hover {
      background-color: var(--ds-gray-500);
    }
  `
);

// A selection: which credential serves the request, and which model to ask for.
// configSource is the value sent as config.llm_config_source.
interface ModelOption {
  provider: string;
  model: string;
  configSource?: string;
  configName?: string;
}

// One distinct destination the server resolved — provider plus every routing
// and auth field. Model is deliberately NOT part of a credential's identity, so
// each carries the models reachable through it.
export interface LLMCredential {
  id: string;
  name: string;
  provider: string;
  configSource: string;
  // Every configured slot resolving to this credential. Not for display — used
  // to match a stored pin (which may name any of them) back to this entry.
  sources: string[];
  models: { model: string }[];
}

// One configured slot as the server reports it: a (config, model) pair that
// keeps the config's own name. credentials[] folds these by destination, which
// collapses configs sharing an API key into one entry under a single name — so
// the picker lists these instead.
export interface LLMModelEntry {
  model: string;
  provider: string;
  configSource: string;
  // Optional because the server sends config_name with omitempty and only
  // fills it for a slot that has a config source — so it really can arrive
  // undefined, whatever a required type here would claim.
  configName?: string;
  isFallback?: boolean;
}

// A config as the picker lists it: every slot belonging to one integration (or
// to the system ENV config), keyed by the source id with any tier/agent/fallback
// suffix removed.
interface ConfigEntry {
  key: string;
  name: string;
  entries: LLMModelEntry[];
}

// Trims the slot decoration the server appends for display —
// "name · Summary tier (fallback)" is the same config as "name".
//
// Falls back to the source id when there is no name, which is what the server
// itself does for a slot it can't name. A generic placeholder would be worse
// here: every unnamed config would collapse into one indistinguishable row.
const configNameForEntry = (entry: LLMModelEntry): string => {
  const raw = entry.configName || entry.configSource || '';
  return (
    raw
      .replace(/\s*\(fallback\)\s*$/i, '')
      .replace(/\s+·\s+.*$/, '')
      .trim() || raw
  );
};

// A selection is (credential, model). configSource identifies the credential,
// so two entries for the same model on different credentials stay distinct.
//
// Exception for selections restored from conversations that predate pinning:
// they carry a provider/model but no configSource. Comparing strictly would
// leave the user's own saved pick unhighlighted, so a missing configSource on
// either side falls back to matching provider+model alone.
const isSameModelOption = (a?: ModelOption | null, b?: ModelOption | null): boolean => {
  if (!a || !b) return false;
  if (a.provider !== b.provider || a.model !== b.model) return false;
  if (!a.configSource || !b.configSource) return true;
  return a.configSource === b.configSource;
};

// Finds the credential a selection belongs to. Matches on sources[], not just
// configSource, so a conversation pinned to a tier slot that has since been
// folded into its parent credential still resolves.
const credentialForSelection = (option: ModelOption | null | undefined, credentials: LLMCredential[]): LLMCredential | undefined => {
  if (!option?.configSource) return undefined;
  return credentials.find((c) => c.configSource === option.configSource || c.sources.includes(option.configSource as string));
};

// SKUs that may struggle with the planner step — fires an advisory hint
// when picked for Reasoning or the blanket model. Heuristic, not a block.
const LOWER_TIER_REGEX: Record<string, RegExp> = {
  googleai: /flash|lite/i,
  vertex: /flash|lite/i,
  anthropic: /haiku/i,
  openai: /-mini\b|gpt-3\.5/i,
  azure: /-mini\b|gpt-3\.5/i,
  bedrock: /haiku|llama3-8b|command-light/i,
};

const isLowerTierForReasoning = (provider?: string, model?: string): boolean => {
  if (!provider || !model) return false;
  const re = LOWER_TIER_REGEX[provider.toLowerCase()];
  return !!re && re.test(model);
};

const PICKER_TIER_KEYS = ['reasoning', 'retrieval', 'summary'] as const;
type PickerTierKey = (typeof PICKER_TIER_KEYS)[number];
const PICKER_TIER_LABELS: Record<PickerTierKey, string> = {
  reasoning: 'Reasoning',
  retrieval: 'Retrieval',
  summary: 'Summary',
};
type TierModelMap = Partial<Record<PickerTierKey, ModelOption>>;

const pickerButtonLabel = (
  selectedModel?: ModelOption | null,
  selectedTierModels?: TierModelMap | null,
  credentials: LLMCredential[] = [],
  selectedConfig?: ConfigOption | null,
  configs: ConfigEntry[] = []
): string => {
  if (selectedConfig) {
    // A pin restored from a conversation carries only the source id, so the
    // name is resolved from the loaded configs rather than stored with it.
    // Done here rather than at restore time because the model list can still
    // be in flight then — this way the label fills in once it arrives.
    const key = configKeyForSource(selectedConfig.configSource);
    return selectedConfig.configName || configs.find((c) => c.key === key)?.name || 'Config';
  }
  if (selectedModel) {
    // The config is part of the identity, so the trigger names it — two entries
    // reading just 'Qwen3.6-35B' would be indistinguishable. The selection's own
    // configName wins: the credential fold puts configs sharing an API key under
    // a single name, which would label the pick as a config it didn't come from.
    // Selections restored from older conversations carry no name, so those still
    // resolve through the credential.
    const name = selectedModel.configName || credentialForSelection(selectedModel, credentials)?.name;
    return name ? `${selectedModel.model} · ${name}` : selectedModel.model;
  }
  if (selectedTierModels && Object.keys(selectedTierModels).length > 0) return 'By task';
  return 'Model';
};

// Wire format expected by the ai_execute_investigation `images` field.
export interface OutgoingImage {
  data: string; // base64, data-URI prefix stripped
  mime_type: string;
}

// Server-advertised image capability (from ai_list_models.image_support).
interface ImageSupport {
  enabled: boolean;
  maxPerMessage: number;
  maxSizeMb: number;
  allowedMimeTypes: string[];
}

interface AttachedImage extends OutgoingImage {
  id: string;
  name: string;
}

interface AutoSuggestTextareaProps {
  value: string;
  suggestionsAt: { name: string; display_name: string }[];
  functionSuggestions?: { name: string; description: string; variables?: any; variable_defaults?: any }[];
  placeholder: string;
  maxRows: number;
  maxLength: number;
  onKeyDown: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
  fontSize: string;
  fontWeight: string;
  onClick: () => void;
  buttonProperties: {
    show: boolean;
    enable: boolean;
    onClick: (e: string, config?: { llm_provider?: string; llm_model_name?: string; llm_config_source?: string }, images?: OutgoingImage[]) => void;
    onClickStop: () => void;
  };
  chatScreen?: boolean;
  isFollowUp?: boolean;
  disabled?: boolean;
  allowStop?: boolean;
  credentials?: LLMCredential[];
  models?: LLMModelEntry[];
  defaultModel?: { provider: string; model: string };
  selectedModel?: ModelOption | null;
  onModelSelect?: (model: ModelOption | null) => void;
  // Mutually exclusive with selectedModel (reducer enforces).
  selectedTierModels?: TierModelMap | null;
  onTierModelsSelect?: (picks: TierModelMap | null) => void;
  selectedConfig?: ConfigOption | null;
  onConfigSelect?: (config: ConfigOption | null) => void;
  onConfigClear?: () => void;
  popupInitial?: boolean;
  imageSupport?: ImageSupport;
  externalAgentsLoading?: boolean;
  submitOnModEnter?: boolean;
}

// A whole-config selection: the config decides the model, per task, using its
// own tier slots. configSource is the ':all' form of the config's source id.
type PickerMode = 'blanket' | 'tier' | 'config';
const PICKER_MODE_HELP: Record<PickerMode, string> = {
  blanket:
    'The selected model is used for every LLM call in this conversation — including background tasks (memory, titles, light summaries). Its config’s own per-task models are not used.',
  tier: 'By-task picks apply only to LLM calls tagged with that task. Untagged background calls (memory, titles, light summaries) keep the operator default.',
  config:
    'The whole config is used, and it picks the model per task from its own settings. Tasks it does not configure fall back to the config’s default model.',
};

// A whole-config selection: the config decides the model, per task, using its
// own tier slots. configSource is the ':all' form of the config's source id.
export interface ConfigOption {
  configSource: string;
  configName?: string;
}

interface ModelPickerPopoverProps {
  credentials: LLMCredential[];
  // Ungrouped (config, model) rows. The picker lists configs from these; the
  // credentials prop stays only so a stored pin can still be matched back to a
  // credential for the trigger label.
  models?: LLMModelEntry[];
  selectedModel?: ModelOption | null;
  onModelSelect?: (model: ModelOption | null) => void;
  selectedTierModels?: TierModelMap | null;
  onTierModelsSelect?: (picks: TierModelMap | null) => void;
  selectedConfig?: ConfigOption | null;
  onConfigSelect?: (config: ConfigOption | null) => void;
  onConfigClear?: () => void;
  disabled?: boolean;
}

export const ModelPickerPopover: React.FC<ModelPickerPopoverProps> = ({
  // Defaulted because this component is exported: callers inside this file
  // guard on a non-empty list, but an external consumer needn't.
  credentials = [],
  models = [],
  selectedModel,
  onModelSelect,
  selectedTierModels,
  onTierModelsSelect,
  selectedConfig,
  onConfigSelect,
  onConfigClear,
  disabled = false,
}) => {
  const [open, setOpen] = useState(false);
  const anchorRef = useRef<HTMLDivElement | null>(null);
  // 'blanket' stages one model for every call, 'tier' one per task, and
  // 'config' hands the whole config over so its own per-task models apply. The
  // three are mutually exclusive, so each keeps its own staged value and Apply
  // commits whichever mode is showing.
  const [mode, setMode] = useState<PickerMode>('blanket');
  const [staged, setStaged] = useState<ModelOption | null>(null);
  const [stagedTier, setStagedTier] = useState<TierModelMap>({});
  const [stagedConfig, setStagedConfig] = useState<ConfigOption | null>(null);
  const [activeTier, setActiveTier] = useState<PickerTierKey>('reasoning');
  // Which config the right-hand pane is showing. Held as a key rather than an
  // index so a search that reorders or drops entries can't point it at the
  // wrong one — a stale key simply falls back to the first.
  const [activeConfigKey, setActiveConfigKey] = useState<string>('');
  const [search, setSearch] = useState('');

  const openPopover = () => {
    if (disabled) return;
    const tierPicks = selectedTierModels ?? {};
    // Reopen in the mode the conversation is actually in, rather than always
    // in All-calls — landing in another mode reads as the selection vanishing.
    let startMode: PickerMode = 'blanket';
    if (selectedConfig) startMode = 'config';
    else if (!selectedModel && Object.keys(tierPicks).length > 0) startMode = 'tier';
    setMode(startMode);
    setStaged(selectedModel ?? null);
    setStagedTier(tierPicks);
    setStagedConfig(selectedConfig ?? null);
    setActiveTier('reasoning');
    setSearch('');
    // Open on the config the current selection came from, so reopening lands
    // where the user left it rather than on the first config.
    const landOn =
      startMode === 'blanket' ? selectedModel : startMode === 'config' ? selectedConfig : tierPicks.reasoning ?? Object.values(tierPicks)[0];
    setActiveConfigKey(landOn?.configSource ? configKeyForSource(landOn.configSource) : '');
    setOpen(true);
  };

  // The three modes are mutually exclusive, so applying one always clears the
  // other two — a leftover selection would silently outrank what was just
  // picked.
  const handleApply = () => {
    if (mode === 'blanket') {
      onTierModelsSelect?.(null);
      onConfigSelect?.(null);
      onModelSelect?.(staged);
    } else if (mode === 'tier') {
      const cleaned: TierModelMap = {};
      for (const t of PICKER_TIER_KEYS) {
        const p = stagedTier[t];
        if (p && p.provider && p.model) cleaned[t] = p;
      }
      onModelSelect?.(null);
      onConfigSelect?.(null);
      onTierModelsSelect?.(Object.keys(cleaned).length > 0 ? cleaned : null);
    } else {
      onModelSelect?.(null);
      onTierModelsSelect?.(null);
      onConfigSelect?.(stagedConfig);
    }
    setOpen(false);
  };

  const handleClear = () => {
    onModelSelect?.(null);
    onTierModelsSelect?.(null);
    onConfigSelect?.(null);
    // Nulling both selections is indistinguishable from never having picked
    // one, and that state inherits the conversation's stored config. Clearing
    // has to say so explicitly or the old pick comes straight back.
    onConfigClear?.();
    setOpen(false);
  };

  // Every slot the server reported, grouped by the config it belongs to. This
  // is a change of axis over data the server already resolved — no deduping or
  // identity decisions happen here.
  const configEntries = useMemo(() => {
    const byKey = new Map<string, ConfigEntry>();
    for (const entry of models) {
      const key = configKeyForSource(entry.configSource);
      if (!key) continue;
      const existing = byKey.get(key);
      if (existing) existing.entries.push(entry);
      else byKey.set(key, { key, name: '', entries: [entry] });
    }
    // Named after grouping, from the first slot that actually carries a name:
    // naming from whichever slot arrived first would label the whole config
    // with a raw source id whenever that one slot happened to lack one.
    for (const cfg of byKey.values()) {
      cfg.name = configNameForEntry(cfg.entries.find((e) => e.configName) ?? cfg.entries[0]);
    }
    return [...byKey.values()];
  }, [models]);

  // Search matches a config's name or any provider/model it serves — a config
  // survives if either side hits, so searching a model name narrows to the
  // configs that can actually serve it.
  const filteredConfigs = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return configEntries;
    return configEntries
      .map((c) => {
        // Every provider in the config, not just the first slot's: a config
        // can mix them (Gemini for reasoning, HF for summary), and matching
        // only one would hide it from a search for the other.
        const configHit = c.name.toLowerCase().includes(q) || c.entries.some((e) => e.provider.toLowerCase().includes(q));
        return { ...c, entries: configHit ? c.entries : c.entries.filter((e) => e.model.toLowerCase().includes(q)) };
      })
      .filter((c) => c.entries.length > 0);
  }, [configEntries, search]);

  // A stale key (search dropped the config, list reloaded) resolves to the first
  // entry rather than leaving an empty pane.
  const activeConfig = filteredConfigs.find((c) => c.key === activeConfigKey) ?? filteredConfigs[0];

  // One row per distinct model in the active config. A model usually appears
  // more than once — as the config's base model and again under a tier — and
  // those are the same choice here, since picking a model bypasses tiers. The
  // slot kept is the one that becomes the pin, so a primary slot always wins
  // over a fallback.
  const activeRows = useMemo(() => {
    const byModel = new Map<string, { model: string; provider: string; source: string; isFallback: boolean }>();
    for (const e of activeConfig?.entries ?? []) {
      const row = { model: e.model, provider: e.provider, source: e.configSource, isFallback: !!e.isFallback };
      const existing = byModel.get(e.model);
      if (!existing || (existing.isFallback && !row.isFallback)) byModel.set(e.model, row);
    }
    return [...byModel.values()];
  }, [activeConfig]);

  type ModelRow = (typeof activeRows)[number];

  // The selection sent to the server: the slot's own source is the pin, so a
  // model only reachable under one tier resolves through that tier's credential.
  const optionForRow = (row: ModelRow): ModelOption => ({
    provider: row.provider,
    model: row.model,
    configSource: row.source,
    configName: activeConfig?.name,
  });

  // What a click stages, and what the panes mark, is whichever slot the current
  // mode is filling — the blanket model, or the tier being edited.
  const currentPick = mode === 'blanket' ? staged : stagedTier[activeTier];

  const handleRowPick = (row: ModelRow) => {
    const option = optionForRow(row);
    if (mode === 'blanket') setStaged(option);
    else setStagedTier({ ...stagedTier, [activeTier]: option });
  };

  const isRowSelected = (row: ModelRow): boolean => isSameModelOption(currentPick, optionForRow(row));

  // Lets the left pane mark which config holds the current pick, so the user
  // doesn't have to open each one to find it. Selections restored from older
  // conversations carry no configSource, so those match on the model instead.
  const configHasSelection = (c: ConfigEntry): boolean => {
    if (mode === 'config') return stagedConfig?.configSource === wholeConfigSourceFor(c.key);
    if (!currentPick) return false;
    if (currentPick.configSource) return configKeyForSource(currentPick.configSource) === c.key;
    return c.entries.some((e) => e.provider === currentPick.provider && e.model === currentPick.model);
  };

  const handleClearTier = (t: PickerTierKey) => {
    const next: TierModelMap = { ...stagedTier };
    delete next[t];
    setStagedTier(next);
  };

  // In Config mode the left pane is the selection, not just navigation.
  const handleConfigClick = (c: ConfigEntry) => {
    setActiveConfigKey(c.key);
    if (mode === 'config') setStagedConfig({ configSource: wholeConfigSourceFor(c.key), configName: c.name });
  };

  // The tier slots the active config actually defines, so Config mode can show
  // what "uses its tiers" will mean for this config instead of promising tiers
  // it doesn't have. A config with none resolves every call to its base model.
  const activeConfigTiers = useMemo(() => {
    const out: { tier: PickerTierKey; model: string }[] = [];
    for (const t of PICKER_TIER_KEYS) {
      const slot = (activeConfig?.entries ?? []).find((e) => e.configSource.endsWith(`:tier:${t}`) && !e.isFallback);
      if (slot) out.push({ tier: t, model: slot.model });
    }
    return out;
  }, [activeConfig]);

  // The model every untiered call falls back to — background work (titles,
  // memory) is never tier-tagged, so this is not a detail.
  const activeConfigBaseModel = activeConfig
    ? activeConfig.entries.find((e) => e.configSource === baseConfigSourceFor(activeConfig.key) && !e.isFallback)?.model
    : undefined;

  // Each mode commits something different, so each has its own empty state:
  // Blanket needs a model, Config needs a config, By-task may legitimately
  // apply an empty map (every tier cleared).
  const applyDisabled = (mode === 'blanket' && !staged) || (mode === 'config' && !stagedConfig);

  return (
    <>
      <Box
        ref={anchorRef}
        data-testid='model-picker-trigger'
        onClick={openPopover}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-1)',
          cursor: disabled ? 'default' : 'pointer',
          color: 'var(--ds-gray-600)',
          border: '0.5px solid var(--ds-gray-300)',
          borderRadius: 'var(--ds-radius-sm)',
          padding: 'var(--ds-space-1) var(--ds-space-2)',
          whiteSpace: 'nowrap',
          flexShrink: 0,
          '&:hover': disabled ? {} : { backgroundColor: 'var(--ds-gray-100)' },
        }}
      >
        <Typography
          sx={{
            fontSize: 'var(--ds-text-caption)',
            whiteSpace: 'nowrap',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            maxWidth: ds.space.mul(0, 40),
          }}
        >
          {pickerButtonLabel(selectedModel, selectedTierModels, credentials, selectedConfig, configEntries)}
        </Typography>
        <ArrowDropDownIcon sx={{ fontSize: 'var(--ds-text-title)' }} />
      </Box>
      {open && (
        <Popper
          open={open}
          anchorEl={anchorRef.current}
          placement='top-start'
          modifiers={[
            // Prefer opening upward, but fall back around the anchor rather than
            // running off the top — this surface is tall enough that the space
            // above the composer often isn't enough for it.
            { name: 'flip', enabled: true, options: { fallbackPlacements: ['bottom-start', 'top-end', 'bottom-end'], padding: 8 } },
            // altAxis lets it slide vertically to stay on screen; tether:false
            // allows it to detach from the anchor edge when that's the only way
            // to keep the whole surface visible.
            { name: 'preventOverflow', enabled: true, options: { padding: 8, altAxis: true, tether: false } },
          ]}
          sx={{ zIndex: 9999 }}
        >
          <ClickAwayListener onClickAway={() => setOpen(false)}>
            <Box
              data-testid='model-picker-popover'
              sx={{
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--ds-space-3)',
                padding: 'var(--ds-space-4)',
                border: 'var(--ds-popover-border, 1px solid var(--ds-gray-200))',
                borderRadius: 'var(--ds-radius-md)',
                backgroundColor: 'var(--ds-background-100)',
                boxShadow: 'var(--ds-overlay-shadow)',
                // ~560px: the two-pane list needs room for an endpoint column
                // plus model names without either side truncating.
                width: ds.space.mul(0, 280),
                // Never taller than the viewport. The list below is the only
                // flexible part, so it absorbs the shrink and keeps the mode
                // toggle, search box and Apply/Clear row always reachable.
                maxHeight: 'calc(100vh - 16px)',
                maxWidth: 'calc(100vw - 16px)',
              }}
            >
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                <Box sx={{ flex: 1 }}>
                  <ToggleGroup
                    size='sm'
                    selection='single'
                    value={mode}
                    onChange={(v) => setMode(v as 'blanket' | 'tier')}
                    ariaLabel='Model picker mode'
                    options={[
                      { value: 'blanket', label: 'All calls' },
                      { value: 'tier', label: 'By task' },
                      { value: 'config', label: 'Config' },
                    ]}
                  />
                </Box>
                <Tooltip
                  placement='top'
                  PopperProps={{ sx: { zIndex: 10000 }, modifiers: [{ name: 'preventOverflow', options: { padding: 8 } }] }}
                  title={PICKER_MODE_HELP[mode]}
                >
                  <InfoOutlinedIcon sx={{ fontSize: 16, color: 'var(--ds-gray-500)', cursor: 'help' }} />
                </Tooltip>
              </Box>

              {mode === 'tier' && (
                <ToggleGroup
                  size='sm'
                  selection='single'
                  value={activeTier}
                  onChange={(v) => setActiveTier(v as PickerTierKey)}
                  ariaLabel='Active task'
                  options={PICKER_TIER_KEYS.map((t) => ({ value: t, label: PICKER_TIER_LABELS[t] }))}
                />
              )}

              <Input
                size='sm'
                type='text'
                placeholder='Search configs and models…'
                value={search}
                onChange={(v) => setSearch(v)}
                aria-label='Search models'
              />

              {/* Two panes: configs on the left, the selected config's models on
                  the right. The config carries the credential, so naming it
                  first is what makes two identically-named models from
                  different configs distinguishable. */}
              <Box
                sx={{
                  display: 'flex',
                  // Grows with content up to ~280px, and shrinks below that on a
                  // short viewport (minHeight:0 is what allows a flex child to
                  // shrink past its content). The panes scroll inside it.
                  flex: '0 1 auto',
                  minHeight: 0,
                  maxHeight: `min(${ds.space.mul(0, 140)}, 40vh)`,
                  border: '1px solid var(--ds-gray-200)',
                  borderRadius: 'var(--ds-radius-md)',
                  overflow: 'hidden',
                }}
              >
                {filteredConfigs.length === 0 ? (
                  <Box
                    sx={{
                      flex: 1,
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      color: 'var(--ds-gray-500)',
                      fontSize: 'var(--ds-text-caption)',
                    }}
                  >
                    No models match
                  </Box>
                ) : (
                  <>
                    <Box
                      role='listbox'
                      aria-label='Configs'
                      data-testid='config-pane'
                      sx={{
                        width: ds.space.mul(0, 118),
                        flexShrink: 0,
                        overflowY: 'auto',
                        borderRight: '1px solid var(--ds-gray-200)',
                      }}
                    >
                      {filteredConfigs.map((cfg) => {
                        const isActive = activeConfig?.key === cfg.key;
                        const holdsSelection = configHasSelection(cfg);
                        return (
                          <MuiButtonBase
                            key={cfg.key}
                            role='option'
                            aria-selected={isActive}
                            onClick={() => handleConfigClick(cfg)}
                            sx={{
                              display: 'flex',
                              alignItems: 'center',
                              width: '100%',
                              textAlign: 'left',
                              gap: 'var(--ds-space-1)',
                              padding: 'var(--ds-space-2) var(--ds-space-3)',
                              borderLeft: `2px solid ${isActive ? 'var(--ds-blue-600)' : 'transparent'}`,
                              backgroundColor: isActive ? 'var(--ds-blue-100)' : 'transparent',
                              '&:hover': {
                                backgroundColor: isActive ? 'var(--ds-blue-100)' : 'var(--ds-overlay-item-hover-bg, var(--ds-gray-100))',
                              },
                            }}
                          >
                            <Typography
                              sx={{
                                flex: 1,
                                minWidth: 0,
                                fontSize: 'var(--ds-text-small)',
                                fontWeight: isActive ? 'var(--ds-font-weight-semibold)' : 400,
                                color: isActive ? 'var(--ds-blue-600)' : 'var(--ds-gray-700)',
                                whiteSpace: 'nowrap',
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                              }}
                            >
                              {cfg.name}
                            </Typography>
                            {holdsSelection && <CheckIcon sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-blue-600)', flexShrink: 0 }} />}
                          </MuiButtonBase>
                        );
                      })}
                    </Box>

                    {mode === 'config' ? (
                      /* Config mode selects on the left, so the right pane
                         answers "what will this config actually do" rather than
                         offering a second choice. */
                      <Box data-testid='config-summary-pane' sx={{ flex: 1, minWidth: 0, overflowY: 'auto', padding: 'var(--ds-space-3)' }}>
                        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', mb: ds.space[1] }}>
                          {activeConfigTiers.length > 0 ? 'Models this config uses per task' : 'This config sets no per-task models'}
                        </Typography>
                        {activeConfigTiers.map(({ tier, model }) => (
                          <Box key={tier} sx={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--ds-space-2)', py: ds.space[0] }}>
                            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)', fontWeight: 500 }}>
                              {PICKER_TIER_LABELS[tier]}
                            </Typography>
                            <Typography
                              sx={{
                                fontSize: 'var(--ds-text-small)',
                                color: 'var(--ds-gray-700)',
                                minWidth: 0,
                                whiteSpace: 'nowrap',
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                              }}
                            >
                              {model}
                            </Typography>
                          </Box>
                        ))}
                        {activeConfigBaseModel && (
                          <Box sx={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--ds-space-2)', py: ds.space[0] }}>
                            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>
                              {activeConfigTiers.length > 0 ? 'Everything else' : 'All calls'}
                            </Typography>
                            <Typography
                              sx={{
                                fontSize: 'var(--ds-text-small)',
                                color: 'var(--ds-gray-500)',
                                minWidth: 0,
                                whiteSpace: 'nowrap',
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                              }}
                            >
                              {activeConfigBaseModel}
                            </Typography>
                          </Box>
                        )}
                      </Box>
                    ) : (
                      <Box
                        role='listbox'
                        aria-label={mode === 'blanket' ? 'Models' : `Models for ${PICKER_TIER_LABELS[activeTier]}`}
                        data-testid='model-pane'
                        sx={{ flex: 1, minWidth: 0, overflowY: 'auto' }}
                      >
                        {activeRows.map((row) => {
                          const selected = isRowSelected(row);
                          return (
                            <MuiButtonBase
                              key={`${activeConfig?.key}-${row.model}`}
                              role='option'
                              aria-selected={selected}
                              onClick={() => handleRowPick(row)}
                              sx={{
                                display: 'flex',
                                alignItems: 'center',
                                justifyContent: 'space-between',
                                width: '100%',
                                textAlign: 'left',
                                padding: 'var(--ds-overlay-item-padding-md, var(--ds-space-2) var(--ds-space-3))',
                                gap: 'var(--ds-space-2)',
                                backgroundColor: selected ? 'var(--ds-overlay-item-selected-bg, var(--ds-blue-100))' : 'transparent',
                                '&:hover': {
                                  backgroundColor: selected
                                    ? 'var(--ds-overlay-item-selected-bg, var(--ds-blue-100))'
                                    : 'var(--ds-overlay-item-hover-bg, var(--ds-gray-100))',
                                },
                              }}
                            >
                              <Typography
                                sx={{
                                  minWidth: 0,
                                  fontSize: 'var(--ds-text-small)',
                                  fontWeight: selected ? 500 : 400,
                                  color: selected ? 'var(--ds-blue-600)' : 'var(--ds-gray-700)',
                                  whiteSpace: 'nowrap',
                                  overflow: 'hidden',
                                  textOverflow: 'ellipsis',
                                }}
                              >
                                {row.model}
                              </Typography>
                              {selected && <CheckIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-blue-600)', flexShrink: 0 }} />}
                            </MuiButtonBase>
                          );
                        })}
                      </Box>
                    )}
                  </>
                )}
              </Box>

              {mode === 'blanket' && staged && isLowerTierForReasoning(staged.provider, staged.model) && (
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    color: 'var(--ds-amber-700)',
                    lineHeight: 1.3,
                    mt: ds.space[1],
                  }}
                >
                  ⚠ Lighter models may struggle with multi-step planning. Consider a Pro model for All-calls mode.
                </Typography>
              )}

              {mode === 'tier' && (
                <Box
                  sx={{
                    display: 'flex',
                    flexDirection: 'column',
                    gap: ds.space[1],
                    padding: 'var(--ds-space-2) var(--ds-space-3)',
                    backgroundColor: 'var(--ds-gray-100)',
                    border: '1px solid var(--ds-gray-200)',
                    borderRadius: 'var(--ds-radius-sm)',
                  }}
                >
                  {PICKER_TIER_KEYS.map((t) => {
                    const cur = stagedTier[t];
                    const showWarn = t === 'reasoning' && cur && isLowerTierForReasoning(cur.provider, cur.model);
                    return (
                      <Box key={t} sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[0] }}>
                        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)' }}>
                          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-700)', fontWeight: 500 }}>
                            {PICKER_TIER_LABELS[t]}
                          </Typography>
                          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0 }}>
                            <Typography
                              sx={{
                                fontSize: 'var(--ds-text-small)',
                                color: cur ? 'var(--ds-gray-700)' : 'var(--ds-gray-500)',
                                whiteSpace: 'nowrap',
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                              }}
                            >
                              {/* The config is named too: two configs can serve
                                  the same model name, so the model alone doesn't
                                  say which credential the tier will use. */}
                              {cur ? `${cur.model}${cur.configName ? ` · ${cur.configName}` : ''}` : 'Inherit default'}
                            </Typography>
                            {cur && (
                              <MuiButtonBase
                                aria-label={`Clear ${PICKER_TIER_LABELS[t]}`}
                                onClick={() => handleClearTier(t)}
                                sx={{ padding: ds.space[0], borderRadius: '50%', '&:hover': { backgroundColor: 'var(--ds-gray-200)' } }}
                              >
                                <CloseIcon sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }} />
                              </MuiButtonBase>
                            )}
                          </Box>
                        </Box>
                        {showWarn && (
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-amber-700)', lineHeight: 1.3 }}>
                            ⚠ Lighter models may struggle with multi-step planning. Consider a Pro model for Reasoning.
                          </Typography>
                        )}
                      </Box>
                    );
                  })}
                </Box>
              )}

              <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-2)', mt: 'var(--ds-space-1)' }}>
                <Button size='sm' tone='secondary' onClick={handleClear}>
                  Clear all
                </Button>
                <Button size='sm' onClick={handleApply} disabled={applyDisabled}>
                  Apply
                </Button>
              </Box>
            </Box>
          </ClickAwayListener>
        </Popper>
      )}
    </>
  );
};

const AutoSuggestTextarea = React.forwardRef<HTMLTextAreaElement, AutoSuggestTextareaProps>(function AutoSuggestTextarea(
  {
    value,
    suggestionsAt,
    functionSuggestions = [],
    placeholder,
    maxLength,
    maxRows,
    onKeyDown,
    fontSize,
    fontWeight,
    buttonProperties,
    chatScreen = false,
    isFollowUp = false,
    disabled = false,
    allowStop = false,
    credentials = [],
    models = [],
    defaultModel: _defaultModel,
    selectedModel,
    onModelSelect,
    selectedTierModels,
    onTierModelsSelect,
    selectedConfig,
    onConfigSelect,
    onConfigClear,
    popupInitial = false,
    imageSupport,
    externalAgentsLoading = false,
    submitOnModEnter = false,
  },
  forwardedRef
) {
  const [text, setText] = useState('');
  const [showSuggestions, setShowSuggestions] = useState(false);
  const [anchorEl, setAnchorEl] = useState<null | HTMLElement>(null);
  const [filteredSuggestions, setFilteredSuggestions] = useState<{ name: string; display_name: string }[]>([]);
  const [filteredFunctions, setFilteredFunctions] = useState<{ name: string; description: string; variables?: any; variable_defaults?: any }[]>([]);
  const [selectedIndex, setSelectedIndex] = useState(-1);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  // Point both the internal ref (used for focus/anchor positioning below) and any
  // ref forwarded by the parent (which calls `.focus()`) at the same DOM textarea.
  const mergedTextareaRef = useForkRef(textareaRef, forwardedRef);
  const [suggestionsTrigger, setSuggestionsTrigger] = useState<'at' | 'button' | 'call'>('at');
  const [selectedAgent, setSelectedAgent] = useState<string | null>(null);
  const agentButtonRef = useRef<HTMLDivElement | null>(null);
  const [attachedImages, setAttachedImages] = useState<AttachedImage[]>([]);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const imagesEnabled = !!imageSupport?.enabled;
  const allowedMimeTypes = imageSupport?.allowedMimeTypes ?? [];
  const maxPerMessage = imageSupport?.maxPerMessage ?? 0;
  const maxSizeMb = imageSupport?.maxSizeMb ?? 0;

  // Validate + read selected/pasted files into base64 attachments. Limits
  // mirror the server's advertised image_support so we fail fast with a clear
  // message instead of letting the request 400 server-side.
  //
  // `scheduled` counts files synchronously queued for read so a single
  // multi-file paste/drop can't bypass `maxPerMessage`; the async setter also
  // re-checks against `cur` to close the race when a second batch fires
  // before the first batch's readers resolve.
  const addFiles = (files: File[]) => {
    if (!imagesEnabled || files.length === 0) return;
    let scheduled = attachedImages.length;
    for (const file of files) {
      if (maxPerMessage > 0 && scheduled >= maxPerMessage) {
        snackbar.error(`You can attach at most ${maxPerMessage} image${maxPerMessage === 1 ? '' : 's'} per message.`);
        break;
      }
      if (allowedMimeTypes.length > 0 && !allowedMimeTypes.includes(file.type)) {
        snackbar.error(`Unsupported image type "${file.type || 'unknown'}". Allowed: ${allowedMimeTypes.join(', ')}.`);
        continue;
      }
      if (maxSizeMb > 0 && file.size > maxSizeMb * 1024 * 1024) {
        snackbar.error(`"${file.name || 'image'}" exceeds the ${maxSizeMb} MB limit.`);
        continue;
      }
      scheduled++;
      const reader = new FileReader();
      const id = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      reader.onload = () => {
        const result = typeof reader.result === 'string' ? reader.result : '';
        // strip "data:<mime>;base64," prefix — backend wants raw base64
        const base64 = result.includes(',') ? result.slice(result.indexOf(',') + 1) : result;
        if (!base64) return;
        setAttachedImages((cur) => {
          if (maxPerMessage > 0 && cur.length >= maxPerMessage) return cur;
          if (cur.some((img) => img.id === id)) return cur;
          return [...cur, { id, name: file.name || 'image', data: base64, mime_type: file.type }];
        });
      };
      reader.onerror = () => snackbar.error(`Failed to read "${file.name || 'image'}".`);
      reader.readAsDataURL(file);
    }
  };

  const removeImage = (id: string) => setAttachedImages((prev) => prev.filter((img) => img.id !== id));

  const handlePaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    if (!imagesEnabled) return;
    const imageFiles = Array.from(e.clipboardData?.files ?? []).filter((f) => f.type.startsWith('image/'));
    if (imageFiles.length > 0) {
      e.preventDefault();
      addFiles(imageFiles);
    }
  };

  // Single send path used by every submit affordance so text + image clearing
  // and the outgoing payload stay consistent.
  const handleSend = () => {
    const config = selectedModel
      ? {
          llm_provider: selectedModel.provider,
          llm_model_name: selectedModel.model,
          ...(selectedModel.configSource && { llm_config_source: selectedModel.configSource }),
        }
      : undefined;
    const images: OutgoingImage[] = attachedImages.map(({ data, mime_type }) => ({ data, mime_type }));
    buttonProperties.onClick(text, config, images.length ? images : undefined);
    setText('');
    setAttachedImages([]);
  };

  const buildFunctionCall = (selectedFunction: { name: string; variables?: any; variable_defaults?: any }) => {
    let functionCall = `/call ${selectedFunction.name}`;
    if (selectedFunction.variables && selectedFunction.variables.length > 0) {
      const paramPairs = selectedFunction.variables.map((variable: string) => {
        const defaultValue = selectedFunction.variable_defaults?.[variable] || '';
        return `${variable}="${defaultValue}"`;
      });
      functionCall += ` ${paramPairs.join(' ')}`;
    }
    return functionCall;
  };

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const value = e.target.value;
    setText(value);

    // Handle @agent suggestions
    const atMatch = /^@(\w+)/.exec(value);
    const typedAgent = atMatch ? atMatch[1].trim().toLowerCase() : '';
    const matchedAgents = suggestionsAt.filter(
      (suggest) => suggest.name.toLowerCase().startsWith(typedAgent) || suggest.display_name.toLowerCase().startsWith(typedAgent)
    );

    // Handle /call function suggestions
    const callMatch = /^\/call(?:\s+(\w*))?/.exec(value);
    const typedFunction = callMatch && callMatch[1] ? callMatch[1].trim().toLowerCase() : '';
    const matchedFunctions = functionSuggestions.filter((func) => func.name.toLowerCase().startsWith(typedFunction));

    // Check if function name is complete and parameters are present
    const hasCompleteFunction = /^\/call\s+(\w+)(?:\s+\w+="[^"]*")*/.test(value);
    const hasParametersInText = /^\/call\s+\w+\s+\w+="/.test(value);

    if (value.startsWith('@') && suggestionsAt.length > 0 && matchedAgents.length > 0) {
      setSuggestionsTrigger('at');
      setFilteredSuggestions(matchedAgents);
      setFilteredFunctions([]);
      setShowSuggestions(true);
      setSelectedIndex(-1);
      const isSuggestionPresent = matchedAgents.some(
        (suggest) => suggest.name.toLowerCase() === typedAgent || suggest.display_name.toLowerCase() === typedAgent
      );
      if (isSuggestionPresent) {
        setShowSuggestions(false);
      }
      setAnchorEl(textareaRef.current);
    } else if (value.startsWith('/call') && functionSuggestions.length > 0) {
      setSuggestionsTrigger('call');
      setFilteredSuggestions([]);

      // Only show suggestions if function name is not complete or has no parameters yet
      if (!hasCompleteFunction || (typedFunction !== '' && !hasParametersInText)) {
        const functionsToShow = typedFunction === '' ? functionSuggestions : matchedFunctions;
        setFilteredFunctions(functionsToShow);
        setShowSuggestions(true);
        setSelectedIndex(-1);
        setAnchorEl(textareaRef.current);
      } else {
        setShowSuggestions(false);
      }
    } else {
      setShowSuggestions(false);
    }
  };

  const handleSelectSuggestion = (suggest: string) => {
    if (suggestionsTrigger === 'at') {
      const atIndex = text.indexOf('@');
      if (atIndex !== -1) {
        const beforeAt = text.substring(0, atIndex);
        const afterAtPattern = text.substring(atIndex).match(/^@\w*/);
        const afterAtEnd = afterAtPattern ? atIndex + afterAtPattern[0].length : atIndex + 1;
        const afterReplacement = text.substring(afterAtEnd);
        setText(beforeAt + `@${suggest}` + afterReplacement);
      } else {
        setText(`@${suggest} `);
      }
      setSelectedAgent(suggest);
    } else if (suggestionsTrigger === 'button') {
      setText(`@${suggest} `);
      setSelectedAgent(suggest);
    } else if (suggestionsTrigger === 'call') {
      // Find the selected function details
      const selectedFunc = filteredFunctions.find((func) => func.name === suggest);
      if (selectedFunc) {
        const callIndex = text.indexOf('/call');
        if (callIndex !== -1) {
          const beforeCall = text.substring(0, callIndex);
          const afterCallPattern = text.substring(callIndex).match(/^\/call\s*\w*/);
          const afterCallEnd = afterCallPattern ? callIndex + afterCallPattern[0].length : callIndex + 5;
          const afterReplacement = text.substring(afterCallEnd);
          setText(beforeCall + buildFunctionCall(selectedFunc) + afterReplacement);
        } else {
          setText(buildFunctionCall(selectedFunc) + ' ');
        }
      }
    }
    setShowSuggestions(false);
    setSelectedIndex(-1);
    setTimeout(() => {
      textareaRef.current?.focus();
    }, 0);
  };

  useEffect(() => {
    setText(value);
  }, [value]);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (submitOnModEnter && e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      if (!(isFollowUp && allowStop) && text.trim() && buttonProperties.enable) {
        handleSend();
      }
      return;
    }
    if (showSuggestions) {
      const currentList = suggestionsTrigger === 'call' ? filteredFunctions : filteredSuggestions;
      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault();
          setSelectedIndex((prev) => (prev < currentList.length - 1 ? prev + 1 : 0));
          break;
        case 'ArrowUp':
          e.preventDefault();
          setSelectedIndex((prev) => (prev > 0 ? prev - 1 : currentList.length - 1));
          break;
        case 'Enter':
          e.preventDefault();
          if (selectedIndex >= 0) {
            if (suggestionsTrigger === 'call') {
              handleSelectSuggestion(filteredFunctions[selectedIndex].name);
            } else {
              handleSelectSuggestion(filteredSuggestions[selectedIndex].name);
            }
            return;
          }
          break;
        case 'Escape':
          setShowSuggestions(false);
          setSelectedIndex(-1);
          break;
      }
    }
    onKeyDown?.(e);
  };

  const clearSelectedAgent = () => {
    if (selectedAgent) {
      setText('');
    }
    setSelectedAgent(null);
  };

  const handleButtonClick = () => {
    setSuggestionsTrigger('button');
    setFilteredSuggestions(suggestionsAt);
    setShowSuggestions(!showSuggestions);
    setAnchorEl(agentButtonRef.current || textareaRef.current);
    setSelectedIndex(-1);
    setTimeout(() => {
      textareaRef.current?.focus();
    }, 0);
  };

  useEffect(() => {
    if (text.startsWith('@')) {
      const match = text.match(/^@(\w+)/);
      if (match) {
        const typedAgent = match[1];
        const filteredValue = suggestionsAt.find((suggest) => suggest.name === typedAgent);
        if (filteredValue) {
          setSelectedAgent(typedAgent);
        }
      }
    } else if (selectedAgent) {
      setSelectedAgent(null);
    }
  }, [text, suggestionsAt]);

  return (
    <Box sx={{ width: '100%', display: 'flex', flexDirection: popupInitial ? 'column' : chatScreen ? 'row' : 'column' }}>
      <div style={{ position: 'relative', flex: chatScreen ? '1' : undefined, width: '100%' }}>
        <Textarea
          ref={mergedTextareaRef}
          fontSize={fontSize}
          fontWeight={fontWeight}
          value={text}
          placeholder={placeholder}
          onChange={handleChange}
          maxRows={maxRows}
          maxLength={maxLength}
          onKeyDown={handleKeyDown}
          onPaste={handlePaste}
          sx={{
            maxHeight: `calc(${maxRows} * ${ds.space[5]})`,
            overflowY: 'auto',
            '::placeholder': {
              color: ds.gray[400],
            },
            '&:disabled': {
              opacity: 0.5,
            },
          }}
          disabled={disabled}
        />

        {showSuggestions && (
          <Popper
            open={showSuggestions}
            anchorEl={anchorEl}
            placement={suggestionsTrigger === 'button' ? 'top-start' : isFollowUp ? 'top-start' : 'bottom-start'}
            sx={{ zIndex: 9999 }}
            modifiers={[
              {
                name: 'offset',
                options: {
                  offset: [0, suggestionsTrigger === 'button' ? 8 : isFollowUp ? 8 : 80],
                },
              },
              {
                name: 'preventOverflow',
                options: {
                  boundary: 'viewport',
                  padding: 8,
                },
              },
              {
                name: 'flip',
                options: {
                  fallbackPlacements: ['bottom-start', 'top-start'],
                },
              },
            ]}
          >
            <ClickAwayListener
              onClickAway={() => {
                setShowSuggestions(false);
                setSelectedIndex(-1);
              }}
            >
              <Box
                sx={{
                  display: 'grid',
                  gridTemplateColumns:
                    (suggestionsTrigger === 'call' ? filteredFunctions.length : filteredSuggestions.length) <= 3 ? '1fr' : 'repeat(3, 1fr)',
                  gap: 'var(--ds-space-1)',
                  padding: 'var(--ds-space-2)',
                  border: '1px solid var(--ds-blue-300)',
                  borderRadius: 'var(--ds-radius-sm)',
                  backgroundColor: 'var(--ds-background-100)',
                  width:
                    (suggestionsTrigger === 'call' ? filteredFunctions.length : filteredSuggestions.length) <= 3
                      ? ds.space.mul(0, 100)
                      : ds.space.mul(0, 280),
                  maxHeight: ds.space.mul(0, 119),
                  overflowY: 'auto',
                  '&::-webkit-scrollbar': {
                    width: ds.space[1],
                    borderRadius: 'var(--ds-radius-lg)',
                  },
                  '@media (max-width: 1100px)': {
                    width:
                      (suggestionsTrigger === 'call' ? filteredFunctions.length : filteredSuggestions.length) <= 3
                        ? ds.space.mul(0, 90)
                        : ds.space.mul(0, 245),
                  },
                }}
              >
                {suggestionsTrigger === 'call'
                  ? filteredFunctions.map((func, index) => (
                      <Box
                        key={func.name}
                        sx={{
                          display: 'flex',
                          flexDirection: 'column',
                          alignItems: 'flex-start',
                          gap: 'var(--ds-space-1)',
                          padding: 'var(--ds-space-2)',
                          cursor: 'pointer',
                          textAlign: 'left',
                          backgroundColor: selectedIndex === index ? ds.gray[100] : 'transparent',
                          '&:hover': { backgroundColor: 'var(--ds-blue-100)', borderRadius: 'var(--ds-radius-sm)', color: ds.blue[500] },
                          fontSize: 'var(--ds-text-small)',
                          fontWeight: 'var(--ds-font-weight-regular)',
                          color: ds.gray[700],
                          '@media (max-width: 1300px)': {
                            fontSize: 'var(--ds-text-caption)',
                          },
                        }}
                        onClick={() => handleSelectSuggestion(func.name)}
                      >
                        <Typography sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: ds.blue[500], fontSize: 'var(--ds-text-small)' }}>
                          {func.name}
                        </Typography>
                        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], lineHeight: 1.2 }}>{func.description}</Typography>
                        {func.variables && func.variables.length > 0 && (
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[700], fontStyle: 'italic' }}>
                            {func.variables.length} parameter{func.variables.length !== 1 ? 's' : ''}
                          </Typography>
                        )}
                      </Box>
                    ))
                  : filteredSuggestions.map((suggest, index) => (
                      <Box
                        key={suggest.name}
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: 'var(--ds-space-2)',
                          padding: 'var(--ds-space-2)',
                          cursor: 'pointer',
                          textAlign: 'left',
                          backgroundColor: selectedIndex === index ? ds.gray[100] : 'transparent',
                          '&:hover': { backgroundColor: 'var(--ds-blue-100)', borderRadius: 'var(--ds-radius-sm)', color: ds.blue[500] },
                          fontSize: 'var(--ds-text-small)',
                          fontWeight: 'var(--ds-font-weight-regular)',
                          color: ds.gray[700],
                          '@media (max-width: 1300px)': {
                            fontSize: 'var(--ds-text-caption)',
                            '& img': {
                              width: ds.space.mul(0, 7),
                              height: ds.space.mul(0, 7),
                            },
                          },
                        }}
                        onClick={() => handleSelectSuggestion(suggest.name)}
                      >
                        {getIcon(suggest.name) ? (
                          <SafeIcon src={getIcon(suggest.name)?.default || CustomAgentBlueIcon} alt='agent icon' width={20} height={20} />
                        ) : (
                          <Avatar
                            style={{
                              width: ds.space[3],
                              height: ds.space[3],
                              border: `1px solid ${ds.blue[400]}`,
                              color: `${ds.blue[400]}`,
                              backgroundColor: ds.background[100],
                              fontSize: 'var(--ds-text-small)',
                              fontWeight: 'var(--ds-font-weight-medium)',
                              borderRadius: 'var(--ds-radius-sm)',
                              padding: 0,
                            }}
                          >
                            {suggest.name[0].toUpperCase()}
                          </Avatar>
                        )}
                        {suggest.display_name}
                      </Box>
                    ))}
              </Box>
            </ClickAwayListener>
          </Popper>
        )}
      </div>
      {chatScreen && (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0 }}>
          {/* Model Selector for chat screen — popover supports both
              "Blanket" (one model) and "Per category" (one model per tier)
              modes; mutually exclusive at the hook level. */}
          {(models.length > 0 || credentials.length > 0) && (
            <>
              <Box sx={{ width: '1px', height: ds.space[5], backgroundColor: 'var(--ds-brand-200)', mx: 'var(--ds-space-3)' }} />
              <ModelPickerPopover
                credentials={credentials}
                models={models}
                selectedModel={selectedModel}
                onModelSelect={onModelSelect}
                selectedTierModels={selectedTierModels}
                onTierModelsSelect={onTierModelsSelect}
                selectedConfig={selectedConfig}
                onConfigSelect={onConfigSelect}
                onConfigClear={onConfigClear}
                disabled={disabled}
              />
            </>
          )}
          <Box sx={{ width: '1px', height: ds.space[5], backgroundColor: 'var(--ds-brand-200)', mx: 'var(--ds-space-3)' }} />
          <Button
            size='md'
            onClick={() => {
              if (isFollowUp && allowStop) {
                buttonProperties.onClickStop();
              } else {
                handleSend();
              }
            }}
            icon={
              isFollowUp && allowStop ? <StopIcon sx={{ color: 'white' }} /> : <SafeIcon src={ArrowRightWhiteIcon} alt='' width={18} height={18} />
            }
            aria-label={isFollowUp && allowStop ? 'Stop' : 'Send'}
            disabled={!(isFollowUp && allowStop) && (!text || !buttonProperties.enable)}
          />
        </Box>
      )}

      {imagesEnabled && attachedImages.length > 0 && (
        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-2)', mt: 'var(--ds-space-2)' }}>
          {attachedImages.map((img) => (
            <Box
              key={img.id}
              title={img.name}
              sx={{
                position: 'relative',
                width: ds.space.mul(0, 28),
                height: ds.space.mul(0, 28),
                borderRadius: 'var(--ds-radius-md)',
                overflow: 'hidden',
                border: `1px solid var(--ds-gray-200)`,
                backgroundColor: 'var(--ds-gray-100)',
              }}
            >
              <Box
                component='img'
                src={`data:${img.mime_type};base64,${img.data}`}
                alt={img.name}
                sx={{ width: '100%', height: '100%', objectFit: 'cover', display: 'block' }}
              />
              <Box
                role='button'
                aria-label={`Remove ${img.name}`}
                data-testid={`remove-attached-image-${img.id}`}
                onClick={() => removeImage(img.id)}
                sx={{
                  position: 'absolute',
                  top: ds.space[0],
                  right: ds.space[0],
                  width: ds.space[4],
                  height: ds.space[4],
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  borderRadius: '50%',
                  backgroundColor: 'color-mix(in srgb, var(--ds-gray-700) 55%, transparent)',
                  cursor: 'pointer',
                  '&:hover': { backgroundColor: 'color-mix(in srgb, var(--ds-gray-700) 80%, transparent)' },
                }}
              >
                <CloseIcon sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-background-100)' }} />
              </Box>
            </Box>
          ))}
        </Box>
      )}

      {buttonProperties.show && !chatScreen ? (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
            mt: 'var(--ds-space-1)',
            pt: 'var(--ds-space-1)',
          }}
        >
          {imagesEnabled && (
            <>
              <input
                ref={fileInputRef}
                type='file'
                accept={allowedMimeTypes.length > 0 ? allowedMimeTypes.join(',') : 'image/*'}
                multiple
                style={{ display: 'none' }}
                onChange={(e) => {
                  addFiles(Array.from(e.target.files ?? []));
                  e.target.value = '';
                }}
                data-testid='chat-image-file-input'
              />
              <Box
                role='button'
                aria-label='Attach image'
                data-testid='chat-attach-image-btn'
                onClick={() => fileInputRef.current?.click()}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  cursor: 'pointer',
                  p: 'var(--ds-space-1)',
                  borderRadius: 'var(--ds-radius-sm)',
                  '&:hover': { backgroundColor: 'var(--ds-gray-100)' },
                }}
              >
                <AttachFileIcon sx={{ fontSize: 'var(--ds-text-title)', color: ds.gray[600] }} />
              </Box>
            </>
          )}
          {/* Agent Selector */}
          <Box
            ref={agentButtonRef}
            sx={{
              display: 'flex',
              alignItems: 'center',
              color: suggestionsAt.length === 0 ? ds.gray[400] : ds.gray[600],
              border: `0.5px solid ${ds.gray[300]}`,
              borderRadius: 'var(--ds-radius-sm)',
              padding: 'var(--ds-space-1) var(--ds-space-2)',
              gap: 'var(--ds-space-1)',
              cursor: suggestionsAt.length === 0 ? 'default' : 'pointer',
              whiteSpace: 'nowrap',
              flexShrink: 0,
            }}
            onClick={suggestionsAt.length === 0 ? undefined : handleButtonClick}
          >
            {selectedAgent ? (
              <>
                {getIcon(selectedAgent) ? (
                  <SafeIcon src={getIcon(selectedAgent)?.default} alt='agent icon' width={14} height={14} />
                ) : (
                  <Avatar
                    style={{
                      width: ds.space[4],
                      height: ds.space[4],
                      border: `1px solid ${ds.blue[400]}`,
                      color: `${ds.blue[400]}`,
                      backgroundColor: ds.background[100],
                      fontSize: 'var(--ds-text-caption)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      borderRadius: 'var(--ds-radius-sm)',
                    }}
                  >
                    {selectedAgent[0].toUpperCase()}
                  </Avatar>
                )}
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                    maxWidth: ds.space.mul(0, 30),
                  }}
                >
                  {selectedAgent}
                </Typography>
                <Box
                  component='span'
                  sx={{
                    color: ds.gray[700],
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'bold',
                    flexShrink: 0,
                    lineHeight: 1,
                    '&:hover': { color: ds.blue[500] },
                  }}
                  onClick={(e) => {
                    e.stopPropagation();
                    clearSelectedAgent();
                  }}
                >
                  ✕
                </Box>
              </>
            ) : suggestionsAt.length === 0 ? (
              externalAgentsLoading ? (
                <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[400], fontStyle: 'italic' }}>Loading agents...</Typography>
              ) : (
                <Tooltip title='You can add an agent in Settings' variant='default' placement='top'>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[400] }}>No agent connected</Typography>
                </Tooltip>
              )
            ) : (
              <>
                <Typography sx={{ fontSize: 'var(--ds-text-caption)' }}>Agent</Typography>
                <ArrowDropDownIcon sx={{ fontSize: 'var(--ds-text-title)' }} />
              </>
            )}
          </Box>
          <Box sx={{ width: '1px', height: ds.space.mul(0, 9), backgroundColor: 'var(--ds-gray-200)', flexShrink: 0 }} />
          {/* Model Selector — popover variant for non-chat (popup) flow.
              Same component as the chat-screen path; renders the same two
              modes (Blanket / Per category) with mutual exclusivity. */}
          {(models.length > 0 || credentials.length > 0) && (
            <ModelPickerPopover
              credentials={credentials}
              models={models}
              selectedModel={selectedModel}
              onModelSelect={onModelSelect}
              selectedTierModels={selectedTierModels}
              onTierModelsSelect={onTierModelsSelect}
              selectedConfig={selectedConfig}
              onConfigSelect={onConfigSelect}
              onConfigClear={onConfigClear}
              disabled={disabled}
            />
          )}
          <Box sx={{ flex: 1 }} />
          {text.length >= maxLength * 0.8 && (
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                color: text.length >= maxLength * 0.9 ? ds.amber[700] : ds.gray[700],
                mr: 'var(--ds-space-2)',
                whiteSpace: 'nowrap',
              }}
              data-testid='ask-nudgebee-prompt-char-counter'
            >
              {text.length.toLocaleString()} / {maxLength.toLocaleString()}
            </Typography>
          )}
          {/* Submit / Stop button */}
          <Button
            id='set-config-btn'
            size='md'
            onClick={() => {
              if (isFollowUp && allowStop) {
                buttonProperties.onClickStop();
              } else {
                handleSend();
              }
            }}
            icon={
              isFollowUp && allowStop ? <StopIcon sx={{ color: 'white' }} /> : <SafeIcon src={ArrowRightWhiteIcon} alt='' width={18} height={18} />
            }
            aria-label={isFollowUp && allowStop ? 'Stop' : 'Send'}
            disabled={!(isFollowUp && allowStop) && (!text || !buttonProperties.enable)}
          />
        </Box>
      ) : null}
    </Box>
  );
});

export default AutoSuggestTextarea;
