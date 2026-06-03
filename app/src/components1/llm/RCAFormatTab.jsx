import React, { useState, useEffect } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Skeleton } from '@components1/ds/Skeleton';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { Button } from '@components1/ds/Button';
import { toast as snackbar } from '@components1/ds/Toast';
import WidgetCard from '@components1/ds/WidgetCard';
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

  const fetchRCAFormat = React.useCallback(async () => {
    setLoading(true);
    try {
      const resp = await apiAskNudgebee.getRcaFormat(accountId);
      if (resp?.data?.format != null) {
        setFormat(resp.data.format);
      } else {
        // Fallback default if nothing is found (though API usually provides the default)
        setFormat(DEFAULT_RCA_FORMAT);
      }
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
    setSaving(true);
    try {
      const payload = {
        account_id: accountId,
        format: format,
      };
      const resp = await apiAskNudgebee.updateRcaFormat(payload);
      if (resp?.data) {
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
        <Button tone='primary' size='md' onClick={handleSave} loading={saving} disabled={loading || saving}>
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
