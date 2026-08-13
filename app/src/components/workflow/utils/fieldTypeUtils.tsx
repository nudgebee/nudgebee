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

export interface SchemaProperty {
  type: string;
  description?: string;
  required?: boolean;
  default?: any;
  enum?: string[];
  integration_type?: string;
  is_encrypted?: boolean;
  sub_type?: string;
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

// Jinja (default engine) uses {{ }} for expressions and {% %} for statements;
// Go text/template only uses {{ }}. A string matching either is a template
// reference the backend resolves at runtime, not a user-entered literal.
export const isTemplateString = (s: string): boolean => /\{\{|\{%/.test(s);

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
    ticketConfigurations: { label: string; value: string; icon?: any }[];
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
    return data.ticketConfigurations;
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
      const option: { label: string; value: string; icon?: React.ReactNode } = {
        label: value.charAt(0).toUpperCase() + value.slice(1).replace(/_/g, ' '),
        value: value,
      };
      // Add icons for provider fields (notification providers). Wrap in SafeIcon —
      // these are webpack asset modules, not React components, and ds/Select renders
      // option.icon directly as a JSX child (React.ReactNode).
      if ((fieldName === 'provider' || fieldName === 'im_provider') && PROVIDER_ICONS[value]) {
        option.icon = <SafeIcon src={PROVIDER_ICONS[value]} alt={value} style={{ width: 16, height: 16, objectFit: 'contain' }} />;
      }
      return option;
    });
  }
  return [];
};
