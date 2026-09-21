import React, { useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import { Card } from '@ui/Card';
import { FormField } from '@shared/forms/FormComponents';

interface ClarificationOption {
  label: string;
  description?: string;
}

interface ClarificationQuestionProps {
  question: string;
  options: ClarificationOption[];
  allowCustom?: boolean;
  allowSkip?: boolean;
  onSelect: (answer: string) => void;
  disabled?: boolean;
}

export default function ClarificationQuestion({
  question,
  options,
  allowCustom = true,
  allowSkip = true,
  onSelect,
  disabled = false,
}: Readonly<ClarificationQuestionProps>): React.JSX.Element {
  const [showCustomInput, setShowCustomInput] = useState(false);
  const [customText, setCustomText] = useState('');

  // Filter out "Skip" from the main options — we render it separately
  const mainOptions = options.filter((opt) => opt.label.toLowerCase() !== 'skip');

  const handleOptionClick = (label: string): void => {
    if (disabled) {
      return;
    }
    onSelect(label);
  };

  const handleCustomSubmit = (): void => {
    if (disabled || !customText.trim()) {
      return;
    }
    onSelect(customText.trim());
  };

  return (
    <Box sx={{ mt: 2, mb: 2 }}>
      <Typography variant='subtitle2' sx={{ mb: 2, fontWeight: 'var(--ds-font-weight-semibold)', lineHeight: 1.5 }}>
        {question}
      </Typography>

      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
        {mainOptions.map((opt, index) => (
          <Card
            key={opt.label}
            variant='outlined'
            size='sm'
            elevation='flat'
            interactive={!disabled}
            onClick={disabled ? undefined : () => handleOptionClick(opt.label)}
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 1.5,
              opacity: disabled ? 0.6 : 1,
            }}
          >
            <Box
              sx={{
                width: 24,
                height: 24,
                borderRadius: '50%',
                bgcolor: 'action.selected',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                flexShrink: 0,
              }}
            >
              <Typography variant='caption' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-small)' }}>
                {index + 1}
              </Typography>
            </Box>
            <Box sx={{ flex: 1, minWidth: 0 }}>
              <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }}>
                {opt.label}
              </Typography>
              {opt.description && (
                <Typography variant='caption' sx={{ color: 'text.secondary' }}>
                  {opt.description}
                </Typography>
              )}
            </Box>
            <Typography sx={{ color: 'text.secondary', fontSize: 'var(--ds-text-title)' }}>›</Typography>
          </Card>
        ))}

        {allowCustom && !showCustomInput && (
          <Card
            variant='outlined'
            size='sm'
            elevation='flat'
            interactive={!disabled}
            onClick={disabled ? undefined : () => setShowCustomInput(true)}
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 1.5,
              opacity: disabled ? 0.6 : 1,
              // Card has no dashed variant — the dashed edge distinguishes the
              // "write your own" row from the concrete options above it.
              borderStyle: 'dashed',
            }}
          >
            <Box
              sx={{
                width: 24,
                height: 24,
                borderRadius: '50%',
                bgcolor: 'action.selected',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                flexShrink: 0,
              }}
            >
              <Typography variant='caption' sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-small)' }}>
                ✎
              </Typography>
            </Box>
            <Typography variant='body2' sx={{ color: 'text.secondary' }}>
              Something else
            </Typography>
          </Card>
        )}

        {showCustomInput && (
          <Box sx={{ mt: 1 }}>
            <FormField
              value={customText}
              onChange={(e: React.ChangeEvent<HTMLTextAreaElement>) => setCustomText(e.target.value)}
              label=''
              fieldType='textarea'
              placeholder='Type your preference...'
              multiline
              minRows={2}
              maxRows={4}
            />
            <Box sx={{ display: 'flex', gap: 1, mt: 1, justifyContent: 'flex-end' }}>
              <Button
                tone='secondary'
                size='sm'
                onClick={() => {
                  setShowCustomInput(false);
                  setCustomText('');
                }}
              >
                Cancel
              </Button>
              <Button tone='primary' size='sm' onClick={handleCustomSubmit} disabled={!customText.trim()}>
                Submit
              </Button>
            </Box>
          </Box>
        )}
      </Box>

      {allowSkip && !showCustomInput && (
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', mt: 2 }}>
          <Button tone='secondary' size='sm' onClick={() => onSelect('Skip')} disabled={disabled}>
            Skip
          </Button>
        </Box>
      )}
    </Box>
  );
}
