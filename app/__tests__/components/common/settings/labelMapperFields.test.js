import {
  EMPTY_TRACE_LABEL_SETTINGS,
  TRACE_LABEL_ADVANCED_FIELDS,
  TRACE_LABEL_FIELDS,
  traceLabelsToSettings,
  traceSettingsToLabelsValue,
} from '@shared/settings/labelMapperFields';

// Hard copy of canonicalTraceFields in api-server/services/observability/service.go.
// The form's keys ARE the JSON keys the Go reader unmarshals, so a typo here writes a
// mapping the backend accepts and then silently ignores — nothing else in the stack
// would catch it. Update this list only alongside that Go slice.
const CANONICAL_TRACE_FIELDS = [
  'service_name',
  'workload_name',
  'workload_namespace',
  'span_name',
  'trace_id',
  'duration_ns',
  'http_status_code',
  'status_code',
  'resource',
  'destination_workload_name',
  'destination_workload_namespace',
];

const allKeys = () => [...TRACE_LABEL_FIELDS, ...TRACE_LABEL_ADVANCED_FIELDS].map(({ field }) => field);

describe('trace label field descriptors', () => {
  it('covers exactly the backend canonical trace vocabulary', () => {
    expect(allKeys().sort()).toEqual([...CANONICAL_TRACE_FIELDS].sort());
  });

  it('splits the vocabulary without overlap or duplicates', () => {
    const keys = allKeys();
    expect(new Set(keys).size).toBe(keys.length);
    const advanced = new Set(TRACE_LABEL_ADVANCED_FIELDS.map(({ field }) => field));
    TRACE_LABEL_FIELDS.forEach(({ field }) => expect(advanced.has(field)).toBe(false));
  });

  it('gives every field a label and a placeholder', () => {
    [...TRACE_LABEL_FIELDS, ...TRACE_LABEL_ADVANCED_FIELDS].forEach(({ label, placeholder }) => {
      expect(label).toBeTruthy();
      expect(placeholder).toBeTruthy();
    });
  });

  it('EMPTY_TRACE_LABEL_SETTINGS is every canonical key mapped to an empty string', () => {
    expect(Object.keys(EMPTY_TRACE_LABEL_SETTINGS).sort()).toEqual([...CANONICAL_TRACE_FIELDS].sort());
    expect(Object.values(EMPTY_TRACE_LABEL_SETTINGS).every((v) => v === '')).toBe(true);
  });
});

describe('traceLabelsToSettings', () => {
  it.each([
    ['null (safeJSONParse miss)', null],
    ['undefined (no stored attribute)', undefined],
    ['an array', []],
    ['a string', 'not-json'],
  ])('returns the all-empty shape for %s', (_label, input) => {
    expect(traceLabelsToSettings(input)).toEqual(EMPTY_TRACE_LABEL_SETTINGS);
  });

  it('fills the stored fields and blanks the rest', () => {
    const settings = traceLabelsToSettings({ service_name: 'k8s.service' });
    expect(settings.service_name).toBe('k8s.service');
    expect(settings.workload_name).toBe('');
    expect(Object.keys(settings).sort()).toEqual([...CANONICAL_TRACE_FIELDS].sort());
  });

  // A non-string value makes the backend's map[string]string unmarshal reject the whole
  // document, killing every override — and would make the Input uncontrolled here.
  it('drops non-string values rather than rendering them', () => {
    expect(traceLabelsToSettings({ duration_ns: 5 }).duration_ns).toBe('');
  });

  it('drops the dead defaultQuery key', () => {
    expect(traceLabelsToSettings({ defaultQuery: 'level=error' })).not.toHaveProperty('defaultQuery');
  });

  // Hand-written SQL overrides are exactly what this screen replaces, and a
  // non-canonical key is a working mapping entry (getMergedTraceLabelMapping maps.Copy).
  it('carries over non-canonical keys instead of discarding them', () => {
    expect(traceLabelsToSettings({ my_custom_field: 'x' }).my_custom_field).toBe('x');
  });
});

describe('traceSettingsToLabelsValue', () => {
  const parsed = (settings) => JSON.parse(traceSettingsToLabelsValue(settings));

  it('always emits every canonical key, so clearing a field removes the override', () => {
    const out = parsed({ ...EMPTY_TRACE_LABEL_SETTINGS, service_name: 'k8s.service' });
    expect(Object.keys(out).sort()).toEqual([...CANONICAL_TRACE_FIELDS].sort());
    expect(out.service_name).toBe('k8s.service');
    expect(out.workload_name).toBe('');
  });

  it('emits valid JSON with all-empty values for an untouched form', () => {
    const out = parsed({});
    expect(Object.keys(out).sort()).toEqual([...CANONICAL_TRACE_FIELDS].sort());
    expect(Object.values(out).every((v) => v === '')).toBe(true);
  });

  it('trims surrounding whitespace, which would otherwise never match a backend field', () => {
    expect(parsed({ service_name: '  k8s.service  ' }).service_name).toBe('k8s.service');
  });

  it('never writes the dead defaultQuery key back, even when the stored blob had one', () => {
    expect(parsed({ ...EMPTY_TRACE_LABEL_SETTINGS, defaultQuery: 'level=error' })).not.toHaveProperty('defaultQuery');
  });

  it('preserves a non-canonical key so a SQL-era override survives a save', () => {
    expect(parsed({ ...EMPTY_TRACE_LABEL_SETTINGS, my_custom_field: 'x' }).my_custom_field).toBe('x');
  });

  it('drops a blank non-canonical key rather than persisting noise', () => {
    expect(parsed({ ...EMPTY_TRACE_LABEL_SETTINGS, my_custom_field: '   ' })).not.toHaveProperty('my_custom_field');
  });

  it('round-trips through the reader unchanged', () => {
    const stored = { service_name: 'k8s.service', trace_id: 'traceID', my_custom_field: 'x' };
    const roundTripped = traceLabelsToSettings(JSON.parse(traceSettingsToLabelsValue(traceLabelsToSettings(stored))));
    expect(roundTripped).toEqual({ ...EMPTY_TRACE_LABEL_SETTINGS, ...stored });
  });

  it('tolerates a missing settings object', () => {
    expect(() => traceSettingsToLabelsValue(undefined)).not.toThrow();
  });
});
