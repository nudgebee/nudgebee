import MarkDowns from '@shared/viewers/MarkDowns';
import Text from '@shared/format/Text';
import { isMarkdown } from 'src/utils/common';
import { ds } from '@utils/colors';

// Keys most likely to carry an object's human-readable identity, in priority
// order (e.g. a gh issue author {id, is_bot, login, name} reads as its name).
const SUMMARY_KEYS = ['name', 'login', 'title', 'displayName', 'message', 'id'];

export const summarizeValue = (value) => {
  if (value === null || value === undefined) {
    return undefined;
  }
  if (Array.isArray(value)) {
    return value
      .map((v) => summarizeValue(v))
      .filter((s) => s !== undefined && s !== '')
      .join(', ');
  }
  if (typeof value === 'object') {
    for (const key of SUMMARY_KEYS) {
      const v = value[key];
      if ((typeof v === 'string' && v) || typeof v === 'number') {
        return String(v);
      }
    }
    return JSON.stringify(value);
  }
  return String(value);
};

export const getTableDataFromArrayOfObject = (t) => {
  // Drop nullish/primitive rows — Object.keys/entries on a non-object throws.
  const dataArray = (Array.isArray(t) ? t : [t]).filter((item) => item && typeof item === 'object');
  if (dataArray.length === 0) {
    return { headers: [], tableData: [] };
  }
  // CustomTable defaults any header without a width to 20%, so 6+ columns
  // over-constrain the row and the last column collapses to a sliver.
  // Divide the row evenly instead so the widths always sum to 100%.
  const keys = Object.keys(dataArray[0]);
  const headers = keys.map((val) => ({ name: val, width: `${(100 / keys.length).toFixed(2)}%` }));
  const tableData = dataArray.map((item) =>
    Object.entries(item).map(([_key, value]) => ({
      component: (
        <>
          {typeof value === 'string' && isMarkdown(value) ? (
            <MarkDowns
              sx={{
                width: 'fit-content',
              }}
              data={value}
            />
          ) : (
            <Text value={summarizeValue(value)} showAutoEllipsis lineClamp={2} sx={{ minWidth: ds.space.mul(0, 40) }} />
          )}
        </>
      ),
    }))
  );
  return {
    headers,
    tableData,
  };
};

export const calculatePercentage = (recommendedReq, allocatedReq) => {
  const epsilon = 1e-10;
  if (!isNaN(recommendedReq) && !isNaN(allocatedReq) && allocatedReq > epsilon) {
    return Math.abs(((allocatedReq - recommendedReq) / allocatedReq) * 100).toFixed() + '%';
  }
  return '-';
};

export function flattenObject(obj, prefix = '', res = {}) {
  for (const [key, value] of Object.entries(obj || {})) {
    const newKey = prefix ? `${prefix}.${key}` : key;

    if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
      flattenObject(value, newKey, res);
    } else {
      res[newKey] = value;
    }
  }
  return res;
}
