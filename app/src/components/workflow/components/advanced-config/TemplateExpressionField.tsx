import React, { useState, useEffect } from 'react';
import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import { ContentCopy, Check, KeyboardArrowDown, Code } from '@mui/icons-material';
import { Input } from '@ui/Input';
import { DropdownMenu, type DropdownMenuItem } from '@ui/DropdownMenu';
import { CONDITIONAL_PRESETS, FIELD_HELPER_TEXT, FIELD_PLACEHOLDERS } from './advancedConfigPresets';
import { useCopyToClipboard } from '@components/workflow/hooks/useCopyToClipboard';

interface PreviousTask {
  id: string;
  name: string;
  type: string;
}

interface TemplateExpressionFieldProps {
  label: string;
  value: string | undefined;
  onChange: (value: string) => void;
  disabled?: boolean;
  previousTasks?: PreviousTask[];
  customHelperText?: string;
}

const TemplateExpressionField: React.FC<TemplateExpressionFieldProps> = ({
  label,
  value,
  onChange,
  disabled = false,
  previousTasks = [],
  customHelperText,
}) => {
  const [localValue, setLocalValue] = useState(value || '');
  const { copied, copy } = useCopyToClipboard();

  const helperText = customHelperText || FIELD_HELPER_TEXT.if || '';
  const placeholder = FIELD_PLACEHOLDERS.if || '';

  useEffect(() => {
    setLocalValue(value || '');
  }, [value]);

  const handleChange = (newValue: string) => {
    setLocalValue(newValue);
    onChange(newValue);
  };

  const handleCopy = async () => {
    await copy(localValue);
  };

  const handlePresetSelect = (presetValue: string) => {
    setLocalValue(presetValue);
    onChange(presetValue);
  };

  const handleTaskSelect = (task: PreviousTask) => {
    const template = `{{ .Tasks.${task.id}.output }}`;
    const newValue = localValue ? `${localValue} ${template}` : template;
    setLocalValue(newValue);
    onChange(newValue);
  };

  const insertTemplate = (template: string) => {
    const newValue = localValue ? `${localValue} ${template}` : template;
    setLocalValue(newValue);
    onChange(newValue);
  };

  // Monospace secondary line for template/expression values (optional color).
  const mono = (text: string, color?: string) => (
    <Box component='span' sx={{ fontFamily: 'var(--ds-font-mono)', ...(color ? { color } : {}) }}>
      {text}
    </Box>
  );

  const presetItems: DropdownMenuItem[] = CONDITIONAL_PRESETS.map((preset) => ({
    label: preset.label,
    description: mono(preset.value as string, 'var(--ds-brand-500)'),
    onSelect: () => handlePresetSelect(preset.value as string),
  }));

  const taskMenuItems: DropdownMenuItem[] = [
    { type: 'section', label: 'Previous Tasks' },
    ...previousTasks.map((task) => ({
      id: task.id,
      label: task.name || task.id,
      description: mono(`{{ .Tasks.${task.id}.output }}`),
      icon: <Code sx={{ fontSize: 'var(--ds-text-body-lg)' }} />,
      onSelect: () => handleTaskSelect(task),
    })),
    { type: 'section', label: 'Common Variables' },
    { label: 'Automation Inputs', description: mono('{{ .Inputs }}'), onSelect: () => insertTemplate('{{ .Inputs }}') },
    { label: 'Automation State', description: mono('{{ .State }}'), onSelect: () => insertTemplate('{{ .State }}') },
    { label: 'Automation Variables', description: mono('{{ .Vars }}'), onSelect: () => insertTemplate('{{ .Vars }}') },
  ];

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
        <Typography
          variant='body2'
          sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' }}
        >
          {label}
        </Typography>
        <Box sx={{ display: 'flex', gap: 0.5 }}>
          {/* Preset expressions */}
          <DropdownMenu
            align='end'
            minWidth={250}
            // Portal to body (as the original MUI Menu did) so the panel isn't
            // clipped/mispositioned when this field renders inside a transformed
            // or overflow-clipped config panel.
            disablePortal={false}
            trigger={
              <Button
                composition='icon-only'
                tone='ghost'
                size='xs'
                tooltip='Preset expressions'
                aria-label='Preset expressions'
                disabled={disabled}
                icon={
                  <Box sx={{ display: 'inline-flex', alignItems: 'center' }}>
                    <Code sx={{ fontSize: 'var(--ds-text-title)' }} />
                    <KeyboardArrowDown sx={{ fontSize: 'var(--ds-text-body-lg)' }} />
                  </Box>
                }
              />
            }
            items={presetItems}
          />

          {/* Previous tasks */}
          {previousTasks.length > 0 && (
            <DropdownMenu
              align='end'
              minWidth={250}
              // Portal to body (see the preset menu above) — same clipping guard.
              disablePortal={false}
              trigger={
                <Button
                  composition='icon-only'
                  tone='ghost'
                  size='xs'
                  tooltip='Insert task reference'
                  aria-label='Insert task reference'
                  disabled={disabled}
                  icon={<Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-semibold)' }}>{'{{ }}'}</Typography>}
                />
              }
              items={taskMenuItems}
            />
          )}

          <Button
            composition='icon-only'
            tone='ghost'
            size='xs'
            tooltip={copied ? 'Copied!' : 'Copy'}
            aria-label='Copy'
            disabled={!localValue.trim()}
            onClick={handleCopy}
            icon={
              copied ? (
                <Check sx={{ fontSize: 'var(--ds-text-title)', color: 'success.main' }} />
              ) : (
                <ContentCopy sx={{ fontSize: 'var(--ds-text-title)' }} />
              )
            }
          />
        </Box>
      </Box>
      <Input size='sm' value={localValue} onChange={handleChange} placeholder={placeholder} disabled={disabled} help={helperText || undefined} />
    </Box>
  );
};

export default TemplateExpressionField;
