export const STRUCTURED_FILTER_FIELDS = [
  { filterType: 'event_type', eventField: 'event_type', label: 'Event Type' },
  { filterType: 'cluster', eventField: 'cluster', label: 'Cluster' },
  { filterType: 'namespace', eventField: 'subject_namespace', label: 'Namespace' },
  { filterType: 'source', eventField: 'source', label: 'Source' },
  { filterType: 'priority', eventField: 'priority', label: 'Priority' },
] as const;

export const buildFilterExpression = (currentValues: Record<string, string>, overrides?: Record<string, string>): string => {
  const conditions: string[] = [];
  for (const field of STRUCTURED_FILTER_FIELDS) {
    const val = overrides?.[field.filterType] ?? currentValues[field.filterType] ?? '';
    if (val) {
      const sanitized = val.replace(/"/g, "'");
      conditions.push(`event.${field.eventField} == "${sanitized}"`);
    }
  }
  if (conditions.length === 0) {
    return '';
  }
  return `{{ ${conditions.join(' and ')} }}`;
};

export const parseFilterExpression = (filter: string): Record<string, string> => {
  const result: Record<string, string> = {};
  if (!filter?.includes('{{')) {
    return result;
  }

  for (const field of STRUCTURED_FILTER_FIELDS) {
    const regex = new RegExp(`event\\.${field.eventField}\\s*==\\s*"([^"]*)"`, 'i');
    const match = regex.exec(filter);
    if (match) {
      result[field.filterType] = match[1];
    }
  }
  return result;
};
