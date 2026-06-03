import React, { useState, useEffect } from 'react';
import { Box, Typography, Menu, MenuItem, ListItemText, ListItemIcon } from '@mui/material';
import { Button } from '@components1/ds/Button';
import { ContentCopy, Check, KeyboardArrowDown, Code } from '@mui/icons-material';
import { Input } from '@components1/ds/Input';
import { colors } from 'src/utils/colors';
import { CONDITIONAL_PRESETS, FIELD_HELPER_TEXT, FIELD_PLACEHOLDERS } from './advancedConfigPresets';
import { useCopyToClipboard } from '@components1/workflow/hooks/useCopyToClipboard';

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
  const [presetAnchor, setPresetAnchor] = useState<null | HTMLElement>(null);
  const [taskAnchor, setTaskAnchor] = useState<null | HTMLElement>(null);

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
    setPresetAnchor(null);
  };

  const handleTaskSelect = (task: PreviousTask) => {
    const template = `{{ .Tasks.${task.id}.output }}`;
    const newValue = localValue ? `${localValue} ${template}` : template;
    setLocalValue(newValue);
    onChange(newValue);
    setTaskAnchor(null);
  };

  const insertTemplate = (template: string) => {
    const newValue = localValue ? `${localValue} ${template}` : template;
    setLocalValue(newValue);
    onChange(newValue);
  };

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
        <Typography
          variant='body2'
          sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.secondary }}
        >
          {label}
        </Typography>
        <Box sx={{ display: 'flex', gap: 0.5 }}>
          {/* Preset expressions */}
          <Button
            composition='icon-only'
            tone='ghost'
            size='xs'
            tooltip='Preset expressions'
            aria-label='Preset expressions'
            disabled={disabled}
            onClick={(e) => setPresetAnchor(e.currentTarget)}
            icon={
              <Box sx={{ display: 'inline-flex', alignItems: 'center' }}>
                <Code sx={{ fontSize: 16 }} />
                <KeyboardArrowDown sx={{ fontSize: 14 }} />
              </Box>
            }
          />
          <Menu anchorEl={presetAnchor} open={Boolean(presetAnchor)} onClose={() => setPresetAnchor(null)}>
            {CONDITIONAL_PRESETS.map((preset, index) => (
              <MenuItem key={index} onClick={() => handlePresetSelect(preset.value as string)} sx={{ minWidth: 250 }}>
                <ListItemText
                  primary={preset.label}
                  secondary={
                    <Typography component='span' sx={{ fontFamily: 'monospace', fontSize: 'var(--ds-text-caption)', color: colors.text.secondary }}>
                      {preset.value as string}
                    </Typography>
                  }
                  primaryTypographyProps={{ fontSize: 'var(--ds-text-body)' }}
                />
              </MenuItem>
            ))}
          </Menu>

          {/* Previous tasks */}
          {previousTasks.length > 0 && (
            <>
              <Button
                composition='icon-only'
                tone='ghost'
                size='xs'
                tooltip='Insert task reference'
                aria-label='Insert task reference'
                disabled={disabled}
                onClick={(e) => setTaskAnchor(e.currentTarget)}
                icon={<Typography sx={{ fontSize: 12, fontWeight: 'var(--ds-font-weight-semibold)' }}>{'{{ }}'}</Typography>}
              />
              <Menu anchorEl={taskAnchor} open={Boolean(taskAnchor)} onClose={() => setTaskAnchor(null)}>
                <MenuItem disabled sx={{ opacity: 0.7 }}>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-semibold)' }}>Previous Tasks</Typography>
                </MenuItem>
                {previousTasks.map((task) => (
                  <MenuItem key={task.id} onClick={() => handleTaskSelect(task)}>
                    <ListItemIcon>
                      <Code sx={{ fontSize: 14 }} />
                    </ListItemIcon>
                    <ListItemText
                      primary={task.name || task.id}
                      secondary={`{{ .Tasks.${task.id}.output }}`}
                      primaryTypographyProps={{ fontSize: 'var(--ds-text-small)' }}
                      secondaryTypographyProps={{ fontSize: 'var(--ds-text-caption)', fontFamily: 'monospace' }}
                    />
                  </MenuItem>
                ))}
                <MenuItem disabled sx={{ opacity: 0.7, mt: 1 }}>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-semibold)' }}>Common Variables</Typography>
                </MenuItem>
                <MenuItem onClick={() => insertTemplate('{{ .Inputs }}')}>
                  <ListItemText
                    primary='Automation Inputs'
                    secondary='{{ .Inputs }}'
                    primaryTypographyProps={{ fontSize: 'var(--ds-text-small)' }}
                    secondaryTypographyProps={{ fontSize: 'var(--ds-text-caption)', fontFamily: 'monospace' }}
                  />
                </MenuItem>
                <MenuItem onClick={() => insertTemplate('{{ .State }}')}>
                  <ListItemText
                    primary='Automation State'
                    secondary='{{ .State }}'
                    primaryTypographyProps={{ fontSize: 'var(--ds-text-small)' }}
                    secondaryTypographyProps={{ fontSize: 'var(--ds-text-caption)', fontFamily: 'monospace' }}
                  />
                </MenuItem>
                <MenuItem onClick={() => insertTemplate('{{ .Vars }}')}>
                  <ListItemText
                    primary='Automation Variables'
                    secondary='{{ .Vars }}'
                    primaryTypographyProps={{ fontSize: 'var(--ds-text-small)' }}
                    secondaryTypographyProps={{ fontSize: 'var(--ds-text-caption)', fontFamily: 'monospace' }}
                  />
                </MenuItem>
              </Menu>
            </>
          )}

          <Button
            composition='icon-only'
            tone='ghost'
            size='xs'
            tooltip={copied ? 'Copied!' : 'Copy'}
            aria-label='Copy'
            disabled={!localValue.trim()}
            onClick={handleCopy}
            icon={copied ? <Check sx={{ fontSize: 16, color: 'success.main' }} /> : <ContentCopy sx={{ fontSize: 16 }} />}
          />
        </Box>
      </Box>
      <Input size='sm' value={localValue} onChange={handleChange} placeholder={placeholder} disabled={disabled} help={helperText || undefined} />
    </Box>
  );
};

export default TemplateExpressionField;
