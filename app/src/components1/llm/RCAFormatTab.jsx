import React, { useState, useEffect } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import ShimmerLoading from '@components1/common/ShimmerLoading';
import apiAskNudgebee from '@api1/ask-nudgebee';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from 'src/utils/colors';
import WidgetCard from '@components1/common/WidgetCard';

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
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '16px', py: 2 }}>
      <WidgetCard sx={{ p: '16px 20px', mt: 0, mb: 2, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Box>
          <Typography sx={{ fontSize: '14px', fontWeight: 600, color: colors.text.secondary, fontFamily: 'Poppins' }}>
            Root Cause Analysis (RCA) Format
          </Typography>
          <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>
            Customize the Markdown template used by AI to generate RCA documents for your events.
          </Typography>
        </Box>
        <CustomButton text='Save Changes' variant='primary' onClick={handleSave} loading={saving} disabled={loading || saving} />
      </WidgetCard>

      {loading ? (
        <ShimmerLoading isLoading={true} height='400px' />
      ) : (
        <ReactCodeMirror
          value={format}
          height='400px'
          extensions={[markdown()]}
          onChange={(val) => setFormat(val)}
          theme='light'
          style={{
            border: `1px solid ${colors.border.secondaryLightest}`,
            borderRadius: '4px',
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
