import React, { useEffect, useMemo, useState } from 'react';
import { Box, FormControl, FormControlLabel, InputLabel, MenuItem, Select, Switch, TextField, Typography } from '@mui/material';
import NDialog from '@components1/common/modal/NDialog';

// Mirrors the templates registered server-side in
// collector-server/cloud-collector/providers/aws/ssm_run_command.go. Keep in
// sync — adding a template requires both ends.
interface SsmTemplate {
  id: string;
  label: string;
  description: string;
  params?: {
    field: string;
    label: string;
    helper?: string;
    type: 'text' | 'switch';
    defaultValue?: string | boolean;
  }[];
}

const SSM_TEMPLATES: SsmTemplate[] = [
  {
    id: 'check_disk_space',
    label: 'Check disk space',
    description: 'Runs `df -h` on the instance and returns the output.',
  },
  {
    id: 'check_memory',
    label: 'Check memory',
    description: 'Runs `free -h` on the instance and returns the output.',
  },
  {
    id: 'update_ssm_agent',
    label: 'Update SSM agent',
    description: 'Updates the AWS Systems Manager agent to the latest version.',
    params: [
      {
        field: 'allowDowngrade',
        label: 'Allow downgrade',
        helper: 'Permit re-installing an older agent version. Off by default.',
        type: 'switch',
        defaultValue: false,
      },
    ],
  },
  {
    id: 'install_cloudwatch_agent',
    label: 'Install CloudWatch agent',
    description: 'Installs the Amazon CloudWatch agent on the instance.',
    params: [
      {
        field: 'version',
        label: 'Version',
        helper: 'Leave blank to install the latest.',
        type: 'text',
        defaultValue: '',
      },
    ],
  },
];

interface RunSsmCommandDialogProps {
  open: boolean;
  resource: any | null;
  loading: boolean;
  onConfirm: (args: { template_id: string; parameters?: Record<string, any> }) => void;
  onCancel: () => void;
}

const RunSsmCommandDialog: React.FC<RunSsmCommandDialogProps> = ({ open, resource, loading, onConfirm, onCancel }) => {
  const [templateId, setTemplateId] = useState<string>(SSM_TEMPLATES[0].id);
  const [paramValues, setParamValues] = useState<Record<string, any>>({});

  const selectedTemplate = useMemo(() => SSM_TEMPLATES.find((t) => t.id === templateId) ?? SSM_TEMPLATES[0], [templateId]);

  // Reset param values when the template changes so stale values from a
  // previous template don't leak into the submitted args.
  useEffect(() => {
    const defaults: Record<string, any> = {};
    selectedTemplate.params?.forEach((p) => {
      if (p.defaultValue !== undefined) defaults[p.field] = p.defaultValue;
    });
    setParamValues(defaults);
  }, [templateId, selectedTemplate]);

  // When the dialog is closed externally (e.g. via cancel), reset to defaults
  // so the next open starts clean.
  useEffect(() => {
    if (!open) {
      setTemplateId(SSM_TEMPLATES[0].id);
      setParamValues({});
    }
  }, [open]);

  if (!resource) return null;

  const handleConfirm = () => {
    const parameters: Record<string, any> = {};
    selectedTemplate.params?.forEach((p) => {
      const v = paramValues[p.field];
      // SSM `parameters` map must be string-values per template allowlist;
      // booleans render as "true"/"false", numbers as their string form.
      if (v !== undefined && v !== '' && v !== null) {
        parameters[p.field] = typeof v === 'string' ? v : String(v);
      }
    });
    onConfirm({
      template_id: selectedTemplate.id,
      parameters: Object.keys(parameters).length > 0 ? parameters : undefined,
    });
  };

  const content = (
    <Box>
      <Typography sx={{ mb: 2 }}>
        Run a predefined command on instance <strong>{resource.name || resource.resourse_id}</strong> via AWS Systems Manager. The instance must have
        the SSM agent installed and online.
      </Typography>

      <FormControl fullWidth size='small' sx={{ mb: 2 }}>
        <InputLabel id='ssm-template-label'>Command</InputLabel>
        <Select
          labelId='ssm-template-label'
          label='Command'
          value={templateId}
          onChange={(e) => setTemplateId(e.target.value as string)}
          data-testid='ssm-template-select'
        >
          {SSM_TEMPLATES.map((t) => (
            <MenuItem key={t.id} value={t.id}>
              {t.label}
            </MenuItem>
          ))}
        </Select>
      </FormControl>

      <Typography variant='body2' sx={{ color: 'text.secondary', mb: 2 }}>
        {selectedTemplate.description}
      </Typography>

      {selectedTemplate.params?.map((p) => {
        if (p.type === 'switch') {
          return (
            <Box key={p.field} sx={{ mt: 1 }}>
              <FormControlLabel
                control={
                  <Switch
                    checked={!!paramValues[p.field]}
                    onChange={(e) => setParamValues({ ...paramValues, [p.field]: e.target.checked })}
                    data-testid={`ssm-param-${p.field}`}
                  />
                }
                label={p.label}
              />
              {p.helper && (
                <Typography variant='caption' sx={{ display: 'block', color: 'text.secondary' }}>
                  {p.helper}
                </Typography>
              )}
            </Box>
          );
        }
        return (
          <TextField
            key={p.field}
            label={p.label}
            helperText={p.helper}
            fullWidth
            size='small'
            sx={{ mt: 1 }}
            value={paramValues[p.field] ?? ''}
            onChange={(e) => setParamValues({ ...paramValues, [p.field]: e.target.value })}
            data-testid={`ssm-param-${p.field}`}
          />
        );
      })}
    </Box>
  );

  return (
    <NDialog
      open={open}
      dialogTitle='Run SSM Command'
      dialogContent={null}
      additionalComponent={content}
      buttonText='Run Command'
      handleSubmit={handleConfirm}
      handleClose={onCancel}
      loading={loading}
      width='sm'
    />
  );
};

export default RunSsmCommandDialog;
