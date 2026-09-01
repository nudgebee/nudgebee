/**
 * Field descriptors and (de)serialisers for the label mappers rendered by
 * TenantAccountCommonSettings — the tenant-wide Settings modal and the per-account
 * K8s settings modal.
 *
 * Trace state keys ARE the canonical trace field names (canonicalTraceFields in
 * api-server/services/observability/service.go). The stored attribute value is
 * {canonical_field: provider_field} — exactly the shape getCustomTraceLabels and
 * getTenantTraceLabels unmarshal — so keeping form state on the same keys means
 * there is no translation table that can drift from the backend vocabulary.
 */

export const LOG_LABEL_FIELDS = [
  { label: 'Pod', field: 'logPodLabel', placeholder: 'Log Pod label' },
  { label: 'Namespace', field: 'logNamespaceLabel', placeholder: 'Log Namespace label' },
  { label: 'App', field: 'logAppLabel', placeholder: 'Log App label' },
  { label: 'Default query', field: 'logDefaultQuery', placeholder: 'Default Query' },
];

// The five canonical fields an operator retunes on nearly every non-OTel trace backend.
export const TRACE_LABEL_FIELDS = [
  { label: 'Service name', field: 'service_name', placeholder: 'Provider field for service_name' },
  { label: 'Workload name', field: 'workload_name', placeholder: 'Provider field for workload_name' },
  { label: 'Span name', field: 'span_name', placeholder: 'Provider field for span_name' },
  { label: 'Duration (ns)', field: 'duration_ns', placeholder: 'Provider field for duration_ns' },
  { label: 'Status code', field: 'status_code', placeholder: 'Provider field for status_code' },
];

// The rest of canonicalTraceFields. Behind a disclosure rather than dropped: they are
// legal overrides, just rarely retuned. The mapper auto-expands when any of them
// already holds a value, so a configured override is never hidden.
export const TRACE_LABEL_ADVANCED_FIELDS = [
  { label: 'Trace ID', field: 'trace_id', placeholder: 'Provider field for trace_id' },
  { label: 'HTTP status code', field: 'http_status_code', placeholder: 'Provider field for http_status_code' },
  { label: 'Resource', field: 'resource', placeholder: 'Provider field for resource' },
  { label: 'Destination workload name', field: 'destination_workload_name', placeholder: 'Provider field for destination_workload_name' },
  {
    label: 'Destination workload namespace',
    field: 'destination_workload_namespace',
    placeholder: 'Provider field for destination_workload_namespace',
  },
];

const ALL_TRACE_LABEL_FIELDS = [...TRACE_LABEL_FIELDS, ...TRACE_LABEL_ADVANCED_FIELDS];

// Confirmed dead across Go, Python and the frontend: both label readers strip it and
// nothing consumes it (tracked as #37402). The trace mapper never renders an input for
// it and never writes it back, even when a stored blob carries one.
const DEAD_LABEL_KEY = 'defaultQuery';

export const EMPTY_TRACE_LABEL_SETTINGS = Object.freeze(Object.fromEntries(ALL_TRACE_LABEL_FIELDS.map(({ field }) => [field, ''])));

/**
 * Parsed trace_labels blob -> form state.
 *
 * Starts from the canonical shape so every rendered Input is a controlled '' by default,
 * then carries over anything else already stored. Non-canonical keys are legal —
 * getMergedTraceLabelMapping maps.Copy-s them over the provider's static mapping, so they
 * add a working mapping entry — and are typically the hand-written SQL overrides this
 * screen exists to replace. They must survive a round trip instead of being deleted the
 * first time someone opens the form and saves.
 *
 * @param {unknown} parsed output of safeJSONParse on the stored attribute value
 * @returns {Record<string, string>}
 */
export function traceLabelsToSettings(parsed) {
  const source = parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {};
  const settings = { ...EMPTY_TRACE_LABEL_SETTINGS };
  for (const [key, value] of Object.entries(source)) {
    if (key === DEAD_LABEL_KEY) continue;
    // Non-strings would make the Input uncontrolled, and would also make the backend's
    // map[string]string unmarshal reject the whole document — killing every override,
    // not just the offending key.
    if (typeof value === 'string') settings[key] = value;
  }
  return settings;
}

/**
 * Form state -> the trace_labels attribute value.
 *
 * @param {Record<string, string>} settings
 * @returns {string} JSON, ready for the attribute row
 */
export function traceSettingsToLabelsValue(settings) {
  const source = settings && typeof settings === 'object' ? settings : {};
  const out = {};
  // Canonical keys are always present, even when blank: an empty value is how "remove this
  // override" is expressed — the reader drops empty values and falls back to the provider's
  // static mapping. Omitting blank keys instead would make clearing a field a no-op.
  for (const { field } of ALL_TRACE_LABEL_FIELDS) {
    out[field] = String(source[field] ?? '').trim();
  }
  // Then any non-canonical key the stored blob arrived with, preserved verbatim.
  for (const [key, value] of Object.entries(source)) {
    if (key in out || key === DEAD_LABEL_KEY) continue;
    if (typeof value === 'string' && value.trim()) out[key] = value.trim();
  }
  return JSON.stringify(out);
}
