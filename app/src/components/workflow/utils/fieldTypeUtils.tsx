import React from 'react';
import { SlackIcon, MSTeamsIcon, GChatIcon, PostgresIcon, MySqlIcon, ClickhouseIcon, ouMssql, ouOracle } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';

// Shared field-schema helpers used by the schema-driven parameter forms
// (ActionDetailsSidebar's renderField and the foreach SubTaskParamForm).
// Moved verbatim from ActionDetailsSidebar module scope — keep pure, no
// component state.

// Icon mapping for notification providers
const PROVIDER_ICONS: Record<string, any> = {
  slack: SlackIcon,
  ms_teams: MSTeamsIcon,
  google_chat: GChatIcon,
  slack_webhook: SlackIcon,
  ms_teams_webhook: MSTeamsIcon,
  google_chat_webhook: GChatIcon,
};

// Webhook integrations as notification providers, named as the notification rules picker names them.
export const PROVIDER_LABELS: Record<string, string> = {
  slack_webhook: 'Slack webhook',
  ms_teams_webhook: 'Teams webhook',
  google_chat_webhook: 'Google Chat webhook',
};

// Chat apps whose channel list says when the app isn't connected for the tenant.
const APP_PROVIDER_LABELS: Record<string, string> = {
  slack: 'Slack',
  ms_teams: 'Microsoft Teams',
};

export const isWebhookProvider = (provider: unknown): provider is string =>
  typeof provider === 'string' && Object.prototype.hasOwnProperty.call(PROVIDER_LABELS, provider);

// What a picker says when the chat app isn't connected: connect it, or the other
// way the step can send. Only Send IM can post through a webhook, and an approval
// can't (a webhook carries no buttons).
export const notConnectedLabel = (provider: string) => `${APP_PROVIDER_LABELS[provider] ?? provider} isn't connected`;

export const notConnectedHint = (actionType: string, provider: string) => {
  const app = APP_PROVIDER_LABELS[provider] ?? provider;
  const webhook = PROVIDER_LABELS[`${provider}_webhook`];
  if (actionType === 'notifications.im' && webhook) return `Connect ${app} in Integrations, or pick the ${webhook} provider.`;
  if (actionType === 'core.approval') return `Connect ${app} in Integrations, or pick another provider.`;
  return `Connect ${app} in Integrations.`;
};

// Static dbms_type dropdown options. Lives here (not in ActionDetailsSidebar)
// so SubTaskParamForm can import it without a sidebar → foreach-editor →
// form → sidebar import cycle.
export const DBMS_OPTIONS = [
  { label: 'PostgreSQL', value: 'postgresql', icon: PostgresIcon },
  { label: 'MySQL', value: 'mysql', icon: MySqlIcon },
  { label: 'ClickHouse', value: 'clickhouse', icon: ClickhouseIcon },
  { label: 'SQL Server', value: 'mssql', icon: ouMssql },
  { label: 'Oracle', value: 'oracle', icon: ouOracle },
];

// A ready-to-use sample value for a field, mirroring runbook-server's
// types.PropertyExample. Rendered as a click-to-fill chip under the input.
export interface PropertyExample {
  label: string;
  value: any;
  note?: string;
}

export interface SchemaProperty {
  type: string;
  description?: string;
  // Long-form markdown reference content, shown in an info-icon tooltip in the
  // guidance row below the input (backend: types.Property.Help).
  help?: string;
  // Click-to-fill sample values shown in the same guidance row.
  examples?: PropertyExample[];
  required?: boolean;
  default?: any;
  enum?: string[];
  integration_type?: string;
  is_encrypted?: boolean;
  sub_type?: string;
  sub_types?: string[];
  schema?: {
    properties?: Record<string, SchemaProperty>;
  };
  options?: string[];
  order?: number;
  title?: string;
  hidden?: boolean;
  depends_on?: string[];
  visible_when?: {
    field: string;
    value: string[];
  };
  required_when?: {
    field: string;
    value: string[];
  };
  options_source?: {
    type: string;
    dependency_mapping?: Record<string, string>;
  };
  dynamic_fields_source?: {
    type: string;
    dependency_mapping?: Record<string, string>;
  };
}

