import React, { useState } from 'react';
import { Box, Typography, Chip } from '@mui/material';
import { Button } from '@components1/ds/Button';
import { Add, ExpandMore, ExpandLess } from '@mui/icons-material';
import { Input } from '@components1/ds/Input';
import { colors } from 'src/utils/colors';
import { HOOKS_PRESETS, FIELD_HELPER_TEXT } from './advancedConfigPresets';
import { useJsonViewMode } from '@components1/workflow/hooks/useJsonViewMode';
import FieldHeader from './FieldHeader';
import JsonTextArea from './JsonTextArea';
import { DeleteIconRed } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';

interface HookAction {
  type: string;
  params?: Record<string, unknown>;
}

interface Hooks {
  success?: HookAction[];
  failure?: HookAction[];
  always?: HookAction[];
}

interface HooksFieldProps {
  value: Hooks | undefined;
  onChange: (value: Hooks | undefined) => void;
  disabled?: boolean;
}

const HOOK_TYPES = [
  { label: 'On Success', key: 'success' as const, color: 'var(--ds-teal-500)' },
  { label: 'On Failure', key: 'failure' as const, color: 'var(--ds-red-500)' },
  { label: 'On Complete', key: 'always' as const, color: 'var(--ds-purple-400)' },
];

const HookActionEditor: React.FC<{
  action: HookAction;
  onChange: (action: HookAction) => void;
  onDelete: () => void;
  disabled?: boolean;
}> = ({ action, onChange, onDelete, disabled }) => {
  const [paramsJson, setParamsJson] = useState(action.params ? JSON.stringify(action.params, null, 2) : '{}');
  const [paramsError, setParamsError] = useState('');

  const handleParamsChange = (json: string) => {
    setParamsJson(json);
    try {
      const parsed = JSON.parse(json);
      setParamsError('');
      onChange({ ...action, params: parsed });
    } catch (e) {
      console.error(e);
      setParamsError('Invalid JSON');
    }
  };

  return (
    <Box
      sx={{
        p: 1.5,
        border: `1px solid ${colors.lowestLight}`,
        borderRadius: 1,
        bgcolor: 'white',
        display: 'flex',
        flexDirection: 'column',
        gap: 1,
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <Box sx={{ flex: 1 }}>
          <Input
            size='sm'
            label='Task Type'
            value={action.type ?? ''}
            onChange={(next) => onChange({ ...action, type: next })}
            disabled={disabled}
            placeholder='e.g., notifications.im'
          />
        </Box>
        <Button
          composition='icon-only'
          tone='ghost'
          size='sm'
          aria-label='Delete hook'
          icon={<SafeIcon src={DeleteIconRed} alt='delete' width={16} height={16} />}
          disabled={disabled}
          onClick={onDelete}
        />
      </Box>
      <Input
        size='sm'
        label='Parameters (JSON)'
        type='textarea'
        minRows={2}
        maxRows={6}
        value={paramsJson}
        onChange={handleParamsChange}
        disabled={disabled}
        error={paramsError || undefined}
      />
    </Box>
  );
};

const HookSection: React.FC<{
  label: string;
  color: string;
  actions: HookAction[];
  onChange: (actions: HookAction[]) => void;
  disabled?: boolean;
}> = ({ label, color, actions, onChange, disabled }) => {
  const [expanded, setExpanded] = useState(actions.length > 0);

  const handleAddAction = () => {
    onChange([...actions, { type: '', params: {} }]);
    setExpanded(true);
  };

  const handleActionChange = (index: number, action: HookAction) => {
    const newActions = [...actions];
    newActions[index] = action;
    onChange(newActions);
  };

  const handleDeleteAction = (index: number) => {
    onChange(actions.filter((_, i) => i !== index));
  };

  return (
    <Box
      sx={{
        border: `1px solid color-mix(in srgb, ${color} 20%, transparent)`,
        borderRadius: 1,
        overflow: 'hidden',
      }}
    >
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          px: 1.5,
          py: 1,
          bgcolor: `color-mix(in srgb, ${color} 10%, transparent)`,
          cursor: 'pointer',
        }}
        onClick={() => setExpanded(!expanded)}
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <Box sx={{ width: 8, height: 8, borderRadius: '50%', bgcolor: color }} />
          <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}>
            {label}
          </Typography>
          {actions.length > 0 && (
            <Chip label={actions.length} size='small' sx={{ height: 18, fontSize: 'var(--ds-text-caption)', bgcolor: color, color: 'white' }} />
          )}
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
          <Button
            tone='ghost'
            size='xs'
            icon={<Add sx={{ fontSize: 14 }} />}
            disabled={disabled}
            onClick={(e) => {
              e.stopPropagation();
              handleAddAction();
            }}
          >
            Add
          </Button>
          {expanded ? <ExpandLess sx={{ fontSize: 16 }} /> : <ExpandMore sx={{ fontSize: 16 }} />}
        </Box>
      </Box>
      {expanded && (
        <Box sx={{ p: 1.5, display: 'flex', flexDirection: 'column', gap: 1 }}>
          {actions.length === 0 ? (
            <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: colors.text.secondary, fontStyle: 'italic', textAlign: 'center', py: 1 }}>
              No actions configured
            </Typography>
          ) : (
            actions.map((action, index) => (
              <HookActionEditor
                key={index}
                action={action}
                onChange={(newAction) => handleActionChange(index, newAction)}
                onDelete={() => handleDeleteAction(index)}
                disabled={disabled}
              />
            ))
          )}
        </Box>
      )}
    </Box>
  );
};

const HooksField: React.FC<HooksFieldProps> = ({ value, onChange, disabled = false }) => {
  const { viewMode, setViewMode, jsonValue, jsonError, copied, handleJsonChange, handleCopy } = useJsonViewMode({ value, onChange });

  const handleHookChange = (hookKey: keyof Hooks, actions: HookAction[]) => {
    const newValue = { ...value };
    if (actions.length > 0) {
      newValue[hookKey] = actions;
    } else {
      delete newValue[hookKey];
    }
    if (Object.keys(newValue).length === 0) {
      onChange(undefined);
    } else {
      onChange(newValue);
    }
  };

  return (
    <Box>
      <FieldHeader
        label='Task Hooks'
        viewMode={viewMode}
        onViewModeChange={setViewMode}
        copied={copied}
        onCopy={handleCopy}
        presets={HOOKS_PRESETS}
        onPresetClick={(preset) => onChange(preset.value as Hooks)}
        disabled={disabled}
      />

      {viewMode === 'structured' ? (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
          {HOOK_TYPES.map((hook) => (
            <HookSection
              key={hook.key}
              label={hook.label}
              color={hook.color}
              actions={value?.[hook.key] || []}
              onChange={(actions) => handleHookChange(hook.key, actions)}
              disabled={disabled}
            />
          ))}
        </Box>
      ) : (
        <JsonTextArea
          value={jsonValue}
          onChange={handleJsonChange}
          error={jsonError}
          helperText={FIELD_HELPER_TEXT.hooks}
          placeholder={JSON.stringify({ success: [{ type: 'notifications.im', params: {} }] }, null, 2)}
          disabled={disabled}
          minRows={5}
          maxRows={15}
        />
      )}
    </Box>
  );
};

export default HooksField;
