import React, { useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import { Form } from '@shared/forms/Form';
import { Input } from '@ui/Input';
import { Select } from '@ui/Select';
import { Button } from '@ui/Button';
import { Callout } from '@ui/Callout';
import Text from '@shared/format/Text';
import apiIntegrations from '@api1/integrations';
import { ds } from 'src/utils/colors';
import { useAccountOptions } from '../hooks/useAccountOptions';
import { applyTemplateValues, buildTemplateFields, initialTemplateValues, missingRequired, TemplateField } from '../utils/templateValues';

interface TemplateValuesFormProps {
  template: any;
  accountId: string;
  eventContext?: Record<string, string>;
  onBack: () => void;
  onSubmit: (template: any) => void;
}

type Option = { value: string; label: string };

const toOptions = (rows: any[] | null | undefined, valueKey: 'id' | 'name'): Option[] =>
  (rows ?? [])
    .filter((row) => row?.[valueKey])
    .map((row) => ({ value: row[valueKey], label: valueKey === 'id' && row.type ? `${row.name} (${row.type})` : row.name }));

const TemplateValuesForm: React.FC<TemplateValuesFormProps> = ({ template, accountId, eventContext, onBack, onSubmit }) => {
  const templateFields = useMemo(() => buildTemplateFields(template), [template]);
  const [values, setValues] = useState<Record<string, string>>(() => initialTemplateValues(template, templateFields, { accountId, eventContext }));
  const needs = (kind: TemplateField['kind']) => templateFields.some((f) => f.kind === kind);
  const { options: accountOptions, isLoading: accountsLoading } = useAccountOptions(needs('account'));
  const [integrationOptions, setIntegrationOptions] = useState<Option[]>([]);
  // null while loading; an empty list means the account has no webhook integration yet.
  const [webhookOptions, setWebhookOptions] = useState<Option[] | null>(null);
  const noWebhooks = webhookOptions?.length === 0;
  // With nothing to pick, the webhook field must not block; the user can add one or change the trigger.
  const fields = useMemo(
    () => (noWebhooks ? templateFields.map((f) => (f.kind === 'webhook' ? { ...f, required: false } : f)) : templateFields),
    [templateFields, noWebhooks]
  );

  useEffect(() => {
    if (!needs('integration')) return;
    let active = true;
    apiIntegrations
      .listIntegrations({ limit: 100, offset: 0, status: 'enabled' })
      .then((res: any) => active && setIntegrationOptions(toOptions(res?.data?.data?.integrations_list?.rows, 'id')))
      .catch(() => active && setIntegrationOptions([]));
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [templateFields]);

  useEffect(() => {
    if (!needs('webhook')) return;
    let active = true;
    apiIntegrations
      .listIntegrations({
        type: 'workflow_webhook',
        status: 'enabled',
        limit: 100,
        offset: 0,
        cloudAccountId: accountId && accountId !== 'demo' ? accountId : undefined,
      })
      .then((res: any) => active && setWebhookOptions(toOptions(res?.data?.data?.integrations_list?.rows, 'name')))
      .catch(() => active && setWebhookOptions([]));
    return () => {
      active = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [templateFields, accountId]);

  const missing = missingRequired(fields, values);
  const groups = useMemo(() => Array.from(new Set(fields.map((f) => f.group))), [fields]);
  const setValue = (key: string, next: string) => setValues((prev) => ({ ...prev, [key]: next }));

  const renderControl = (field: TemplateField) => {
    const common = { required: field.required, placeholder: field.placeholder };
    const value = values[field.key] || '';
    if (field.kind === 'webhook' && noWebhooks) {
      return (
        <Callout
          id={`wf-template-value-${field.key}-empty`}
          tone='info'
          message='This account has no webhook integration yet. Add one under Integrations, or change the trigger in the builder.'
        />
      );
    }
    if (field.kind === 'text') {
      return <Input {...common} data-testid={`wf-template-value-${field.key}`} value={value} onChange={(next) => setValue(field.key, next)} />;
    }
    const options =
      field.kind === 'select'
        ? field.options || []
        : field.kind === 'account'
        ? accountOptions
        : field.kind === 'integration'
        ? integrationOptions
        : webhookOptions || [];
    return (
      <Select
        {...common}
        options={options}
        value={value || null}
        onChange={(next) => setValue(field.key, next)}
        loading={(field.kind === 'account' && accountsLoading) || (field.kind === 'webhook' && webhookOptions === null)}
        grouped={field.kind === 'account'}
      />
    );
  };

  return (
    <Box id='wf-template-values-form' sx={{ display: 'flex', flexDirection: 'column', minHeight: 0 }}>
      <Text
        value={`Fill in the values "${template?.name}" needs before it opens in the builder.`}
        sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], mb: 'var(--ds-space-3)' }}
      />
      <Box sx={{ overflowY: 'auto', flex: 1 }}>
        <Form variant='stacked' density='default'>
          {groups.map((group) => (
            <Form.Section key={group} title={group}>
              {fields
                .filter((f) => f.group === group)
                .map((field) => (
                  <Form.Field
                    key={field.key}
                    id={`wf-template-value-${field.key}`}
                    label={field.label}
                    description={field.helpText}
                    required={field.required}
                  >
                    {renderControl(field)}
                  </Form.Field>
                ))}
            </Form.Section>
          ))}
        </Form>
      </Box>
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'flex-end',
          gap: 'var(--ds-space-3)',
          paddingTop: 'var(--ds-space-4)',
          borderTop: '1px solid var(--ds-gray-200)',
        }}
      >
        <Button id='wf-template-values-back' tone='secondary' size='md' onClick={onBack}>
          Back to templates
        </Button>
        <Button
          id='wf-template-values-submit'
          size='md'
          disabled={missing.length > 0}
          onClick={() => onSubmit(applyTemplateValues(template, values))}
        >
          Use template
        </Button>
      </Box>
    </Box>
  );
};

export default TemplateValuesForm;
