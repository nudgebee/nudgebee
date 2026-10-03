import React from 'react';
import { Box, Typography } from '@mui/material';
import { InfoOutlined } from '@mui/icons-material';
import { Chip } from '@ui/Chip';
import CustomTooltip from '@ui/Tooltip';
import MarkDowns from '@shared/viewers/MarkDowns';
import type { PropertyExample } from '../utils/fieldTypeUtils';

/**
 * FieldGuidance — the "how do I fill this in?" row rendered directly under a
 * schema-driven parameter input.
 *
 * Two independent affordances, both driven by the backend task schema
 * (runbook-server `types.Property`), both optional:
 *
 *   - `help`     → an info-icon tooltip carrying long-form markdown (operator
 *                  cheatsheets, quoting rules, doc links). Restores the
 *                  `FieldHelpIcon` mechanism that `Property.Help` was written
 *                  for and that has had no renderer since the ExecutionsView
 *                  revert.
 *   - `examples` → one click-to-fill chip per sample value. Clicking replaces
 *                  the field's current value, so each example must be valid on
 *                  its own.
 *
 * Renders `null` when the field declares neither, which is every action that
 * has not opted in — they stay pixel-identical.
 *
 * It sits below the control rather than beside the label on purpose: the label
 * markup is duplicated across a dozen branches of `renderField`, and the
 * example belongs next to the box it fills.
 */
interface FieldGuidanceProps {
  /** Markdown reference content (schema `help`). */
  help?: string;
  /** Click-to-fill sample values (schema `examples`). */
  examples?: PropertyExample[];
  /** Called with the example's raw value when a chip is clicked. */
  onApply: (value: any) => void;
  /** Suppresses the chips (the tooltip stays — reading help is not an edit). */
  disabled?: boolean;
  /** Field name, used to build stable testids. */
  fieldName: string;
}

const FieldGuidance: React.FC<FieldGuidanceProps> = ({ help, examples, onApply, disabled = false, fieldName }) => {
  const hasExamples = !!examples?.length;
  if (!help && !hasExamples) {
    return null;
  }

  return (
    <Box
      data-testid={`field-guidance-${fieldName}`}
      sx={{ mt: 'var(--ds-space-1)', display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 'var(--ds-space-1)' }}
    >
      {help && (
        <CustomTooltip
          title={
            <MarkDowns
              data={help}
              sx={{
                fontSize: 'var(--ds-text-caption)',
                lineHeight: 1.5,
                '& p': { m: 0, mb: 0.5 },
                '& ul': { pl: 2, m: 0, mb: 0.5 },
                '& li': { mb: 0.25 },
                '& code': { fontSize: 'var(--ds-text-caption)' },
              }}
              allowExecutable={false}
              canRunCode={false}
              onLinkClick={null}
            />
          }
          placement='right-start'
          tooltipStyle={{ maxWidth: '480px' }}
        >
          <Box
            component='span'
            data-testid={`field-help-${fieldName}`}
            sx={{ display: 'inline-flex', alignItems: 'center', gap: '2px', cursor: 'help', color: 'var(--ds-gray-600)' }}
          >
            <InfoOutlined sx={{ fontSize: 'var(--ds-text-body)' }} />
            <Typography component='span' sx={{ fontSize: 'var(--ds-text-caption)' }}>
              Syntax
            </Typography>
          </Box>
        </CustomTooltip>
      )}

      {hasExamples && (
        <>
          <Typography component='span' sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>
            Examples:
          </Typography>
          {examples?.map((example, index) => (
            <CustomTooltip key={`${example.label}-${index}`} title={example.note || 'Click to use this value'}>
              <span>
                <Chip
                  size='xs'
                  tone='info'
                  shape='rect'
                  disabled={disabled}
                  data-testid={`field-example-${fieldName}-${index}`}
                  onClick={() => onApply(example.value)}
                  sx={{
                    fontFamily: 'monospace',
                    maxWidth: '320px',
                    '& > span': { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
                  }}
                  displayTooltip={false}
                >
                  {example.label}
                </Chip>
              </span>
            </CustomTooltip>
          ))}
        </>
      )}
    </Box>
  );
};

export default FieldGuidance;
