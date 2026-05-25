import React, { useState, useEffect } from 'react';
import { Box, TextField, Typography, IconButton, Tooltip, Menu, MenuItem, ListItemText, ListItemIcon } from '@mui/material';
import { ContentCopy, Check, KeyboardArrowDown, Code } from '@mui/icons-material';
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

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const newValue = e.target.value;
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
        <Typography variant='body2' sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>
          {label}
        </Typography>
        <Box sx={{ display: 'flex', gap: 0.5 }}>
          {/* Preset expressions */}
          <Tooltip title='Preset expressions'>
            <IconButton size='small' onClick={(e) => setPresetAnchor(e.currentTarget)} disabled={disabled} sx={{ p: 0.5 }}>
              <Code sx={{ fontSize: 16 }} />
              <KeyboardArrowDown sx={{ fontSize: 14 }} />
            </IconButton>
          </Tooltip>
          <Menu anchorEl={presetAnchor} open={Boolean(presetAnchor)} onClose={() => setPresetAnchor(null)}>
            {CONDITIONAL_PRESETS.map((preset, index) => (
              <MenuItem key={index} onClick={() => handlePresetSelect(preset.value as string)} sx={{ minWidth: 250 }}>
                <ListItemText
                  primary={preset.label}
                  secondary={
                    <Typography component='span' sx={{ fontFamily: 'monospace', fontSize: '10px', color: colors.text.secondary }}>
                      {preset.value as string}
                    </Typography>
                  }
                  primaryTypographyProps={{ fontSize: '13px' }}
                />
              </MenuItem>
            ))}
          </Menu>

          {/* Previous tasks */}
          {previousTasks.length > 0 && (
            <>
              <Tooltip title='Insert task reference'>
                <IconButton size='small' onClick={(e) => setTaskAnchor(e.currentTarget)} disabled={disabled} sx={{ p: 0.5 }}>
                  <Typography sx={{ fontSize: 12, fontWeight: 600 }}>{'{{ }}'}</Typography>
                </IconButton>
              </Tooltip>
              <Menu anchorEl={taskAnchor} open={Boolean(taskAnchor)} onClose={() => setTaskAnchor(null)}>
                <MenuItem disabled sx={{ opacity: 0.7 }}>
                  <Typography sx={{ fontSize: '11px', fontWeight: 600 }}>Previous Tasks</Typography>
                </MenuItem>
                {previousTasks.map((task) => (
                  <MenuItem key={task.id} onClick={() => handleTaskSelect(task)}>
                    <ListItemIcon>
                      <Code sx={{ fontSize: 14 }} />
                    </ListItemIcon>
                    <ListItemText
                      primary={task.name || task.id}
                      secondary={`{{ .Tasks.${task.id}.output }}`}
                      primaryTypographyProps={{ fontSize: '12px' }}
                      secondaryTypographyProps={{ fontSize: '10px', fontFamily: 'monospace' }}
                    />
                  </MenuItem>
                ))}
                <MenuItem disabled sx={{ opacity: 0.7, mt: 1 }}>
                  <Typography sx={{ fontSize: '11px', fontWeight: 600 }}>Common Variables</Typography>
                </MenuItem>
                <MenuItem onClick={() => insertTemplate('{{ .Inputs }}')}>
                  <ListItemText
                    primary='Automation Inputs'
                    secondary='{{ .Inputs }}'
                    primaryTypographyProps={{ fontSize: '12px' }}
                    secondaryTypographyProps={{ fontSize: '10px', fontFamily: 'monospace' }}
                  />
                </MenuItem>
                <MenuItem onClick={() => insertTemplate('{{ .State }}')}>
                  <ListItemText
                    primary='Automation State'
                    secondary='{{ .State }}'
                    primaryTypographyProps={{ fontSize: '12px' }}
                    secondaryTypographyProps={{ fontSize: '10px', fontFamily: 'monospace' }}
                  />
                </MenuItem>
                <MenuItem onClick={() => insertTemplate('{{ .Vars }}')}>
                  <ListItemText
                    primary='Automation Variables'
                    secondary='{{ .Vars }}'
                    primaryTypographyProps={{ fontSize: '12px' }}
                    secondaryTypographyProps={{ fontSize: '10px', fontFamily: 'monospace' }}
                  />
                </MenuItem>
              </Menu>
            </>
          )}

          <Tooltip title={copied ? 'Copied!' : 'Copy'}>
            <IconButton size='small' onClick={handleCopy} disabled={!localValue.trim()} sx={{ p: 0.5 }}>
              {copied ? <Check sx={{ fontSize: 16, color: 'success.main' }} /> : <ContentCopy sx={{ fontSize: 16 }} />}
            </IconButton>
          </Tooltip>
        </Box>
      </Box>
      <TextField
        fullWidth
        size='small'
        value={localValue}
        onChange={handleChange}
        placeholder={placeholder}
        disabled={disabled}
        helperText={helperText}
        sx={{
          '& .MuiInputBase-input': {
            fontFamily: 'monospace',
            fontSize: '12px',
          },
        }}
        FormHelperTextProps={{
          sx: { fontSize: '11px' },
        }}
      />
    </Box>
  );
};

export default TemplateExpressionField;
