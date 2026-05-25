import React from 'react';
import { Box, Typography, IconButton, Tooltip, Chip } from '@mui/material';
import { Code, ViewModule, ContentCopy, Check } from '@mui/icons-material';
import { colors } from 'src/utils/colors';
import { viewToggleStyles } from './advancedConfigStyles';
import type { Preset } from './advancedConfigPresets';

interface FieldHeaderProps {
  label: string;
  viewMode: 'structured' | 'json';
  onViewModeChange: (mode: 'structured' | 'json') => void;
  copied: boolean;
  onCopy: () => void;
  presets?: Preset[];
  onPresetClick?: (preset: Preset) => void;
  disabled?: boolean;
  labelExtra?: React.ReactNode;
}

const FieldHeader: React.FC<FieldHeaderProps> = ({
  label,
  viewMode,
  onViewModeChange,
  copied,
  onCopy,
  presets,
  onPresetClick,
  disabled,
  labelExtra,
}) => (
  <Box>
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 1.5 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <Typography variant='body2' sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>
          {label}
        </Typography>
        {labelExtra}
      </Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
        <Tooltip title='Structured view - Edit as form fields'>
          <IconButton size='small' onClick={() => onViewModeChange('structured')} sx={viewToggleStyles(viewMode === 'structured')}>
            <ViewModule sx={{ fontSize: 16 }} />
          </IconButton>
        </Tooltip>
        <Tooltip title='JSON view - Edit raw JSON'>
          <IconButton size='small' onClick={() => onViewModeChange('json')} sx={viewToggleStyles(viewMode === 'json')}>
            <Code sx={{ fontSize: 16 }} />
          </IconButton>
        </Tooltip>
        <Tooltip title={copied ? 'Copied!' : 'Copy JSON'}>
          <IconButton size='small' onClick={onCopy} sx={{ p: 0.5 }}>
            {copied ? <Check sx={{ fontSize: 16, color: 'success.main' }} /> : <ContentCopy sx={{ fontSize: 16 }} />}
          </IconButton>
        </Tooltip>
      </Box>
    </Box>
    {presets && presets.length > 0 && onPresetClick && (
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5, mb: 1.5 }}>
        {presets.map((preset, idx) => (
          <Tooltip key={idx} title={preset.description || ''}>
            <Chip label={preset.label} size='small' onClick={() => onPresetClick(preset)} disabled={disabled} sx={{ fontSize: '10px', height: 20 }} />
          </Tooltip>
        ))}
      </Box>
    )}
  </Box>
);

export default FieldHeader;
