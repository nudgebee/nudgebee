import type { TemplateVariable } from '@api1/workflow/types';

export type TemplateFieldKind = 'text' | 'select' | 'account' | 'integration' | 'webhook';

export interface TemplateField {
  /** Input id, or `trigger:<index>` for a webhook trigger's integration. */
  key: string;
  label: string;
  helpText?: string;
  placeholder?: string;
  required: boolean;
  kind: TemplateFieldKind;
  options?: string[];
  group: string;
}

export const WEBHOOK_FIELD_PREFIX = 'trigger:';

const fieldKind = (variable: TemplateVariable, inputType?: string): TemplateFieldKind => {
  const type = (variable.type || inputType || '').toLowerCase();
  if (type === 'account' || type === 'account_selector') return 'account';
  if (type === 'integration' || type === 'integration_selector') return 'integration';
  if (variable.options?.length) return 'select';
  return 'text';
};

// One field per template variable that points at a real input, plus one per webhook trigger.
export const buildTemplateFields = (template: any): TemplateField[] => {
  const inputs: any[] = (template?.definition?.inputs ?? []).filter((input: any) => input?.id);
  const byId = new Map(inputs.map((input) => [input.id, input]));
  const fields: TemplateField[] = [];
  const seen = new Set<string>();
  (template?.template_variables ?? []).forEach((variable: TemplateVariable) => {
    const input = variable.input_ref ? byId.get(variable.input_ref) : undefined;
    if (!input || seen.has(input.id)) return;
    seen.add(input.id);
    fields.push({
      key: input.id,
      label: variable.display_name || input.id,
      helpText: variable.help_text || input.description,
      placeholder: variable.placeholder,
      required: Boolean(variable.required ?? input.required),
      kind: fieldKind(variable, input.type),
      options: variable.options,
      group: variable.group || 'Settings',
    });
  });
  (template?.definition?.triggers ?? []).forEach((trigger: any, index: number) => {
    if (trigger?.type !== 'webhook') return;
    fields.push({
      key: `${WEBHOOK_FIELD_PREFIX}${index}`,
      label: 'Webhook Integration',
      helpText: 'The workflow webhook integration whose calls start this automation',
      required: true,
      kind: 'webhook',
      group: 'Trigger',
    });
  });
  return fields;
};

const asText = (value: any): string => (['string', 'number', 'boolean'].includes(typeof value) ? String(value) : '');

export const initialTemplateValues = (
  template: any,
  fields: TemplateField[],
  context: { accountId?: string; eventContext?: Record<string, string> }
): Record<string, string> => {
  const inputs: any[] = template?.definition?.inputs ?? [];
  const triggers: any[] = template?.definition?.triggers ?? [];
  const values: Record<string, string> = {};
  fields.forEach((field) => {
    if (field.kind === 'webhook') {
      const index = Number(field.key.slice(WEBHOOK_FIELD_PREFIX.length));
      values[field.key] = asText(triggers[index]?.params?.integration_name);
      return;
    }
    const input = inputs.find((i) => i?.id === field.key);
    values[field.key] = context.eventContext?.[field.key] || asText(input?.default) || (field.kind === 'account' ? context.accountId || '' : '');
  });
  return values;
};

export const missingRequired = (fields: TemplateField[], values: Record<string, string>): string[] =>
  fields.filter((field) => field.required && !(values[field.key] || '').trim()).map((field) => field.key);

// Returns a copy of the template with the values set as input defaults and webhook integrations.
export const applyTemplateValues = (template: any, values: Record<string, string>): any => {
  const definition = template?.definition || {};
  const filled = (key: string) => (values[key] || '').trim();
  return {
    ...template,
    definition: {
      ...definition,
      inputs: (definition.inputs ?? []).map((input: any) => (input?.id && filled(input.id) ? { ...input, default: filled(input.id) } : input)),
      triggers: (definition.triggers ?? []).map((trigger: any, index: number) => {
        const name = filled(`${WEBHOOK_FIELD_PREFIX}${index}`);
        return trigger?.type === 'webhook' && name ? { ...trigger, params: { ...(trigger.params || {}), integration_name: name } } : trigger;
      }),
    },
  };
};