// Constants and configuration
export const FIELD_TYPE_MAP: Record<string, string> = {
  account: 'dropdown',
  integration: 'dropdown',
  notification: 'dropdown',
  ticket: 'dropdown',
  resource_name: 'resource_dropdown',
  boolean: 'switch',
  number: 'number',
  integer: 'number',
  timestamp: 'timestamp',
  array: 'array',
  object: 'json',
  any: 'json',
  'map[string]string': 'keyvalue',
  'map[string]any': 'json',
};

// Fields that should use duration input
export const DURATION_FIELD_PATTERNS = ['timeout', 'duration', 'interval', 'delay', 'wait'];

// Fields that should use key-value editor instead of JSON
export const KEYVALUE_FIELD_PATTERNS = ['headers', 'env', 'environment', 'labels', 'annotations', 'tags', 'metadata'];

export const TEXTAREA_FIELDS = ['script', 'command', 'expression', 'query'];

export const FIELD_PLACEHOLDERS: Record<string, string> = {
  script: "#!/bin/bash\necho 'Starting script execution...'\ncurl -X GET 'https://api.example.com/data'\necho 'Script completed successfully'",
  env: '{"API_KEY": "your-key", "ENV": "production"}',
  resources: '{\n  "cpu_request": "100m",\n  "cpu_limit": "500m",\n  "memory_request": "128Mi",\n  "memory_limit": "512Mi"\n}',
};

// Placeholder text for a field's empty state: an explicit FIELD_PLACEHOLDERS
// entry wins, otherwise the first string example doubles as one. That makes the
// empty box self-documenting for any field that declares examples, without a
// second place to keep sample values in sync.
export const getExamplePlaceholder = (fieldName: string, fieldSchema: SchemaProperty): string => {
  if (FIELD_PLACEHOLDERS[fieldName]) return FIELD_PLACEHOLDERS[fieldName];
  const first = fieldSchema.examples?.[0]?.value;
  return typeof first === 'string' ? first : '';
};

// Click-to-fill example values arrive as strings, because that is what a user
// would type. For JSON-typed fields, store the parsed value so the field holds
// the same shape typing it by hand would produce; anything that isn't JSON — a
// `{{ Tasks[...].output }}` template, most of all — stays a string.
export const parseJsonExample = (value: any): any => {
  if (typeof value !== 'string') return value;
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
};

