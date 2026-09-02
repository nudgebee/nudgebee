export type OperatorKind = 'chip' | 'line';

// Normalized label data type, as returned on OutputLogLabel.data_type. Mirrors
// query.LabelType* in api-server/services/query/operator_catalog.go. 'unknown' is
// the fallback for a label the backend could not type, and is permissive.
export type LabelDataType = 'string' | 'number' | 'bool' | 'timestamp' | 'unknown';

// Shape of capabilities.supported_operator_descriptors as returned by
// observability_get_default_provider / observability_list_provider_capabilities. Backend is
// the source of truth for chip/line labels and chip-vs-line kind metadata; the
// UI stays thin around this.
export interface OperatorDescriptor {
  token: string;
  chip_label?: string;
  line_label?: string;
  kinds: OperatorKind[];
  // Label data types this operator accepts. Absent when talking to a backend that
  // predates the field, which getOperatorsForLabel treats as "no restriction".
  applicable_data_types?: LabelDataType[];
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

// operatorAppliesToType decides whether one operator is meaningful for a label's
// data type. Fails OPEN — an absent descriptor field, an empty list, a missing type
// and 'unknown' all return true — so this can only ever hide an operator we
// positively know would be rejected. Mirrors query.DataTypesApplyToType in Go.
export function operatorAppliesToType(descriptor: OperatorDescriptor, dataType?: string): boolean {
  if (!dataType || dataType === 'unknown') return true;
  if (!descriptor.applicable_data_types || descriptor.applicable_data_types.length === 0) return true;
  // dataType arrives as a raw API string, so compare rather than narrow it first.
  return descriptor.applicable_data_types.some((t) => t === dataType);
}

// getOperatorsForLabel narrows getOperatorsForKind to the operators valid for a
// specific label's data type — the two axes the backend exposes: which operators the
// PROVIDER supports (the descriptor list itself) intersected with which ones the
// label's TYPE accepts (applicable_data_types).
//
// This is what stops a regex or contains chip being built against a numeric column,
// which the provider would reject at query time. Passing no dataType returns the
// full list, so callers with no type information behave exactly as before.
export function getOperatorsForLabel(descriptors: OperatorDescriptor[] | undefined, kind: OperatorKind, dataType?: string): OperatorOption[] {
  if (!descriptors || descriptors.length === 0) return [];
  return getOperatorsForKind(
    descriptors.filter((d) => operatorAppliesToType(d, dataType)),
    kind
  );
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
