import React, { useState } from 'react';
import { Box } from '@mui/material';
import EditIcon from '@mui/icons-material/Edit';
import SaveIcon from '@mui/icons-material/CheckCircleOutline';
import MarkDowns from '@common-new/MarkDowns';
import Tooltip from '@components1/ds/Tooltip';
import { Button } from '@components1/ds/Button';
import { Input } from '@components1/ds/Input';
import { Link } from '@components1/ds/Link';

const labelSx = {
  fontFamily: 'var(--ds-font-display)',
  fontSize: 'var(--ds-text-small)',
  color: 'var(--ds-gray-700)',
  fontWeight: 'var(--ds-font-weight-medium)',
  marginBottom: 'var(--ds-space-2)',
  display: 'block',
};

const containerSx = {
  maxWidth: '100%',
  border: '1px solid var(--ds-gray-300)',
  borderRadius: 'var(--ds-radius-sm)',
  padding: 'var(--ds-space-3)',
  backgroundColor: 'var(--ds-background-200)',
  marginBottom: 'var(--ds-space-2)',
  position: 'relative',
};

function TicketsDescriptionEditor({ issueUrl, value, onChange, error }) {
  const [isEditing, setIsEditing] = useState(false);

  const handleEditToggle = () => setIsEditing((prev) => !prev);

  const getDisplayValue = () => {
    if (!value) return '';
    if (isEditing) return value;
    return value.replace(/~/g, '\\~');
  };

  const hasError = error && !value;

  return (
    <Box>
      {isEditing ? (
        <>
          <Box component='label' htmlFor='description' sx={labelSx}>
            Description
          </Box>
          <Box sx={{ position: 'relative' }}>
            <Input
              id='description'
              type='textarea'
              value={value || ''}
              onChange={(next) => onChange(next)}
              minRows={3}
              error={hasError ? 'Description is required' : ''}
            />
            <Box sx={{ position: 'absolute', top: 'var(--ds-space-2)', right: 'var(--ds-space-2)', zIndex: 1 }}>
              <Tooltip title='Save'>
                <Button
                  tone='secondary'
                  composition='icon-only'
                  icon={<SaveIcon />}
                  size='sm'
                  aria-label='Save description'
                  onClick={handleEditToggle}
                />
              </Tooltip>
            </Box>
          </Box>
        </>
      ) : (
        <>
          <Box
            sx={{
              ...labelSx,
              ...(hasError ? { color: 'var(--ds-red-600)' } : {}),
            }}
          >
            Description *
          </Box>
          <Box
            sx={{
              ...containerSx,
              borderColor: hasError ? 'var(--ds-red-500)' : 'var(--ds-gray-300)',
              overflowY: 'visible',
              maxHeight: 'none',
            }}
          >
            <Box sx={{ position: 'absolute', top: 'var(--ds-space-2)', right: 'var(--ds-space-2)' }}>
              <Tooltip title='Edit'>
                <Button
                  tone='secondary'
                  composition='icon-only'
                  icon={<EditIcon />}
                  size='sm'
                  aria-label='Edit description'
                  onClick={handleEditToggle}
                />
              </Tooltip>
            </Box>
            <Box sx={{ marginRight: 'var(--ds-space-2)', marginTop: 'var(--ds-space-1)' }}>
              <MarkDowns
                data={getDisplayValue()}
                sx={{
                  maxWidth: '100%',
                  pr: 'var(--ds-space-5)',
                  maxHeight: '300px',
                  overflowY: 'auto',
                }}
              />
            </Box>
            {issueUrl && (
              <Box>
                <Link href={issueUrl} openInNew>
                  more details
                </Link>
              </Box>
            )}
          </Box>
        </>
      )}
    </Box>
  );
}

export default TicketsDescriptionEditor;