// Jinja (default engine) uses {{ }} for expressions and {% %} for statements;
// Go text/template only uses {{ }}. A string matching either is a template
// reference the backend resolves at runtime, not a user-entered literal.
export const isTemplateString = (s: string): boolean => /\{\{|\{%/.test(s);

const isPlainObject = (value: any): boolean => !!value && typeof value === 'object' && !Array.isArray(value);

// Normalize what an object-typed field has *stored*: a blank string becomes {},
// a string holding a JSON object becomes that object. Anything else is kept when
// the field renders as raw JSON text — it is the JsonEditor's in-progress edit, and
// rewriting it mid-keystroke destroyed what the user typed (validateTaskData already
// flags it). Other editors need an object, so they keep the old fall back to {}.
export const normalizeStoredObjectValue = (value: any, rendersRawJsonText: boolean): { changed: boolean; value: any } => {
  if (typeof value !== 'string' || isTemplateString(value)) {
    return { changed: false, value };
  }
  if (value.trim()) {
    try {
      const parsed = JSON.parse(value);
      // Plain objects only: an array is not a valid value for an object-typed
      // field, and rejecting primitives keeps this converging after one pass.
      if (isPlainObject(parsed)) {
        return { changed: true, value: parsed };
      }
    } catch {
      // Falls through.
    }
    if (rendersRawJsonText) {
      return { changed: false, value };
    }
  }
  return { changed: true, value: {} };
};

// Value an object-typed field should render with. Key/value-style editors need a
// real object, so a stored string is parsed (or dropped) for them. Editors that own
// the user's raw text (JsonEditor) get the string untouched: a half-typed document
// is not parseable, and coercing it would hand the editor a value it never emitted,
// reseeding the box mid-keystroke. Callers pass rendersRawJsonText because the two
// param forms render the same field type with different editors.
export const resolveObjectFieldValue = (rawValue: any, rendersRawJsonText: boolean): any => {
  if (typeof rawValue !== 'string' || rendersRawJsonText) {
    return rawValue;
  }
  // Template strings pass through so the hybrid editors can flip to expression mode.
  if (isTemplateString(rawValue)) {
    return rawValue;
  }
  if (rawValue.trim()) {
    try {
      const parsed = JSON.parse(rawValue);
      if (isPlainObject(parsed)) {
        return parsed;
      }
    } catch {
      // Falls through.
    }
  }
  return {};
};

// Detect code language from field name or sub_type
export const getCodeLanguage = (fieldName: string, subType?: string): 'bash' | 'javascript' | 'sql' | 'json' | 'jsonata' => {
  if (subType) {
    const subTypeLower = subType.toLowerCase();
    if (subTypeLower === 'javascript' || subTypeLower === 'js') {
      return 'javascript';
    }
    if (subTypeLower === 'sql') {
      return 'sql';
    }
    if (subTypeLower === 'jsonata') {
      return 'jsonata';
    }
    if (subTypeLower === 'json') {
      return 'json';
    }
  }
  const fieldLower = fieldName.toLowerCase();
  if (fieldLower.includes('query') || fieldLower.includes('sql')) {
    return 'sql';
  }
  if (fieldLower.includes('expression') || fieldLower.includes('jsonata')) {
    return 'jsonata';
  }
  if (fieldLower.includes('javascript') || fieldLower.includes('js')) {
    return 'javascript';
  }
  return 'bash';
};

// Narrow a ticket-integration option list to the integration types a field
// accepts. Backends declare that as `sub_type` (single, e.g. github) or
// `sub_types` (a set, e.g. the incident-only tasks that take pagerduty /
// zenduty). Without a declaration every configured ticket integration is
// offered, which is how unsupported providers used to reach the action and
// fail at execution time.
export const filterTicketOptionsBySubType = <T extends { tool?: string }>(
  options: T[],
  fieldSchema: { sub_type?: string; sub_types?: string[] }
): T[] => {
  const allowed = fieldSchema.sub_types?.length ? fieldSchema.sub_types : fieldSchema.sub_type ? [fieldSchema.sub_type] : [];
  if (allowed.length === 0) return options;
  const allowedSet = new Set(allowed.map((t) => t.toLowerCase()));
  return options.filter((o) => allowedSet.has((o.tool || '').toLowerCase()));
};

// Utility functions
export const formatFieldLabel = (fieldName: string): string => {
  return fieldName.charAt(0).toUpperCase() + fieldName.slice(1).replace(/_/g, ' ');
};

// Determine the renderer for a field based on its schema. Pure version of the
// sidebar's getFieldType: the "name field alongside account/namespace/kind"
// resource-dropdown heuristic is opt-in via hasResourceNameField so leaner
// consumers (foreach sub-task form) can skip it.
export const resolveFieldType = (fieldName: string, fieldSchema: SchemaProperty, opts?: { hasResourceNameField?: boolean }): string => {
  // Check for encrypted fields first - always use password
  if (fieldSchema.is_encrypted) {
    return 'password';
  }

  // Check sub_type from backend schema (e.g., "textarea" for prompt fields)
  if (fieldSchema.sub_type === 'textarea') {
    return 'textarea';
  }

  // A field declaring JSON gets the code editor, whatever it is called; the
  // name-based rule below only catches fields called script/command/query.
  if (fieldSchema.sub_type === 'json') {
    return 'script';
  }

  // Check for nested schema objects
  // Schema can have properties directly under schema or under schema.properties
  if (fieldSchema.type === 'object' && fieldSchema.schema && Object.keys(fieldSchema.schema).length > 0) {
    // Check if schema has direct properties (not wrapped in 'properties' key)
    const schemaObj = fieldSchema.schema as Record<string, any>;
    const hasDirectProperties = Object.keys(schemaObj).some((key) => key !== 'properties' && typeof schemaObj[key] === 'object');
    const hasNestedProperties = fieldSchema.schema.properties && Object.keys(fieldSchema.schema.properties).length > 0;
    if (hasDirectProperties || hasNestedProperties) {
      return 'nested_schema';
    }
  }

  // Check for array with options (multi-select)
  if (fieldSchema.type === 'array' && (fieldSchema.options || fieldSchema.enum)) {
    return 'multiselect';
  }

  // Check predefined type mappings
  if (FIELD_TYPE_MAP[fieldSchema.type]) {
    return FIELD_TYPE_MAP[fieldSchema.type];
  }

  // Special cases based on field name
  const fieldNameLower = fieldName.toLowerCase();

  // Duration fields
  if (DURATION_FIELD_PATTERNS.some((pattern) => fieldNameLower.includes(pattern))) {
    return 'duration';
  }

  // Key-value fields
  if (fieldSchema.type === 'object' && KEYVALUE_FIELD_PATTERNS.some((pattern) => fieldNameLower.includes(pattern))) {
    return 'keyvalue';
  }

  // Resource name dropdown when name field appears alongside account/namespace/kind
  if (fieldNameLower === 'name' && opts?.hasResourceNameField) {
    return 'resource_dropdown';
  }

  // Dropdown for specific field names, enums, or options
  if (fieldName === 'dbms_type' || fieldNameLower === 'namespace' || fieldNameLower === 'kind' || fieldSchema.enum || fieldSchema.options) {
    return 'dropdown';
  }

  // Check if field name suggests script editor (with syntax highlighting)
  if (TEXTAREA_FIELDS.some((field) => fieldNameLower.includes(field))) {
    return 'script';
  }

  // Default to textfield
  return 'textfield';
};

// Helper function to get dropdown options based on field schema and available data
export const getDropdownOptionsForField = (
  fieldName: string,
  fieldSchema: any,
  data: {
    cloudAccounts: { label: string; value: string; cloud_provider?: string; account_type?: string }[];
    integrations: { label: string; value: string }[];
    notifications: { label: string; value: string }[];
    ticketConfigurations: { label: string; value: string; tool?: string; icon?: any }[];
    namespaces: { label: string; value: string }[];
    resourceTypes: { label: string; value: string }[];
    workloadKinds: { label: string; value: string }[];
    dbmsOptions: { label: string; value: string }[];
  }
): { label: string; value: string; icon?: any }[] => {
  if (fieldSchema.type === 'account') {
    return data.cloudAccounts;
  }
  if (fieldSchema.type === 'integration') {
    return data.integrations;
  }
  if (fieldSchema.type === 'notification') {
    return data.notifications;
  }
  if (fieldSchema.type === 'ticket') {
    return filterTicketOptionsBySubType(data.ticketConfigurations, fieldSchema);
  }
  if (fieldName.toLowerCase() === 'namespace') {
    return data.namespaces;
  }
  if (fieldName.toLowerCase() === 'kind') {
    // Merge schema-declared kinds (e.g. Pod, Deployment, DaemonSet) with the
    // kinds actually present in the selected cluster. Using only the dynamic
    // list would hide the schema default ("Pod") when the cluster has no
    // standalone pods, which in turn trips HybridField's detectMode and
    // flips the field to Expression mode on first render.
    const schemaOptions = fieldSchema.enum || fieldSchema.options;
    const merged: { label: string; value: string }[] = [];
    const seen = new Set<string>();
    const addKind = (value: string) => {
      if (!value || seen.has(value)) return;
      seen.add(value);
      merged.push({
        label: value.charAt(0).toUpperCase() + value.slice(1).replace(/_/g, ' '),
        value,
      });
    };
    (data.workloadKinds || []).forEach((opt) => addKind(opt.value));
    if (Array.isArray(schemaOptions)) {
      schemaOptions.forEach((v: string) => addKind(v));
    }
    if (merged.length > 0) return merged;
    return data.resourceTypes;
  }
  if (fieldName === 'dbms_type') {
    return data.dbmsOptions;
  }
  if (fieldSchema.enum || fieldSchema.options) {
    const options = fieldSchema.enum || fieldSchema.options || [];
    return options.map((value: string) => {
      const isProviderField = fieldName === 'provider' || fieldName === 'im_provider';
      const option: { label: string; value: string; icon?: React.ReactNode } = {
        label: (isProviderField && PROVIDER_LABELS[value]) || value.charAt(0).toUpperCase() + value.slice(1).replace(/_/g, ' '),
        value: value,
      };
      // Add icons for provider fields (notification providers). Wrap in SafeIcon —
      // these are webpack asset modules, not React components, and ds/Select renders
      // option.icon directly as a JSX child (React.ReactNode).
      if (isProviderField && PROVIDER_ICONS[value]) {
        option.icon = <SafeIcon src={PROVIDER_ICONS[value]} alt={value} style={{ width: 16, height: 16, objectFit: 'contain' }} />;
      }
      return option;
    });
  }
  return [];
};
