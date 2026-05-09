import React, { useState } from 'react';
import { Tooltip, TextField, Typography, Box, IconButton } from '@mui/material';
import EditIcon from '@mui/icons-material/Edit';
import { inputSx } from '@data/themes/inputField';
import SaveIcon from '@mui/icons-material/CheckCircleOutline';
import Link from 'next/link';
import MarkDowns from '@components1/common/MarkDowns';
import { colors } from 'src/utils/colors';

const styles = {
  markdownContainer: {
    maxHeight: '300px',
    maxWidth: '100%',
    overflowY: 'auto',
    border: `1px solid ${colors.border.ticketDescription}`,
    borderRadius: '4px',
    padding: '10px',
    backgroundColor: colors.background.ticketDescription,
    marginBottom: '8px',
    position: 'relative',
  },
  editButton: {
    position: 'absolute',
    top: '8px',
    right: '20px',
  },
  saveButton: {
    position: 'absolute',
    top: '8px',
    right: '8px',
    color: colors.text.cpuRecommendation,
  },
  descriptionLabel: {
    marginBottom: '8px',
  },
};
const inputSxOverride = {
  border: 'none',
};

function TicketsDescriptionEditor({ issueUrl, value, onChange, error }) {
  const [isEditing, setIsEditing] = useState(false);
  const handleEditToggle = () => {
    setIsEditing(!isEditing);
  };

  // Only escape for markdown display, not for editing
  const getDisplayValue = () => {
    if (!value) {
      return '';
    }
    if (isEditing) {
      return value; // Raw value for editing
    }
    // Escape only for markdown display
    return value.replace(/~/g, '\\~');
  };

  const handleDescriptionChange = (e) => {
    onChange(e.target.value);
  };

  return (
    <Box>
      {isEditing ? (
        <>
          <Typography sx={styles.descriptionLabel}>Description</Typography>
          <Box sx={{ ...styles.markdownContainer, borderColor: error && !value ? colors.border.errorOutline : colors.border.ticketDescription }}>
            <TextField
              sx={{
                ...inputSx,
                ...inputSxOverride,
                '& .MuiInputBase-root': {
                  paddingRight: '40px',
                },
              }}
              multiline
              id='description'
              value={value}
              variant='standard'
              InputProps={{}}
              fullWidth
              onChange={handleDescriptionChange}
            />
            <Tooltip title='Save'>
              <IconButton sx={styles.saveButton} onClick={handleEditToggle}>
                <SaveIcon />
              </IconButton>
            </Tooltip>
          </Box>
        </>
      ) : (
        <>
          <Typography sx={{ ...styles.descriptionLabel, ...(error && !value ? { color: colors.error } : {}) }}>Description *</Typography>
          <Box
            sx={{
              ...styles.markdownContainer,
              borderColor: error && !value ? colors.border.errorOutline : colors.border.ticketDescription,
              overflowY: 'visible',
              maxHeight: 'none',
            }}
          >
            <Tooltip title='Edit'>
              <IconButton sx={styles.editButton} onClick={handleEditToggle}>
                <EditIcon />
              </IconButton>
            </Tooltip>
            <Box sx={{ marginRight: '2px', marginTop: '1px' }}>
              <MarkDowns
                data={getDisplayValue()}
                sx={{
                  maxWidth: '100%',
                  pr: '30px',
                  maxHeight: '300px',
                  overflowY: 'auto',
                }}
              />
            </Box>
            {!issueUrl ? null : (
              <Box>
                <Link href={issueUrl}>more details</Link>
              </Box>
            )}
          </Box>
        </>
      )}
    </Box>
  );
}

export default TicketsDescriptionEditor;
