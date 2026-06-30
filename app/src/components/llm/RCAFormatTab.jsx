import React, { useState, useEffect } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Skeleton } from '@ui/Skeleton';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { Button } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import WidgetCard from '@ui/WidgetCard';
import { ds } from '@utils/colors';

// Import MonocoEditor if available or a simple textarea as a fallback
// For simplicity and matching other components, assuming we have a code editor component
import ReactCodeMirror from '@uiw/react-codemirror';
import { markdown } from '@codemirror/lang-markdown';

const DEFAULT_RCA_FORMAT = `# 📝 Root Cause Analysis (RCA) Report

## 📊 Event Summary
Provide a brief description of the event...`;

const RCAFormatTab = ({ accountId }) => {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [format, setFormat] = useState('');
  // Baseline of the last loaded/saved content; Save stays disabled until the user edits away from it.
  const [savedFormat, setSavedFormat] = useState('');

  const isDirty = format !== savedFormat;

  const fetchRCAFormat = React.useCallback(async () => {
    setLoading(true);
    // Clear any previous account's content first so a failed load can't leave stale data on screen.
    setFormat('');
    setSavedFormat('');
    try {
      const resp = await apiAskNudgebee.getRcaFormat(accountId);
      // getRcaFormat resolves with { data, errors } instead of throwing, so surface API errors explicitly.
      if (resp?.errors?.length) {
        throw new Error(resp.errors[0]?.message || 'Failed to load RCA Format');
      }
      const loaded = resp?.data?.format != null ? resp.data.format : DEFAULT_RCA_FORMAT;
      setFormat(loaded);
      setSavedFormat(loaded);
    } catch (error) {
      console.error('Failed to fetch RCA Format:', error);
      snackbar.error('Failed to load RCA Format.');
    } finally {
      setLoading(false);
    }
  }, [accountId]);

  useEffect(() => {
    fetchRCAFormat();
  }, [fetchRCAFormat]);

  const handleSave = async () => {
    const formatToSave = format;
    setSaving(true);
    try {
      const payload = {
        account_id: accountId,
        format: formatToSave,
      };
      const resp = await apiAskNudgebee.updateRcaFormat(payload);
      if (resp?.data) {
        // Re-baseline so the button disables again until the next edit.
        setSavedFormat(formatToSave);
        snackbar.success('RCA Format updated successfully!');
      } else {
        snackbar.error('Failed to update RCA Format.');
      }
    } catch (e) {
      console.error('Failed to update RCA format:', e);
      snackbar.error('An error occurred while saving the RCA format.');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4], py: ds.space[4] }}>
      <WidgetCard
        sx={{
          p: `${ds.space[4]} ${ds.space.mul(1, 5)}`,
          mt: 0,
          mb: ds.space[4],
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
        }}
      >
        <Box>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
              fontFamily: 'Poppins',
            }}
          >
            Root Cause Analysis (RCA) Format
          </Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>
            Customize the Markdown template used by AI to generate RCA documents for your events.
          </Typography>
        </Box>
        <Button tone='primary' size='md' onClick={handleSave} loading={saving} disabled={loading || saving || !isDirty}>
          Save Changes
        </Button>
      </WidgetCard>

      {loading ? (
        <Skeleton shape='rect' height={ds.space.mul(1, 100)} sx={{ display: 'block' }} />
      ) : (
        <ReactCodeMirror
          value={format}
          height={ds.space.mul(1, 100)}
          extensions={[markdown()]}
          onChange={(val) => setFormat(val)}
          theme='light'
          style={{
            border: `1px solid ${'var(--ds-gray-200)'}`,
            borderRadius: ds.radius.sm,
            overflow: 'hidden',
          }}
        />
      )}
    </Box>
  );
};

RCAFormatTab.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default RCAFormatTab;
