import React from 'react';
import { Box, Typography } from '@mui/material';
import { Chip } from '@ui/Chip';
import Tooltip from '@ui/Tooltip';
import { Code, ViewModule, ContentCopy, Check } from '@mui/icons-material';
import { Button } from '@ui/Button';
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
        <Typography
          variant='body2'
          sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' }}
        >
          {label}
        </Typography>
        {labelExtra}
      </Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
        <Button
          composition='icon-only'
          tone={viewMode === 'structured' ? 'primary' : 'ghost'}
          size='xs'
          tooltip='Structured view - Edit as form fields'
          aria-label='Structured view'
          icon={<ViewModule sx={{ fontSize: 'var(--ds-text-title)' }} />}
          onClick={() => onViewModeChange('structured')}
        />
        <Button
          composition='icon-only'
          tone={viewMode === 'json' ? 'primary' : 'ghost'}
          size='xs'
          tooltip='JSON view - Edit raw JSON'
          aria-label='JSON view'
          icon={<Code sx={{ fontSize: 'var(--ds-text-title)' }} />}
          onClick={() => onViewModeChange('json')}
        />
        <Button
          composition='icon-only'
          tone='ghost'
          size='xs'
          tooltip={copied ? 'Copied!' : 'Copy JSON'}
          aria-label='Copy JSON'
          icon={
            copied ? (
              <Check sx={{ fontSize: 'var(--ds-text-title)', color: 'success.main' }} />
            ) : (
              <ContentCopy sx={{ fontSize: 'var(--ds-text-title)' }} />
            )
          }
          onClick={onCopy}
        />
      </Box>
    </Box>
    {presets && presets.length > 0 && onPresetClick && (
      <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5, mb: 1.5 }}>
        {presets.map((preset, idx) => (
          <Tooltip key={idx} title={preset.description || ''}>
            <Box component='span' sx={{ display: 'inline-flex' }}>
              <Chip size='sm' onClick={() => onPresetClick(preset)} disabled={disabled}>
                {preset.label}
              </Chip>
            </Box>
          </Tooltip>
        ))}
      </Box>
    )}
  </Box>
);

export default FieldHeader;
