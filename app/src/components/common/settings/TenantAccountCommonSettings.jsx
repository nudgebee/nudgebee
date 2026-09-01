import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Input } from '@ui/Input';
import { Button } from '@ui/Button';
import { ds } from '@utils/colors';
import { LOG_LABEL_FIELDS } from './labelMapperFields';

/**
 * Two-column label-mapper grid, shared by the tenant-wide Settings modal and the
 * per-account K8s settings modal, for both the log and the trace mapping.
 *
 * The props are generic (settings / setSettings / fields) rather than log-specific
 * because the same grid now writes log_labels and trace_labels; a prop called
 * `logSettings` holding {service_name: ...} would be a lie. The defaults reproduce the
 * log mapper exactly, so the log call sites keep their existing behaviour.
 */
const TenantAccountCommonSettings = ({
  settings,
  setSettings,
  fields = LOG_LABEL_FIELDS,
  advancedFields = [],
  title = 'Log Label Mapper',
  idPrefix = 'log-label',
  advancedLabel = 'advanced fields',
  disabled = false,
}) => {
  const handleChange = (field) => (value) => {
    setSettings((prev) => ({
      ...prev,
      [field]: value,
    }));
  };

  // A stored override sitting in a collapsed field would be invisible: the operator sees
  // an empty-looking mapper while the backend happily applies a value they cannot see.
  // Seeded from props for the synchronous case AND re-checked in an effect, because both
  // callers hydrate asynchronously — this component mounts before getTenantAttributes /
  // the account listing resolve, so initial state alone would hide it forever.
  const hasAdvancedValue = advancedFields.some(({ field }) => Boolean(settings?.[field]));
  const [showAdvanced, setShowAdvanced] = useState(hasAdvancedValue);
  useEffect(() => {
    // Only ever expands. Collapsing on the way back down would yank the section shut
    // under a user who just cleared the last advanced value mid-edit.
    if (hasAdvancedValue) {
      setShowAdvanced(true);
    }
  }, [hasAdvancedValue]);

  const renderField = ({ label, field, placeholder }) => (
    <Input
      key={field}
      size='sm'
      label={label}
      id={`${idPrefix}-${field}`}
      data-testid={`${idPrefix}-${field}`}
      value={settings?.[field] || ''}
      placeholder={placeholder}
      onChange={handleChange(field)}
      disabled={disabled}
    />
  );

  return (
    <Box>
      {title && (
        <Typography sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)', mb: ds.space[4] }}>{title}</Typography>
      )}
      <Box display='grid' gridTemplateColumns='1fr 1fr' gap={ds.space[4]}>
        {fields.map(renderField)}
      </Box>
      {advancedFields.length > 0 && (
        <Box sx={{ mt: ds.space[3] }}>
          {/* Deliberately NOT disabled for a read-only viewer: revealing a mapping
              someone else configured is a read, and gating the toggle would make a
              configured override unreachable for exactly the persona most likely to be
              diagnosing it. Only the Inputs themselves go inert. */}
          <Button
            tone='link'
            size='sm'
            id={`${idPrefix}-toggle-advanced`}
            data-testid={`${idPrefix}-toggle-advanced`}
            aria-expanded={showAdvanced}
            aria-controls={`${idPrefix}-advanced`}
            onClick={() => setShowAdvanced((prev) => !prev)}
          >
            {showAdvanced ? `Hide ${advancedLabel}` : `Show ${advancedLabel}`}
          </Button>
          {showAdvanced && (
            <Box id={`${idPrefix}-advanced`} display='grid' gridTemplateColumns='1fr 1fr' gap={ds.space[4]} sx={{ mt: ds.space[3] }}>
              {advancedFields.map(renderField)}
            </Box>
          )}
        </Box>
      )}
    </Box>
  );
};

const fieldListShape = PropTypes.arrayOf(PropTypes.shape({ label: PropTypes.string, field: PropTypes.string, placeholder: PropTypes.string }));

TenantAccountCommonSettings.propTypes = {
  settings: PropTypes.object.isRequired,
  setSettings: PropTypes.func.isRequired,
  fields: fieldListShape,
  // Rendered behind a "Show …" disclosure; auto-expanded when any already has a value.
  advancedFields: fieldListShape,
  // Pass null when the caller supplies its own heading — K8sIntegrationTile uses
  // <Heading borderWidth='md'> to match the sections either side of it.
  title: PropTypes.string,
  // Prefixes each field's id/data-testid so two mappers on one screen stay independently
  // addressable. Adding ids/testids is always safe (app/CLAUDE.md); renaming is not.
  idPrefix: PropTypes.string,
  advancedLabel: PropTypes.string,
  // Read-only mode — set by callers whose user may view but not change the
  // tenant/account config these inputs write (see canEditTenantSettings / hasWriteAccess).
  disabled: PropTypes.bool,
};

export default TenantAccountCommonSettings;
