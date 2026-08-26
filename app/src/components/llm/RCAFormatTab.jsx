import React, { useState, useEffect } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { Skeleton } from '@ui/Skeleton';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { Button } from '@ui/Button';
import { DropdownMenu } from '@ui/DropdownMenu';
import { Modal } from '@ui/Modal';
import { toast as snackbar } from '@ui/Toast';
import WidgetCard from '@ui/WidgetCard';
import { ds } from '@utils/colors';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import { NUDGEBEE_DEFAULT_TEMPLATE_ID, NAMED_RCA_FORMAT_TEMPLATES } from './rcaFormatTemplates';

// Import MonocoEditor if available or a simple textarea as a fallback
// For simplicity and matching other components, assuming we have a code editor component
import ReactCodeMirror from '@uiw/react-codemirror';
import { markdown } from '@codemirror/lang-markdown';

const DEFAULT_TEMPLATE_DESCRIPTION = 'The built-in Nudgebee template.';

const RCAFormatTab = ({ accountId }) => {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [format, setFormat] = useState('');
  // Baseline of the last loaded/saved content; Save stays disabled until the user edits away from it.
  const [savedFormat, setSavedFormat] = useState('');
  // The built-in template text, served alongside the account's format so the
  // "Nudgebee default" picker entry has a body without duplicating it here.
  const [defaultFormat, setDefaultFormat] = useState('');
  // Template awaiting an overwrite confirmation: { name, body } or null.
  const [pendingTemplate, setPendingTemplate] = useState(null);

  const isDirty = format !== savedFormat;

  const fetchRCAFormat = React.useCallback(async () => {
    setLoading(true);
    // Clear any previous account's content first so a failed load can't leave stale data on screen.
    setFormat('');
    setSavedFormat('');
    setDefaultFormat('');
    setPendingTemplate(null);
    try {
      const resp = await apiAskNudgebee.getRcaFormat(accountId);
      // getRcaFormat resolves with { data, errors } instead of throwing, so surface API errors explicitly.
      if (resp?.errors?.length) {
        throw new Error(resp.errors[0]?.message || 'Failed to load RCA Format');
      }
      const loaded = resp?.data?.format ?? '';
      setFormat(loaded);
      setSavedFormat(loaded);
      // Fall back to the loaded text if the backend predates the default_format field.
      setDefaultFormat(resp?.data?.default_format ?? loaded);
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

  const resolveTemplate = React.useCallback(
    (id) => {
      if (id === NUDGEBEE_DEFAULT_TEMPLATE_ID) return { name: 'Nudgebee default', body: defaultFormat };
      const match = NAMED_RCA_FORMAT_TEMPLATES.find((t) => t.id === id);
      return match ? { name: match.name, body: match.body } : null;
    },
    [defaultFormat]
  );

  // Load a template into the editor only. savedFormat is untouched, so isDirty
  // flips true and "Save Changes" enables — nothing is persisted here.
  const handlePickTemplate = React.useCallback(
    (id) => {
      const picked = resolveTemplate(id);
      if (!picked || !picked.body || picked.body === format) return;
      if (isDirty) {
        setPendingTemplate(picked);
      } else {
        setFormat(picked.body);
      }
    },
    [resolveTemplate, format, isDirty]
  );

  const confirmOverwrite = () => {
    if (pendingTemplate) setFormat(pendingTemplate.body);
    setPendingTemplate(null);
  };

  const templateItems = [
    {
      id: 'rca-template-nudgebee-default',
      label: 'Nudgebee default',
      description: DEFAULT_TEMPLATE_DESCRIPTION,
      onSelect: () => handlePickTemplate(NUDGEBEE_DEFAULT_TEMPLATE_ID),
    },
    ...NAMED_RCA_FORMAT_TEMPLATES.map((t) => ({
      id: `rca-template-${t.id}`,
      label: t.name,
      description: t.description,
      onSelect: () => handlePickTemplate(t.id),
    })),
  ];

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
              fontFamily: ds.font.display,
            }}
          >
            Root Cause Analysis (RCA) Format
          </Typography>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>
            Customize the Markdown template used by AI to generate RCA documents for your events.
          </Typography>
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[3] }}>
          <DropdownMenu
            align='end'
            minWidth={260}
            items={templateItems}
            trigger={
              <Button
                id='rca-format-template-picker'
                data-testid='rca-format-template-picker'
                tone='secondary'
                size='md'
                icon={<ExpandMoreIcon />}
                iconPlacement='end'
                disabled={loading || saving || accountId === 'demo'}
              >
                Start from template…
              </Button>
            }
          />
          <Button
            tone='primary'
            size='md'
            onClick={handleSave}
            loading={saving}
            disabled={loading || saving || !isDirty}
            data-testid='rca-format-save-btn'
          >
            Save Changes
          </Button>
        </Box>
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

      <Modal
        open={Boolean(pendingTemplate)}
        handleClose={() => setPendingTemplate(null)}
        title='Replace editor contents?'
        width='xs'
        actionButtons={
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'flex-end',
              alignItems: 'center',
              gap: ds.space[4],
              p: `${ds.space[3]} ${ds.space[5]}`,
            }}
          >
            <Button tone='secondary' size='sm' onClick={() => setPendingTemplate(null)} data-testid='rca-template-confirm-cancel'>
              Cancel
            </Button>
            <Button tone='danger' size='sm' onClick={confirmOverwrite} data-testid='rca-template-confirm-load'>
              Load template
            </Button>
          </Box>
        }
      >
        <Typography
          sx={{
            my: 2,
            fontSize: 'var(--ds-text-body)',
            color: ds.gray[700],
            lineHeight: 1.5,
          }}
        >
          You have unsaved changes to your RCA format. Loading the <strong>{pendingTemplate?.name}</strong> template will overwrite them. This
          can&apos;t be undone.
        </Typography>
      </Modal>
    </Box>
  );
};

RCAFormatTab.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default RCAFormatTab;
