import { titleCase } from '@lib/formatter';
import Memory from '@shared/format/Memory';
import { formatClockCompact } from '@shared/format/Datetime';

// Turns an auto optimize task's meta into [property, current, proposed] rows,
// for the Tasks page and the run approval review.

// Backend stores CPU as raw cores (e.g. 0.5, 0.09); display as millicores to match the "500m" style used in task messages.
const formatCpuValue = (value) => {
  if (value === null || value === undefined || value === '-' || (typeof value === 'string' && value.trim() === '')) return '-';
  const num = Number(value);
  if (isNaN(num)) return value; // already unit-formatted (e.g. "500m"), leave as-is
  return `${Math.round(num * 1000)}m`;
};

const getCPUMemoryConfigs = (data) => {
  return data.map((item) => {
    const isCpu = typeof item.property_name === 'string' && item.property_name.toLowerCase().includes('cpu');
    const currentValue = item.current_value ?? '-';
    const newValue = item.new_value ?? '-';
    return [
      { text: titleCase(item.property_name) || '-' },
      { text: isCpu ? formatCpuValue(currentValue) : currentValue },
      { text: isCpu ? formatCpuValue(newValue) : newValue },
    ];
  });
};

export const parseRecommendationMeta = (meta) => {
  if (meta?.changes && meta?.changes?.length > 0) {
    return getCPUMemoryConfigs(meta.changes);
  }

  const recommendation = meta?.recommendation;
  const name = meta?.name;

  if (recommendation && name && recommendation[name]) {
    const configs = [];
    recommendation[name].forEach((rec) => {
      const resource = rec.resource;
      if (rec.allocated) {
        Object.keys(rec.allocated).forEach((key) => {
          if (rec.allocated[key] !== null || (rec.recommended && rec.recommended[key] !== null)) {
            const resourceTitle = titleCase(`${resource} ${key}`);
            const allocatedValue = rec.allocated[key] ?? '-';
            const recommendedValue = rec.recommended ? rec.recommended[key] ?? '-' : '-';
            const isMemory = resource === 'memory';
            const isCpu = resource === 'cpu';
            configs.push([
              { text: resourceTitle },
              isMemory && !isNaN(Number(allocatedValue))
                ? { component: <Memory value={Number(allocatedValue)} sourceUnit='bytes' targetUnit='mb' /> }
                : { text: isCpu ? formatCpuValue(allocatedValue) : allocatedValue },
              isMemory && !isNaN(Number(recommendedValue))
                ? { component: <Memory value={Number(recommendedValue)} sourceUnit='bytes' targetUnit='mb' /> }
                : { text: isCpu ? formatCpuValue(recommendedValue) : recommendedValue },
            ]);
          }
        });
      }
    });
    return configs;
  }

  // Replica and volume tasks carry the value they will apply, fixed when the run was planned.
  if (meta?.change_to !== undefined && meta?.change_to !== null) {
    // A forecast target is right for the hour it was forecast for, which can be
    // well before an approved plan applies.
    const forecastFor = formatClockCompact(meta.forecast_for);
    const target = forecastFor ? `${meta.change_to} (forecast for ${forecastFor})` : String(meta.change_to);
    return [[{ text: 'Replicas' }, { text: 'current' }, { text: target }]];
  }
  if (meta?.change_by) {
    return [[{ text: 'Storage size' }, { text: 'current' }, { text: `+${meta.change_by}` }]];
  }

  return [];
};
