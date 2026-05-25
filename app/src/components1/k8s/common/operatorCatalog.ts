export type OperatorKind = 'chip' | 'line';

// Shape of capabilities.supported_operator_descriptors as returned by
// get_default_provider / observability_list_provider_capabilities. Backend is
// the source of truth for chip/line labels and chip-vs-line kind metadata; the
// UI stays thin around this.
export interface OperatorDescriptor {
  token: string;
  chip_label?: string;
  line_label?: string;
  kinds: OperatorKind[];
}

export interface OperatorOption {
  label: string;
  value: string;
}

export function getOperatorsForKind(descriptors: OperatorDescriptor[] | undefined, kind: OperatorKind): OperatorOption[] {
  if (!descriptors || descriptors.length === 0) return [];
  return descriptors
    .filter((d) => d.kinds.includes(kind))
    .map((d) => ({
      label: (kind === 'line' ? d.line_label : d.chip_label) ?? d.token,
      value: d.token,
    }));
}

// Inverse map from legacy UI values (CONTAINS, NOT ILIKE, =, ...) to backend
// tokens. Mirrors lineOperatorMap + operatorMap in LogGenerateQuery.js but runs
// on the INBOUND (hydration) path for persisted URLs and saved queries. Stays
// UI-only — the backend never saw these legacy strings.
const LEGACY_TO_TOKEN: Record<string, string> = {
  '=': '_eq',
  '!=': '_neq',
  '<': '_lt',
  '<=': '_lte',
  '>': '_gt',
  '>=': '_gte',
  '=~': '_regex',
  '!~': '_nregex',
  CONTAINS: '_contains',
  'NOT CONTAINS': '_nlike',
  ICONTAINS: '_icontains',
  'NOT ICONTAINS': '_nlike',
  LIKE: '_like',
  ILIKE: '_ilike',
  'NOT LIKE': '_nlike',
  'NOT ILIKE': '_nlike',
  REGEX: '_regex',
  'NOT REGEX': '_nregex',
  REGEXP: '_regex',
  'NOT REGEXP': '_nregex',
  IN: '_in',
  'NOT IN': '_not_in',
  EXISTS: '_has_key',
  'NOT EXISTS': '_is_null',
  BETWEEN: '_between',
};

export const normalizeLegacyOperator = (op: string): string => LEGACY_TO_TOKEN[op] ?? op;

// Short chip-style label for any operator value the UI might hold (backend
// token OR legacy UI value). Returns the raw input when descriptors are
// unavailable or the token is unknown.
export const getOperatorDisplayLabel = (op: string, descriptors: OperatorDescriptor[] | undefined): string => {
  if (!op) return '';
  if (!descriptors || descriptors.length === 0) return op;
  const token = normalizeLegacyOperator(op);
  const entry = descriptors.find((d) => d.token === token);
  return entry?.chip_label ?? op;
};
